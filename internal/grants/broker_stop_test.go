package grants

import (
	"context"
	"errors"
	"os"
	"slices"
	"testing"
)

// assertStop checks that e is a stop event that names exactly the members want.
func assertStop(t *testing.T, e Event, want ...Member) {
	t.Helper()
	if e.Grant != nil || !slices.Equal(e.Stop, want) {
		t.Fatalf("event = %+v, want a stop naming %+v", e, want)
	}
}

// queuedCommands returns the commands of the requests queued in b, in arrival order.
func queuedCommands(b *Broker) [][]string {
	b.mu.Lock()
	defer b.mu.Unlock()
	commands := make([][]string, 0, len(b.waiting))
	for _, w := range b.waiting {
		commands = append(commands, w.req.Command)
	}
	return commands
}

// TestHoldStopsTheHoldersGrantUntilItsCallEnds checks a hold on a granted member. The call that holds the grant gets a
// stop that names the member, and the grant stays granted until the call's context ends. Then the grant's rows are gone,
// and a reclaim by its grant id and pid queues at the request's place, ahead of a request that queued behind it. The
// reclaim is granted a new grant once the member is freed, and the request behind it is not granted meanwhile.
func TestHoldStopsTheHoldersGrantUntilItsCallEnds(t *testing.T) {
	b, s := newBroker(t)
	p := awakePool(t, "p", 1, "m1")
	ctx, cancel := context.WithCancel(t.Context())
	holder := start(ctx, b, request("a", p, 1))
	holder.next(t)
	grant := holder.next(t).Grant
	if grant == nil {
		t.Fatal("holder got no grant")
	}

	if err := b.Hold(t.Context(), "m1"); err != nil {
		t.Fatalf("Hold: %v", err)
	}
	assertStop(t, holder.next(t), Member{Pool: "p", Name: "m1"})
	if _, held := memberOf(t, b, "p", "m1"); held != grant.ID {
		t.Fatalf("m1 held by %q after the stop, want the grant %s still holding it", held, grant.ID)
	}
	if rows := storedGrants(t, s); len(rows) != 1 || rows[0].ID != grant.ID {
		t.Fatalf("stored grants after the stop = %+v, want only grant %s", rows, grant.ID)
	}

	behind := start(t.Context(), b, request("b", p, 1))
	if e := behind.next(t); e.Position != 1 {
		t.Fatalf("behind event = %+v, want position 1 while the stopping grant holds m1", e)
	}

	cancel()
	if err := holder.finish(t); !errors.Is(err, context.Canceled) {
		t.Fatalf("holder Acquire = %v, want context.Canceled", err)
	}
	if rows := storedGrants(t, s); len(rows) != 0 {
		t.Fatalf("stored grants after the stopped call ended = %+v, want none", rows)
	}
	if _, held := memberOf(t, b, "p", "m1"); held != "" {
		t.Fatalf("m1 held by %q after the stopped call ended, want it free", held)
	}
	if got := queuedProjects(b); !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("queued = %v, want the requeued request ahead of the request behind it", got)
	}

	reclaimCtx, cancelReclaim := context.WithCancel(t.Context())
	reclaim := start(reclaimCtx, b, Request{Project: "a", ClientPID: 4242, GrantID: grant.ID})
	if e := reclaim.next(t); e.Position != 1 {
		t.Fatalf("reclaim event = %+v, want position 1, the requeued request's place", e)
	}
	if got := queuedProjects(b); !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("queued = %v while m1 is held, want both still waiting", got)
	}

	if err := b.Free(t.Context(), "m1"); err != nil {
		t.Fatalf("Free: %v", err)
	}
	regranted := reclaim.next(t).Grant
	if regranted == nil || regranted.ID == grant.ID || !slices.Equal(regranted.Members, grant.Members) {
		t.Fatalf("reclaim got %+v, want a new grant of %+v once m1 is free", regranted, grant.Members)
	}
	if got := queuedProjects(b); !slices.Equal(got, []string{"b"}) {
		t.Fatalf("queued = %v after the reclaim is granted, want only the request behind it", got)
	}
	if rows := storedGrants(t, s); len(rows) != 1 || rows[0].ID != regranted.ID {
		t.Fatalf("stored grants = %+v, want only the reclaim's grant %s", rows, regranted.ID)
	}
	cancelReclaim()
	if err := reclaim.finish(t); !errors.Is(err, context.Canceled) {
		t.Fatalf("reclaim Acquire = %v, want context.Canceled", err)
	}
}

// TestReclaimRacingTheStoppedStreamWaitsThenQueues checks a reclaim that reaches the broker while the stopped grant's
// stream is still open. The reclaim does not fail with no such grant: it waits for that stream to end, then gets its
// place in the queue.
func TestReclaimRacingTheStoppedStreamWaitsThenQueues(t *testing.T) {
	b, _ := newBroker(t)
	p := awakePool(t, "p", 1, "m1")
	ctx, cancel := context.WithCancel(t.Context())
	holder := start(ctx, b, request("a", p, 1))
	holder.next(t)
	grant := holder.next(t).Grant
	if err := b.Hold(t.Context(), "m1"); err != nil {
		t.Fatalf("Hold: %v", err)
	}
	assertStop(t, holder.next(t), Member{Pool: "p", Name: "m1"})

	reclaimCtx, cancelReclaim := context.WithCancel(t.Context())
	reclaim := start(reclaimCtx, b, Request{Project: "a", ClientPID: 4242, GrantID: grant.ID})
	cancel()
	if err := holder.finish(t); !errors.Is(err, context.Canceled) {
		t.Fatalf("holder Acquire = %v, want context.Canceled", err)
	}
	if e := reclaim.next(t); e.Position != 1 {
		t.Fatalf("reclaim event = %+v, want position 1 once the stopped stream has ended", e)
	}
	if err := b.Free(t.Context(), "m1"); err != nil {
		t.Fatalf("Free: %v", err)
	}
	regranted := reclaim.next(t).Grant
	if regranted == nil || regranted.ID == grant.ID {
		t.Fatalf("reclaim got %+v, want a new grant once m1 is free", regranted)
	}
	cancelReclaim()
	if err := reclaim.finish(t); !errors.Is(err, context.Canceled) {
		t.Fatalf("reclaim Acquire = %v, want context.Canceled", err)
	}
}

// TestReclaimThatEndsWhileQueuedKeepsItsPlace checks a reclaim whose call ends before the request is granted. The
// request stays queued at its place for the client's next reclaim, so a stream that breaks while the request waits does
// not drop it.
func TestReclaimThatEndsWhileQueuedKeepsItsPlace(t *testing.T) {
	b, _ := newBroker(t)
	p := awakePool(t, "p", 1, "m1")
	ctx, cancel := context.WithCancel(t.Context())
	holder := start(ctx, b, request("a", p, 1))
	holder.next(t)
	grant := holder.next(t).Grant
	if err := b.Hold(t.Context(), "m1"); err != nil {
		t.Fatalf("Hold: %v", err)
	}
	assertStop(t, holder.next(t), Member{Pool: "p", Name: "m1"})
	cancel()
	if err := holder.finish(t); !errors.Is(err, context.Canceled) {
		t.Fatalf("holder Acquire = %v, want context.Canceled", err)
	}

	first, cancelFirst := context.WithCancel(t.Context())
	reclaim := start(first, b, Request{Project: "a", ClientPID: 4242, GrantID: grant.ID})
	if e := reclaim.next(t); e.Position != 1 {
		t.Fatalf("first reclaim event = %+v, want position 1", e)
	}
	cancelFirst()
	if err := reclaim.finish(t); !errors.Is(err, context.Canceled) {
		t.Fatalf("first reclaim Acquire = %v, want context.Canceled", err)
	}
	if got := queuedProjects(b); !slices.Equal(got, []string{"a"}) {
		t.Fatalf("queued = %v after the first reclaim ended, want the request still at its place", got)
	}

	again := start(t.Context(), b, Request{Project: "a", ClientPID: 4242, GrantID: grant.ID})
	if e := again.next(t); e.Position != 1 {
		t.Fatalf("second reclaim event = %+v, want position 1", e)
	}
}

// TestRequeuedRequestIsNotPassedByLaterRequests checks that a request a stop requeues keeps its place while its client has
// not reclaimed it, even though a member it could take is free. A request that queues behind it waits, and gets the free
// member only after the requeued request has been granted and has ended.
func TestRequeuedRequestIsNotPassedByLaterRequests(t *testing.T) {
	s := openStore(t)
	p := awakePool(t, "p", 3, "m1", "m2", "m3")
	b := brokerOn(t, s, nil)
	// take has a request of project hold one member until its end is called, and returns once the member is granted.
	take := func(project string) (*call, context.CancelFunc) {
		ctx, end := context.WithCancel(t.Context())
		c := start(ctx, b, request(project, p, 1))
		c.next(t)
		if e := c.next(t); e.Grant == nil {
			t.Fatalf("request of project %s event = %+v, want its grant", project, e)
		}
		return c, end
	}
	// finishTake ends a request taken with take, and checks it returned because its context ended.
	finishTake := func(c *call, end context.CancelFunc) {
		end()
		if err := c.finish(t); !errors.Is(err, context.Canceled) {
			t.Fatalf("taken request Acquire = %v, want context.Canceled", err)
		}
	}
	// Three requests take the three members, so all are admitted. Two end, leaving m2 and m3 free and admitted; the
	// first keeps m1, so the pool stays in use and keeps their admitted state.
	_, endX := take("x")
	defer endX()
	y, endY := take("y")
	z, endZ := take("z")
	finishTake(z, endZ)
	finishTake(y, endY)

	ctx, cancel := context.WithCancel(t.Context())
	holder := start(ctx, b, request("a", p, 1))
	holder.next(t)
	grant := holder.next(t).Grant
	if grant == nil || len(grant.Members) != 1 {
		t.Fatalf("holder got %+v, want one member", grant)
	}
	// The members were admitted in an order the test does not fix, so the free one is read from the pool: the one member
	// admitted and granted to no grant.
	held := grant.Members[0]
	var freed []Member
	for _, name := range p.Members {
		if state, owner := memberOf(t, b, "p", name); state == Admitted && owner == "" {
			freed = append(freed, Member{Pool: "p", Name: name})
		}
	}
	if len(freed) != 1 {
		t.Fatalf("admitted free members = %+v, want exactly one", freed)
	}
	free := freed[0]
	if err := b.Hold(t.Context(), held.Name); err != nil {
		t.Fatalf("Hold: %v", err)
	}
	assertStop(t, holder.next(t), held)
	cancel()
	if err := holder.finish(t); !errors.Is(err, context.Canceled) {
		t.Fatalf("holder Acquire = %v, want context.Canceled", err)
	}

	behind := start(t.Context(), b, request("b", p, 1))
	if e := behind.next(t); e.Position != 2 {
		t.Fatalf("behind event = %+v, want position 2, behind the requeued request", e)
	}
	if rows := storedGrants(t, s); len(rows) != 1 || rows[0].Project != "x" {
		t.Fatalf("stored grants = %+v while the requeued request waits, want only x's", rows)
	}

	reclaimCtx, cancelReclaim := context.WithCancel(t.Context())
	reclaim := start(reclaimCtx, b, Request{Project: "a", ClientPID: 4242, GrantID: grant.ID})
	if e := reclaim.next(t); e.Position != 1 {
		t.Fatalf("reclaim event = %+v, want position 1", e)
	}
	regranted := reclaim.next(t).Grant
	if regranted == nil || !slices.Equal(regranted.Members, []Member{free}) {
		t.Fatalf("reclaim got %+v, want the free member %+v", regranted, free)
	}
	if got := queuedProjects(b); !slices.Equal(got, []string{"b"}) {
		t.Fatalf("queued = %v once the reclaim is granted, want the request behind it", got)
	}

	cancelReclaim()
	if err := reclaim.finish(t); !errors.Is(err, context.Canceled) {
		t.Fatalf("reclaim Acquire = %v, want context.Canceled", err)
	}
	if e := behind.next(t); e.Grant == nil || !slices.Equal(e.Grant.Members, []Member{free}) {
		t.Fatalf("behind event = %+v, want %+v once the reclaimed grant ends", e, free)
	}
}

// TestRestoredGrantWhoseMemberWasHeldGetsStopAtReattach checks a grant a restart kept, whose member a hold keeps. Its
// reclaim gets the grant, then a stop that names the held member, and the grant ends at the stop's stream end and is
// requeued for its client.
func TestRestoredGrantWhoseMemberWasHeldGetsStopAtReattach(t *testing.T) {
	s := openStore(t)
	p := awakePool(t, "p", 1, "m1")
	before := brokerOn(t, s, nil)
	grant, blocked := grantTo(t, before, p, "a", livePID)
	if err := before.Hold(t.Context(), "m1"); err != nil {
		t.Fatalf("Hold: %v", err)
	}
	closeWithBlockedCalls(t, before, blocked)

	clients := &pidSet{live: map[int]bool{livePID: true}}
	after := brokerOn(t, s, clients.alive)
	if err := after.Restore(t.Context()); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	reclaimCtx, cancelReclaim := context.WithCancel(t.Context())
	reclaim := start(reclaimCtx, after, Request{Project: "a", ClientPID: livePID, GrantID: grant.ID})
	if e := reclaim.next(t); e.Grant == nil || e.Grant.ID != grant.ID {
		t.Fatalf("reclaim event = %+v, want the restored grant %s", e, grant.ID)
	}
	assertStop(t, reclaim.next(t), Member{Pool: "p", Name: "m1"})
	cancelReclaim()
	if err := reclaim.finish(t); !errors.Is(err, context.Canceled) {
		t.Fatalf("reclaim Acquire = %v, want context.Canceled", err)
	}
	if got := queuedProjects(after); !slices.Equal(got, []string{"a"}) {
		t.Fatalf("queued = %v after the stopped reclaim ended, want the requeued request", got)
	}

	again := start(t.Context(), after, Request{Project: "a", ClientPID: livePID, GrantID: grant.ID})
	if e := again.next(t); e.Position != 1 {
		t.Fatalf("second reclaim event = %+v, want position 1", e)
	}
	// The reclaim settles the queue before it sends its place, so a grant made while m1 is held would be stored by now.
	if rows, err := s.Grants(t.Context()); err != nil || len(rows) != 0 {
		t.Fatalf("stored grants = %+v, %v while m1 is held, want none", rows, err)
	}
	if err := after.Free(t.Context(), "m1"); err != nil {
		t.Fatalf("Free: %v", err)
	}
	regranted := again.next(t).Grant
	if regranted == nil || regranted.ID == grant.ID {
		t.Fatalf("second reclaim got %+v, want a new grant once m1 is free", regranted)
	}
	if want := []Member{{Pool: "p", Name: "m1"}}; !slices.Equal(regranted.Members, want) {
		t.Fatalf("new grant members = %v, want %v", regranted.Members, want)
	}
	rows, err := s.Grants(t.Context())
	if err != nil {
		t.Fatalf("Grants: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != regranted.ID || rows[0].Pools == "" {
		t.Fatalf("stored grants = %+v, want the new grant with its pools", rows)
	}
}

// TestRequeuedRequestOfDeadClientEndsOnTick checks a requeued request whose client dies before it reclaims. The next
// tick ends it, so the requests behind it are not held back, and a reclaim by its grant id finds nothing.
func TestRequeuedRequestOfDeadClientEndsOnTick(t *testing.T) {
	s := openStore(t)
	p := awakePool(t, "p", 1, "m1")
	clients := &pidSet{live: map[int]bool{livePID: true}}
	b := brokerOn(t, s, clients.alive)
	ctx, cancel := context.WithCancel(t.Context())
	req := request("a", p, 1)
	req.ClientPID = livePID
	holder := start(ctx, b, req)
	holder.next(t)
	grant := holder.next(t).Grant
	if grant == nil {
		t.Fatal("holder got no grant")
	}
	if err := b.Hold(t.Context(), "m1"); err != nil {
		t.Fatalf("Hold: %v", err)
	}
	assertStop(t, holder.next(t), Member{Pool: "p", Name: "m1"})
	cancel()
	if err := holder.finish(t); !errors.Is(err, context.Canceled) {
		t.Fatalf("holder Acquire = %v, want context.Canceled", err)
	}
	if got := queuedProjects(b); !slices.Equal(got, []string{"a"}) {
		t.Fatalf("queued = %v after the stopped call ended, want the requeued request", got)
	}

	clients.setAlive(livePID, false)
	tick := runTicks(t, b)
	tick.send(t)
	tick.drain(t)
	if got := queuedProjects(b); len(got) != 0 {
		t.Fatalf("queued = %v after the tick, want the dead client's request ended", got)
	}
	reclaim := start(t.Context(), b, Request{Project: "a", ClientPID: livePID, GrantID: grant.ID})
	if err := reclaim.finish(t); !errors.Is(err, ErrNoGrant) {
		t.Fatalf("reclaim after the tick = %v, want ErrNoGrant", err)
	}
	if rows := storedGrants(t, s); len(rows) != 0 {
		t.Fatalf("stored grants = %+v, want none", rows)
	}
}

// TestCommandSurvivesARestart checks that a grant stores the command it is for, that a restart restores the command,
// and that the request a stop requeues keeps it.
func TestCommandSurvivesARestart(t *testing.T) {
	s := openStore(t)
	p := awakePool(t, "p", 1, "m1")
	command := []string{"make", "check"}
	before := brokerOn(t, s, nil)
	req := request("a", p, 1)
	req.ClientPID = os.Getpid()
	req.Command = command
	held := start(t.Context(), before, req)
	held.next(t)
	grant := held.next(t).Grant
	if grant == nil {
		t.Fatal("request got no grant")
	}
	closeWithBlockedCalls(t, before, held)
	if rows := storedGrants(t, s); len(rows) != 1 || !slices.Equal(rows[0].Command, command) {
		t.Fatalf("stored grants = %+v, want one grant whose command is %v", rows, command)
	}

	clients := &pidSet{live: map[int]bool{os.Getpid(): true}}
	after := brokerOn(t, s, clients.alive)
	if err := after.Restore(t.Context()); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	reclaimCtx, cancelReclaim := context.WithCancel(t.Context())
	reclaim := start(reclaimCtx, after, Request{Project: "a", ClientPID: os.Getpid(), GrantID: grant.ID})
	if e := reclaim.next(t); e.Grant == nil {
		t.Fatalf("reclaim event = %+v, want the restored grant", e)
	}
	if err := after.Hold(t.Context(), "m1"); err != nil {
		t.Fatalf("Hold: %v", err)
	}
	assertStop(t, reclaim.next(t), Member{Pool: "p", Name: "m1"})
	cancelReclaim()
	if err := reclaim.finish(t); !errors.Is(err, context.Canceled) {
		t.Fatalf("reclaim Acquire = %v, want context.Canceled", err)
	}
	if got := queuedCommands(after); len(got) != 1 || !slices.Equal(got[0], command) {
		t.Fatalf("queued commands = %v, want the requeued request's command %v", got, command)
	}
}
