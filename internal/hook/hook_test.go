package hook

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/protocol"
)

const (
	testPane    = "%1"
	testTmux    = "/tmp/example-tmux,1234,0"
	testBaseURL = "https://patchbay.example.com/ctx/example-context"
	testHook    = `{"hook_event_name":"Stop","session_id":"example-session"}`

	// frameWait bounds how long a test waits for the listener to receive a frame.
	frameWait = 2 * time.Second
	// slack is the allowance over forwardDeadline that TestHookDeadline tolerates.
	slack = 500 * time.Millisecond
	// promptly is the bound TestHookDaemonDown applies to a Run with no daemon.
	promptly = time.Second
	// quietWindow is how long TestHookSentinel waits for a stray connection to arrive.
	quietWindow = 200 * time.Millisecond
	// oversize is larger than a default unix socket buffer, so an unread write blocks.
	oversize = 4 << 20
)

// testEnv returns a getenv backed by vars.
func testEnv(vars map[string]string) func(string) string {
	return func(name string) string { return vars[name] }
}

// listen starts a unix listener on a fresh socket path inside the test's temp directory.
func listen(t *testing.T) (string, net.Listener) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "h.sock")
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "unix", path)
	if err != nil {
		t.Fatalf("listen %s: %v", path, err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return path, ln
}

// captureFile points *target at a fresh file for the rest of the test and returns that file.
func captureFile(t *testing.T, target **os.File) *os.File {
	t.Helper()
	f, err := os.Create(filepath.Join(t.TempDir(), "out"))
	if err != nil {
		t.Fatalf("create capture file: %v", err)
	}
	saved := *target
	*target = f
	t.Cleanup(func() {
		*target = saved
		_ = f.Close()
	})
	return f
}

type received struct {
	frame protocol.HookFrame
	err   error
}

func TestHookForwardsFrame(t *testing.T) {
	t.Chdir(t.TempDir())
	wantCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	path, ln := listen(t)

	got := make(chan received, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			got <- received{err: err}
			return
		}
		defer func() { _ = conn.Close() }()
		frame, err := protocol.ReadFrame(conn)
		got <- received{frame: frame, err: err}
	}()

	env := map[string]string{
		"TMUX_PANE":          testPane,
		"TMUX":               testTmux,
		"ANTHROPIC_BASE_URL": testBaseURL,
	}
	Run(strings.NewReader(testHook), testEnv(env), path)

	var result received
	select {
	case result = <-got:
	case <-time.After(frameWait):
		t.Fatal("daemon received no frame")
	}
	if result.err != nil {
		t.Fatalf("read frame: %v", result.err)
	}
	if string(result.frame.Hook) != testHook {
		t.Errorf("hook = %s, want %s", result.frame.Hook, testHook)
	}
	if result.frame.TmuxPane != testPane {
		t.Errorf("tmuxPane = %q, want %q", result.frame.TmuxPane, testPane)
	}
	if result.frame.Tmux != testTmux {
		t.Errorf("tmux = %q, want %q", result.frame.Tmux, testTmux)
	}
	if result.frame.Cwd != wantCwd {
		t.Errorf("cwd = %q, want %q", result.frame.Cwd, wantCwd)
	}
	if result.frame.AnthropicBaseURL != testBaseURL {
		t.Errorf("anthropicBaseUrl = %q, want %q", result.frame.AnthropicBaseURL, testBaseURL)
	}
}

func TestHookDaemonDown(t *testing.T) {
	stdout := captureFile(t, &os.Stdout)
	stderr := captureFile(t, &os.Stderr)
	path := filepath.Join(t.TempDir(), "absent.sock")

	start := time.Now()
	Run(strings.NewReader(testHook), testEnv(map[string]string{"TMUX_PANE": testPane}), path)
	if elapsed := time.Since(start); elapsed > promptly {
		t.Fatalf("Run took %v with no daemon, want under %v", elapsed, promptly)
	}

	for name, f := range map[string]*os.File{"stdout": stdout, "stderr": stderr} {
		info, err := f.Stat()
		if err != nil {
			t.Fatalf("stat %s capture: %v", name, err)
		}
		if info.Size() != 0 {
			t.Errorf("Run wrote %d bytes to %s, want none", info.Size(), name)
		}
	}
}

func TestHookDeadline(t *testing.T) {
	path, ln := listen(t)

	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		accepted <- conn
	}()

	hook := `{"hook_event_name":"Stop","padding":"` + strings.Repeat("x", oversize) + `"}`
	env := testEnv(map[string]string{"TMUX_PANE": testPane})
	limit := forwardDeadline + slack

	// Run runs in its own goroutine so an overrunning Run fails the test rather than hanging it.
	finished := make(chan time.Duration, 1)
	go func() {
		start := time.Now()
		Run(strings.NewReader(hook), env, path)
		finished <- time.Since(start)
	}()

	var elapsed time.Duration
	select {
	case elapsed = <-finished:
	case <-time.After(frameWait):
		t.Fatalf("Run did not return within %v with an unread daemon", frameWait)
	}
	if elapsed > limit {
		t.Fatalf("Run took %v with an unread daemon, want under %v", elapsed, limit)
	}

	select {
	case conn := <-accepted:
		t.Cleanup(func() { _ = conn.Close() })
	case <-time.After(frameWait):
		t.Fatal("daemon never accepted the connection")
	}
}

func TestHookSentinel(t *testing.T) {
	path, ln := listen(t)

	dialed := make(chan struct{}, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		_ = conn.Close()
		dialed <- struct{}{}
	}()

	env := map[string]string{
		protocol.EnvSummarizer: protocol.SummarizerValue,
		"TMUX_PANE":            testPane,
	}
	Run(strings.NewReader(testHook), testEnv(env), path)

	select {
	case <-dialed:
		t.Fatal("summarizer hook dialed the daemon")
	case <-time.After(quietWindow):
	}
}
