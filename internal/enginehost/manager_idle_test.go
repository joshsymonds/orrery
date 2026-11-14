package enginehost

import (
	"context"
	"errors"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// idleClock is the clock of the idle tests. The test sets it by hand while the manager's goroutines read it, so it is
// atomic.
type idleClock struct{ nanos atomic.Int64 }

// newIdleClock returns a clock at the fixed test time.
func newIdleClock() *idleClock {
	c := &idleClock{}
	c.nanos.Store(fixedClock().UnixNano())
	return c
}

// now returns the clock's time.
func (c *idleClock) now() time.Time { return time.Unix(0, c.nanos.Load()) }

// advance moves the clock forward by d.
func (c *idleClock) advance(d time.Duration) { c.nanos.Add(int64(d)) }

// newIdleManager returns a manager with the given cap whose clock is set by hand, and the idle timeout left at its
// default. Its store is at the returned path.
func newIdleManager(t *testing.T, limit int) (*Manager, string, *idleClock) {
	t.Helper()
	m, path := newTestManager(t, limit, fakeClaudePath(t))
	clock := newIdleClock()
	m.now = clock.now
	return m, path, clock
}

// lastState returns the state the last engine_changed event of history sets, or "" when none sets one.
func lastState(t *testing.T, history store.EngineHistory) string {
	t.Helper()
	state := ""
	for _, fields := range changedFields(t, history) {
		if s, ok := fields["state"]; ok {
			state = s
		}
	}
	return state
}

// requireRunning checks that session id is live and that its process pid still runs.
func requireRunning(t *testing.T, m *Manager, id string, pid int) {
	t.Helper()
	if _, err := m.live(id); err != nil {
		t.Fatalf("session %s is not live: %v", id, err)
	}
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("process %d of session %s is gone: %v", pid, id, err)
	}
}

func TestIdleStopsFinishedSessionAfterTenMinutes(t *testing.T) {
	t.Parallel()
	m, path, clock := newIdleManager(t, 1)
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "hi"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, path, id, "the first reply done", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1") && lastState(t, h) == string(protocol.StateDone)
	})
	pid := fakePID(t, dir)
	before := historyOf(t, path, id)

	clock.advance(10*time.Minute - time.Millisecond)
	m.checkIdle(t.Context())
	requireRunning(t, m, id, pid)

	clock.advance(time.Millisecond)
	m.checkIdle(t.Context())
	requireGone(t, pid)
	if _, err := m.live(id); !errors.Is(err, ErrNoSession) {
		t.Fatalf("live after idle stop: err = %v, want ErrNoSession", err)
	}
	after := historyOf(t, path, id)
	if len(after.Events) != len(before.Events) {
		t.Fatalf("idle stop changed events: %d before, %d after", len(before.Events), len(after.Events))
	}
	if got := lastState(t, after); got != string(protocol.StateDone) {
		t.Fatalf("state after idle stop = %q, want %q", got, protocol.StateDone)
	}

	if err := m.Say(t.Context(), SayRequest{ID: id, Text: "again"}); err != nil {
		t.Fatalf("Say of an idle-stopped session: %v", err)
	}
	waitHistory(t, path, id, "the resumed reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 2")
	})
}

func TestIdleKeepsAskAndHangSessions(t *testing.T) {
	t.Parallel()
	m, path, clock := newIdleManager(t, 2)
	askDir, hangDir := t.TempDir(), t.TempDir()
	askID, err := m.Start(t.Context(), StartRequest{Dir: askDir, Prompt: "ASK"})
	if err != nil {
		t.Fatalf("Start ASK: %v", err)
	}
	hangID, err := m.Start(t.Context(), StartRequest{Dir: hangDir, Prompt: "HANG"})
	if err != nil {
		t.Fatalf("Start HANG: %v", err)
	}
	waitHistory(t, path, askID, "the open question", func(h store.EngineHistory) bool {
		return lastState(t, h) == string(protocol.StateWaiting)
	})
	waitHistory(t, path, hangID, "the hanging reply", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake hanging 1")
	})
	askPID := fakePID(t, askDir)
	hangPID := fakePID(t, hangDir)

	clock.advance(11 * time.Minute)
	m.checkIdle(t.Context())
	requireRunning(t, m, askID, askPID)
	requireRunning(t, m, hangID, hangPID)
}

func TestIdleKeepsBackgroundSessionUntilItEnds(t *testing.T) {
	t.Parallel()
	m, path, clock := newIdleManager(t, 1)
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "BACKGROUND"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, path, id, "the background turn done", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake background 1") && lastState(t, h) == string(protocol.StateDone)
	})
	pid := fakePID(t, dir)

	clock.advance(11 * time.Minute)
	m.checkIdle(t.Context())
	requireRunning(t, m, id, pid)

	if err := m.Say(t.Context(), SayRequest{ID: id, Text: "BGEND"}); err != nil {
		t.Fatalf("Say BGEND: %v", err)
	}
	waitHistory(t, path, id, "the reply after the background task ended", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 2") && lastState(t, h) == string(protocol.StateDone)
	})

	clock.advance(10*time.Minute - time.Millisecond)
	m.checkIdle(t.Context())
	requireRunning(t, m, id, pid)

	clock.advance(time.Millisecond)
	m.checkIdle(t.Context())
	requireGone(t, pid)
}

func TestIdleStopFreesSlotForStart(t *testing.T) {
	t.Parallel()
	m, path, clock := newIdleManager(t, 1)
	id, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "hi"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, path, id, "the first reply done", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1") && lastState(t, h) == string(protocol.StateDone)
	})
	if _, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "hi"}); !errors.Is(err, ErrCapReached) {
		t.Fatalf("Start at the cap: err = %v, want ErrCapReached", err)
	}

	clock.advance(10 * time.Minute)
	m.checkIdle(t.Context())
	next, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "hi"})
	if err != nil {
		t.Fatalf("Start after the idle stop: %v", err)
	}
	waitHistory(t, path, next, "the reply of the next session", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1")
	})
}

func TestRunIdleReturnsWhenContextEnds(t *testing.T) {
	t.Parallel()
	m, _, _ := newIdleManager(t, 1)
	ctx, cancel := context.WithCancel(t.Context())
	returned := make(chan struct{})
	go func() {
		m.RunIdle(ctx)
		close(returned)
	}()
	cancel()
	select {
	case <-returned:
	case <-time.After(managerGuard):
		t.Fatalf("RunIdle did not return within %v of its context ending", managerGuard)
	}
}
