package store

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// RecordStepReads stores one read for each of reads: the step that made it, the contract it read and the version it
// read, which is 0 for a draft. The reads are stored in one write, so an error stores none. A read that repeats one the
// same step already made of the same contract is an error.
func (s *Store) RecordStepReads(ctx context.Context, reads []StepRead) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		for _, read := range reads {
			err := queries.InsertContractStepRead(ctx, sqlcgen.InsertContractStepReadParams{
				Step: read.Step, Contract: read.Contract, Version: read.Version,
			})
			if err != nil {
				return fmt.Errorf("insert read of contract %s by step %s: %w", read.Contract, read.Step, err)
			}
		}
		return nil
	})
}

// ContractStepReads returns the reads of the contract id, oldest first: by the start of the step that made each, and
// ties by step id. A contract no step read has none.
func (s *Store) ContractStepReads(ctx context.Context, id string) ([]StepRead, error) {
	tx, err := s.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin step reads read: %w", err)
	}
	reads, err := readContractStepReads(ctx, sqlcgen.New(tx), id)
	if err != nil {
		return nil, errors.Join(err, tx.Rollback())
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("end step reads read: %w", err)
	}
	return reads, nil
}

// ContractStepReads returns the reads of the contract id as Store.ContractStepReads does, read without writing the
// store.
func (r *Reader) ContractStepReads(ctx context.Context, id string) ([]StepRead, error) {
	var reads []StepRead
	err := r.readEngine(ctx, func(queries *sqlcgen.Queries) error {
		var readErr error
		reads, readErr = readContractStepReads(ctx, queries, id)
		return readErr
	})
	return reads, err
}

// ContractRowWrites returns the row writes of the contract id, oldest first, as Store.ContractRowWrites does, read
// without writing the store.
func (r *Reader) ContractRowWrites(ctx context.Context, id string) ([]ContractRowWrite, error) {
	var writes []ContractRowWrite
	err := r.readEngine(ctx, func(queries *sqlcgen.Queries) error {
		var readErr error
		writes, readErr = readContractRowWrites(ctx, queries, id)
		return readErr
	})
	return writes, err
}

// readContractStepReads returns the reads of the contract id in the order ContractStepReads gives them, read with
// queries. Each read's step is read for its start. Steps are never removed, so a read whose step has no row is an
// error.
func readContractStepReads(ctx context.Context, queries *sqlcgen.Queries, id string) ([]StepRead, error) {
	rows, err := queries.ListContractStepReads(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("list step reads of contract %s: %w", id, err)
	}
	reads := make([]StepRead, 0, len(rows))
	steps := make([]string, 0, len(rows))
	for _, row := range rows {
		reads = append(reads, StepRead{Step: row.Step, Contract: row.Contract, Version: row.Version})
		steps = append(steps, row.Step)
	}
	if len(steps) == 0 {
		return reads, nil
	}
	facts, err := queries.ListStepFacts(ctx, steps)
	if err != nil {
		return nil, fmt.Errorf("read steps of contract %s: %w", id, err)
	}
	started := make(map[string]int64, len(facts))
	for _, fact := range facts {
		started[fact.ID] = fact.Started
	}
	for _, step := range steps {
		if _, ok := started[step]; !ok {
			return nil, fmt.Errorf("read step %s of contract %s: no such step", step, id)
		}
	}
	slices.SortFunc(reads, func(first, second StepRead) int {
		return cmp.Or(cmp.Compare(started[first.Step], started[second.Step]), strings.Compare(first.Step, second.Step))
	})
	return reads, nil
}
