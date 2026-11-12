package grants

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/pools"
	"github.com/joshsymonds/orrery/internal/store"
)

// stepClock is the clock of a status test. The broker reads it under its lock while the test advances it between calls,
// so it is atomic.
type stepClock struct {
	unix atomic.Int64
}

// newStepClock returns a clock that reads fixedNow.
func newStepClock() *stepClock {
	c := &stepClock{}
	c.unix.Store(fixedNow().Unix())
	return c
}

// now returns the time the clock reads.
func (c *stepClock) now() time.Time {
	return time.Unix(c.unix.Load(), 0).UTC()
}

// advance moves the clock forward by seconds.
func (c *stepClock) advance(seconds int64) {
	c.unix.Add(seconds)
}

// at returns the time seconds after fixedNow, which is what a clock advanced by seconds reads.
func at(seconds int64) time.Time {
	return fixedNow().Add(time.Duration(seconds) * time.Second)
}

// statusBroker returns a broker on s that reads clock, and whose clients are alive as alive reports, or as processAlive
// reports when alive is nil. It is closed when the test ends, before the store.
func statusBroker(t *testing.T, s *store.Store, clock *stepClock, alive func(int, int64) bool) *Broker {
	t.Helper()
	b := NewBroker(Config{
		Host: testHost, Store: s, Environ: commandEnv(), Now: clock.now,
		Logger: slog.New(slog.DiscardHandler), Alive: alive,
	})
	t.Cleanup(b.Close)
	return b
}

// commanded returns req with command as the argv its grant stores.
func commanded(req Request, command ...string) Request {
	req.Command = command
	return req
}

// poolNamed returns the pool of status named name, and fails the test when status has none.
func poolNamed(t *testing.T, status Status, name string) PoolStatus {
	t.Helper()
	i := slices.IndexFunc(status.Pools, func(p PoolStatus) bool { return p.Name == name })
	if i < 0 {
		t.Fatalf("status has no pool %s: %+v", name, status.Pools)
	}
	return status.Pools[i]
}

// memberNamed returns the member of pool named name, and fails the test when pool has none.
func memberNamed(t *testing.T, pool PoolStatus, name string) MemberStatus {
	t.Helper()
	i := slices.IndexFunc(pool.Members, func(m MemberStatus) bool { return m.Name == name })
	if i < 0 {
		t.Fatalf("pool %s has no member %s: %+v", pool.Name, name, pool.Members)
	}
	return pool.Members[i]
}

// awaitAwake polls until member of pool p is awake, which is when its first status has ended.
func awaitAwake(t *testing.T, b *Broker, member string) {
	t.Helper()
	await(t, "member "+member+" of pool p to be awake", func() bool {
		state, _ := memberOf(t, b, "p", member)
		return state == Awake
	})
}

// wantHolder fails the test unless got is the holder want describes, with times compared as instants.
func wantHolder(t *testing.T, got *Holder, want Holder) {
	t.Helper()
	if got == nil || got.GrantID != want.GrantID || got.Project != want.Project || got.ClientPID != want.ClientPID ||
		!slices.Equal(got.Command, want.Command) || !got.Since.Equal(want.Since) || got.Stopping != want.Stopping {
		t.Fatalf("holder = %+v, want %+v", got, want)
	}
}

// wantWaiter fails the test unless got is the waiter want describes, with times compared as instants.
func wantWaiter(t *testing.T, got, want WaiterStatus) {
	t.Helper()
	if got.Position != want.Position || got.Project != want.Project || got.Count != want.Count ||
		!slices.Equal(got.Command, want.Command) || !got.Since.Equal(want.Since) {
		t.Fatalf("waiter = %+v, want %+v", got, want)
	}
}

// wantHolds fails the test unless status lists exactly the holds want, in order, with times compared as instants.
func wantHolds(t *testing.T, status Status, want ...HoldStatus) {
	t.Helper()
	if len(status.Holds) != len(want) {
		t.Fatalf("holds = %+v, want %+v", status.Holds, want)
	}
	for i, h := range status.Holds {
		if h.Member != want[i].Member || !h.Since.Equal(want[i].Since) {
			t.Fatalf("holds = %+v, want %+v", status.Holds, want)
		}
	}
}

// TestStatusReportsGrantedMemberAndFreeMember checks the two members of a pool with one grant. The granted member names
// its holder, with the grant id, project, client pid, command and the time the grant was stored, and the free member has
// no holder.
func TestStatusReportsGrantedMemberAndFreeMember(t *testing.T) {
	b := statusBroker(t, openStore(t), newStepClock(), nil)
	p := awakePool(t, "p", 2, "m1", "m2")
	holder := start(t.Context(), b, commanded(request("a", p, 1), "sleep", "60"))
	holder.next(t)
	grant := holder.next(t).Grant
	if grant == nil {
		t.Fatal("holder got no grant")
	}
	taken := grant.Members[0].Name
	idle := "m2"
	if taken == "m2" {
		idle = "m1"
	}
	awaitAwake(t, b, idle)

	status := b.Status()
	ps := poolNamed(t, status, "p")
	if ps.Cap != 2 || len(ps.Members) != 2 || ps.Members[0].Name != "m1" || ps.Members[1].Name != "m2" {
		t.Fatalf("pool p = %+v, want cap 2 with members m1 and m2 in declared order", ps)
	}
	granted := memberNamed(t, ps, taken)
	if granted.State != Admitted || !granted.HeldSince.IsZero() {
		t.Fatalf("granted member = %+v, want admitted and not held", granted)
	}
	wantHolder(t, granted.Holder, Holder{
		GrantID: grant.ID, Project: "a", ClientPID: 4242, Command: []string{"sleep", "60"}, Since: at(0),
	})
	free := memberNamed(t, ps, idle)
	if free.State != Awake || free.Holder != nil || !free.HeldSince.IsZero() {
		t.Fatalf("free member = %+v, want awake with no holder and no held since", free)
	}
	if len(ps.Waiters) != 0 || len(status.Holds) != 0 {
		t.Fatalf("pool p = %+v, holds = %+v, want no waiters and no holds", ps, status.Holds)
	}
}

// TestStatusReportsStoppingHolderAndHeldMemberState checks a hold on a granted member. The holder is reported as
// stopping, the member keeps its own awake state rather than the unavailable one Plan sees, and the hold is listed with
// the time it was placed.
func TestStatusReportsStoppingHolderAndHeldMemberState(t *testing.T) {
	clock := newStepClock()
	b := statusBroker(t, openStore(t), clock, nil)
	p := awakePool(t, "p", 2, "m1", "m2")
	holder := start(t.Context(), b, commanded(request("a", p, 1), "sleep", "60"))
	holder.next(t)
	grant := holder.next(t).Grant
	if grant == nil {
		t.Fatal("holder got no grant")
	}
	held := grant.Members[0].Name
	idle := "m2"
	if held == "m2" {
		idle = "m1"
	}
	awaitAwake(t, b, idle)

	clock.advance(30)
	if err := b.Hold(t.Context(), held); err != nil {
		t.Fatalf("Hold: %v", err)
	}

	status := b.Status()
	ps := poolNamed(t, status, "p")
	heldMember := memberNamed(t, ps, held)
	if heldMember.State != Admitted {
		t.Fatalf("held member is %v, want admitted: its own state, not the unavailable one Plan sees", heldMember.State)
	}
	if !heldMember.HeldSince.Equal(at(30)) {
		t.Fatalf("held member held since %v, want the hold's time %v", heldMember.HeldSince, at(30))
	}
	wantHolder(t, heldMember.Holder, Holder{
		GrantID: grant.ID, Project: "a", ClientPID: 4242, Command: []string{"sleep", "60"},
		Since: at(0), Stopping: true,
	})
	if idleMember := memberNamed(t, ps, idle); idleMember.Holder != nil {
		t.Fatalf("free member %s has holder %+v, want none", idle, idleMember.Holder)
	}
	wantHolds(t, status, HoldStatus{Member: held, Since: at(30)})
}

// TestStatusReportsWaitersOfAPoolInQueueOrder checks two requests waiting for one pool. Each is reported with its
// position, its count of members from the pool, its command and the time it queued.
func TestStatusReportsWaitersOfAPoolInQueueOrder(t *testing.T) {
	clock := newStepClock()
	b := statusBroker(t, openStore(t), clock, nil)
	p := awakePool(t, "p", 2, "m1", "m2")
	holder := start(t.Context(), b, commanded(request("a", p, 1), "hold-it"))
	holder.next(t)
	grant := holder.next(t).Grant
	if grant == nil {
		t.Fatal("holder got no grant")
	}
	idle := "m2"
	if grant.Members[0].Name == "m2" {
		idle = "m1"
	}
	awaitAwake(t, b, idle)

	clock.advance(10)
	first := start(t.Context(), b, commanded(request("b", p, 2), "b-run"))
	if e := first.next(t); e.Position != 1 {
		t.Fatalf("first waiter event = %+v, want position 1", e)
	}
	clock.advance(5)
	second := start(t.Context(), b, commanded(request("c", p, 1), "c-run"))
	if e := second.next(t); e.Position != 2 {
		t.Fatalf("second waiter event = %+v, want position 2", e)
	}

	ps := poolNamed(t, b.Status(), "p")
	if len(ps.Waiters) != 2 {
		t.Fatalf("waiters = %+v, want two", ps.Waiters)
	}
	wantWaiter(t, ps.Waiters[0], WaiterStatus{
		Position: 1, Project: "b", Command: []string{"b-run"}, Count: 2, Since: at(10),
	})
	wantWaiter(t, ps.Waiters[1], WaiterStatus{
		Position: 2, Project: "c", Command: []string{"c-run"}, Count: 1, Since: at(15),
	})
}

// TestStatusCountsPositionsOverTheWholeQueue checks a request that waits for two pools. It is queued behind a request
// for the first pool alone, so its position in pool p's waiters is 2, not 1, and pool q lists both requests in order.
func TestStatusCountsPositionsOverTheWholeQueue(t *testing.T) {
	clock := newStepClock()
	b := statusBroker(t, openStore(t), clock, nil)
	q := awakePool(t, "q", 1, "q1")
	p := awakePool(t, "p", 2, "m1", "m2")
	holder := start(t.Context(), b, request("h", q, 1))
	holder.next(t)
	if holder.next(t).Grant == nil {
		t.Fatal("holder of q got no grant")
	}

	clock.advance(10)
	behindQ := start(t.Context(), b, commanded(request("w", q, 1), "wq-run"))
	if e := behindQ.next(t); e.Position != 1 {
		t.Fatalf("waiter of q event = %+v, want position 1", e)
	}
	clock.advance(5)
	both := commanded(Request{
		Project: "x", Pools: []pools.Pool{q, p}, Needs: []Need{{Pool: "q", Count: 1}, {Pool: "p", Count: 1}},
		ClientPID: 4242,
	}, "x-run")
	waiting := start(t.Context(), b, both)
	if e := waiting.next(t); e.Position != 2 {
		t.Fatalf("waiter of p and q event = %+v, want position 2", e)
	}

	status := b.Status()
	if len(status.Pools) != 2 || status.Pools[0].Name != "p" || status.Pools[1].Name != "q" {
		t.Fatalf("pools = %+v, want p and q in name order", status.Pools)
	}
	ps := poolNamed(t, status, "p")
	if len(ps.Waiters) != 1 {
		t.Fatalf("waiters of p = %+v, want the request that needs p", ps.Waiters)
	}
	wantWaiter(t, ps.Waiters[0], WaiterStatus{
		Position: 2, Project: "x", Command: []string{"x-run"}, Count: 1, Since: at(15),
	})
	qs := poolNamed(t, status, "q")
	if len(qs.Waiters) != 2 {
		t.Fatalf("waiters of q = %+v, want two", qs.Waiters)
	}
	wantWaiter(t, qs.Waiters[0], WaiterStatus{
		Position: 1, Project: "w", Command: []string{"wq-run"}, Count: 1, Since: at(10),
	})
	wantWaiter(t, qs.Waiters[1], WaiterStatus{
		Position: 2, Project: "x", Command: []string{"x-run"}, Count: 1, Since: at(15),
	})
}

// TestStatusReportsOwnerHoldsSortedByMember checks the holds. One is on a member of no pool in use, and one is on a free
// member of a pool in use that the next request therefore does not get. Holds are listed sorted by member, and the free
// held member reports its own state and no holder.
func TestStatusReportsOwnerHoldsSortedByMember(t *testing.T) {
	clock := newStepClock()
	b := statusBroker(t, openStore(t), clock, nil)
	if err := b.Hold(t.Context(), "z9"); err != nil {
		t.Fatalf("Hold z9: %v", err)
	}
	clock.advance(5)
	if err := b.Hold(t.Context(), "m1"); err != nil {
		t.Fatalf("Hold m1: %v", err)
	}

	p := awakePool(t, "p", 2, "m1", "m2")
	holder := start(t.Context(), b, commanded(request("a", p, 1), "a-run"))
	holder.next(t)
	grant := holder.next(t).Grant
	if grant == nil {
		t.Fatal("holder got no grant")
	}
	if grant.Members[0].Name != "m2" {
		t.Fatalf("holder got %+v, want m2, since m1 is held", grant.Members)
	}
	awaitAwake(t, b, "m1")

	status := b.Status()
	ps := poolNamed(t, status, "p")
	held := memberNamed(t, ps, "m1")
	if held.State != Awake || held.Holder != nil || !held.HeldSince.Equal(at(5)) {
		t.Fatalf("owner-held member = %+v, want awake with no holder, held since %v", held, at(5))
	}
	wantHolds(t, status, HoldStatus{Member: "m1", Since: at(5)}, HoldStatus{Member: "z9", Since: at(0)})
}

// TestStatusReportsRestoredGrantWithItsOriginalSince checks a grant a restart restores. It is reported with the holder it
// had: its grant id, project, client pid and command, and the time it was stored, not the restored broker's clock.
func TestStatusReportsRestoredGrantWithItsOriginalSince(t *testing.T) {
	s := openStore(t)
	pids := &pidSet{live: map[int]bool{4242: true}}
	first := statusBroker(t, s, newStepClock(), pids.alive)
	p := awakePool(t, "p", 1, "m1")
	holder := start(t.Context(), first, commanded(request("a", p, 1), "kept", "run"))
	holder.next(t)
	grant := holder.next(t).Grant
	if grant == nil {
		t.Fatal("holder got no grant")
	}
	closeWithBlockedCalls(t, first, holder)

	clock := newStepClock()
	clock.advance(100)
	second := statusBroker(t, s, clock, pids.alive)
	if err := second.Restore(t.Context()); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	awaitAwake(t, second, "m1")

	m := memberNamed(t, poolNamed(t, second.Status(), "p"), "m1")
	if !m.HeldSince.IsZero() {
		t.Fatalf("restored member held since %v, want not held", m.HeldSince)
	}
	wantHolder(t, m.Holder, Holder{
		GrantID: grant.ID, Project: "a", ClientPID: 4242, Command: []string{"kept", "run"}, Since: at(0),
	})
}

// TestMemberStateStringNamesEveryState checks the name of each member state as the API reports it, and that a value
// outside the states names itself by number.
func TestMemberStateStringNamesEveryState(t *testing.T) {
	cases := []struct {
		state MemberState
		want  string
	}{
		{Unknown, "unknown"},
		{Asleep, "asleep"},
		{Awake, "awake"},
		{Waking, "waking"},
		{Admitting, "admitting"},
		{Admitted, "admitted"},
		{Unavailable, "unavailable"},
		{MemberState(99), "MemberState(99)"},
	}
	for _, tc := range cases {
		if got := tc.state.String(); got != tc.want {
			t.Errorf("state %d String() = %q, want %q", int(tc.state), got, tc.want)
		}
	}
}

// TestStatusReportsRequeuedRestoredRequestSinceItsGrant checks a grant a restart restores while its member is held. Its
// reclaim gets the stop, its stream ends, and its request waits again. A restart keeps no queue time, so the waiter is
// reported since its stored grant time rather than a zero time.
func TestStatusReportsRequeuedRestoredRequestSinceItsGrant(t *testing.T) {
	s := openStore(t)
	pids := &pidSet{live: map[int]bool{4242: true}}
	first := statusBroker(t, s, newStepClock(), pids.alive)
	p := awakePool(t, "p", 1, "m1")
	holder := start(t.Context(), first, commanded(request("a", p, 1), "kept"))
	holder.next(t)
	grant := holder.next(t).Grant
	if grant == nil {
		t.Fatal("holder got no grant")
	}
	if err := first.Hold(t.Context(), "m1"); err != nil {
		t.Fatalf("Hold: %v", err)
	}
	holder.next(t)
	closeWithBlockedCalls(t, first, holder)

	clock := newStepClock()
	clock.advance(100)
	second := statusBroker(t, s, clock, pids.alive)
	if err := second.Restore(t.Context()); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	reclaim := start(ctx, second, Request{Project: "a", ClientPID: 4242, GrantID: grant.ID})
	reclaim.next(t)
	reclaim.next(t)
	cancel()
	if err := reclaim.finish(t); !errors.Is(err, context.Canceled) {
		t.Fatalf("reclaim Acquire = %v, want context.Canceled", err)
	}

	ps := poolNamed(t, second.Status(), "p")
	if len(ps.Waiters) != 1 {
		t.Fatalf("waiters = %+v, want the requeued request", ps.Waiters)
	}
	wantWaiter(t, ps.Waiters[0], WaiterStatus{
		Position: 1, Project: "a", Command: []string{"kept"}, Count: 1, Since: at(0),
	})
}
