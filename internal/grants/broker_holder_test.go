package grants

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"sync"
	"testing"

	"github.com/joshsymonds/orrery/internal/pools"
	"github.com/joshsymonds/orrery/internal/store"
)

// holderOne and holderTwo are the holder directories of the holder tests. The fake HolderAlive reports only the holders
// a test puts in its holderSet.
const (
	holderOne = "/run/orrery/holders/one"
	holderTwo = "/run/orrery/holders/two"
	// otherPID is a client pid that is neither livePID nor deadPID, so a reclaim from it matches a grant by holder alone.
	otherPID = 7171
)

// holderSet is the set of holder directories a test's fake HolderAlive reports alive. The test changes it while a
// broker reads it.
type holderSet struct {
	mu   sync.Mutex
	live map[string]bool
}

// alive reports whether dir is in the set.
func (s *holderSet) alive(dir string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.live[dir]
}

// setAlive puts dir in the set when on is true, and takes it out otherwise.
func (s *holderSet) setAlive(dir string, on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.live[dir] = on
}

// brokerOnHolders returns a broker on s whose clients are alive as alive reports and whose holders are alive as
// holderAlive reports, closed when the test ends.
func brokerOnHolders(t *testing.T, s *store.Store, alive func(int, int64) bool, holderAlive func(string) bool) *Broker {
	t.Helper()
	b := NewBroker(Config{
		Host: testHost, Store: s, Environ: commandEnv(), Now: fixedNow,
		Logger: slog.New(slog.DiscardHandler), Alive: alive, HolderAlive: holderAlive,
	})
	t.Cleanup(b.Close)
	return b
}

// grantHeldTo is grantTo for a request of project from client pid that names holder. It returns the grant and the call,
// which holds the grant until the test cancels the call or closes b.
func grantHeldTo(t *testing.T, b *Broker, p pools.Pool, project string, pid int, holder string) (*Grant, *call) {
	t.Helper()
	req := request(project, p, 1)
	req.ClientPID = pid
	req.Holder = holder
	c := start(t.Context(), b, req)
	c.next(t)
	grant := c.next(t).Grant
	if grant == nil {
		t.Fatalf("request of project %s got no grant", project)
	}
	return grant, c
}

// storeHeldGrant has a broker grant one member of p to a client that dies, which names holderOne, then closes it. It
// returns the grant, which the store keeps.
func storeHeldGrant(t *testing.T, s *store.Store, p pools.Pool) *Grant {
	t.Helper()
	before := brokerOn(t, s, nil)
	held, heldCall := grantHeldTo(t, before, p, "a", deadPID, holderOne)
	closeWithBlockedCalls(t, before, heldCall)
	if rows := storedGrants(t, s); len(rows) != 1 || rows[0].ID != held.ID || rows[0].Holder != holderOne {
		t.Fatalf("stored grants = %+v, want grant %s naming holder %s", rows, held.ID, holderOne)
	}
	return held
}

// restoreKept restores after, a broker on s, and checks that the grant held is kept with its member held.
func restoreKept(t *testing.T, s *store.Store, after *Broker, p pools.Pool, held *Grant) {
	t.Helper()
	if err := after.Restore(t.Context()); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if rows := storedGrants(t, s); len(rows) != 1 || rows[0].ID != held.ID {
		t.Fatalf("stored grants after Restore = %+v, want grant %s kept by its live holder", rows, held.ID)
	}
	if _, grant := memberOf(t, after, p.Name, held.Members[0].Name); grant != held.ID {
		t.Fatalf("%s held by %q after Restore, want the kept grant %s", held.Members[0].Name, grant, held.ID)
	}
}

// restoreHeldGrant has a broker grant one member of p to a client that dies, which names holderOne, then closes it. It
// restores a broker on the same store, whose clients are only livePID and whose holders are holders, and checks that
// the grant is kept with its member held. It returns that broker and the grant.
func restoreHeldGrant(t *testing.T, s *store.Store, p pools.Pool, holders *holderSet) (*Broker, *Grant) {
	t.Helper()
	held := storeHeldGrant(t, s, p)
	clients := &pidSet{live: map[int]bool{livePID: true}}
	after := brokerOnHolders(t, s, clients.alive, holders.alive)
	restoreKept(t, s, after, p, held)
	return after, held
}

// TestRestoreKeepsGrantWhoseHolderLivesAndReclaimByHolder checks that a stored grant whose client is dead survives a
// restart while its holder lives, with its member held. A reclaim that names the holder from another pid gets the grant.
func TestRestoreKeepsGrantWhoseHolderLivesAndReclaimByHolder(t *testing.T) {
	s := openStore(t)
	p := awakePool(t, "p", 1, "m1")
	holders := &holderSet{live: map[string]bool{holderOne: true}}
	after, held := restoreHeldGrant(t, s, p, holders)

	reclaimCtx, cancelReclaim := context.WithCancel(t.Context())
	reclaim := start(reclaimCtx, after, Request{Project: "a", ClientPID: otherPID, Holder: holderOne, GrantID: held.ID})
	got := reclaim.next(t).Grant
	if got == nil || got.ID != held.ID || !slices.Equal(got.Members, held.Members) {
		t.Fatalf("reclaim by holder got %+v, want grant %s with members %+v", got, held.ID, held.Members)
	}
	cancelReclaim()
	if err := reclaim.finish(t); !errors.Is(err, context.Canceled) {
		t.Fatalf("reclaim Acquire = %v, want context.Canceled", err)
	}
}

// TestTickEndsGrantOnceItsHolderIsGone checks that a kept grant whose client is dead lasts while its holder lives: the
// client's watch signals, and the grant is kept. Once the holder is gone and its watch signals, the grant ends, frees
// its member, and grants it to the request waiting for it.
func TestTickEndsGrantOnceItsHolderIsGone(t *testing.T) {
	s := openStore(t)
	p := awakePool(t, "p", 1, "m1")
	holders := &holderSet{live: map[string]bool{holderOne: true}}
	held := storeHeldGrant(t, s, p)
	clients := &pidSet{live: map[int]bool{livePID: true}}
	exits := newExitFakes()
	after := brokerWatched(t, s, clients.alive, holders.alive, exits)
	restoreKept(t, s, after, p, held)
	exits.awaitCheck()
	waiter := start(t.Context(), after, request("b", p, 1))
	if e := waiter.next(t); e.Position != 1 {
		t.Fatalf("waiter event = %+v, want position 1", e)
	}

	exits.signal(t, clientKey(deadPID))
	exits.awaitCheck()
	if _, grant := memberOf(t, after, "p", "m1"); grant != held.ID {
		t.Fatalf("m1 held by %q after the client's exit, want the grant %s its live holder keeps", grant, held.ID)
	}

	holders.setAlive(holderOne, false)
	exits.signal(t, holderKey(holderOne))
	granted := waiter.next(t).Grant
	if granted == nil || !slices.Equal(granted.Members, []Member{{Pool: "p", Name: "m1"}}) {
		t.Fatalf("waiter got %+v, want m1 once the grant's holder is gone", granted)
	}
	if rows := storedGrants(t, s); len(rows) != 1 || rows[0].ID != granted.ID {
		t.Fatalf("stored grants = %+v, want only the waiter's grant %s", rows, granted.ID)
	}
}

// TestRestoreEndsGrantWithoutHolderWhenClientIsDead checks that a stored grant that names no holder still ends at the
// restart when its client is dead, whatever HolderAlive reports.
func TestRestoreEndsGrantWithoutHolderWhenClientIsDead(t *testing.T) {
	s := openStore(t)
	p := awakePool(t, "p", 1, "m1")
	before := brokerOn(t, s, nil)
	_, deadCall := grantTo(t, before, p, "a", deadPID)
	closeWithBlockedCalls(t, before, deadCall)

	clients := &pidSet{live: map[int]bool{livePID: true}}
	holders := &holderSet{live: map[string]bool{holderOne: true}}
	after := brokerOnHolders(t, s, clients.alive, holders.alive)
	if err := after.Restore(t.Context()); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if rows := storedGrants(t, s); len(rows) != 0 {
		t.Fatalf("stored grants after Restore = %+v, want none: the grant names no holder and its client is dead", rows)
	}
	waiter := start(t.Context(), after, request("b", p, 1))
	if e := waiter.next(t); e.Position != 1 {
		t.Fatalf("waiter event = %+v, want position 1", e)
	}
	granted := waiter.next(t).Grant
	if granted == nil || !slices.Equal(granted.Members, []Member{{Pool: "p", Name: "m1"}}) {
		t.Fatalf("waiter got %+v, want m1, free once the grant without a holder ended", granted)
	}
}

// TestReclaimNamingAnotherHolderIsErrNoGrant checks that a reclaim of a kept grant is refused with ErrNoGrant, and sends
// nothing, when it names a holder other than the grant's.
func TestReclaimNamingAnotherHolderIsErrNoGrant(t *testing.T) {
	s := openStore(t)
	p := awakePool(t, "p", 1, "m1")
	holders := &holderSet{live: map[string]bool{holderOne: true, holderTwo: true}}
	after, held := restoreHeldGrant(t, s, p, holders)

	err := after.Acquire(t.Context(), Request{Project: "a", ClientPID: otherPID, Holder: holderTwo, GrantID: held.ID},
		func(e Event) error {
			t.Errorf("refused reclaim sent %+v", e)
			return nil
		})
	if !errors.Is(err, ErrNoGrant) {
		t.Fatalf("reclaim naming another holder = %v, want ErrNoGrant", err)
	}
}

// TestStoppedGrantIsReclaimedByItsHolderAndRequeuedLastsWithIt checks a grant whose holder names it as well as its
// client. A hold stops the grant; a reclaim by the holder from another pid is queued at the grant's place once the call
// ends. The client's exit then keeps the requeued request while its holder lives, and the holder's exit ends it.
func TestStoppedGrantIsReclaimedByItsHolderAndRequeuedLastsWithIt(t *testing.T) {
	s := openStore(t)
	p := awakePool(t, "p", 1, "m1")
	clients := &pidSet{live: map[int]bool{livePID: true}}
	holders := &holderSet{live: map[string]bool{holderOne: true}}
	exits := newExitFakes()
	b := brokerWatched(t, s, clients.alive, holders.alive, exits)
	ctx, cancel := context.WithCancel(t.Context())
	req := request("a", p, 1)
	req.ClientPID = livePID
	req.Holder = holderOne
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

	reclaimCtx, cancelReclaim := context.WithCancel(t.Context())
	reclaim := start(reclaimCtx, b, Request{Project: "a", ClientPID: otherPID, Holder: holderOne, GrantID: grant.ID})
	cancel()
	if err := holder.finish(t); !errors.Is(err, context.Canceled) {
		t.Fatalf("holder Acquire = %v, want context.Canceled", err)
	}
	if e := reclaim.next(t); e.Position != 1 {
		t.Fatalf("reclaim by holder event = %+v, want position 1 in the requeued place", e)
	}
	cancelReclaim()
	if err := reclaim.finish(t); !errors.Is(err, context.Canceled) {
		t.Fatalf("reclaim Acquire = %v, want context.Canceled", err)
	}

	clients.setAlive(livePID, false)
	exits.signal(t, clientKey(livePID))
	exits.awaitCheck()
	if got := queuedProjects(b); !slices.Equal(got, []string{"a"}) {
		t.Fatalf("queued = %v after the client's exit while the holder lives, want the requeued request kept", got)
	}

	holders.setAlive(holderOne, false)
	exits.signal(t, holderKey(holderOne))
	exits.awaitCheck()
	if got := queuedProjects(b); len(got) != 0 {
		t.Fatalf("queued = %v after the holder's exit, want the requeued request ended", got)
	}
}
