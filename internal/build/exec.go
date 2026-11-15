package build

import (
	"context"
	"errors"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/store"
)

// execute carries out action for the task of view in epic, and reports whether the store or a start changed. A write
// or start that fails is logged and reports false, so the task waits for the next pass.
func (r *Runner) execute(ctx context.Context, epic store.EpicRow, view View, action Action) bool {
	seq, from := view.Task.Seq, view.Task.Build
	switch action.Kind {
	case ActNone:
		return false
	case ActMove:
		return r.written(ctx, epic.ID, seq, "move build", r.store.SetTaskBuild(ctx, epic.ID, seq, from, action.Next))
	case ActSnapshot:
		return r.snapshotRefs(ctx, epic, view)
	case ActRefs:
		return r.checkRefs(ctx, epic, view, action)
	case ActStartRun:
		return r.startRun(ctx, epic, view, action)
	case ActStartStep:
		return r.startUnit(ctx, epic, view, action)
	case ActFail:
		return r.fail(ctx, epic, view, action.Reason)
	case ActAccept:
		return r.accept(ctx, epic, view, action)
	case ActPause:
		err := r.store.StopTask(ctx, epic.ID, seq, from, action.Next)
		return r.written(ctx, epic.ID, seq, "pause task", err)
	case ActPark:
		err := r.store.ParkTask(ctx, epic.ID, seq, from, action.Next, action.Reason, r.now())
		return r.written(ctx, epic.ID, seq, "park task", err)
	case ActReview:
		err := r.store.AddTaskReview(ctx, epic.ID, seq, from, action.Next, action.Review)
		return r.written(ctx, epic.ID, seq, "store review", err)
	case ActFinding:
		err := r.store.SetFindingResult(ctx, epic.ID, seq, from, action.Next, action.Finding)
		return r.written(ctx, epic.ID, seq, "store finding", err)
	}
	return false
}

// snapshotRefs stores the refs snapshot the implementer's step is checked against.
func (r *Runner) snapshotRefs(ctx context.Context, epic store.EpicRow, view View) bool {
	seq, from := view.Task.Seq, view.Task.Build
	snap, err := snapshotOf(ctx, r.store, epic)
	if err != nil {
		r.report(ctx, epic.ID, seq, "snapshot refs", err)
		return false
	}
	next := from
	next.Refs = snap
	return r.written(ctx, epic.ID, seq, "store refs snapshot", r.store.SetTaskBuild(ctx, epic.ID, seq, from, next))
}

// checkRefs stores the implementer's ref moves as the build no longer checks them, in one write with the tells of the
// moves that do not park, so the owner is told of each move the build stored. A move that parks stores the build and
// parks the epic with the reason, and the other tells ride that park.
func (r *Runner) checkRefs(ctx context.Context, epic store.EpicRow, view View, action Action) bool {
	seq, from := view.Task.Seq, view.Task.Build
	var err error
	if action.Reason != "" {
		err = r.store.ParkTask(ctx, epic.ID, seq, from, action.Next, action.Reason, r.now(), action.Tells...)
	} else {
		err = r.store.SetTaskBuildTelling(ctx, epic.ID, seq, from, action.Next, action.Tells, r.now())
	}
	return r.written(ctx, epic.ID, seq, "check ref moves", err)
}

// startRun starts the evidence run action names. A run the evidence refuses is handled as DecideStartError decides.
func (r *Runner) startRun(ctx context.Context, epic store.EpicRow, view View, action Action) bool {
	seq := view.Task.Seq
	_, err := r.evidence.RunTask(ctx, action.ID, epic.ID, seq, action.Revision, action.Phase)
	if errors.Is(err, enginehost.ErrEvidenceRefused) {
		return r.execute(ctx, epic, view, DecideStartError(view, err))
	}
	return r.written(ctx, epic.ID, seq, "start evidence run", err)
}

// startUnit starts the step action names and records its id as the build's step. A start the cap refuses changes
// nothing, so the next pass retries it. A role error parks the task held parked. Any other start error is handled as
// DecideStartError decides.
func (r *Runner) startUnit(ctx context.Context, epic store.EpicRow, view View, action Action) bool {
	seq, from := view.Task.Seq, view.Task.Build
	id, err := r.startStep(ctx, epic, view, action)
	var roleErr RoleError
	switch {
	case err == nil:
		next := from
		next.Step = id
		return r.written(ctx, epic.ID, seq, "record step", r.store.SetTaskBuild(ctx, epic.ID, seq, from, next))
	case errors.Is(err, enginehost.ErrCapReached):
		return false
	case errors.As(err, &roleErr):
		next := from
		next.Held = HeldParked
		park := r.store.ParkTask(ctx, epic.ID, seq, from, next, err.Error(), r.now())
		return r.written(ctx, epic.ID, seq, "park on role error", park)
	default:
		return r.execute(ctx, epic, view, DecideStartError(view, err))
	}
}

// fail fails the task with reason, then removes its worktrees.
func (r *Runner) fail(ctx context.Context, epic store.EpicRow, view View, reason string) bool {
	seq := view.Task.Seq
	err := r.store.FailTask(ctx, epic.ID, seq, view.Task.Build, reason, r.now())
	if !r.written(ctx, epic.ID, seq, "fail task", err) {
		return false
	}
	r.removeWorktrees(ctx, epic, seq)
	return true
}

// accept pins the task's revision under refs/orrery, accepts the task with its review, then removes its worktrees.
func (r *Runner) accept(ctx context.Context, epic store.EpicRow, view View, action Action) bool {
	seq, from := view.Task.Seq, view.Task.Build
	ref := refsOrreryPrefix + Slug(epic.ID) + "/" + taskLabel(seq)
	if err := r.git.Pin(ctx, epic.Repository, ref, from.Revision); err != nil {
		r.report(ctx, epic.ID, seq, "pin accepted task", err)
		return false
	}
	err := r.store.AcceptTask(ctx, epic.ID, seq, from, action.Reason)
	if !r.written(ctx, epic.ID, seq, "accept task", err) {
		return false
	}
	r.removeWorktrees(ctx, epic, seq)
	return true
}

// removeWorktrees removes the worktrees of task seq. The task's state is stored before this runs, so a failure is
// logged and the task stays as it is.
func (r *Runner) removeWorktrees(ctx context.Context, epic store.EpicRow, seq int64) {
	err := r.git.RemoveWorktrees(ctx, epic.Repository, TaskDir(r.stateDir, epic.ID, seq))
	r.report(ctx, epic.ID, seq, "remove task worktrees", err)
}

// report logs err, the failure of action for task seq of epic.
func (r *Runner) report(ctx context.Context, epic string, seq int64, action string, err error) {
	if err != nil {
		r.logger.ErrorContext(ctx, "build action failed", "action", action, "epic", epic, "task", seq, "err", err)
	}
}

// written reports whether a write that returned err changed the store, and logs its failure otherwise.
func (r *Runner) written(ctx context.Context, epic string, seq int64, action string, err error) bool {
	r.report(ctx, epic, seq, action, err)
	return err == nil
}
