package node

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/holder"
	"github.com/joshsymonds/orrery/internal/holderhost"
)

// testBound is the Bound of the Clients in these tests. Each test passes it on a manualClock, so nothing waits on it.
const testBound = 3 * time.Second

// manualClock is the Clock of these tests. Its timers fire only when a test fires them, so the test sees each wait a
// Client makes on the clock and decides when it ends.
type manualClock struct {
	mu sync.Mutex
	// pending holds the timers that are set and not yet claimed by await.
	pending []*manualTimer
	// set receives a signal at each After, so an await that found no timer looks again.
	set chan struct{}
}

// manualTimer is a timer of a manualClock. It has the duration it was set for, and fires once.
type manualTimer struct {
	d time.Duration
	c chan time.Time
}

// newManualClock returns a manualClock with no timers set.
func newManualClock() *manualClock {
	return &manualClock{set: make(chan struct{}, 1)}
}

// After sets a timer of duration d, and returns the channel it fires on.
func (c *manualClock) After(d time.Duration) <-chan time.Time {
	t := &manualTimer{d: d, c: make(chan time.Time, 1)}
	c.mu.Lock()
	c.pending = append(c.pending, t)
	c.mu.Unlock()
	select {
	case c.set <- struct{}{}:
	default:
	}
	return t.c
}

// await waits until a timer of duration d is set and not yet claimed. It claims that timer and returns it.
func (c *manualClock) await(d time.Duration) *manualTimer {
	for {
		c.mu.Lock()
		for i, t := range c.pending {
			if t.d == d {
				c.pending = slices.Delete(c.pending, i, i+1)
				c.mu.Unlock()
				return t
			}
		}
		c.mu.Unlock()
		<-c.set
	}
}

// fire fires the timer, so that its channel receives.
func (t *manualTimer) fire() {
	t.c <- time.Time{}
}

// routedDialer dials the address its target names. A test retargets it while the Client that dials through it runs.
type routedDialer struct {
	mu     sync.Mutex
	target string
}

// DialContext dials the current target, whatever address the Client names.
func (r *routedDialer) DialContext(ctx context.Context, network, _ string) (net.Conn, error) {
	r.mu.Lock()
	target := r.target
	r.mu.Unlock()
	conn, err := (&net.Dialer{}).DialContext(ctx, network, target)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", target, err)
	}
	return conn, nil
}

// retarget makes addr the address the next dial reaches.
func (r *routedDialer) retarget(addr string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.target = addr
}

// refusedAddr returns a loopback address with no listener on it, so that a dial to it is refused.
func refusedAddr(t *testing.T) string {
	t.Helper()
	ln := listenLoopback(t)
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	return addr
}

// silentAgent listens on loopback and accepts each connection without answering it. It reports each accepted connection
// on accepted, and each connection's close on closed, which is the error of reading to the end of it and closing it.
func silentAgent(t *testing.T) (string, <-chan struct{}, <-chan error) {
	t.Helper()
	ln := listenLoopback(t)
	t.Cleanup(func() {
		if err := ln.Close(); err != nil {
			t.Errorf("close the silent agent: %v", err)
		}
	})
	accepted := make(chan struct{}, 1)
	closed := make(chan error, 1)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			accepted <- struct{}{}
			go func() {
				_, readErr := io.Copy(io.Discard, conn)
				closed <- errors.Join(readErr, conn.Close())
			}()
		}
	}()
	return ln.Addr().String(), accepted, closed
}

// awaitUnreachable reads client's Reach, and waits for it to read unreachable once the clock passes Bound. It fails when
// Reach reads unreachable before then.
func awaitUnreachable(t *testing.T, client *Client, clock *manualClock) {
	t.Helper()
	reachable, changed := client.Reach()
	if !reachable {
		t.Fatal("Reach reads unreachable before the clock passes Bound")
	}
	clock.await(testBound).fire()
	<-changed
	if reachable, _ = client.Reach(); reachable {
		t.Fatal("Reach reads reachable once the clock passes Bound with no answer")
	}
}

// TestNodeClientRunsSessionOverAgent runs a session through a Client over a real agent. The Client launches the session,
// attaches to it at an offset, stops it, reads its spool and exit record, and clears it.
func TestNodeClientRunsSessionOverAgent(t *testing.T) {
	local, stateDir := nodeLocal(t)
	addr, _ := startNodeAgent(t, local, stateDir)
	client := NewClient(ClientConfig{
		Name:    "remote",
		Address: addr,
		Dial:    (&net.Dialer{}).DialContext,
		Bound:   testBound,
		Clock:   newManualClock(),
	})
	ctx := t.Context()

	launched, err := client.Launch(ctx, holderhost.Spec{Session: "s1", Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	// The launch's controller is detached, so that the reattach below is the holder's one controller.
	if err = launched.Detach(); err != nil {
		t.Fatalf("detach the launched session: %v", err)
	}
	process, _, err := client.Reattach(ctx, "s1", int64(len(firstLine)), holder.WireVersion)
	if err != nil {
		t.Fatalf("reattach at offset %d: %v", len(firstLine), err)
	}
	out := make([]byte, len(secondLine))
	if _, err = io.ReadFull(process.Stdout(), out); err != nil {
		t.Fatalf("read stdout from offset %d: %v", len(firstLine), err)
	}
	if got := string(out); got != secondLine {
		t.Fatalf("stdout from offset %d = %q, want %q", len(firstLine), got, secondLine)
	}

	ids, err := client.Sessions(ctx)
	if err != nil {
		t.Fatalf("sessions: %v", err)
	}
	if !slices.Equal(ids, []string{"s1"}) {
		t.Fatalf("sessions = %v, want [s1]", ids)
	}
	if err = client.Stop(ctx, "s1"); err != nil {
		t.Fatalf("stop: %v", err)
	}
	gone, err := client.OpenGone(ctx, "s1", 0)
	if err != nil {
		t.Fatalf("open gone: %v", err)
	}
	exit, found := gone.Exit()
	if !found || !exitStopped(&exit) {
		t.Fatalf("gone exit = %+v, found %t, want the stopped exit record", exit, found)
	}
	spool, err := io.ReadAll(gone.Stdout())
	if err != nil {
		t.Fatalf("read the spool: %v", err)
	}
	if got, want := string(spool), firstLine+secondLine; got != want {
		t.Fatalf("spool = %q, want %q", got, want)
	}
	if err = client.Clear(ctx, "s1"); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if ids, err = client.Sessions(ctx); err != nil {
		t.Fatalf("sessions after clear: %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("sessions after clear = %v, want none", ids)
	}
}

// TestNodeClientUnknownSessionIsGone has a Client ask its agent for ops on a session the agent never held. Each op fails
// with the error that holder.ErrGone matches, as Local's ops do for a session that has ended.
func TestNodeClientUnknownSessionIsGone(t *testing.T) {
	local, stateDir := nodeLocal(t)
	addr, _ := startNodeAgent(t, local, stateDir)
	client := NewClient(ClientConfig{
		Name:    "remote",
		Address: addr,
		Dial:    (&net.Dialer{}).DialContext,
		Bound:   testBound,
		Clock:   newManualClock(),
	})
	ctx := t.Context()
	for _, tc := range []struct {
		name string
		op   func() error
	}{
		{name: "stop", op: func() error { return client.Stop(ctx, "unknown") }},
		{name: "kill", op: func() error { return client.Kill(ctx, "unknown") }},
		{name: "clear", op: func() error { return client.Clear(ctx, "unknown") }},
		{name: "stop orphan", op: func() error { return client.StopOrphan(ctx, "unknown") }},
		{name: "open gone", op: func() error {
			_, err := client.OpenGone(ctx, "unknown", 0)
			return err
		}},
		{name: "reattach", op: func() error {
			_, _, err := client.Reattach(ctx, "unknown", 0, holder.WireVersion)
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.op(); !errors.Is(err, holder.ErrGone) {
				t.Fatalf("%s of an unknown session = %v, want an error matching holder.ErrGone", tc.name, err)
			}
		})
	}
}

// TestNodeClientRefusedAddressIsUnreachable makes calls to an address that refuses every dial. Each call retries the dial
// until the clock passes Bound, then fails with an error that matches holderhost.ErrUnreachable. Reach then reads
// unreachable once the clock passes Bound.
func TestNodeClientRefusedAddressIsUnreachable(t *testing.T) {
	clock := newManualClock()
	client := NewClient(ClientConfig{
		Name:    "refused",
		Address: refusedAddr(t),
		Dial:    (&net.Dialer{}).DialContext,
		Bound:   testBound,
		Clock:   clock,
	})
	calls := []struct {
		name string
		call func(context.Context) error
	}{
		{name: "sessions", call: func(ctx context.Context) error {
			_, err := client.Sessions(ctx)
			return err
		}},
		{name: "stop", call: func(ctx context.Context) error { return client.Stop(ctx, "s1") }},
	}
	for _, tc := range calls {
		errs := make(chan error, 1)
		go func() { errs <- tc.call(t.Context()) }()
		// Each refused dial waits a retry on the clock. Two retries fired show the dial is retried, not given up.
		clock.await(testBound / retriesPerBound).fire()
		clock.await(testBound / retriesPerBound).fire()
		clock.await(testBound).fire()
		if err := <-errs; !errors.Is(err, holderhost.ErrUnreachable) {
			t.Fatalf("%s = %v, want an error matching holderhost.ErrUnreachable", tc.name, err)
		}
	}
	awaitUnreachable(t, client, clock)
}

// TestNodeClientSilentAgentIsUnreachable makes a call to an agent that accepts its connection and never answers. The
// call closes the connection once the clock passes Bound, and fails with an error that matches
// holderhost.ErrUnreachable. Reach then reads unreachable once the clock passes Bound.
func TestNodeClientSilentAgentIsUnreachable(t *testing.T) {
	addr, accepted, closed := silentAgent(t)
	clock := newManualClock()
	client := NewClient(ClientConfig{
		Name:    "silent",
		Address: addr,
		Dial:    (&net.Dialer{}).DialContext,
		Bound:   testBound,
		Clock:   clock,
	})
	errs := make(chan error, 1)
	go func() {
		_, err := client.Sessions(t.Context())
		errs <- err
	}()
	<-accepted
	clock.await(testBound).fire()
	if err := <-errs; !errors.Is(err, holderhost.ErrUnreachable) {
		t.Fatalf("sessions = %v, want an error matching holderhost.ErrUnreachable", err)
	}
	if err := <-closed; err != nil {
		t.Fatalf("the Client's close of the silent connection: %v", err)
	}
	awaitUnreachable(t, client, clock)
}

// TestNodeClientReachReadsReachableAgain has Reach read unreachable, then retargets the Client's dials to an agent that
// answers. The next ping is answered, and Reach reads reachable again.
func TestNodeClientReachReadsReachableAgain(t *testing.T) {
	local, stateDir := nodeLocal(t)
	addr, _ := startNodeAgent(t, local, stateDir)
	route := &routedDialer{target: refusedAddr(t)}
	clock := newManualClock()
	client := NewClient(ClientConfig{
		Name:    "remote",
		Address: addr,
		Dial:    route.DialContext,
		Bound:   testBound,
		Clock:   clock,
	})
	awaitUnreachable(t, client, clock)

	route.retarget(addr)
	_, changed := client.Reach()
	clock.await(testBound / pingsPerBound).fire()
	<-changed
	if reachable, _ := client.Reach(); !reachable {
		t.Fatal("Reach reads unreachable after the agent answered a ping")
	}
}
