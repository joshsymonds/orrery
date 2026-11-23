package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// StepFacts are the facts a step's end carries: Rounds is the rounds its loop ran, Seconds is the seconds from its start
// to its end, and Lines is its line counts.
type StepFacts struct {
	Rounds  int
	Seconds int64
	Lines   StepLines
}

// StepFactsOf returns the facts of the step row. Rounds is the row's loop round. Seconds is the seconds from the row's
// start to its end, zero while the row has not ended and never below zero. Lines is the row's line counts.
func StepFactsOf(row StepRow) StepFacts {
	facts := StepFacts{Rounds: row.LoopRound, Lines: row.Lines}
	if !row.Ended.IsZero() {
		facts.Seconds = max(row.Ended.Unix()-row.Started.Unix(), 0)
	}
	return facts
}

// StepFacts returns the facts of each step id that has a row, keyed by id. An id with no row is left out, and no ids
// return an empty map without opening the store. It answers every id in one query, on a connection it opens and
// closes. It returns an error that matches ErrUnavailable when the file is missing or the database is busy. It never
// creates the file and never writes the database.
func (r *Reader) StepFacts(ctx context.Context, ids []string) (map[string]StepFacts, error) {
	if len(ids) == 0 {
		return map[string]StepFacts{}, nil
	}
	db, closeDB, err := r.database()
	if err != nil {
		return nil, err
	}
	rows, err := sqlcgen.New(db).ListStepFacts(ctx, ids)
	if closeErr := closeDB(); closeErr != nil {
		err = errors.Join(err, fmt.Errorf("close store reader: %w", closeErr))
	}
	if err != nil {
		if unavailable(err) {
			return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
		}
		return nil, fmt.Errorf("list step facts: %w", err)
	}
	facts := make(map[string]StepFacts, len(rows))
	for _, row := range rows {
		step := StepRow{
			Started:   time.Unix(row.Started, 0),
			LoopRound: int(row.LoopRound),
			Lines: StepLines{
				Counted: row.LinesCounted != 0, Files: row.LinesFiles, Added: row.LinesAdded, Removed: row.LinesRemoved,
			},
		}
		// A step has ended once StepEnd has set its ended time, which is zero before.
		if row.Ended != 0 {
			step.Ended = time.Unix(row.Ended, 0)
		}
		facts[row.ID] = StepFactsOf(step)
	}
	return facts, nil
}
