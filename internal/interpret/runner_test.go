package interpret

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/storetest"
	"github.com/joshsymonds/orrery/internal/workflow"
)

// The runner's fakes, and the real store, satisfy the interfaces the runner takes.
var (
	_ Planner = (*fakePlanner)(nil)
	_ Builder = (*fakeBuilder)(nil)
	_ Store   = (*store.Store)(nil)
)

const (
	runEpic  = "orrery-1a2b3c4/epic-runner"
	runRepo  = "/work/synthetic-repo"
	runBase  = "0123456789abcdef0123456789abcdef01234567"
	runPhase = "accepted"
)

// fakePlanner records the calls the runner makes to it. Its PassOnly names each of known that open reports true for,
// and afterPass, when set, runs once a pass returns.
type fakePlanner struct {
	known     []string
	calls     []string
	named     [][]string
	afterPass func()
}

func (p *fakePlanner) Recover(context.Context) {
	p.calls = append(p.calls, "recover")
}

func (p *fakePlanner) PassOnly(_ context.Context, open func(epic string) bool) {
	var named []string
	for _, epic := range p.known {
		if open(epic) {
			named = append(named, epic)
		}
	}
	p.named = append(p.named, named)
	p.calls = append(p.calls, "pass")
	if p.afterPass != nil {
		p.afterPass()
	}
}

func (p *fakePlanner) WaitTurns() {
	p.calls = append(p.calls, "wait")
}

// taskPass is one PassTasks call: the epic and the seqs it named.
type taskPass struct {
	epic string
	seqs []int64
}

// fakeBuilder records each PassTasks call.
type fakeBuilder struct {
	passes []taskPass
}

func (b *fakeBuilder) PassTasks(_ context.Context, id string, seqs []int64) {
	b.passes = append(b.passes, taskPass{epic: id, seqs: seqs})
}

// parkCounter is the store a test wraps to count the epic parks the runner writes, and their reasons.
type parkCounter struct {
	Store
	parks []string
}

func (c *parkCounter) ParkEpic(
	ctx context.Context,
	epic string,
	class store.ParkClass,
	reason string,
	at time.Time,
) error {
	c.parks = append(c.parks, reason)
	return c.Store.ParkEpic(ctx, epic, class, reason, at)
}

// runClock is the time the runner's clock reads, so every start, end and park is stamped at a known second.
func runClock() time.Time {
	return time.Unix(1700002000, 0)
}

// openRunStore opens a migrated store file for one test and returns it.
func openRunStore(t *testing.T) *store.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "orrery.db")
	storetest.Seed(t, path)
	s, err := store.Open(context.Background(), path, storetest.NoSync())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return s
}

// builtinPin returns the built-in epic as an epic pins it: the name, text and sha256 Load gives, and the nodes the
// store keeps for it in preorder.
func builtinPin(t *testing.T) store.EpicWorkflow {
	t.Helper()
	pinned, err := workflow.Load("epic", noRepoFile)
	if err != nil {
		t.Fatalf("Load built-in epic: %v", err)
	}
	return store.EpicWorkflow{
		Name: pinned.Name, Text: pinned.Text, SHA256: pinned.SHA256,
		Nodes: []store.EpicWorkflowNode{
			{ID: "red", Kind: "evidence"},
			{ID: "cost", Kind: "cost"},
			{ID: "plan", Kind: "plan"},
			{ID: "build", Parent: "plan", Kind: "build"},
			{ID: "integrate", Parent: "plan", Kind: "integrate"},
			{ID: "review", Kind: "review"},
			{ID: "correct", Kind: "loop"},
			{ID: "fix", Parent: "correct", Kind: "plan"},
			{ID: "rebuild", Parent: "fix", Kind: "build"},
			{ID: "reintegrate", Parent: "fix", Kind: "integrate"},
			{ID: "closure", Parent: "correct", Kind: "closure"},
			{ID: "conclude", Kind: "conclude"},
			{ID: "release", Kind: "release"},
		},
	}
}

// planBuildPin returns the pin of a workflow of its own: a plan holding build, which the built-in epic was before red
// and integrate. The tests of plan and build mechanics pin it, so a change to the built-in epic does not change them.
func planBuildPin() store.EpicWorkflow {
	text := "name: plan-build\nnodes:\n  - id: plan\n    kind: plan\n    body:\n" +
		"      - id: build\n        kind: build\n"
	return store.EpicWorkflow{
		Name: "plan-build", Text: text, SHA256: sha256Hex(text),
		Nodes: []store.EpicWorkflowNode{{ID: "plan", Kind: "plan"}, {ID: "build", Parent: "plan", Kind: "build"}},
	}
}

// pinnedText returns the pin of an epic that pins text under sha256, with the built-in's name and nodes.
func pinnedText(original store.EpicWorkflow, text, sum string) store.EpicWorkflow {
	return store.EpicWorkflow{Name: original.Name, Text: text, SHA256: sum, Nodes: original.Nodes}
}

// sha256Hex returns the lowercase hex sha256 of text.
func sha256Hex(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// createRunEpic stores the test epic pinned to pin, in the accepted phase with a base, so its build runs may start.
func createRunEpic(t *testing.T, s *store.Store, pin store.EpicWorkflow) {
	t.Helper()
	if err := s.CreateEpic(context.Background(), store.EpicRow{
		ID: runEpic, Repository: runRepo, Base: runBase, Title: "Run the workflow", Phase: runPhase,
		Created: runClock(), Changed: runClock(), Workflow: pin,
	}); err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
}

// newRunner returns a runner over s with the planner and builder given, the clock at runClock, and a tick an hour
// long, so Run passes once unless a test ends it first.
func newRunner(s Store, planner Planner, builder Builder) *Runner {
	return New(Config{Store: s, Planner: planner, Builder: builder, Tick: time.Hour, Now: runClock})
}

// epicOf returns the test epic as the store holds it.
func epicOf(t *testing.T, s *store.Store) store.EpicRow {
	t.Helper()
	row, err := s.Epic(context.Background(), runEpic)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	return row
}

// runsOf returns the node runs of the test epic.
func runsOf(t *testing.T, s *store.Store) []store.NodeRun {
	t.Helper()
	runs, err := s.NodeRuns(context.Background(), runEpic)
	if err != nil {
		t.Fatalf("NodeRuns: %v", err)
	}
	return runs
}

// runAt returns the node run at path of the test epic, and fails the test when there is none.
func runAt(t *testing.T, s *store.Store, path string) store.NodeRun {
	t.Helper()
	for _, run := range runsOf(t, s) {
		if run.Path == path {
			return run
		}
	}
	t.Fatalf("no node run at %s", path)
	return store.NodeRun{}
}

// runTask returns a planner's task titled title, on file, that carries Requirement R1 and its evidence item.
func runTask(title, file string) store.EpicTaskInput {
	return store.EpicTaskInput{
		Title: title, Deliverable: "the runner", Files: []string{file}, Requirements: []string{"R1"}, Gate: "check",
		Evidence: evidence.Item{
			Capture: []string{"scripts/check"}, Limit: 60, Artifacts: []string{}, Supplied: []string{},
			Conclusion: "the runner runs", Judge: "a judge",
		},
	}
}

// runPlan returns a planner return of two tasks, the first on runner.go and the second on decide.go, with outcome.
func runPlan(outcome string) store.EpicPlan {
	return store.EpicPlan{
		Decisions: []string{},
		Tasks: []store.EpicTaskInput{
			runTask("first", "internal/interpret/runner.go"), runTask("second", "internal/interpret/decide.go"),
		},
		Outline: "two tasks", Outcome: outcome,
	}
}

// completeTurn claims the test epic's next turn and completes it with plan.
func completeTurn(t *testing.T, s *store.Store, plan store.EpicPlan) {
	t.Helper()
	ctx := context.Background()
	turn, err := s.ClaimTurn(ctx, runEpic, runClock())
	if err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	if err = s.CompleteTurn(ctx, runEpic, turn.Seq, plan, runClock()); err != nil {
		t.Fatalf("CompleteTurn of turn %d: %v", turn.Seq, err)
	}
}

// TestNewEpicOnBuiltInCapturesRedThenStartsPlan: a new epic pinned to the built-in epic gets its red run at base
// captured and passed by the first pass, then its cost run measured at base, which a check with no Done gate records
// as not run, then its plan run stored, the planner is passed for that epic, and no build run is handed out.
func TestNewEpicOnBuiltInCapturesRedThenStartsPlan(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	// The cost node prepares its run at the epic's repository, so the epic is on a repository that exists.
	repo, base := nodeRepo(t)
	s := openRunStore(t)
	if err := s.CreateEpic(ctx, store.EpicRow{
		ID: runEpic, Repository: repo, Base: base, Title: "Run the workflow", Phase: runPhase,
		Created: runClock(), Changed: runClock(), Workflow: builtinPin(t),
	}); err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
	planner, builder := &fakePlanner{known: []string{runEpic}}, &fakeBuilder{}
	runner := New(Config{
		Store: s, Planner: planner, Builder: builder, Checks: &fakeChecks{store: s}, Tick: time.Hour, Now: runClock,
	})
	runner.Pass(ctx)

	got := runsOf(t, s)
	// The cost run's refs are the snapshot of the repository, which this test does not fix.
	for i := range got {
		if got[i].Path == "cost" {
			got[i].Refs = ""
		}
	}
	want := []store.NodeRun{
		{
			Path: "cost", ID: store.NodeRunID(runEpic, "cost", 0, "start"), Node: "cost", Kind: "cost",
			State: store.NodeEnded, Outcome: "passed", Output: "no Done gate to measure at the base",
			Revision: base, RefsChecked: true, Started: runClock(), Ended: runClock(),
		},
		{
			Path: "plan", ID: store.NodeRunID(runEpic, "plan", 0, "start"), Node: "plan", Kind: "plan",
			State: store.NodeRunning, Started: runClock(),
		},
		{
			Path: "red", ID: store.NodeRunID(runEpic, "red", 0, "start"), Node: "red", Kind: "evidence",
			State: store.NodeEnded, Outcome: "passed", Output: "no Requirement names an evidence item",
			Started: runClock(), Ended: runClock(),
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("node runs = %+v, want %+v", got, want)
	}
	if wantNamed := [][]string{{runEpic}}; !reflect.DeepEqual(planner.named, wantNamed) {
		t.Errorf("PassOnly named %v, want %v", planner.named, wantNamed)
	}
	if len(builder.passes) != 0 {
		t.Errorf("PassTasks calls = %+v, want none before a turn completes", builder.passes)
	}
}

// TestCompletedTurnPassesTasksThenEndsPlan: a completed turn's two tasks reach the builder by seq, the plan ends
// complete once they are accepted, and later passes hand out nothing and leave the epic's phase as it is.
func TestCompletedTurnPassesTasksThenEndsPlan(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunStore(t)
	createRunEpic(t, s, planBuildPin())
	planner, builder := &fakePlanner{known: []string{runEpic}}, &fakeBuilder{}
	runner := newRunner(s, planner, builder)
	runner.Pass(ctx)
	completeTurn(t, s, runPlan("complete"))

	runner.Pass(ctx)
	if want := []taskPass{{epic: runEpic, seqs: []int64{1, 2}}}; !reflect.DeepEqual(builder.passes, want) {
		t.Fatalf("PassTasks calls = %+v, want %+v", builder.passes, want)
	}
	for _, seq := range []int64{1, 2} {
		path := fmt.Sprintf("plan/build/T%d", seq)
		if run := runAt(t, s, path); run.Kind != "build" || run.Task != seq || run.State != store.NodeRunning {
			t.Errorf("child %s = %+v, want a running build run of task %d", path, run, seq)
		}
	}

	for _, seq := range []int64{1, 2} {
		if err := s.AcceptTask(ctx, runEpic, seq, store.TaskBuild{}, "reviewed"); err != nil {
			t.Fatalf("AcceptTask %d: %v", seq, err)
		}
	}
	runner.Pass(ctx)
	plan := runAt(t, s, "plan")
	if plan.State != store.NodeEnded || plan.Outcome != "complete" || !plan.Ended.Equal(runClock()) {
		t.Errorf("plan = %+v, want ended complete at the clock", plan)
	}
	for _, path := range []string{"plan/build/T1", "plan/build/T2"} {
		if run := runAt(t, s, path); run.State != store.NodeEnded {
			t.Errorf("child %s = %+v, want ended", path, run)
		}
	}
	settled := runsOf(t, s)

	runner.Pass(ctx)
	if !reflect.DeepEqual(runsOf(t, s), settled) {
		t.Errorf("node runs changed on a later pass: %+v, want %+v", runsOf(t, s), settled)
	}
	if want := []taskPass{{epic: runEpic, seqs: []int64{1, 2}}}; !reflect.DeepEqual(builder.passes, want) {
		t.Errorf("PassTasks calls = %+v, want only %+v", builder.passes, want)
	}
	if wantNamed := [][]string{{runEpic}, {runEpic}, nil, nil}; !reflect.DeepEqual(planner.named, wantNamed) {
		t.Errorf("PassOnly named %v, want %v", planner.named, wantNamed)
	}
	if got := epicOf(t, s).Phase; got != runPhase {
		t.Errorf("epic phase = %q, want %q unchanged", got, runPhase)
	}
}

// TestTasksPlannedBeforeAnyPlanRunGetChildren: an epic whose open tasks were planned before any plan run existed gets
// their build children from the pass, under the ids a completed turn gives them (E8).
func TestTasksPlannedBeforeAnyPlanRunGetChildren(t *testing.T) {
	t.Parallel()
	s := openRunStore(t)
	createRunEpic(t, s, planBuildPin())
	completeTurn(t, s, runPlan("work"))
	planner, builder := &fakePlanner{known: []string{runEpic}}, &fakeBuilder{}
	newRunner(s, planner, builder).Pass(context.Background())

	want := []store.NodeRun{
		{
			Path: "plan", ID: store.NodeRunID(runEpic, "plan", 0, "start"), Node: "plan", Kind: "plan",
			State: store.NodeRunning, Started: runClock(),
		},
		{
			Path: "plan/build/T1", ID: store.NodeRunID(runEpic, "plan/build/T1", 0, "turn 1 task 1"), Node: "build",
			Kind: "build", Task: 1, State: store.NodeRunning, Started: runClock(),
		},
		{
			Path: "plan/build/T2", ID: store.NodeRunID(runEpic, "plan/build/T2", 0, "turn 1 task 2"), Node: "build",
			Kind: "build", Task: 2, State: store.NodeRunning, Started: runClock(),
		},
	}
	if got := runsOf(t, s); !reflect.DeepEqual(got, want) {
		t.Errorf("node runs = %+v, want %+v", got, want)
	}
	if wantTasks := []taskPass{{epic: runEpic, seqs: []int64{1, 2}}}; !reflect.DeepEqual(builder.passes, wantTasks) {
		t.Errorf("PassTasks calls = %+v, want %+v", builder.passes, wantTasks)
	}
	if wantNamed := [][]string{{runEpic}}; !reflect.DeepEqual(planner.named, wantNamed) {
		t.Errorf("PassOnly named %v, want %v", planner.named, wantNamed)
	}
}

// TestUnloadablePinnedTextParksEpicOnce: a pinned text whose hash or check fails parks the unparked epic once, with
// the workflow's reason, and runs nothing for it.
func TestUnloadablePinnedTextParksEpicOnce(t *testing.T) {
	t.Parallel()
	original := builtinPin(t)
	tampered := strings.Replace(original.Text, "id: build", "id: bulid", 1)
	duplicated := strings.Replace(original.Text, "id: build", "id: plan", 1)
	hashReason := "workflow epic: text sha256 " + sha256Hex(tampered) + " does not match the pinned " + original.SHA256
	cases := []struct {
		name       string
		pin        store.EpicWorkflow
		wantReason string
	}{
		{
			name:       "hash",
			pin:        pinnedText(original, tampered, original.SHA256),
			wantReason: hashReason,
		},
		{
			name:       "check",
			pin:        pinnedText(original, duplicated, sha256Hex(duplicated)),
			wantReason: "workflow epic: ",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := openRunStore(t)
			createRunEpic(t, s, tc.pin)
			planner, builder := &fakePlanner{known: []string{runEpic}}, &fakeBuilder{}
			counter := &parkCounter{Store: s}
			runner := newRunner(counter, planner, builder)
			runner.Pass(context.Background())
			runner.Pass(context.Background())

			if len(counter.parks) != 1 {
				t.Fatalf("ParkEpic calls = %q, want one", counter.parks)
			}
			if !strings.HasPrefix(counter.parks[0], tc.wantReason) {
				t.Errorf("park reason = %q, want prefix %q", counter.parks[0], tc.wantReason)
			}
			if got := epicOf(t, s).Parked; got != counter.parks[0] {
				t.Errorf("epic Parked = %q, want the park reason %q", got, counter.parks[0])
			}
			if runs := runsOf(t, s); len(runs) != 0 {
				t.Errorf("node runs = %+v, want none for an unloadable epic", runs)
			}
			if wantNamed := [][]string{nil, nil}; !reflect.DeepEqual(planner.named, wantNamed) {
				t.Errorf("PassOnly named %v, want %v", planner.named, wantNamed)
			}
			if len(builder.passes) != 0 {
				t.Errorf("PassTasks calls = %+v, want none", builder.passes)
			}
		})
	}
}

// TestRunRecoversPassesAndWaitsTurnsWhenCtxEnds: Run recovers the planner's turns, passes, and once ctx ends after that
// pass, waits for the planner's turns before it returns.
func TestRunRecoversPassesAndWaitsTurnsWhenCtxEnds(t *testing.T) {
	t.Parallel()
	s := openRunStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	planner := &fakePlanner{afterPass: cancel}
	newRunner(s, planner, &fakeBuilder{}).Run(ctx)

	if want := []string{"recover", "pass", "wait"}; !reflect.DeepEqual(planner.calls, want) {
		t.Errorf("planner calls = %v, want %v", planner.calls, want)
	}
}

// TestLoopMovesToNextRoundThenEnds: a loop whose review does not report done in round 1 moves to round 2, where the
// review reports done, and the loop ends there.
func TestLoopMovesToNextRoundThenEnds(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunStore(t)
	createRunEpic(t, s, store.EpicWorkflow{})
	runner := newRunner(s, &fakePlanner{}, &fakeBuilder{})
	runner.kinds = refineKinds("", "done")
	flow := workflow.Workflow{Name: "refine", Nodes: []workflow.Node{refine("park")}}
	if runner.passFlow(ctx, store.EpicRow{ID: runEpic}, flow) {
		t.Error("passFlow reports a plan run, want none: the flow has no plan")
	}

	if loop := runAt(t, s, "refine"); loop.State != store.NodeEnded || loop.LoopRound != 2 {
		t.Errorf("loop = %+v, want ended in round 2", loop)
	}
	if got := runAt(t, s, "refine@2/review").Outcome; got != "done" {
		t.Errorf("review of round 2 outcome = %q, want done", got)
	}
}

// TestLoopSpentParksLoopAndEpic: a loop whose rounds run out without its until outcome parks, and parks its epic with
// the loop's reason.
func TestLoopSpentParksLoopAndEpic(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunStore(t)
	createRunEpic(t, s, store.EpicWorkflow{})
	runner := newRunner(s, &fakePlanner{}, &fakeBuilder{})
	runner.kinds = refineKinds("", "")
	flow := workflow.Workflow{Name: "refine", Nodes: []workflow.Node{refine("park")}}
	if runner.passFlow(ctx, store.EpicRow{ID: runEpic}, flow) {
		t.Error("passFlow reports a plan run, want none: the flow has no plan")
	}

	if loop := runAt(t, s, "refine"); loop.State != store.NodeParked || loop.LoopRound != 2 {
		t.Errorf("loop = %+v, want parked in round 2", loop)
	}
	want := "loop refine spent its 2 rounds without review.done"
	if got := epicOf(t, s).Parked; got != want {
		t.Errorf("epic Parked = %q, want %q", got, want)
	}
}
