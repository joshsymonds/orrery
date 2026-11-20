package enginehost

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// Synthetic values for the outside hold tests. The repository, the labels and the limits are invented.
const (
	slotOutsideRepo = "synthetic-outside-repo"
	slotOutsideE1   = "gambit:e1"
	slotOutsideE2   = "gambit:e2"
	// slotOutsideHold is the limit of the hold that the first label takes in the tests.
	slotOutsideHold = 30 * time.Minute
	// slotOutsideWait is the limit the waiting label asks for in the tests.
	slotOutsideWait = 10 * time.Minute
	// slotOutsideBuffer sizes the channels the fakes record on, so a pass never blocks on them.
	slotOutsideBuffer = 64
)

// slotOutsideStart is the instant the clock starts at. It is whole seconds, as the store keeps deadlines in seconds.
func slotOutsideStart() time.Time { return time.Date(2026, time.November, 19, 9, 0, 0, 0, time.UTC) }

// slotOutsideClock is the clock the tests read as now. The Run goroutine reads it while the test sets it, so it is guarded.
type slotOutsideClock struct {
	mu sync.Mutex
	at time.Time
}

// read returns the clock's time.
func (c *slotOutsideClock) read() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

// set moves the clock to at.
func (c *slotOutsideClock) set(at time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = at
}

// slotOutsideTimer is a CronTimer that the test fires by sending on c. d is the duration it was armed for.
type slotOutsideTimer struct {
	c chan time.Time
	d time.Duration
}

// C returns the channel the test fires the timer on.
func (t *slotOutsideTimer) C() <-chan time.Time { return t.c }

// Stop reports that the timer was stopped. The fake has nothing to stop, so it reports true, as a timer does when it
// stops one that is still armed.
func (t *slotOutsideTimer) Stop() bool { return true }

// slotOutsideTimers records each timer the ReleaseSlots arms, in order.
type slotOutsideTimers struct {
	armed chan *slotOutsideTimer
}

// arm returns a new fake timer armed for d, and records it.
func (f *slotOutsideTimers) arm(d time.Duration) CronTimer {
	timer := &slotOutsideTimer{c: make(chan time.Time, 1), d: d}
	f.armed <- timer
	return timer
}

// awaitArmed waits for the next timer the ReleaseSlots arms, and fails the test unless it was armed for want.
func (f *slotOutsideTimers) awaitArmed(t *testing.T, want time.Duration) *slotOutsideTimer {
	t.Helper()
	timer := <-f.armed
	if timer.d != want {
		t.Fatalf("a timer was armed for %v, want %v", timer.d, want)
	}
	return timer
}

// slotOutsideFreed counts the calls to Freed.
type slotOutsideFreed struct {
	mu sync.Mutex
	n  int
}

// call records one call to Freed.
func (f *slotOutsideFreed) call() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n++
}

// count returns how many times Freed was called.
func (f *slotOutsideFreed) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.n
}

// slotOutsidePasses is a ReleaseSlotStore over the store that reports on passed each time NextReleaseSlotDeadline
// returns. That read is the last store read of a pass, so a receive on passed waits for the pass to finish its reads.
type slotOutsidePasses struct {
	ReleaseSlotStore
	passed chan struct{}
}

// NextReleaseSlotDeadline reads the next deadline from the store, and then reports that a pass has read the store.
func (p slotOutsidePasses) NextReleaseSlotDeadline(ctx context.Context) (time.Time, bool, error) {
	at, held, err := p.ReleaseSlotStore.NextReleaseSlotDeadline(ctx)
	p.passed <- struct{}{}
	return at, held, err
}

// slotOutsideRig is a ReleaseSlots over a store, with the clock, the timers and the Freed calls it runs on.
type slotOutsideRig struct {
	clock  *slotOutsideClock
	timers *slotOutsideTimers
	freed  *slotOutsideFreed
	passed chan struct{}
	slots  *ReleaseSlots
}

// newSlotOutsideRig returns a ReleaseSlots over st whose clock reads slotOutsideStart.
func newSlotOutsideRig(st ReleaseSlotStore) *slotOutsideRig {
	rig := &slotOutsideRig{
		clock:  &slotOutsideClock{at: slotOutsideStart()},
		timers: &slotOutsideTimers{armed: make(chan *slotOutsideTimer, slotOutsideBuffer)},
		freed:  &slotOutsideFreed{},
		passed: make(chan struct{}, slotOutsideBuffer),
	}
	rig.slots = NewReleaseSlots(ReleaseSlotsConfig{
		Store:    slotOutsidePasses{ReleaseSlotStore: st, passed: rig.passed},
		Now:      rig.clock.read,
		NewTimer: rig.timers.arm,
		Freed:    rig.freed.call,
	})
	return rig
}

// slotOutsideRun runs the rig's Run on its own goroutine until the test ends.
func slotOutsideRun(t *testing.T, rig *slotOutsideRig) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		rig.slots.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
}

// slotOutsideWaitResult is what a HoldWait returns.
type slotOutsideWaitResult struct {
	holder store.ReleaseSlotHolder
	err    error
}

// slotOutsideStartWait runs HoldWait for label with a limit of slotOutsideWait on its own goroutine. It returns the
// channel that the waiting callback sends each holder it waits behind on, and the channel that HoldWait's result is
// sent on.
func slotOutsideStartWait(
	ctx context.Context, rig *slotOutsideRig, label string,
) (<-chan store.ReleaseSlotHolder, <-chan slotOutsideWaitResult) {
	waited := make(chan store.ReleaseSlotHolder, slotOutsideBuffer)
	done := make(chan slotOutsideWaitResult, 1)
	go func() {
		holder, err := rig.slots.HoldWait(
			ctx,
			slotOutsideRepo,
			label,
			slotOutsideWait,
			func(h store.ReleaseSlotHolder) error {
				waited <- h
				return nil
			},
		)
		done <- slotOutsideWaitResult{holder: holder, err: err}
	}()
	return waited, done
}

// TestSlotOutsideHoldLimitBounds checks that a limit that is not more than zero and at most MaxReleaseSlotLimit is
// refused without writing the store, and that exactly MaxReleaseSlotLimit holds with the deadline the clock sets.
func TestSlotOutsideHoldLimitBounds(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	st, _ := openStore(t)
	rig := newSlotOutsideRig(st)

	for _, limit := range []time.Duration{0, -time.Minute, MaxReleaseSlotLimit + time.Second} {
		_, held, err := rig.slots.Hold(ctx, slotOutsideRepo, slotOutsideE1, limit)
		if !errors.Is(err, ErrReleaseSlotLimit) {
			t.Errorf("Hold for %v: err = %v, want ErrReleaseSlotLimit", limit, err)
		}
		if held {
			t.Errorf("Hold for %v held the slot, want refused", limit)
		}
		if _, held, err := rig.slots.Show(ctx, slotOutsideRepo); err != nil || held {
			t.Errorf("Show after Hold for %v = held %v, %v; want free", limit, held, err)
		}
	}

	if _, held, err := rig.slots.Hold(ctx, slotOutsideRepo, slotOutsideE1, MaxReleaseSlotLimit); err != nil || !held {
		t.Fatalf("Hold for %v held %v, %v; want held", MaxReleaseSlotLimit, held, err)
	}
	shown, held, err := rig.slots.Show(ctx, slotOutsideRepo)
	if err != nil || !held {
		t.Fatalf("Show after Hold for %v = held %v, %v; want held", MaxReleaseSlotLimit, held, err)
	}
	if shown.Kind != store.ReleaseSlotOutside || shown.Label != slotOutsideE1 {
		t.Errorf("Show = %+v, want outside holder %s", shown, slotOutsideE1)
	}
	if want := slotOutsideStart().Add(MaxReleaseSlotLimit); !shown.Deadline.Equal(want) {
		t.Errorf("Show deadline = %v, want %v", shown.Deadline, want)
	}
}

// TestSlotOutsideHoldTimerFreesAtLimit checks that Run arms its one timer for a hold's deadline, that a fire before
// the deadline keeps the hold and arms for the time left, and that a fire at the deadline frees the slot and calls
// Freed once.
func TestSlotOutsideHoldTimerFreesAtLimit(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	st, _ := openStore(t)
	rig := newSlotOutsideRig(st)
	slotOutsideRun(t, rig)

	// The start pass reads the store before the hold below, so it arms nothing. Wait for it to finish its reads.
	<-rig.passed
	if _, held, err := rig.slots.Hold(ctx, slotOutsideRepo, slotOutsideE1, slotOutsideHold); err != nil || !held {
		t.Fatalf("Hold of %s = held %v, %v; want held", slotOutsideE1, held, err)
	}
	// The hold kicks Run, and that pass reads the store and arms the timer for the deadline.
	<-rig.passed
	timer := rig.timers.awaitArmed(t, slotOutsideHold)

	rig.clock.set(slotOutsideStart().Add(slotOutsideHold - time.Second))
	timer.c <- rig.clock.read()
	<-rig.passed
	if shown, held, err := rig.slots.Show(ctx, slotOutsideRepo); err != nil || !held || shown.Label != slotOutsideE1 {
		t.Errorf("Show before the deadline = %+v, held %v, %v; want %s held", shown, held, err, slotOutsideE1)
	}
	if n := rig.freed.count(); n != 0 {
		t.Errorf("Freed was called %d times before the deadline, want none", n)
	}
	timer = rig.timers.awaitArmed(t, time.Second)

	rig.clock.set(slotOutsideStart().Add(slotOutsideHold))
	timer.c <- rig.clock.read()
	<-rig.passed
	if _, held, err := rig.slots.Show(ctx, slotOutsideRepo); err != nil || held {
		t.Errorf("Show at the deadline = held %v, %v; want free", held, err)
	}
	if n := rig.freed.count(); n != 1 {
		t.Errorf("Freed was called %d times at the deadline, want once", n)
	}
}

// TestSlotOutsideHoldGiveWakesWaiter checks that a give by a label that does not hold the slot frees nothing, and that
// a give by the holder frees the slot, calls Freed once, and wakes the HoldWait that waits behind it.
func TestSlotOutsideHoldGiveWakesWaiter(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	st, _ := openStore(t)
	rig := newSlotOutsideRig(st)

	if _, held, err := rig.slots.Hold(ctx, slotOutsideRepo, slotOutsideE1, slotOutsideHold); err != nil || !held {
		t.Fatalf("Hold of %s = held %v, %v; want held", slotOutsideE1, held, err)
	}
	waited, done := slotOutsideStartWait(ctx, rig, slotOutsideE2)
	if h := <-waited; h.Label != slotOutsideE1 {
		t.Fatalf("waiting received holder %+v, want %s", h, slotOutsideE1)
	}

	given, err := rig.slots.Give(ctx, slotOutsideRepo, slotOutsideE2)
	if err != nil || given {
		t.Fatalf("Give by %s = %v, %v; want false", slotOutsideE2, given, err)
	}
	if n := rig.freed.count(); n != 0 {
		t.Errorf("Freed was called %d times by a give that freed nothing, want none", n)
	}

	given, err = rig.slots.Give(ctx, slotOutsideRepo, slotOutsideE1)
	if err != nil || !given {
		t.Fatalf("Give by %s = %v, %v; want true", slotOutsideE1, given, err)
	}
	if n := rig.freed.count(); n != 1 {
		t.Errorf("Freed was called %d times by the give, want once", n)
	}

	res := <-done
	if res.err != nil {
		t.Fatalf("HoldWait of %s: %v", slotOutsideE2, res.err)
	}
	if res.holder.Label != slotOutsideE2 {
		t.Errorf("HoldWait returned holder %+v, want %s", res.holder, slotOutsideE2)
	}
	if want := slotOutsideStart().Add(slotOutsideWait); !res.holder.Deadline.Equal(want) {
		t.Errorf("HoldWait deadline = %v, want %v", res.holder.Deadline, want)
	}
}

// TestSlotOutsideHoldWaitRetriesOnChanged checks that a HoldWait waiting behind a hold takes the slot once the store
// frees it and Changed is called, although ReleaseSlots did not free it.
func TestSlotOutsideHoldWaitRetriesOnChanged(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	st, _ := openStore(t)
	rig := newSlotOutsideRig(st)

	if _, held, err := st.HoldReleaseSlot(
		ctx,
		slotOutsideRepo,
		slotOutsideE1,
		slotOutsideStart().Add(slotOutsideHold),
	); err != nil ||
		!held {
		t.Fatalf("HoldReleaseSlot of %s = held %v, %v; want held", slotOutsideE1, held, err)
	}
	waited, done := slotOutsideStartWait(ctx, rig, slotOutsideE2)
	if h := <-waited; h.Label != slotOutsideE1 {
		t.Fatalf("waiting received holder %+v, want %s", h, slotOutsideE1)
	}

	if given, err := st.GiveReleaseSlot(ctx, slotOutsideRepo, slotOutsideE1); err != nil || !given {
		t.Fatalf("GiveReleaseSlot of %s = %v, %v; want given", slotOutsideE1, given, err)
	}
	rig.slots.Changed()

	res := <-done
	if res.err != nil {
		t.Fatalf("HoldWait of %s: %v", slotOutsideE2, res.err)
	}
	if res.holder.Kind != store.ReleaseSlotOutside || res.holder.Label != slotOutsideE2 {
		t.Errorf("HoldWait returned holder %+v, want outside holder %s", res.holder, slotOutsideE2)
	}
}

// slotOutsideFlaky is a ReleaseSlotStore whose FreeExpiredReleaseSlots fails its first failures calls. calls counts
// every call, so the test can tell that a retry ran.
type slotOutsideFlaky struct {
	ReleaseSlotStore
	failures int
	calls    int
}

// FreeExpiredReleaseSlots fails while failures are left, and otherwise frees the expired holds in the store.
func (f *slotOutsideFlaky) FreeExpiredReleaseSlots(ctx context.Context, now time.Time) ([]string, error) {
	f.calls++
	if f.calls <= f.failures {
		return nil, errors.New("synthetic store failure")
	}
	return f.ReleaseSlotStore.FreeExpiredReleaseSlots(ctx, now)
}

// TestSlotOutsideHoldRunRetriesAfterStoreError checks that a pass which fails on a store error arms a retry after
// releaseSlotRetry, and that the retry fire runs the pass again, which reads the store.
func TestSlotOutsideHoldRunRetriesAfterStoreError(t *testing.T) {
	t.Parallel()
	st, _ := openStore(t)
	flaky := &slotOutsideFlaky{ReleaseSlotStore: st, failures: 1}
	rig := newSlotOutsideRig(flaky)
	slotOutsideRun(t, rig)

	// The start pass fails on its first store read, so the retry is the only timer it arms.
	timer := rig.timers.awaitArmed(t, releaseSlotRetry)

	timer.c <- rig.clock.read()
	// The retry pass reads the store, which no longer fails, and the read reports on passed.
	<-rig.passed
	if flaky.calls != 2 {
		t.Errorf("FreeExpiredReleaseSlots was called %d times, want 2: the failed pass and its retry", flaky.calls)
	}
}

// TestSlotOutsideHoldWaitEndsWithContext checks that a HoldWait behind a hold returns context.Canceled once its context
// is canceled, and that the hold it waited behind is still held.
func TestSlotOutsideHoldWaitEndsWithContext(t *testing.T) {
	t.Parallel()
	st, _ := openStore(t)
	rig := newSlotOutsideRig(st)

	if _, held, err := rig.slots.Hold(
		t.Context(),
		slotOutsideRepo,
		slotOutsideE1,
		slotOutsideHold,
	); err != nil ||
		!held {
		t.Fatalf("Hold of %s = held %v, %v; want held", slotOutsideE1, held, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	waited, done := slotOutsideStartWait(ctx, rig, slotOutsideE2)
	if h := <-waited; h.Label != slotOutsideE1 {
		t.Fatalf("waiting received holder %+v, want %s", h, slotOutsideE1)
	}

	cancel()
	if res := <-done; !errors.Is(res.err, context.Canceled) {
		t.Errorf("HoldWait after cancel: err = %v, want context.Canceled", res.err)
	}
	if shown, held, err := rig.slots.Show(
		t.Context(),
		slotOutsideRepo,
	); err != nil || !held ||
		shown.Label != slotOutsideE1 {
		t.Errorf("Show after cancel = %+v, held %v, %v; want %s held", shown, held, err, slotOutsideE1)
	}
}
