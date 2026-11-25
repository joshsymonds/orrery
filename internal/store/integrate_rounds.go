package store

import (
	"context"
	"fmt"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// IntegrateRound is a round of the integrator of an integrate run for a conflicting task: the round at Round of the
// task at Task, which the run at Path ran at Attempt. Step is the step the round ran under, Verdict is merged, clash or
// ended, Head the commit the round left the candidate on, and Explanation what the integrator said. Outcome is empty
// until the round is composed or failed, and Failure says why a failed round failed. Created is the second the round
// was recorded at.
type IntegrateRound struct {
	Path        string
	Attempt     int64
	Task        int64
	Round       int64
	Step        string
	Verdict     string
	Head        string
	Explanation string
	Outcome     string
	Failure     string
	Created     time.Time
}

// RecordIntegrateRound stores round of epic in one write, created at the second at. The round's Created is not stored:
// at is. A round already stored under its path, attempt, task and round writes nothing, so a repeat leaves the first
// record as it is.
func (s *Store) RecordIntegrateRound(ctx context.Context, epic string, round IntegrateRound, at time.Time) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := queries.InsertIntegrateRound(ctx, sqlcgen.InsertIntegrateRoundParams{
			Epic: epic, Path: round.Path, Attempt: round.Attempt, Task: round.Task, Round: round.Round,
			Step: round.Step, Verdict: round.Verdict, Head: round.Head, Explanation: round.Explanation,
			Outcome: round.Outcome, Failure: round.Failure, Created: at.Unix(),
		}); err != nil {
			return fmt.Errorf("record round %d of task %d of node run %s of epic %s: %w",
				round.Round, round.Task, round.Path, epic, err)
		}
		return nil
	})
}

// ComposeIntegrateRound composes the round at round of the task at task, which the run at path ran at attempt, in one
// write: the round's outcome becomes composed, and the run's revision becomes commit and its result result. It changes
// nothing unless the round has no outcome yet and the run is running at attempt, so a round that is already composed or
// failed is not composed again, and a run that is not running keeps its revision and result. A change moves the epic's
// changed to at.
func (s *Store) ComposeIntegrateRound(
	ctx context.Context, epic, path string, attempt, task, round int64, commit, result string, at time.Time,
) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		composed, err := queries.ComposeIntegrateRound(ctx, sqlcgen.ComposeIntegrateRoundParams{
			Epic: epic, Path: path, Attempt: attempt, Task: task, Round: round,
		})
		if err != nil {
			return fmt.Errorf("compose round %d of task %d of node run %s of epic %s: %w",
				round, task, path, epic, err)
		}
		if composed == 0 {
			return nil
		}
		if err = queries.SetIntegrateRunWork(ctx, sqlcgen.SetIntegrateRunWorkParams{
			Revision: commit, Result: result, Epic: epic, Path: path, Attempt: attempt,
		}); err != nil {
			return fmt.Errorf("set work of node run %s of epic %s: %w", path, epic, err)
		}
		return moveIntegrateChanged(ctx, queries, epic, at)
	})
}

// SwapIntegrateRunResult replaces the result of the run at path of epic, running at attempt, with result, in one
// write, only while the run still holds was. It reports whether it wrote, so a result the run no longer holds, a run at
// another attempt and a run that is not running change nothing, and a repeat of a swap already written writes nothing.
// A change moves the epic's changed to at. An integrate run records its verification choices this way, on the result
// its kind decides from.
func (s *Store) SwapIntegrateRunResult(
	ctx context.Context, epic, path string, attempt int64, was, result string, at time.Time,
) (bool, error) {
	swapped := false
	err := s.write(ctx, func(queries *sqlcgen.Queries) error {
		rows, err := queries.SwapIntegrateRunResult(ctx, sqlcgen.SwapIntegrateRunResultParams{
			Result: result, Epic: epic, Path: path, Attempt: attempt, Was: was,
		})
		if err != nil {
			return fmt.Errorf("swap result of node run %s of epic %s: %w", path, epic, err)
		}
		if rows == 0 {
			return nil
		}
		swapped = true
		return moveIntegrateChanged(ctx, queries, epic, at)
	})
	return swapped, err
}

// FailIntegrateRound fails the round at round of the task at task, which the run at path ran at attempt, with failure,
// in one write. It changes nothing unless the round has no outcome yet, so a round that is already composed or failed
// keeps its outcome, and the rounds before it are kept as they are. A change moves the epic's changed to at.
func (s *Store) FailIntegrateRound(
	ctx context.Context, epic, path string, attempt, task, round int64, failure string, at time.Time,
) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		failed, err := queries.FailIntegrateRound(ctx, sqlcgen.FailIntegrateRoundParams{
			Failure: failure, Epic: epic, Path: path, Attempt: attempt, Task: task, Round: round,
		})
		if err != nil {
			return fmt.Errorf("fail round %d of task %d of node run %s of epic %s: %w",
				round, task, path, epic, err)
		}
		if failed == 0 {
			return nil
		}
		return moveIntegrateChanged(ctx, queries, epic, at)
	})
}

// moveIntegrateChanged moves the changed time of epic to at, in the caller's write.
func moveIntegrateChanged(ctx context.Context, queries *sqlcgen.Queries, epic string, at time.Time) error {
	if err := queries.UpdateEpicChanged(ctx, sqlcgen.UpdateEpicChangedParams{
		Changed: at.Unix(), ID: epic,
	}); err != nil {
		return fmt.Errorf("move changed of epic %s: %w", epic, err)
	}
	return nil
}

// readIntegrateRounds returns the integrate rounds of epic as queries read them, in the order of their path, attempt,
// task and round.
func readIntegrateRounds(ctx context.Context, queries *sqlcgen.Queries, epic string) ([]IntegrateRound, error) {
	rows, err := queries.ListEpicIntegrateRounds(ctx, epic)
	if err != nil {
		return nil, fmt.Errorf("list integrate rounds of epic %s: %w", epic, err)
	}
	return mapInOrder(rows, func(row sqlcgen.IntegrateRound) IntegrateRound {
		return IntegrateRound{
			Path: row.Path, Attempt: row.Attempt, Task: row.Task, Round: row.Round, Step: row.Step,
			Verdict: row.Verdict, Head: row.Head, Explanation: row.Explanation, Outcome: row.Outcome,
			Failure: row.Failure, Created: time.Unix(row.Created, 0),
		}
	}), nil
}
