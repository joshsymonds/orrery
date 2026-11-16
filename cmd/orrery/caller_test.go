package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/joshsymonds/orrery/internal/enginehost"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/protocol"
)

// callerEpics is the epic service behind the test session socket. GetEpic records the request headers of each call
// and answers with the epic named in the request. The other methods are not reached by these tests.
type callerEpics struct {
	enginev1connect.UnimplementedEpicsHandler
	mu     sync.Mutex
	header http.Header
}

func (e *callerEpics) GetEpic(
	_ context.Context, req *connect.Request[v1.GetEpicRequest],
) (*connect.Response[v1.GetEpicResponse], error) {
	e.mu.Lock()
	e.header = req.Header().Clone()
	e.mu.Unlock()
	epic := &v1.StoredEpic{Contract: &v1.EpicContract{Id: req.Msg.GetId()}}
	return connect.NewResponse(&v1.GetEpicResponse{Epic: epic}), nil
}

// lastHeader returns the request headers of the most recent GetEpic call.
func (e *callerEpics) lastHeader() http.Header {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.header
}

// startCallerDaemon serves epics on sessions.sock in a temporary directory, the session socket that the epic
// commands dial beside the daemon socket. It returns the daemon socket path that `--socket` names.
func startCallerDaemon(t *testing.T, epics enginev1connect.EpicsHandler) string {
	t.Helper()
	dir := t.TempDir()
	ln, err := enginehost.Listen(filepath.Join(dir, "sessions.sock"))
	if err != nil {
		t.Fatalf("enginehost.Listen: %v", err)
	}
	path, handler := enginev1connect.NewEpicsHandler(epics)
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ln) }()
	t.Cleanup(func() {
		if err := srv.Close(); err != nil {
			t.Errorf("close session server: %v", err)
		}
		if err := <-done; !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("serve session socket: %v", err)
		}
	})
	return filepath.Join(dir, "orreryd.sock")
}

// envFrom returns a getenv that reads values and returns "" for every other name.
func envFrom(values map[string]string) func(string) string {
	return func(name string) string {
		return values[name]
	}
}

// startCallerRun runs `orrery args` on its own goroutine with pipes for stdin and stdout and with getenv as its
// environment. The command closes stdout when it returns.
func startCallerRun(t *testing.T, getenv func(string) string, args ...string) *mcpRun {
	t.Helper()
	stdinR, stdinW := mustPipe(t)
	stdoutR, stdoutW := mustPipe(t)
	r := &mcpRun{t: t, stdin: stdinW, stdout: stdoutR, lines: bufio.NewReader(stdoutR), done: make(chan int, 1)}
	t.Cleanup(func() {
		closeFile(t, stdinW)
		closeFile(t, stdoutR)
	})
	go func() {
		code := run(args, stdinR, stdoutW, &r.stderr, getenv)
		closeFile(t, stdoutW)
		closeFile(t, stdinR)
		r.done <- code
	}()
	return r
}

// TestEpicGetSendsSessionHeader runs `orrery --socket S epic get ID` in a session that orrery started and checks
// that the epic service receives the session as Orrery-Session.
func TestEpicGetSendsSessionHeader(t *testing.T) {
	t.Parallel()
	epics := &callerEpics{}
	socket := startCallerDaemon(t, epics)
	getenv := envFrom(map[string]string{protocol.EnvSession: "session-7"})
	stdin, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatalf("open %s: %v", os.DevNull, err)
	}
	t.Cleanup(func() { closeFile(t, stdin) })
	var stdout, stderr bytes.Buffer
	args := []string{"--socket", socket, "epic", "get", epicTestID}
	if code := run(args, stdin, &stdout, &stderr, getenv); code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr %q", code, stderr.String())
	}
	header := epics.lastHeader()
	if header == nil {
		t.Fatal("epic service received no GetEpic call")
	}
	if got := header.Get(protocol.HeaderSession); got != "session-7" {
		t.Fatalf("%s = %q, want %q", protocol.HeaderSession, got, "session-7")
	}
}

// TestMCPEpicGetSendsTmuxHeaders runs `orrery --socket S mcp` in a session in tmux, calls epic_get over MCP, and
// checks that the epic service receives the TMUX and TMUX_PANE of the session as Orrery-Tmux and Orrery-Tmux-Pane.
func TestMCPEpicGetSendsTmuxHeaders(t *testing.T) {
	t.Parallel()
	epics := &callerEpics{}
	socket := startCallerDaemon(t, epics)
	getenv := envFrom(map[string]string{"TMUX": "/tmp/tmux-1000/orrery-test,4242,0", "TMUX_PANE": "%3"})
	cmd := startCallerRun(t, getenv, "--socket", socket, "mcp")

	cmd.send(mcpInitialize)
	requireResult(t, cmd.receive(), 1)
	cmd.send(mcpInitialized)
	cmd.send(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"epic_get","arguments":{"id":"` +
		epicTestID + `"}}}`)
	result := requireResult(t, cmd.receive(), 2)
	if !strings.Contains(string(result), epicTestID) {
		t.Fatalf("epic_get result %s does not name %s", result, epicTestID)
	}

	if code := cmd.finish(); code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr %q", code, cmd.stderr.String())
	}
	header := epics.lastHeader()
	if header == nil {
		t.Fatal("epic service received no GetEpic call")
	}
	if got := header.Get(protocol.HeaderTmux); got != "/tmp/tmux-1000/orrery-test,4242,0" {
		t.Fatalf("%s = %q, want the TMUX of the session", protocol.HeaderTmux, got)
	}
	if got := header.Get(protocol.HeaderTmuxPane); got != "%3" {
		t.Fatalf("%s = %q, want %q", protocol.HeaderTmuxPane, got, "%3")
	}
}
