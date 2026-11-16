package interpret

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/store"
)

// release carries out one work action of a release run: the prepare of its candidate, the release slot taken or freed, a
// stop, the epic's ending, a Done gate check at the candidate, or the record of a candidate. The release steps and the
// judge are not carried out yet, so they are refused.
func (r *Runner) release(ctx context.Context, epic store.EpicRow, act Action, at time.Time) error {
	switch act.Work {
	case kinds.ActPrepare:
		return r.prepareRelease(ctx, epic, act)
	case kinds.ActTake:
		return r.takeSlot(ctx, epic)
	case kinds.ActFree:
		return r.freeSlot(ctx, epic)
	case kinds.ActStop:
		return r.stopRelease(ctx, epic, act, at)
	case kinds.ActEnding:
		return r.endEpic(ctx, epic, act, at)
	case kinds.ActStart:
		return r.start(ctx, epic, act)
	case kinds.ActRecord:
		if len(act.Items) > 0 && act.Items[0] == store.ReleasePartCandidate {
			return r.recordCandidate(ctx, epic, act, at)
		}
		return releaseRefused(act)
	default:
		return releaseRefused(act)
	}
}

// takeSlot takes the release slot of epic's repository for epic. When another epic holds the slot nothing changes, and
// the holder shows in the epic's rows.
func (r *Runner) takeSlot(ctx context.Context, epic store.EpicRow) error {
	if _, err := r.store.TakeReleaseSlot(ctx, epic.ID); err != nil {
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

// stopRelease stops the release at the run of act, with the action's output as its reason.
func (r *Runner) stopRelease(ctx context.Context, epic store.EpicRow, act Action, at time.Time) error {
	if err := r.store.StopRelease(ctx, epic.ID, act.Path, act.Run.Attempt, act.Output, at); err != nil {
		return fmt.Errorf("stop release of epic %s: %w", epic.ID, err)
	}
	return nil
}

// endEpic ends epic through the Ending, to the phase act names, with a body that names the epic and its ending. An epic
// with no Ending configured is an error.
func (r *Runner) endEpic(ctx context.Context, epic store.EpicRow, act Action, at time.Time) error {
	if r.ending == nil {
		return errors.New("no Ending configured")
	}
	body := fmt.Sprintf("Epic %s ended %s.", epic.ID, act.Outcome)
	if err := r.ending.EndEpic(ctx, epic.ID, act.Outcome, body, at); err != nil {
		return fmt.Errorf("end epic %s: %w", epic.ID, err)
	}
	return nil
}

// recordCandidate stores the candidate of the run's attempt as passed. Its Start is the tip the candidate was made from
// and its Head the candidate. Its Commits name the candidate when it is behind the target, since then the candidate is
// the merge the release made.
func (r *Runner) recordCandidate(ctx context.Context, epic store.EpicRow, act Action, at time.Time) error {
	result, err := kinds.ParseReleaseResult(act.Run.Result)
	if err != nil {
		return fmt.Errorf("read result of node %s: %w", act.Path, err)
	}
	run := store.ReleaseRun{
		Part: store.ReleasePartCandidate, Attempt: act.Run.Attempt, Start: result.Tip, Head: act.Run.Revision,
		Passed: true, At: at,
	}
	if result.Behind {
		run.Commits = []string{act.Run.Revision}
	}
	if err = r.store.RecordRelease(ctx, epic.ID, run); err != nil {
		return fmt.Errorf("record candidate of epic %s: %w", epic.ID, err)
	}
	return nil
}

// releaseRefused is the error of release work the executor does not carry out.
func releaseRefused(act Action) error {
	return fmt.Errorf("release work %s is not one the executor carries out", act.Work)
}
