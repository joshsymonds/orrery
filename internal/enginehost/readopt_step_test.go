package enginehost

import (
	"context"
	"os"
	"os/exec"
	"slices"
	"syscall"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/holder"
	"github.com/joshsymonds/orrery/internal/store"
)

// newStepCrashRig returns a crashRig whose manager runs steps, with a step cap of one and the step agents resolved under
// its HOME. Its writer refuses from the first batch trigger accepts, as newCrashRig's does. Every holder under the rig's
// root is cleared when the test ends.
func newStepCrashRig(t *testing.T, trigger func([]store.EngineOp) bool) crashRig {
	t.Helper()
	s, path := openStore(t)
	root := holderRoot(t)
	t.Cleanup(func() { clearHolderDirs(t, root) })
	crash := &crashApplier{inner: s, trigger: trigger}
	writer := NewWriter(WriterConfig{Applier: crash, Tick: 5 * time.Millisecond})
	startRun(t, writer)
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Fatalf("find sh: %v", err)
	}
	home := t.TempDir()
	writeStepAgents(t, home)
	m := NewManager(Config{
		Shell:       sh,
		Claude:      fakeClaudePath(t),
		Environ:     []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home},
		Cap:         1,
		StepCap:     1,
		Writer:      writer,
		Now:         fixedClock,
		Notifier:    &fakeSink{},
		Store:       s,
		HolderScope: fakeSystemdRunPath(t),
		StateDir:    root,
	})
	t.Cleanup(m.Close)
	return crashRig{manager: m, store: s, path: path, root: root, crash: crash}
}

// stalledStep returns the step id in dir, as testStep does, with a prompt that stalls its turn.
func stalledStep(id, dir string) StepSession {
	step := testStep(id, dir)
	step.Prompt = "STALL"
	return step
}

// restartBeforeReconcile ends the manager of rig as a crash leaves its sessions, and returns a restarted manager that has
// not reconciled yet, with the notices it sends. The restart has a fresh writer on the same store and state dir.
func restartBeforeReconcile(t *testing.T, rig crashRig) (*Manager, *fakeSink) {
	t.Helper()
	rig.manager.Close()
	writer := NewWriter(WriterConfig{Applier: rig.store, Tick: 5 * time.Millisecond})
	startRun(t, writer)
	sink := &fakeSink{}
	return newStateManager(t, rig.store, writer, sink, fakeSystemdRunPath(t), rig.root, nil), sink
}

// requireStepSlots fails unless m holds steps step slots and no session slot.
func requireStepSlots(t *testing.T, m *Manager, steps int) {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stepsRunning != steps || m.reserved != 0 {
		t.Fatalf("slots = %d steps and %d sessions, want %d steps and no session", m.stepsRunning, m.reserved, steps)
	}
}

// turnEndTrigger accepts a batch that stores a turn's end.
func turnEndTrigger(ops []store.EngineOp) bool {
	return slices.ContainsFunc(ops, func(op store.EngineOp) bool {
		_, ok := op.(store.EngineTurnEnded)
		return ok
	})
}

// startReplyStep starts the step stepA in dir, whose first reply ends its turn in a batch the store refuses. The pump has
// read that turn end from the holder when the store refuses it, so the holder has spooled the reply and its turn end, and
// the store never stores the turn end. The step's row stays working.
func startReplyStep(t *testing.T, rig crashRig, dir string) {
	t.Helper()
	if err := rig.manager.StartStep(t.Context(), testStep(stepA, dir)); err != nil {
		t.Fatalf("StartStep: %v", err)
	}
	waitUntil(t, "the store refuses the batch of the reply's turn end", rig.crash.isRefusing)
}

// requireUndrained fails unless the step's row settles as a restart leaves a working row, so the turn end the holder
// spooled while down is not drained, and the new observer gets no turn end or exit and sends no notice.
func requireUndrained(t *testing.T, rig crashRig, sink *fakeSink, obs *stepObserver) {
	t.Helper()
	requireEngineRows(t, rig.store, map[string]restartRow{
		stepA: {state: "interrupted", reason: restartedReason},
	})
	if got := obs.returned(); got != 0 {
		t.Fatalf("turn ends handed to the new observer = %d, want 0", got)
	}
	if got := obs.exited(); len(got) != 0 {
		t.Fatalf("exits handed to the new observer = %q, want none", got)
	}
	if got := sink.got(); len(got) != 0 {
		t.Fatalf("notices after the restart = %+v, want none", got)
	}
}

func TestReadoptStepWithExpectedObserverGoesLive(t *testing.T) {
	rig := newStepCrashRig(t, nil)
	dir := t.TempDir()
	if err := rig.manager.StartStep(t.Context(), testStep(stepA, dir)); err != nil {
		t.Fatalf("StartStep: %v", err)
	}
	waitHistory(t, rig.path, stepA, "the step's first turn done", func(h store.EngineHistory) bool {
		return stepDone(t, h, "fake reply 1")
	})
	pid := fakePID(t, dir)

	rig.crash.refuse()
	restarted, sink := restartBeforeReconcile(t, rig)
	obs := &stepObserver{}
	restarted.ExpectStep(stepA, obs)
	if err := restarted.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile after the crash: %v", err)
	}

	requireProcessRunning(t, pid)
	if !restarted.StepLive(stepA) {
		t.Fatalf("step %s is not live after the restart", stepA)
	}
	requireStepSlots(t, restarted, 1)
	prompt := StepPrompt{Text: "again", Sender: store.PromptSenderOwner}
	if err := restarted.Continue(t.Context(), stepA, prompt); err != nil {
		t.Fatalf("Continue of the re-adopted step: %v", err)
	}
	waitHistory(t, rig.path, stepA, "the re-adopted step's reply done", func(h store.EngineHistory) bool {
		return stepDone(t, h, "fake reply 2")
	})
	// Stop waits for the step's pump, so the turn end handed to the observer has arrived when it returns.
	if err := restarted.Stop(t.Context(), stepA); err != nil {
		t.Fatalf("Stop of the re-adopted step: %v", err)
	}
	if got := obs.returned(); got != 1 {
		t.Fatalf("turn ends handed to the new observer = %d, want 1", got)
	}
	if got := sink.got(); len(got) != 0 {
		t.Fatalf("notices after the restart = %+v, want none", got)
	}
}

func TestReadoptStepWithoutExpectedObserverIsCleared(t *testing.T) {
	rig := newStepCrashRig(t, nil)
	dir := t.TempDir()
	if err := rig.manager.StartStep(t.Context(), stalledStep(stepA, dir)); err != nil {
		t.Fatalf("StartStep: %v", err)
	}
	waitUntil(t, "the stalled block's unfinished text", func() bool {
		return countRows(t, rig.path, "engine_text") == 1
	})
	pid := fakePID(t, dir)

	rig.crash.refuse()
	restarted, sink := restartBeforeReconcile(t, rig)
	if err := restarted.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile after the crash: %v", err)
	}

	gateWaitGone(t, pid)
	requireDirGone(t, holderDir(t, rig.root, stepA))
	if restarted.StepLive(stepA) {
		t.Fatalf("step %s is live after the restart without an expected observer", stepA)
	}
	requireStepSlots(t, restarted, 0)
	requireEngineRows(t, rig.store, map[string]restartRow{
		stepA: {state: "interrupted", reason: restartedReason},
	})
	if n := countRows(t, rig.path, "engine_text"); n != 0 {
		t.Fatalf("unfinished text rows after the restart = %d, want 0", n)
	}
	if got := sink.got(); len(got) != 0 {
		t.Fatalf("notices after the restart = %+v, want none", got)
	}
}

func TestReadoptStepEndedWhileDownIsClearedNotDrained(t *testing.T) {
	rig := newStepCrashRig(t, turnEndTrigger)
	dir := t.TempDir()
	startReplyStep(t, rig, dir)
	pid := fakePID(t, dir)

	restarted, sink := restartBeforeReconcile(t, rig)
	ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), managerGuard)
	defer cancel()
	if err := holder.Stop(ctx, holderDir(t, rig.root, stepA)); err != nil {
		t.Fatalf("stop holder of step %s: %v", stepA, err)
	}
	gateWaitGone(t, pid)
	obs := &stepObserver{}
	restarted.ExpectStep(stepA, obs)
	if err := restarted.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile after the crash: %v", err)
	}

	requireDirGone(t, holderDir(t, rig.root, stepA))
	if restarted.StepLive(stepA) {
		t.Fatalf("step %s is live after the restart, though its holder ended", stepA)
	}
	requireStepSlots(t, restarted, 0)
	requireUndrained(t, rig, sink, obs)
}

func TestReadoptStepUnattachableHolderIsClearedNotDrained(t *testing.T) {
	rig := newStepCrashRig(t, turnEndTrigger)
	dir := t.TempDir()
	startReplyStep(t, rig, dir)
	pid := fakePID(t, dir)
	pids := holderPids(t, holderDir(t, rig.root, stepA))

	restarted, sink := restartBeforeReconcile(t, rig)
	// A stopped holder keeps its lock and its socket, but never answers a hello.
	if err := syscall.Kill(pids.Holder, syscall.SIGSTOP); err != nil {
		t.Fatalf("stop holder %d: %v", pids.Holder, err)
	}
	obs := &stepObserver{}
	restarted.ExpectStep(stepA, obs)
	if err := restarted.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile after the crash: %v", err)
	}

	waitHolderEnded(t, pids)
	gateWaitGone(t, pid)
	requireDirGone(t, holderDir(t, rig.root, stepA))
	if restarted.StepLive(stepA) {
		t.Fatalf("step %s is live after the restart, though its holder cannot be attached", stepA)
	}
	requireStepSlots(t, restarted, 0)
	requireUndrained(t, rig, sink, obs)
}
