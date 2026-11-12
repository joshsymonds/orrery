package grants

import (
	"errors"
	"log/slog"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/pools"
	"github.com/joshsymonds/orrery/internal/store"
)

// firstAwake defines pool p of cap limit over members, whose status exits 0 for the first member alone, so that member
// is awake and the others are asleep. Every wake and admit exits 0. members must not be empty.
func firstAwake(t *testing.T, limit int, members ...string) pools.Pool {
	t.Helper()
	return poolOf(t, "p", limit, members, `test "$ORRERY_MEMBER" = `+members[0], healthy, healthy)
}

// storedHolds returns the holds in s.
func storedHolds(t *testing.T, s *store.Store) []store.HoldRow {
	t.Helper()
	rows, err := s.Holds(t.Context())
	if err != nil {
		t.Fatalf("Holds: %v", err)
	}
	return rows
}

// awaitState polls until member of pool is in state want. A member command sets the state when it ends, and the settle
// that follows it runs under the same lock, so the state a poll sees is the state after that settle.
func awaitState(t *testing.T, b *Broker, pool, member string, want MemberState) {
	t.Helper()
	deadline := time.Now().Add(guard)
	for {
		if got, _ := memberOf(t, b, pool, member); got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("member %s of pool %s did not reach state %d within the guard", member, pool, want)
		}
		time.Sleep(time.Millisecond)
	}
}

// TestHeldMemberIsNotGrantedUntilFreed checks that a held member is never granted. With m1 held and m2 free, the first
// request is granted m2 while a second request waits; Free of m1 then lets the second request be granted m1.
func TestHeldMemberIsNotGrantedUntilFreed(t *testing.T) {
	b, _ := newBroker(t)
	p := firstAwake(t, 2, "m1", "m2")
	if err := b.Hold(t.Context(), "m1"); err != nil {
		t.Fatalf("Hold: %v", err)
	}
	first := start(t.Context(), b, request("a", p, 1))
	if e := first.next(t); e.Position != 1 {
		t.Fatalf("first event = %+v, want position 1", e)
	}
	granted := first.next(t).Grant
	if granted == nil || !slices.Equal(granted.Members, []Member{{Pool: "p", Name: "m2"}}) {
		t.Fatalf("first request got %+v, want the free member m2", granted)
	}

	second := start(t.Context(), b, request("b", p, 1))
	if e := second.next(t); e.Position != 1 {
		t.Fatalf("second event = %+v, want position 1", e)
	}
	if got := queuedProjects(b); !slices.Equal(got, []string{"b"}) {
		t.Fatalf("queued = %v, want the second request still waiting", got)
	}
	if _, grant := memberOf(t, b, "p", "m1"); grant != "" {
		t.Fatalf("held member m1 is held by grant %q, want it free", grant)
	}

	if err := b.Free(t.Context(), "m1"); err != nil {
		t.Fatalf("Free: %v", err)
	}
	granted = second.next(t).Grant
	if granted == nil || !slices.Equal(granted.Members, []Member{{Pool: "p", Name: "m1"}}) {
		t.Fatalf("second request got %+v, want m1 once it is freed", granted)
	}
}

// TestHoldSurvivesRestartAndFreeFreesIt checks that a hold placed on one broker keeps its member from a request made to
// the next broker on the same store, and that Free on the next broker lets that request be granted the member.
func TestHoldSurvivesRestartAndFreeFreesIt(t *testing.T) {
	s := openStore(t)
	p := firstAwake(t, 1, "m1")
	before := brokerOn(t, s, nil)
	if err := before.Hold(t.Context(), "m1"); err != nil {
		t.Fatalf("Hold: %v", err)
	}
	before.Close()
	if rows := storedHolds(t, s); len(rows) != 1 || rows[0].Member != "m1" {
		t.Fatalf("stored holds = %+v, want the hold on m1", rows)
	}

	after := brokerOn(t, s, nil)
	if err := after.Restore(t.Context()); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	waiter := start(t.Context(), after, request("a", p, 1))
	if e := waiter.next(t); e.Position != 1 {
		t.Fatalf("waiter event = %+v, want position 1", e)
	}
	awaitState(t, after, "p", "m1", Awake)
	if got := queuedProjects(after); !slices.Equal(got, []string{"a"}) {
		t.Fatalf("queued = %v once m1 is awake, want the request still waiting on the held member", got)
	}

	if err := after.Free(t.Context(), "m1"); err != nil {
		t.Fatalf("Free: %v", err)
	}
	granted := waiter.next(t).Grant
	if granted == nil || !slices.Equal(granted.Members, []Member{{Pool: "p", Name: "m1"}}) {
		t.Fatalf("waiter got %+v, want m1 once it is freed", granted)
	}
}

// TestHoldOfAHeldMemberKeepsItsFirstSince checks that a second Hold of a held member succeeds and that the stored hold
// keeps the time the first Hold stamped.
func TestHoldOfAHeldMemberKeepsItsFirstSince(t *testing.T) {
	s := openStore(t)
	now := fixedNow()
	first := now
	b := NewBroker(Config{
		Host: testHost, Store: s, Now: func() time.Time { return now }, Logger: slog.New(slog.DiscardHandler),
	})
	t.Cleanup(b.Close)
	if err := b.Hold(t.Context(), "m1"); err != nil {
		t.Fatalf("Hold: %v", err)
	}
	now = now.Add(time.Hour)
	if err := b.Hold(t.Context(), "m1"); err != nil {
		t.Fatalf("second Hold of a held member: %v", err)
	}
	rows := storedHolds(t, s)
	if len(rows) != 1 || !rows[0].Since.Equal(first) {
		t.Fatalf("stored holds = %+v, want one hold on m1 since %v", rows, first)
	}
}

// TestFreeOfAMemberNotHeldIsErrNotHeld checks that Free refuses a member that is not held, both before any hold and
// after the hold is freed.
func TestFreeOfAMemberNotHeldIsErrNotHeld(t *testing.T) {
	b, _ := newBroker(t)
	if err := b.Free(t.Context(), "m1"); !errors.Is(err, ErrNotHeld) {
		t.Fatalf("Free of a member never held = %v, want ErrNotHeld", err)
	}
	if err := b.Hold(t.Context(), "m1"); err != nil {
		t.Fatalf("Hold: %v", err)
	}
	if err := b.Free(t.Context(), "m1"); err != nil {
		t.Fatalf("Free: %v", err)
	}
	if err := b.Free(t.Context(), "m1"); !errors.Is(err, ErrNotHeld) {
		t.Fatalf("Free of a member already freed = %v, want ErrNotHeld", err)
	}
}

// TestBadMemberNameIsErrBadMember checks that Hold and Free refuse a name outside [A-Za-z0-9][A-Za-z0-9._-]*, that a
// refused Hold stores nothing, and that the names the pattern allows are held.
func TestBadMemberNameIsErrBadMember(t *testing.T) {
	b, s := newBroker(t)
	for _, name := range []string{"", "-m1", ".m1", "m 1", "m/1", "m:1", "m\n1"} {
		if err := b.Hold(t.Context(), name); !errors.Is(err, ErrBadMember) {
			t.Fatalf("Hold(%q) = %v, want ErrBadMember", name, err)
		}
		if err := b.Free(t.Context(), name); !errors.Is(err, ErrBadMember) {
			t.Fatalf("Free(%q) = %v, want ErrBadMember", name, err)
		}
	}
	if rows := storedHolds(t, s); len(rows) != 0 {
		t.Fatalf("refused holds stored %+v, want none", rows)
	}
	valid := []string{"m1", "A.b_c-9", "9x"}
	for _, name := range valid {
		if err := b.Hold(t.Context(), name); err != nil {
			t.Fatalf("Hold(%q) = %v, want it held", name, err)
		}
	}
	if rows := storedHolds(t, s); len(rows) != len(valid) {
		t.Fatalf("stored holds = %+v, want %d holds", rows, len(valid))
	}
}

// TestHoldAndFreeReturnErrClosedAfterClose checks that once the broker closes, Hold and Free return ErrClosed, even for
// a held member whose Free would otherwise succeed.
func TestHoldAndFreeReturnErrClosedAfterClose(t *testing.T) {
	b, _ := newBroker(t)
	if err := b.Hold(t.Context(), "m1"); err != nil {
		t.Fatalf("Hold: %v", err)
	}
	b.Close()
	if err := b.Hold(t.Context(), "m2"); !errors.Is(err, ErrClosed) {
		t.Fatalf("Hold after Close = %v, want ErrClosed", err)
	}
	if err := b.Free(t.Context(), "m1"); !errors.Is(err, ErrClosed) {
		t.Fatalf("Free after Close = %v, want ErrClosed", err)
	}
}

// TestHoldOfAMemberOfNoPoolInUseAppliesWhenItsPoolComesIntoUse checks that a hold placed before any pool is in use
// keeps its member from the first request for that pool.
func TestHoldOfAMemberOfNoPoolInUseAppliesWhenItsPoolComesIntoUse(t *testing.T) {
	b, _ := newBroker(t)
	p := firstAwake(t, 2, "m1", "m2")
	if err := b.Hold(t.Context(), "m1"); err != nil {
		t.Fatalf("Hold of a member of no pool in use: %v", err)
	}
	c := start(t.Context(), b, request("a", p, 1))
	if e := c.next(t); e.Position != 1 {
		t.Fatalf("event = %+v, want position 1", e)
	}
	granted := c.next(t).Grant
	if granted == nil || !slices.Equal(granted.Members, []Member{{Pool: "p", Name: "m2"}}) {
		t.Fatalf("request got %+v, want the free member m2 while m1 is held", granted)
	}
}

// TestHoldOfAGrantedMemberLeavesItsGrantAlone checks that a hold on a member a request already holds leaves that grant
// stored and held. Ending the grant of a held member is the stop path, a later task.
func TestHoldOfAGrantedMemberLeavesItsGrantAlone(t *testing.T) {
	b, s := newBroker(t)
	p := firstAwake(t, 1, "m1")
	grant, _ := grantTo(t, b, p, "a", os.Getpid())
	if err := b.Hold(t.Context(), "m1"); err != nil {
		t.Fatalf("Hold of a granted member: %v", err)
	}
	if _, holder := memberOf(t, b, "p", "m1"); holder != grant.ID {
		t.Fatalf("m1 held by %q after Hold, want the grant %s still holding it", holder, grant.ID)
	}
	if rows := storedGrants(t, s); len(rows) != 1 || rows[0].ID != grant.ID {
		t.Fatalf("stored grants after Hold = %+v, want only grant %s", rows, grant.ID)
	}
}
