package enginehost

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/engine/session"
	"github.com/joshsymonds/orrery/internal/store"
)

// handClock is the clock a parent rig reads. The test moves it with advance, so an idle check sees time pass without a
// real clock.
type handClock struct {
	mu  sync.Mutex
	now time.Time
}

// Now returns the clock's instant.
func (c *handClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// advance moves the clock forward by d.
func (c *handClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// callerSpy is the step store a parent's Steps reads through. It records each RunningCallerSteps read by its caller, and
// otherwise reads through the store it embeds. A non-nil fail is returned by every RunningCallerSteps read.
type callerSpy struct {
	StepStore
	fail  error
	mu    sync.Mutex
	reads []string
}

// RunningCallerSteps records the read of caller's running steps, then returns the embedded store's answer, or fail.
func (s *callerSpy) RunningCallerSteps(ctx context.Context, caller string) ([]store.StepRow, error) {
	s.mu.Lock()
	s.reads = append(s.reads, caller)
	s.mu.Unlock()
	if s.fail != nil {
		return nil, s.fail
	}
	return s.StepStore.RunningCallerSteps(ctx, caller)
}

// readsOf returns how many reads of caller's running steps the spy has recorded.
func (s *callerSpy) readsOf(caller string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, read := range s.reads {
		if read == caller {
			n++
		}
	}
	return n
}

// parentRig is a manager at a cap of two sessions and two steps, and the Steps that starts its steps. The Steps reads the
// store through a callerSpy, and the manager's clock is a handClock the test moves by hand.
type parentRig struct {
	manager   *Manager
	steps     *Steps
	spy       *callerSpy
	clock     *handClock
	worktrees string
}

// newParentRig returns a parent rig over a fresh store. Its holders are cleared when the test ends.
func newParentRig(t *testing.T) parentRig {
	t.Helper()
	s, _ := openStore(t)
	root, gateRoot := holderRoot(t), holderRoot(t)
	t.Cleanup(func() {
		clearHolderDirs(t, root)
		clearHolderDirs(t, gateRoot)
	})
	writer := NewWriter(WriterConfig{Applier: s})
	startRun(t, writer)
	home := t.TempDir()
	writeStepAgents(t, home)
	environ := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home}
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Fatalf("find sh: %v", err)
	}
	clock := &handClock{now: fixedClock()}
	scope := fakeSystemdRunPath(t)
	manager := NewManager(Config{
		Shell: sh, Claude: fakeClaudePath(t), Environ: environ, Cap: 2, StepCap: 2, Writer: writer, Now: clock.Now,
		Notifier: &fakeSink{}, Store: s, HolderScope: scope, StateDir: root,
	})
	t.Cleanup(manager.Close)
	spy := &callerSpy{StepStore: s}
	worktrees := t.TempDir()
	steps, err := NewSteps(StepsConfig{
		Sessions: manager, Store: spy, Writer: writer, Worktrees: worktrees, Environ: environ,
		Logger: slog.New(slog.DiscardHandler), Now: fixedClock, GateStateDir: gateRoot, GateScope: scope,
	})
	if err != nil {
		t.Fatalf("NewSteps: %v", err)
	}
	t.Cleanup(steps.Close)
	return parentRig{manager: manager, steps: steps, spy: spy, clock: clock, worktrees: worktrees}
}

// startParent starts a read-only step of repo under prompt, whose return returnCheck checks when it is set. It returns
// the step's id.
func (r parentRig) startParent(t *testing.T, repo, prompt string, returnCheck func(json.RawMessage) []string) string {
	t.Helper()
	start := stepStartReadOnly(repo, "")
	start.Prompt = prompt
	start.ReturnCheck = returnCheck
	id, err := r.steps.Start(t.Context(), start)
	if err != nil {
		t.Fatalf("Start of a %s step: %v", prompt, err)
	}
	return id
}

// startChild starts a read-only step under parent whose first prompt never ends its turn, so it runs until it is stopped.
// It returns the step's id.
func (r parentRig) startChild(t *testing.T, repo, parent string) string {
	t.Helper()
	start := stepStartReadOnly(repo, "")
	start.Prompt = "STALL"
	start.Caller = store.EpicCaller{Session: parent}
	id, err := r.steps.Start(t.Context(), start)
	if err != nil {
		t.Fatalf("Start of a child of step %s: %v", parent, err)
	}
	return id
}

// releaseStep writes the fakeclaude.release file that the fake claude of the step id waits for, in its worktree.
func (r parentRig) releaseStep(t *testing.T, id string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(r.worktrees, id, "fakeclaude.release"), nil, 0o600); err != nil {
		t.Fatalf("write fakeclaude.release of step %s: %v", id, err)
	}
}

// settleAwaited waits until run has read the running steps of parent, which a turn end that is not done does, and then
// returns once run has finished handling that turn end. The no-op owner call is taken only once the loop is back at its
// select, so the turn end has been handled when settleAwaited returns.
func settleAwaited(t *testing.T, eng engine, spy *callerSpy, run *stepRun, parent string) {
	t.Helper()
	waitUntil(t, eng, "the running steps of "+parent+" to be read", func() bool { return spy.readsOf(parent) > 0 })
	if err := run.do(t.Context(), func(context.Context) error { return nil }); err != nil {
		t.Fatalf("settle the loop of step %s: %v", parent, err)
	}
}

// requireUnreturned fails the test unless the step id is open with no return, check or round stored.
func requireUnreturned(t *testing.T, steps *Steps, id string) {
	t.Helper()
	row := stepRowOf(t, steps, id)
	if row.End != "" || row.Result != "" || len(row.Checks) != 0 || row.LoopRound != 0 {
		t.Fatalf("step %s: end %q result %q with %d checks in round %d, want open with no return, check or round",
			id, row.End, row.Result, len(row.Checks), row.LoopRound)
	}
}

// TestParentStepOpenNotDoneTurnWithChildStaysOpen covers a not-done return handed to the run while a step it started
// runs. The return is neither checked nor judged, nothing is stored for it, and the step stays open. The test hands the
// run the return itself, since the fake claude ends no turn with a not-done return and no background task.
func TestParentStepOpenNotDoneTurnWithChildStaysOpen(t *testing.T) {
	t.Parallel()
	rig := newParentRig(t)
	repo := stepGitTestRepo(t)
	parent := rig.startParent(t, repo, "STALL", nil)
	child := rig.startChild(t, repo, parent)
	run := stepsRunOf(t, rig.steps, parent)
	run.Return(session.TurnEnded{
		StructuredOutput: json.RawMessage(`{"status":"needs_context","summary":"waiting on a child"}`),
	})
	settleAwaited(t, engineOf(rig.manager, rig.steps), rig.spy, run, parent)
	requireUnreturned(t, rig.steps, parent)
	if row := stepRowOf(t, rig.steps, child); row.End != "" {
		t.Fatalf("child %s ended %q, want it still running", child, row.End)
	}
}

// TestParentStepOpenNotDoneWithoutChildEnds covers a not-done return with no step running under it, which ends the step
// returned as it does without any child.
func TestParentStepOpenNotDoneWithoutChildEnds(t *testing.T) {
	t.Parallel()
	rig := newParentRig(t)
	parent := rig.startParent(t, stepGitTestRepo(t), "STALL", nil)
	stepsRunOf(t, rig.steps, parent).Return(session.TurnEnded{
		StructuredOutput: json.RawMessage(`{"status":"needs_context","summary":"no child"}`),
	})
	var row store.StepRow
	waitUntil(t, engineOf(rig.manager, rig.steps), "the step to end", func() bool {
		row = stepRowOf(t, rig.steps, parent)
		return row.End != ""
	})
	if row.End != stepLoopEndReturned || row.Status != "needs_context" {
		t.Fatalf("end = %q status %q, want returned needs_context", row.End, row.Status)
	}
}

// TestParentStepOpenDoneReturnEndsWhileChildRuns covers a done return while a step it started runs: the step ends done
// at once, and the child is left running.
func TestParentStepOpenDoneReturnEndsWhileChildRuns(t *testing.T) {
	t.Parallel()
	rig := newParentRig(t)
	repo := stepGitTestRepo(t)
	passes := func(json.RawMessage) []string { return nil }
	parent := rig.startParent(t, repo, "READDONE one", passes)
	child := rig.startChild(t, repo, parent)
	rig.releaseStep(t, parent)
	var row store.StepRow
	waitUntil(t, engineOf(rig.manager, rig.steps), "the step to end", func() bool {
		row = stepRowOf(t, rig.steps, parent)
		return row.End != ""
	})
	if row.End != stepLoopEndDone || row.Status != stepDefaultDone {
		t.Fatalf("end = %q status %q, want done %q", row.End, row.Status, stepDefaultDone)
	}
	if open := stepRowOf(t, rig.steps, child); open.End != "" {
		t.Fatalf("child %s ended %q with its parent, want it still running", child, open.End)
	}
}

// TestParentStepOpenReadoptedNotDoneWaitsForChild covers a re-adopted step whose stored latest return is not done while a
// step it started runs. Its resume neither judges nor ends it, and the step waits with no turn running.
func TestParentStepOpenReadoptedNotDoneWaitsForChild(t *testing.T) {
	t.Parallel()
	f := newStepLoopFixture(t, nil)
	const child = "parent-open-child"
	if err := f.store.ApplyEngine(t.Context(), []store.EngineOp{
		store.EngineAdd{ID: child, Dir: "/work", State: "working", Kind: store.KindStep},
		store.StepAdd{ID: child, Agent: "reviewer", Prompt: "look", Caller: store.EpicCaller{Session: stepLoopID}},
	}, time.Unix(2, 0)); err != nil {
		t.Fatalf("add child step: %v", err)
	}
	spy := &callerSpy{StepStore: f.store}
	f.run.store = spy
	row := f.commit(t,
		store.StepReturn{ID: stepLoopID, Result: `{"status":"needs_context"}`, Checks: []store.StepCheck{
			{Round: 1, Name: stepLoopSchemaName, OK: true},
		}},
		store.StepLoop{ID: stepLoopID, Round: 1},
	)
	f.resumeFrom(stepRestore{Row: row, Turn: store.StepTurnEnd{Seq: 3, Returned: true}, Found: true})
	f.start(t)
	settleAwaited(t, engineOf(f.run), spy, f.run, stepLoopID)
	got := f.row(t)
	if got.End != "" || got.LoopRound != 1 || len(got.Checks) != 1 || got.Result != `{"status":"needs_context"}` {
		t.Fatalf("end %q round %d checks %d result %q, want open at round 1 with its stored return alone",
			got.End, got.LoopRound, len(got.Checks), got.Result)
	}
	if texts := f.sessions.continuedTexts(); texts != nil {
		t.Fatalf("Continue texts = %q, want none: the re-adopted return was not judged", texts)
	}
}

// TestParentStepOpenReadFailureKeepsItOpen covers a read of the running steps that fails on a not-done return. The step
// stays open and the return is not judged.
func TestParentStepOpenReadFailureKeepsItOpen(t *testing.T) {
	t.Parallel()
	f := newStepLoopFixture(t, nil)
	spy := &callerSpy{StepStore: f.store, fail: errors.New("read running steps")}
	f.run.store = spy
	f.start(t)
	f.run.Return(session.TurnEnded{StructuredOutput: json.RawMessage(`{"status":"needs_context"}`)})
	settleAwaited(t, engineOf(f.run), spy, f.run, stepLoopID)
	got := f.row(t)
	if got.End != "" || got.Result != "" || len(got.Checks) != 0 {
		t.Fatalf("end %q result %q with %d checks, want open and unreturned after a failed read",
			got.End, got.Result, len(got.Checks))
	}
}
