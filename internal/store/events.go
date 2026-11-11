package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// EventPage is one page of the event log, in seq order, with the oldest and newest seq the log holds. Earliest and
// Newest are 0 when the log has no events.
type EventPage struct {
	Events   []Event
	Earliest int64
	Newest   int64
}

// Event is one events row, decoded by its kind. Server and Pane are set for session_added. Changes is set for
// session_changed. session_removed carries neither. An engine kind (IsEngineKind) sets none of them: its payload is
// not decoded.
type Event struct {
	Seq       int64
	SessionID string
	Kind      string
	At        int64
	Server    string
	Pane      string
	Changes   FieldChanges
}

// FieldChanges holds the mirrored fields a session_changed event changed. A field the event does not change is nil,
// and a field changed to the empty string points at "".
type FieldChanges struct {
	State         *string
	Changed       *string
	Seen          *string
	Question      *string
	Summary       *string
	Label         *string
	Context       *string
	Tool          *string
	ClaudeSession *string
	Transcript    *string
}

// set records value as the change to the field named name. It reports false when name is not a mirrored field.
func (c *FieldChanges) set(name, value string) bool {
	switch name {
	case "state":
		c.State = &value
	case "changed":
		c.Changed = &value
	case "seen":
		c.Seen = &value
	case "question":
		c.Question = &value
	case "summary":
		c.Summary = &value
	case "label":
		c.Label = &value
	case "context":
		c.Context = &value
	case "tool":
		c.Tool = &value
	case "claude_session":
		c.ClaudeSession = &value
	case "transcript":
		c.Transcript = &value
	default:
		return false
	}
	return true
}

// Events reads at most limit events with seq greater than after, in seq order, with the oldest and newest seq the log
// holds. It does all of that in one read transaction on a connection it opens and closes. It returns an error that
// matches ErrUnavailable when the file is missing or the database is busy, and an error that does not match it when a
// row does not decode. It returns an error when limit is below 1, because SQLite reads a negative LIMIT as no limit.
// It never creates the file and never writes the database.
func (r *Reader) Events(ctx context.Context, after int64, limit int) (EventPage, error) {
	if limit < 1 {
		return EventPage{}, fmt.Errorf("event page limit %d: must be at least 1", limit)
	}
	db, err := sql.Open("sqlite", readOnlyDSN(r.path))
	if err != nil {
		return EventPage{}, fmt.Errorf("open store reader: %w", err)
	}
	page, err := readEventPage(ctx, db, after, limit)
	if closeErr := db.Close(); closeErr != nil {
		err = errors.Join(err, fmt.Errorf("close store reader: %w", closeErr))
	}
	if err != nil {
		if unavailable(err) {
			return EventPage{}, fmt.Errorf("%w: %w", ErrUnavailable, err)
		}
		return EventPage{}, err
	}
	return page, nil
}

// readEventPage runs the page read and both bounds in one read transaction, so the page and its bounds describe the
// same state.
func readEventPage(ctx context.Context, db *sql.DB, after int64, limit int) (EventPage, error) {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return EventPage{}, fmt.Errorf("begin store read: %w", err)
	}
	page, err := readEventPageInTx(ctx, tx, after, limit)
	if err != nil {
		return EventPage{}, errors.Join(err, tx.Rollback())
	}
	if err = tx.Commit(); err != nil {
		return EventPage{}, fmt.Errorf("end store read: %w", err)
	}
	return page, nil
}

func readEventPageInTx(ctx context.Context, tx *sql.Tx, after int64, limit int) (EventPage, error) {
	queries := sqlcgen.New(tx)
	rows, err := queries.EventPageAfter(ctx, sqlcgen.EventPageAfterParams{Seq: after, Limit: int64(limit)})
	if err != nil {
		return EventPage{}, fmt.Errorf("read event page after seq %d: %w", after, err)
	}
	oldest, err := queries.OldestEventSeq(ctx)
	if err != nil {
		return EventPage{}, fmt.Errorf("read oldest event seq: %w", err)
	}
	newest, err := queries.NewestEventSeq(ctx)
	if err != nil {
		return EventPage{}, fmt.Errorf("read newest event seq: %w", err)
	}
	events := make([]Event, 0, len(rows))
	for _, row := range rows {
		decoded, decodeErr := decodeEvent(row)
		if decodeErr != nil {
			return EventPage{}, decodeErr
		}
		events = append(events, decoded)
	}
	return EventPage{Events: events, Earliest: oldest, Newest: newest}, nil
}

// decodeEvent decodes one events row by its kind. Each payload must fit its kind exactly: no unknown fields, no
// unknown field names, and no data after the JSON value. An engine kind is returned with its common fields only, and
// its payload is not read.
func decodeEvent(row sqlcgen.Event) (Event, error) {
	event := Event{Seq: row.Seq, SessionID: row.SessionID, Kind: row.Kind, At: row.At}
	switch row.Kind {
	case EventSessionAdded:
		var payload addedPayload
		if err := decodePayload(row.Payload, &payload); err != nil {
			return Event{}, fmt.Errorf("decode %s event %d: %w", row.Kind, row.Seq, err)
		}
		event.Server, event.Pane = payload.Server, payload.Pane
	case EventSessionChanged:
		var payload changedPayload
		if err := decodePayload(row.Payload, &payload); err != nil {
			return Event{}, fmt.Errorf("decode %s event %d: %w", row.Kind, row.Seq, err)
		}
		for name, value := range payload.Fields {
			if !event.Changes.set(name, value) {
				return Event{}, fmt.Errorf("decode %s event %d: unknown field %q", row.Kind, row.Seq, name)
			}
		}
	case EventSessionRemoved:
		if err := decodePayload(row.Payload, &struct{}{}); err != nil {
			return Event{}, fmt.Errorf("decode %s event %d: %w", row.Kind, row.Seq, err)
		}
	default:
		if IsEngineKind(row.Kind) {
			return event, nil
		}
		return Event{}, fmt.Errorf("event %d has unknown kind %q", row.Seq, row.Kind)
	}
	return event, nil
}

// decodePayload decodes one payload into target. It rejects unknown fields and any data after the JSON value.
func decodePayload(payload string, target any) error {
	decoder := json.NewDecoder(strings.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode payload: %w", err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errors.New("payload has data after its JSON value")
	}
	return nil
}
