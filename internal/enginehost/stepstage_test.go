package enginehost

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/gates"
	"github.com/joshsymonds/orrery/internal/store"
)

// stepStageRig is a manager and the Steps that start its steps, over one store, on a fake clock that the Steps and its
// writer both read. Its pools queue every gate's grant until the test closes the queue, unless it was built unqueued.
type stepStageRig struct {
	stepsRig
	clock *watchLoopClock
	pools *fakeGatePools
}

// newStepStageRig returns a rig whose clock reads watchEpoch. Its environment names a config home whose machine pool file
// declares the default gate pool, and its holders are cleared when the test ends.
func newStepStageRig(t *testing.T) *stepStageRig {
	t.Helper()
	s, path := openStore(t)
	root, gateRoot := holderRoot(t), holderRoot(t)
	t.Cleanup(func() {
		clearHolderDirs(t, root)
		clearHolderDirs(t, gateRoot)
	})
	home := t.TempDir()
	writeStepAgents(t, home)
	stepGateMachinePoolsFile(t, home, gates.DefaultPool)
	rig := &stepStageRig{
		stepsRig: stepsRig{
			store: s, path: path, root: root, gateRoot: gateRoot, worktrees: t.TempDir(),
			environ: []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "XDG_CONFIG_HOME=" + home},
			applier: &crashApplier{inner: s},
		},
		clock: &watchLoopClock{at: watchEpoch()},
	}
	rig.build(t, rig.applier, true)
	return rig
}

// build starts the manager and the Steps of the rig, with a writer over applier that reads the rig's clock. When queued is
// set, the Steps' pools queue each gate's grant until the test closes the queue; otherwise they grant at once.
func (r *stepStageRig) build(t *testing.T, applier Applier, queued bool) {
	t.Helper()
	writer := NewWriter(WriterConfig{Applier: applier, Now: r.clock.now})
	startRun(t, writer)
	r.manager = newRigManager(t, r.stepsRig, writer)
	r.pools = &fakeGatePools{}
	if queued {
		r.pools.queue = make(chan struct{})
	}
	steps := NewSteps(StepsConfig{
		Sessions: r.manager, Store: r.store, Writer: writer, Pools: r.pools, Worktrees: r.worktrees,
		Environ: r.environ, Logger: slog.New(slog.DiscardHandler), Now: r.clock.now, GateStateDir: r.gateRoot,
		GateScope: fakeSystemdRunPath(t),
	})
	t.Cleanup(steps.Close)
	r.steps = steps
}

// restartStepStage crashes the rig as a crash leaves it, with its running gate detached under its holder, and restarts it
// over the store as restart does: a new writer, manager and Steps, then Readopt and Reconcile. queued is build's.
func (r *stepStageRig) restartStepStage(t *testing.T, queued bool) {
	t.Helper()
	r.crash()
	r.steps.Close()
	r.build(t, r.store, queued)
	if err := r.steps.Readopt(t.Context(), r.manager); err != nil {
		t.Fatalf("Readopt: %v", err)
	}
	if err := r.manager.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile of the manager: %v", err)
	}
	if err := r.steps.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile of the steps: %v", err)
	}
}

// requireStepStage fails the test unless the stored row of id is in stage, from the clock reading at.
func requireStepStage(t *testing.T, steps *Steps, id, stage string, at time.Duration) {
	t.Helper()
	row := stepRowOf(t, steps, id)
	if row.LoopStage != stage || !row.LoopStageFrom.Equal(watchAt(at)) {
		t.Fatalf("stored stage %q from %v, want %q from %v", row.LoopStage, row.LoopStageFrom, stage, watchAt(at))
	}
}

// stepStageWrite writes contents to path, where the fake claude or the gate waits for it.
func stepStageWrite(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// TestStepStageSurvivesRestartAtEachStage covers R1: one writing step goes through turn, checks, a queued gate, a granted
// gate and a usage limit on a fake clock, and is restarted in each. Each stored stage carries the clock reading of its
// change: the checks start holds through the queue wait, the gate starts at its grant, and every restart keeps both.
func TestStepStageSurvivesRestartAtEachStage(t *testing.T) {
	t.Parallel()
	rig := newStepStageRig(t)
	gateStarted := filepath.Join(t.TempDir(), "gate.started")
	gateRelease := filepath.Join(t.TempDir(), "gate.release")
	gatesFile := filepath.Join(t.TempDir(), "gates.toml")
	script := fmt.Sprintf(`echo $$ > %q; while [ ! -e %q ]; do sleep 0.05; done; exit 1`, gateStarted, gateRelease)
	stepGateWriteFile(t, gatesFile, stepGrantGatesText(stepGateShell(t), script, ""))
	linked := stepWorktreeTestAddLinked(t, stepGitTestRepo(t))
	start := stepGateNamedStart(linked, "unit", gatesFile)
	start.Prompt = "STEPDONE"
	// The fake claude's STEPDONE return is done, which is not the start's default done value.
	start.DoneValue = "done"
	id, err := rig.steps.Start(t.Context(), start)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	// A writing step's session runs in its workspace, so the fake claude's files are written there.
	dir := linked

	fakePID(t, dir)
	requireStepStage(t, rig.steps, id, store.StepStageTurn, 0)
	rig.restartStepStage(t, true)
	requireStepStage(t, rig.steps, id, store.StepStageTurn, 0)

	// The turn ends at a minute, and its return's checks start there and queue the gate.
	rig.clock.set(time.Minute)
	stepStageWrite(t, filepath.Join(dir, "fakeclaude.release"), "")
	waitUntil(
		t,
		engineOf(rig.manager, rig.steps),
		"the gate's grant request",
		func() bool { return len(rig.pools.requested()) == 1 },
	)
	requireStepStage(t, rig.steps, id, store.StepStageChecks, time.Minute)
	rig.restartStepStage(t, true)
	requireStepStage(t, rig.steps, id, store.StepStageChecks, time.Minute)

	// The clock moves on while the gate waits for its grant, and the checks start does not.
	rig.clock.set(2 * time.Minute)
	waitUntil(
		t,
		engineOf(rig.manager, rig.steps),
		"the gate's grant request after the restart",
		func() bool { return len(rig.pools.requested()) == 1 },
	)
	requireStepStage(t, rig.steps, id, store.StepStageChecks, time.Minute)
	rig.restartStepStage(t, true)
	waitUntil(t, engineOf(rig.manager, rig.steps), "the gate's grant request after the second restart", func() bool {
		return len(rig.pools.requested()) == 1
	})
	requireStepStage(t, rig.steps, id, store.StepStageChecks, time.Minute)

	// The grant lands at three minutes, and the gate runs from there.
	rig.clock.set(3 * time.Minute)
	close(rig.pools.queue)
	waitUntil(t, engineOf(rig.manager, rig.steps), "the gate running", func() bool {
		_, err := os.Stat(gateStarted)
		return err == nil
	})
	requireStepStage(t, rig.steps, id, store.StepStageGate, 3*time.Minute)
	// The restarted gate is re-adopted under its grant, which the restarted pools grant at once.
	rig.restartStepStage(t, false)
	requireStepStage(t, rig.steps, id, store.StepStageGate, 3*time.Minute)

	// The gate fails, so the step is sent back, and the resent turn reads the usage limit.
	rig.clock.set(4 * time.Minute)
	stepStageWrite(t, filepath.Join(dir, "fakeclaude.next"), "LIMIT")
	stepStageWrite(t, gateRelease, "")
	waitUntil(
		t,
		engineOf(rig.manager, rig.steps),
		"step limited",
		func() bool { return stepRowOf(t, rig.steps, id).LoopLimited },
	)
	requireStepStage(t, rig.steps, id, store.StepStageLimited, 4*time.Minute)
	rig.restartStepStage(t, true)
	requireStepStage(t, rig.steps, id, store.StepStageLimited, 4*time.Minute)
	if row := stepRowOf(t, rig.steps, id); row.End != "" {
		t.Fatalf("End = %q after the restart, want the step open", row.End)
	}
}
