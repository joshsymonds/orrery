package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// Event kinds appended to the events table. Each payload is fixed per kind.
const (
	EventSessionAdded   = "session_added"
	EventSessionChanged = "session_changed"
	EventSessionRemoved = "session_removed"
)

// EventRetention is how long SyncServer keeps events; older events are pruned.
const EventRetention = 14 * 24 * time.Hour

// removedPayload is the fixed payload of a session_removed event.
const removedPayload = "{}"

// Fields holds the mirrored pane options. The empty string means the option is unset.
type Fields struct {
	State         string
	Changed       string
	Seen          string
	Question      string
	Summary       string
	Label         string
	Context       string
	Tool          string
	ClaudeSession string
	Transcript    string
}

// Pane is one tmux pane's snapshot. ID is the row id (the pane's @orrery_id) and Pane is the tmux pane id.
type Pane struct {
	ID     string
	Pane   string
	Fields Fields
}

type addedPayload struct {
	Server string `json:"server"`
	Pane   string `json:"pane"`
}

type changedPayload struct {
	Fields map[string]string `json:"fields"`
}

// named returns each field under its store name.
func (f Fields) named() map[string]string {
	return map[string]string{
		"state":          f.State,
		"changed":        f.Changed,
		"seen":           f.Seen,
		"question":       f.Question,
		"summary":        f.Summary,
		"label":          f.Label,
		"context":        f.Context,
		"tool":           f.Tool,
		"claude_session": f.ClaudeSession,
		"transcript":     f.Transcript,
	}
}

func fieldsOf(row sqlcgen.Session) Fields {
	return Fields{
		State:         row.State,
		Changed:       row.Changed,
		Seen:          row.Seen,
		Question:      row.Question,
		Summary:       row.Summary,
		Label:         row.Label,
		Context:       row.Context,
		Tool:          row.Tool,
		ClaudeSession: row.ClaudeSession,
		Transcript:    row.Transcript,
	}
}

// changedFields returns the fields of next that differ from current, under their store names.
func changedFields(current, next Fields) map[string]string {
	before := current.named()
	changed := map[string]string{}
	for name, value := range next.named() {
		if before[name] != value {
			changed[name] = value
		}
	}
	return changed
}

// SyncPane writes one pane's snapshot to its row and appends one event per change.
func (s *Store) SyncPane(ctx context.Context, server string, pane Pane, now time.Time) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		return syncPane(ctx, queries, server, pane, now)
	})
}

// SyncServer syncs every pane of one tmux server, removes the server's rows whose id is absent from panes,
// and prunes events older than EventRetention.
func (s *Store) SyncServer(ctx context.Context, server string, panes []Pane, now time.Time) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		present := make(map[string]bool, len(panes))
		for _, pane := range panes {
			if err := syncPane(ctx, queries, server, pane, now); err != nil {
				return err
			}
			present[pane.ID] = true
		}
		rows, err := queries.ListServerSessions(ctx, server)
		if err != nil {
			return fmt.Errorf("list sessions of server %q: %w", server, err)
		}
		for _, row := range rows {
			if present[row.ID] {
				continue
			}
			if err = queries.DeleteSession(ctx, row.ID); err != nil {
				return fmt.Errorf("delete session %s: %w", row.ID, err)
			}
			appendErr := appendEvent(ctx, queries, row.ID, EventSessionRemoved, removedPayload, now)
			if appendErr != nil {
				return appendErr
			}
		}
		if err = queries.PruneEvents(ctx, now.Add(-EventRetention).Unix()); err != nil {
			return fmt.Errorf("prune events: %w", err)
		}
		return nil
	})
}

// syncPane upserts one pane's row and appends its events. A new row appends session_added, then one
// session_changed when it has any non-empty field. An existing row whose fields differ appends one
// session_changed with only the differing fields.
func syncPane(ctx context.Context, queries *sqlcgen.Queries, server string, pane Pane, now time.Time) error {
	current := Fields{}
	exists := true
	row, err := queries.GetSession(ctx, pane.ID)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		exists = false
	case err != nil:
		return fmt.Errorf("read session %s: %w", pane.ID, err)
	default:
		current = fieldsOf(row)
	}

	if !exists {
		if err = queries.InsertSession(ctx, sqlcgen.InsertSessionParams{
			ID: pane.ID, Server: server, Pane: pane.Pane,
		}); err != nil {
			return fmt.Errorf("insert session %s: %w", pane.ID, err)
		}
		payload, encErr := json.Marshal(addedPayload{Server: server, Pane: pane.Pane})
		if encErr != nil {
			return fmt.Errorf("encode %s payload: %w", EventSessionAdded, encErr)
		}
		appendErr := appendEvent(ctx, queries, pane.ID, EventSessionAdded, string(payload), now)
		if appendErr != nil {
			return appendErr
		}
	}

	changed := changedFields(current, pane.Fields)
	if len(changed) == 0 {
		return nil
	}
	if err = queries.UpdateSessionFields(ctx, sqlcgen.UpdateSessionFieldsParams{
		State:         pane.Fields.State,
		Changed:       pane.Fields.Changed,
		Seen:          pane.Fields.Seen,
		Question:      pane.Fields.Question,
		Summary:       pane.Fields.Summary,
		Label:         pane.Fields.Label,
		Context:       pane.Fields.Context,
		Tool:          pane.Fields.Tool,
		ClaudeSession: pane.Fields.ClaudeSession,
		Transcript:    pane.Fields.Transcript,
		ID:            pane.ID,
	}); err != nil {
		return fmt.Errorf("update session %s: %w", pane.ID, err)
	}
	payload, err := json.Marshal(changedPayload{Fields: changed})
	if err != nil {
		return fmt.Errorf("encode %s payload: %w", EventSessionChanged, err)
	}
	return appendEvent(ctx, queries, pane.ID, EventSessionChanged, string(payload), now)
}

func appendEvent(ctx context.Context, queries *sqlcgen.Queries, sessionID, kind, payload string, now time.Time) error {
	if err := queries.InsertEvent(ctx, sqlcgen.InsertEventParams{
		SessionID: sessionID, Kind: kind, At: now.Unix(), Payload: payload,
	}); err != nil {
		return fmt.Errorf("append %s for session %s: %w", kind, sessionID, err)
	}
	return nil
}

// write runs fn in one write transaction. BEGIN IMMEDIATE takes the write lock up front, so the 200 ms busy
// timeout bounds the wait for it. A failed fn or commit rolls back, so a failed write leaves no rows behind.
func (s *Store) write(ctx context.Context, fn func(queries *sqlcgen.Queries) error) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("open store connection: %w", err)
	}
	return errors.Join(runWrite(ctx, conn, fn), conn.Close())
}

func runWrite(ctx context.Context, conn *sql.Conn, fn func(queries *sqlcgen.Queries) error) error {
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("begin store write: %w", err)
	}
	if err := fn(sqlcgen.New(conn)); err != nil {
		return errors.Join(err, rollback(ctx, conn))
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return errors.Join(fmt.Errorf("commit store write: %w", err), rollback(ctx, conn))
	}
	return nil
}

// rollback runs even when ctx is done, so a canceled write does not leave its transaction open on the
// connection.
func rollback(ctx context.Context, conn *sql.Conn) error {
	if _, err := conn.ExecContext(context.WithoutCancel(ctx), "ROLLBACK"); err != nil {
		return fmt.Errorf("roll back store write: %w", err)
	}
	return nil
}
