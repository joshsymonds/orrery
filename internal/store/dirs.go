package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// Dirs returns the directories orreryd's store knows: the dir of every engine session and the workspace of every
// step, newest first, each once, at its newest position. Empty values and the worktrees of step sessions are left
// out. It does the read in one read transaction on a connection it opens and closes. It returns an error that
// matches ErrUnavailable when the file is missing or the database is busy. It never creates the file and never
// writes the database.
func (r *Reader) Dirs(ctx context.Context) ([]string, error) {
	db, err := sql.Open("sqlite", readOnlyDSN(r.path))
	if err != nil {
		return nil, fmt.Errorf("open store reader: %w", err)
	}
	dirs, err := readDirs(ctx, db)
	if closeErr := db.Close(); closeErr != nil {
		err = errors.Join(err, fmt.Errorf("close store reader: %w", closeErr))
	}
	if err != nil {
		if unavailable(err) {
			return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
		}
		return nil, err
	}
	return dirs, nil
}

// readDirs runs the listing in one read transaction.
func readDirs(ctx context.Context, db *sql.DB) ([]string, error) {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin store read: %w", err)
	}
	dirs, err := sqlcgen.New(tx).ListKnownDirs(ctx)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("list known dirs: %w", err), tx.Rollback())
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("end store read: %w", err)
	}
	return dirs, nil
}
