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
// as its reason. That holds for a round blocked from a start an earlier replan already named too: no rule stops a
// repeat, and the planner judges what the change is.
//
// reviews are what a passed round settles for the release reviewers of the candidate it made, stored in the same write
// as RecordRelease stores each: a carried review, of part review, passed, with the end ReleaseCarriedEnd, for each
// reviewer whose earlier verdict still stands, and a rereview run for each reviewer that reviews the candidate again
// with the change since the candidate it last reviewed. A review of any other kind, and any review of a round that did
// not pass, is an error, and the write stores nothing.
func (s *Store) RecordIntegratorRound(
	ctx context.Context, epic string, round ReleaseRun, replan bool, reviews []ReleaseRun,
) error {
	for _, review := range reviews {
		carried := review.Part == ReleasePartReview && review.Passed && review.End == ReleaseCarriedEnd
		if !round.Passed || (!carried && review.Part != ReleasePartRereview) {
			return fmt.Errorf("release integrator round of epic %s: %s run of %s is not a review it can settle",
				epic, review.Part, review.Step)
		}
	}
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := recordRelease(ctx, queries, epic, round); err != nil {
			return err
		}
		for _, review := range reviews {
			if err := recordRelease(ctx, queries, epic, review); err != nil {
				return err
			}
		}
		if !replan {
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

// retryStoppedRelease ends the stop of head's release for a retry, in the caller's write, whatever stopped the release: the
// release node run parked at the stop is replanned (replanStoppedRelease). head stays stopped until the release kind's next
// decision sends the release back to planning with the stop's reason (ReplanRelease), so a candidate that needs a change of
// its own is planned again instead of resumed as it was. An epic with no parked release run is a RefusalError. Only
// ReleaseEpic, or a planner's resume_release action that resumeReleaseByPlanner allows, resumes a stopped release as it was.
func retryStoppedRelease(ctx context.Context, queries *sqlcgen.Queries, head sqlcgen.Epic, at time.Time) error {
	parked, err := parkedReleaseRun(ctx, queries, head.ID)
	if err != nil {
		return err
	}
	return replanStoppedRelease(ctx, queries, head.ID, parked, at)
}

// replanStoppedRelease sends the stopped release of epic back to planning for the stop its parked release node run holds,
// in the caller's write. It stores a replan release run of the run's attempt, from the release tip the stop came from
// (stopStart), with the run's output as its reason, which is the reason the release stopped with. It turns the run running
// at the same attempt, its outcome and output cleared, answers each open park question retry, and ends the park, as
// RetryEpic does. The release kind's next decision sends the release back to planning with that reason (kinds.replanAction).
func replanStoppedRelease(
	ctx context.Context, queries *sqlcgen.Queries, epic string, parked NodeRun, at time.Time,
) error {
	runs, err := readReleaseRuns(ctx, queries, epic)
	if err != nil {
		return err
	}
	if recordErr := recordRelease(ctx, queries, epic, ReleaseRun{
		Part: ReleasePartReplan, Attempt: parked.Attempt, Step: replanStepName, Start: stopStart(runs, parked.Attempt),
		Reason: parked.Output, At: at,
	}); recordErr != nil {
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

// replanStoppedEpic replans the stopped release of epic in the caller's write, as retryStoppedRelease does, when epic is
// in phase stopped and holds a parked release run. An epic that is not stopped, or holds no parked release run, changes
// nothing.
func replanStoppedEpic(ctx context.Context, queries *sqlcgen.Queries, epic string, at time.Time) error {
	head, err := epicHead(ctx, queries, epic)
	if err != nil {
		return err
	}
	release, stopped, err := parkedStoppedRelease(ctx, queries, head)
	if err != nil || !stopped {
		return err
	}
	return replanStoppedRelease(ctx, queries, epic, release, at)
}

// stopStart returns the release tip a stop at attempt came from, as the replan of the stop records it: the start of the
// blocked round of the release integrator at attempt, else the candidate a release reviewer refused at attempt, which is its
// start or, when it has none, its head. It returns "" when runs hold neither at attempt.
func stopStart(runs []ReleaseRun, attempt int64) string {
	blocked := slices.IndexFunc(runs, func(run ReleaseRun) bool {
		return run.Part == ReleasePartIntegrator && run.Attempt == attempt && !run.Passed &&
			run.End == integratorReturnedEnd
	})
	if blocked >= 0 {
		return runs[blocked].Start
	}
	refused := slices.IndexFunc(runs, func(run ReleaseRun) bool {
		return run.Part == ReleasePartReview && run.Attempt == attempt && !run.Passed
	})
	if refused < 0 {
		return ""
	}
	if runs[refused].Start != "" {
		return runs[refused].Start
	}
	return runs[refused].Head
}

// parkedStoppedRelease returns the parked release node run of head and true when head is in phase stopped and holds one.
// It returns false, with no error, for an epic that is not stopped or holds no parked release run.
func parkedStoppedRelease(ctx context.Context, queries *sqlcgen.Queries, head sqlcgen.Epic) (NodeRun, bool, error) {
	if head.Phase != PhaseStopped {
		return NodeRun{}, false, nil
	}
	parked, err := parkedReleaseRun(ctx, queries, head.ID)
	var refusal RefusalError
	if errors.As(err, &refusal) {
		return NodeRun{}, false, nil
	}
	if err != nil {
		return NodeRun{}, false, err
	}
	return parked, true, nil
}

// ownerStoppedRelease reports whether head is stopped at a release its owner stopped: the attempt of its parked release node
// run holds the owner's stop (ownerStoppedAt). An epic with no parked release run is not.
func ownerStoppedRelease(ctx context.Context, queries *sqlcgen.Queries, head sqlcgen.Epic) (bool, error) {
	parked, stopped, err := parkedStoppedRelease(ctx, queries, head)
	if err != nil || !stopped {
		return false, err
	}
	runs, err := readReleaseRuns(ctx, queries, head.ID)
	if err != nil {
		return false, err
	}
	return ownerStoppedAt(runs, parked.Attempt), nil
}
