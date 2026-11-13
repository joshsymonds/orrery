package rpcretry_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/joshsymonds/orrery/internal/rpcretry"
)

const procedure = "/orrery.test.Ping/Ping"

// pingClient returns a Connect client whose every connection dials the unix socket at socketPath.
func pingClient(socketPath string) *connect.Client[emptypb.Empty, emptypb.Empty] {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, "unix", socketPath)
		},
	}
	return connect.NewClient[emptypb.Empty, emptypb.Empty](
		&http.Client{Transport: transport}, "http://orrery"+procedure)
}

// ping makes one Ping call over the unix socket at socketPath.
func ping(ctx context.Context, socketPath string) error {
	_, err := pingClient(socketPath).CallUnary(ctx, connect.NewRequest(&emptypb.Empty{}))
	return err
}

// serve listens on a unix socket in a new directory and serves handler until the test ends. It returns the socket path.
func serve(t *testing.T, handler http.Handler) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "orreryd.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := &http.Server{Handler: handler}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	return path
}

// flakyServer reads each request and then drops its connection for the first cuts requests. It answers every later
// request with an empty success.
func flakyServer(t *testing.T, cuts int32) http.Handler {
	t.Helper()
	var hits atomic.Int32
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			t.Errorf("read request: %v", err)
			return
		}
		if hits.Add(1) <= cuts {
			conn, _, err := http.NewResponseController(w).Hijack()
			if err != nil {
				t.Errorf("hijack: %v", err)
				return
			}
			_ = conn.Close()
			return
		}
		w.Header().Set("Content-Type", "application/proto")
	})
}

// refusedSocket returns the path of a unix socket file that nothing listens on, so a dial to it is refused.
func refusedSocket(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "stale.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	listener.SetUnlinkOnClose(false)
	if err := listener.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}
	return path
}

func TestDialFailuresAreNeverConnectedAndUnavailable(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.sock")
	for _, socket := range []string{missing, refusedSocket(t)} {
		err := ping(t.Context(), socket)
		if err == nil {
			t.Fatalf("ping on %s succeeded, want a dial failure", socket)
		}
		if !rpcretry.NeverConnected(err) {
			t.Errorf("NeverConnected(%v) = false on %s, want true", err, socket)
		}
		if !rpcretry.Unavailable(err) {
			t.Errorf("Unavailable(%v) = false on %s, want true", err, socket)
		}
	}
}

func TestCutMidCallIsUnavailableNotNeverConnected(t *testing.T) {
	socket := serve(t, flakyServer(t, 1))
	err := ping(t.Context(), socket)
	if err == nil {
		t.Fatal("ping succeeded, want the connection cut mid-call")
	}
	if !rpcretry.Unavailable(err) {
		t.Errorf("Unavailable(%v) = false, want true", err)
	}
	if rpcretry.NeverConnected(err) {
		t.Errorf("NeverConnected(%v) = true, want false: the connection was made", err)
	}
}

// fakeClock is a Clock whose time moves only when Do waits: each After call advances it by the wait.
type fakeClock struct {
	now   time.Time
	waits []time.Duration
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Unix(0, 0)}
}

func (c *fakeClock) Now() time.Time {
	return c.now
}

func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	c.waits = append(c.waits, d)
	c.now = c.now.Add(d)
	fired := make(chan time.Time, 1)
	fired <- c.now
	return fired
}

// stallingClock cancels the context Do waits on, and its timers never fire.
type stallingClock struct {
	cancel context.CancelFunc
}

func (c stallingClock) Now() time.Time {
	return time.Time{}
}

func (c stallingClock) After(time.Duration) <-chan time.Time {
	c.cancel()
	return make(chan time.Time)
}

func unavailableErr(n int) error {
	return connect.NewError(connect.CodeUnavailable, fmt.Errorf("attempt %d", n))
}

func TestDoRetriesUntilSuccess(t *testing.T) {
	socket := serve(t, flakyServer(t, 2))
	clock := newFakeClock()
	calls := 0
	err := rpcretry.Do(t.Context(), clock, rpcretry.Unavailable, func(ctx context.Context) error {
		calls++
		return ping(ctx, socket)
	})
	if err != nil {
		t.Fatalf("Do = %v, want nil after the cuts", err)
	}
	if calls != 3 {
		t.Errorf("calls = %d, want 3", calls)
	}
	if len(clock.waits) != calls-1 {
		t.Fatalf("waits = %v, want one wait between each pair of calls", clock.waits)
	}
	if clock.waits[1] <= clock.waits[0] {
		t.Errorf("backoff did not grow: waits = %v", clock.waits)
	}
}

func TestDoStopsAtWindowWithLastError(t *testing.T) {
	clock := newFakeClock()
	start := clock.Now()
	var last error
	calls := 0
	err := rpcretry.Do(t.Context(), clock, rpcretry.Unavailable, func(context.Context) error {
		calls++
		last = unavailableErr(calls)
		return last
	})
	if !errors.Is(err, last) {
		t.Errorf("Do = %v, want the last call's error %v", err, last)
	}
	if calls < 2 {
		t.Errorf("calls = %d, want retries before the window ends", calls)
	}
	if got := clock.Now().Sub(start); got != rpcretry.Window {
		t.Errorf("Do returned after %v on the clock, want exactly Window %v", got, rpcretry.Window)
	}
}

func TestDoNeverRetriesErrorThePredicateRefuses(t *testing.T) {
	clock := newFakeClock()
	refused := errors.New("bad request")
	calls := 0
	err := rpcretry.Do(t.Context(), clock, rpcretry.Unavailable, func(context.Context) error {
		calls++
		return refused
	})
	if !errors.Is(err, refused) {
		t.Errorf("Do = %v, want %v", err, refused)
	}
	if calls != 1 || len(clock.waits) != 0 {
		t.Errorf("calls = %d, waits = %v; want one call and no wait", calls, clock.waits)
	}
}

func TestDoNeverRetriesMidCallCutWhenPredicateIsNeverConnected(t *testing.T) {
	socket := serve(t, flakyServer(t, 1))
	clock := newFakeClock()
	calls := 0
	err := rpcretry.Do(t.Context(), clock, rpcretry.NeverConnected, func(ctx context.Context) error {
		calls++
		return ping(ctx, socket)
	})
	if err == nil || !rpcretry.Unavailable(err) {
		t.Fatalf("Do = %v, want the mid-call Unavailable error", err)
	}
	if calls != 1 || len(clock.waits) != 0 {
		t.Errorf("calls = %d, waits = %v; want one call and no wait", calls, clock.waits)
	}
}

func TestDoReturnsWhenContextEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	calls := 0
	err := rpcretry.Do(ctx, stallingClock{cancel: cancel}, rpcretry.Unavailable, func(context.Context) error {
		calls++
		return unavailableErr(calls)
	})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Do = %v, want context.Canceled", err)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1 before the context ended", calls)
	}
}
