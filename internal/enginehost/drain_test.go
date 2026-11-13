package enginehost

import (
	"context"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/holder"
	"github.com/joshsymonds/orrery/internal/store"
)

// textSpy is an Applier that stores through inner and keeps the text of each EngineText op it stored, in order.
type textSpy struct {
	inner Applier

	mu    sync.Mutex
	texts []string
}

// ApplyEngine stores ops through inner, then keeps the text of each EngineText among them.
func (s *textSpy) ApplyEngine(ctx context.Context, ops []store.EngineOp, now time.Time) error {
	if err := s.inner.ApplyEngine(ctx, ops, now); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, op := range ops {
		if streamed, ok := op.(store.EngineText); ok {
			s.texts = append(s.texts, streamed.Text)
		}
	}
	return nil
}

// storedTexts returns the texts of the EngineText ops the spy stored, in order.
func (s *textSpy) storedTexts() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.texts)
}

// textTrigger accepts a batch that stores unfinished text whose streamed text is text.
func textTrigger(text string) func([]store.EngineOp) bool {
	return func(ops []store.EngineOp) bool {
		for _, op := range ops {
			if streamed, ok := op.(store.EngineText); ok && streamed.Text == text {
				return true
			}
		}
		return false
	}
}

// restartOver ends the manager of rig as a crash leaves it, then restarts orreryd over the same store and state dir with
// its writer storing through spy, and runs its Reconcile. It returns the notices the restart sent.
func restartOver(t *testing.T, rig crashRig, spy *textSpy) *fakeSink {
	t.Helper()
	return restartOverEvery(t, rig, spy, 5*time.Millisecond)
}

// restartOverEvery is restartOver with the restarted writer storing once per tick, so a test can leave the restart's
// ops queued while Reconcile reads the store.
func restartOverEvery(t *testing.T, rig crashRig, spy *textSpy, tick time.Duration) *fakeSink {
	t.Helper()
	rig.manager.Close()
	writer := NewWriter(WriterConfig{Applier: spy, Tick: tick})
	startRun(t, writer)
	sink := &fakeSink{}
	restarted := newStateManager(t, rig.store, writer, sink, fakeSystemdRunPath(t), rig.root, nil)
	if err := restarted.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile after the crash: %v", err)
	}
	return sink
}

// writeAsController writes line to the child of the holder that serves socket, under the write id id, from a connection
// that no Process wraps. The holder acknowledges the write, and the connection then closes. Nothing removes the holder's
// directory when the holder ends, as a Process would unless it were detached.
func writeAsController(t *testing.T, socket string, from int64, id string, line []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), managerGuard)
	defer cancel()
	conn, err := holder.Dial(ctx, socket)
	if err != nil {
		t.Fatalf("dial holder %s: %v", socket, err)
	}
	if _, err := conn.Attach(from); err != nil {
		t.Fatalf("attach holder %s at %d: %v", socket, from, err)
	}
	if err := conn.Write(id, line); err != nil {
		t.Fatalf("write %s to holder %s: %v", id, socket, err)
	}
	for {
		frame, err := conn.Read()
		if err != nil {
			t.Fatalf("read the holder's answer to write %s: %v", id, err)
		}
		if frame.Type == holder.TypeWritten && frame.ID == id {
			break
		}
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("close controller connection to %s: %v", socket, err)
	}
}

// holderPids returns the pids record of the holder in dir, once the holder has written it.
func holderPids(t *testing.T, dir string) holder.Pids {
	t.Helper()
	var pids holder.Pids
	waitUntil(t, "the pids record of "+dir, func() bool {
		var err error
		pids, err = holder.ReadPids(dir)
		return err == nil
	})
	return pids
}

// waitHolderEnded waits until the holder process that pids records has ended, as /proc shows it.
func waitHolderEnded(t *testing.T, pids holder.Pids) {
	t.Helper()
	waitUntil(t, "the holder process to end", func() bool {
		running, err := holder.Running(pids.Holder, pids.HolderStart)
		if err != nil {
			t.Fatalf("read holder %d: %v", pids.Holder, err)
		}
		return !running
	})
}

func TestDrainStoresDieTurnWrittenWhileDownOnce(t *testing.T) {
	rig := newCrashRig(t, nil, false)
	dir := t.TempDir()
	id, err := rig.manager.Start(t.Context(), StartRequest{Dir: dir, Prompt: "hi"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, rig.path, id, "the done state of the first turn", func(h store.EngineHistory) bool {
		return h.State == "done"
	})
	pid := fakePID(t, dir)
	pids := holderPids(t, holderDir(t, rig.root, id))
	row, err := rig.store.EngineSession(t.Context(), id)
	if err != nil {
		t.Fatalf("read row of session %s: %v", id, err)
	}
	// orreryd goes down with its session detached, so the holder and its child run on with no controller.
	rig.manager.Close()
	dieLine := []byte(`{"type":"user","message":{"role":"user","content":"DIE"}}` + "\n")
	writeAsController(t, holderSocket(t, rig.root, id), row.ReplayFrom, "die-prompt", dieLine)
	waitHolderEnded(t, pids)

	spy := &textSpy{inner: rig.store}
	sink := restartOver(t, rig, spy)

	if got := spy.storedTexts(); !slices.Equal(got, []string{"fake dying 2"}) {
		t.Fatalf("texts stored after the restart = %q, want the DIE turn's text once", got)
	}
	history := historyOf(t, rig.path, id)
	if history.State != "failed" || !strings.Contains(history.Reason, "exit status 3") ||
		!strings.HasSuffix(history.Reason, "fake died") {
		t.Fatalf("state after the restart = %q reason %q, want failed with exit status 3 and fake died",
			history.State, history.Reason)
	}
	failures := 0
	for _, fields := range changedFields(t, history) {
		if strings.Contains(fields["reason"], "fake died") {
			failures++
		}
	}
	if failures != 1 {
		t.Fatalf("failure reasons naming fake died stored = %d, want 1", failures)
	}
	requireDirGone(t, holderDir(t, rig.root, id))
	gateWaitGone(t, pid)
	got := sink.got()
	if len(got) != 1 || got[0].State != "failed" || !strings.HasSuffix(got[0].Body, "fake died") {
		t.Fatalf("notices after the restart = %+v, want one failed notice naming fake died", got)
	}
}

func TestDrainKilledHolderStoresStalledTextAndInterruptsTurn(t *testing.T) {
	rig := newCrashRig(t, textTrigger("fake stalling 1"), false)
	dir := t.TempDir()
	id, err := rig.manager.Start(t.Context(), StartRequest{Dir: dir, Prompt: "STALL"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	// The store refuses the stalled block's first streamed text, so the holder spools it unrecorded.
	waitUntil(t, "the store refuses the batch of fake stalling 1", rig.crash.isRefusing)
	pid := fakePID(t, dir)
	pids := holderPids(t, holderDir(t, rig.root, id))
	// orreryd goes down with its session detached, so nothing removes the holder's directory when the holder dies.
	rig.manager.Close()
	running, err := holder.Running(pids.Holder, pids.HolderStart)
	if err != nil || !running {
		t.Fatalf("holder %d running = %v, err %v, want running before the kill", pids.Holder, running, err)
	}
	if err := syscall.Kill(pids.Holder, syscall.SIGKILL); err != nil {
		t.Fatalf("kill holder %d: %v", pids.Holder, err)
	}
	waitHolderEnded(t, pids)

	spy := &textSpy{inner: rig.store}
	sink := restartOver(t, rig, spy)

	if got := spy.storedTexts(); !slices.Equal(got, []string{"fake stalling 1"}) {
		t.Fatalf("texts stored after the restart = %q, want the stalled text once", got)
	}
	gateWaitGone(t, pid)
	requireDirGone(t, holderDir(t, rig.root, id))
	requireEngineRows(t, rig.store, map[string]restartRow{
		id: {state: "interrupted", reason: restartedReason},
	})
	if n := countRows(t, rig.path, "engine_text"); n != 0 {
		t.Fatalf("unfinished text rows after the restart = %d, want 0", n)
	}
	want := Notice{Session: id, Dir: dir, State: "interrupted", Body: restartedReason}
	if got := sink.got(); len(got) != 1 || got[0] != want {
		t.Fatalf("notices = %+v, want exactly [%+v]", got, want)
	}
}

func TestDrainOfAWorkingTurnWhoseChildDiedRecordsItFailed(t *testing.T) {
	rig := newCrashRig(t, textTrigger("fake stalling 1"), false)
	dir := t.TempDir()
	id, err := rig.manager.Start(t.Context(), StartRequest{Dir: dir, Prompt: "STALL"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	// The store refuses the stalled text, so the row is still working when orreryd goes down.
	waitUntil(t, "the store refuses the batch of fake stalling 1", rig.crash.isRefusing)
	pid := fakePID(t, dir)
	pids := holderPids(t, holderDir(t, rig.root, id))
	rig.manager.Close()
	// The child dies while orreryd is down, so its holder writes an exit record of its own end and ends.
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatalf("kill fake claude %d: %v", pid, err)
	}
	waitHolderEnded(t, pids)

	// A slow tick leaves the drained exit queued while the drain settles the row, as a loaded store would.
	sink := restartOverEvery(t, rig, &textSpy{inner: rig.store}, time.Second)

	history := historyOf(t, rig.path, id)
	if history.State != "failed" || !strings.Contains(history.Reason, "signal: killed") {
		t.Fatalf("state after the restart = %q reason %q, want failed with signal: killed",
			history.State, history.Reason)
	}
	got := sink.got()
	if len(got) != 1 || got[0].State != "failed" {
		t.Fatalf("notices after the restart = %+v, want one failed notice", got)
	}
}
