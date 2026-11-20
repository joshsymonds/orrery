package grants

import (
	"context"
	"errors"
	"log/slog"
	"os/exec"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// exitWatch is a fake exit watch: exited closes when the test signals the exit, and stopped records that the broker
// stopped the watch.
type exitWatch struct {
	exited  chan struct{}
	stopped bool
}

// exitFakes stands in for the exit watches of a test broker. Each watch the broker starts is kept under its key, the
// client pid or the holder directory it watches, so the test signals it and reads whether the broker stopped it. The
// broker makes each client check under its lock and sends on checks first, so a test that receives from checks and then
// takes the lock waits for that check to end.
type exitFakes struct {
	mu      sync.Mutex
	watches map[string]*exitWatch
	checks  chan struct{}
}

// newExitFakes returns exitFakes with no watch started.
func newExitFakes() *exitFakes {
	return &exitFakes{watches: make(map[string]*exitWatch), checks: make(chan struct{}, 64)}
}

// clientKey is the key of the watch of the client pid, and holderKey the key of the watch of the holder dir.
func clientKey(pid int) string { return "client " + strconv.Itoa(pid) }

func holderKey(dir string) string { return "holder " + dir }

// start records a new watch under key. It returns the watch's exited channel and its stop, which records the stop.
func (f *exitFakes) start(key string) (<-chan struct{}, func()) {
	w := &exitWatch{exited: make(chan struct{})}
	f.mu.Lock()
	f.watches[key] = w
	f.mu.Unlock()
	return w.exited, func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		w.stopped = true
	}
}

// watchClient is the WatchClient of the fakes, and watchHolder their WatchHolder.
func (f *exitFakes) watchClient(pid int, _ int64) (<-chan struct{}, func()) {
	return f.start(clientKey(pid))
}

func (f *exitFakes) watchHolder(dir string) (<-chan struct{}, func()) {
	return f.start(holderKey(dir))
}

// signal signals the exit of the watch under key, which the test must have started.
func (f *exitFakes) signal(t *testing.T, key string) {
	t.Helper()
	f.mu.Lock()
	w, ok := f.watches[key]
	f.mu.Unlock()
	if !ok {
		t.Fatalf("no exit watch of %s", key)
	}
	close(w.exited)
}

// stopped reports whether the broker stopped the watch under key, which the test must have started.
func (f *exitFakes) stopped(t *testing.T, key string) bool {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	w, ok := f.watches[key]
	if !ok {
		t.Fatalf("no exit watch of %s", key)
	}
	return w.stopped
}

// probed returns alive with a send on checks before each call.
func (f *exitFakes) probed(alive func(int, int64) bool) func(int, int64) bool {
	return func(pid int, start int64) bool {
		f.checks <- struct{}{}
		return alive(pid, start)
	}
}

// awaitCheck waits until the broker starts a client check. The check runs under the broker's lock, so a call of the
// broker that takes the lock after awaitCheck returns only once the check has ended.
func (f *exitFakes) awaitCheck() {
	<-f.checks
}

// fakeClient is the liveness of the one client a wake test has: its pid is alive until the test kills it.
type fakeClient struct {
	alive atomic.Bool
}

// newFakeClient returns a fakeClient that is alive.
func newFakeClient() *fakeClient {
	c := &fakeClient{}
	c.alive.Store(true)
	return c
}

// isAlive is the Alive of the broker, and kill makes the client dead.
func (c *fakeClient) isAlive(int, int64) bool {
	return c.alive.Load()
}

func (c *fakeClient) kill() {
	c.alive.Store(false)
}

// brokerWatched returns a broker on s whose clients are alive as alive reports, whose holders are alive as holderAlive
// reports, and whose exit watches are the fakes in exits. A nil alive means processAlive. A watch the test never
// signals leaves its entry as it is, so a test whose clients are fake pids can use newExitFakes for a broker that never
// ends them. The broker is closed when the test ends.
func brokerWatched(
	t *testing.T, s *store.Store, alive func(int, int64) bool, holderAlive func(string) bool, exits *exitFakes,
) *Broker {
	t.Helper()
	if alive == nil {
		alive = processAlive
	}
	b := NewBroker(Config{
		Host: testHost, Store: s, Environ: commandEnv(), Now: fixedNow, Logger: slog.New(slog.DiscardHandler),
		Alive: exits.probed(alive), HolderAlive: holderAlive,
		WatchClient: exits.watchClient, WatchHolder: exits.watchHolder,
	})
	t.Cleanup(b.Close)
	return b
}

// TestBrokerWakeEndsDetachedGrantWhenItsClientExits checks that a grant a restart kept ends as soon as its client's
// watch signals and the client is gone. Its member goes to the request waiting for it, and no tick is sent.
func TestBrokerWakeEndsDetachedGrantWhenItsClientExits(t *testing.T) {
	s := openStore(t)
	p := awakePool(t, "p", 1, "m1")
	before := brokerOn(t, s, nil)
	live, liveCall := grantTo(t, before, p, "a", livePID)
	closeWithBlockedCalls(t, before, liveCall)

	client := newFakeClient()
	exits := newExitFakes()
	after := brokerWatched(t, s, client.isAlive, nil, exits)
	if err := after.Restore(t.Context()); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	waiter := start(t.Context(), after, request("b", p, 1))
	if e := waiter.next(t); e.Position != 1 {
		t.Fatalf("waiter event = %+v, want position 1", e)
	}

	client.kill()
	exits.signal(t, clientKey(livePID))
	granted := waiter.next(t).Grant
	if granted == nil || !slices.Equal(granted.Members, []Member{{Pool: "p", Name: "m1"}}) {
		t.Fatalf("waiter got %+v, want m1, the member of the client that exited (grant %s)", granted, live.ID)
	}
	if !exits.stopped(t, clientKey(livePID)) {
		t.Fatal("the client watch of the ended grant still runs")
	}
	if rows := storedGrants(t, s); len(rows) != 1 || rows[0].ID != granted.ID {
		t.Fatalf("stored grants = %+v, want only the waiter's grant %s", rows, granted.ID)
	}
}

// TestBrokerWakeEndsRequeuedRequestWhenItsClientExits checks that a requeued request ends as soon as its client's watch
// signals and the client is gone. The request queued behind it is then granted, since the requeued request is no
// longer ahead of it.
func TestBrokerWakeEndsRequeuedRequestWhenItsClientExits(t *testing.T) {
	s := openStore(t)
	p := awakePool(t, "p", 1, "m1")
	client := newFakeClient()
	exits := newExitFakes()
	b := brokerWatched(t, s, client.isAlive, nil, exits)
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
	if err := b.Free(t.Context(), "m1"); err != nil {
		t.Fatalf("Free: %v", err)
	}
	behind := start(t.Context(), b, request("b", p, 1))
	if e := behind.next(t); e.Position != 2 {
		t.Fatalf("behind event = %+v, want position 2, behind the requeued request", e)
	}
	if got := queuedProjects(b); !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("queued = %v while the client lives, want the requeued request ahead of the one behind it", got)
	}

	client.kill()
	exits.signal(t, clientKey(livePID))
	granted := behind.next(t).Grant
	if granted == nil || !slices.Equal(granted.Members, []Member{{Pool: "p", Name: "m1"}}) {
		t.Fatalf("behind got %+v, want m1 once the requeued request of the exited client ends", granted)
	}
	if got := queuedProjects(b); len(got) != 0 {
		t.Fatalf("queued = %v after the grant, want none", got)
	}
	if !exits.stopped(t, clientKey(livePID)) {
		t.Fatal("the client watch of the ended request still runs")
	}
	reclaim := start(t.Context(), b, Request{Project: "a", ClientPID: livePID, GrantID: grant.ID})
	if err := reclaim.finish(t); !errors.Is(err, ErrNoGrant) {
		t.Fatalf("reclaim of the ended request = %v, want ErrNoGrant", err)
	}
	if rows := storedGrants(t, s); len(rows) != 1 || rows[0].ID != granted.ID {
		t.Fatalf("stored grants = %+v, want only the behind request's grant %s", rows, granted.ID)
	}
}

// TestBrokerWakeEndsGrantOnlyOnceItsHolderExits checks a kept grant that names a holder. Its client's watch signals
// while the holder lives, and nothing ends. Once the holder's watch signals and the holder is gone, the grant ends.
func TestBrokerWakeEndsGrantOnlyOnceItsHolderExits(t *testing.T) {
	s := openStore(t)
	p := awakePool(t, "p", 1, "m1")
	before := brokerOn(t, s, nil)
	held, heldCall := grantHeldTo(t, before, p, "a", livePID, holderOne)
	closeWithBlockedCalls(t, before, heldCall)

	client := newFakeClient()
	holders := &holderSet{live: map[string]bool{holderOne: true}}
	exits := newExitFakes()
	after := brokerWatched(t, s, client.isAlive, holders.alive, exits)
	if err := after.Restore(t.Context()); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	exits.awaitCheck()
	waiter := start(t.Context(), after, request("b", p, 1))
	if e := waiter.next(t); e.Position != 1 {
		t.Fatalf("waiter event = %+v, want position 1", e)
	}

	client.kill()
	exits.signal(t, clientKey(livePID))
	exits.awaitCheck()
	if _, grant := memberOf(t, after, "p", "m1"); grant != held.ID {
		t.Fatalf("m1 held by %q after the client's exit, want the grant %s its live holder keeps", grant, held.ID)
	}
	if rows := storedGrants(t, s); len(rows) != 1 || rows[0].ID != held.ID {
		t.Fatalf("stored grants after the client's exit = %+v, want only grant %s", rows, held.ID)
	}

	holders.setAlive(holderOne, false)
	exits.signal(t, holderKey(holderOne))
	granted := waiter.next(t).Grant
	if granted == nil || !slices.Equal(granted.Members, []Member{{Pool: "p", Name: "m1"}}) {
		t.Fatalf("waiter got %+v, want m1 once the holder is gone", granted)
	}
	if !exits.stopped(t, holderKey(holderOne)) {
		t.Fatal("the holder watch of the ended grant still runs")
	}
	if rows := storedGrants(t, s); len(rows) != 1 || rows[0].ID != granted.ID {
		t.Fatalf("stored grants = %+v, want only the waiter's grant %s", rows, granted.ID)
	}
}

// TestBrokerWakeClientWatchSignalsWhenTheProcessExits checks the default client watch. It stays open while a process
// runs with the start time it is given, and closes once that process is killed.
func TestBrokerWakeClientWatchSignalsWhenTheProcessExits(t *testing.T) {
	child := exec.Command("sleep", "60")
	if err := child.Start(); err != nil {
		t.Fatalf("start child: %v", err)
	}
	t.Cleanup(func() {
		// The child is killed, so Wait reports its signal, which the test does not check.
		_ = child.Process.Kill()
		_ = child.Wait()
	})
	exited, stop := clientExitWatch(slog.New(slog.DiscardHandler), child.Process.Pid, startTimeOf(t, child.Process.Pid))
	defer stop()
	select {
	case <-exited:
		t.Fatal("the client watch of a running process signaled")
	default:
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatalf("kill child: %v", err)
	}
	<-exited
}
