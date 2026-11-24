package enginehost

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// deadlineTimer is one idle timer the fake seam armed: the time it fires at by the test clock, the function it calls, and
// whether it is still armed.
type deadlineTimer struct {
	at    time.Time
	fire  func()
	armed bool
}

// deadlineTimers is the fake timer seam of the idle deadline tests. A timer fires only when the test moves the clock to its
// deadline (see advance), so a deadline reaches RunIdle only when the test sends it there.
type deadlineTimers struct {
	clock  *idleClock
	mu     sync.Mutex
	timers []*deadlineTimer
}

// afterFunc arms a timer that fires dur after the clock's now, and returns its stop, as Config.AfterFunc does.
func (d *deadlineTimers) afterFunc(dur time.Duration, f func()) func() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	timer := &deadlineTimer{at: d.clock.now().Add(dur), fire: f, armed: true}
	d.timers = append(d.timers, timer)
	return func() bool {
		d.mu.Lock()
		defer d.mu.Unlock()
		wasArmed := timer.armed
		timer.armed = false
		return wasArmed
	}
}

// armed returns the timers still armed, in the order they were armed.
func (d *deadlineTimers) armed() []*deadlineTimer {
	d.mu.Lock()
	defer d.mu.Unlock()
	var armed []*deadlineTimer
	for _, timer := range d.timers {
		if timer.armed {
			armed = append(armed, timer)
		}
	}
	return armed
}

// isArmed reports whether timer is still armed.
func (d *deadlineTimers) isArmed(timer *deadlineTimer) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return timer.armed
}

// advance moves the clock forward by dur, and fires the armed timers the clock reaches, in the order they were armed. Each
// fire returns once RunIdle has taken its deadline.
func (d *deadlineTimers) advance(dur time.Duration) {
	d.clock.advance(dur)
	now := d.clock.now()
	d.mu.Lock()
	var due []*deadlineTimer
	for _, timer := range d.timers {
		if timer.armed && !timer.at.After(now) {
			timer.armed = false
			due = append(due, timer)
		}
	}
	d.mu.Unlock()
	for _, timer := range due {
		timer.fire()
	}
}

// idleChecksRun returns how many idle checks m has run.
func idleChecksRun(m *Manager) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.idleChecks
}

// newIdleDeadlineManager returns a manager whose clock and idle timers are the test's. Its idle checks
// run once runIdleDeadlines starts RunIdle. Its store is at the returned path.
func newIdleDeadlineManager(t *testing.T) (*Manager, string, *deadlineTimers) {
	t.Helper()
	m, path, clock := newIdleManager(t)
	timers := &deadlineTimers{clock: clock}
	m.afterFunc = timers.afterFunc
	return m, path, timers
}

// runIdleDeadlines runs m's RunIdle on its own goroutine, and returns the function that ends it and waits until it has
// returned. Once that function returns, the check of the last deadline handed to RunIdle is over. The test ends it too.
func runIdleDeadlines(t *testing.T, m *Manager) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	returned := make(chan struct{})
	go func() {
		m.RunIdle(ctx)
		close(returned)
	}()
	end := func() {
		cancel()
		<-returned
	}
	t.Cleanup(end)
	return end
}

func TestIdleDeadlineStopsSessionAtItsDeadline(t *testing.T) {
	t.Parallel()
	m, path, timers := newIdleDeadlineManager(t)
	end := runIdleDeadlines(t, m)
	id, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "hi"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the first reply done", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1") && lastState(t, h) == string(protocol.StateDone)
	})
	live, err := m.live(id)
	if err != nil {
		t.Fatalf("live before the deadline: %v", err)
	}
	armed := timers.armed()
	if len(armed) != 1 {
		t.Fatalf("%d idle timers armed, want the one for the session", len(armed))
	}
	if want := fixedClock().Add(defaultIdleAfter); !armed[0].at.Equal(want) {
		t.Fatalf("idle timer due at %v, want %v", armed[0].at, want)
	}

	timers.advance(defaultIdleAfter - time.Millisecond)
	if _, err := m.live(id); err != nil {
		t.Fatalf("session stopped a millisecond before its deadline: %v", err)
	}

	timers.advance(time.Millisecond)
	end()
	if _, err := m.live(id); !errors.Is(err, ErrNoSession) {
		t.Fatalf("live at the deadline: err = %v, want ErrNoSession", err)
	}
	select {
	case <-live.done:
	default:
		t.Fatal("the pump of the session the deadline stopped is still running")
	}
}

func TestIdleDeadlineBusySessionIsNotStopped(t *testing.T) {
	t.Parallel()
	m, path, timers := newIdleDeadlineManager(t)
	end := runIdleDeadlines(t, m)
	id, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "hi"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the first reply done", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1") && lastState(t, h) == string(protocol.StateDone)
	})
	spell := timers.armed()
	if len(spell) != 1 {
		t.Fatalf("%d idle timers armed, want the one for the session", len(spell))
	}

	timers.advance(5 * time.Minute)
	if err := m.Say(t.Context(), SayRequest{ID: id, Text: "HANG"}); err != nil {
		t.Fatalf("Say HANG: %v", err)
	}
	if timers.isArmed(spell[0]) {
		t.Fatal("the idle timer of a session that turned busy is still armed")
	}

	timers.advance(10 * time.Minute)
	if n := idleChecksRun(m); n != 0 {
		t.Fatalf("%d idle checks ran for a busy session, want none", n)
	}

	// The deadline of the idle spell that ended reaches RunIdle anyway, as a timer that fired before the session turned
	// busy would deliver it. The check finds the session busy and stops nothing.
	spell[0].fire()
	end()
	if _, err := m.live(id); err != nil {
		t.Fatalf("busy session stopped at an idle spell's deadline: %v", err)
	}
	if n := idleChecksRun(m); n != 1 {
		t.Fatalf("%d idle checks ran, want the one delivered deadline", n)
	}
}

func TestIdleDeadlineChecksOnlyAtDeadlines(t *testing.T) {
	t.Parallel()
	m, path, timers := newIdleDeadlineManager(t)
	end := runIdleDeadlines(t, m)
	id, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "hi"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the first reply done", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1") && lastState(t, h) == string(protocol.StateDone)
	})

	// Moving the clock through the idle spell in 30 s steps runs no check and arms no timer beyond the session's own.
	for step := range 19 {
		timers.advance(30 * time.Second)
		if n := idleChecksRun(m); n != 0 {
			t.Fatalf("%d idle checks ran by step %d of the idle spell, want none", n, step+1)
		}
		if n := len(timers.armed()); n != 1 {
			t.Fatalf("%d idle timers armed by step %d of the idle spell, want the one for the session", n, step+1)
		}
	}
	timers.advance(defaultIdleAfter - 19*30*time.Second - time.Millisecond)
	if n := idleChecksRun(m); n != 0 {
		t.Fatalf("%d idle checks ran a millisecond before the deadline, want none", n)
	}

	timers.advance(time.Millisecond)
	end()
	if n := idleChecksRun(m); n != 1 {
		t.Fatalf("%d idle checks ran by the deadline, want the one at it", n)
	}
}
