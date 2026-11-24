package enginehost

import (
	"context"
	"slices"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/holder"
	"github.com/joshsymonds/orrery/internal/store"
)

// runningAtStore is an Applier that stores through inner. For each batch that stores text, it records whether the holder
// that pids names was still live, running and not exiting as /proc shows it (holderLive), when the batch was stored.
type runningAtStore struct {
	t     *testing.T
	inner Applier
	pids  holder.Pids

	mu      sync.Mutex
	running []bool
}

// ApplyEngine stores ops through inner, and records the holder's state when ops store text.
func (s *runningAtStore) ApplyEngine(ctx context.Context, ops []store.EngineOp, now time.Time) error {
	if slices.ContainsFunc(ops, func(op store.EngineOp) bool {
		_, ok := op.(store.EngineText)
		return ok
	}) {
		running, err := holderLive(s.pids)
		if err != nil {
			s.t.Errorf("read holder %d while storing text: %v", s.pids.Holder, err)
		}
		s.mu.Lock()
		s.running = append(s.running, running)
		s.mu.Unlock()
	}
	return s.inner.ApplyEngine(ctx, ops, now)
}

// runningWhenStored returns the holder's state recorded for each batch that stored text, in order.
func (s *runningAtStore) runningWhenStored() []bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.running)
}

// TestReconcileStopsUnattachableHolderThenDrainsIt crashes orreryd during the stalled turn, then stops the holder with
// SIGSTOP, so its hello never comes and Reattach cannot attach it. The restart stops that live holder first, then drains
// it as a gone holder: its child ends, the text it spooled unrecorded is stored while the holder is gone, and the turn
// is resumed on its Claude session.
func TestReconcileStopsUnattachableHolderThenDrainsIt(t *testing.T) {
	t.Parallel()
	rig := newCrashRig(t, stalledTextTrigger(), false)
	dir := t.TempDir()
	id, err := rig.manager.Start(t.Context(), StartRequest{Dir: dir, Prompt: "STALL"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	// The store refuses the stalled block's first streamed text, so the holder spools it unrecorded.
	waitUntil(t, engineOf(rig.manager), "the store refuses the batch of fake stalling 1", rig.crash.isRefusing)
	pid := fakePID(t, dir)
	pids := holderPids(t, holderDir(t, rig.root, id))
	rig.manager.Close()
	running, err := holder.Running(pids.Holder, pids.HolderStart)
	if err != nil || !running {
		t.Fatalf("holder %d running = %v, err %v, want running before the stop", pids.Holder, running, err)
	}
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
	spy := &textSpy{inner: rig.store}
	ordered := &runningAtStore{t: t, inner: spy, pids: pids}
	writer := NewWriter(WriterConfig{Applier: ordered})
	startRun(t, writer)
	sink := &fakeSink{}
	restarted := newStateManager(t, rig.store, writer, sink, fakeSystemdRunPath(t), rig.root, nil)
	// The stopped holder answers no attach, so the attach runs out of time however short its bound is.
	restarted.attachTimeout = time.Millisecond
	if err := restarted.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile after the crash: %v", err)
	}

	// Reattach waits AttachTimeout for the hello. Stop, and then the clear, each run to their own clearTimeout bound.
	waitHolderEnded(t, pids)
	gateWaitGone(t, pid)
	requireDirPresent(t, holderDir(t, rig.root, id))
	requireResumed(t, rig.store, rig.path, id, dir, sink)
	waitUntil(
		t,
		engineOf(rig.manager, restarted),
		"the resumed turn's text",
		func() bool { return len(spy.storedTexts()) == 2 },
	)
	if got := spy.storedTexts(); !slices.Equal(got, []string{"fake stalling 1", "fake stalling 2"}) {
		t.Fatalf("texts stored after the restart = %q, want the stalled text once, then the resumed text", got)
	}
	if got := ordered.runningWhenStored(); len(got) == 0 || slices.Contains(got, true) {
		t.Fatalf("holder running when its text was stored = %v, want stored only once it had ended", got)
	}
	if n := countRows(t, rig.path, "engine_text"); n != 1 {
		t.Fatalf("unfinished text rows after the restart = %d, want the resumed turn's 1", n)
	}
}
