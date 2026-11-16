package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// EpicCaller names the session that made a call of an epic. A non-empty Session names the sessions row with that id.
// Otherwise the caller is a tmux pane, and Server and Pane name its row.
type EpicCaller struct{ Session, Server, Pane string }

// RecordEpicCall records call, made at at, as the newest call of epic by the session that made it, in one write. The
// caller resolves to its sessions row, and that session's row for epic takes the new call and time, so a later call by
// the same session replaces the earlier one. It reports false and writes nothing when no row resolves.
func (s *Store) RecordEpicCall(
	ctx context.Context, epic string, caller EpicCaller, call string, at time.Time,
) (bool, error) {
	var recorded bool
	err := s.write(ctx, func(queries *sqlcgen.Queries) error {
		session, err := resolveEpicCaller(ctx, queries, caller)
		if err != nil {
			return err
		}
		if session == "" {
			return nil
		}
		err = queries.UpsertEpicSession(ctx, sqlcgen.UpsertEpicSessionParams{
			Epic: epic, Session: session, Call: call, At: at.Unix(),
		})
		if err != nil {
			return fmt.Errorf("record call of epic %s by session %s: %w", epic, session, err)
		}
		recorded = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return recorded, nil
}

// resolveEpicCaller returns the id of the sessions row caller names, or "" when none does. A caller with a session id
// names the row with that id, whatever its kind. Any other caller names the pane row with its server and pane.
func resolveEpicCaller(ctx context.Context, queries *sqlcgen.Queries, caller EpicCaller) (string, error) {
	if caller.Session != "" {
		row, err := queries.GetSession(ctx, caller.Session)
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		if err != nil {
			return "", fmt.Errorf("read session %s: %w", caller.Session, err)
		}
		return row.ID, nil
	}
	id, err := queries.GetPaneSession(ctx, sqlcgen.GetPaneSessionParams{Server: caller.Server, Pane: caller.Pane})
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read pane %s of server %q: %w", caller.Pane, caller.Server, err)
	}
	return id, nil
}

// EpicSessionRow is one session that made a call of an epic: its newest call of the epic, the unix second it was made
// at, and the session's kind, server, pane, label and dir from its sessions row.
type EpicSessionRow struct {
	Session string
	Kind    string
	Server  string
	Pane    string
	Label   string
	Dir     string
	Call    string
	At      int64
}

// EpicSessions returns the sessions that made a call of epic and that the sessions table still holds, newest call
// first and ties by session id. It does the read in one read transaction on a connection it opens and closes. It
// returns an error that matches ErrUnavailable when the file is missing or the database is busy. It never creates the
// file and never writes the database.
func (r *Reader) EpicSessions(ctx context.Context, epic string) ([]EpicSessionRow, error) {
	db, err := sql.Open("sqlite", readOnlyDSN(r.path))
	if err != nil {
		return nil, fmt.Errorf("open store reader: %w", err)
	}
	rows, err := readEpicSessions(ctx, db, epic)
	if closeErr := db.Close(); closeErr != nil {
		err = errors.Join(err, fmt.Errorf("close store reader: %w", closeErr))
	}
	if err != nil {
		if unavailable(err) {
			return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
		}
		return nil, err
	}
	return rows, nil
}

// readEpicSessions runs the listing of epic in one read transaction.
func readEpicSessions(ctx context.Context, db *sql.DB, epic string) ([]EpicSessionRow, error) {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin store read: %w", err)
	}
	rows, err := sqlcgen.New(tx).ListEpicSessions(ctx, epic)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("list sessions of epic %s: %w", epic, err), tx.Rollback())
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("end store read: %w", err)
	}
	listed := make([]EpicSessionRow, 0, len(rows))
	for _, row := range rows {
		listed = append(listed, EpicSessionRow{
			Session: row.Session, Kind: row.Kind, Server: row.Server, Pane: row.Pane, Label: row.Label,
			Dir: row.Dir, Call: row.Call, At: row.At,
		})
	}
	return listed, nil
}
