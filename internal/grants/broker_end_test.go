package grants

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/storetest"
)

// storeLock holds the write lock of a store file from a second connection, as Litestream does during a checkpoint.
type storeLock struct {
	conn *sql.Conn
}

// openBusyStore returns a new store under t.TempDir() with its path, closed when the test ends. Its busy timeout is 0, so
// a write while a storeLock holds the file fails at once with SQLITE_BUSY and the test waits out no timeout.
func openBusyStore(t *testing.T) (*store.Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "orrery.db")
	storetest.Seed(t, path)
	s, err := store.Open(t.Context(), path, storetest.NoSync(), store.WithBusyTimeout(0))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	return s, path
}

// lockStore takes the write lock of the store file at path on a second connection. The connection closes when the test
// ends, which frees a lock the test still holds.
func lockStore(t *testing.T, path string) *storeLock {
	t.Helper()
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open lock connection: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close lock database: %v", err)
		}
	})
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatalf("lock connection: %v", err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Errorf("close lock connection: %v", err)
		}
	})
	if _, err = conn.ExecContext(t.Context(), "BEGIN IMMEDIATE"); err != nil {
		t.Fatalf("take write lock: %v", err)
	}
	return &storeLock{conn: conn}
}

// release frees the write lock.
func (l *storeLock) release(t *testing.T) {
	t.Helper()
	if _, err := l.conn.ExecContext(t.Context(), "ROLLBACK"); err != nil {
		t.Fatalf("release write lock: %v", err)
	}
}

// TestEndedGrantTheStoreRefusesKeepsItsMembersUntilASettleRemovesIt ends a grant while the store refuses writes. The
// grant's row stays, so its member stays granted to it and a request queued for the member is not granted it. Once the
// store takes writes again, the next settle, here the one the store's retry fires, removes the row and grants the
// member to the queued request, which stores it with no ErrMemberGranted.
func TestEndedGrantTheStoreRefusesKeepsItsMembersUntilASettleRemovesIt(t *testing.T) {
	s, path := openBusyStore(t)
	timers := &retryTimers{}
	b := wakeBroker(t, s, nil, nil, timers)
	p := awakePool(t, "p", 1, "m1")
	ctx, cancel := context.WithCancel(t.Context())
	first := start(ctx, b, request("a", p, 1))
	first.next(t)
	held := first.next(t).Grant

	lock := lockStore(t, path)
	cancel()
	err := first.finish(t)
	if !errors.Is(err, context.Canceled) || !store.Busy(err) {
		t.Fatalf("Acquire = %v, want context.Canceled joined with the store's busy refusal of the end", err)
	}
	if _, grant := memberOf(t, b, "p", "m1"); grant != held.ID {
		t.Fatalf("m1 held by %q after its grant ended unstored, want the ended grant %s", grant, held.ID)
	}
	second := start(t.Context(), b, request("b", p, 1))
	if e := second.next(t); e.Position != 1 {
		t.Fatalf("second event = %+v, want position 1", e)
	}
	if got := queuedProjects(b); !slices.Equal(got, []string{"b"}) {
		t.Fatalf("queued = %v while the ended grant's row stays, want b waiting", got)
	}
	if _, grant := memberOf(t, b, "p", "m1"); grant != held.ID {
		t.Fatalf("m1 held by %q while the store refuses the end, want the ended grant %s", grant, held.ID)
	}

	lock.release(t)
	timers.fire(t)
	next := second.next(t).Grant
	if next == nil || !slices.Equal(next.Members, []Member{{Pool: "p", Name: "m1"}}) {
		t.Fatalf("second request got %+v, want m1 once the ended grant's row is removed", next)
	}
	if rows := storedGrants(t, s); len(rows) != 1 || rows[0].ID != next.ID {
		t.Fatalf("stored grants = %+v, want only the second grant %s", rows, next.ID)
	}
}

// TestGrantTheStoreRefusesBusyKeepsTheRequestQueuedUntilASettleStoresIt queues a request while the store refuses writes.
// Its grant is not stored, and the request stays queued rather than failing. Once the store takes writes again, the
// settle the store's retry fires stores the grant and grants the request.
func TestGrantTheStoreRefusesBusyKeepsTheRequestQueuedUntilASettleStoresIt(t *testing.T) {
	s, path := openBusyStore(t)
	timers := &retryTimers{}
	b := wakeBroker(t, s, nil, nil, timers)
	p := awakePool(t, "p", 1, "m1")
	lock := lockStore(t, path)
	c := start(t.Context(), b, request("a", p, 1))
	if e := c.next(t); e.Position != 1 {
		t.Fatalf("first event = %+v, want position 1", e)
	}
	// The status and the admit that follow it run after the position is sent. Once m1 is admitted, its settle has
	// refused the grant and armed the retry.
	awaitState(t, b, "p", "m1", Admitted)
	if got := queuedProjects(b); !slices.Equal(got, []string{"a"}) {
		t.Fatalf("queued = %v while the store refuses the grant, want a waiting", got)
	}
	if rows := storedGrants(t, s); len(rows) != 0 {
		t.Fatalf("stored grants while the store refuses writes = %+v, want none", rows)
	}

	lock.release(t)
	timers.fire(t)
	granted := c.next(t).Grant
	if granted == nil || !slices.Equal(granted.Members, []Member{{Pool: "p", Name: "m1"}}) {
		t.Fatalf("request got %+v, want m1 once the store takes the grant", granted)
	}
	if rows := storedGrants(t, s); len(rows) != 1 || rows[0].ID != granted.ID {
		t.Fatalf("stored grants = %+v, want the grant %s", rows, granted.ID)
	}
}

// TestRestoreKeepsADeadGrantTheStoreRefusesToEndUntilASettleRemovesIt restores a stored grant of a dead client while the
// store refuses writes. Restore serves all the same, with the grant's member held for the grant, and the settle of the
// next request, once the store takes writes, removes the row and grants the member.
func TestRestoreKeepsADeadGrantTheStoreRefusesToEndUntilASettleRemovesIt(t *testing.T) {
	s, path := openBusyStore(t)
	p := awakePool(t, "p", 1, "m1")
	before := brokerOn(t, s, nil)
	dead, deadCall := grantTo(t, before, p, "a", deadPID)
	closeWithBlockedCalls(t, before, deadCall)

	after := brokerOn(t, s, (&pidSet{live: map[int]bool{}}).alive)
	lock := lockStore(t, path)
	if err := after.Restore(t.Context()); err != nil {
		t.Fatalf("Restore while the store refuses the end: %v", err)
	}
	if _, grant := memberOf(t, after, p.Name, "m1"); grant != dead.ID {
		t.Fatalf("m1 held by %q after Restore, want the dead grant %s until its row is removed", grant, dead.ID)
	}
	if rows := storedGrants(t, s); len(rows) != 1 || rows[0].ID != dead.ID {
		t.Fatalf("stored grants after Restore = %+v, want the dead grant %s", rows, dead.ID)
	}

	lock.release(t)
	waiter := start(t.Context(), after, request("c", p, 1))
	waiter.next(t)
	granted := waiter.next(t).Grant
	if granted == nil || !slices.Equal(granted.Members, []Member{{Pool: "p", Name: "m1"}}) {
		t.Fatalf("waiter got %+v, want m1 once the dead grant's row is removed", granted)
	}
	if rows := storedGrants(t, s); len(rows) != 1 || rows[0].ID != granted.ID {
		t.Fatalf("stored grants = %+v, want only the waiter's grant %s", rows, granted.ID)
	}
}
