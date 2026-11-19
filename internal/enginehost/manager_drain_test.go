package enginehost

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/holder"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/waittest"
)

// newDrainManager returns a manager with one slot whose controller speaks holder wire version version, over writer and s.
// Its sessions run their holders under the fake systemd-run in root. Close runs at the end of the test. The caller runs
// Reconcile.
func newDrainManager(t *testing.T, s Store, writer *Writer, sink NoticeSink, root string, version int) *Manager {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Fatalf("find sh: %v", err)
	}
	m := NewManager(Config{
		Shell:         sh,
		Claude:        fakeClaudePath(t),
		Environ:       []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir()},
		Cap:           1,
		Writer:        writer,
		Now:           fixedClock,
		Notifier:      sink,
		Store:         s,
		HolderScope:   fakeSystemdRunPath(t),
		StateDir:      root,
		HolderVersion: version,
	})
	t.Cleanup(m.Close)
	return m
}

// restartDrain ends the manager of rig, detaching its sessions as a crash leaves them, and returns a manager over the same
// store and state dir whose controller speaks version. Its writer stores through applier, and the notices it sends go to
// the returned sink. The caller runs Reconcile.
func restartDrain(t *testing.T, rig crashRig, applier Applier, version int) (*Manager, *fakeSink) {
	t.Helper()
	rig.manager.Close()
	writer := NewWriter(WriterConfig{Applier: applier})
	startRun(t, writer)
	sink := &fakeSink{}
	return newDrainManager(t, rig.store, writer, sink, rig.root, version), sink
}

// turnEndHold is an Applier that stores through inner, except that a batch holding a turn's end waits until the hold is
// opened. open is idempotent, so a test can open the hold at its end as well.
type turnEndHold struct {
	inner   Applier
	release chan struct{}
	once    sync.Once
}

// newTurnEndHold returns a turnEndHold that stores through inner.
func newTurnEndHold(inner Applier) *turnEndHold {
	return &turnEndHold{inner: inner, release: make(chan struct{})}
}

// open releases the batches that hold a turn's end.
func (h *turnEndHold) open() {
	h.once.Do(func() { close(h.release) })
}

// ApplyEngine stores ops through inner. A batch holding a turn's end waits until the hold is opened, or ctx ends.
func (h *turnEndHold) ApplyEngine(ctx context.Context, ops []store.EngineOp, now time.Time) error {
	if slices.ContainsFunc(ops, func(op store.EngineOp) bool {
		_, ok := op.(store.EngineTurnEnded)
		return ok
	}) {
		select {
		case <-h.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return h.inner.ApplyEngine(ctx, ops, now)
}

// releaseHeldTurn ends the JUDGEHOLDS turn that the fake claude in dir holds, by making its release file.
func releaseHeldTurn(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "fakeclaude.release"), nil, 0o600); err != nil {
		t.Fatalf("write fake claude release: %v", err)
	}
}

// stalledSession starts a STALL turn in dir on the manager of rig, and waits for its unfinished text. The manager is left
// for the caller to restart, which detaches the holder. It returns the session id, the pid of its fake claude and the pids
// of its holder.
func stalledSession(t *testing.T, rig crashRig, dir string) (string, int, holder.Pids) {
	t.Helper()
	id, err := rig.manager.Start(t.Context(), StartRequest{Dir: dir, Prompt: "STALL"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitUntil(t, engineOf(rig.manager), "the stalled block's unfinished text", func() bool {
		return countRows(t, rig.path, "engine_text") == 1
	})
	return id, fakePID(t, dir), holderPids(t, holderDir(t, rig.root, id))
}

// TestReconcileDrainsOneVersionOlderHolderAfterItsTurn crashes orreryd partway through a trickling turn, so the turn's
// later blocks are only in the holder's spool. The fake holds the turn's last block until the restart has re-adopted
// the holder of one version older, so the turn is still running then. The restart records the rest of the turn on it,
// and stops the holder once the turn has ended.
func TestReconcileDrainsOneVersionOlderHolderAfterItsTurn(t *testing.T) {
	t.Parallel()
	rig := newCrashRig(t, blockTrigger("fake trickle 1.5"), false)
	dir := t.TempDir()
	id, err := rig.manager.Start(t.Context(), StartRequest{Dir: dir, Prompt: "TRICKLEHELD"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitUntil(t, engineOf(rig.manager), "the store refuses the batch of fake trickle 1.5", rig.crash.isRefusing)
	pid := fakePID(t, dir)
	pids := holderPids(t, holderDir(t, rig.root, id))

	restarted, sink := restartDrain(t, rig, rig.store, holder.WireVersion+1)
	if err := restarted.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if !slices.Contains(restarted.Live(), id) {
		t.Fatalf("session %s is not live after the restart, want its held turn re-adopted", id)
	}
	if err := os.WriteFile(filepath.Join(dir, "fakeclaude.release"), nil, 0o600); err != nil {
		t.Fatalf("release the turn's last block: %v", err)
	}

	history := waitHistory(
		t,
		engineOf(rig.manager, restarted),
		rig.path,
		id,
		"the done state of the trickled turn",
		func(h store.EngineHistory) bool {
			return h.State == "done"
		},
	)
	if slices.Contains(restarted.Live(), id) {
		t.Fatalf("session %s is live after its turn ended, want its holder stopped", id)
	}
	var want []string
	for k := 1; k <= 60; k++ {
		want = append(want, fmt.Sprintf("fake trickle 1.%d", k))
	}
	if got := blockTexts(t, history); !slices.Equal(got, want) {
		t.Fatalf("finished blocks after the restart = %q, want each of the 60 once in order", got)
	}
	// The pump tells the owner once the turn's end is stored, so the notice can still be on its way.
	want1 := Notice{Session: id, Dir: dir, State: "done", Body: "fake trickle 1.60"}
	if got := waitNotices(t, engineOf(rig.manager, restarted), sink); len(got) != 1 || got[0] != want1 {
		t.Fatalf("notices after the restart = %+v, want exactly [%+v]", got, want1)
	}
	waitHolderEnded(t, pids)
	gateWaitGone(t, pid)
}

// TestReconcileStopsOneVersionOlderIdleHolderAndNextSayResumes re-adopts the holder of one version older whose turn has
// ended. The restart stops it at once, records nothing, and the next Say starts claude with --resume in a new holder.
func TestReconcileStopsOneVersionOlderIdleHolderAndNextSayResumes(t *testing.T) {
	t.Parallel()
	rig := newCrashRig(t, nil, false)
	dir := t.TempDir()
	id, err := rig.manager.Start(t.Context(), StartRequest{Dir: dir, Prompt: "hi"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(
		t,
		engineOf(rig.manager),
		rig.path,
		id,
		"the done state of the first turn",
		func(h store.EngineHistory) bool {
			return h.State == "done"
		},
	)
	pid := fakePID(t, dir)
	pids := holderPids(t, holderDir(t, rig.root, id))

	restarted, sink := restartDrain(t, rig, rig.store, holder.WireVersion+1)
	if err := restarted.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if slices.Contains(restarted.Live(), id) {
		t.Fatalf("session %s is live after the restart, want its holder stopped", id)
	}
	waitHolderEnded(t, pids)
	gateWaitGone(t, pid)
	// The pump ends after the stopped child's exit has been read, so its end means any exit record has been stored.
	waitUntil(t, engineOf(rig.manager, restarted), "the stopped session's pump to end", func() bool {
		restarted.mu.Lock()
		defer restarted.mu.Unlock()
		return restarted.stopping[id] == nil
	})
	row, err := rig.store.EngineSession(t.Context(), id)
	if err != nil {
		t.Fatalf("EngineSession: %v", err)
	}
	if row.State != "done" || row.Reason != "" {
		t.Fatalf("row after the stop = state %q reason %q, want done with no reason", row.State, row.Reason)
	}
	if got := sink.got(); len(got) != 0 {
		t.Fatalf("notices after the stop = %+v, want none", got)
	}

	if err := restarted.Say(t.Context(), SayRequest{ID: id, Text: "again"}); err != nil {
		t.Fatalf("Say after the stop: %v", err)
	}
	waitHistory(
		t,
		engineOf(rig.manager, restarted),
		rig.path,
		id,
		"the reply to the next Say",
		func(h store.EngineHistory) bool {
			return h.State == "done" && hasBlock(t, h, "fake reply 2")
		},
	)
	starts := fakeStarts(t, dir)
	if len(starts) != 2 || !strings.Contains(starts[1], "--resume session_1") {
		t.Fatalf("fake claude starts = %q, want a second start with --resume session_1", starts)
	}
	// The pump tells the owner once the turn's end is stored, so the notice can still be on its way.
	if got := waitNotices(t, engineOf(rig.manager, restarted), sink); len(got) != 1 || got[0].State != "done" {
		t.Fatalf("notices after the next Say = %+v, want only the reply's done notice", got)
	}
}

// TestSayWhileOldHolderStopsStartsResumedHolder holds the turn end of a one-version-older holder in the store. Its session
// is taken out of the live set at the turn end, but its pump, which holds the session's slot, cannot finish until the hold
// opens. A Say that arrives then waits for that pump, and starts claude with --resume in a new holder, which answers it.
func TestSayWhileOldHolderStopsStartsResumedHolder(t *testing.T) {
	t.Parallel()
	rig := newCrashRig(t, nil, false)
	dir := t.TempDir()
	id, err := rig.manager.Start(t.Context(), StartRequest{Dir: dir, Prompt: "JUDGEHOLDS"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	pid := fakePID(t, dir)

	// The hold opens in the test body's defer, which runs before the cleanups that stop the writer it blocks.
	hold := newTurnEndHold(rig.store)
	defer hold.open()
	restarted, sink := restartDrain(t, rig, hold, holder.WireVersion+1)
	if err := restarted.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if !slices.Contains(restarted.Live(), id) {
		t.Fatalf("session %s is not live after the restart: live %v", id, restarted.Live())
	}

	releaseHeldTurn(t, dir)
	waitUntil(
		t,
		engineOf(rig.manager, restarted),
		"the turn end to take session "+id+" out of the live set",
		func() bool {
			return !slices.Contains(restarted.Live(), id)
		},
	)
	said := make(chan error, 1)
	go func() {
		said <- restarted.Say(t.Context(), SayRequest{ID: id, Text: "again"})
	}()
	waitUntil(t, engineOf(rig.manager, restarted), "the Say to wait for the old holder to stop", func() bool {
		// Only a Say that finds no live session takes resumeMu, so holding it means the Say is in resumeSession, and the
		// held pump keeps that Say from resuming until the hold opens.
		if restarted.resumeMu.TryLock() {
			restarted.resumeMu.Unlock()
			return false
		}
		return true
	})
	hold.open()
	if err := <-said; err != nil {
		t.Fatalf("Say while the old holder stopped: %v", err)
	}

	waittest.Until(t, nil, "the fake claude to start a second time", func() bool {
		return len(fakeStarts(t, dir)) == 2
	})
	// The Say waited for the old holder to end, so the old claude is gone by the time the new one starts.
	if !gateProcessGone(pid) {
		t.Fatalf("old fake claude %d still running when the new one started", pid)
	}
	if starts := fakeStarts(t, dir); !strings.Contains(starts[1], "--resume session_1") {
		t.Fatalf("fake claude starts = %q, want the second with --resume session_1", starts)
	}
	waitHistory(
		t,
		engineOf(rig.manager, restarted),
		rig.path,
		id,
		"the reply to the Say that waited",
		func(h store.EngineHistory) bool {
			return h.State == "done" && hasBlock(t, h, "fake reply 2")
		},
	)
	for _, n := range sink.got() {
		if n.State == "failed" {
			t.Fatalf("notice %+v, want the Say answered, not failed", n)
		}
	}
}

// TestReconcileKeepsOwnVersionHolderLiveAfterItsTurn re-adopts a holder of the controller's own version. Its turn's end
// leaves the session live and its holder running.
func TestReconcileKeepsOwnVersionHolderLiveAfterItsTurn(t *testing.T) {
	t.Parallel()
	rig := newCrashRig(t, nil, false)
	dir := t.TempDir()
	id, err := rig.manager.Start(t.Context(), StartRequest{Dir: dir, Prompt: "JUDGEHOLDS"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	pid := fakePID(t, dir)
	pids := holderPids(t, holderDir(t, rig.root, id))

	restarted, _ := restartDrain(t, rig, rig.store, holder.WireVersion)
	if err := restarted.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if !slices.Contains(restarted.Live(), id) {
		t.Fatalf("session %s is not live after the restart: live %v", id, restarted.Live())
	}

	releaseHeldTurn(t, dir)
	waitHistory(
		t,
		engineOf(rig.manager, restarted),
		rig.path,
		id,
		"the done state of the held turn",
		func(h store.EngineHistory) bool {
			return h.State == "done"
		},
	)
	if !slices.Contains(restarted.Live(), id) {
		t.Fatalf("session %s is not live after its turn ended, want it kept for its own version", id)
	}
	running, err := holder.Running(pids.Holder, pids.HolderStart)
	if err != nil || !running {
		t.Fatalf("holder %d running = %v, err %v, want running", pids.Holder, running, err)
	}
	requireProcessRunning(t, pid)
}

// TestReconcileStopsTwoVersionsOlderHolderThenResumesIt re-adopts nothing from a holder two versions older: the restart
// stops it and settles its stalled turn as a drained row, which resumes on its Claude session.
func TestReconcileStopsTwoVersionsOlderHolderThenResumesIt(t *testing.T) {
	t.Parallel()
	rig := newCrashRig(t, nil, false)
	dir := t.TempDir()
	id, pid, pids := stalledSession(t, rig, dir)
	stallNextPrompt(t, dir)

	restarted, sink := restartDrain(t, rig, rig.store, holder.WireVersion+2)
	if err := restarted.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	waitHolderEnded(t, pids)
	gateWaitGone(t, pid)
	requireResumed(t, rig.store, rig.path, id, dir, sink)
}

// TestReconcileStopsOneVersionNewerHolderThenResumesIt re-adopts nothing from a holder one version newer: the restart
// stops it and settles its stalled turn as a drained row, which resumes on its Claude session.
func TestReconcileStopsOneVersionNewerHolderThenResumesIt(t *testing.T) {
	t.Parallel()
	rig := newCrashRig(t, nil, false)
	dir := t.TempDir()
	id, pid, pids := stalledSession(t, rig, dir)
	stallNextPrompt(t, dir)

	restarted, sink := restartDrain(t, rig, rig.store, holder.WireVersion)
	// Zero in Config takes holder.WireVersion, so a controller one version below the holder is set on the field the default
	// fills.
	restarted.holderVersion = holder.WireVersion - 1
	if err := restarted.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	waitHolderEnded(t, pids)
	gateWaitGone(t, pid)
	requireResumed(t, rig.store, rig.path, id, dir, sink)
}

// TestReconcileKeepsOneVersionOlderHolderWhileItsBackgroundTaskRuns re-adopts the holder of one version older whose turn
// ended with a background task still running. The restart keeps it live, so the task is not killed; the next Say is
// answered on that same holder, and once that turn ends with no task running the holder is stopped.
func TestReconcileKeepsOneVersionOlderHolderWhileItsBackgroundTaskRuns(t *testing.T) {
	t.Parallel()
	rig := newCrashRig(t, nil, false)
	dir := t.TempDir()
	id, err := rig.manager.Start(t.Context(), StartRequest{Dir: dir, Prompt: "BACKGROUND"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, engineOf(rig.manager), rig.path, id, "the background turn done", func(h store.EngineHistory) bool {
		return h.State == "done" && hasBlock(t, h, "fake background 1")
	})
	pids := holderPids(t, holderDir(t, rig.root, id))

	restarted, _ := restartDrain(t, rig, rig.store, holder.WireVersion+1)
	if err := restarted.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if !slices.Contains(restarted.Live(), id) {
		t.Fatalf("session %s is not live after the restart, want its holder kept while its background task runs", id)
	}

	if err := restarted.Say(t.Context(), SayRequest{ID: id, Text: "BGEND"}); err != nil {
		t.Fatalf("Say BGEND: %v", err)
	}
	waitHistory(
		t,
		engineOf(rig.manager, restarted),
		rig.path,
		id,
		"the reply after the background task ended",
		func(h store.EngineHistory) bool {
			return h.State == "done" && hasBlock(t, h, "fake reply 2")
		},
	)
	if starts := fakeStarts(t, dir); len(starts) != 1 {
		t.Fatalf("fake claude starts = %q, want the one start, the Say answered on the old holder", starts)
	}
	waitHolderEnded(t, pids)
	if slices.Contains(restarted.Live(), id) {
		t.Fatalf("session %s is live after its last turn ended with no task running, want its holder stopped", id)
	}
}
