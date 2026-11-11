package hook

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
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
	// whitespace is scanned by the JSON encoder but dropped from the frame it writes, so
	// encoding alone takes well over forwardDeadline while the bytes on the wire stay small.
	whitespace = 16 << 20
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

func TestHookEncodingOutsideDeadline(t *testing.T) {
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

	hook := `{"hook_event_name":"Stop",` + strings.Repeat(" ", whitespace) + `"session_id":"example-session"}`
	Run(strings.NewReader(hook), testEnv(map[string]string{"TMUX_PANE": testPane}), path)

	var result received
	select {
	case result = <-got:
	case <-time.After(frameWait):
		t.Fatal("daemon received no frame")
	}
	if result.err != nil {
		t.Fatalf("read frame: %v (the frame was dropped; encoding ran inside the deadline)", result.err)
	}
	if string(result.frame.Hook) != testHook {
		t.Errorf("hook = %s, want %s", result.frame.Hook, testHook)
	}
}

func TestHookDeadline(t *testing.T) {
	path, ln := listen(t)

	// accepted records when the daemon accepted the connection: the deadline governs the time
	// from there to Run's return.
	type acceptance struct {
		conn net.Conn
		at   time.Time
	}
	accepted := make(chan acceptance, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		accepted <- acceptance{conn: conn, at: time.Now()}
	}()

	hook := `{"hook_event_name":"Stop","padding":"` + strings.Repeat("x", oversize) + `"}`
	env := testEnv(map[string]string{"TMUX_PANE": testPane})
	limit := forwardDeadline + slack

	// Run runs in its own goroutine so an overrunning Run fails the test rather than hanging it.
	finished := make(chan time.Time, 1)
	go func() {
		Run(strings.NewReader(hook), env, path)
		finished <- time.Now()
	}()

	var returned time.Time
	select {
	case returned = <-finished:
	case <-time.After(frameWait):
		t.Fatalf("Run did not return within %v with an unread daemon", frameWait)
	}

	var acc acceptance
	select {
	case acc = <-accepted:
		t.Cleanup(func() { _ = acc.conn.Close() })
	case <-time.After(frameWait):
		t.Fatal("daemon never accepted the connection")
	}
	if elapsed := returned.Sub(acc.at); elapsed > limit {
		t.Fatalf("Run returned %v after the daemon accepted, want under %v", elapsed, limit)
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

func TestHookSessionMarker(t *testing.T) {
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

	for _, value := range []string{"1", "headless"} {
		t.Run(value, func(t *testing.T) {
			// The write end stays open and never writes, so a Run that reads stdin blocks forever.
			stdin, writer := io.Pipe()
			t.Cleanup(func() { _ = writer.Close() })

			env := map[string]string{
				protocol.EnvSession: value,
				"TMUX_PANE":         testPane,
			}
			finished := make(chan struct{})
			go func() {
				Run(stdin, testEnv(env), path)
				close(finished)
			}()

			select {
			case <-finished:
			case <-time.After(frameWait):
				t.Fatal("Run blocked under the session marker, want it to return without reading stdin")
			}

			select {
			case <-dialed:
				t.Fatal("session-marked hook dialed the daemon")
			case <-time.After(quietWindow):
			}
		})
	}
}

// testSeen is a seen timestamp for the seen-report tests.
const testSeen int64 = 1_700_000_000_000

func TestReportSeenFrame(t *testing.T) {
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

	if err := ReportSeen(path, testPane, testSeen); err != nil {
		t.Fatalf("ReportSeen: %v", err)
	}

	var result received
	select {
	case result = <-got:
	case <-time.After(frameWait):
		t.Fatal("daemon received no frame")
	}
	if result.err != nil {
		t.Fatalf("read frame: %v", result.err)
	}
	want := protocol.HookFrame{TmuxPane: testPane, Seen: testSeen}
	if !reflect.DeepEqual(result.frame, want) {
		t.Errorf("frame = %+v, want %+v", result.frame, want)
	}
}

func TestReportSeenNoSocketFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.sock")
	if err := ReportSeen(path, testPane, testSeen); err != nil {
		t.Fatalf("ReportSeen with no socket file: %v, want nil", err)
	}
}

func TestReportSeenStaleSocket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stale.sock")
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatalf("listen %s: %v", path, err)
	}
	// Closing leaves the socket file behind, so the path exists with no listener behind it.
	ln.SetUnlinkOnClose(false)
	if err := ln.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stale socket file missing: %v", err)
	}

	if err := ReportSeen(path, testPane, testSeen); err != nil {
		t.Fatalf("ReportSeen with stale socket: %v, want nil", err)
	}
}

func TestReportSeenEmptySocketPath(t *testing.T) {
	if err := ReportSeen("", testPane, testSeen); err != nil {
		t.Fatalf("ReportSeen with empty socket path: %v, want nil", err)
	}
}

func TestReportSeenFailure(t *testing.T) {
	// A regular file in place of the socket's directory makes stat fail with ENOTDIR, which is
	// neither a missing socket nor a refused connection, so it must surface as an error.
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	path := filepath.Join(blocker, "h.sock")

	if err := ReportSeen(path, testPane, testSeen); err == nil {
		t.Fatal("ReportSeen with an unreachable socket path returned nil, want an error")
	}
}
