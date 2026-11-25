package enginehost

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// dueDeadlineClock is the clock of the deadline tests. A test moves it between passes.
type dueDeadlineClock struct {
	mu  sync.Mutex
	now time.Time
}

// read returns the clock's time.
func (c *dueDeadlineClock) read() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// set moves the clock to at.
func (c *dueDeadlineClock) set(at time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = at
}

// dueDeadlineTimer is a timer a deadline test's NewTimer armed: its duration, and the channel the test fires it on.
type dueDeadlineTimer struct {
	d    time.Duration
	fire chan time.Time
}

// dueDeadlineTimers is the NewTimer of the deadline tests. It arms no real timer: each timer it arms is sent on armed for
// the test to receive, and it fires only when the test sends on its fire channel.
type dueDeadlineTimers struct {
	armed chan dueDeadlineTimer
}

// newTimer arms a fake timer for d and sends it on armed. Its stop reports true, as a timer stopped before it fires does.
func (t *dueDeadlineTimers) newTimer(d time.Duration) (<-chan time.Time, func() bool) {
	fire := make(chan time.Time, 1)
	t.armed <- dueDeadlineTimer{d: d, fire: fire}
	return fire, func() bool { return true }
}

// dueDeadlineSteps is the DueSteps of the deadline tests. Each AutoResume is sent on calls, and returns err.
type dueDeadlineSteps struct {
	err   error
	calls chan dueStepCall
}

// newDueDeadlineSteps returns the DueSteps whose AutoResume returns err.
func newDueDeadlineSteps(err error) *dueDeadlineSteps {
	return &dueDeadlineSteps{err: err, calls: make(chan dueStepCall, 8)}
}

// AutoResume sends the call on calls, unless ctx ends first, and returns the scripted error.
func (s *dueDeadlineSteps) AutoResume(ctx context.Context, id string, resets time.Time) error {
	select {
	case s.calls <- dueStepCall{id: id, resets: resets.Unix()}:
	case <-ctx.Done():
	}
	return s.err
}

// dueDeadlineRig is a DuePass that its Run drives on its own goroutine, over a fake clock and fake timers.
type dueDeadlineRig struct {
	pass   *DuePass
	clock  *dueDeadlineClock
	timers *dueDeadlineTimers
	steps  *dueDeadlineSteps
	cancel context.CancelFunc
	done   chan struct{}
}

// dueDeadlineStart runs a DuePass over st and steps on its own goroutine, its clock reading start. The test's end stops
// it.
func dueDeadlineStart(t *testing.T, st DueStore, steps *dueDeadlineSteps, start time.Time) *dueDeadlineRig {
	t.Helper()
	rig := &dueDeadlineRig{
		clock:  &dueDeadlineClock{now: start},
		timers: &dueDeadlineTimers{armed: make(chan dueDeadlineTimer, 8)},
		steps:  steps,
		done:   make(chan struct{}),
	}
	rig.pass = NewDuePass(DuePassConfig{Store: st, Steps: steps, Now: rig.clock.read, NewTimer: rig.timers.newTimer})
	ctx, cancel := context.WithCancel(t.Context())
	rig.cancel = cancel
	go func() {
		defer close(rig.done)
		rig.pass.Run(ctx)
	}()
	t.Cleanup(rig.stop)
	return rig
}

// stop cancels Run and waits for it to return. Once it has, each pass Run made, and each timer it armed, is done.
func (r *dueDeadlineRig) stop() {
	r.cancel()
	<-r.done
}

// awaitArmed receives the next timer the rig arms. It fails the test when Run returns first.
func (r *dueDeadlineRig) awaitArmed(t *testing.T) dueDeadlineTimer {
	t.Helper()
	select {
	case timer := <-r.timers.armed:
		return timer
	case <-r.done:
		t.Fatal("Run returned before it armed the timer the test waits for")
		return dueDeadlineTimer{}
	}
}

// awaitCall receives the next AutoResume the rig's steps record. It fails the test when Run returns first.
func (r *dueDeadlineRig) awaitCall(t *testing.T) dueStepCall {
	t.Helper()
	select {
	case call := <-r.steps.calls:
		return call
	case <-r.done:
		t.Fatal("Run returned before the resume the test waits for")
		return dueStepCall{}
	}
}

// requireNoTimer fails the test when the rig has armed a timer the test has not received.
func (r *dueDeadlineRig) requireNoTimer(t *testing.T) {
	t.Helper()
	select {
	case timer := <-r.timers.armed:
		t.Fatalf("timer armed for %v, want none", timer.d)
	default:
	}
}

// requireNoCall fails the test when the rig's steps recorded an AutoResume the test has not received.
func (r *dueDeadlineRig) requireNoCall(t *testing.T) {
	t.Helper()
	select {
	case call := <-r.steps.calls:
		t.Fatalf("AutoResume of %s for reset %d, want none", call.id, call.resets)
	default:
	}
}

// TestDuePassDueDeadlineResumesAtItsMoment covers R1: a due step row resets at R, so its moment is a minute after. The
// start pass, with the clock a second before R, resumes nothing and arms a timer for the moment. A fire of that timer
// with the clock a second short of the moment resumes nothing and arms the second, and a fire at the moment resumes the
// step once, for R.
func TestDuePassDueDeadlineResumesAtItsMoment(t *testing.T) {
	t.Parallel()
	st, _ := openStore(t)
	resets := time.Unix(probeResetUnix, 0)
	addDueStep(t, st, duePassStep, resets)
	moment := resets.Add(dueGrace)
	start := resets.Add(-time.Second)
	rig := dueDeadlineStart(t, st, newDueDeadlineSteps(nil), start)

	first := rig.awaitArmed(t)
	if want := moment.Sub(start); first.d != want {
		t.Fatalf("timer armed for %v at the start, want %v, the moment less the clock", first.d, want)
	}
	rig.requireNoCall(t)

	rig.clock.set(moment.Add(-time.Second))
	first.fire <- time.Time{}
	second := rig.awaitArmed(t)
	if second.d != time.Second {
		t.Fatalf("timer re-armed for %v one second short of the moment, want 1s", second.d)
	}
	rig.requireNoCall(t)

	rig.clock.set(moment)
	second.fire <- time.Time{}
	want := dueStepCall{id: duePassStep, resets: resets.Unix()}
	if call := rig.awaitCall(t); call != want {
		t.Fatalf("AutoResume at the moment = %+v, want %+v", call, want)
	}
	rig.stop()
	rig.requireNoCall(t)
	rig.requireNoTimer(t)
}

// dueDeadlineReads is the DueStore of the idle test. It reads the due rows of the store, then sends on reads, so a test
// that receives from reads knows a pass has read its rows and has nothing left that reads the context.
type dueDeadlineReads struct {
	*store.Store
	reads chan struct{}
}

// DueResumes reads the due rows of the store, and sends on reads once the read has returned.
func (r dueDeadlineReads) DueResumes(ctx context.Context) ([]store.DueResume, error) {
	rows, err := r.Store.DueResumes(ctx)
	r.reads <- struct{}{}
	return rows, err
}

// TestDuePassDueDeadlineIdleArmsNothing covers R1: with no due row, the start pass arms no timer, and a wake gives
// exactly one more pass.
func TestDuePassDueDeadlineIdleArmsNothing(t *testing.T) {
	t.Parallel()
	st, _ := openStore(t)
	reads := make(chan struct{}, 4)
	rig := dueDeadlineStart(
		t, dueDeadlineReads{Store: st, reads: reads}, newDueDeadlineSteps(nil), time.Unix(probeResetUnix, 0),
	)

	awaitPass(t, reads, rig.done, "the pass at start")
	rig.pass.Wake()
	awaitPass(t, reads, rig.done, "the pass on the wake")
	rig.stop()
	rig.requireNoTimer(t)
	if got := len(reads); got != 0 {
		t.Fatalf("passes after the wake = %d more, want none", got)
	}
}

// TestDuePassDueDeadlineGateRefusalRetriesOnce covers R1: a row the step's gate refuses arms one timer for dueRetry, and
// firing it resumes the step once more, which arms the next retry.
func TestDuePassDueDeadlineGateRefusalRetriesOnce(t *testing.T) {
	t.Parallel()
	st, _ := openStore(t)
	resets := time.Unix(probeResetUnix, 0)
	addDueStep(t, st, duePassStep, resets)
	rig := dueDeadlineStart(t, st, newDueDeadlineSteps(ErrStepGating), resets.Add(dueGrace))
	want := dueStepCall{id: duePassStep, resets: resets.Unix()}

	if call := rig.awaitCall(t); call != want {
		t.Fatalf("AutoResume at start = %+v, want %+v", call, want)
	}
	retry := rig.awaitArmed(t)
	if retry.d != dueRetry {
		t.Fatalf("timer armed for %v after the gate's refusal, want %v", retry.d, dueRetry)
	}
	rig.requireNoCall(t)
	rig.requireNoTimer(t)

	retry.fire <- time.Time{}
	if call := rig.awaitCall(t); call != want {
		t.Fatalf("AutoResume when the retry fires = %+v, want %+v", call, want)
	}
	next := rig.awaitArmed(t)
	if next.d != dueRetry {
		t.Fatalf("timer armed for %v after the retry's refusal, want %v", next.d, dueRetry)
	}
	rig.requireNoCall(t)
}
