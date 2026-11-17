package build

import (
	"context"
	"errors"
	"fmt"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/store"
)

// execute carries out action for the task of view in epic, and reports whether the store or a start changed. A write
// or start that fails is logged and reports false, so the task waits for the next pass.
func (r *Runner) execute(ctx context.Context, epic store.EpicRow, view View, action Action) bool {
	switch action.Kind {
	case ActNone:
		return false
	case ActGuard:
		return r.guardCommit(ctx, epic, view, action)
	case ActSnapshot:
		return r.snapshotRefs(ctx, epic, view)
	case ActRefs:
		return r.checkRefs(ctx, epic, view, action)
	case ActStartRun:
		return r.startRun(ctx, epic, view, action)
	case ActStartStep:
		return r.startUnit(ctx, epic, view, action)
	case ActRepro:
		return r.reproduce(ctx, epic, view, action)
	case ActFail:
		return r.fail(ctx, epic, view, action)
	case ActGrant:
		return r.grant(ctx, epic, view, action)
	case ActDecide:
		return r.decide(ctx, epic, view, action)
	case ActAsk:
		return r.ask(ctx, epic, view, action)
	case ActResume:
		return r.resume(ctx, epic, view, action)
	case ActAccept:
		return r.accept(ctx, epic, view, action)
	default:
		return r.record(ctx, epic, view, action)
	}
}

// record carries out an action that is one store write of the task's build: a move, a pause, a park, a review or a
// finding. Any other action does nothing and reports false.
func (r *Runner) record(ctx context.Context, epic store.EpicRow, view View, action Action) bool {
	seq, from := view.Task.Seq, view.Task.Build
	switch action.Kind {
	case ActMove:
		return r.written(ctx, epic.ID, seq, "move build", r.store.SetTaskBuild(ctx, epic.ID, seq, from, action.Next))
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
	default:
		return false
	}
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

// startUnit starts the step action names and records its id as the build's step. An orchestrate unit records nothing,
// since its build's step is the implementer step it decides, and its own step is found by its derived id. A start the
// cap refuses changes nothing, so the next pass retries it. A role error parks the task held parked. Any other start
// error is handled as DecideStartError decides.
func (r *Runner) startUnit(ctx context.Context, epic store.EpicRow, view View, action Action) bool {
	seq, from := view.Task.Seq, view.Task.Build
	id, err := r.startStep(ctx, epic, view, action)
	var roleErr RoleError
	switch {
	case err == nil && action.Unit == StageOrchestrate:
		return true
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

// fail fails the task with action's reason and the files action names as needed, then removes its worktrees. A fail an
// orchestrator decided records its decision in the same write, and keeps a longer reason (store.FailTaskDecided).
func (r *Runner) fail(ctx context.Context, epic store.EpicRow, view View, action Action) bool {
	seq := view.Task.Seq
	var err error
	if action.Decision != nil {
		err = r.store.FailTaskDecided(
			ctx, epic.ID, seq, view.Task.Build, action.Reason, action.NeedsFiles, *action.Decision, r.now(),
		)
	} else {
		err = r.store.FailTask(ctx, epic.ID, seq, view.Task.Build, action.Reason, action.NeedsFiles, r.now())
	}
	if !r.written(ctx, epic.ID, seq, "fail task", err) {
		return false
	}
	r.removeWorktrees(ctx, epic, seq)
	return true
}

// grant gives the task the files action names and stores action's build, which runs its implementer again in the same
// worktree. When another open task owns one of the files, the store writes nothing, and the return is a request the
// grant could not settle: it goes to the orchestrator (request), and fails the task with action's reason and the
// refusal, carrying the files it named, when the task has no decisions left.
func (r *Runner) grant(ctx context.Context, epic store.EpicRow, view View, action Action) bool {
	seq := view.Task.Seq
	err := r.store.GrantTaskFiles(ctx, epic.ID, seq, view.Task.Build, action.Next, action.NeedsFiles)
	var owned store.FileOwnedError
	if errors.As(err, &owned) {
		reason := action.Reason + "; " + owned.Error()
		return r.execute(
			ctx,
			epic,
			view,
			request(view, Action{Kind: ActFail, Reason: reason, NeedsFiles: action.NeedsFiles}),
		)
	}
	return r.written(ctx, epic.ID, seq, "grant files", err)
}

// decide carries out an orchestrator's rerun or grant: in one write the task gets the files action names, its build
// moves to action's, and the decision is recorded (store.Store.RerunTask). When an open task the build does not wait
// for owns one of the files, the store writes nothing, and the request is settled as action's fallback, with the
// refusal added.
func (r *Runner) decide(ctx context.Context, epic store.EpicRow, view View, action Action) bool {
	seq := view.Task.Seq
	err := r.store.RerunTask(
		ctx, epic.ID, seq, view.Task.Build, action.Next, action.NeedsFiles, action.Behind, *action.Decision, r.now(),
	)
	var owned store.FileOwnedError
	if errors.As(err, &owned) && action.Fallback != nil {
		return r.execute(ctx, epic, view, refused(*action.Fallback, decisionGrant, []string{owned.Error()}))
	}
	return r.written(ctx, epic.ID, seq, "carry out decision", err)
}

// ask opens the owner question an orchestrator's ask names, with the tell that carries it, holds the build on it and
// records the decision, in one write (store.Store.AskTask). A question the store refuses is settled as action's
// fallback, with the refusal added.
func (r *Runner) ask(ctx context.Context, epic store.EpicRow, view View, action Action) bool {
	seq := view.Task.Seq
	err := r.store.AskTask(
		ctx, epic.ID, seq, view.Task.Build, action.Next, *action.Question, action.Reason, *action.Decision, r.now(),
	)
	if errors.Is(err, store.ErrBadQuestion) && action.Fallback != nil {
		return r.execute(ctx, epic, view, refused(*action.Fallback, decisionAsk, []string{err.Error()}))
	}
	return r.written(ctx, epic.ID, seq, "ask owner", err)
}

// resume moves a waiting build's implement worktree from its base onto action's revision, keeping its work
// (MoveWorktree), then stores action's build. A move git refuses fails the task with
// `T<seq> could not move onto the new base <base>: <error>`.
func (r *Runner) resume(ctx context.Context, epic store.EpicRow, view View, action Action) bool {
	seq, from := view.Task.Seq, view.Task.Build
	path := ImplementWorktree(r.stateDir, epic.ID, seq)
	if err := r.git.MoveWorktree(ctx, path, from.Base, action.Revision); err != nil {
		reason := fmt.Sprintf("%s could not move onto the new base %s: %v", taskLabel(seq), action.Revision, err)
		return r.fail(ctx, epic, view, Action{Kind: ActFail, Reason: reason})
	}
	return r.written(ctx, epic.ID, seq, "resume build", r.store.SetTaskBuild(ctx, epic.ID, seq, from, action.Next))
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
