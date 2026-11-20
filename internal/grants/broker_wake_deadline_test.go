package grants

import (
	"context"
	"errors"
	"slices"
	"testing"
)

// commandsInFlight returns how many member commands the broker has started and not yet ended, of any pool.
func commandsInFlight(b *Broker) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.cancels)
}

// pendingDeadlines returns how many status deadlines of the fake timers still wait to fire.
func (ts *retryTimers) pendingDeadlines() int {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	n := 0
	for _, a := range ts.arms {
		if a.pending && a.d == statusDeadline {
			n++
		}
	}
	return n
}

// deadlineArms returns the function of each status deadline of the fake timers, pending or stopped, in the order they
// were armed.
func (ts *retryTimers) deadlineArms() []func() {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	var fns []func()
	for _, a := range ts.arms {
		if a.d == statusDeadline {
			fns = append(fns, a.f)
		}
	}
	return fns
}

// TestBrokerWakeWaitingRequestRerunsAMembersStatusWhenItsDeadlineFires checks that a request waiting on a member that
// its status left unavailable arms the member's status deadline, that no second status runs before the deadline fires,
// and that the fired deadline runs the member's status once and arms the deadline again, since the request still waits.
func TestBrokerWakeWaitingRequestRerunsAMembersStatusWhenItsDeadlineFires(t *testing.T) {
	f := newFakes(t)
	s := openStore(t)
	p := poolOf(t, "fleet", 1, []string{"m1"}, statusBody, failing, healthy)
	timers := &retryTimers{}
	b := wakeBroker(t, s, f.env(), nil, timers)
	f.setMode(t, "m1", "down")
	c := start(t.Context(), b, request("a", p, 1))
	if e := c.next(t); e.Position != 1 {
		t.Fatalf("first event = %+v, want position 1", e)
	}
	// The status exits 1, so m1 is asleep and its wake, which fails, leaves it unavailable. The request waits on it.
	awaitStatuses(t, b, f, 1)
	wantFree(t, b, "m1", Unavailable)
	if got := queuedProjects(b); !slices.Equal(got, []string{"a"}) {
		t.Fatalf("queued projects = %v, want [a] still waiting", got)
	}
	if n := timers.pendingDeadlines(); n != 1 {
		t.Fatalf("%d status deadlines pending while the request waits, want one", n)
	}

	timers.fireAfter(t, statusDeadline)
	awaitStatuses(t, b, f, 2)
	if runs := lines(t, f.runs); !slices.Equal(runs, []string{"m1", "m1"}) {
		t.Fatalf("status runs = %v, want one more run of m1 when its deadline fires", runs)
	}
	if n := timers.pendingDeadlines(); n != 1 {
		t.Fatalf("%d status deadlines pending after the fired status ends, want one armed again", n)
	}
}

// TestBrokerWakeLeftRequestStopsItsPoolsStatusDeadlines checks that when the only request waiting on a pool leaves, the
// status deadline of the pool's member is stopped, so firing it anyway runs no status and arms no new deadline.
func TestBrokerWakeLeftRequestStopsItsPoolsStatusDeadlines(t *testing.T) {
	f := newFakes(t)
	s := openStore(t)
	p := poolOf(t, "fleet", 1, []string{"m1"}, statusBody, failing, healthy)
	timers := &retryTimers{}
	b := wakeBroker(t, s, f.env(), nil, timers)
	f.setMode(t, "m1", "down")
	ctx, cancel := context.WithCancel(t.Context())
	c := start(ctx, b, request("a", p, 1))
	if e := c.next(t); e.Position != 1 {
		t.Fatalf("first event = %+v, want position 1", e)
	}
	awaitStatuses(t, b, f, 1)
	if n := timers.pendingDeadlines(); n != 1 {
		t.Fatalf("%d status deadlines pending while the request waits, want one", n)
	}

	cancel()
	if err := c.finish(t); !errors.Is(err, context.Canceled) {
		t.Fatalf("Acquire = %v, want context.Canceled", err)
	}
	if n := timers.pendingDeadlines(); n != 0 {
		t.Fatalf("%d status deadlines pending after the only waiting request left, want none", n)
	}
	deadlines := timers.deadlineArms()
	if len(deadlines) != 1 {
		t.Fatalf("%d status deadlines armed, want the one the request armed", len(deadlines))
	}
	deadlines[0]()
	if n := commandsInFlight(b); n != 0 {
		t.Fatalf("%d member commands run after the stopped deadline fired, want none", n)
	}
	if n := timers.pendingDeadlines(); n != 0 {
		t.Fatalf("%d status deadlines pending after the stopped one fired, want none", n)
	}
	if n := len(timers.deadlineArms()); n != len(deadlines) {
		t.Fatalf("%d status deadlines armed after the stopped one fired, want %d: no new one", n, len(deadlines))
	}
}

// deadlineArmed reports whether member of pool has a status deadline armed.
func deadlineArmed(t *testing.T, b *Broker, pool, member string) bool {
	t.Helper()
	b.mu.Lock()
	defer b.mu.Unlock()
	use, ok := b.pools[pool]
	if !ok {
		t.Fatalf("pool %s is not in use", pool)
	}
	mr, ok := use.members[member]
	if !ok {
		t.Fatalf("pool %s has no member %s", pool, member)
	}
	return mr.deadline != nil
}

// TestBrokerWakeGrantedMemberHasNoStatusDeadline checks that a free member whose deadline is armed loses it when the
// store takes its grant, while a second request still waits on the pool: the granted member has no deadline and runs no
// status after its grant, and the member left free keeps its deadline.
func TestBrokerWakeGrantedMemberHasNoStatusDeadline(t *testing.T) {
	f := newFakes(t)
	s, path := openBusyStore(t)
	p := statusPool(t, 1, "m1", "m2")
	timers := &retryTimers{}
	b := wakeBroker(t, s, f.env(), nil, timers)
	lock := lockStore(t, path)
	first := start(t.Context(), b, request("a", p, 1))
	if e := first.next(t); e.Position != 1 {
		t.Fatalf("first event = %+v, want position 1", e)
	}
	// Both members run their status. The one admitted first has its grant refused by the store, and stays free for the
	// waiting request, so both members are free and their deadlines are armed. The second request is of the same project,
	// so the project's cap keeps it from reserving the other member, and it waits on the pool once the first is granted.
	awaitIdle(t, b)
	second := start(t.Context(), b, request("a", p, 1))
	if e := second.next(t); e.Position != 2 {
		t.Fatalf("second event = %+v, want position 2", e)
	}
	for _, name := range p.Members {
		if !deadlineArmed(t, b, "fleet", name) {
			t.Fatalf("%s has no status deadline while the store refuses the grant, want one", name)
		}
	}

	lock.release(t)
	timers.fire(t)
	granted := first.next(t).Grant
	if granted == nil || len(granted.Members) != 1 {
		t.Fatalf("first request got %+v, want one member once the store takes writes", granted)
	}
	name := granted.Members[0].Name
	if deadlineArmed(t, b, "fleet", name) {
		t.Fatalf("%s is granted but still has a status deadline, want none", name)
	}
	for _, other := range p.Members {
		if other != name && !deadlineArmed(t, b, "fleet", other) {
			t.Fatalf("%s is free and still waited on but has no status deadline, want one", other)
		}
	}
	if runs := slices.Sorted(slices.Values(lines(t, f.runs))); !slices.Equal(runs, p.Members) {
		t.Fatalf("status runs = %v, want one status of each member before the grant", runs)
	}
}
