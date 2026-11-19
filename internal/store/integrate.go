package store

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// The epic phases the integration writes set or reads. An epic is built once the integrate batches of its plan have
// passed and every Requirement is green at its stored base. An evidence node at the epic's base that finds a
// Requirement already met ends passed with a tell, and the epic goes on. A park an evidence node made before that is
// still answered: the owner's end answer to it sets met_at_base. A plan that ends built sets built only on an accepted
// epic.
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

// BatchEnd is the end of an integrate run: the running run at Path, at Attempt, ends with Outcome and Output.
// Candidate is the epic's candidate once the batch has ended, which the caller worked out with git: a commit that holds
// Over, the stored base the caller read, and builds on it. For a passed batch Over is the run's revision, the stored
// base it moves to, and an empty Candidate leaves the epic with none. For a failed batch Over is the stored base the
// epic held, and an empty Candidate leaves the epic's candidate as it is.
type BatchEnd struct {
	Path      string
	Attempt   int64
	Outcome   string
	Output    string
	Candidate string
	Over      string
}

// EndBatch ends the running run end names, and settles the epic's bases by the run's revision and end's candidate, in
// one write (settleBatchBase). A non-nil trigger is added pending unless its id is stored. A run at another attempt and
// a run that is not running change nothing, so a repeat adds no trigger.
func (s *Store) EndBatch(ctx context.Context, epic string, end BatchEnd, trigger *NodeTrigger, at time.Time) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		ended, err := queries.EndEpicNodeRunAttempt(ctx, sqlcgen.EndEpicNodeRunAttemptParams{
			Outcome: end.Outcome, Ended: at.Unix(), Output: capNodeOutput(end.Output), Epic: epic, Path: end.Path,
			Attempt: end.Attempt,
		})
		if err != nil {
			return fmt.Errorf("end node run %s of epic %s: %w", end.Path, epic, err)
		}
		if ended == 0 {
			return nil
		}
		run, err := queries.GetEpicNodeRun(ctx, sqlcgen.GetEpicNodeRunParams{Epic: epic, Path: end.Path})
		if err != nil {
			return fmt.Errorf("read node run %s of epic %s: %w", end.Path, epic, err)
		}
		if settleErr := settleBatchBase(ctx, queries, epic, end, run); settleErr != nil {
			return settleErr
		}
		if trigger == nil {
			return nil
		}
		return insertNodeTrigger(ctx, queries, epic, *trigger, at)
	})
}

// settleBatchBase moves the bases of epic for run, the integrate run end ends, in the caller's write. A passed run moves
// the stored base to its revision and clears the candidate. The candidate is then set to end's candidate when there is
// one and end's Over is the stored base the epic holds after the end: the caller checked with git that the candidate
// holds and builds on that commit, so a candidate checked against a stored base the epic no longer holds is never
// stored, and the candidate always builds on the stored base. A failed run moves nothing else, and a candidate that is
// the stored base itself is no candidate. A run with no revision, an empty batch, moves nothing.
func settleBatchBase(
	ctx context.Context, queries *sqlcgen.Queries, epic string, end BatchEnd, run sqlcgen.EpicNodeRun,
) error {
	if run.Revision == "" {
		return nil
	}
	head, err := epicHead(ctx, queries, epic)
	if err != nil {
		return err
	}
	storedBase := head.StoredBase
	switch end.Outcome {
	case "passed":
		if err = queries.MoveEpicStoredBase(ctx, sqlcgen.MoveEpicStoredBaseParams{
			StoredBase: run.Revision, ID: epic,
		}); err != nil {
			return fmt.Errorf("move stored base of epic %s: %w", epic, err)
		}
		storedBase = run.Revision
	case "failed":
	default:
		return nil
	}
	if end.Candidate == "" || end.Over != storedBase || end.Candidate == storedBase {
		return nil
	}
	if err = queries.SetEpicCandidate(ctx, sqlcgen.SetEpicCandidateParams{
		Candidate: end.Candidate, ID: epic,
	}); err != nil {
		return fmt.Errorf("set candidate of epic %s: %w", epic, err)
	}
	return nil
}

// EndNodesTelling ends each of ends at its running run, and adds tell as a tell of epic, in one write. The tell is
// added only when the first end's run is still running, as EndPlanBuilt requires, so a run that has ended already, and
// no end, change nothing and a repeat adds no tell.
func (s *Store) EndNodesTelling(ctx context.Context, epic string, ends []NodeEnd, tell string, at time.Time) error {
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
		return endAndTell(ctx, queries, epic, ends, tell, at)
	})
}

// endAndTell ends each of ends at its running run, and adds tell as a tell of epic, in the caller's write.
func endAndTell(
	ctx context.Context,
	queries *sqlcgen.Queries,
	epic string,
	ends []NodeEnd,
	tell string,
	at time.Time,
) error {
	if err := endNodeRuns(ctx, queries, epic, ends, at); err != nil {
		return err
	}
	return insertTell(ctx, queries, epic, capTurnText(tell), at)
}

// ParkEpicWaking parks epic with reason as the not_built planner class, and adds trigger pending, in one write. The
// planner class adds no tell, and the trigger is the one park's wake, so no second trigger is added. A trigger whose id
// is stored in any state changes nothing, so a repeat parks nothing.
func (s *Store) ParkEpicWaking(ctx context.Context, epic, reason string, trigger NodeTrigger, at time.Time) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		stored, err := queries.CountEpicTrigger(ctx, sqlcgen.CountEpicTriggerParams{Epic: epic, ID: trigger.ID})
		if err != nil {
			return fmt.Errorf("read trigger %s of epic %s: %w", trigger.ID, epic, err)
		}
		if stored != 0 {
			return nil
		}
		if _, err = setParked(ctx, queries, epic, ParkClassNotBuilt, "", capTurnText(reason), at); err != nil {
			return err
		}
		return insertNodeTrigger(ctx, queries, epic, trigger, at)
	})
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
	question, err := openPark(ctx, queries, epic, park, ParkClassMetAtBase, metAtBaseQuestion(park, met))
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
	if err := queries.SetEpicPhase(withEpicEvent(ctx), sqlcgen.SetEpicPhaseParams{
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

// batchComposition is the part of an integrate run's result the store reads: the tasks the batch composed into commits.
type batchComposition struct {
	Composed []composedTask `json:"composed"`
}

// composedTask is one task a batch composed, by its seq.
type composedTask struct {
	Task int64 `json:"task"`
}

// composesTask reports whether run is an integrate run whose result composed task seq into a commit. A result that is not
// a batch result composed none.
func composesTask(run NodeRun, seq int64) bool {
	if run.Kind != integrateKind {
		return false
	}
	var result batchComposition
	if json.Unmarshal([]byte(run.Result), &result) != nil {
		return false
	}
	return slices.ContainsFunc(result.Composed, func(composed composedTask) bool { return composed.Task == seq })
}

// runningBatchOf returns the path of a running integrate run that claims task or composes it, or "" when none does. Such a
// run carries the task's commit onward, so the task is not sent back while one runs.
func runningBatchOf(runs []NodeRun, task sqlcgen.EpicTask) string {
	for _, run := range runs {
		if run.State != NodeRunning || run.Kind != integrateKind {
			continue
		}
		if (task.Batch != "" && run.Path == task.Batch) || composesTask(run, task.Seq) {
			return run.Path
		}
	}
	return ""
}

// claimedPassed reports whether the integrate run that claims task ended passed with the task in its batch, so the stored
// base holds the task's commit. A task its claiming run left out is not in the base, though the run passed.
func claimedPassed(runs []NodeRun, task sqlcgen.EpicTask) bool {
	if task.Batch == "" {
		return false
	}
	i := slices.IndexFunc(runs, func(run NodeRun) bool { return run.Path == task.Batch })
	return i >= 0 && runs[i].State == NodeEnded && runs[i].Outcome == nodePassedOutcome &&
		composesTask(runs[i], task.Seq)
}
