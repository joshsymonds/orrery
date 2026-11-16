package store

import (
	"context"
	"fmt"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// EndClosure ends the running run at path of epic at attempt with outcome and output, and stores round and closures in
// the same write. Every closure must name round: a closure that names another round is an error and nothing is written.
// A round already stored under its number keeps the round and closures it stored first, so a run ending at that number
// stores neither. A run that is not running at attempt changes nothing.
func (s *Store) EndClosure(
	ctx context.Context, epic, path string, attempt int64, outcome, output string, round ReviewRound,
	closures []Closure, at time.Time,
) error {
	for _, closure := range closures {
		if closure.Round != round.Round {
			return fmt.Errorf("closure of finding %s names round %d, not round %d of epic %s",
				closure.Finding, closure.Round, round.Round, epic)
		}
	}
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		ended, err := queries.EndEpicNodeRunAttempt(ctx, sqlcgen.EndEpicNodeRunAttemptParams{
			Outcome: outcome, Ended: at.Unix(), Output: capNodeOutput(output), Epic: epic, Path: path, Attempt: attempt,
		})
		if err != nil {
			return fmt.Errorf("end node run %s of epic %s: %w", path, epic, err)
		}
		if ended == 0 {
			return nil
		}
		return insertClosureRound(ctx, queries, epic, round, closures)
	})
}

// insertClosureRound stores round and each of closures in the caller's write, unless a round with round's number is
// stored already, which keeps its round and closures as they are.
func insertClosureRound(
	ctx context.Context, queries *sqlcgen.Queries, epic string, round ReviewRound, closures []Closure,
) error {
	stored, err := queries.ListEpicReviewRounds(ctx, epic)
	if err != nil {
		return fmt.Errorf("list rounds of epic %s: %w", epic, err)
	}
	for _, r := range stored {
		if r.Round == round.Round {
			return nil
		}
	}
	tasks, err := encodeList(round.Tasks)
	if err != nil {
		return fmt.Errorf("encode tasks of round %d of epic %s: %w", round.Round, epic, err)
	}
	if err = queries.InsertEpicReviewRound(ctx, sqlcgen.InsertEpicReviewRoundParams{
		Epic: epic, Round: round.Round, Revision: round.Revision, Tasks: tasks, Outcome: round.Outcome,
	}); err != nil {
		return fmt.Errorf("insert round %d of epic %s: %w", round.Round, epic, err)
	}
	for _, closure := range closures {
		if err = queries.InsertEpicClosure(ctx, sqlcgen.InsertEpicClosureParams{
			Epic: epic, Round: closure.Round, Finding: closure.Finding, Step: closure.Step, Result: closure.Result,
			Reason: closure.Reason,
		}); err != nil {
			return fmt.Errorf("insert closure of finding %s in round %d of epic %s: %w",
				closure.Finding, closure.Round, epic, err)
		}
	}
	return nil
}
