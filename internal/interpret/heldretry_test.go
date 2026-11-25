package interpret

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/hostexec"
	"github.com/joshsymonds/orrery/internal/store"
)

// heldRetryOrigin is the OriginMain of the held-retry tests. It counts every call, and returns an error while fail is set
// and commit otherwise.
type heldRetryOrigin struct {
	calls  atomic.Int64
	fail   atomic.Bool
	commit string
}

// OriginMain counts the call, and returns an error while fail is set and commit otherwise.
func (o *heldRetryOrigin) OriginMain(_ context.Context, _ hostexec.Host, _ string) (string, error) {
	o.calls.Add(1)
	if o.fail.Load() {
		return "", errors.New("origin main unreachable")
	}
	return o.commit, nil
}

// heldRetryTimers is the afterFunc of the held-retry tests. It records each timer armed, with its duration and function,
// and never fires one.
type heldRetryTimers struct {
	mu        sync.Mutex
	durations []time.Duration
	funcs     []func()
}

// afterFunc records a timer of d that runs f, and does not run f.
func (t *heldRetryTimers) afterFunc(d time.Duration, f func()) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.durations = append(t.durations, d)
	t.funcs = append(t.funcs, f)
}

// snapshot returns the durations and functions of the timers armed so far.
func (t *heldRetryTimers) snapshot() ([]time.Duration, []func()) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return slices.Clone(t.durations), slices.Clone(t.funcs)
}

// startHeldRetryRun starts a Runner over s whose OriginMain is origin and whose timers are timers, and waits for its start
// pass. The Runner passes once more for each tick the run's pass sends, and nothing else passes it.
func startHeldRetryRun(t *testing.T, s *store.Store, origin *heldRetryOrigin, timers *heldRetryTimers) *heldRun {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	run := &heldRun{ticks: make(chan time.Time), passed: make(chan struct{}), done: make(chan struct{}), cancel: cancel}
	runner := New(Config{
		Store: s, Planner: &fakePlanner{}, Builder: &fakeBuilder{}, Now: runClock, OriginMain: origin.OriginMain,
		Ticks: run.ticks,
		Passed: func() {
			select {
			case run.passed <- struct{}{}:
			case <-ctx.Done():
			}
		},
	})
	runner.afterFunc = timers.afterFunc
	go func() {
		runner.Run(ctx)
		close(run.done)
	}()
	run.awaitPass()
	return run
}

// TestDesignHeldEpicRetryArmsOneTimer asserts that a held epic whose origin main fetch fails has one retry armed, and that
// the passes after it, which find the retry pending, fetch nothing and arm nothing more: the failing fetch is called once,
// and the epic stays held.
func TestDesignHeldEpicRetryArmsOneTimer(t *testing.T) {
	t.Parallel()
	s := openRunStore(t)
	heldStartWorld(t, s, heldStartPredecessor)
	heldStartRelease(t, s, heldStartPredecessor)
	origin := &heldRetryOrigin{commit: heldStartCommit}
	origin.fail.Store(true)
	timers := &heldRetryTimers{}
	run := startHeldRetryRun(t, s, origin, timers)
	for range 10 {
		run.pass()
	}
	run.stop()

	if n := origin.calls.Load(); n != 1 {
		t.Errorf("origin main calls = %d, want 1: a held epic with a retry pending is not fetched", n)
	}
	durations, funcs := timers.snapshot()
	if len(funcs) != 1 {
		t.Fatalf("timers armed = %d, want 1: a held epic has one pending retry", len(funcs))
	}
	if durations[0] != defaultHeldRetry {
		t.Errorf("timer armed for %v, want the held retry %v", durations[0], defaultHeldRetry)
	}
	if got := heldStartEpicRow(t, s).Phase; got != store.PhaseHeld {
		t.Errorf("epic phase = %q after failed fetches, want %q", got, store.PhaseHeld)
	}
}

// TestDesignHeldEpicRetryAfterTimerFires asserts that when the armed retry fires after the origin main fetch fails, the
// next pass fetches the epic again, starts it from the commit the fetch returns, and arms no retry of its own.
func TestDesignHeldEpicRetryAfterTimerFires(t *testing.T) {
	t.Parallel()
	s := openRunStore(t)
	heldStartWorld(t, s, heldStartPredecessor)
	heldStartRelease(t, s, heldStartPredecessor)
	origin := &heldRetryOrigin{commit: heldStartCommit}
	origin.fail.Store(true)
	timers := &heldRetryTimers{}
	run := startHeldRetryRun(t, s, origin, timers)

	_, funcs := timers.snapshot()
	if len(funcs) != 1 {
		t.Fatalf("timers armed after the failed start pass = %d, want 1", len(funcs))
	}
	origin.fail.Store(false)
	funcs[0]()
	run.pass()
	run.stop()

	if n := origin.calls.Load(); n != 2 {
		t.Errorf("origin main calls = %d, want 2: the start pass and the pass after the retry fired", n)
	}
	if row := heldStartEpicRow(t, s); row.Phase != runPhase || row.Base != heldStartCommit {
		t.Errorf("epic phase = %q, base = %q after the retry, want %q and %q",
			row.Phase, row.Base, runPhase, heldStartCommit)
	}
	if _, funcs = timers.snapshot(); len(funcs) != 1 {
		t.Errorf("timers armed = %d after the epic started, want still 1: a started epic arms no retry", len(funcs))
	}
}
