package hook

import (
	"context"
	"errors"
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
	testBaseURL = "https://proxy.example.com/ctx/example-context"
	testHook    = `{"hook_event_name":"Stop","session_id":"example-session"}`

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

	result := <-got
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

	Run(strings.NewReader(testHook), testEnv(map[string]string{"TMUX_PANE": testPane}), path)

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

// A frame whose encoding is slow, here for its whitespace, reaches the daemon compacted, and the hook reads no clock for
// it: a frame that fits the socket never waits on the daemon, so neither its encoding nor any stall counts against the
// deadline.
func TestHookEncodingNeverCountsAgainstTheDeadline(t *testing.T) {
	path, ln := listen(t)
	hook := `{"hook_event_name":"Stop",` + strings.Repeat(" ", whitespace) + `"session_id":"example-session"}`
	clockRead := false
	result := receiveAfter(t, path, ln, func() {
		run(strings.NewReader(hook), testEnv(map[string]string{"TMUX_PANE": testPane}), path, func() time.Time {
			clockRead = true
			return time.Now()
		})
	})
	if result.err != nil {
		t.Fatalf("read frame: %v (a frame that fits the socket was dropped)", result.err)
	}
	if string(result.frame.Hook) != testHook {
		t.Errorf("hook = %s, want %s", result.frame.Hook, testHook)
	}
	if clockRead {
		t.Error("the hook read the clock for a frame that fit the socket: something waited on the daemon")
	}
}

// A daemon that never reads holds up the hook no longer than the deadline: the part of a frame too big for the socket
// waits for room until then, and the rest is dropped. The stalled clock puts the deadline in the past, so Run returns
// at once with only what fit written.
func TestHookDeadline(t *testing.T) {
	path, ln := listen(t)
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			close(accepted)
			return
		}
		accepted <- conn
	}()

	hook := `{"hook_event_name":"Stop","padding":"` + strings.Repeat("x", oversize) + `"}`
	run(strings.NewReader(hook), testEnv(map[string]string{"TMUX_PANE": testPane}), path, stalledClock)

	conn, ok := <-accepted
	if !ok {
		t.Fatal("the daemon accepted no connection")
	}
	t.Cleanup(func() { _ = conn.Close() })
	got, err := io.ReadAll(conn)
	if err != nil {
		t.Fatalf("read what the hook wrote: %v", err)
	}
	if len(got) == 0 || len(got) >= oversize {
		t.Fatalf(
			"the daemon got %d bytes, want part of the %d-byte frame: what fit, without the wait",
			len(got),
			oversize,
		)
	}
}

// requireNothingSent fails t unless the daemon on ln got no connection from send: once send has returned, the first
// connection accepted must be receiveAfter's sentinel, whose read is EOF.
func requireNothingSent(t *testing.T, path string, ln net.Listener, what string, send func()) {
	t.Helper()
	if result := receiveAfter(t, path, ln, send); !errors.Is(result.err, io.EOF) {
		t.Fatalf("%s reached the daemon: first connection read %+v, %v; want the sentinel's EOF", what, result.frame,
			result.err)
	}
}

func TestHookSentinel(t *testing.T) {
	path, ln := listen(t)
	env := map[string]string{
		protocol.EnvSummarizer: protocol.SummarizerValue,
		"TMUX_PANE":            testPane,
	}
	requireNothingSent(t, path, ln, "the summarizer hook", func() {
		Run(strings.NewReader(testHook), testEnv(env), path)
	})
}

// unreadStdin is a stdin a session-marked hook must not read: a read fails the test and ends the input.
type unreadStdin struct{ t *testing.T }

func (r unreadStdin) Read([]byte) (int, error) {
	r.t.Error("the session-marked hook read its stdin")
	return 0, io.EOF
}

func TestHookSessionMarker(t *testing.T) {
	for _, value := range []string{"1", "headless"} {
		t.Run(value, func(t *testing.T) {
			path, ln := listen(t)
			env := map[string]string{
				protocol.EnvSession: value,
				"TMUX_PANE":         testPane,
			}
			requireNothingSent(t, path, ln, "the session-marked hook", func() {
				Run(unreadStdin{t: t}, testEnv(env), path)
			})
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

	result := <-got
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

// testRead is a read timestamp for the read-report tests.
const testRead int64 = 1_700_000_001_000

func TestReportReadFrame(t *testing.T) {
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

	if err := ReportRead(path, testPane, testRead); err != nil {
		t.Fatalf("ReportRead: %v", err)
	}

	result := <-got
	if result.err != nil {
		t.Fatalf("read frame: %v", result.err)
	}
	want := protocol.HookFrame{TmuxPane: testPane, Read: testRead}
	if !reflect.DeepEqual(result.frame, want) {
		t.Errorf("frame = %+v, want %+v", result.frame, want)
	}
}

func TestReportReadNoSocketFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.sock")
	if err := ReportRead(path, testPane, testRead); err != nil {
		t.Fatalf("ReportRead with no socket file: %v, want nil", err)
	}
}

func TestReportReadStaleSocket(t *testing.T) {
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

	if err := ReportRead(path, testPane, testRead); err != nil {
		t.Fatalf("ReportRead with stale socket: %v, want nil", err)
	}
}

func TestReportReadEmptySocketPath(t *testing.T) {
	if err := ReportRead("", testPane, testRead); err != nil {
		t.Fatalf("ReportRead with empty socket path: %v, want nil", err)
	}
}

func TestReportReadFailure(t *testing.T) {
	// A regular file in place of the socket's directory makes stat fail with ENOTDIR, which is
	// neither a missing socket nor a refused connection, so it must surface as an error.
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	path := filepath.Join(blocker, "h.sock")

	if err := ReportRead(path, testPane, testRead); err == nil {
		t.Fatal("ReportRead with an unreachable socket path returned nil, want an error")
	}
}

// testInput is an input timestamp for the input-report tests.
const testInput int64 = 1_700_000_002_000

func TestReportInputFrame(t *testing.T) {
	cases := []struct {
		name string
		view protocol.ViewKey
	}{
		{"no viewing key", protocol.ViewKey{}},
		{"viewing a session", protocol.ViewKey{Session: "synthetic-session"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
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

			if err := ReportInput(path, testInput, tc.view); err != nil {
				t.Fatalf("ReportInput: %v", err)
			}

			result := <-got
			if result.err != nil {
				t.Fatalf("read frame: %v", result.err)
			}
			want := protocol.HookFrame{Input: testInput, View: tc.view}
			if !reflect.DeepEqual(result.frame, want) {
				t.Errorf("frame = %+v, want %+v", result.frame, want)
			}
		})
	}
}

func TestReportInputNoSocketFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.sock")
	if err := ReportInput(path, testInput, protocol.ViewKey{}); err != nil {
		t.Fatalf("ReportInput with no socket file: %v, want nil", err)
	}
}

func TestReportInputStaleSocket(t *testing.T) {
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

	if err := ReportInput(path, testInput, protocol.ViewKey{}); err != nil {
		t.Fatalf("ReportInput with stale socket: %v, want nil", err)
	}
}

func TestReportInputEmptySocketPath(t *testing.T) {
	if err := ReportInput("", testInput, protocol.ViewKey{}); err != nil {
		t.Fatalf("ReportInput with empty socket path: %v, want nil", err)
	}
}

func TestReportInputFailure(t *testing.T) {
	// A regular file in place of the socket's directory makes stat fail with ENOTDIR, which is
	// neither a missing socket nor a refused connection, so it must surface as an error.
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	path := filepath.Join(blocker, "h.sock")

	if err := ReportInput(path, testInput, protocol.ViewKey{}); err == nil {
		t.Fatal("ReportInput with an unreachable socket path returned nil, want an error")
	}
}

// stalledClock reads an hour before the real time, as the clock reads for a hook that stalled an hour, a GC pause or a
// busy CPU drawn out, after it read the time and before it reached the daemon.
func stalledClock() time.Time {
	return time.Now().Add(-time.Hour)
}

// receiveAfter runs send and returns the first frame the daemon on ln gets. A send that drops the frame before it
// connects would leave nothing to accept, so once send has returned a sentinel connects and closes: connections are
// accepted in the order they arrive, so the sentinel is accepted first only when send made none, and its read is EOF.
func receiveAfter(t *testing.T, path string, ln net.Listener, send func()) received {
	t.Helper()
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
	send()
	var dialer net.Dialer
	sentinel, err := dialer.DialContext(t.Context(), "unix", path)
	if err != nil {
		t.Fatalf("dial the sentinel: %v", err)
	}
	if err = sentinel.Close(); err != nil {
		t.Fatalf("close the sentinel: %v", err)
	}
	return <-got
}

// A healthy daemon takes a frame that fits its socket however long the hook stalled first: nothing waits on the daemon,
// so no deadline can have passed. The stalled clock forces the stall between reading the time and reaching the daemon.
func TestHookDeliversAFrameAfterAStall(t *testing.T) {
	path, ln := listen(t)
	result := receiveAfter(t, path, ln, func() {
		run(strings.NewReader(testHook), testEnv(map[string]string{"TMUX_PANE": testPane}), path, stalledClock)
	})
	if result.err != nil {
		t.Fatalf("read frame: %v (a frame the daemon could take at once was dropped after the stall)", result.err)
	}
	if string(result.frame.Hook) != testHook {
		t.Errorf("hook = %s, want %s", result.frame.Hook, testHook)
	}
}
