package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// LastTurnEnds maps each session id to the at of its newest session_changed event whose payload sets state to
// done. A session with no such event is absent. It does the read in one read transaction on a connection it opens
// and closes. It returns an error that matches ErrUnavailable when the file is missing or the database is busy.
// It never creates the file and never writes the database.
func (r *Reader) LastTurnEnds(ctx context.Context) (map[string]int64, error) {
	db, err := sql.Open("sqlite", readOnlyDSN(r.path))
	if err != nil {
		return nil, fmt.Errorf("open store reader: %w", err)
	}
	ends, err := readLastTurnEnds(ctx, db)
	if closeErr := db.Close(); closeErr != nil {
		err = errors.Join(err, fmt.Errorf("close store reader: %w", closeErr))
	}
	if err != nil {
		if unavailable(err) {
			return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
		}
		return nil, err
	}
	return ends, nil
}

// readLastTurnEnds runs the turn-end query in one read transaction.
func readLastTurnEnds(ctx context.Context, db *sql.DB) (map[string]int64, error) {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin store read: %w", err)
	}
	rows, err := sqlcgen.New(tx).LastTurnEnds(ctx)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("list turn ends: %w", err), tx.Rollback())
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("end store read: %w", err)
	}
	ends := make(map[string]int64, len(rows))
	for _, row := range rows {
		ends[row.SessionID] = row.At
	}
	return ends, nil
}
