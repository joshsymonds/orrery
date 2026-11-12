package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// HeadlessRow is one headless engine session the phone lists, with its newest main-conversation reply.
type HeadlessRow struct {
	ID, Dir, State, Changed, Seen, Question, Reason string
	// Reply is the text of the newest engine_block of kind text with an empty parentToolUseId, or "" when none.
	Reply string
	// NewestReply is the at of the newest such block or of the newest engine_request whose refused is false,
	// whichever is later, or 0 when the session has neither.
	NewestReply int64
}

// HeadlessSessions reads the headless engine sessions with an event at or after cutoff, in id order, each with its
// newest main-conversation reply. Step and pane sessions are never listed. It does the read in one read transaction
// on a connection it opens and closes. It returns an error that matches ErrUnavailable when the file is missing or
// the database is busy. It never creates the file and never writes the database.
func (r *Reader) HeadlessSessions(ctx context.Context, cutoff int64) ([]HeadlessRow, error) {
	db, err := sql.Open("sqlite", readOnlyDSN(r.path))
	if err != nil {
		return nil, fmt.Errorf("open store reader: %w", err)
	}
	rows, err := readHeadlessSessions(ctx, db, cutoff)
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

// readHeadlessSessions runs the listing in one read transaction.
func readHeadlessSessions(ctx context.Context, db *sql.DB, cutoff int64) ([]HeadlessRow, error) {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin store read: %w", err)
	}
	rows, err := sqlcgen.New(tx).ListHeadlessSessions(ctx, cutoff)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("list headless sessions: %w", err), tx.Rollback())
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("end store read: %w", err)
	}
	headless := make([]HeadlessRow, 0, len(rows))
	for _, row := range rows {
		headless = append(headless, HeadlessRow{
			ID: row.ID, Dir: row.Dir, State: row.State, Changed: row.Changed, Seen: row.Seen,
			Question: row.Question, Reason: row.Reason, Reply: row.Reply, NewestReply: row.NewestReply,
		})
	}
	return headless, nil
}
