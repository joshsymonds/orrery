package interpret

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/store"
)

// release carries out one work action of a release run: the prepare of its candidate, the release slot taken or freed,
// a stop, the epic's ending, a Done gate check or a capture at the candidate, the start of a round of the release
// integrator, of a release step, of a release reviewer or of the judge, the record of a candidate, a round of the release
// integrator, a release step, a release reviewer or the judge, or the replan that sends the release back to planning and
// the resume once that plan has ended.
func (r *Runner) release(ctx context.Context, epic store.EpicRow, act Action, at time.Time) error {
	switch act.Work {
	case kinds.ActPrepare:
		return r.prepareRelease(ctx, epic, act)
	case kinds.ActTake:
		return r.takeSlot(ctx, epic, at)
	case kinds.ActFree:
		return r.freeSlot(ctx, epic)
	case kinds.ActStop:
		return r.stopRelease(ctx, epic, act, at)
	case kinds.ActEnding:
		return r.endEpic(ctx, epic, act, at)
	case kinds.ActStart:
		return r.start(ctx, epic, act)
	case kinds.ActCapture:
		return r.capture(ctx, epic, act)
	case kinds.ActIntegrator:
		return r.startIntegrator(ctx, epic, act, at)
	case kinds.ActStep:
		return r.startReleaseWork(ctx, epic, act, at)
	case kinds.ActJudge:
		return r.startJudge(ctx, epic, act, at)
	case kinds.ActRecord:
		return r.recordRelease(ctx, epic, act, at)
	case kinds.ActReplan:
		return r.replanRelease(ctx, epic, act, at)
	case kinds.ActResume:
		return r.resumeReplanned(ctx, epic, act, at)
	default:
		return releaseRefused(act)
	}
}

// recordRelease records the run act names: the candidate, the judge, a round of the release integrator, a release
// reviewer's review, or else a release step, which its label names.
func (r *Runner) recordRelease(ctx context.Context, epic store.EpicRow, act Action, at time.Time) error {
	if len(act.Items) == 0 {
		return releaseRefused(act)
	}
	switch act.Items[0] {
	case store.ReleasePartCandidate:
		return r.recordCandidate(ctx, epic, act, at)
	case store.ReleasePartJudge:
		return r.recordJudge(ctx, epic, act, at)
	case store.ReleasePartIntegrator:
		return r.recordIntegrator(ctx, epic, act, at)
	case store.ReleasePartReview:
		return r.recordReleaseReview(ctx, epic, act, at)
	default:
		return r.recordReleaseStep(ctx, epic, act, at)
	}
}

// releaseReviewItems is the number of items a start or record of a release review carries: its part, its candidate, its
// reviewer and its position.
const releaseReviewItems = 4

// releaseStoppedEnd is the end a step stopped by a pause stores, as the engine's Stop ends it.
const releaseStoppedEnd = "stopped"

// startReleaseWork starts the work act names as a release step: a release reviewer's review when act's first item names
// one, and the release step otherwise.
func (r *Runner) startReleaseWork(ctx context.Context, epic store.EpicRow, act Action, at time.Time) error {
	if len(act.Items) > 0 && act.Items[0] == store.ReleasePartReview {
		return r.startReleaseReview(ctx, epic, act, at)
	}
	return r.startReleaseStep(ctx, epic, act, at)
}

// releaseReviewOf returns the review run act names: its part, position, attempt, candidate and reviewer, and the time at
// which it is recorded. Its outcome is left to the caller.
func releaseReviewOf(act Action, at time.Time) (store.ReleaseRun, error) {
	if len(act.Items) != releaseReviewItems {
		return store.ReleaseRun{}, releaseRefused(act)
	}
	position, err := strconv.ParseInt(act.Items[3], 10, 64)
	if err != nil {
		return store.ReleaseRun{}, fmt.Errorf("read the position of release review %s: %w", act.Items[2], err)
	}
	return store.ReleaseRun{
		Part: store.ReleasePartReview, Position: position, Attempt: act.Run.Attempt, Head: act.Items[1],
		Step: act.Items[2], At: at,
	}, nil
}

// startReleaseReview starts the release reviewer act names, read-only at the candidate act names, over the change from the
// epic's change base. A reviewer or start refused for good records the review failed at once, and the release stops at it
// on its next decision.
func (r *Runner) startReleaseReview(ctx context.Context, epic store.EpicRow, act Action, at time.Time) error {
	run, err := releaseReviewOf(act, at)
	if err != nil {
		return err
	}
	id := act.IDs[0]
	start, err := r.reviewers.Start(ctx, epic, run.Step, changeBase(epic), run.Head, nil, nil)
	if err != nil {
		if !refusedForGood(err) {
			return fmt.Errorf("start release reviewer %s of epic %s: %w", run.Step, epic.ID, err)
		}
		return r.failReleaseReview(ctx, epic, run, err)
	}
	// The step's task is the release run's path. The rows of a release read a step under that path, which is how they
	// read a review's steps, since the review's label is none the release reads by name.
	start.ID, start.Epic, start.Task = id, epic.ID, act.Path
	if _, err = r.steps.Start(ctx, start); err != nil {
		if !refusedForGood(err) {
			return fmt.Errorf("start step %s: %w", id, err)
		}
		return r.failReleaseReview(ctx, epic, run, err)
	}
	return nil
}

// failReleaseReview records run, a release review that never started, as failed with the reason it did not start.
func (r *Runner) failReleaseReview(ctx context.Context, epic store.EpicRow, run store.ReleaseRun, cause error) error {
	run.Reason = "the reviewer did not start: " + cause.Error()
	if err := r.store.RecordRelease(ctx, epic.ID, run); err != nil {
		return fmt.Errorf("record release reviewer %s of epic %s: %w", run.Step, epic.ID, err)
	}
	return nil
}

// recordReleaseReview records the release reviewer act names once its step's end is stored. Its review is recorded failed
// with its reason when releaseReviewVerdict refuses the candidate, and otherwise passed, with the reason its end gives.
func (r *Runner) recordReleaseReview(ctx context.Context, epic store.EpicRow, act Action, at time.Time) error {
	run, err := releaseReviewOf(act, at)
	if err != nil {
		return err
	}
	row, err := r.storedStep(ctx, epic.ID, act.IDs[0])
	if err != nil {
		return err
	}
	refused, reason := releaseReviewVerdict(row)
	run.End, run.Reason, run.Passed = row.End, reason, !refused
	if err = r.store.RecordRelease(ctx, epic.ID, run); err != nil {
		return fmt.Errorf("record release reviewer %s of epic %s: %w", run.Step, epic.ID, err)
	}
	return nil
}

// releaseReviewVerdict returns whether the release reviewer whose step is row refuses its candidate, and the reason its
// review is recorded with, which is "" when the step ended done. A step that failed with findings and no undetermined
// assertion refuses it, named by each finding's claim. A step stopped by a pause refuses it as well, so the release stops
// at its review. Any other end holds the candidate, and is recorded with the reason releaseEndReason names it.
func releaseReviewVerdict(row store.StepRow) (bool, string) {
	if row.End == releaseDone {
		return false, ""
	}
	if claims := kinds.ReleaseRefusalClaims(row); len(claims) > 0 {
		return true, "the release reviewer refused the candidate: " + strings.Join(claims, "; ")
	}
	return row.End == releaseStoppedEnd, releaseEndReason("review", row)
}

// takeSlot takes the release slot of epic's repository for epic. When another epic holds the slot nothing changes, and
// the holder shows in the epic's rows.
func (r *Runner) takeSlot(ctx context.Context, epic store.EpicRow, at time.Time) error {
	if _, err := r.store.TakeReleaseSlot(ctx, epic.ID, at); err != nil {
		return fmt.Errorf("take release slot of epic %s: %w", epic.ID, err)
	}
	return nil
}

// freeSlot frees the release slot epic holds.
func (r *Runner) freeSlot(ctx context.Context, epic store.EpicRow) error {
	if err := r.store.FreeReleaseSlot(ctx, epic.ID); err != nil {
		return fmt.Errorf("free release slot of epic %s: %w", epic.ID, err)
	}
	return nil
}

// stopRelease stops the release at the run of act, with the action's output as its reason. The run's release
// integrator worktree, when it has one, is removed first, so a stop never leaves it behind.
func (r *Runner) stopRelease(ctx context.Context, epic store.EpicRow, act Action, at time.Time) error {
	if err := r.removeIntegrator(ctx, epic, act.Run); err != nil {
		return err
	}
	if err := r.store.StopRelease(ctx, epic.ID, act.Path, act.Run.Attempt, act.Output, at); err != nil {
		return fmt.Errorf("stop release of epic %s: %w", epic.ID, err)
	}
	return nil
}

// endEpic ends epic through the Ending, to the phase act names, with the body act's output gives as its tell. An epic
// with no Ending configured is an error.
func (r *Runner) endEpic(ctx context.Context, epic store.EpicRow, act Action, at time.Time) error {
	if r.ending == nil {
		return errors.New("no Ending configured")
	}
	if err := r.ending.EndEpic(ctx, epic.ID, act.Outcome, act.Output, nil, at); err != nil {
		return fmt.Errorf("end epic %s: %w", epic.ID, err)
	}
	return nil
}

// recordCandidate stores the candidate of the run's attempt as passed, after the one it replaces when it was prepared
// again. Its Start is the tip the candidate was made from and its Head the candidate, which is the merge the release
// integrator committed when the action names that revision. Its Commits name the candidate when it is behind the
// target, since then the candidate is the merge the release made. The run's release integrator worktree, when it has
// one, is removed first: the merge it holds is pinned.
func (r *Runner) recordCandidate(ctx context.Context, epic store.EpicRow, act Action, at time.Time) error {
	result, err := kinds.ParseReleaseResult(act.Run.Result)
	if err != nil {
		return fmt.Errorf("read result of node %s: %w", act.Path, err)
	}
	if removeErr := r.removeIntegrator(ctx, epic, act.Run); removeErr != nil {
		return removeErr
	}
	run := store.ReleaseRun{
		Part: store.ReleasePartCandidate, Attempt: act.Run.Attempt, Start: result.Tip, Head: act.Run.Revision,
		Passed: true, At: at,
	}
	if result.Behind {
		run.Commits = []string{act.Run.Revision}
	}
	if err = r.store.RecordCandidate(ctx, epic.ID, run); err != nil {
		return fmt.Errorf("record candidate of epic %s: %w", epic.ID, err)
	}
	return nil
}

// verifyCandidate checks that origin's current main is in candidate head, the candidate that the release run act names
// reuses from an earlier attempt, before the run's first step starts from it. It returns true when origin's main is in
// it. When it is not, the candidate is marked stale at act's attempt, so the run prepares a new candidate, and it returns
// false. A check that cannot be made stops the release with its error, and returns false.
func (r *Runner) verifyCandidate(
	ctx context.Context, epic store.EpicRow, act Action, head string, at time.Time,
) (bool, error) {
	contained, err := candidateContainsOrigin(ctx, epic, head)
	if err != nil {
		reason := fmt.Sprintf("candidate %s could not be checked against origin's main: %v", head, err)
		if stopErr := r.store.StopRelease(ctx, epic.ID, act.Path, act.Run.Attempt, reason, at); stopErr != nil {
			return false, fmt.Errorf("stop release of epic %s: %w", epic.ID, stopErr)
		}
		return false, nil
	}
	if contained {
		return true, nil
	}
	reason := fmt.Sprintf("origin's main is not in candidate %s, so the candidate is prepared again", head)
	if err = r.store.MarkCandidateStale(ctx, epic.ID, act.Path, act.Run.Attempt, head, reason, at); err != nil {
		return false, fmt.Errorf("mark candidate %s of epic %s stale: %w", head, epic.ID, err)
	}
	return false, nil
}

// releaseRefused is the error of release work the executor does not carry out.
func releaseRefused(act Action) error {
	return fmt.Errorf("release work %s is not one the executor carries out", act.Work)
}
