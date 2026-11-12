package grants

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/joshsymonds/orrery/internal/pools"
	"github.com/joshsymonds/orrery/internal/store"
)

// deadPID and livePID are the clients of the restart tests. The fake Alive reports only livePID.
const (
	deadPID = 5151
	livePID = 6161
)

// pidSet is the set of pids a test's fake Alive reports alive. The test changes it while a broker reads it.
type pidSet struct {
	mu   sync.Mutex
	live map[int]bool
}

// alive reports whether pid is in the set. It ignores the start time: these tests check the death of clients, and
// TestRestartEndsGrantWhosePidNowBelongsToAnotherProcess checks the start time with the real Alive.
func (s *pidSet) alive(pid int, _ int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.live[pid]
}

// setAlive puts pid in the set when on is true, and takes it out otherwise.
func (s *pidSet) setAlive(pid int, on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.live[pid] = on
}

// openStore returns a new store under t.TempDir(), closed when the test ends.
func openStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "orrery.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	return s
}

// brokerOn returns a broker on s whose clients are alive as alive reports, closed when the test ends. Cleanups run last
// in first out, so the broker is closed before the store it shares.
func brokerOn(t *testing.T, s *store.Store, alive func(int, int64) bool) *Broker {
	t.Helper()
	b := NewBroker(Config{
		Host: testHost, Store: s, Environ: commandEnv(), Now: fixedNow,
		Logger: slog.New(slog.DiscardHandler), Alive: alive,
	})
	t.Cleanup(b.Close)
	return b
}

// grantTo has b grant one member of p to a request of project from client pid. It returns the grant and the call, which
// holds the grant until the test cancels the call or closes b.
func grantTo(t *testing.T, b *Broker, p pools.Pool, project string, pid int) (*Grant, *call) {
	t.Helper()
	req := request(project, p, 1)
	req.ClientPID = pid
	c := start(t.Context(), b, req)
	c.next(t)
	grant := c.next(t).Grant
	if grant == nil {
		t.Fatalf("request of project %s got no grant", project)
	}
	return grant, c
}

// closeWithBlockedCalls closes b, then checks that each call returns ErrClosed, which leaves its grant stored.
func closeWithBlockedCalls(t *testing.T, b *Broker, calls ...*call) {
	t.Helper()
	b.Close()
	for _, c := range calls {
		if err := c.finish(t); !errors.Is(err, ErrClosed) {
			t.Fatalf("Acquire on the closed broker = %v, want ErrClosed", err)
		}
	}
}

// TestRestartKeepsLiveClientsGrantAndEndsDeadOnes checks the restart a grant must survive. A broker closes with two
// grants stored, one for a client that dies and one for a live client. The next broker on the same store ends the dead
// client's grant at once, so its member goes to a waiting request, and it keeps the live client's member for that
// client until the client reclaims the grant.
func TestRestartKeepsLiveClientsGrantAndEndsDeadOnes(t *testing.T) {
	s := openStore(t)
	p := awakePool(t, "p", 2, "m1", "m2")
	before := brokerOn(t, s, nil)
	dead, deadCall := grantTo(t, before, p, "a", deadPID)
	live, liveCall := grantTo(t, before, p, "b", livePID)
	closeWithBlockedCalls(t, before, deadCall, liveCall)
	deadMember, liveMember := dead.Members[0], live.Members[0]

	clients := &pidSet{live: map[int]bool{livePID: true}}
	after := brokerOn(t, s, clients.alive)
	if err := after.Restore(t.Context()); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if rows := storedGrants(t, s); len(rows) != 1 || rows[0].ID != live.ID {
		t.Fatalf("stored grants after Restore = %+v, want only live grant %s", rows, live.ID)
	}
	if _, grant := memberOf(t, after, p.Name, deadMember.Name); grant != "" {
		t.Fatalf("%s held by %q after Restore, want it free: its client is dead", deadMember.Name, grant)
	}
	if _, grant := memberOf(t, after, p.Name, liveMember.Name); grant != live.ID {
		t.Fatalf("%s held by %q after Restore, want the live grant %s", liveMember.Name, grant, live.ID)
	}

	waiter := start(t.Context(), after, request("c", p, 1))
	if e := waiter.next(t); e.Position != 1 {
		t.Fatalf("waiter event = %+v, want position 1", e)
	}
	granted := waiter.next(t).Grant
	if granted == nil || !slices.Equal(granted.Members, []Member{deadMember}) {
		t.Fatalf("waiter got %+v, want %+v, the member of the dead client", granted, deadMember)
	}

	queued := start(t.Context(), after, request("d", p, 1))
	if e := queued.next(t); e.Position != 1 {
		t.Fatalf("second waiter event = %+v, want position 1 while the live member is held", e)
	}
	reclaimCtx, cancelReclaim := context.WithCancel(t.Context())
	reclaim := start(reclaimCtx, after, Request{Project: "b", ClientPID: livePID, GrantID: live.ID})
	reclaimed := reclaim.next(t).Grant
	if reclaimed == nil || reclaimed.ID != live.ID || !slices.Equal(reclaimed.Members, live.Members) {
		t.Fatalf("reclaim got %+v, want grant %s with members %+v", reclaimed, live.ID, live.Members)
	}
	if got := queuedProjects(after); !slices.Equal(got, []string{"d"}) {
		t.Fatalf("queued projects while the live grant is reclaimed = %v, want [d]", got)
	}
	cancelReclaim()
	if err := reclaim.finish(t); !errors.Is(err, context.Canceled) {
		t.Fatalf("reclaim Acquire = %v, want context.Canceled", err)
	}
	if e := queued.next(t); e.Grant == nil || !slices.Equal(e.Grant.Members, []Member{liveMember}) {
		t.Fatalf("second waiter event = %+v, want %+v once the reclaimed call ends", e, liveMember)
	}
	if rows := storedGrants(t, s); len(rows) != 2 {
		t.Fatalf("stored grants = %+v, want the two waiters' grants", rows)
	}
}

// TestReclaimNeedsStoredGrantIDAndItsClientPID checks that a kept grant is reclaimed by its id and its client's pid,
// and once. Any other id or pid is refused with ErrNoGrant and sends nothing. A grant that a call holds refuses a
// second reclaim, and an ended grant refuses a reclaim.
func TestReclaimNeedsStoredGrantIDAndItsClientPID(t *testing.T) {
	s := openStore(t)
	p := awakePool(t, "p", 1, "m1")
	before := brokerOn(t, s, nil)
	live, liveCall := grantTo(t, before, p, "b", livePID)
	closeWithBlockedCalls(t, before, liveCall)

	clients := &pidSet{live: map[int]bool{livePID: true}}
	after := brokerOn(t, s, clients.alive)
	if err := after.Restore(t.Context()); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	refused := func(t *testing.T, req Request) {
		t.Helper()
		err := after.Acquire(t.Context(), req, func(e Event) error {
			t.Errorf("refused reclaim sent %+v", e)
			return nil
		})
		if !errors.Is(err, ErrNoGrant) {
			t.Fatalf("Acquire = %v, want ErrNoGrant", err)
		}
	}
	t.Run("unknown grant", func(t *testing.T) {
		refused(t, Request{Project: "b", ClientPID: livePID, GrantID: "no-such-grant"})
	})
	t.Run("another client's pid", func(t *testing.T) {
		refused(t, Request{Project: "b", ClientPID: deadPID, GrantID: live.ID})
	})

	reclaimCtx, cancelReclaim := context.WithCancel(t.Context())
	reclaim := start(reclaimCtx, after, Request{Project: "b", ClientPID: livePID, GrantID: live.ID})
	got := reclaim.next(t).Grant
	if got == nil || got.ID != live.ID || !slices.Equal(got.Members, live.Members) {
		t.Fatalf("reclaim got %+v, want grant %s with members %+v", got, live.ID, live.Members)
	}
	t.Run("attached grant", func(t *testing.T) {
		refused(t, Request{Project: "b", ClientPID: livePID, GrantID: live.ID})
	})
	cancelReclaim()
	if err := reclaim.finish(t); !errors.Is(err, context.Canceled) {
		t.Fatalf("reclaim Acquire = %v, want context.Canceled", err)
	}
	t.Run("ended grant", func(t *testing.T) {
		refused(t, Request{Project: "b", ClientPID: livePID, GrantID: live.ID})
	})
	if rows := storedGrants(t, s); len(rows) != 0 {
		t.Fatalf("stored grants after the reclaimed call ended = %+v, want none", rows)
	}
}

// TestTickEndsKeptGrantWhoseClientDies checks that a grant a restart kept ends on the next tick once its client is no
// longer alive: its store row is removed, its member is freed, and the request waiting for the member gets it.
func TestTickEndsKeptGrantWhoseClientDies(t *testing.T) {
	s := openStore(t)
	p := awakePool(t, "p", 1, "m1")
	before := brokerOn(t, s, nil)
	live, liveCall := grantTo(t, before, p, "a", livePID)
	closeWithBlockedCalls(t, before, liveCall)

	clients := &pidSet{live: map[int]bool{livePID: true}}
	after := brokerOn(t, s, clients.alive)
	if err := after.Restore(t.Context()); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if _, grant := memberOf(t, after, "p", "m1"); grant != live.ID {
		t.Fatalf("m1 held by %q after Restore, want the live grant %s", grant, live.ID)
	}
	waiter := start(t.Context(), after, request("b", p, 1))
	if e := waiter.next(t); e.Position != 1 {
		t.Fatalf("waiter event = %+v, want position 1", e)
	}

	clients.setAlive(livePID, false)
	tick := runTicks(t, after)
	tick.send(t)
	granted := waiter.next(t).Grant
	if granted == nil || !slices.Equal(granted.Members, []Member{{Pool: "p", Name: "m1"}}) {
		t.Fatalf("waiter got %+v, want m1 once the dead client's grant ends", granted)
	}
	if rows := storedGrants(t, s); len(rows) != 1 || rows[0].ID != granted.ID {
		t.Fatalf("stored grants = %+v, want only the waiter's grant %s", rows, granted.ID)
	}
}

// TestRestartEndsGrantWhosePidNowBelongsToAnotherProcess checks a restart against pid reuse. A stored grant whose client
// pid a live, unrelated process now holds is not kept: the start time stored for the client differs from the process's,
// so the client is gone. The grant ends at the restart, a tick leaves it ended, and its member is free for a request.
func TestRestartEndsGrantWhosePidNowBelongsToAnotherProcess(t *testing.T) {
	s := openStore(t)
	p := awakePool(t, "p", 1, "m1")
	unrelated := exec.Command("sleep", "60")
	if err := unrelated.Start(); err != nil {
		t.Fatalf("start unrelated process: %v", err)
	}
	t.Cleanup(func() {
		// The process is killed, so Wait reports its signal, which the test does not check.
		_ = unrelated.Process.Kill()
		_ = unrelated.Wait()
	})
	defsJSON, err := encodePools([]pools.Pool{p})
	if err != nil {
		t.Fatalf("encode pools: %v", err)
	}
	// No process has a negative start time, so the stored start differs from the unrelated process's.
	const reusedStart = -1
	row := store.GrantRow{
		ID: "reused", Project: "a", ClientPID: unrelated.Process.Pid, ClientStart: reusedStart, Pools: defsJSON,
		Members: []store.GrantMember{{Pool: "p", Member: "m1"}}, Granted: fixedNow(),
	}
	if err := s.AddGrant(t.Context(), row); err != nil {
		t.Fatalf("AddGrant: %v", err)
	}

	after := brokerOn(t, s, nil)
	if err := after.Restore(t.Context()); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if rows := storedGrants(t, s); len(rows) != 0 {
		t.Fatalf("stored grants after Restore = %+v, want none: the pid belongs to another process", rows)
	}
	tick := runTicks(t, after)
	tick.send(t)
	if rows := storedGrants(t, s); len(rows) != 0 {
		t.Fatalf("stored grants after a tick = %+v, want none", rows)
	}
	waiter := start(t.Context(), after, request("b", p, 1))
	if e := waiter.next(t); e.Position != 1 {
		t.Fatalf("waiter event = %+v, want position 1", e)
	}
	granted := waiter.next(t).Grant
	if granted == nil || !slices.Equal(granted.Members, []Member{{Pool: "p", Name: "m1"}}) {
		t.Fatalf("waiter got %+v, want m1, free once the reused grant ends", granted)
	}
}

// TestDefaultAliveNeedsPIDAndItsStartTime checks the Alive a Config without one gets: a process is alive while it runs
// with the start time /proc records for it, and a child that has been waited for is not alive with its start time.
func TestDefaultAliveNeedsPIDAndItsStartTime(t *testing.T) {
	b := NewBroker(Config{})
	self, selfStart := os.Getpid(), startTimeOf(t, os.Getpid())
	if !b.alive(self, selfStart) {
		t.Fatal("this process reported dead")
	}
	if b.alive(self, selfStart+1) {
		t.Fatal("this process reported alive with another start time")
	}
	child := exec.Command(os.Args[0], "-test.run=^$")
	if err := child.Start(); err != nil {
		t.Fatalf("start child: %v", err)
	}
	childStart := startTimeOf(t, child.Process.Pid)
	if err := child.Wait(); err != nil {
		t.Fatalf("run child: %v", err)
	}
	if b.alive(child.Process.Pid, childStart) {
		t.Fatalf("reaped child %d reported alive", child.Process.Pid)
	}
}
