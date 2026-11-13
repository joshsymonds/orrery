package holder

import (
	"sync"
	"testing"
	"time"
)

// waitArmedTimeout bounds how long waitArmed waits for waiters, and how long SystemClock may take to fire.
const waitArmedTimeout = 5 * time.Second

// fakeWaiter is one timer armed on a fakeClock. It fires once the clock reaches deadline.
type fakeWaiter struct {
	deadline time.Time
	fire     chan time.Time
}

// fakeClock is a Clock whose time moves only through Advance. It is safe for concurrent use.
type fakeClock struct {
	mu      sync.Mutex
	now     time.Time
	waiters []fakeWaiter
	// armed is closed and replaced each time a waiter registers, so waitArmed can block without polling.
	armed chan struct{}
}

func newFakeClock() *fakeClock {
	return &fakeClock{
		now:   time.Unix(0, 0).UTC(),
		armed: make(chan struct{}),
	}
}

// After arms a waiter that fires once d has passed on the fake clock. A waiter whose deadline has
// already passed fires at once.
func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	fire := make(chan time.Time, 1)
	c.waiters = append(c.waiters, fakeWaiter{deadline: c.now.Add(d), fire: fire})
	close(c.armed)
	c.armed = make(chan struct{})
	c.fireDueLocked()
	return fire
}

// Advance moves the fake clock forward by d and fires every waiter that has come due.
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.now = c.now.Add(d)
	c.fireDueLocked()
}

// fireDueLocked fires and removes every waiter whose deadline is not after now. Each channel has room
// for its one value, so a send never blocks. The caller holds c.mu.
func (c *fakeClock) fireDueLocked() {
	pending := c.waiters[:0]
	for _, waiter := range c.waiters {
		if c.now.Before(waiter.deadline) {
			pending = append(pending, waiter)
			continue
		}
		waiter.fire <- c.now
	}
	c.waiters = pending
}

// waitArmed blocks until at least n unfired waiters are armed, failing the test after waitArmedTimeout.
func (c *fakeClock) waitArmed(tb testing.TB, n int) {
	tb.Helper()

	timeout := time.After(waitArmedTimeout)
	for {
		c.mu.Lock()
		count := len(c.waiters)
		armed := c.armed
		c.mu.Unlock()
		if count >= n {
			return
		}
		select {
		case <-armed:
		case <-timeout:
			tb.Fatalf("fake clock has %d waiters armed after %v, want %d", count, waitArmedTimeout, n)
		}
	}
}

func requireFired(tb testing.TB, ch <-chan time.Time) {
	tb.Helper()
	select {
	case <-ch:
	default:
		tb.Fatal("timer did not fire")
	}
}

func requireSilent(tb testing.TB, ch <-chan time.Time) {
	tb.Helper()
	select {
	case <-ch:
		tb.Fatal("timer fired before its deadline or more than once")
	default:
	}
}

func TestFakeClockFiresOnlyDueWaitersAndEachOnce(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	early := clock.After(10 * time.Second)
	late := clock.After(20 * time.Second)

	clock.Advance(5 * time.Second)
	requireSilent(t, early)
	requireSilent(t, late)

	clock.Advance(5 * time.Second)
	requireFired(t, early)
	requireSilent(t, late)

	clock.Advance(10 * time.Second)
	requireFired(t, late)

	clock.Advance(time.Hour)
	requireSilent(t, early)
	requireSilent(t, late)
}

func TestFakeClockZeroDelayFiresAtOnce(t *testing.T) {
	t.Parallel()

	requireFired(t, newFakeClock().After(0))
}

func TestFakeClockWaitArmedBlocksUntilWaitersArm(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	go func() {
		clock.After(time.Second)
		clock.After(time.Second)
	}()
	clock.waitArmed(t, 2)

	clock.mu.Lock()
	armed := len(clock.waiters)
	clock.mu.Unlock()
	if armed < 2 {
		t.Fatalf("waitArmed(2) returned with %d waiters armed", armed)
	}
	clock.Advance(time.Second)
}

func TestSystemClockFiresAfterItsDelay(t *testing.T) {
	t.Parallel()

	select {
	case <-SystemClock{}.After(time.Millisecond):
	case <-time.After(waitArmedTimeout):
		t.Fatal("SystemClock{}.After(1ms) did not fire within 5s")
	}
}
