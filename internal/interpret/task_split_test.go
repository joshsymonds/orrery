package interpret

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/build"
	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/workflow"
)

// TestTaskSplitDecideEndsParentBuildAndStartsDescendants checks what Decide asks of a plan whose task T1 was split on
// turn 2 into T2 and T3: T1's build run ends, T2 and T3 start their build runs on turn 2, and a T1 whose build run has
// ended starts nothing again. The control keeps T1 open, so its build run keeps running and nothing is split.
func TestTaskSplitDecideEndsParentBuildAndStartsDescendants(t *testing.T) {
	t.Parallel()
	turns := []store.EpicTurn{turnAt(1, "work"), turnAt(2, "work")}
	flow := []workflow.Node{planNode()}
	reg := map[string]kinds.Kind{"plan": kinds.Plan{}, "build": kinds.Build{}}
	split := []store.EpicTask{
		{Seq: 1, Turn: 1, Status: "split", Build: store.TaskBuild{Stage: "implement", Attempt: 4}},
		openTask(2, 2),
		openTask(3, 2),
	}
	parentRun := running(childOf("plan/build/T1", 0, 1, 1, 1))
	decideCases(t, []decideCase{
		{
			name: "a split task's running build ends, and its descendants start their build runs",
			flow: flow,
			rows: rowsOf(turns, split, runningPlan(0), parentRun),
			reg:  reg,
			want: []Action{
				{Kind: ActRun, Path: "plan", NodeKind: "plan"},
				{Kind: ActStart, Runs: []store.NodeRun{childOf("plan/build/T2", 0, 2, 2, 1)}},
				{Kind: ActStart, Runs: []store.NodeRun{childOf("plan/build/T3", 0, 3, 2, 2)}},
				{Kind: ActEnd, Ends: []store.NodeEnd{{Path: "plan/build/T1"}}},
			},
		},
		{
			name: "a split task whose build run has ended starts nothing for it again",
			flow: flow,
			rows: rowsOf(turns, split, runningPlan(0), ended(parentRun, "")),
			reg:  reg,
			want: []Action{
				{Kind: ActRun, Path: "plan", NodeKind: "plan"},
				{Kind: ActStart, Runs: []store.NodeRun{childOf("plan/build/T2", 0, 2, 2, 1)}},
				{Kind: ActStart, Runs: []store.NodeRun{childOf("plan/build/T3", 0, 3, 2, 2)}},
			},
		},
		{
			name: "control: the same task still open keeps its build run and no split starts",
			flow: flow,
			rows: rowsOf(turns, []store.EpicTask{openTask(1, 1)}, runningPlan(0), parentRun),
			reg:  reg,
			want: []Action{
				{Kind: ActRun, Path: "plan", NodeKind: "plan"},
				{Kind: ActRun, Path: "plan/build/T1", NodeKind: "build", Task: 1},
			},
		},
	})
}

// taskSplitSteps is the fake claude at the step seam of the build runner. It records each step the build starts, and
// reports each one as running until the test ends it, so the build waits on it as it waits on a live agent.
type taskSplitSteps struct {
	starts []enginehost.StepStart
}

// Start records start, and returns the id it carries, as the engine starts a step under the id it is given.
func (f *taskSplitSteps) Start(_ context.Context, start enginehost.StepStart) (string, error) {
	f.starts = append(f.starts, start)
	return start.ID, nil
}

// Status reports the step with id as running when one was started under it, and as no step otherwise.
func (f *taskSplitSteps) Status(_ context.Context, id string) (store.StepRow, error) {
	if slices.ContainsFunc(f.starts, func(start enginehost.StepStart) bool { return start.ID == id }) {
		return store.StepRow{ID: id}, nil
	}
	return store.StepRow{}, store.ErrNoStep
}

// startsOf returns the steps started for the task labelled task, in the order they started.
func (f *taskSplitSteps) startsOf(task string) []enginehost.StepStart {
	var starts []enginehost.StepStart
	for _, start := range f.starts {
		if start.Task == task {
			starts = append(starts, start)
		}
	}
	return starts
}

// taskSplitEvidence is the evidence the build's red runs read. Each run it starts is stored captured and judged to fail
// at the base, which is the red a Requirement not yet met holds, so the build goes on to implement.
type taskSplitEvidence struct {
	s *store.Store
}

// RunTask stores a run under id, captured and judged to fail at revision, and returns it. A run the store already holds
// under id is returned as it is. The run's spec is the evidence item of task seq of epic.
func (e taskSplitEvidence) RunTask(
	ctx context.Context, id, epic string, seq int64, revision, phase string,
) (store.EvidenceRun, error) {
	stored, err := e.s.EvidenceRun(ctx, id)
	if err == nil {
		return stored, nil
	}
	if !errors.Is(err, store.ErrNoEvidenceRun) {
		return store.EvidenceRun{}, err
	}
	row, err := e.s.Epic(ctx, epic)
	if err != nil {
		return store.EvidenceRun{}, err
	}
	task, ok := taskSplitFind(row, seq)
	if !ok {
		return store.EvidenceRun{}, fmt.Errorf("no task %d of epic %s", seq, epic)
	}
	run := store.EvidenceRun{
		ID: id, Epic: epic, Revision: revision, Phase: phase, Source: "captured", Spec: task.Evidence,
		Created: runClock(),
	}
	if err = e.s.AddEvidenceRun(ctx, run); err != nil {
		return store.EvidenceRun{}, err
	}
	if err = e.s.RecordEvidenceCapture(ctx, id, store.EvidenceCapture{At: runClock(), Exit: 1}); err != nil {
		return store.EvidenceRun{}, err
	}
	verdict := store.EvidenceVerdict{
		Verdict: evidence.VerdictFails, Passed: evidence.Passed(evidence.PhaseRed, "", 0, evidence.VerdictFails),
		At: runClock(),
	}
	if err = e.s.RecordEvidenceVerdict(ctx, id, verdict); err != nil {
		return store.EvidenceRun{}, err
	}
	return run, nil
}

// Artifact returns no artifact: the red runs of these tests keep none.
func (e taskSplitEvidence) Artifact(context.Context, string, string) ([]byte, string, error) {
	return nil, "", nil
}

// StderrLog returns no log: the red runs of these tests keep none.
func (e taskSplitEvidence) StderrLog(context.Context, string) ([]byte, string, error) {
	return nil, "", nil
}

// taskSplitEpicGates returns the gates the split epic defines: a command gate named check, so an implement step
// resolves its gate on the epic without a project file.
func taskSplitEpicGates() []store.EpicGate {
	return []store.EpicGate{{Name: "check", Argv: []string{"/usr/bin/true"}, Limit: 60}}
}

// taskSplitRoles is the role file the split epic runs under, which names claude-impl as the implementer's agent.
const taskSplitRoles = "implementer = \"claude-impl\"\nverifier = \"claude-verify\"\n"

// taskSplitWorld is the epic a split test runs. Its repository and store are real, its build runner runs over the fake
// steps and evidence, and its planner is a fake that the runner passes but never wakes.
type taskSplitWorld struct {
	t        *testing.T
	s        *store.Store
	repo     string
	steps    *taskSplitSteps
	stateDir string
	getenv   func(string) string
}

// newTaskSplitWorld returns the split epic on a repository with one commit, planned on turn 1 with task T1: one task on
// R1 that names two files, runner.go and decide.go.
func newTaskSplitWorld(t *testing.T) *taskSplitWorld {
	t.Helper()
	ctx := context.Background()
	repo, base := nodeRepo(t)
	s := openRunStore(t)
	config := t.TempDir()
	if err := os.MkdirAll(filepath.Join(config, "orrery"), 0o750); err != nil {
		t.Fatalf("make the role file directory: %v", err)
	}
	roles := filepath.Join(config, "orrery", "roles.toml")
	if err := os.WriteFile(roles, []byte(taskSplitRoles), 0o600); err != nil {
		t.Fatalf("write the role file: %v", err)
	}
	if err := s.CreateEpic(ctx, store.EpicRow{
		ID: runEpic, Repository: repo, Base: base, Title: "Split a wide task", Phase: runPhase,
		Created: runClock(), Changed: runClock(), Workflow: planBuildPin(), Gates: taskSplitEpicGates(),
	}); err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
	plan := store.EpicPlan{
		Decisions: []string{}, Outline: "one wide task", Outcome: "work",
		Tasks: []store.EpicTaskInput{runTask("wide", "internal/interpret/runner.go")},
	}
	plan.Tasks[0].Files = []string{"internal/interpret/runner.go", "internal/interpret/decide.go"}
	completeTurn(t, s, plan)
	return &taskSplitWorld{
		t: t, s: s, repo: repo, steps: &taskSplitSteps{}, stateDir: t.TempDir(),
		getenv: func(name string) string {
			if name == "XDG_CONFIG_HOME" {
				return config
			}
			return ""
		},
	}
}

// runner returns a runner over the world's store, with a build runner over its fake steps and evidence. A runner built
// anew after the others reads the same store, as an orreryd restart's runner does.
func (w *taskSplitWorld) runner() *Runner {
	builder := build.New(build.Config{
		Store: w.s, Steps: w.steps, Evidence: taskSplitEvidence{s: w.s}, Getenv: w.getenv, StateDir: w.stateDir,
		Now: runClock,
	})
	return New(Config{Store: w.s, Planner: &fakePlanner{}, Builder: builder, Now: runClock})
}

// climb moves task seq of the world's epic to rung, from the rung it is on, as the build's failure of that rung does:
// the rung it is on ends failed and the new rung is written, with the failure as its cause.
func (w *taskSplitWorld) climb(seq, rung int64) {
	w.t.Helper()
	task := taskSplitTask(w.t, w.s, seq)
	const cause = "rung failed"
	move := store.RungMove{MovedBy: store.MovedByUnit, Cause: cause, To: rung}
	to := store.ClimbedBuild(task.Build, rung, cause)
	err := w.s.ClimbTask(context.Background(), runEpic, seq, task.Build, to, move, nil, nil, runClock())
	if err != nil {
		w.t.Fatalf("ClimbTask of T%d to rung %d: %v", seq, rung, err)
	}
}

// claimTurn claims the planner turn of the world's epic, and returns its seq.
func (w *taskSplitWorld) claimTurn() int64 {
	w.t.Helper()
	turn, err := w.s.ClaimTurn(context.Background(), runEpic, runClock())
	if err != nil {
		w.t.Fatalf("ClaimTurn: %v", err)
	}
	return turn.Seq
}

// completeTurn completes the turn seq with one action, and no task or question.
func (w *taskSplitWorld) completeTurn(seq int64, action store.EpicActionInput) {
	w.t.Helper()
	plan := store.EpicPlan{
		Decisions: []string{}, Outline: "split the task", Outcome: "work", Actions: []store.EpicActionInput{action},
	}
	if err := w.s.CompleteTurn(context.Background(), runEpic, seq, plan, runClock()); err != nil {
		w.t.Fatalf("CompleteTurn of turn %d: %v", seq, err)
	}
}

// carryOut carries out each pending planner action of the world's epic, as the planner's pass does.
func (w *taskSplitWorld) carryOut() {
	w.t.Helper()
	actions, err := w.s.PendingActions(context.Background())
	if err != nil {
		w.t.Fatalf("PendingActions: %v", err)
	}
	for _, action := range actions {
		if _, err = w.s.CarryOutAction(context.Background(), action, runClock()); err != nil {
			w.t.Fatalf("CarryOutAction: %v", err)
		}
	}
}

// taskSplitParts returns the two descendants of the split the tests give: T2 on runner.go, which claims R1, and T3 on
// decide.go, which claims none. Together they hold the files of T1, and R1 moves to T2.
func taskSplitParts() []store.SplitTask {
	return []store.SplitTask{
		{
			Title: "Runner", Deliverable: "the runner splits", Files: []string{"internal/interpret/runner.go"},
			Claims: []string{"R1"}, Gate: "check",
			Evidence: evidence.Item{
				Capture: []string{"scripts/check"}, Limit: 60, Conclusion: "the runner splits", Judge: "a judge",
			},
		},
		{
			Title: "Decide", Deliverable: "decide splits", Files: []string{"internal/interpret/decide.go"},
			Gate: "check",
			Evidence: evidence.Item{
				Capture: []string{"scripts/check"}, Limit: 60, Conclusion: "decide splits", Judge: "a judge",
			},
		},
	}
}

// taskSplitStages is the number of steps the world's build started at each stage of runTaskSplit, and the Requirements
// the parent held before its split.
type taskSplitStages struct {
	beforeClimb, atRungFour, afterSplit, afterRestart int
	parentClaims                                      []string
}

// runTaskSplit runs the split through the engine. T1 starts its first implement step; it climbs to rung 4 on three
// failures, and the build starts nothing for it there; then the planner turn that answers T1's approach trigger splits
// it into T2 and T3, and the split is carried out. A new runner passes the epic after the split, and again after that.
// It returns the world and the steps started at each stage.
func runTaskSplit(t *testing.T) (*taskSplitWorld, taskSplitStages) {
	t.Helper()
	ctx := context.Background()
	w := newTaskSplitWorld(t)
	w.runner().Pass(ctx)
	var stages taskSplitStages
	stages.beforeClimb = len(w.steps.starts)
	w.climb(1, 2)
	w.climb(1, 3)
	w.climb(1, 4)
	w.runner().Pass(ctx)
	stages.atRungFour = len(w.steps.starts)
	stages.parentClaims = taskSplitTask(t, w.s, 1).Requirements
	turn := w.claimTurn()
	w.completeTurn(turn, store.EpicActionInput{
		Kind: store.ActionSplit, Task: 1, Split: taskSplitParts(), Reason: "the change is too wide for one task",
	})
	w.carryOut()
	w.runner().Pass(ctx)
	stages.afterSplit = len(w.steps.starts)
	w.runner().Pass(ctx)
	stages.afterRestart = len(w.steps.starts)
	return w, stages
}

// taskSplitTask returns task seq of the world's epic as the store reads it.
func taskSplitTask(t *testing.T, s *store.Store, seq int64) store.EpicTask {
	t.Helper()
	task, ok := taskSplitFind(epicOf(t, s), seq)
	if !ok {
		t.Fatalf("no task %d of epic %s", seq, runEpic)
	}
	return task
}

// taskSplitFind returns the task of row with seq, and false when it has none.
func taskSplitFind(row store.EpicRow, seq int64) (store.EpicTask, bool) {
	i := slices.IndexFunc(row.Tasks, func(task store.EpicTask) bool { return task.Seq == seq })
	if i < 0 {
		return store.EpicTask{}, false
	}
	return row.Tasks[i], true
}

// TestTaskSplitParentNeverRunsAgain checks that the split task T1 ends split, its build run ends, and no step of T1
// starts again: T1 starts its one implement step before it climbs, and nothing starts for it on rung 4, after the
// split, or after a restart.
func TestTaskSplitParentNeverRunsAgain(t *testing.T) {
	t.Parallel()
	w, stages := runTaskSplit(t)
	if stages.beforeClimb != 1 {
		t.Fatalf("steps started before T1 climbs = %d, want its one implement step", stages.beforeClimb)
	}
	if stages.atRungFour != stages.beforeClimb {
		t.Errorf("steps started on rung 4 = %d, want none: T1 waits for its change of approach", stages.atRungFour)
	}
	if got := len(w.steps.startsOf("T1")); got != 1 {
		t.Errorf("steps started for T1 = %d, want only the one from before its climb", got)
	}
	if status := taskSplitTask(t, w.s, 1).Status; status != "split" {
		t.Errorf("T1 status = %q, want split", status)
	}
	if run := runAt(t, w.s, "plan/build/T1"); run.State != store.NodeEnded {
		t.Errorf("build run of T1 = %+v, want ended", run)
	}
}

// TestTaskSplitDescendantsStartOnRungOneOnTheEpicBranch checks that each descendant's first implement step starts at
// attempt 1, on rung 1, with its base at the epic branch's tip, on the implementer role's own agent. Each descendant
// has one step, started after red passed, which the build records as its step.
func TestTaskSplitDescendantsStartOnRungOneOnTheEpicBranch(t *testing.T) {
	t.Parallel()
	w, _ := runTaskSplit(t)
	tip := nodeGit(t, w.repo, "rev-parse", build.BranchRef(runEpic))
	for _, seq := range []int64{2, 3} {
		label := fmt.Sprintf("T%d", seq)
		starts := w.steps.startsOf(label)
		if len(starts) != 1 {
			t.Fatalf("steps started for %s = %d, want one implement step", label, len(starts))
		}
		start := starts[0]
		if start.Agent != "claude-impl" {
			t.Errorf("%s step agent = %q, want the implementer's claude-impl", label, start.Agent)
		}
		if start.Revision != tip {
			t.Errorf("%s step revision = %q, want the epic branch's tip %q", label, start.Revision, tip)
		}
		task := taskSplitTask(t, w.s, seq)
		if task.Build.Stage != "implement" || task.Build.Attempt != 1 || store.RungOf(task.Build) != 1 {
			t.Errorf("%s build = stage %q attempt %d, want implement on attempt and rung 1",
				label, task.Build.Stage, task.Build.Attempt)
		}
		if task.Build.Base != tip {
			t.Errorf("%s build base = %q, want the epic branch's tip %q", label, task.Build.Base, tip)
		}
		if task.Build.Step != start.ID || task.Build.RedRun == "" {
			t.Errorf("%s build step = %q red run = %q, want its implement step %q after its red run",
				label, task.Build.Step, task.Build.RedRun, start.ID)
		}
	}
}

// TestTaskSplitSurvivesRestartWithDescendantsRunning checks that a new runner over the same store leaves the split in
// place: T1 stays split, T2 and T3 stay open on their running build runs with the steps they started, and the restarted
// runner starts nothing.
func TestTaskSplitSurvivesRestartWithDescendantsRunning(t *testing.T) {
	t.Parallel()
	w, stages := runTaskSplit(t)
	if stages.afterRestart != stages.afterSplit {
		t.Errorf("steps started after the restart = %d, want none beyond the %d after the split",
			stages.afterRestart, stages.afterSplit)
	}
	if status := taskSplitTask(t, w.s, 1).Status; status != "split" {
		t.Errorf("T1 status after the restart = %q, want split", status)
	}
	for _, seq := range []int64{2, 3} {
		task := taskSplitTask(t, w.s, seq)
		if task.Status != "open" {
			t.Errorf("T%d status after the restart = %q, want open", seq, task.Status)
		}
		if run := runAt(t, w.s, fmt.Sprintf("plan/build/T%d", seq)); run.State != store.NodeRunning {
			t.Errorf("build run of T%d after the restart = %+v, want running", seq, run)
		}
		if starts := w.steps.startsOf(fmt.Sprintf("T%d", seq)); len(starts) != 1 || task.Build.Step != starts[0].ID {
			t.Errorf("T%d step after the restart = %q, want the one step it started", seq, task.Build.Step)
		}
	}
}

// TestTaskSplitDescendantsOwnTheParentsFilesAndClaims checks that T2 and T3 divide the files of T1 between them with no
// file in both, that together they are exactly T1's files, and that R1, which T1 held, is held by T2 alone.
func TestTaskSplitDescendantsOwnTheParentsFilesAndClaims(t *testing.T) {
	t.Parallel()
	w, stages := runTaskSplit(t)
	if !slices.Equal(stages.parentClaims, []string{"R1"}) {
		t.Fatalf("T1 claims before the split = %q, want R1", stages.parentClaims)
	}
	parent := taskSplitTask(t, w.s, 1)
	first, second := taskSplitTask(t, w.s, 2), taskSplitTask(t, w.s, 3)
	for _, child := range []store.EpicTask{first, second} {
		if child.SplitFrom != 1 || child.Turn != 2 {
			t.Errorf("descendant T%d split from T%d on turn %d, want T1 on turn 2",
				child.Seq, child.SplitFrom, child.Turn)
		}
	}
	if slices.ContainsFunc(first.Files, func(file string) bool { return slices.Contains(second.Files, file) }) {
		t.Errorf("descendants share a file: T2 %v, T3 %v", first.Files, second.Files)
	}
	together := append(slices.Clone(first.Files), second.Files...)
	slices.Sort(together)
	want := slices.Clone(parent.Files)
	slices.Sort(want)
	if !slices.Equal(together, want) {
		t.Errorf("descendants' files = %v, want T1's files %v", together, want)
	}
	if !slices.Equal(first.Requirements, []string{"R1"}) || len(second.Requirements) != 0 {
		t.Errorf("claims of T2 = %q and T3 = %q, want R1 on T2 and none on T3", first.Requirements, second.Requirements)
	}
	if len(parent.Requirements) != 0 {
		t.Errorf("claims of split T1 = %q, want none after the claims move", parent.Requirements)
	}
}

// TestTaskSplitDescendantMayNotSplit checks that a descendant of a split whose approach wakes the planner may not split
// again: the engine refuses the split, which leaves T2 open, and it wakes the planner with a trigger that names the
// refusal, so the planner is told why.
func TestTaskSplitDescendantMayNotSplit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w, _ := runTaskSplit(t)
	w.climb(2, 2)
	w.climb(2, 3)
	w.climb(2, 4)
	turn := w.claimTurn()
	w.completeTurn(turn, store.EpicActionInput{
		Kind: store.ActionSplit, Task: 2, Split: taskSplitParts(), Reason: "T2 is too wide too",
	})
	actions, err := w.s.PendingActions(ctx)
	if err != nil {
		t.Fatalf("PendingActions: %v", err)
	}
	if len(actions) != 1 {
		t.Fatalf("pending actions = %d, want the split of T2", len(actions))
	}
	if _, err = w.s.CarryOutAction(ctx, actions[0], runClock()); err != nil {
		t.Fatalf("CarryOutAction of the split of T2: %v", err)
	}
	if status := taskSplitTask(t, w.s, 2).Status; status != "open" {
		t.Errorf("T2 status after the refused split = %q, want open", status)
	}
	if actions, err = w.s.PendingActions(ctx); err != nil || len(actions) != 0 {
		t.Errorf("pending actions after the refusal = %d (%v), want none: the refusal settles the action",
			len(actions), err)
	}
	triggers, err := w.s.TurnTriggers(ctx, runEpic, 0)
	if err != nil {
		t.Fatalf("TurnTriggers: %v", err)
	}
	refusal := slices.ContainsFunc(triggers, func(trigger store.EpicTrigger) bool {
		return strings.Contains(trigger.Cause, "refused") && strings.Contains(trigger.Cause, "descendant of a split")
	})
	if !refusal {
		t.Errorf("pending triggers = %+v, want a refusal trigger naming the descendant of a split", triggers)
	}
}
