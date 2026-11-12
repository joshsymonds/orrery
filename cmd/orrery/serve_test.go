package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
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
	"github.com/joshsymonds/orrery/internal/tmuxtest"
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

// serveTestRead is the synthetic read time, in unix seconds, the read reporter tests send.
const serveTestRead int64 = 1_700_000_001

// servePane is the synthetic pane the read reporter tests name.
const servePane = "%7"

// serveQuietWait bounds how long a test waits to see whether a second connection reaches the daemon socket. A
// connection already queued is accepted at once, so this wait only runs out when no connection is queued.
const serveQuietWait = 200 * time.Millisecond

// serveReceived is one frame a test daemon read, or the error that stopped the read.
type serveReceived struct {
	frame protocol.HookFrame
	err   error
}

// serveNoEnv is a getenv that sets nothing, so no daemon socket resolves from the environment.
func serveNoEnv(string) string { return "" }

// listenDaemon listens on a fresh unix socket that stands in for orreryd, and returns its path and listener.
func listenDaemon(t *testing.T) (string, *net.UnixListener) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "orreryd.sock")
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatalf("listen %s: %v", path, err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return path, ln
}

// receiveFrame accepts one connection on ln and reads its frame in the background. The result arrives on the
// returned channel.
func receiveFrame(ln net.Listener) <-chan serveReceived {
	got := make(chan serveReceived, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			got <- serveReceived{err: err}
			return
		}
		defer func() { _ = conn.Close() }()
		frame, err := protocol.ReadFrame(conn)
		got <- serveReceived{frame: frame, err: err}
	}()
	return got
}

// TestReadReporterSendsOneReadReport runs the reporter against a daemon socket a test listener holds. It sends one
// read report that names the pane and the read time, and no second connection follows.
func TestReadReporterSendsOneReadReport(t *testing.T) {
	path, ln := listenDaemon(t)
	got := receiveFrame(ln)

	report := readReporter(path, serveNoEnv)
	if report == nil {
		t.Fatal("readReporter returned nil for a resolvable socket, want a reporter")
	}
	report(servePane, serveTestRead)

	var result serveReceived
	select {
	case result = <-got:
	case <-time.After(serveTestWait):
		t.Fatalf("daemon received no frame within %v", serveTestWait)
	}
	if result.err != nil {
		t.Fatalf("read frame: %v", result.err)
	}
	want := protocol.HookFrame{TmuxPane: servePane, Read: serveTestRead}
	if !reflect.DeepEqual(result.frame, want) {
		t.Fatalf("frame = %+v, want %+v", result.frame, want)
	}

	// The reporter returns after its write, so a second connection would already be queued and Accept would return it.
	if err := ln.SetDeadline(time.Now().Add(serveQuietWait)); err != nil {
		t.Fatalf("set deadline: %v", err)
	}
	if conn, err := ln.Accept(); err == nil {
		_ = conn.Close()
		t.Fatal("daemon received a second connection, want exactly one read report")
	}
}

// TestReadReporterNilWithoutSocket leaves the reporter nil when neither the flag nor the environment names a socket.
func TestReadReporterNilWithoutSocket(t *testing.T) {
	if report := readReporter("", serveNoEnv); report != nil {
		t.Fatal("readReporter returned a reporter with no daemon socket, want nil")
	}
}

// TestReadReporterFailureWritesStderr points the reporter at a socket whose directory is a regular file. The dial fails
// for a reason other than absence, so the reporter returns normally and writes the error to the writer readReporter
// gives standard error.
func TestReadReporterFailureWritesStderr(t *testing.T) {
	// A regular file in place of the socket's directory makes the dial fail with ENOTDIR.
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	capturePath := filepath.Join(t.TempDir(), "stderr")
	capture, err := os.Create(capturePath)
	if err != nil {
		t.Fatalf("create stderr capture: %v", err)
	}
	t.Cleanup(func() {
		if err := capture.Close(); err != nil {
			t.Errorf("close stderr capture: %v", err)
		}
	})
	report := readReporterTo(filepath.Join(blocker, "orreryd.sock"), serveNoEnv, capture)
	if report == nil {
		t.Fatal("readReporterTo returned nil for a resolvable socket, want a reporter")
	}

	report(servePane, serveTestRead)

	out, err := os.ReadFile(capturePath)
	if err != nil {
		t.Fatalf("read stderr capture: %v", err)
	}
	const prefix = "orrery serve: report read: "
	if !strings.HasPrefix(string(out), prefix) || !strings.HasSuffix(string(out), "\n") {
		t.Fatalf("stderr = %q, want one line starting %q", out, prefix)
	}
}

// serveNewTmux returns a unique private tmux server and kills it at cleanup.
func serveNewTmux(t *testing.T) tmuxctl.Server {
	t.Helper()
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatalf("random server name: %v", err)
	}
	tmuxServer := tmuxctl.Server{Name: fmt.Sprintf("orrery-serve-read-%d-%x", os.Getpid(), suffix)}
	t.Cleanup(func() {
		if err := tmuxtest.Stop(t, tmuxServer); err != nil {
			t.Errorf("kill-server: %v", err)
		}
	})
	return tmuxServer
}

// serveStartPane starts a detached session on tmuxServer and returns the ID of its one pane.
func serveStartPane(t *testing.T, tmuxServer tmuxctl.Server) string {
	t.Helper()
	client, err := tmuxctl.New(tmuxServer)
	if err != nil {
		t.Fatalf("tmuxctl.New: %v", err)
	}
	if _, err := client.Run(t.Context(), "-f", "/dev/null", "new-session", "-d", "-s", "serve",
		"-c", t.TempDir(), "sleep 300"); err != nil {
		t.Fatalf("new-session: %v", err)
	}
	out, err := client.Run(t.Context(), "list-panes", "-t", "serve", "-F", "#{pane_id}")
	if err != nil {
		t.Fatalf("list-panes: %v", err)
	}
	return strings.TrimSpace(out)
}

// TestServeReportsPaneReadToDaemon runs orrery serve through runServe against a live tmux pane and a test daemon
// socket. A Panes.Screen read of the pane answers the phone and sends one read report naming the pane and the time of
// the read.
func TestServeReportsPaneReadToDaemon(t *testing.T) {
	// tmux prints the list-panes field separator as "_" without a UTF-8 locale, as in the nix build sandbox, and
	// serve's pane check then fails. The tmux server and clients this test starts inherit this locale.
	t.Setenv("LC_ALL", "C.UTF-8")
	tmuxServer := serveNewTmux(t)
	pane := serveStartPane(t, tmuxServer)
	daemonSocket, daemon := listenDaemon(t)
	got := receiveFrame(daemon)

	addr := serveFreeLoopbackAddr(t)
	state := t.TempDir()
	getenv := func(key string) string { return map[string]string{"XDG_STATE_HOME": state}[key] }
	opts := globals{server: tmuxServer, daemonSocket: daemonSocket}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- runServe(ctx, opts, []string{"--listen", addr, "--allow-login", serveTestLogin}, getenv)
	}()

	panes := orreryv1connect.NewPanesClient(
		&http.Client{Transport: serveLoginTransport{base: http.DefaultTransport}}, "http://"+addr)
	screen := connect.NewRequest(&orreryv1.ScreenRequest{Pane: pane})
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.After(serveTestWait)
	before := time.Now().Unix()
	for {
		callCtx, cancelCall := context.WithTimeout(ctx, serveTestWait)
		_, err := panes.Screen(callCtx, screen)
		cancelCall()
		if err == nil {
			break
		}
		select {
		case serveErr := <-done:
			t.Fatalf("runServe returned before serving: %v", serveErr)
		case <-deadline:
			t.Fatalf("Panes.Screen on %s: %v", pane, err)
		case <-ticker.C:
		}
	}
	after := time.Now().Unix()

	var result serveReceived
	select {
	case result = <-got:
	case <-time.After(serveTestWait):
		t.Fatalf("daemon received no read report within %v", serveTestWait)
	}
	if result.err != nil {
		t.Fatalf("read frame: %v", result.err)
	}
	if result.frame.TmuxPane != pane {
		t.Fatalf("read report pane = %q, want %q", result.frame.TmuxPane, pane)
	}
	if result.frame.Read < before || result.frame.Read > after {
		t.Fatalf("read report time = %d, want within [%d, %d]", result.frame.Read, before, after)
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
