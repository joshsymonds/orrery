package enginehost

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/holder"
	"github.com/joshsymonds/orrery/internal/store"
)

// enteredTurnEnd is an Applier that stores through inner, and closes entered when the first batch holding a turn's end
// reaches it.
type enteredTurnEnd struct {
	inner   *turnEndHold
	entered chan struct{}
	once    sync.Once
}

// ApplyEngine stores ops through inner, after it closes entered if ops holds a turn's end.
func (a *enteredTurnEnd) ApplyEngine(ctx context.Context, ops []store.EngineOp, now time.Time) error {
	if slices.ContainsFunc(ops, func(op store.EngineOp) bool {
		_, ok := op.(store.EngineTurnEnded)
		return ok
	}) {
		a.once.Do(func() { close(a.entered) })
	}
	return a.inner.ApplyEngine(ctx, ops, now)
}

// rowsAfterEntry is a Store whose second EngineSessions read, the one Reconcile makes after its holder pass, waits until
// entered is closed. Its first read, which precedes the holder pass, returns at once.
type rowsAfterEntry struct {
	Store
	entered <-chan struct{}
	reads   int
}

// EngineSessions returns the rows of the embedded Store. Its second call waits until entered is closed, or ctx ends.
func (s *rowsAfterEntry) EngineSessions(ctx context.Context) ([]store.EngineRow, error) {
	s.reads++
	if s.reads == 2 {
		select {
		case <-s.entered:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return s.Store.EngineSessions(ctx)
}

// TestReconcileLeavesStoppingDrainingRowToItsPump re-adopts the holder of one version older whose turn ended only in its
// spool, while the store holds the turn's end. The turn end has retired the session, so its pump is still running and
// records the session's state itself. Reconcile returns with the row untouched, and resumes nothing. Once the hold opens,
// the pump records the turn as done, and the owner is told of that alone.
func TestReconcileLeavesStoppingDrainingRowToItsPump(t *testing.T) {
	t.Parallel()
	rig := newCrashRig(t, blockTrigger("fake trickle 1.5"), false)
	dir := t.TempDir()
	id, err := rig.manager.Start(t.Context(), StartRequest{Dir: dir, Prompt: "TRICKLE"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitUntil(t, engineOf(rig.manager), "the store refuses the batch of fake trickle 1.5", rig.crash.isRefusing)
	pid := fakePID(t, dir)
	pids := holderPids(t, holderDir(t, rig.root, id))

	// The hold opens in the test body's defer, which runs before the cleanups that stop the writer it blocks.
	hold := newTurnEndHold(rig.store)
	defer hold.open()
	entered := make(chan struct{})
	rig.manager.Close()
	applier := &enteredTurnEnd{inner: hold, entered: entered}
	writer := NewWriter(WriterConfig{Applier: applier, Tick: 5 * time.Millisecond})
	startRun(t, writer)
	sink := &fakeSink{}
	restarted := newDrainManager(t, &rowsAfterEntry{Store: rig.store, entered: entered}, writer, sink, rig.root,
		holder.WireVersion+1)

	ctx := t.Context()
	if err := restarted.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile while the turn end's write is held: %v", err)
	}

	restarted.mu.Lock()
	retired := restarted.stopping[id] != nil
	restarted.mu.Unlock()
	if !retired {
		t.Fatalf("session %s is not retired while its turn end's write is held", id)
	}
	row, err := rig.store.EngineSession(t.Context(), id)
	if err != nil {
		t.Fatalf("EngineSession while the hold is closed: %v", err)
	}
	if row.State != "working" || row.Reason != "" {
		t.Fatalf("row while the hold is closed = state %q reason %q, want working with no reason",
			row.State, row.Reason)
	}
	if n := stdinLinesWith(t, dir, resumePrompt); n != 0 {
		t.Fatalf("fake claude read %d %q prompts while the hold is closed, want none", n, resumePrompt)
	}
	if got := sink.got(); len(got) != 0 {
		t.Fatalf("notices while the hold is closed = %+v, want none", got)
	}

	hold.open()
	waitHistory(t, rig.path, id, "the done state of the trickled turn", func(h store.EngineHistory) bool {
		return h.State == "done"
	})
	waitUntil(t, engineOf(rig.manager, restarted), "the pump of session "+id+" to end", func() bool {
		restarted.mu.Lock()
		defer restarted.mu.Unlock()
		return restarted.stopping[id] == nil
	})
	waitHolderEnded(t, pids)
	gateWaitGone(t, pid)
	if n := stdinLinesWith(t, dir, resumePrompt); n != 0 {
		t.Fatalf("fake claude read %d %q prompts after the turn ended, want none", n, resumePrompt)
	}
	want := Notice{Session: id, Dir: dir, State: "done", Body: "fake trickle 1.60"}
	if got := sink.got(); len(got) != 1 || got[0] != want {
		t.Fatalf("notices after the turn ended = %+v, want exactly [%+v]", got, want)
	}
}
