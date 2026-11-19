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

// record carries out an action that is one store write of the task's build: a move, a settle of a checking version, a
// pause, a park, a review or a finding. Any other action does nothing and reports false.
func (r *Runner) record(ctx context.Context, epic store.EpicRow, view View, action Action) bool {
	seq, from := view.Task.Seq, view.Task.Build
	switch action.Kind {
	case ActMove:
		if action.Notes != nil {
			err := r.store.SetTaskBuildNotes(ctx, epic.ID, seq, from, action.Next, action.Notes, action.Tells, r.now())
			return r.written(ctx, epic.ID, seq, "move build with notes", err)
		}
		return r.written(ctx, epic.ID, seq, "move build", r.store.SetTaskBuild(ctx, epic.ID, seq, from, action.Next))
	case ActSettleVersion:
		err := r.store.SettleTaskVersion(ctx, epic.ID, seq, from, action.Next, *action.Settle, r.now())
		return r.written(ctx, epic.ID, seq, "settle version", err)
	case ActPause:
		err := r.store.StopTask(ctx, epic.ID, seq, from, action.Next)
		return r.written(ctx, epic.ID, seq, "pause task", err)
	case ActPark:
		err := r.store.ParkTask(ctx, epic.ID, seq, from, action.Next, store.ParkClassTask, action.Reason, r.now())
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
		err = r.store.ParkTask(
			ctx, epic.ID, seq, from, action.Next, store.ParkClassRefMove, action.Reason, r.now(), action.Tells...,
		)
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
		park := r.store.ParkTask(ctx, epic.ID, seq, from, next, store.ParkClassTask, err.Error(), r.now())
		return r.written(ctx, epic.ID, seq, "park on role error", park)
	default:
		return r.execute(ctx, epic, view, DecideStartError(view, err))
	}
}

// fail climbs the task one rung off action's failure, in one write (store.Store.ClimbTask): the rung it climbs to is
// action's Move.To, or the rung after the task's own. The failure is the cause the climb records, the files action
// names are the task's needs files, and a fail an orchestrator decided records its decision in the same write, with a
// longer cause. A climb below the gap rung carries the task's kept work onto the base it lands on (carry) before the
// climb is written, so no rung starts from a base without the work. The climb to the gap rung carries nothing, and
// removes the task's worktrees. Any other climb keeps them, so the next rung's implementer continues in the same
// worktree.
func (r *Runner) fail(ctx context.Context, epic store.EpicRow, view View, action Action) bool {
	seq, from := view.Task.Seq, view.Task.Build
	target := action.Move.To
	if target == 0 {
		target = store.RungOf(from) + 1
	}
	cause := store.ClimbCause(action.Reason, action.Decision != nil)
	next := store.ClimbedBuild(from, min(target, store.GapRung), cause)
	var version *store.TaskVersionInput
	if next.Attempt < store.GapRung {
		var carried bool
		if next, version, carried = r.carry(ctx, epic, view, next); !carried {
			return false
		}
	}
	move := action.Move
	if move.MovedBy == "" {
		move.MovedBy = store.MovedByUnit
	}
	move.Cause = cause
	move.Step = from.Step
	move.NeedsFiles = action.NeedsFiles
	err := r.store.ClimbTask(ctx, epic.ID, seq, from, next, move, version, action.Decision, r.now())
	if !r.written(ctx, epic.ID, seq, "climb task", err) {
		return false
	}
	if next.Attempt == store.GapRung {
		r.removeWorktrees(ctx, epic, seq)
	}
	return true
}

// carry carries the kept work of the task of view onto the base the climb to next lands on: the epic's candidate, or
// its stored base when it has none, as readRequest reads a waiting build's base. A base that is empty or is the task's
// own base carries nothing. When git moves the implement worktree onto that base, next's base is that base. When git
// refuses the move, the work is kept on CarryRef, and the climb keeps its base with a carry version whose deliverable
// and reason name the ref. The version is returned with next and true. A keep that fails is reported, and carry returns
// false, so nothing is written and the next pass tries again.
func (r *Runner) carry(
	ctx context.Context, epic store.EpicRow, view View, next store.TaskBuild,
) (store.TaskBuild, *store.TaskVersionInput, bool) {
	seq, from := view.Task.Seq, view.Task.Build
	newBase := epic.Candidate
	if newBase == "" {
		newBase = epic.StoredBase
	}
	if newBase == "" || newBase == from.Base {
		return next, nil, true
	}
	path, wip := ImplementWorktree(r.stateDir, epic.ID, seq), WipRef(epic.ID, seq)
	moveErr := r.git.MoveWorktree(ctx, path, wip, newBase)
	if moveErr == nil {
		next.Base = newBase
		return next, nil, true
	}
	ref := CarryRef(epic.ID, seq, next.Attempt)
	commit, keepErr := r.git.KeepWork(ctx, path, wip, ref)
	if keepErr != nil {
		r.report(ctx, epic.ID, seq, "keep work", keepErr)
		return next, nil, false
	}
	note := fmt.Sprintf(
		"orreryd could not carry %s's work onto the epic's branch at %s: %v. The work of the earlier rungs stays in "+
			"this task's worktree on its base %s, and is kept at %s (commit %s).",
		taskLabel(seq), newBase, moveErr, from.Base, ref, commit,
	)
	deliverable := view.Task.Deliverable + "\n\n" + note
	return next, &store.TaskVersionInput{
		Source: store.SourceCarry, Step: from.Step, Reason: note, Deliverable: &deliverable,
	}, true
}

// grant gives the task the files action names and stores action's build, which runs its implementer again in the same
// worktree. When another open task owns one of the files, the store writes nothing, and the return is a request the
// grant could not settle: it goes to the orchestrator (request), and when the task has no decisions left, the task
// climbs with action's reason, the refusal and the files it named.
func (r *Runner) grant(ctx context.Context, epic store.EpicRow, view View, action Action) bool {
	seq := view.Task.Seq
	err := r.store.GrantTaskFiles(ctx, epic.ID, seq, view.Task.Build, action.Next, store.TaskVersionInput{
		Source: store.SourceGrant, Step: view.Task.Build.Step, Reason: action.Next.Failure, Files: action.NeedsFiles,
	}, r.now())
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

// decide carries out an orchestrator's rerun, grant or re-brief: in one write the task's version, action's Version, is
// stored with the files it adds and removes, its build moves to action's, and the decision is recorded
// (store.Store.RerunTask). When an open task the build does not wait for owns one of the files, the store writes
// nothing, and the request is settled as action's fallback, with the refusal added.
func (r *Runner) decide(ctx context.Context, epic store.EpicRow, view View, action Action) bool {
	seq := view.Task.Seq
	err := r.store.RerunTask(
		ctx, epic.ID, seq, view.Task.Build, action.Next, action.Version, action.Behind, *action.Decision, r.now(),
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

// resume moves a waiting build's implement worktree onto action's revision, keeping its work, with the task's own ref
// holding its uncommitted work while the move runs (MoveWorktree, WipRef), then stores action's build. A move git
// refuses climbs the task through fail, with `T<seq> could not move onto the new base <base>: <error>` as its failure.
// fail then tries the carry onto that base, and when git refuses it again, keeps the work with a carry version.
func (r *Runner) resume(ctx context.Context, epic store.EpicRow, view View, action Action) bool {
	seq, from := view.Task.Seq, view.Task.Build
	path := ImplementWorktree(r.stateDir, epic.ID, seq)
	if err := r.git.MoveWorktree(ctx, path, WipRef(epic.ID, seq), action.Revision); err != nil {
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
