package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// EpicFlow is one epic's rows as the interpreter decides from them: the epic as Epic returns it, its node runs as
// NodeRuns returns them, and how many of its triggers are pending.
type EpicFlow struct {
	Epic    EpicRow
	Runs    []NodeRun
	Pending int
}

// EpicFlow returns the stored epic with id with its node runs and its pending trigger count. The three are read in one
// read transaction, so they describe one state of the store. It returns an error matching ErrNoEpic when no epic has
// that id.
func (s *Store) EpicFlow(ctx context.Context, id string) (EpicFlow, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return EpicFlow{}, fmt.Errorf("begin epic read: %w", err)
	}
	flow, err := readEpicFlow(ctx, sqlcgen.New(tx), id)
	if err != nil {
		return EpicFlow{}, errors.Join(err, tx.Rollback())
	}
	if err = tx.Commit(); err != nil {
		return EpicFlow{}, fmt.Errorf("end epic read: %w", err)
	}
	return flow, nil
}

// readEpicFlow reads the epic with id, its node runs and its pending trigger count with queries.
func readEpicFlow(ctx context.Context, queries *sqlcgen.Queries, id string) (EpicFlow, error) {
	epic, err := readEpic(ctx, queries, id)
	if err != nil {
		return EpicFlow{}, err
	}
	runs, err := readNodeRuns(ctx, queries, id)
	if err != nil {
		return EpicFlow{}, err
	}
	pending, err := queries.CountPendingEpicTriggers(ctx, id)
	if err != nil {
		return EpicFlow{}, fmt.Errorf("count pending triggers of epic %s: %w", id, err)
	}
	return EpicFlow{Epic: epic, Runs: runs, Pending: int(pending)}, nil
}
