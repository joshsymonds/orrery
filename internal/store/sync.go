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

// EventRetention is how long SyncServer keeps events; older events are pruned, except the newest, so the largest
// seq the log has stored is never lost.
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
// prunes events older than EventRetention, except the newest event, and removes each engine session that has
// no event at or after the same cutoff, with its unfinished text. A live engine session, as SetEngineLive
// reports, is kept with its text. The removal appends no event.
func (s *Store) SyncServer(ctx context.Context, server string, panes []Pane, now time.Time) error {
	live := s.engineLiveIDs()
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
		return pruneRetention(ctx, queries, now, live)
	})
}

// pruneRetention prunes events older than EventRetention, then removes each engine session with no event at or
// after the same cutoff and no id in live, with its unfinished text and failure markers.
func pruneRetention(ctx context.Context, queries *sqlcgen.Queries, now time.Time, live []string) error {
	cutoff := now.Add(-EventRetention).Unix()
	if err := queries.PruneEvents(ctx, cutoff); err != nil {
		return fmt.Errorf("prune events: %w", err)
	}
	liveJSON, err := json.Marshal(live)
	if err != nil {
		return fmt.Errorf("encode live engine sessions: %w", err)
	}
	// Engine text and failure markers go first: their deletes select engine sessions that the session delete removes.
	if err = queries.DeleteStaleEngineText(ctx, sqlcgen.DeleteStaleEngineTextParams{
		Cutoff: cutoff, Live: string(liveJSON),
	}); err != nil {
		return fmt.Errorf("delete stale engine text: %w", err)
	}
	if err = queries.DeleteStaleEnginePromptFailures(ctx, sqlcgen.DeleteStaleEnginePromptFailuresParams{
		Cutoff: cutoff, Live: string(liveJSON),
	}); err != nil {
		return fmt.Errorf("delete stale engine prompt failures: %w", err)
	}
	if err = queries.DeleteStaleEngineSessions(ctx, sqlcgen.DeleteStaleEngineSessionsParams{
		Cutoff: cutoff, Live: string(liveJSON),
	}); err != nil {
		return fmt.Errorf("delete stale engine sessions: %w", err)
	}
	return nil
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

// write runs fn in one write transaction. BEGIN IMMEDIATE takes the write lock up front, so the busy timeout (see
// BusyTimeout) bounds the wait for it, and a write that waits it out fails with an error Busy reports. A failed fn or commit rolls back, so a failed write leaves no rows behind.
//
// The write is one timed store call (see WithLogger), from the wait for the connection until it is free again.
//
// A write that commits after one of its statements ran with a context from withEpicEvent calls the hook OnCallerEvent
// set, once, after the connection is free again, so the hook may read the store. A write that moved the revision of
// any epic (see runWrite) then calls the hook OnEpicsChanged set, once, with those epics. A write that commits and
// names a session (see OnSessionEvents) then calls the hook OnSessionEvents set, once, after that. A write that commits
// calls each housekeeping hook its statements were marked for (see withHousekeepMark), once per id, the same way.
func (s *Store) write(ctx context.Context, fn func(queries *sqlcgen.Queries) error) error {
	return s.writeDB(ctx, func(db sqlcgen.DBTX) error { return fn(sqlcgen.New(db)) })
}

// writeDB is write with fn given the write's statements as they run, rather than their queries.
func (s *Store) writeDB(ctx context.Context, fn func(db sqlcgen.DBTX) error) error {
	ctx, timer := s.db.slow.begin(ctx, endByCaller, "")
	conn, err := s.db.Conn(ctx)
	if err != nil {
		timer.finish(ctx)
		return fmt.Errorf("open store connection: %w", err)
	}
	done, runErr := runWrite(ctx, conn, fn)
	err = errors.Join(runErr, conn.Close())
	timer.finish(ctx)
	if runErr == nil && done.epicEvent {
		s.fireCallerEvent()
	}
	if runErr == nil {
		s.fireEpicsChanged(done.moved)
		s.fireSessionEvents(done.session)
		s.fireHousekeep(done.housekeep)
	}
	return err
}

// writeDone is what a committed write did: whether a statement ran with a context from withEpicEvent and succeeded,
// the epics whose revision it moved, sorted, what it did to the session feed (see sessionChange), and the housekeeping
// marks of its statements that succeeded, each once.
type writeDone struct {
	epicEvent bool
	moved     []string
	session   sessionChange
	housekeep []housekeepMark
}

// runWrite runs fn in the write transaction on conn and commits it. While fn runs, the connection's pre-update hook
// records each epic a written row of an epic table names (epicMoves) and each row of the session feed's tables (see
// recordSession); before the commit, the write moves the revision of each of those epics by one (epicMoves.flush), in
// the same transaction, and reads the sessions those rows name, and the steerer of each epic it moved
// (epicMoves.flushSessions). It reports what the write did, which is set only when runWrite returns nil.
func runWrite(
	ctx context.Context, conn *sql.Conn, fn func(db sqlcgen.DBTX) error,
) (done writeDone, err error) {
	moves, err := movesOf(conn)
	if err != nil {
		return writeDone{}, err
	}
	if _, err = conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return writeDone{}, fmt.Errorf("begin store write: %w", err)
	}
	moves.arm()
	statements := &writeConn{conn: conn}
	defer func() { err = errors.Join(err, statements.close()) }()
	if err = fn(statements); err != nil {
		moves.disarm()
		return writeDone{}, errors.Join(err, rollback(ctx, conn))
	}
	moved, err := moves.flush(ctx, statements)
	if err != nil {
		return writeDone{}, errors.Join(err, rollback(ctx, conn))
	}
	session, err := moves.flushSessions(ctx, statements, moved)
	if err != nil {
		return writeDone{}, errors.Join(err, rollback(ctx, conn))
	}
	if _, err = conn.ExecContext(ctx, "COMMIT"); err != nil {
		return writeDone{}, errors.Join(fmt.Errorf("commit store write: %w", err), rollback(ctx, conn))
	}
	return writeDone{
		epicEvent: statements.epicEvent, moved: moved, session: session, housekeep: statements.housekeep,
	}, nil
}

// epicEventKey is the context key withEpicEvent sets.
type epicEventKey struct{}

// withEpicEvent returns ctx marked for a statement that creates an epic caller event: an epic question, an epic phase,
// or a stopped release. The write's writeConn records the mark when such a statement runs and succeeds, so the write
// calls the hook once it commits. A helper passes the marked context to its own statement only.
func withEpicEvent(ctx context.Context) context.Context {
	return context.WithValue(ctx, epicEventKey{}, true)
}

// housekeepHook names a housekeeping hook: the one of OnEpicStored, OnEpicEnded, OnFirstPrompt or OnTurnEnded a write
// calls for a mark's id.
type housekeepHook int

const (
	hookEpicStored housekeepHook = iota + 1
	hookEpicEnded
	hookFirstPrompt
	hookTurnEnded
)

// housekeepMark is a housekeeping hook and the id it is called with, once the write that marked it commits.
type housekeepMark struct {
	hook housekeepHook
	id   string
}

// housekeepMarkKey is the context key withHousekeepMark sets.
type housekeepMarkKey struct{}

// withHousekeepMark returns ctx marked for a statement whose write, once it commits, calls hook with id. The write's
// writeConn records the mark when the statement runs and succeeds. A write that marks the same id for the same hook
// more than once calls that hook once for it. A helper passes the marked context to its own statement only.
func withHousekeepMark(ctx context.Context, hook housekeepHook, id string) context.Context {
	return context.WithValue(ctx, housekeepMarkKey{}, housekeepMark{hook: hook, id: id})
}

// writeConn runs the statements of one store write on its connection, preparing each query once. A write's ops run the
// same few queries once per op, so a batch of many ops would otherwise have sqlite parse each query again for every op.
// The statements are closed when the write ends.
type writeConn struct {
	conn       *sql.Conn
	statements map[string]writeStatement
	// epicEvent is set once a statement run with a context from withEpicEvent succeeds in this write.
	epicEvent bool
	// housekeep is the marks of the statements run with a context from withHousekeepMark that succeeded in this write,
	// each once, in the order they first ran. housekeepSeen holds the same marks.
	housekeep     []housekeepMark
	housekeepSeen map[housekeepMark]bool
}

// writeStatement is a statement a writeConn prepared. The writeConn owns it, and its close closes the statement.
type writeStatement struct {
	stmt *sql.Stmt
}

// prepared returns the statement of query, prepared on the write's connection the first time the write runs query.
func (w *writeConn) prepared(ctx context.Context, query string) (writeStatement, error) {
	if st, ok := w.statements[query]; ok {
		return st, nil
	}
	stmt, err := w.conn.PrepareContext(ctx, query)
	if err != nil {
		return writeStatement{}, fmt.Errorf("%w", err)
	}
	if w.statements == nil {
		w.statements = map[string]writeStatement{}
	}
	w.statements[query] = writeStatement{stmt: stmt}
	return w.statements[query], nil
}

// ExecContext runs query with args as its prepared statement.
func (w *writeConn) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	st, err := w.prepared(ctx, query)
	if err != nil {
		return nil, err
	}
	result, err := st.stmt.ExecContext(ctx, args...)
	if err != nil {
		return nil, fmt.Errorf("%w", err)
	}
	if _, marked := ctx.Value(epicEventKey{}).(bool); marked {
		w.epicEvent = true
	}
	if mark, marked := ctx.Value(housekeepMarkKey{}).(housekeepMark); marked {
		w.recordHousekeep(mark)
	}
	return result, nil
}

// recordHousekeep adds mark to the write's housekeeping marks, unless the write already has it.
func (w *writeConn) recordHousekeep(mark housekeepMark) {
	if w.housekeepSeen[mark] {
		return
	}
	if w.housekeepSeen == nil {
		w.housekeepSeen = map[housekeepMark]bool{}
	}
	w.housekeepSeen[mark] = true
	w.housekeep = append(w.housekeep, mark)
}

// QueryContext runs query with args as its prepared statement.
func (w *writeConn) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	st, err := w.prepared(ctx, query)
	if err != nil {
		return nil, err
	}
	rows, err := st.stmt.QueryContext(ctx, args...)
	if err != nil {
		return nil, fmt.Errorf("%w", err)
	}
	return rows, nil
}

// QueryRowContext runs query with args as its prepared statement. A query that cannot be prepared runs on the connection
// unprepared, so its row carries the error.
func (w *writeConn) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	st, err := w.prepared(ctx, query)
	if err != nil {
		return w.conn.QueryRowContext(ctx, query, args...)
	}
	return st.stmt.QueryRowContext(ctx, args...)
}

// PrepareContext prepares query on the write's connection, apart from the statements the write keeps.
func (w *writeConn) PrepareContext(ctx context.Context, query string) (*sql.Stmt, error) {
	stmt, err := w.conn.PrepareContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("%w", err)
	}
	return stmt, nil
}

// close closes the statements the write prepared.
func (w *writeConn) close() error {
	var errs []error
	for _, st := range w.statements {
		if err := st.stmt.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close store statement: %w", err))
		}
	}
	return errors.Join(errs...)
}

// rollback runs even when ctx is done, so a canceled write does not leave its transaction open on the
// connection.
func rollback(ctx context.Context, conn *sql.Conn) error {
	if _, err := conn.ExecContext(context.WithoutCancel(ctx), "ROLLBACK"); err != nil {
		return fmt.Errorf("roll back store write: %w", err)
	}
	return nil
}
