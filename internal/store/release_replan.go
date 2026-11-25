package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// replanStepName is the step a replan release run records.
const replanStepName = "replan"

// integratorReturnedEnd is the end of a round of the release integrator that returned blocked: a step that returned a
// status other than its done value ends returned, and the integrator's only other status is blocked.
const integratorReturnedEnd = "returned"

// RecordIntegratorRound stores round, a round of the release integrator, as a release run of epic in one write, as
// RecordRelease does. When replan is set, the round was blocked on a change to the epic's own candidate, and the write
// also stores a replan release run at the round's attempt, with the round's start as its start and the round's reason
// as its reason, unless a replan release run of epic already names that start: a replan that left the release tip
// where it was would only send the same merge back to the integrator, so the release stops on the round instead.
func (s *Store) RecordIntegratorRound(ctx context.Context, epic string, round ReleaseRun, replan bool) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := recordRelease(ctx, queries, epic, round); err != nil {
			return err
		}
		if !replan {
			return nil
		}
		runs, err := readReleaseRuns(ctx, queries, epic)
		if err != nil {
			return err
		}
		if slices.ContainsFunc(runs, func(run ReleaseRun) bool {
			return run.Part == ReleasePartReplan && run.Start == round.Start
		}) {
			return nil
		}
		return recordReplan(ctx, queries, epic, round)
	})
}

// recordReplan stores the replan release run of round, a blocked round of the release integrator, at the round's attempt
// and time, in the caller's write. A replan of the same attempt stored already changes nothing.
func recordReplan(ctx context.Context, queries *sqlcgen.Queries, epic string, round ReleaseRun) error {
	return recordRelease(ctx, queries, epic, ReleaseRun{
		Part: ReleasePartReplan, Attempt: round.Attempt, Step: replanStepName, Start: round.Start,
		Reason: round.Reason, At: round.At,
	})
}

// ReplanRelease sends the release of epic back to planning, in one write, for the replan release run of the running
// release node run at path and attempt. It moves epic from releasing, or from stopped when a retry ended the stop, to
// correcting, starts plan, the plan run that plans and integrates the change to the candidate, adds trigger, which wakes
// the planner with the integrator's report, and adds tell, cut to TurnReasonMax bytes. An epic in another phase, or
// whose release node run is not running at attempt, changes nothing, so a repeat is a no-op.
func (s *Store) ReplanRelease(
	ctx context.Context, epic, path string, attempt int64, plan NodeRun, trigger NodeTrigger, tell string,
	at time.Time,
) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		_, err := replanInWrite(ctx, queries, epic, path, attempt, plan, trigger, tell, at)
		return err
	})
}

// ReplanReview sends the release of epic back to planning for a release review that refused its candidate with
// findings, in one write. It does what ReplanRelease does, and when the release moves it also stores replan, a replan
// release run of the attempt of the running release node run at path, with the epic's branch tip the candidate was made
// from as its head and the review's reason as its reason. Its part, step and time are set here. A release that does
// not move changes nothing, so a repeat is a no-op.
func (s *Store) ReplanReview(
	ctx context.Context, epic, path string, replan ReleaseRun, plan NodeRun, trigger NodeTrigger, tell string,
	at time.Time,
) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		sent, err := replanInWrite(ctx, queries, epic, path, replan.Attempt, plan, trigger, tell, at)
		if err != nil || !sent {
			return err
		}
		replan.Part, replan.Step, replan.At = ReleasePartReplan, replanStepName, at
		return recordRelease(ctx, queries, epic, replan)
	})
}

// replanInWrite sends the release of epic at path and attempt back to planning, in the caller's write: the epic moves
// from releasing or stopped to correcting, plan is started, and trigger and tell are added, as ReplanRelease does. It
// reports whether the epic moved. A release node run that is not running at attempt, or an epic in another phase,
// changes nothing.
func replanInWrite(
	ctx context.Context, queries *sqlcgen.Queries, epic, path string, attempt int64, plan NodeRun,
	trigger NodeTrigger, tell string, at time.Time,
) (bool, error) {
	running, err := runningAt(ctx, queries, epic, path, attempt)
	if err != nil || !running {
		return false, err
	}
	moved, err := queries.ReplanEpicRelease(ctx, sqlcgen.ReplanEpicReleaseParams{PhaseAt: at.Unix(), ID: epic})
	if err != nil {
		return false, fmt.Errorf("move epic %s back to planning: %w", epic, err)
	}
	if moved == 0 {
		return false, nil
	}
	if startErr := insertNodeRuns(ctx, queries, epic, []NodeRun{plan}, at); startErr != nil {
		return false, startErr
	}
	if triggerErr := insertNodeTrigger(ctx, queries, epic, trigger, at); triggerErr != nil {
		return false, triggerErr
	}
	if tellErr := insertTell(ctx, queries, epic, capTurnText(tell), at); tellErr != nil {
		return false, tellErr
	}
	return true, nil
}

// runningAt reports whether the node run of epic at path is running at attempt.
func runningAt(ctx context.Context, queries *sqlcgen.Queries, epic, path string, attempt int64) (bool, error) {
	run, err := queries.GetEpicNodeRun(ctx, sqlcgen.GetEpicNodeRunParams{Epic: epic, Path: path})
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read node run %s of epic %s: %w", path, epic, err)
	}
	return run.State == NodeRunning && run.Attempt == attempt, nil
}

// ResumeReplannedRelease resumes the release of epic once its replan has ended, in one write. The running release node
// run at path and attempt moves to its next attempt with its outcome, output, revision, refs, refs checked, result and
// end cleared, so it prepares its candidate again from the epic's branch tip, and epic moves from correcting to clean, so
// the release starts again. A release node run that is not running at attempt changes nothing, so a repeat is a no-op.
func (s *Store) ResumeReplannedRelease(ctx context.Context, epic, path string, attempt int64, at time.Time) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		moved, err := queries.NextEpicNodeRunAttempt(ctx, sqlcgen.NextEpicNodeRunAttemptParams{
			Epic: epic, Path: path, Attempt: attempt,
		})
		if err != nil {
			return fmt.Errorf("move node run %s of epic %s to its next attempt: %w", path, epic, err)
		}
		if moved == 0 {
			return nil
		}
		if err = queries.CleanReplannedEpic(
			ctx,
			sqlcgen.CleanReplannedEpicParams{PhaseAt: at.Unix(), ID: epic},
		); err != nil {
			return fmt.Errorf("move epic %s back to clean: %w", epic, err)
		}
		return nil
	})
}

// retryStoppedRelease ends the stop of head's release for a retry, in the caller's write, when the release integrator or
// a release reviewer stopped it: the attempt of its parked release node run holds a round of the release integrator that
// returned blocked, or a review that refused the candidate. It stores a replan release run of that round or review, as
// the integrator's own replan stores one, turns the release node run running at the same attempt, answers each open park
// question retry and ends the park as RetryEpic does. head stays stopped until the release kind's next decision sends
// the release back to planning with the round's or the review's report (ReplanRelease), so a candidate that needs a
// change of its own is planned again instead of resumed as it was. Any other stopped release is a RefusalError, and only
// ReleaseEpic, or a planner's resume_release action, resumes it.
func retryStoppedRelease(ctx context.Context, queries *sqlcgen.Queries, head sqlcgen.Epic) error {
	epic := head.ID
	refusal := RefusalError(fmt.Sprintf(
		"epic %s is stopped; retry ends a stopped release only when its release integrator or reviewer stopped it",
		epic))
	parked, err := parkedReleaseRun(ctx, queries, epic)
	var refused RefusalError
	if errors.As(err, &refused) {
		return refusal
	}
	if err != nil {
		return err
	}
	runs, err := readReleaseRuns(ctx, queries, epic)
	if err != nil {
		return err
	}
	stop, ok := replanOfStop(runs, parked.Attempt)
	if !ok {
		return refusal
	}
	if recordErr := recordReplan(ctx, queries, epic, stop); recordErr != nil {
		return recordErr
	}
	if _, unparkErr := queries.UnparkEpicNodeRun(ctx, sqlcgen.UnparkEpicNodeRunParams{
		Epic: epic, Path: parked.Path, Attempt: parked.Attempt,
	}); unparkErr != nil {
		return fmt.Errorf("turn node run %s of epic %s running: %w", parked.Path, epic, unparkErr)
	}
	if closeErr := closeParkQuestions(ctx, queries, epic); closeErr != nil {
		return closeErr
	}
	return endPark(ctx, queries, epic)
}

// replanOfStop returns the release run a retry replans the release stopped at attempt from, as runs hold it: the round of
// the release integrator that returned blocked at attempt, else a review at attempt that refused the candidate, with the
// candidate it refused as its start. It reports false when runs hold neither.
func replanOfStop(runs []ReleaseRun, attempt int64) (ReleaseRun, bool) {
	blocked := slices.IndexFunc(runs, func(run ReleaseRun) bool {
		return run.Part == ReleasePartIntegrator && run.Attempt == attempt && !run.Passed &&
			run.End == integratorReturnedEnd
	})
	if blocked >= 0 {
		return runs[blocked], true
	}
	refused := slices.IndexFunc(runs, func(run ReleaseRun) bool {
		return run.Part == ReleasePartReview && run.Attempt == attempt && !run.Passed
	})
	if refused < 0 {
		return ReleaseRun{}, false
	}
	review := runs[refused]
	if review.Start == "" {
		review.Start = review.Head
	}
	return review, true
}
