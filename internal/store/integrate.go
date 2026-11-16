package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// The epic phases the integration writes set or reads. An epic is built once the integrate batches of its plan have
// passed and every Requirement is green at its stored base. An evidence node at the epic's base that finds a Requirement
// already met parks the epic with a question, and the owner's end answer sets met_at_base. A plan that ends built sets
// built only on an accepted epic.
const (
	phaseAccepted  = "accepted"
	phaseBuilt     = "built"
	phaseMetAtBase = "met_at_base"
)

// ClaimBatch stores revision, refs and result as the revision, refs and result of the running run at path of epic at
// attempt, and claims each of tasks that is done and has no batch or the batch path, in one write. A run that already
// has a revision, a run at another attempt and a run that is not running change nothing, so the claim is made once.
func (s *Store) ClaimBatch(
	ctx context.Context, epic, path string, attempt int64, revision, refs, result string, tasks []int64,
) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		claimed, err := queries.ClaimEpicNodeRunWork(ctx, sqlcgen.ClaimEpicNodeRunWorkParams{
			Revision: revision, Refs: refs, Result: result, Epic: epic, Path: path, Attempt: attempt,
		})
		if err != nil {
			return fmt.Errorf("claim node run %s of epic %s: %w", path, epic, err)
		}
		if claimed == 0 {
			return nil
		}
		return claimBatchTasks(ctx, queries, epic, path, tasks)
	})
}

// claimBatchTasks claims each of tasks that is done and has no batch or the batch path for the batch at path, in the
// caller's write.
func claimBatchTasks(ctx context.Context, queries *sqlcgen.Queries, epic, path string, tasks []int64) error {
	for _, seq := range tasks {
		if err := queries.ClaimEpicTaskBatch(ctx, sqlcgen.ClaimEpicTaskBatchParams{
			Batch: path, Epic: epic, Seq: seq,
		}); err != nil {
			return fmt.Errorf("claim task %d of epic %s: %w", seq, epic, err)
		}
	}
	return nil
}

// EndBatch ends the running run at path of epic at attempt with outcome and output, and settles the epic's bases by
// the run's revision, in one write. A passed batch moves the stored base to its revision and clears the candidate. A
// failed batch at a revision other than the stored base sets the candidate to it. A non-nil trigger is added pending
// unless its id is stored. A run at another attempt and a run that is not running change nothing, so a repeat adds no
// trigger.
func (s *Store) EndBatch(
	ctx context.Context, epic, path string, attempt int64, outcome, output string, trigger *NodeTrigger, at time.Time,
) error {
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
		run, err := queries.GetEpicNodeRun(ctx, sqlcgen.GetEpicNodeRunParams{Epic: epic, Path: path})
		if err != nil {
			return fmt.Errorf("read node run %s of epic %s: %w", path, epic, err)
		}
		return settleEndedBatch(ctx, queries, epic, outcome, run.Revision, trigger, at)
	})
}

// settleEndedBatch settles the bases of epic for a run that ended with outcome at revision, then adds trigger pending
// when it is not nil, in the caller's write.
func settleEndedBatch(
	ctx context.Context, queries *sqlcgen.Queries, epic, outcome, revision string, trigger *NodeTrigger, at time.Time,
) error {
	if err := settleBatchBase(ctx, queries, epic, outcome, revision); err != nil {
		return err
	}
	if trigger == nil {
		return nil
	}
	return insertNodeTrigger(ctx, queries, epic, *trigger, at)
}

// settleBatchBase moves the bases of epic for an integrate run that ended with outcome at revision. A passed run moves
// the stored base to revision and clears the candidate. A failed run sets the candidate to revision unless revision is
// the stored base. A run with no revision moves nothing.
func settleBatchBase(ctx context.Context, queries *sqlcgen.Queries, epic, outcome, revision string) error {
	if revision == "" {
		return nil
	}
	switch outcome {
	case "passed":
		if err := queries.MoveEpicStoredBase(ctx, sqlcgen.MoveEpicStoredBaseParams{
			StoredBase: revision, ID: epic,
		}); err != nil {
			return fmt.Errorf("move stored base of epic %s: %w", epic, err)
		}
	case "failed":
		head, err := epicHead(ctx, queries, epic)
		if err != nil {
			return err
		}
		if revision == head.StoredBase {
			return nil
		}
		if err = queries.SetEpicCandidate(ctx, sqlcgen.SetEpicCandidateParams{
			Candidate: revision, ID: epic,
		}); err != nil {
			return fmt.Errorf("set candidate of epic %s: %w", epic, err)
		}
	}
	return nil
}

// ParkEpicWaking parks epic with reason, adds reason as a tell and adds trigger pending, in one write. A trigger whose id
// is stored in any state changes nothing, so a repeat parks and tells nothing.
func (s *Store) ParkEpicWaking(ctx context.Context, epic, reason string, trigger NodeTrigger, at time.Time) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		stored, err := queries.CountEpicTrigger(ctx, sqlcgen.CountEpicTriggerParams{Epic: epic, ID: trigger.ID})
		if err != nil {
			return fmt.Errorf("read trigger %s of epic %s: %w", trigger.ID, epic, err)
		}
		if stored != 0 {
			return nil
		}
		return parkWithTrigger(ctx, queries, epic, reason, trigger, at)
	})
}

// parkWithTrigger parks epic with reason, adds reason as a tell and adds trigger pending, in the caller's write.
func parkWithTrigger(
	ctx context.Context, queries *sqlcgen.Queries, epic, reason string, trigger NodeTrigger, at time.Time,
) error {
	if err := parkWithTell(ctx, queries, epic, capTurnText(reason), at); err != nil {
		return err
	}
	return insertNodeTrigger(ctx, queries, epic, trigger, at)
}

// EndPlanBuilt ends each of ends at its running run, in one write, when the run of the first end is still running. The
// phase of epic becomes built only when it is accepted, so a plan that ends built while the epic is correcting leaves
// the phase correcting. A first end whose run has ended already, and no end, change nothing.
func (s *Store) EndPlanBuilt(ctx context.Context, epic string, ends []NodeEnd, at time.Time) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if len(ends) == 0 {
			return nil
		}
		running, err := queries.CountRunningEpicNodeRun(ctx, sqlcgen.CountRunningEpicNodeRunParams{
			Epic: epic, Path: ends[0].Path,
		})
		if err != nil {
			return fmt.Errorf("read node run %s of epic %s: %w", ends[0].Path, epic, err)
		}
		if running == 0 {
			return nil
		}
		return endAndBuild(ctx, queries, epic, ends, at)
	})
}

// endAndBuild ends each of ends at its running run, in the caller's write. It sets the phase of epic to built when the
// phase is accepted and leaves any other phase as it is.
func endAndBuild(ctx context.Context, queries *sqlcgen.Queries, epic string, ends []NodeEnd, at time.Time) error {
	if err := endNodeRuns(ctx, queries, epic, ends, at); err != nil {
		return err
	}
	head, err := epicHead(ctx, queries, epic)
	if err != nil {
		return err
	}
	if head.Phase != phaseAccepted {
		return nil
	}
	return setEpicPhase(ctx, queries, epic, phaseBuilt, at)
}

// ParkNodeMetAtBase parks the running run at path of epic at attempt with outcome and output, parks epic with reason,
// adds reason as a tell and opens the park question for met, the Requirements that run judged holding at the epic's
// base, in one write. The question offers retry, drop and end. The write also keeps the run's path and attempt and met,
// which answering the question needs. A run at another attempt and a run that is not running change nothing, so a
// repeat adds no tell. met is not empty.
func (s *Store) ParkNodeMetAtBase(
	ctx context.Context, epic, path string, attempt int64, outcome, output, reason string, met []string, at time.Time,
) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		parked, err := parkRunningRun(ctx, queries, epic, path, attempt, outcome, output)
		if err != nil || !parked {
			return err
		}
		return parkMetAtBase(ctx, queries, epic, path, attempt, reason, met, at)
	})
}

// parkMetAtBase parks epic with reason and opens its park question for met, as metAtBaseQuestion gives it. It stores
// the run at path of attempt and met under that question, and adds reason as a tell that carries the question's seq, in
// the caller's write.
func parkMetAtBase(
	ctx context.Context, queries *sqlcgen.Queries, epic, path string, attempt int64, reason string, met []string,
	at time.Time,
) error {
	park := capTurnText(reason)
	question, err := openPark(ctx, queries, epic, park, metAtBaseQuestion(park, met))
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(met)
	if err != nil {
		return fmt.Errorf("requirements of the park of epic %s: %w", epic, err)
	}
	if err = queries.InsertEpicMetAtBase(ctx, sqlcgen.InsertEpicMetAtBaseParams{
		Epic: epic, Question: question, Path: path, Attempt: attempt, Requirements: string(encoded),
	}); err != nil {
		return fmt.Errorf("record the park question %d of epic %s: %w", question, epic, err)
	}
	return insertQuestionTell(ctx, queries, epic, park, question, at)
}

// setEpicPhase stores phase as the phase of epic in the caller's write, at as the time it was set. A phase that epic
// already has keeps the time it was set.
func setEpicPhase(ctx context.Context, queries *sqlcgen.Queries, epic, phase string, at time.Time) error {
	if err := queries.SetEpicPhase(ctx, sqlcgen.SetEpicPhaseParams{
		Phase: phase, At: at.Unix(), ID: epic,
	}); err != nil {
		return fmt.Errorf("set phase of epic %s: %w", epic, err)
	}
	return nil
}

// metAtBaseRefusal is the refusal of a retry of an epic in phase met_at_base, which the owner's end answer set, with
// the park reason it is parked with.
func metAtBaseRefusal(epic, reason string) RefusalError {
	return RefusalError(fmt.Sprintf("epic %s is parked for good: %s", epic, reason))
}
