package store

import (
	"context"
	"fmt"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// AddTaskReview stores review, with its findings as given, for task seq of epic and stores to as the task's build, in
// one write, when the stored build is from. A task that is not open, or whose stored build is not from, is ErrTaskMoved
// and writes nothing. A verdict other than holds or fails is refused by the store and writes nothing.
func (s *Store) AddTaskReview(
	ctx context.Context,
	epic string,
	seq int64,
	from, to TaskBuild,
	review TaskReview,
) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := moveTask(ctx, queries, epic, seq, from, to); err != nil {
			return err
		}
		if err := queries.InsertEpicTaskReview(ctx, sqlcgen.InsertEpicTaskReviewParams{
			Epic: epic, Task: seq, Attempt: review.Attempt, Position: review.Position,
			Gate: review.Gate, Step: review.Step, Verdict: review.Verdict,
		}); err != nil {
			return fmt.Errorf("insert review %d of task %d of epic %s: %w", review.Position, seq, epic, err)
		}
		for _, finding := range review.Findings {
			if err := queries.InsertEpicTaskFinding(ctx, sqlcgen.InsertEpicTaskFindingParams{
				Epic: epic, Task: seq, Attempt: review.Attempt, Review: review.Position, Position: finding.Position,
				ID: finding.ID, Claim: finding.Claim, Citation: finding.Citation, Location: finding.Location,
				Evidence: finding.Evidence, VerifyBy: finding.VerifyBy, Step: finding.Step, Result: finding.Result,
				Reason: finding.Reason, Unverified: boolInt(finding.Unverified),
			}); err != nil {
				return fmt.Errorf("insert finding %d of review %d of task %d of epic %s: %w",
					finding.Position, review.Position, seq, epic, err)
			}
		}
		return nil
	})
}

// SetFindingResult stores result, with its step and reason, on one finding of task seq of epic and stores to as the
// task's build, in one write, when the stored build is from. A result with no outcome is refused and writes nothing. A
// task that is not open, or whose stored build is not from, is ErrTaskMoved and writes nothing. A finding that is not
// stored, or already has a result, is ErrTaskMoved and writes nothing.
func (s *Store) SetFindingResult(
	ctx context.Context,
	epic string,
	seq int64,
	from, to TaskBuild,
	result TaskFindingResult,
) error {
	if result.Result == "" {
		return fmt.Errorf("finding %d of review %d of task %d of epic %s: result is empty",
			result.Position, result.Review, seq, epic)
	}
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := moveTask(ctx, queries, epic, seq, from, to); err != nil {
			return err
		}
		rows, err := queries.SetEpicTaskFindingResult(ctx, sqlcgen.SetEpicTaskFindingResultParams{
			Step: result.Step, Result: result.Result, Reason: result.Reason,
			Epic: epic, Task: seq, Attempt: result.Attempt, Review: result.Review, Position: result.Position,
		})
		if err != nil {
			return fmt.Errorf("set result of finding %d of review %d of task %d of epic %s: %w",
				result.Position, result.Review, seq, epic, err)
		}
		if rows == 0 {
			return fmt.Errorf("finding %d of review %d of task %d of epic %s is not stored or has a result: %w",
				result.Position, result.Review, seq, epic, ErrTaskMoved)
		}
		return nil
	})
}
