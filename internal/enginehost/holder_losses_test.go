package enginehost

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"slices"
	"syscall"
	"testing"
	"time"

	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"

	"github.com/joshsymonds/orrery/internal/holder"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/telemetry"
)

// holderLossesSeries is the counter of holder losses in a scrape.
const holderLossesSeries = "orrery_holder_losses_total"

// newLossDaemon returns a telemetry Daemon on a fresh provider, and the provider its series are scraped from.
func newLossDaemon(t *testing.T) (*telemetry.Provider, *telemetry.Daemon) {
	t.Helper()
	p, err := telemetry.New()
	if err != nil {
		t.Fatalf("telemetry.New: %v", err)
	}
	d, err := telemetry.NewDaemon(t.Context(), p, fixedClock())
	if err != nil {
		t.Fatalf("NewDaemon: %v", err)
	}
	return p, d
}

// holderLosses returns the holder losses counted in the scrape of p.
func holderLosses(t *testing.T, p *telemetry.Provider) float64 {
	t.Helper()
	rec := httptest.NewRecorder()
	p.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /metrics status = %d, want %d", rec.Code, http.StatusOK)
	}
	parser := expfmt.NewTextParser(model.LegacyValidation)
	families, err := parser.TextToMetricFamilies(rec.Body)
	if err != nil {
		t.Fatalf("parse exposition: %v", err)
	}
	family, ok := families[holderLossesSeries]
	if !ok || len(family.GetMetric()) != 1 {
		t.Fatalf("scrape has no single %s series", holderLossesSeries)
	}
	return family.GetMetric()[0].GetCounter().GetValue()
}

// newLossManager returns a manager that reads rows from s, writes through writer and counts its holder losses on d. Its
// sessions run their holders under scope in stateDir. Close runs at the end of the test.
func newLossManager(t *testing.T, s Store, writer *Writer, scope, stateDir string, d *telemetry.Daemon) *Manager {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Fatalf("find sh: %v", err)
	}
	m := NewManager(Config{
		Shell:       sh,
		Claude:      fakeClaudePath(t),
		Environ:     []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir()},
		Cap:         1,
		Writer:      writer,
		Now:         fixedClock,
		Notifier:    &fakeSink{},
		Store:       s,
		HolderScope: scope,
		StateDir:    stateDir,
		Metrics:     d,
	})
	t.Cleanup(m.Close)
	return m
}

// restartWithLosses ends the manager of rig as a crash leaves it, and returns a restarted manager that counts its
// holder losses on d and has not reconciled yet. The restart has a fresh writer on the same store and state dir.
func restartWithLosses(t *testing.T, rig crashRig, d *telemetry.Daemon) *Manager {
	t.Helper()
	rig.manager.Close()
	writer := NewWriter(WriterConfig{Applier: rig.store, Tick: 5 * time.Millisecond})
	startRun(t, writer)
	return newLossManager(t, rig.store, writer, fakeSystemdRunPath(t), rig.root, d)
}

// TestHolderLostCountsGoneWorkingEngineHolder ends the holder of a working engine row while orreryd is down. The
// restart finds the holder gone, so its re-adoption counts one holder loss, and the row is drained and resumed.
func TestHolderLostCountsGoneWorkingEngineHolder(t *testing.T) {
	t.Parallel()
	rig := newCrashRig(t, nil, false)
	dir := t.TempDir()
	id, err := rig.manager.Start(t.Context(), StartRequest{Dir: dir, Prompt: "STALL"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitUntil(t, engineOf(rig.manager), "the stalled block's unfinished text", func() bool {
		return countRows(t, rig.path, "engine_text") == 1
	})
	pid := fakePID(t, dir)
	rig.manager.Close()
	ctx := context.WithoutCancel(t.Context())
	if err := holder.Stop(ctx, holderDir(t, rig.root, id)); err != nil {
		t.Fatalf("stop holder of session %s: %v", id, err)
	}
	gateWaitGone(t, pid)

	// The resumed turn stalls too, so the row stays working while the test reads it.
	stallNextPrompt(t, dir)
	p, d := newLossDaemon(t)
	restarted := restartWithLosses(t, rig, d)
	if err := restarted.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile after the crash: %v", err)
	}

	if got := holderLosses(t, p); got != 1 {
		t.Fatalf("holder losses after the restart = %v, want 1", got)
	}
}

// TestHolderLostCountsUnattachableEngineHolder stops the holder of a working engine row with SIGSTOP while orreryd is
// down, so its hello never comes. The restart stops that holder and drains it, which counts one holder loss.
func TestHolderLostCountsUnattachableEngineHolder(t *testing.T) {
	t.Parallel()
	rig := newCrashRig(t, nil, false)
	dir := t.TempDir()
	id, err := rig.manager.Start(t.Context(), StartRequest{Dir: dir, Prompt: "STALL"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitUntil(t, engineOf(rig.manager), "the stalled block's unfinished text", func() bool {
		return countRows(t, rig.path, "engine_text") == 1
	})
	pid := fakePID(t, dir)
	pids := holderPids(t, holderDir(t, rig.root, id))
	rig.manager.Close()
	// A stopped holder keeps its lock and its socket, but never answers a hello.
	if err := syscall.Kill(pids.Holder, syscall.SIGSTOP); err != nil {
		t.Fatalf("stop holder %d: %v", pids.Holder, err)
	}
	t.Cleanup(func() {
		// A holder still running at the end is resumed, so the clear of the test's root can stop it through its socket.
		running, err := holder.Running(pids.Holder, pids.HolderStart)
		if err != nil {
			t.Errorf("read holder %d: %v", pids.Holder, err)
			return
		}
		if running {
			if err := syscall.Kill(pids.Holder, syscall.SIGCONT); err != nil {
				t.Errorf("resume holder %d: %v", pids.Holder, err)
			}
		}
	})

	// The resumed turn stalls too, so the row stays working while the test reads it.
	stallNextPrompt(t, dir)
	p, d := newLossDaemon(t)
	restarted := restartWithLosses(t, rig, d)
	if err := restarted.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile after the crash: %v", err)
	}

	waitHolderEnded(t, pids)
	gateWaitGone(t, pid)
	if got := holderLosses(t, p); got != 1 {
		t.Fatalf("holder losses after the restart = %v, want 1", got)
	}
}

// TestHolderLostCountsUnattachableStepHolder stops the holder of a working step with SIGSTOP while orreryd is down. The
// restart clears that step's holder without draining it, which counts one holder loss.
func TestHolderLostCountsUnattachableStepHolder(t *testing.T) {
	t.Parallel()
	rig := newStepCrashRig(t, turnEndTrigger)
	dir := t.TempDir()
	startReplyStep(t, rig, dir)
	pid := fakePID(t, dir)
	pids := holderPids(t, holderDir(t, rig.root, stepA))

	p, d := newLossDaemon(t)
	restarted := restartWithLosses(t, rig, d)
	// A stopped holder keeps its lock and its socket, but never answers a hello.
	if err := syscall.Kill(pids.Holder, syscall.SIGSTOP); err != nil {
		t.Fatalf("stop holder %d: %v", pids.Holder, err)
	}
	restarted.ExpectStep(stepA, &stepObserver{})
	if err := restarted.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile after the crash: %v", err)
	}

	waitHolderEnded(t, pids)
	gateWaitGone(t, pid)
	if got := holderLosses(t, p); got != 1 {
		t.Fatalf("holder losses after the restart = %v, want 1", got)
	}
}

// TestHolderLostCountsNothingForFailedRow releases the holder of a row that failed while orreryd was down. The row is
// cleared without re-adoption, which counts no holder loss.
func TestHolderLostCountsNothingForFailedRow(t *testing.T) {
	t.Parallel()
	rig := newCrashRig(t, nil, false)
	dir := t.TempDir()
	id, err := rig.manager.Start(t.Context(), StartRequest{Dir: dir, Prompt: "STALL"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitUntil(t, engineOf(rig.manager), "the stalled block's unfinished text", func() bool {
		return countRows(t, rig.path, "engine_text") == 1
	})
	pid := fakePID(t, dir)
	failed, reason := "failed", "the process failed"
	set := store.EngineSet{ID: id, Changes: store.EngineChanges{State: &failed, Reason: &reason}}
	if err := rig.store.ApplyEngine(t.Context(), []store.EngineOp{set}, fixedClock()); err != nil {
		t.Fatalf("set failed row: %v", err)
	}

	p, d := newLossDaemon(t)
	restarted := restartWithLosses(t, rig, d)
	if err := restarted.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile after the crash: %v", err)
	}

	gateWaitGone(t, pid)
	if got := holderLosses(t, p); got != 0 {
		t.Fatalf("holder losses after the restart = %v, want 0", got)
	}
}

// TestHolderLostCountsNothingForReadoptedHolder leaves the holder of a working engine row running while orreryd is
// down. The restart re-adopts it, which counts no holder loss.
func TestHolderLostCountsNothingForReadoptedHolder(t *testing.T) {
	t.Parallel()
	rig := newCrashRig(t, nil, false)
	dir := t.TempDir()
	id, err := rig.manager.Start(t.Context(), StartRequest{Dir: dir, Prompt: "STALL"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitUntil(t, engineOf(rig.manager), "the stalled block's unfinished text", func() bool {
		return countRows(t, rig.path, "engine_text") == 1
	})

	p, d := newLossDaemon(t)
	restarted := restartWithLosses(t, rig, d)
	if err := restarted.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile after the crash: %v", err)
	}

	if !slices.Contains(restarted.Live(), id) {
		t.Fatalf("session %s is not live after the restart, want its holder re-adopted", id)
	}
	if got := holderLosses(t, p); got != 0 {
		t.Fatalf("holder losses after the restart = %v, want 0", got)
	}
}

// TestHolderLostCountsNothingForClearedHolders leaves three holders running: one with no row, one whose row is
// untracked, and one whose row is stopped. Reconcile clears each without re-adopting it, which counts no holder loss.
func TestHolderLostCountsNothingForClearedHolders(t *testing.T) {
	t.Parallel()
	root := holderRoot(t)
	s, _ := openStore(t)
	writer := NewWriter(WriterConfig{Applier: s, Tick: 5 * time.Millisecond})
	startRun(t, writer)

	orphanDir := t.TempDir()
	startLeftHolder(t, root, "orphan", orphanDir)
	orphanPID := fakePID(t, orphanDir)

	untrackedDir := t.TempDir()
	untracked := store.EngineAdd{ID: "untracked", Dir: untrackedDir, State: "working", Kind: store.KindEngine}
	if err := s.ApplyEngine(t.Context(), []store.EngineOp{untracked}, time.Unix(1, 0)); err != nil {
		t.Fatalf("seed untracked row: %v", err)
	}
	startLeftHolder(t, root, "untracked", untrackedDir)
	untrackedPID := fakePID(t, untrackedDir)

	stoppedDir := t.TempDir()
	stopped := []store.EngineOp{
		store.EngineAdd{ID: "stopped", Dir: stoppedDir, State: stateStopped, Kind: store.KindEngine},
		store.EngineLaunch{ID: "stopped"},
	}
	if err := s.ApplyEngine(t.Context(), stopped, time.Unix(1, 0)); err != nil {
		t.Fatalf("seed stopped row: %v", err)
	}
	startLeftHolder(t, root, "stopped", stoppedDir)
	stoppedPID := fakePID(t, stoppedDir)

	p, d := newLossDaemon(t)
	m := newLossManager(t, s, writer, "", root, d)
	if err := m.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	gateWaitGone(t, orphanPID, untrackedPID, stoppedPID)
	if got := holderLosses(t, p); got != 0 {
		t.Fatalf("holder losses after the restart = %v, want 0", got)
	}
}
