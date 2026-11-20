package enginehost

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/engine/session"
	"github.com/joshsymonds/orrery/internal/store"
)

// stepDeadlineTimer is a timer stepDeadlineTimers armed: its duration, and the channel the test fires it on.
type stepDeadlineTimer struct {
	d    time.Duration
	fire chan time.Time
}

// stepDeadlineTimers is the NewTimer of the step deadline tests. It arms no real timer: each timer it arms is recorded
// with its duration, and it fires only when the test fires it. It counts the timers it has stopped.
type stepDeadlineTimers struct {
	mu      sync.Mutex
	armed   *stepDeadlineTimer
	arms    []time.Duration
	stopped int
}

// newTimer arms a timer for d and returns its channel and its stop. It is the NewTimer a run is given.
func (ts *stepDeadlineTimers) newTimer(d time.Duration) (<-chan time.Time, func() bool) {
	timer := &stepDeadlineTimer{d: d, fire: make(chan time.Time)}
	ts.mu.Lock()
	ts.armed = timer
	ts.arms = append(ts.arms, d)
	ts.mu.Unlock()
	return timer.fire, func() bool {
		ts.mu.Lock()
		defer ts.mu.Unlock()
		if ts.armed != timer {
			return false
		}
		ts.armed = nil
		ts.stopped++
		return true
	}
}

// fire fires the armed timer at now and returns once the run has taken it. It reports false, and fires nothing, when no
// timer is armed.
func (ts *stepDeadlineTimers) fire(now time.Time) bool {
	ts.mu.Lock()
	timer := ts.armed
	ts.armed = nil
	ts.mu.Unlock()
	if timer == nil {
		return false
	}
	timer.fire <- now
	return true
}

// armedDuration returns the duration of the armed timer, and false when none is armed.
func (ts *stepDeadlineTimers) armedDuration() (time.Duration, bool) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if ts.armed == nil {
		return 0, false
	}
	return ts.armed.d, true
}

// armCount returns how many timers have been armed.
func (ts *stepDeadlineTimers) armCount() int {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return len(ts.arms)
}

// stopCount returns how many armed timers have been stopped.
func (ts *stepDeadlineTimers) stopCount() int {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.stopped
}

// stepDeadlineSettle returns once run has handled every event handed to it so far and has armed its deadline for them.
// The run takes an owner action only in its loop, after the iteration that handled what came before, and it re-arms
// before it replies. A run that has ended has nothing to settle.
func stepDeadlineSettle(t *testing.T, run *stepRun) {
	t.Helper()
	err := run.do(t.Context(), func(context.Context) error { return nil })
	if err != nil && !errors.Is(err, ErrStepNotRunning) {
		t.Fatalf("settle run: %v", err)
	}
}

// stepDeadlineWantArmed settles run and asserts that its deadline timer is armed for want.
func stepDeadlineWantArmed(t *testing.T, run *stepRun, timers *stepDeadlineTimers, want time.Duration) {
	t.Helper()
	stepDeadlineSettle(t, run)
	got, armed := timers.armedDuration()
	if !armed {
		t.Fatalf("no deadline armed, want one for %v", want)
	}
	if got != want {
		t.Fatalf("deadline armed for %v, want %v", got, want)
	}
}

// stepDeadlineWantArms asserts that n timers have been armed in all.
func stepDeadlineWantArms(t *testing.T, timers *stepDeadlineTimers, n int) {
	t.Helper()
	if got := timers.armCount(); got != n {
		t.Fatalf("timers armed = %d, want %d", got, n)
	}
}

// stepDeadlineWantOpen settles f's run and asserts that the step has not ended.
func stepDeadlineWantOpen(t *testing.T, f *watchLoopFixture) {
	t.Helper()
	stepDeadlineSettle(t, f.run)
	if row := f.row(t); row.End != "" {
		t.Fatalf("end = %q, want the step open", row.End)
	}
}

// stepDeadlineWaitEnd waits for f's run to return, asserts that the step ended once, with one session stop, and returns
// the step row.
func stepDeadlineWaitEnd(t *testing.T, f *watchLoopFixture) store.StepRow {
	t.Helper()
	row := f.waitEnd(t)
	if got := f.sessions.stoppedIDs(); len(got) != 1 {
		t.Fatalf("session stops = %d, want 1: the step ended once", len(got))
	}
	return row
}

// TestStepWatchOvertimeStepLimitDeadline covers R2: a step whose model-time limit is under watchPeriod is due overtime
// at the limit, so that moment is its deadline and no check runs before it. A check one second short ends nothing and
// arms the second left; the check at the limit ends the step overtime, once.
func TestStepWatchOvertimeStepLimitDeadline(t *testing.T) {
	t.Parallel()
	limit := 2 * time.Minute
	f := newWatchLoopFixture(t, nil, &stepLoopSessions{}, limit)
	f.event(0, session.Started{})
	stepDeadlineWantArmed(t, f.run, f.timers, limit)

	f.tick(t, limit-time.Second)
	stepDeadlineWantArmed(t, f.run, f.timers, time.Second)
	stepDeadlineWantOpen(t, f)

	f.tick(t, limit)
	if row := stepDeadlineWaitEnd(t, f); row.End != string(watchEndOvertime) {
		t.Fatalf("end = %q, want overtime", row.End)
	}
}

// TestStepWatchSilentStepLimitDeadline covers R2: a step whose one nudge is spent is due silent ten minutes of model
// time after its last event, so that moment is its deadline. The run arms the periodic check at five minutes first. The
// test fires the timer at nine minutes fifty-nine seconds, where that periodic check is due and takes no action, which
// leaves silence one second off. A check one second short of silence ends nothing; the check at the moment ends the
// step silent, once.
func TestStepWatchSilentStepLimitDeadline(t *testing.T) {
	t.Parallel()
	f := newWatchLoopFixture(t, nil, &stepLoopSessions{}, watchLoopModelLimit)
	f.run.watch.restore(true, false, false, watchEpoch())
	f.event(0, session.Started{})
	stepDeadlineWantArmed(t, f.run, f.timers, watchPeriod)

	f.tick(t, watchSilence-time.Second)
	stepDeadlineWantArmed(t, f.run, f.timers, time.Second)
	stepDeadlineWantOpen(t, f)

	f.tick(t, watchSilence)
	if row := stepDeadlineWaitEnd(t, f); row.End != string(watchEndSilent) {
		t.Fatalf("end = %q, want silent", row.End)
	}
}

// TestStepWatchAgentGateStepLimitDeadline covers R2: an agent gate step with no turn running is due overtime at its
// gate limit, so that moment is its deadline. A check one second short ends nothing and arms the second left; the check
// at the limit ends the step overtime, once, with no monitor call.
func TestStepWatchAgentGateStepLimitDeadline(t *testing.T) {
	t.Parallel()
	haiku := &watchLoopHaiku{}
	f := newGateLimitTestFixture(t, haiku, &stepLoopSessions{}, gateLimitTestGate())
	stepDeadlineWantArmed(t, f.run, f.timers, gateLimitTestLimit)

	f.tick(t, gateLimitTestLimit-time.Second)
	stepDeadlineWantArmed(t, f.run, f.timers, time.Second)
	stepDeadlineWantOpen(t, f)

	f.tick(t, gateLimitTestLimit)
	gateLimitTestWantOvertime(t, stepDeadlineWaitEnd(t, f))
	if got := haiku.recorded(); len(got) != 0 {
		t.Fatalf("monitor calls = %d, want none", len(got))
	}
}

// TestStepWatchIdleStepLimitDeadline covers R2: a step with no turn running arms no deadline, and a held step arms none
// either, so the clock advanced hours past every limit ends neither.
func TestStepWatchIdleStepLimitDeadline(t *testing.T) {
	t.Parallel()
	idle := newWatchLoopFixture(t, nil, &stepLoopSessions{}, watchLoopModelLimit)
	stepDeadlineSettle(t, idle.run)
	stepDeadlineWantArms(t, idle.timers, 0)
	idle.clock.set(3 * time.Hour)
	stepDeadlineWantOpen(t, idle)
	stepDeadlineWantArms(t, idle.timers, 0)

	held := newWatchLoopFixture(t, nil, &stepLoopSessions{}, watchLoopModelLimit)
	held.event(0, session.Started{})
	stepDeadlineWantArmed(t, held.run, held.timers, watchPeriod)
	if err := held.run.do(t.Context(), held.run.hold); err != nil {
		t.Fatalf("hold: %v", err)
	}
	stepDeadlineSettle(t, held.run)
	if _, armed := held.timers.armedDuration(); armed {
		t.Fatal("held step has a deadline armed, want none")
	}
	held.clock.set(3 * time.Hour)
	stepDeadlineWantOpen(t, held)
	stepDeadlineWantArms(t, held.timers, 1)
}
