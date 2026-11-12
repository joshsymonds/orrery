package main

import (
	"context"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/open"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/push"
	"github.com/joshsymonds/orrery/internal/server"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
	"github.com/joshsymonds/orrery/internal/webui"
)

const (
	serveTestLogin = "user@example.test"
	serveTestEvent = "orrery-1"
	// serveTestWait bounds every wait on the serve loop or the stream. It is a timeout, not a sleep: each wait returns
	// as soon as the awaited state is observed.
	serveTestWait = 10 * time.Second
)

// serveLoginTransport sends each request with the login and client headers the guard requires.
type serveLoginTransport struct {
	base http.RoundTripper
}

// RoundTrip sends a copy of req with the guard headers set.
func (t serveLoginTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header.Set(server.HeaderLogin, serveTestLogin)
	clone.Header.Set(server.HeaderClient, "synthetic-client")
	return t.base.RoundTrip(clone)
}

// TestServeEndsOpenStreamAtShutdown runs the serve loop on an ephemeral loopback listener over a store that holds
// events. A Watch stream opened after the cursor receives its first message. Canceling the serve context must end
// the stream with Unavailable, and the serve loop must return nil well within serveShutdownTimeout.
func TestServeEndsOpenStreamAtShutdown(t *testing.T) {
	ctx := t.Context()
	dbPath := filepath.Join(t.TempDir(), "orrery.db")
	writer, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := writer.Close(); err != nil {
			t.Errorf("Close store: %v", err)
		}
	})
	at := time.Unix(1_700_000_000, 0)
	if err := writer.SyncServer(ctx, "-L a", []store.Pane{{ID: serveTestEvent, Pane: "%1"}}, at); err != nil {
		t.Fatalf("SyncServer: %v", err)
	}
	busy := store.Pane{ID: serveTestEvent, Pane: "%1", Fields: store.Fields{State: "busy"}}
	if err := writer.SyncPane(ctx, "-L a", busy, at); err != nil {
		t.Fatalf("SyncPane: %v", err)
	}

	tmux, err := tmuxctl.New(tmuxctl.Server{Name: "orrery-serve-test-unstarted"})
	if err != nil {
		t.Fatalf("tmuxctl.New: %v", err)
	}
	pushStore, err := push.NewStore(filepath.Join(t.TempDir(), "push.json"), []string{push.DefaultPushHost})
	if err != nil {
		t.Fatalf("push.NewStore: %v", err)
	}
	shutdown := make(chan struct{})
	handler, err := server.New(server.Config{
		Logins:   []string{serveTestLogin},
		Tmux:     tmux,
		Push:     pushStore,
		Sessions: store.NewReader(dbPath),
		Shutdown: shutdown,
	})
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}

	serveCtx, cancelServe := context.WithCancel(ctx)
	serveDone := make(chan struct{})
	var serveErr error
	go func() {
		serveErr = serveUntilDone(serveCtx, ln, handler, shutdown)
		close(serveDone)
	}()
	t.Cleanup(func() {
		cancelServe()
		select {
		case <-serveDone:
		case <-time.After(serveTestWait):
			t.Error("serve loop did not return before the test ended")
		}
	})

	events := orreryv1connect.NewEventsClient(&http.Client{Transport: serveLoginTransport{base: http.DefaultTransport}},
		"http://"+ln.Addr().String())
	streamCtx, cancelStream := context.WithCancel(ctx)
	defer cancelStream()
	stream, err := events.Watch(streamCtx, connect.NewRequest(&orreryv1.WatchRequest{After: 0}))
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	first := make(chan struct{}, 1)
	streamDone := make(chan error, 1)
	go func() {
		signaled := false
		for stream.Receive() {
			if !signaled {
				first <- struct{}{}
				signaled = true
			}
		}
		streamDone <- stream.Err()
	}()
	select {
	case <-first:
	case <-time.After(serveTestWait):
		t.Fatalf("no message on the Watch stream within %v", serveTestWait)
	}

	started := time.Now()
	cancelServe()
	select {
	case <-serveDone:
	case <-time.After(serveTestWait):
		t.Fatalf("serve did not return within %v", serveTestWait)
	}
	if serveErr != nil {
		t.Fatalf("serve returned %v, want nil", serveErr)
	}
	if elapsed := time.Since(started); elapsed >= serveShutdownTimeout {
		t.Fatalf("serve returned after %v, want it well within the %v shutdown budget", elapsed, serveShutdownTimeout)
	}

	select {
	case err := <-streamDone:
		if got := connect.CodeOf(err); got != connect.CodeUnavailable {
			t.Fatalf("stream ended with code %v (%v), want %v", got, err, connect.CodeUnavailable)
		}
	case <-time.After(serveTestWait):
		t.Fatalf("Watch stream did not end within %v of shutdown", serveTestWait)
	}
}

// servePickerHandler serves the API for the tmux client tmux with picker as its Picker, behind the guard headers the
// test transport sends, and returns the server's URL.
func servePickerHandler(t *testing.T, tmux *tmuxctl.Client, picker func() (open.Config, error)) string {
	t.Helper()
	pushStore, err := push.NewStore(filepath.Join(t.TempDir(), "push.json"), []string{push.DefaultPushHost})
	if err != nil {
		t.Fatalf("push.NewStore: %v", err)
	}
	handler, err := server.New(server.Config{
		Logins: []string{serveTestLogin},
		Tmux:   tmux,
		Push:   pushStore,
		Picker: picker,
	})
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv.URL
}

// writeServeFakeBinary installs an executable named name in dir.
func writeServeFakeBinary(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake %s: %v", name, err)
	}
	return path
}

// TestServePickerAskAnswersMissingBinary runs serve's picker with neither zoxide nor claude on PATH. server.New
// accepts the picker, and an ask through the guard answers 502 with a body naming the missing zoxide.
func TestServePickerAskAnswersMissingBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	tmux, err := tmuxctl.New(tmuxctl.Server{Name: "orrery-serve-test-unstarted"})
	if err != nil {
		t.Fatalf("tmuxctl.New: %v", err)
	}
	getenv := func(key string) string { return map[string]string{"HOME": t.TempDir()}[key] }
	baseURL := servePickerHandler(t, tmux, servePickerConfig(tmux, getenv))

	picker := orreryv1connect.NewPickerClient(
		&http.Client{Transport: serveLoginTransport{base: http.DefaultTransport}}, baseURL)
	ctx, cancel := context.WithTimeout(t.Context(), serveTestWait)
	defer cancel()
	_, err = picker.Ask(ctx, connect.NewRequest(&orreryv1.AskRequest{Text: "example"}))
	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("Picker.Ask code = %v, want unavailable (err %v)", connect.CodeOf(err), err)
	}
	if !strings.Contains(err.Error(), "zoxide") {
		t.Fatalf("Picker.Ask error = %v, want it to name zoxide", err)
	}
}

// TestServePickerConfigResolvesBinaries runs serve's picker with fake zoxide and claude on PATH. The config carries
// their absolute paths, serve's tmux client, and the default root $HOME/Personal.
func TestServePickerConfigResolvesBinaries(t *testing.T) {
	bin := t.TempDir()
	zoxide := writeServeFakeBinary(t, bin, "zoxide")
	claude := writeServeFakeBinary(t, bin, "claude")
	t.Setenv("PATH", bin)
	home := t.TempDir()
	tmux, err := tmuxctl.New(tmuxctl.Server{Name: "orrery-serve-test-unstarted"})
	if err != nil {
		t.Fatalf("tmuxctl.New: %v", err)
	}
	getenv := func(key string) string { return map[string]string{"HOME": home}[key] }

	cfg, err := servePickerConfig(tmux, getenv)()
	if err != nil {
		t.Fatalf("picker: %v", err)
	}
	if cfg.Zoxide != zoxide {
		t.Errorf("Zoxide = %q, want %q", cfg.Zoxide, zoxide)
	}
	if cfg.Claude != claude {
		t.Errorf("Claude = %q, want %q", cfg.Claude, claude)
	}
	if cfg.Tmux != tmux {
		t.Errorf("Tmux is not serve's tmux client")
	}
	if want := filepath.Join(home, "Personal"); cfg.Root != want {
		t.Errorf("Root = %q, want %q", cfg.Root, want)
	}
}

// serveFreeLoopbackAddr returns a loopback address with a port nothing listens on, for runServe to bind.
func serveFreeLoopbackAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("close probe listener: %v", err)
	}
	return addr
}

// TestServeAnswersPageAtRoot runs orrery serve through runServe on a free loopback port. GET / with the allowed login
// and no client header answers the embedded page, with the page's CSP and nosniff headers, and the page's bytes.
func TestServeAnswersPageAtRoot(t *testing.T) {
	state := t.TempDir()
	getenv := func(key string) string { return map[string]string{"XDG_STATE_HOME": state}[key] }
	addr := serveFreeLoopbackAddr(t)
	opts := globals{server: tmuxctl.Server{Name: "orrery-serve-test-unstarted"}}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- runServe(ctx, opts, []string{"--listen", addr, "--allow-login", serveTestLogin}, getenv)
	}()

	var resp *http.Response
	deadline := time.After(serveTestWait)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for resp == nil {
		select {
		case err := <-done:
			t.Fatalf("runServe returned before serving: %v", err)
		case <-deadline:
			t.Fatalf("no answer from runServe on %s within %v", addr, serveTestWait)
		case <-ticker.C:
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/", nil)
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		req.Header.Set(server.HeaderLogin, serveTestLogin)
		if next, err := http.DefaultClient.Do(req); err == nil {
			resp = next
		}
	}
	body, err := io.ReadAll(resp.Body)
	if closeErr := resp.Body.Close(); closeErr != nil {
		t.Errorf("close body: %v", closeErr)
	}
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /: status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if got := resp.Header.Get("Content-Security-Policy"); got != "default-src 'self'; frame-ancestors 'none'" {
		t.Fatalf("GET /: Content-Security-Policy = %q, want the page CSP", got)
	}
	if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("GET /: X-Content-Type-Options = %q, want nosniff", got)
	}
	want, err := fs.ReadFile(webui.FS(), "index.html")
	if err != nil {
		t.Fatalf("read embedded index.html: %v", err)
	}
	if string(body) != string(want) {
		t.Fatalf("GET /: body = %q, want the embedded index.html %q", body, want)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runServe returned %v, want nil", err)
		}
	case <-time.After(serveTestWait):
		t.Fatalf("runServe did not return within %v of cancel", serveTestWait)
	}
}

// servePools is an in-process orreryd Pools for the serve test. Each call records its name and member, and Status
// answers with status.
type servePools struct {
	enginev1connect.UnimplementedPoolsHandler

	status *enginev1.PoolsStatusResponse
	mu     sync.Mutex
	calls  []string
}

// record appends call to the calls the fake has received.
func (f *servePools) record(call string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
}

// received returns the calls the fake has received, in order.
func (f *servePools) received() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

// Status implements enginev1connect.PoolsHandler.
func (f *servePools) Status(
	_ context.Context,
	_ *connect.Request[enginev1.PoolsStatusRequest],
) (*connect.Response[enginev1.PoolsStatusResponse], error) {
	f.record("Status")
	return connect.NewResponse(f.status), nil
}

// Hold implements enginev1connect.PoolsHandler.
func (f *servePools) Hold(
	_ context.Context,
	req *connect.Request[enginev1.PoolsHoldRequest],
) (*connect.Response[enginev1.PoolsHoldResponse], error) {
	f.record("Hold " + req.Msg.GetMember())
	return connect.NewResponse(&enginev1.PoolsHoldResponse{}), nil
}

// Free implements enginev1connect.PoolsHandler.
func (f *servePools) Free(
	_ context.Context,
	req *connect.Request[enginev1.PoolsFreeRequest],
) (*connect.Response[enginev1.PoolsFreeResponse], error) {
	f.record("Free " + req.Msg.GetMember())
	return connect.NewResponse(&enginev1.PoolsFreeResponse{}), nil
}

// serveFakePools serves fake as Pools on the session socket beside a daemon socket in a temporary directory. It
// returns the daemon socket path that `--socket` names. The server is closed at cleanup.
func serveFakePools(t *testing.T, fake *servePools) string {
	t.Helper()
	daemon := filepath.Join(t.TempDir(), "orreryd.sock")
	ln, err := net.Listen("unix", protocol.SessionSocketPath(daemon))
	if err != nil {
		t.Fatalf("listen on the session socket: %v", err)
	}
	mux := http.NewServeMux()
	path, handler := enginev1connect.NewPoolsHandler(fake)
	mux.Handle(path, handler)
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() {
		if err := srv.Close(); err != nil {
			t.Errorf("close pools server: %v", err)
		}
	})
	return daemon
}

// serveAwaitListener waits until a connection to addr succeeds. It fails the test when done reports that runServe
// returned first, or when no connection succeeds within serveTestWait.
func serveAwaitListener(t *testing.T, addr string, done <-chan error) {
	t.Helper()
	deadline := time.After(serveTestWait)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-done:
			t.Fatalf("runServe returned before serving: %v", err)
		case <-deadline:
			t.Fatalf("no listener on %s within %v", addr, serveTestWait)
		case <-ticker.C:
			if conn, err := net.Dial("tcp", addr); err == nil {
				if err := conn.Close(); err != nil {
					t.Errorf("close probe connection: %v", err)
				}
				return
			}
		}
	}
}

// TestServeForwardsPools runs serve through runServe with --socket naming a daemon socket whose session socket serves a
// fake orreryd Pools. A phone's Pools.Status, Hold and Free through the guard reach that Pools, and Status answers with
// the pools and holds the Pools returns.
func TestServeForwardsPools(t *testing.T) {
	fake := &servePools{status: &enginev1.PoolsStatusResponse{
		Pools: []*enginev1.PoolStatus{{
			Name:    "builds",
			Cap:     2,
			Members: []*enginev1.PoolMemberStatus{{Name: "builds-1", State: "awake"}},
		}},
		Holds: []*enginev1.PoolHold{{Member: "builds-2", Since: 1_700_000_000}},
	}}
	daemon := serveFakePools(t, fake)
	state := t.TempDir()
	getenv := func(key string) string { return map[string]string{"XDG_STATE_HOME": state}[key] }
	addr := serveFreeLoopbackAddr(t)
	opts := globals{server: tmuxctl.Server{Name: "orrery-serve-test-unstarted"}, daemonSocket: daemon}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- runServe(ctx, opts, []string{"--listen", addr, "--allow-login", serveTestLogin}, getenv)
	}()
	serveAwaitListener(t, addr, done)

	pools := orreryv1connect.NewPoolsClient(
		&http.Client{Transport: serveLoginTransport{base: http.DefaultTransport}}, "http://"+addr)
	callCtx, cancelCall := context.WithTimeout(ctx, serveTestWait)
	defer cancelCall()
	status, err := pools.Status(callCtx, connect.NewRequest(&orreryv1.PoolsStatusRequest{}))
	if err != nil {
		t.Fatalf("Pools.Status: %v", err)
	}
	if _, err := pools.Hold(callCtx, connect.NewRequest(&orreryv1.PoolsHoldRequest{Member: "builds-1"})); err != nil {
		t.Fatalf("Pools.Hold: %v", err)
	}
	if _, err := pools.Free(callCtx, connect.NewRequest(&orreryv1.PoolsFreeRequest{Member: "builds-1"})); err != nil {
		t.Fatalf("Pools.Free: %v", err)
	}

	want := &orreryv1.PoolsStatusResponse{
		Pools: []*orreryv1.PoolStatus{{
			Name:    "builds",
			Cap:     2,
			Members: []*orreryv1.PoolMemberStatus{{Name: "builds-1", State: "awake"}},
		}},
		Holds: []*orreryv1.PoolHold{{Member: "builds-2", Since: 1_700_000_000}},
	}
	if !proto.Equal(status.Msg, want) {
		t.Errorf("Pools.Status = %v, want %v", status.Msg, want)
	}
	wantCalls := []string{"Status", "Hold builds-1", "Free builds-1"}
	if got := fake.received(); !slices.Equal(got, wantCalls) {
		t.Errorf("orreryd received %q, want %q", got, wantCalls)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runServe returned %v, want nil", err)
		}
	case <-time.After(serveTestWait):
		t.Fatalf("runServe did not return within %v of cancel", serveTestWait)
	}
}
