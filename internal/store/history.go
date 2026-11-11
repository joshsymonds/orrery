package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// ErrNoEngineSession reports that no headless engine session has the id asked for. A pane session's id matches it
// too, because pane rows are not engine sessions.
var ErrNoEngineSession = errors.New("no engine session")

// EngineEvent is one events row of a headless engine session. Payload is the stored JSON object, not decoded.
type EngineEvent struct {
	Seq     int64
	Kind    string
	At      int64
	Payload string
}

// EngineHistory is a headless engine session's current state and the events after a cursor, in seq order.
type EngineHistory struct {
	State  string
	Events []EngineEvent
}

// EngineHistory reads the engine session id: its current state, and at most limit events with seq greater than
// after, in seq order with their raw payloads. It does both in one read transaction on a connection it opens and
// closes. It returns an error that matches ErrNoEngineSession when no engine session has the id, an error that
// matches ErrUnavailable when the file is missing or the database is busy, and an error when limit is below 1,
// because SQLite reads a negative LIMIT as no limit. It never creates the file and never writes the database.
func (r *Reader) EngineHistory(ctx context.Context, id string, after int64, limit int) (EngineHistory, error) {
	if limit < 1 {
		return EngineHistory{}, fmt.Errorf("engine history limit %d: must be at least 1", limit)
	}
	db, err := sql.Open("sqlite", readOnlyDSN(r.path))
	if err != nil {
		return EngineHistory{}, fmt.Errorf("open store reader: %w", err)
	}
	history, err := readEngineHistory(ctx, db, id, after, limit)
	if closeErr := db.Close(); closeErr != nil {
		err = errors.Join(err, fmt.Errorf("close store reader: %w", closeErr))
	}
	if err != nil {
		if unavailable(err) {
			return EngineHistory{}, fmt.Errorf("%w: %w", ErrUnavailable, err)
		}
		return EngineHistory{}, err
	}
	return history, nil
}

// readEngineHistory runs the state read and the event read in one read transaction, so the state and the events
// describe the same moment.
func readEngineHistory(ctx context.Context, db *sql.DB, id string, after int64, limit int) (EngineHistory, error) {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return EngineHistory{}, fmt.Errorf("begin store read: %w", err)
	}
	history, err := readEngineHistoryInTx(ctx, tx, id, after, limit)
	if err != nil {
		return EngineHistory{}, errors.Join(err, tx.Rollback())
	}
	if err = tx.Commit(); err != nil {
		return EngineHistory{}, fmt.Errorf("end store read: %w", err)
	}
	return history, nil
}

func readEngineHistoryInTx(ctx context.Context, tx *sql.Tx, id string, after int64, limit int) (EngineHistory, error) {
	queries := sqlcgen.New(tx)
	session, err := queries.GetEngineSession(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return EngineHistory{}, fmt.Errorf("%w: %s", ErrNoEngineSession, id)
	}
	if err != nil {
		return EngineHistory{}, fmt.Errorf("read engine session %s: %w", id, err)
	}
	rows, err := queries.SessionEventsAfter(ctx, sqlcgen.SessionEventsAfterParams{
		SessionID: id, Seq: after, Limit: int64(limit),
	})
	if err != nil {
		return EngineHistory{}, fmt.Errorf("read events of engine session %s after seq %d: %w", id, after, err)
	}
	events := make([]EngineEvent, 0, len(rows))
	for _, row := range rows {
		events = append(events, EngineEvent{Seq: row.Seq, Kind: row.Kind, At: row.At, Payload: row.Payload})
	}
	return EngineHistory{State: session.State, Events: events}, nil
}
