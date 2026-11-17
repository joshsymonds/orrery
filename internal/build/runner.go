package build

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/reviewers"
	"github.com/joshsymonds/orrery/internal/store"
)

// The states an epic's branch is in: creating while the branch is being made, and created once it exists. An epic
// whose branch state is empty has no branch yet.
const (
	branchCreating = "creating"
	branchCreated  = "created"
)

// taskOpen is the status of a task whose build a pass moves.
const taskOpen = "open"

// maxActions is the most actions a pass takes for one task, so a build whose actions keep changing it ends the pass.
const maxActions = 8

// Run passes at once, then once per tick, until ctx ends.
func (r *Runner) Run(ctx context.Context) {
	ticker := time.NewTicker(r.tick)
	defer ticker.Stop()
	for {
		r.pass(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// PassTasks ensures the branch of epic id, then carries out the actions of each open task of the epic among seqs, as
// a pass does for each open task of the epic. A seq that is not an open task of the epic moves nothing.
func (r *Runner) PassTasks(ctx context.Context, id string, seqs []int64) {
	r.passEpic(ctx, id, func(epic store.EpicRow) []int64 {
		return slices.DeleteFunc(openSeqs(epic), func(seq int64) bool { return !slices.Contains(seqs, seq) })
	})
}

// pass runs one pass over every build epic. An epic whose pass fails is logged and waits for the next pass.
func (r *Runner) pass(ctx context.Context) {
	ids, err := r.store.BuildEpics(ctx)
	if err != nil {
		r.logger.ErrorContext(ctx, "list build epics", "err", err)
		return
	}
	for _, id := range ids {
		r.passEpic(ctx, id, openSeqs)
	}
}

// passEpic ensures the branch of epic id, then carries out the actions of each task seqs picks from the epic, in the
// order it picks them.
func (r *Runner) passEpic(ctx context.Context, id string, seqs func(store.EpicRow) []int64) {
	epic, err := r.store.Epic(ctx, id)
	if err != nil {
		r.logger.ErrorContext(ctx, "read build epic", "epic", id, "err", err)
		return
	}
	tip := r.ensureBranch(ctx, epic)
	for _, seq := range seqs(epic) {
		if epic, err = r.passTask(ctx, epic, seq, tip); err != nil {
			r.logger.ErrorContext(ctx, "read build epic", "epic", id, "err", err)
			return
		}
	}
}

// passTask carries out the actions Decide gives open task seq of epic at tip, one at a time, up to maxActions. An
// action that changed anything is followed by a read of the epic, so the next action sees what the last one stored.
// A failed read of the epic is returned, since the epic may have changed in a way the pass cannot see. A task whose
// view cannot be read is logged and waits for the next pass.
func (r *Runner) passTask(ctx context.Context, epic store.EpicRow, seq int64, tip string) (store.EpicRow, error) {
	for range maxActions {
		task, found := taskOf(epic, seq)
		if !found || task.Status != taskOpen {
			return epic, nil
		}
		view, err := r.view(ctx, epic, task, tip)
		if err != nil {
			r.logger.ErrorContext(ctx, "read task build", "epic", epic.ID, "task", seq, "err", err)
			return epic, nil
		}
		if !r.execute(ctx, epic, view, Decide(view)) {
			return epic, nil
		}
		if epic, err = r.store.Epic(ctx, epic.ID); err != nil {
			return epic, fmt.Errorf("read epic %s: %w", epic.ID, err)
		}
	}
	return epic, nil
}

// openSeqs returns the seqs of the open tasks of epic, in order.
func openSeqs(epic store.EpicRow) []int64 {
	var seqs []int64
	for _, task := range epic.Tasks {
		if task.Status == taskOpen {
			seqs = append(seqs, task.Seq)
		}
	}
	return seqs
}

// taskOf returns task seq of epic, and false when epic has no such task.
func taskOf(epic store.EpicRow, seq int64) (store.EpicTask, bool) {
	for _, task := range epic.Tasks {
		if task.Seq == seq {
			return task, true
		}
	}
	return store.EpicTask{}, false
}

// ensureBranch returns the tip of BranchRef(epic.ID), or "" when there is none to use. It creates the branch at the
// epic's base when the epic has none. A paused or parked epic creates nothing: it reads the tip of a created branch and
// returns "" for any other state. A failure is logged and returns "", so a task that needs the branch waits for the
// next pass.
func (r *Runner) ensureBranch(ctx context.Context, epic store.EpicRow) string {
	tip, err := r.branchTip(ctx, epic)
	if err != nil {
		r.logger.ErrorContext(ctx, "ensure epic branch", "epic", epic.ID, "err", err)
		return ""
	}
	return tip
}

// branchTip is ensureBranch's work: it returns the tip of epic's branch, creating or adopting the branch as the epic's
// state allows, and "" with no error when the epic is paused or parked and has no created branch.
func (r *Runner) branchTip(ctx context.Context, epic store.EpicRow) (string, error) {
	if epic.Branch == branchCreated {
		return r.createdTip(ctx, epic)
	}
	if epic.Paused || epic.Parked != "" {
		return "", nil
	}
	tip, exists, err := BranchTip(ctx, epic.Repository, epic.ID)
	if err != nil {
		return "", err
	}
	if exists {
		return r.adopt(ctx, epic, tip)
	}
	return r.create(ctx, epic)
}

// createdTip returns the tip of the branch of a created epic. A branch that is gone parks the epic, unless the epic is
// parked already, so the owner is told once and not on every pass.
func (r *Runner) createdTip(ctx context.Context, epic store.EpicRow) (string, error) {
	tip, exists, err := BranchTip(ctx, epic.Repository, epic.ID)
	if err != nil {
		return "", err
	}
	if exists {
		return tip, nil
	}
	if epic.Parked != "" {
		return "", nil
	}
	return "", r.park(ctx, epic.ID, "epic/"+Slug(epic.ID)+" is gone")
}

// adopt returns the tip of the existing branch of an epic that has no branch state yet or is creating one. The branch
// is the epic's only when the epic left it creating at its base and nothing has it checked out; any other existing
// branch parks the epic.
func (r *Runner) adopt(ctx context.Context, epic store.EpicRow, tip string) (string, error) {
	if epic.Branch != branchCreating || tip != epic.Base {
		return "", r.park(ctx, epic.ID, "epic/"+Slug(epic.ID)+" exists")
	}
	checked, err := BranchCheckedOut(ctx, epic.Repository, epic.ID)
	if err != nil {
		return "", err
	}
	if checked {
		return "", r.park(ctx, epic.ID, "epic/"+Slug(epic.ID)+" exists")
	}
	return r.markCreated(ctx, epic.ID, tip)
}

// create makes the branch of an epic that has none: it marks the branch creating, creates it at the base, and marks it
// created. A branch that cannot be created parks the epic with the reason it could not.
func (r *Runner) create(ctx context.Context, epic store.EpicRow) (string, error) {
	if err := r.store.SetEpicBranch(ctx, epic.ID, branchCreating); err != nil {
		return "", fmt.Errorf("mark branch of epic %s creating: %w", epic.ID, err)
	}
	if err := r.git.CreateBranch(ctx, epic.Repository, epic.ID, epic.Base); err != nil {
		return "", r.park(ctx, epic.ID, "epic/"+Slug(epic.ID)+" exists: "+err.Error())
	}
	return r.markCreated(ctx, epic.ID, epic.Base)
}

// markCreated marks the branch of epic created and returns tip, the commit it names.
func (r *Runner) markCreated(ctx context.Context, epic, tip string) (string, error) {
	if err := r.store.SetEpicBranch(ctx, epic, branchCreated); err != nil {
		return "", fmt.Errorf("mark branch of epic %s created: %w", epic, err)
	}
	return tip, nil
}

// park parks epic with reason, which the owner is told.
func (r *Runner) park(ctx context.Context, epic, reason string) error {
	if err := r.store.ParkEpic(ctx, epic, reason, r.now()); err != nil {
		return fmt.Errorf("park epic %s: %w", epic, err)
	}
	return nil
}

// view returns the View of task, which is open in epic, with the epic's branch at tip and the epic's candidate. It
// reads the evidence run or step of the task's current unit by its derived id, and the reproduction check of a test
// item's green unit. The run is kept only while its spec is current with the task's evidence item, as the store reads
// a task's runs, so a run whose item has changed reads missing. It also counts the earlier reruns of a red or green
// unit that exited store.EnvironmentExit in a row, so Decide knows how many runs of the unit have done so. For an
// ended implementer step it reads the HEAD of the workspace the step's row records, which is the worktree the step ran
// in whatever path that has, and when the step's refs snapshot is set, the ref moves since it. It reads what a request
// needs as readRequest gives.
func (r *Runner) view(ctx context.Context, epic store.EpicRow, task store.EpicTask, tip string) (View, error) {
	view := View{
		Epic: epic.ID, Paused: epic.Paused, Parked: epic.Parked != "", ReviewGates: epic.ReviewGates,
		NoVerify: noVerify(epic, task), Task: task, Branch: tip, Candidate: epic.Candidate,
	}
	if err := r.readUnit(ctx, epic, task, &view); err != nil {
		return View{}, err
	}
	if err := r.readRequest(ctx, epic, task, &view); err != nil {
		return View{}, err
	}
	if task.Build.Stage != StageImplement || view.Step == nil || view.Step.End == "" {
		return view, nil
	}
	view.Head = r.head(ctx, view.Step.Workspace)
	if task.Build.Refs != "" {
		moves, err := r.refMoves(ctx, epic, task.Build.Refs, task.Build.Base, view.Head)
		if err != nil {
			return View{}, err
		}
		view.RefMoves = moves
	}
	return view, nil
}

// readUnit sets the parts of view that describe task's current unit: its evidence run or step by the derived id of the
// unit, the reproduction check of a test item's green unit, and how many environment reruns ran just before it.
func (r *Runner) readUnit(ctx context.Context, epic store.EpicRow, task store.EpicTask, view *View) error {
	runID, stepID := unitIDs(epic.ID, task.Seq, task.Build)
	if runID != "" {
		run, ok, err := r.evidenceRun(ctx, runID)
		if err != nil {
			return err
		}
		if ok && evidence.Current(run.Spec, task.Evidence) {
			view.Run = &run
		}
	}
	if stepID != "" {
		row, ok, err := r.stepRow(ctx, stepID)
		if err != nil {
			return err
		}
		if ok {
			view.Step = &row
		}
	}
	repro, found, err := r.reproOf(ctx, epic, task)
	if err != nil {
		return err
	}
	if found {
		view.Repro = &repro
	}
	view.EnvironmentBefore, err = r.environmentBefore(ctx, epic, task)
	return err
}

// readRequest sets the parts of view a request reads. An asked build reads whether its question is answered (answered). A
// waiting build reads the epic's tasks as peers and the base the next integrate batch composes on: the epic's candidate
// when it has one, and its stored base otherwise. An orchestrate unit reads the implementer step it decides, by the
// build's step, and once its own step has ended done, the peers its decision is checked against.
func (r *Runner) readRequest(ctx context.Context, epic store.EpicRow, task store.EpicTask, view *View) error {
	build := task.Build
	switch {
	case build.Held == HeldAsked:
		view.Answered = answered(epic.Questions, build.Question)
		return nil
	case build.Held == HeldWaiting:
		view.NewBase = epic.StoredBase
		if epic.Candidate != "" {
			view.NewBase = epic.Candidate
		}
		peers, err := r.peers(ctx, epic)
		view.Peers = peers
		return err
	case build.Stage != StageOrchestrate:
		return nil
	}
	if build.Step != "" {
		row, ok, err := r.stepRow(ctx, build.Step)
		if err != nil {
			return err
		}
		if ok {
			view.Returned = &row
		}
	}
	if view.Step == nil || view.Step.End != endDone {
		return nil
	}
	peers, err := r.peers(ctx, epic)
	view.Peers = peers
	return err
}

// answered reports whether the owner has answered question seq among questions: it is answered, or the question that
// replaced it, followed to the last replacement, is. A question that is not stored has no answer to wait for, so it
// counts as answered.
func answered(questions []store.EpicQuestion, seq int64) bool {
	for range questions {
		i := slices.IndexFunc(questions, func(question store.EpicQuestion) bool { return question.Seq == seq })
		if i < 0 {
			return true
		}
		if questions[i].State != store.QuestionReplaced || questions[i].ReplacedBy == 0 {
			return questions[i].State == store.QuestionAnswered
		}
		seq = questions[i].ReplacedBy
	}
	return false
}

// peers returns the tasks of epic as a request reads them, with the integration each has by the epic's node runs.
func (r *Runner) peers(ctx context.Context, epic store.EpicRow) ([]Peer, error) {
	runs, err := r.store.NodeRuns(ctx, epic.ID)
	if err != nil {
		return nil, fmt.Errorf("read node runs of epic %s: %w", epic.ID, err)
	}
	return peersOf(ctx, epic, runs)
}

// noVerify reports whether the task-stage reviewer at task's gate position has verify off. The gate positions are the
// task-stage reviewers in their merged order, so a position no task-stage reviewer holds has verify on.
func noVerify(epic store.EpicRow, task store.EpicTask) bool {
	stage := reviewers.ForStage(epic.Reviewers, reviewers.StageTask)
	pos := task.Build.GatePos
	return pos >= 0 && pos < int64(len(stage)) && !stage[pos].Verify
}

// unitIDs returns the derived ids of the evidence run and of the step of the unit build is on. A red or green unit has
// a run and no step, a review or verify unit a step and no run, and an implement unit a step. An orchestrate unit's
// step counts the decisions made on the task where a review step has its gate position. A unit a build is not on
// has neither, so both ids are empty.
func unitIDs(epic string, seq int64, build store.TaskBuild) (string, string) {
	switch build.Stage {
	case StageRed:
		return RunID(epic, seq, StageRed, 0, build.Rerun), ""
	case StageGreen:
		return RunID(epic, seq, StageGreen, build.Attempt, build.Rerun), ""
	case StageImplement:
		return "", StepID(epic, seq, StageImplement, build.Attempt, build.Rerun, 0, 0)
	case StageOrchestrate:
		return "", StepID(epic, seq, StageOrchestrate, build.Attempt, build.Rerun, build.Decisions, 0)
	case StageReview:
		return "", StepID(epic, seq, StageReview, build.Attempt, build.Rerun, build.GatePos, 0)
	case StageVerify:
		return "", StepID(epic, seq, StageVerify, build.Attempt, build.Rerun, build.GatePos, build.FindingPos)
	}
	return "", ""
}

// evidenceRun returns the stored evidence run with id, and false with no error when no run has that id.
func (r *Runner) evidenceRun(ctx context.Context, id string) (store.EvidenceRun, bool, error) {
	run, err := r.store.EvidenceRun(ctx, id)
	if errors.Is(err, store.ErrNoEvidenceRun) {
		return store.EvidenceRun{}, false, nil
	}
	if err != nil {
		return store.EvidenceRun{}, false, fmt.Errorf("read evidence run %s: %w", id, err)
	}
	return run, true, nil
}

// stepRow returns the row of the step with id, and false with no error when no step has that id.
func (r *Runner) stepRow(ctx context.Context, id string) (store.StepRow, bool, error) {
	row, err := r.steps.Status(ctx, id)
	if errors.Is(err, store.ErrNoStep) {
		return store.StepRow{}, false, nil
	}
	if err != nil {
		return store.StepRow{}, false, fmt.Errorf("read step %s: %w", id, err)
	}
	return row, true, nil
}

// reproOf returns the reproduction check of a test item's green unit, and false with no error when the task is at no
// such unit or the check is not stored.
func (r *Runner) reproOf(ctx context.Context, epic store.EpicRow, task store.EpicTask) (store.NodeCheck, bool, error) {
	if task.Build.Stage != StageGreen || task.Evidence.Kind != evidence.KindTest {
		return store.NodeCheck{}, false, nil
	}
	return r.nodeCheck(ctx, RunID(epic.ID, task.Seq, unitRepro, task.Build.Attempt, task.Build.Rerun))
}

// nodeCheck returns the stored node check with id, and false with no error when no check has that id.
func (r *Runner) nodeCheck(ctx context.Context, id string) (store.NodeCheck, bool, error) {
	check, err := r.store.NodeCheck(ctx, id)
	if errors.Is(err, store.ErrNoNodeCheck) {
		return store.NodeCheck{}, false, nil
	}
	if err != nil {
		return store.NodeCheck{}, false, fmt.Errorf("read node check %s: %w", id, err)
	}
	return check, true, nil
}

// environmentBefore returns how many runs of the unit task's build is on exited store.EnvironmentExit without timing out
// in a row just before its current rerun, up to store.EnvironmentReruns. The runs are the stored ones, so a restart keeps
// the count. Only a red or green unit reruns on an environment exit, so any other unit counts none.
func (r *Runner) environmentBefore(ctx context.Context, epic store.EpicRow, task store.EpicTask) (int64, error) {
	build := task.Build
	if build.Stage != StageRed && build.Stage != StageGreen {
		return 0, nil
	}
	var before int64
	for rerun := build.Rerun - 1; rerun >= 0 && before < store.EnvironmentReruns; rerun-- {
		exited, err := r.environmentAt(ctx, epic, task, rerun)
		if err != nil {
			return 0, err
		}
		if !exited {
			break
		}
		before++
	}
	return before, nil
}

// environmentAt reports whether the red or green unit of task's build exited store.EnvironmentExit without timing out at
// rerun of its attempt. A test item's green unit counts as exited that way when its reproduction did, or else when its
// capture did. Each run is read under its derived id, and a capture only while its spec is current, as view reads the
// current run.
func (r *Runner) environmentAt(
	ctx context.Context, epic store.EpicRow, task store.EpicTask, rerun int64,
) (bool, error) {
	build := task.Build
	if build.Stage == StageGreen && task.Evidence.Kind == evidence.KindTest {
		check, found, err := r.nodeCheck(ctx, RunID(epic.ID, task.Seq, unitRepro, build.Attempt, rerun))
		if err != nil {
			return false, err
		}
		if found && check.NotRun == "" && store.EnvironmentExited(check.Exit, check.TimedOut) {
			return true, nil
		}
	}
	runID, _ := unitIDs(epic.ID, task.Seq, store.TaskBuild{Stage: build.Stage, Attempt: build.Attempt, Rerun: rerun})
	run, ok, err := r.evidenceRun(ctx, runID)
	if err != nil {
		return false, err
	}
	return ok && evidence.Current(run.Spec, task.Evidence) && run.NotCaptured == store.NotCapturedEnvironment, nil
}

// head returns the commit HEAD names in the worktree at dir. A worktree that cannot be read has no head: the error is
// logged and "" is returned, so the implementer's return must then name its commit.
func (r *Runner) head(ctx context.Context, dir string) string {
	head, err := runGit(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		r.logger.WarnContext(ctx, "read implementer head", "dir", dir, "err", err)
		return ""
	}
	return head
}

// refMoves returns the ref moves between the snapshot before and a snapshot of epic's repository now. The implementer's
// commits are those between base, the commit its step started at, and head, the HEAD of its worktree. A move of main or
// of HEAD onto a commit that origin's main reaches, and that holds none of those commits, is told rather than parked.
// Without origin's main, or without a head to read the commits from, every move parks as Compare reports it.
func (r *Runner) refMoves(ctx context.Context, epic store.EpicRow, before, base, head string) ([]RefMove, error) {
	after, err := snapshotOf(ctx, r.store, epic)
	if err != nil {
		return nil, err
	}
	moves, err := Compare(before, after)
	if err != nil {
		return nil, fmt.Errorf("compare refs of epic %s: %w", epic.ID, err)
	}
	origin, ok, err := originMain(ctx, epic.Repository)
	if err != nil {
		return nil, fmt.Errorf("read origin main of epic %s: %w", epic.ID, err)
	}
	if !ok || head == "" {
		return moves, nil
	}
	steps, err := stepCommits(ctx, epic.Repository, base, head)
	if err != nil {
		return nil, fmt.Errorf("read implementer commits of epic %s: %w", epic.ID, err)
	}
	moves, err = originTells(ctx, epic.Repository, moves, origin, steps)
	if err != nil {
		return nil, fmt.Errorf("check ref moves of epic %s against origin: %w", epic.ID, err)
	}
	return moves, nil
}

// snapshotOf returns a snapshot of epic's repository. It leaves out the branch of every epic that shares the
// repository, since an epic's own branch is the work the build commits and not a move the implementer made.
func snapshotOf(ctx context.Context, s Store, epic store.EpicRow) (string, error) {
	epics, err := s.Epics(ctx)
	if err != nil {
		return "", fmt.Errorf("list epics: %w", err)
	}
	var exclude []string
	for _, other := range epics {
		if other.Repository == epic.Repository {
			exclude = append(exclude, BranchRef(other.ID))
		}
	}
	return Snapshot(ctx, epic.Repository, exclude)
}
