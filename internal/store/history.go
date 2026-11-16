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

// EngineHistory is a headless engine session's row, the events after a cursor in seq order, and the session's
// unfinished text in ListEngineText order. Kind is KindEngine or KindStep. Unfinished is empty when the session has no
// unfinished block.
type EngineHistory struct {
	Kind        string
	Dir         string
	Title       string
	FirstPrompt string
	State       string
	Changed     string
	Seen        string
	Question    string
	Reason      string
	Events      []EngineEvent
	Unfinished  []UnfinishedText
}

// EngineEventPage is the events of a headless engine session before a seq, newest first, and the session's kind.
// Kind is KindEngine or KindStep.
type EngineEventPage struct {
	Kind   string
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
	var history EngineHistory
	err := r.readEngine(ctx, func(queries *sqlcgen.Queries) error {
		var err error
		history, err = readEngineHistory(ctx, queries, id, after, limit)
		return err
	})
	if err != nil {
		return EngineHistory{}, err
	}
	return history, nil
}

// EngineEventsBefore reads the events of the engine or step session id with a seq below before: at most limit of
// them, newest first, with their raw payloads, and the session's kind. It does both in one read transaction on a
// connection it opens and closes. It returns the errors EngineHistory returns: one that matches ErrNoEngineSession
// when no engine session has the id, one that matches ErrUnavailable when the file is missing or the database is
// busy, and an error when limit is below 1.
func (r *Reader) EngineEventsBefore(ctx context.Context, id string, before int64, limit int) (EngineEventPage, error) {
	if limit < 1 {
		return EngineEventPage{}, fmt.Errorf("engine events limit %d: must be at least 1", limit)
	}
	var page EngineEventPage
	err := r.readEngine(ctx, func(queries *sqlcgen.Queries) error {
		var err error
		page, err = readEngineEventsBefore(ctx, queries, id, before, limit)
		return err
	})
	if err != nil {
		return EngineEventPage{}, err
	}
	return page, nil
}

// readEngine opens a read-only connection to the store, runs read in one read transaction on it, and closes the
// connection. It never creates the file and never writes the database. An error that read returns matches
// ErrUnavailable when the file is missing or the database is busy.
func (r *Reader) readEngine(ctx context.Context, read func(queries *sqlcgen.Queries) error) error {
	db, err := sql.Open("sqlite", readOnlyDSN(r.path))
	if err != nil {
		return fmt.Errorf("open store reader: %w", err)
	}
	err = readEngineTx(ctx, db, read)
	if closeErr := db.Close(); closeErr != nil {
		err = errors.Join(err, fmt.Errorf("close store reader: %w", closeErr))
	}
	if err != nil && unavailable(err) {
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	return err
}

// readEngineTx runs read in one read transaction on db, so the reads it makes describe the same moment.
func readEngineTx(ctx context.Context, db *sql.DB, read func(queries *sqlcgen.Queries) error) error {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return fmt.Errorf("begin store read: %w", err)
	}
	if err = read(sqlcgen.New(tx)); err != nil {
		return errors.Join(err, tx.Rollback())
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("end store read: %w", err)
	}
	return nil
}

// engineSession returns the row of the engine or step session id, or an error that matches ErrNoEngineSession when
// no such session exists.
func engineSession(ctx context.Context, queries *sqlcgen.Queries, id string) (sqlcgen.Session, error) {
	session, err := queries.GetEngineSession(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return sqlcgen.Session{}, fmt.Errorf("%w: %s", ErrNoEngineSession, id)
	}
	if err != nil {
		return sqlcgen.Session{}, fmt.Errorf("read engine session %s: %w", id, err)
	}
	return session, nil
}

// readEngineHistory reads the row, the events after after, and the unfinished text of the engine session id.
func readEngineHistory(
	ctx context.Context,
	queries *sqlcgen.Queries,
	id string,
	after int64,
	limit int,
) (EngineHistory, error) {
	session, err := engineSession(ctx, queries, id)
	if err != nil {
		return EngineHistory{}, err
	}
	firstPrompt, err := queries.GetEngineFirstPrompt(ctx, id)
	if err != nil {
		return EngineHistory{}, fmt.Errorf("read first prompt of engine session %s: %w", id, err)
	}
	rows, err := queries.SessionEventsAfter(ctx, sqlcgen.SessionEventsAfterParams{
		SessionID: id, Seq: after, Limit: int64(limit),
	})
	if err != nil {
		return EngineHistory{}, fmt.Errorf("read events of engine session %s after seq %d: %w", id, after, err)
	}
	texts, err := queries.ListEngineText(ctx, id)
	if err != nil {
		return EngineHistory{}, fmt.Errorf("read unfinished text of engine session %s: %w", id, err)
	}
	unfinished := make([]UnfinishedText, 0, len(texts))
	for _, text := range texts {
		unfinished = append(unfinished, UnfinishedText{
			SessionID: text.SessionID, MessageID: text.MessageID, BlockIndex: text.BlockIndex,
			ParentToolUseID: text.ParentToolUseID, Kind: text.Kind, Text: text.Text,
		})
	}
	return EngineHistory{
		Kind: session.Kind, Dir: session.Dir, Title: session.Title, FirstPrompt: firstPrompt,
		State: session.State, Changed: session.Changed,
		Seen: session.Seen, Question: session.Question, Reason: session.Reason,
		Events: engineEvents(rows), Unfinished: unfinished,
	}, nil
}

// readEngineEventsBefore reads the kind and the events with a seq below before of the engine session id, newest
// first, at most limit of them.
func readEngineEventsBefore(
	ctx context.Context,
	queries *sqlcgen.Queries,
	id string,
	before int64,
	limit int,
) (EngineEventPage, error) {
	session, err := engineSession(ctx, queries, id)
	if err != nil {
		return EngineEventPage{}, err
	}
	rows, err := queries.SessionEventsBefore(ctx, sqlcgen.SessionEventsBeforeParams{
		SessionID: id, Seq: before, Limit: int64(limit),
	})
	if err != nil {
		return EngineEventPage{}, fmt.Errorf("read events of engine session %s before seq %d: %w", id, before, err)
	}
	return EngineEventPage{Kind: session.Kind, Events: engineEvents(rows)}, nil
}

// engineEvents converts the event rows of an engine session to EngineEvents, in the order of rows.
func engineEvents(rows []sqlcgen.Event) []EngineEvent {
	events := make([]EngineEvent, 0, len(rows))
	for _, row := range rows {
		events = append(events, EngineEvent{Seq: row.Seq, Kind: row.Kind, At: row.At, Payload: row.Payload})
	}
	return events
}
