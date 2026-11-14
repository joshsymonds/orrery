package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/rpcretry"
)

// holdAnswer is how a scripted Pools server answers one request. A cut answer drops the connection once the request is
// read, so the client sees a connection cut mid-call. Otherwise err is the Connect error the call returns, and nil is
// success. The zero holdAnswer is success.
type holdAnswer struct {
	cut bool
	err error
}

// answerKey carries the answer a request was given from the middleware to the Pools handler.
type answerKey struct{}

// scriptedPools is an in-process Pools service that answers requests in order from script. Once script is used up,
// each request gets tail.
type scriptedPools struct {
	enginev1connect.UnimplementedPoolsHandler

	mu       sync.Mutex
	script   []holdAnswer
	tail     holdAnswer
	attempts int      // requests that reached the server, cut or answered
	calls    []string // calls the server answered, as "Hold MEMBER" or "Free MEMBER"
}

// arrive counts a request that reached the server and returns the answer it gets.
func (f *scriptedPools) arrive() holdAnswer {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attempts++
	if len(f.script) == 0 {
		return f.tail
	}
	ans := f.script[0]
	f.script = f.script[1:]
	return ans
}

// answer records call and returns the error ctx's request is answered with.
func (f *scriptedPools) answer(ctx context.Context, call string) error {
	f.mu.Lock()
	f.calls = append(f.calls, call)
	f.mu.Unlock()
	ans, _ := ctx.Value(answerKey{}).(holdAnswer)
	return ans.err
}

// received returns the calls the server answered, in order.
func (f *scriptedPools) received() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

// requests returns how many requests reached the server, cut or answered.
func (f *scriptedPools) requests() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.attempts
}

// Hold implements enginev1connect.PoolsHandler.
func (f *scriptedPools) Hold(
	ctx context.Context,
	req *connect.Request[v1.PoolsHoldRequest],
) (*connect.Response[v1.PoolsHoldResponse], error) {
	if err := f.answer(ctx, "Hold "+req.Msg.GetMember()); err != nil {
		return nil, err
	}
	return connect.NewResponse(&v1.PoolsHoldResponse{}), nil
}

// Free implements enginev1connect.PoolsHandler.
func (f *scriptedPools) Free(
	ctx context.Context,
	req *connect.Request[v1.PoolsFreeRequest],
) (*connect.Response[v1.PoolsFreeResponse], error) {
	if err := f.answer(ctx, "Free "+req.Msg.GetMember()); err != nil {
		return nil, err
	}
	return connect.NewResponse(&v1.PoolsFreeResponse{}), nil
}

// serveScriptedPools serves fake as Pools on the session socket beside the daemon socket at daemon. The server is
// closed at cleanup.
func serveScriptedPools(t *testing.T, daemon string, fake *scriptedPools) {
	t.Helper()
	ln, err := net.Listen("unix", protocol.SessionSocketPath(daemon))
	if err != nil {
		t.Fatalf("listen on the session socket: %v", err)
	}
	mux := http.NewServeMux()
	path, handler := enginev1connect.NewPoolsHandler(fake)
	mux.Handle(path, handler)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ans := fake.arrive()
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request: %v", err)
			return
		}
		if ans.cut {
			conn, _, err := http.NewResponseController(w).Hijack()
			if err != nil {
				t.Errorf("hijack: %v", err)
				return
			}
			_ = conn.Close()
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		mux.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), answerKey{}, ans)))
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
}

// holdClock is the rpcretry.Clock of a hold test. Its time moves only when a retry waits: each wait runs beforeWait when
// it is set, then advances the time by the wait.
type holdClock struct {
	now        time.Time
	waits      []time.Duration
	beforeWait func()
}

// newHoldClock returns a holdClock at the Unix epoch.
func newHoldClock() *holdClock {
	return &holdClock{now: time.Unix(0, 0)}
}

// Now returns the clock's time.
func (c *holdClock) Now() time.Time {
	return c.now
}

// After records the wait, advances the time by it, and returns a channel that has already fired.
func (c *holdClock) After(d time.Duration) <-chan time.Time {
	if c.beforeWait != nil {
		c.beforeWait()
	}
	c.waits = append(c.waits, d)
	c.now = c.now.Add(d)
	fired := make(chan time.Time, 1)
	fired <- c.now
	return fired
}

// runHoldRetry runs `orrery hold` with args against the daemon socket on clock. It returns the exit code main would
// report and what was written to stderr.
func runHoldRetry(t *testing.T, clock rpcretry.Clock, daemon string, args ...string) (int, string) {
	t.Helper()
	var stderr bytes.Buffer
	err := runHoldWith(
		t.Context(),
		clock,
		globals{daemonSocket: daemon},
		args,
		&stderr,
		func(string) string { return "" },
	)
	return report(&stderr, err), stderr.String()
}

// connectErr returns a Connect error with code and message.
func connectErr(code connect.Code, message string) error {
	return connect.NewError(code, errors.New(message))
}

// notHeldReply is the message orreryd sends when a Free names a member no hold keeps.
const notHeldReply = `free member "builds-1": member not held`

// TestHoldRetriesUnavailableThenHolds answers the first Hold Unavailable. The retry is held, so orrery hold exits 0
// after one wait and writes nothing.
func TestHoldRetriesUnavailableThenHolds(t *testing.T) {
	fake := &scriptedPools{script: []holdAnswer{{err: connectErr(connect.CodeUnavailable, "orreryd restarting")}}}
	daemon := filepath.Join(t.TempDir(), "orreryd.sock")
	serveScriptedPools(t, daemon, fake)
	clock := newHoldClock()
	code, stderr := runHoldRetry(t, clock, daemon, "builds-1")
	if code != exitOK {
		t.Fatalf("exit status %d, want %d; stderr %q", code, exitOK, stderr)
	}
	want := []string{"Hold builds-1", "Hold builds-1"}
	if got := fake.received(); !slices.Equal(got, want) {
		t.Errorf("orreryd received %q, want %q", got, want)
	}
	if len(clock.waits) != 1 {
		t.Errorf("waits = %v, want one wait before the retry", clock.waits)
	}
	if stderr != "" {
		t.Errorf("hold wrote stderr %q, want nothing", stderr)
	}
}

// TestHoldRetriesCutThenHolds cuts the first Hold mid-call. A hold is idempotent, so the retry is sent and holds.
func TestHoldRetriesCutThenHolds(t *testing.T) {
	fake := &scriptedPools{script: []holdAnswer{{cut: true}}}
	daemon := filepath.Join(t.TempDir(), "orreryd.sock")
	serveScriptedPools(t, daemon, fake)
	code, stderr := runHoldRetry(t, newHoldClock(), daemon, "builds-1")
	if code != exitOK {
		t.Fatalf("exit status %d, want %d; stderr %q", code, exitOK, stderr)
	}
	if got := fake.requests(); got != 2 {
		t.Errorf("orreryd saw %d requests, want the cut and its retry", got)
	}
	if got := fake.received(); !slices.Equal(got, []string{"Hold builds-1"}) {
		t.Errorf("orreryd answered %q, want [Hold builds-1]", got)
	}
}

// TestHoldOffCutThenNotHeldExitsZero cuts the first Free mid-call. The retry hears the member is not held, so the
// earlier attempt freed it: --off exits 0 and writes nothing.
func TestHoldOffCutThenNotHeldExitsZero(t *testing.T) {
	fake := &scriptedPools{script: []holdAnswer{
		{cut: true},
		{err: connectErr(connect.CodeNotFound, notHeldReply)},
	}}
	daemon := filepath.Join(t.TempDir(), "orreryd.sock")
	serveScriptedPools(t, daemon, fake)
	clock := newHoldClock()
	code, stderr := runHoldRetry(t, clock, daemon, "--off", "builds-1")
	if code != exitOK {
		t.Fatalf("exit status %d, want %d; stderr %q", code, exitOK, stderr)
	}
	if got := fake.requests(); got != 2 {
		t.Errorf("orreryd saw %d requests, want the cut and its retry", got)
	}
	if got := fake.received(); !slices.Equal(got, []string{"Free builds-1"}) {
		t.Errorf("orreryd answered %q, want [Free builds-1]", got)
	}
	if stderr != "" {
		t.Errorf("hold --off wrote stderr %q, want nothing", stderr)
	}
}

// TestHoldOffCutThenOtherNotFoundStillFails cuts the first Free mid-call. A retry that is refused not_found for a reason
// other than "member not held" is not a free, so --off exits 1 with the reason.
func TestHoldOffCutThenOtherNotFoundStillFails(t *testing.T) {
	fake := &scriptedPools{script: []holdAnswer{
		{cut: true},
		{err: connectErr(connect.CodeNotFound, "no such grant")},
	}}
	daemon := filepath.Join(t.TempDir(), "orreryd.sock")
	serveScriptedPools(t, daemon, fake)
	code, stderr := runHoldRetry(t, newHoldClock(), daemon, "--off", "builds-1")
	if code != exitFailure {
		t.Errorf("exit status %d, want %d; stderr %q", code, exitFailure, stderr)
	}
	if got := fake.received(); !slices.Equal(got, []string{"Free builds-1"}) {
		t.Errorf("orreryd answered %q, want [Free builds-1]", got)
	}
}

// TestHoldOffNotHeldAtFirstAttemptStillFails refuses the first Free as not held. Nothing was cut, so --off exits 1 with
// orreryd's reason and does not retry.
func TestHoldOffNotHeldAtFirstAttemptStillFails(t *testing.T) {
	fake := &scriptedPools{script: []holdAnswer{{err: connectErr(connect.CodeNotFound, notHeldReply)}}}
	daemon := filepath.Join(t.TempDir(), "orreryd.sock")
	serveScriptedPools(t, daemon, fake)
	clock := newHoldClock()
	code, stderr := runHoldRetry(t, clock, daemon, "--off", "builds-1")
	if code != exitFailure {
		t.Errorf("exit status %d, want %d; stderr %q", code, exitFailure, stderr)
	}
	if !strings.HasPrefix(stderr, "orrery hold: ") || !strings.Contains(stderr, "member not held") {
		t.Errorf("stderr %q, want orreryd's reason prefixed with orrery hold:", stderr)
	}
	if got := fake.received(); !slices.Equal(got, []string{"Free builds-1"}) {
		t.Errorf("orreryd answered %q, want [Free builds-1]", got)
	}
	if len(clock.waits) != 0 {
		t.Errorf("waits = %v, want no retry", clock.waits)
	}
}

// TestHoldOffRefusedAtDialThenNotHeldStillFails finds no orreryd on the first attempt, so that attempt never connects.
// orreryd comes up during the wait and says the member is not held. Nothing was cut, so the member was not freed by
// this command: --off exits 1.
func TestHoldOffRefusedAtDialThenNotHeldStillFails(t *testing.T) {
	daemon := filepath.Join(t.TempDir(), "orreryd.sock")
	fake := &scriptedPools{script: []holdAnswer{{err: connectErr(connect.CodeNotFound, notHeldReply)}}}
	clock := newHoldClock()
	clock.beforeWait = func() { serveScriptedPools(t, daemon, fake) }
	code, stderr := runHoldRetry(t, clock, daemon, "--off", "builds-1")
	if code != exitFailure {
		t.Errorf("exit status %d, want %d; stderr %q", code, exitFailure, stderr)
	}
	if got := fake.received(); !slices.Equal(got, []string{"Free builds-1"}) {
		t.Errorf("orreryd answered %q, want [Free builds-1]", got)
	}
}

// TestHoldGivesUpAfterWindow answers every hold Unavailable. The retries stop when rpcretry.Window has passed on the
// clock, and hold exits 1 with the reason.
func TestHoldGivesUpAfterWindow(t *testing.T) {
	fake := &scriptedPools{tail: holdAnswer{err: connectErr(connect.CodeUnavailable, "orreryd restarting")}}
	daemon := filepath.Join(t.TempDir(), "orreryd.sock")
	serveScriptedPools(t, daemon, fake)
	clock := newHoldClock()
	start := clock.Now()
	code, stderr := runHoldRetry(t, clock, daemon, "builds-1")
	if code != exitFailure {
		t.Errorf("exit status %d, want %d; stderr %q", code, exitFailure, stderr)
	}
	if got := clock.Now().Sub(start); got != rpcretry.Window {
		t.Errorf("hold gave up after %v on the clock, want exactly Window %v", got, rpcretry.Window)
	}
	if fake.requests() < 2 {
		t.Errorf("orreryd saw %d requests, want retries before the window ends", fake.requests())
	}
}
