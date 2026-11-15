package enginehost

import (
	"errors"
	"os"
	"os/exec"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/engine/session"
	"github.com/joshsymonds/orrery/internal/holder"
	"github.com/joshsymonds/orrery/internal/store"
)

// restartStepDrain ends the step manager of rig as a crash leaves it, and returns a manager over the same store and state
// dir. Its step cap is stepCap, its controller speaks holder wire version version, and its notices go to the returned
// sink. The caller runs Reconcile.
func restartStepDrain(t *testing.T, rig crashRig, version, stepCap int) (*Manager, *fakeSink) {
	t.Helper()
	rig.manager.Close()
	writer := NewWriter(WriterConfig{Applier: rig.store, Tick: 5 * time.Millisecond})
	startRun(t, writer)
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Fatalf("find sh: %v", err)
	}
	home := t.TempDir()
	writeStepAgents(t, home)
	sink := &fakeSink{}
	m := NewManager(Config{
		Shell:         sh,
		Claude:        fakeClaudePath(t),
		Environ:       []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home},
		Cap:           1,
		StepCap:       stepCap,
		Writer:        writer,
		Now:           fixedClock,
		Notifier:      sink,
		Store:         rig.store,
		HolderScope:   fakeSystemdRunPath(t),
		StateDir:      rig.root,
		HolderVersion: version,
	})
	t.Cleanup(m.Close)
	return m, sink
}

// startHeldStep starts the step stepA in dir on the step manager of rig, with obs as its observer. Its turn holds until
// fakeclaude.release exists in dir, so the test decides when it ends. It returns once the fake claude has read the
// prompt, so the turn is in flight in its holder.
func startHeldStep(t *testing.T, rig crashRig, dir string, obs StepObserver) {
	t.Helper()
	step := testStep(stepA, dir)
	step.Prompt = "JUDGEHOLDS"
	step.Observer = obs
	if err := rig.manager.StartStep(t.Context(), step); err != nil {
		t.Fatalf("StartStep: %v", err)
	}
	waitUntil(t, "the fake claude to read the held prompt", func() bool {
		return stdinLinesWith(t, dir, "JUDGEHOLDS") == 1
	})
}

// waitStoppingEnded waits until the pump of the retired session id has ended, so nothing it records is still to come.
func waitStoppingEnded(t *testing.T, m *Manager, id string) {
	t.Helper()
	waitUntil(t, "the stopped session "+id+" to end its pump", func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		return m.stopping[id] == nil
	})
}

// drainHeldStep starts the held step in dir with obs as its observer, then restarts the step manager of rig with stepCap
// and a controller one version newer than the step's holder, and re-adopts the step mid-round. It ends the held turn and
// waits for the hand-off to reach obs, for the holder and its claude to end, and for the step's pump to end. The step is
// then drained, with nothing more to record. It returns the restarted manager and its notice sink.
func drainHeldStep(t *testing.T, rig crashRig, dir string, obs *stepObserver, stepCap int) (*Manager, *fakeSink) {
	t.Helper()
	startHeldStep(t, rig, dir, obs)
	pid := fakePID(t, dir)
	pids := holderPids(t, holderDir(t, rig.root, stepA))
	restarted, sink := restartStepDrain(t, rig, holder.WireVersion+1, stepCap)
	restarted.ExpectStep(stepA, obs)
	if err := restarted.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if !restarted.StepLive(stepA) {
		t.Fatalf("step %s is not live after the restart, want its holder re-adopted mid-round", stepA)
	}
	releaseHeldTurn(t, dir)
	waitReturns(t, obs, 1)
	waitHolderEnded(t, pids)
	gateWaitGone(t, pid)
	waitStoppingEnded(t, restarted, stepA)
	return restarted, sink
}

// TestReadoptOneVersionOlderStepStopsAtHandOff re-adopts a step mid-round from a holder one version older. The round's end
// is recorded on that holder and handed to the observer, then the holder and its claude stop. The stop records nothing,
// and the step keeps its slot for its id.
func TestReadoptOneVersionOlderStepStopsAtHandOff(t *testing.T) {
	t.Parallel()
	rig := newStepCrashRig(t, nil)
	dir := t.TempDir()
	obs := &stepObserver{}
	restarted, sink := drainHeldStep(t, rig, dir, obs, 1)

	waitHistory(t, rig.path, stepA, "the held turn's end recorded", func(h store.EngineHistory) bool {
		return h.State == "done"
	})
	if got := obs.returned(); got != 1 {
		t.Fatalf("turn ends handed to the observer = %d, want 1", got)
	}
	if got := obs.exited(); len(got) != 0 {
		t.Fatalf("exits handed to the observer = %q, want none for the stop", got)
	}
	if slices.Contains(restarted.Live(), stepA) {
		t.Fatalf("session %s is live after its hand-off, want its holder stopped", stepA)
	}
	requireStepSlots(t, restarted, 1)
	row, err := rig.store.EngineSession(t.Context(), stepA)
	if err != nil {
		t.Fatalf("EngineSession: %v", err)
	}
	if row.State != "done" || row.Reason != "" {
		t.Fatalf("row after the stop = state %q reason %q, want done with no reason", row.State, row.Reason)
	}
	if got := sink.got(); len(got) != 0 {
		t.Fatalf("notices after the stop = %+v, want none", got)
	}
}

// TestReadoptIdleOneVersionOlderStepKeepsItsSlotForContinue re-adopts a step whose turn ended before the restart, from a
// holder one version older. The restart stops that holder at once and keeps the step's slot, and the step stays held, so
// its loop still runs and its next Continue respawns it with --resume.
func TestReadoptIdleOneVersionOlderStepKeepsItsSlotForContinue(t *testing.T) {
	t.Parallel()
	rig := newStepCrashRig(t, nil)
	dir := t.TempDir()
	if err := rig.manager.StartStep(t.Context(), testStep(stepA, dir)); err != nil {
		t.Fatalf("StartStep: %v", err)
	}
	waitHistory(t, rig.path, stepA, "the step's first turn done", func(h store.EngineHistory) bool {
		return stepDone(t, h, "fake reply 1")
	})
	pid := fakePID(t, dir)
	pids := holderPids(t, holderDir(t, rig.root, stepA))

	restarted, _ := restartStepDrain(t, rig, holder.WireVersion+1, 1)
	restarted.ExpectStep(stepA, &stepObserver{})
	if err := restarted.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	waitHolderEnded(t, pids)
	gateWaitGone(t, pid)
	waitStoppingEnded(t, restarted, stepA)
	if slices.Contains(restarted.Live(), stepA) {
		t.Fatalf("session %s is live after the restart, want its idle holder stopped", stepA)
	}
	if !restarted.StepLive(stepA) {
		t.Fatalf("step %s is not held after the restart, want its slot kept for its next Continue", stepA)
	}
	requireStepSlots(t, restarted, 1)

	prompt := StepPrompt{Text: "again", Sender: store.PromptSenderOwner}
	if err := restarted.Continue(t.Context(), stepA, prompt); err != nil {
		t.Fatalf("Continue of the kept step: %v", err)
	}
	waitHistory(t, rig.path, stepA, "the respawned step's reply done", func(h store.EngineHistory) bool {
		return stepDone(t, h, "fake reply 2")
	})
	args := fakeArgs(t, dir)
	if len(args) != 2 || !hasFlagValue(args[1], "--resume", "session_1") {
		t.Fatalf("fake claude starts = %q, want a second start with --resume session_1", args)
	}
}

// TestContinueRespawnsDrainedStepWithResume sends the next prompt to a step drained at its hand-off. The step's claude
// starts again with --resume, and answers.
func TestContinueRespawnsDrainedStepWithResume(t *testing.T) {
	t.Parallel()
	rig := newStepCrashRig(t, nil)
	dir := t.TempDir()
	restarted, _ := drainHeldStep(t, rig, dir, &stepObserver{}, 1)

	prompt := StepPrompt{Text: "again", Sender: store.PromptSenderOwner}
	if err := restarted.Continue(t.Context(), stepA, prompt); err != nil {
		t.Fatalf("Continue of the drained step: %v", err)
	}
	waitHistory(t, rig.path, stepA, "the respawned step's reply done", func(h store.EngineHistory) bool {
		return stepDone(t, h, "fake reply 2")
	})
	args := fakeArgs(t, dir)
	if len(args) != 2 {
		t.Fatalf("fake claude starts = %q, want two", args)
	}
	if !hasFlagValue(args[1], "--resume", "session_1") {
		t.Fatalf("respawn args = %q, want --resume session_1", args[1])
	}
	requireStepSlots(t, restarted, 1)
}

// TestContinueRespawnsDrainedStepWhenStepCapIsFull fills the step cap with other steps after a step's hand-off. The
// drained step keeps its slot, so a step past the cap is refused, and the drained step's next Continue still starts.
func TestContinueRespawnsDrainedStepWhenStepCapIsFull(t *testing.T) {
	t.Parallel()
	rig := newStepCrashRig(t, nil)
	dir := t.TempDir()
	restarted, _ := drainHeldStep(t, rig, dir, &stepObserver{}, 2)

	if err := restarted.StartStep(t.Context(), testStep(stepB, t.TempDir())); err != nil {
		t.Fatalf("StartStep of a step under the cap: %v", err)
	}
	requireStepSlots(t, restarted, 2)
	err := restarted.StartStep(t.Context(), testStep(stepC, t.TempDir()))
	if !errors.Is(err, ErrCapReached) {
		t.Fatalf("StartStep with the cap full = %v, want ErrCapReached", err)
	}
	requireStepSlots(t, restarted, 2)

	prompt := StepPrompt{Text: "again", Sender: store.PromptSenderOwner}
	if err := restarted.Continue(t.Context(), stepA, prompt); err != nil {
		t.Fatalf("Continue of the drained step with the cap full: %v", err)
	}
	waitHistory(t, rig.path, stepA, "the respawned step's reply done", func(h store.EngineHistory) bool {
		return stepDone(t, h, "fake reply 2")
	})
	args := fakeArgs(t, dir)
	if len(args) != 2 || !hasFlagValue(args[1], "--resume", "session_1") {
		t.Fatalf("fake claude starts = %q, want a second start with --resume session_1", args)
	}
	requireStepSlots(t, restarted, 2)
}

// TestStopOfDrainedStepFreesItsSlot stops a drained step with no Continue after its hand-off. Stop returns no error,
// records the step stopped, and frees the slot the step kept.
func TestStopOfDrainedStepFreesItsSlot(t *testing.T) {
	t.Parallel()
	rig := newStepCrashRig(t, nil)
	dir := t.TempDir()
	restarted, _ := drainHeldStep(t, rig, dir, &stepObserver{}, 1)
	requireStepSlots(t, restarted, 1)

	if err := restarted.Stop(t.Context(), stepA); err != nil {
		t.Fatalf("Stop of the drained step: %v", err)
	}
	requireStepSlots(t, restarted, 0)
	row, err := rig.store.EngineSession(t.Context(), stepA)
	if err != nil {
		t.Fatalf("EngineSession: %v", err)
	}
	if row.State != "stopped" {
		t.Fatalf("row after Stop = state %q, want stopped", row.State)
	}
}

// TestReleaseOfDrainedStepFreesItsSlot releases a drained step with no Continue after its hand-off, as a limit does.
// The release records nothing and frees the slot the step kept.
func TestReleaseOfDrainedStepFreesItsSlot(t *testing.T) {
	t.Parallel()
	rig := newStepCrashRig(t, nil)
	dir := t.TempDir()
	restarted, _ := drainHeldStep(t, rig, dir, &stepObserver{}, 1)
	requireStepSlots(t, restarted, 1)

	if err := restarted.Release(t.Context(), stepA); err != nil {
		t.Fatalf("Release of the drained step: %v", err)
	}
	requireStepSlots(t, restarted, 0)
	row, err := rig.store.EngineSession(t.Context(), stepA)
	if err != nil {
		t.Fatalf("EngineSession: %v", err)
	}
	if row.State != "done" || row.Reason != "" {
		t.Fatalf("row after Release = state %q reason %q, want done with no reason", row.State, row.Reason)
	}
}

// TestReadoptOwnVersionStepStaysLiveAfterHandOff re-adopts a step from a holder of the controller's own version. Its
// hand-off leaves the step live and its holder running.
func TestReadoptOwnVersionStepStaysLiveAfterHandOff(t *testing.T) {
	t.Parallel()
	rig := newStepCrashRig(t, nil)
	dir := t.TempDir()
	obs := &stepObserver{}
	startHeldStep(t, rig, dir, obs)
	pid := fakePID(t, dir)
	pids := holderPids(t, holderDir(t, rig.root, stepA))

	restarted, _ := restartStepDrain(t, rig, holder.WireVersion, 1)
	restarted.ExpectStep(stepA, obs)
	if err := restarted.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if !slices.Contains(restarted.Live(), stepA) {
		t.Fatalf("session %s is not live after the restart: live %v", stepA, restarted.Live())
	}
	releaseHeldTurn(t, dir)
	waitReturns(t, obs, 1)
	waitHistory(t, rig.path, stepA, "the held turn's end recorded", func(h store.EngineHistory) bool {
		return h.State == "done"
	})

	if !slices.Contains(restarted.Live(), stepA) {
		t.Fatalf("session %s is not live after its hand-off, want it kept for its own version", stepA)
	}
	requireStepSlots(t, restarted, 1)
	running, err := holder.Running(pids.Holder, pids.HolderStart)
	if err != nil || !running {
		t.Fatalf("holder %d running = %v, err %v, want running", pids.Holder, running, err)
	}
	requireProcessRunning(t, pid)
}

// holdReturnObserver is a stepObserver whose Return waits until open is called. That keeps the pump of its step from
// ending after the hand-off, so a test decides when the pump ends.
type holdReturnObserver struct {
	stepObserver
	hold chan struct{}
	once sync.Once
}

// newHoldReturnObserver returns a holdReturnObserver whose Return waits until open is called.
func newHoldReturnObserver() *holdReturnObserver {
	return &holdReturnObserver{hold: make(chan struct{})}
}

// open releases a Return that waits on the hold. It is idempotent, so a test can open the hold at its end as well.
func (o *holdReturnObserver) open() {
	o.once.Do(func() { close(o.hold) })
}

// Return waits until the hold opens, then records the turn end as stepObserver does.
func (o *holdReturnObserver) Return(ev session.TurnEnded) {
	<-o.hold
	o.stepObserver.Return(ev)
}

// TestContinueWaitsForDrainedStepsPumpBeforeRespawn holds the pump of a drained step in its hand-off. A Continue that
// arrives then waits for that pump to end, so the new claude does not start over a pump still recording the old holder,
// and then starts with --resume and answers.
func TestContinueWaitsForDrainedStepsPumpBeforeRespawn(t *testing.T) {
	t.Parallel()
	rig := newStepCrashRig(t, nil)
	dir := t.TempDir()
	obs := newHoldReturnObserver()
	// The hold opens in the test body's defer, which runs before the cleanups that stop the manager it blocks.
	defer obs.open()
	startHeldStep(t, rig, dir, obs)
	pid := fakePID(t, dir)
	pids := holderPids(t, holderDir(t, rig.root, stepA))

	restarted, _ := restartStepDrain(t, rig, holder.WireVersion+1, 1)
	restarted.ExpectStep(stepA, obs)
	if err := restarted.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	releaseHeldTurn(t, dir)
	waitHolderEnded(t, pids)
	gateWaitGone(t, pid)

	said := make(chan error, 1)
	go func() {
		said <- restarted.Continue(t.Context(), stepA, StepPrompt{Text: "again", Sender: store.PromptSenderOwner})
	}()
	waitUntil(t, "the Continue to wait for the drained step's pump", func() bool {
		// Only a Continue that finds no live session holds resumeMu, so holding it means the Continue is in respawnStep,
		// and the held hand-off keeps that Continue from respawning until the hold opens.
		if restarted.resumeMu.TryLock() {
			restarted.resumeMu.Unlock()
			return false
		}
		return true
	})
	if starts := fakeArgs(t, dir); len(starts) != 1 {
		t.Fatalf("fake claude starts = %q while the drained step's pump has not ended, want the first only", starts)
	}

	obs.open()
	err := <-said
	if err != nil {
		t.Fatalf("Continue after the drained step's pump ended: %v", err)
	}
	waitHistory(t, rig.path, stepA, "the respawned step's reply done", func(h store.EngineHistory) bool {
		return stepDone(t, h, "fake reply 2")
	})
	args := fakeArgs(t, dir)
	if len(args) != 2 || !hasFlagValue(args[1], "--resume", "session_1") {
		t.Fatalf("fake claude starts = %q, want a second start with --resume session_1", args)
	}
}
