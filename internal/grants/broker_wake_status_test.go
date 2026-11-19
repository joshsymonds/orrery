package grants

import (
	"log/slog"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// retryTimers stands in for time.AfterFunc as a broker's AfterFunc. Each arm is recorded with its delay and function, and
// a test fires the armed retry itself, so no test waits on the store retry's clock.
type retryTimers struct {
	mu   sync.Mutex
	arms []*retryArm
}

// retryArm is one armed retry: its delay, its function, and whether it still waits to fire.
type retryArm struct {
	d       time.Duration
	f       func()
	pending bool
}

// arm records a retry of delay d that runs f, and returns the stop that ends it.
func (ts *retryTimers) arm(d time.Duration, f func()) func() bool {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	a := &retryArm{d: d, f: f, pending: true}
	ts.arms = append(ts.arms, a)
	return func() bool {
		ts.mu.Lock()
		defer ts.mu.Unlock()
		wasPending := a.pending
		a.pending = false
		return wasPending
	}
}

// fire runs the one retry that is armed and still pending, which must have the store retry's delay. It fails the test
// unless exactly one such retry is pending.
func (ts *retryTimers) fire(t *testing.T) {
	t.Helper()
	ts.mu.Lock()
	var pending []*retryArm
	for _, a := range ts.arms {
		if a.pending {
			pending = append(pending, a)
		}
	}
	if len(pending) != 1 || pending[0].d != storeRetry {
		ts.mu.Unlock()
		t.Fatalf("%d retries pending, want one armed after %v", len(pending), storeRetry)
	}
	a := pending[0]
	a.pending = false
	ts.mu.Unlock()
	a.f()
}

// wakeBroker returns a broker on s whose commands run with env, whose clients are alive as alive reports, and whose
// store retries are armed on timers. It is closed when the test ends.
func wakeBroker(t *testing.T, s *store.Store, env []string, alive func(int, int64) bool, timers *retryTimers) *Broker {
	t.Helper()
	b := NewBroker(Config{
		Host: testHost, Store: s, Environ: commandEnv(env...), Now: fixedNow,
		Logger: slog.New(slog.DiscardHandler), Alive: alive, AfterFunc: timers.arm,
	})
	t.Cleanup(b.Close)
	return b
}

// sortedNames returns the names of members in order.
func sortedNames(members []Member) []string {
	names := make([]string, 0, len(members))
	for _, m := range members {
		names = append(names, m.Name)
	}
	slices.Sort(names)
	return names
}

// TestBrokerWakeRestoreRunsNoStatus checks that a restart keeps a live client's grant without running a status: with
// no request waiting, Restore starts no member command.
func TestBrokerWakeRestoreRunsNoStatus(t *testing.T) {
	f := newFakes(t)
	s := openStore(t)
	p := statusPool(t, 2, "m1", "m2")
	timers := &retryTimers{}
	before := wakeBroker(t, s, f.env(), nil, timers)
	held, call := grantTo(t, before, p, "a", livePID)
	awaitStatuses(t, before, f, len(p.Members))
	closeWithBlockedCalls(t, before, call)

	clients := &pidSet{live: map[int]bool{livePID: true}}
	after := wakeBroker(t, s, f.env(), clients.alive, timers)
	if err := after.Restore(t.Context()); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	awaitIdle(t, after)
	if runs := lines(t, f.runs); len(runs) != len(p.Members) {
		t.Fatalf("status runs = %v after Restore, want only the %d from before the restart", runs, len(p.Members))
	}
	if _, grant := memberOf(t, after, "fleet", held.Members[0].Name); grant != held.ID {
		t.Fatalf("%s held by %q after Restore, want the live grant %s", held.Members[0].Name, grant, held.ID)
	}
}

// TestBrokerWakeRequestOnARestoredPoolRunsItsFreeMembersStatus checks that a request queued on a restored pool runs the
// status of each free member, and of no granted member, and that none of them ran before the request queued.
func TestBrokerWakeRequestOnARestoredPoolRunsItsFreeMembersStatus(t *testing.T) {
	f := newFakes(t)
	s := openStore(t)
	p := statusPool(t, 3, "m1", "m2", "m3")
	timers := &retryTimers{}
	before := wakeBroker(t, s, f.env(), nil, timers)
	held, call := grantTo(t, before, p, "a", livePID)
	awaitStatuses(t, before, f, len(p.Members))
	closeWithBlockedCalls(t, before, call)
	var free []string
	for _, name := range p.Members {
		if name != held.Members[0].Name {
			free = append(free, name)
		}
	}
	slices.Sort(free)

	clients := &pidSet{live: map[int]bool{livePID: true}}
	after := wakeBroker(t, s, f.env(), clients.alive, timers)
	if err := after.Restore(t.Context()); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	awaitIdle(t, after)
	if runs := lines(t, f.runs); len(runs) != len(p.Members) {
		t.Fatalf("status runs = %v after Restore, want none beyond the %d from before the restart",
			runs, len(p.Members))
	}

	waiter := start(t.Context(), after, request("b", p, len(free)))
	if e := waiter.next(t); e.Position != 1 {
		t.Fatalf("waiter event = %+v, want position 1", e)
	}
	granted := waiter.next(t).Grant
	if granted == nil {
		t.Fatal("waiter got no grant")
	}
	if got := sortedNames(granted.Members); !slices.Equal(got, free) {
		t.Fatalf("waiter granted %v, want the free members %v", got, free)
	}
	runs := lines(t, f.runs)
	if len(runs) != len(p.Members)+len(free) {
		t.Fatalf("status runs = %v, want the %d from before the restart and one of each free member",
			runs, len(p.Members))
	}
	if got := slices.Sorted(slices.Values(runs[len(p.Members):])); !slices.Equal(got, free) {
		t.Fatalf("status runs after the request queued = %v, want exactly the free members %v", got, free)
	}
}

// TestBrokerWakeRetriesARefusedGrantEndWhenItsTimerFires checks that a restart whose store refuses the end of a dead
// client's grant arms one retry, and that the retry, once the store takes writes, removes the row and frees the member,
// with no status run.
func TestBrokerWakeRetriesARefusedGrantEndWhenItsTimerFires(t *testing.T) {
	f := newFakes(t)
	s, path := openBusyStore(t)
	p := statusPool(t, 1, "m1")
	timers := &retryTimers{}
	before := wakeBroker(t, s, f.env(), nil, timers)
	dead, call := grantTo(t, before, p, "a", deadPID)
	awaitStatuses(t, before, f, 1)
	closeWithBlockedCalls(t, before, call)

	nobodyAlive := &pidSet{live: map[int]bool{}}
	after := wakeBroker(t, s, f.env(), nobodyAlive.alive, timers)
	lock := lockStore(t, path)
	if err := after.Restore(t.Context()); err != nil {
		t.Fatalf("Restore while the store refuses the end: %v", err)
	}
	if _, grant := memberOf(t, after, "fleet", "m1"); grant != dead.ID {
		t.Fatalf("m1 held by %q after Restore, want the dead grant %s until its row is removed", grant, dead.ID)
	}
	lock.release(t)
	if rows := storedGrants(t, s); len(rows) != 1 || rows[0].ID != dead.ID {
		t.Fatalf("stored grants = %+v before the retry fires, want only the dead grant %s", rows, dead.ID)
	}

	timers.fire(t)
	if rows := storedGrants(t, s); len(rows) != 0 {
		t.Fatalf("stored grants after the retry = %+v, want none", rows)
	}
	if n := len(after.Status().Pools); n != 0 {
		t.Fatalf("%d pools in use after the retry, want none: the dead grant's member is freed", n)
	}
	if runs := lines(t, f.runs); len(runs) != 1 {
		t.Fatalf("status runs = %v after the retry, want only the one from before the restart", runs)
	}
}

// TestBrokerWakeRetriesABusyGrantWriteWhenItsTimerFires checks that a request whose grant write the store refuses busy
// stays queued and arms a retry, and that the retry, once the store takes writes, grants it with no member command
// running and no status run.
func TestBrokerWakeRetriesABusyGrantWriteWhenItsTimerFires(t *testing.T) {
	f := newFakes(t)
	s, path := openBusyStore(t)
	p := statusPool(t, 1, "m1")
	timers := &retryTimers{}
	b := wakeBroker(t, s, f.env(), nil, timers)
	lock := lockStore(t, path)
	c := start(t.Context(), b, request("a", p, 1))
	if e := c.next(t); e.Position != 1 {
		t.Fatalf("first event = %+v, want position 1", e)
	}
	// The status runs, then the admit that its settle starts, and the settle after the admit refuses the grant. The
	// position is sent after the status has started, so awaitIdle waits for that whole chain to end.
	awaitIdle(t, b)
	if got := queuedProjects(b); !slices.Equal(got, []string{"a"}) {
		t.Fatalf("queued = %v while the store refuses the grant, want a waiting", got)
	}
	if rows := storedGrants(t, s); len(rows) != 0 {
		t.Fatalf("stored grants while the store refuses writes = %+v, want none", rows)
	}
	if state, grant := memberOf(t, b, "fleet", "m1"); state != Admitted || grant != "" {
		t.Fatalf("m1 is %v holding %q, want admitted and free for the waiting request", state, grant)
	}

	lock.release(t)
	timers.fire(t)
	granted := c.next(t).Grant
	if granted == nil || !slices.Equal(granted.Members, []Member{{Pool: "fleet", Name: "m1"}}) {
		t.Fatalf("request got %+v, want m1 once the store takes the grant", granted)
	}
	if rows := storedGrants(t, s); len(rows) != 1 || rows[0].ID != granted.ID {
		t.Fatalf("stored grants = %+v, want the grant %s", rows, granted.ID)
	}
	if runs := lines(t, f.runs); len(runs) != 1 {
		t.Fatalf("status runs = %v, want the one before the grant", runs)
	}
}
