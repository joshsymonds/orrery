package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/holder"
)

// holderPollInterval is how often a test checks for a file or a socket the holder makes on its own.
const holderPollInterval = 10 * time.Millisecond

// holderFakeChild returns the absolute path of the fake claude the holder runs. It writes its pid and its arguments
// into its working directory, then waits on stdin. Call it before chdirTemp: the path is relative to the package
// directory.
func holderFakeChild(t *testing.T) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "..", "internal", "enginehost", "testdata", "fakeclaude.sh"))
	if err != nil {
		t.Fatalf("resolve fakeclaude.sh: %v", err)
	}
	return path
}

// chdirTemp makes a fresh temporary directory the working directory, which the holder's child inherits, and returns
// it.
func chdirTemp(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	return dir
}

// shortRoot creates a temporary directory whose path is short enough for a session's socket path. The caller removes
// it. A t.TempDir path can exceed sun_path's limit where the temporary directory is long, as on darwin.
func shortRoot() (string, error) {
	root, err := os.MkdirTemp("", "hold")
	if err != nil {
		return "", fmt.Errorf("create short root: %w", err)
	}
	return root, nil
}

// shortStateDir returns a state directory under a short root. The root is removed when the test ends.
func shortStateDir(t *testing.T) string {
	t.Helper()
	root, err := shortRoot()
	if err != nil {
		t.Fatalf("%v", err)
	}
	t.Cleanup(func() {
		if rmErr := os.RemoveAll(root); rmErr != nil {
			t.Errorf("remove %s: %v", root, rmErr)
		}
	})
	return root
}

// sessionDirOf returns the holder directory of session under the state directory root.
func sessionDirOf(t *testing.T, root, session string) string {
	t.Helper()
	dir, err := holder.SessionDir(filepath.Join(root, "holders"), session)
	if err != nil {
		t.Fatalf("SessionDir: %v", err)
	}
	return dir
}

// holderCommand is one `orrery holder` run in process. Its result arrives on ch; done is set once the result has been
// taken, so the cleanup stops only a holder that is still serving.
type holderCommand struct {
	t      *testing.T
	ch     <-chan runResult
	socket string
	done   bool
	res    runResult
}

// startHolderCommand runs `orrery holder --state-dir root --session session -- child...` in process, where child is
// PATH followed by its ARGS. Its cleanup stops a holder still serving, so a failing test leaves no child running.
func startHolderCommand(t *testing.T, root, session string, child ...string) *holderCommand {
	t.Helper()
	socket, err := holder.SocketPath(sessionDirOf(t, root, session))
	if err != nil {
		t.Fatalf("SocketPath: %v", err)
	}
	args := append([]string{"holder", "--state-dir", root, "--session", session, "--"}, child...)
	hc := &holderCommand{t: t, ch: startOrrery(t, io.Discard, args...), socket: socket}
	t.Cleanup(hc.stopIfServing)
	return hc
}

// result returns the command's result, failing the test when it does not return in time.
func (hc *holderCommand) result() runResult {
	hc.t.Helper()
	if !hc.done {
		hc.res = awaitOrrery(hc.t, hc.ch)
		hc.done = true
	}
	return hc.res
}

// dial connects to the holder's socket, retrying while the holder binds it. It fails the test when the command returns
// first or the socket does not accept in time.
func (hc *holderCommand) dial() *holderConn {
	hc.t.Helper()
	deadline := time.After(runTimeout)
	for {
		conn, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: hc.socket, Net: "unix"})
		if err == nil {
			hc.t.Cleanup(func() {
				if closeErr := conn.Close(); closeErr != nil {
					hc.t.Errorf("close client connection: %v", closeErr)
				}
			})
			return &holderConn{t: hc.t, conn: conn, frames: holder.NewFrameReader(conn)}
		}
		select {
		case res := <-hc.ch:
			hc.res, hc.done = res, true
			hc.t.Fatalf("orrery holder returned %+v before its socket %s accepted", res, hc.socket)
		case <-deadline:
			hc.t.Fatalf("socket %s did not accept within %v: %v", hc.socket, runTimeout, err)
		case <-time.After(holderPollInterval):
		}
	}
}

// stopIfServing stops a holder that is still serving, by a stop frame on its socket, and waits for it to return.
func (hc *holderCommand) stopIfServing() {
	if hc.done {
		return
	}
	if conn, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: hc.socket, Net: "unix"}); err == nil {
		if werr := holder.WriteFrame(conn, holder.Frame{Type: holder.TypeStop}); werr != nil {
			hc.t.Errorf("send stop at cleanup: %v", werr)
		}
		if cerr := conn.Close(); cerr != nil {
			hc.t.Errorf("close cleanup connection: %v", cerr)
		}
	}
	select {
	case <-hc.ch:
	case <-time.After(runTimeout):
		hc.t.Errorf("orrery holder still running %v after its test ended", runTimeout)
	}
}

// holderConn is the test's end of one connection to a holder.
type holderConn struct {
	t      *testing.T
	conn   net.Conn
	frames *holder.FrameReader
}

// hello sends hello at the wire version and returns the holder's answer.
func (c *holderConn) hello() holder.Frame {
	c.t.Helper()
	c.send(holder.Frame{Type: holder.TypeHello, Version: holder.WireVersion})
	return c.read()
}

// stop sends stop and returns the holder's answer.
func (c *holderConn) stop() holder.Frame {
	c.t.Helper()
	c.send(holder.Frame{Type: holder.TypeStop})
	return c.read()
}

// send writes frame to the holder.
func (c *holderConn) send(frame holder.Frame) {
	c.t.Helper()
	if err := holder.WriteFrame(c.conn, frame); err != nil {
		c.t.Fatalf("send %s frame: %v", frame.Type, err)
	}
}

// read returns the next frame the holder sends, failing the test when none arrives within runTimeout.
func (c *holderConn) read() holder.Frame {
	c.t.Helper()
	if err := c.conn.SetReadDeadline(time.Now().Add(runTimeout)); err != nil {
		c.t.Fatalf("set read deadline: %v", err)
	}
	frame, err := c.frames.Read()
	if err != nil {
		c.t.Fatalf("read frame: %v", err)
	}
	return frame
}

// awaitFile returns the contents of path once the file ends with a newline, failing the test when that takes longer
// than runTimeout.
func awaitFile(t *testing.T, path string) string {
	t.Helper()
	deadline := time.Now().Add(runTimeout)
	for {
		data, err := os.ReadFile(path)
		if err == nil && strings.HasSuffix(string(data), "\n") {
			return string(data)
		}
		if time.Now().After(deadline) {
			t.Fatalf("file %s was not written within %v: %v", path, runTimeout, err)
		}
		time.Sleep(holderPollInterval)
	}
}

// TestHolderServesHelloAndStop runs a holder for a session. Its socket is in the session's holder directory and answers
// hello with the session. Its child runs in the holder's working directory with the arguments after PATH. A stop frame
// ends the child, the holder exits 0, and the exit file records the stop.
func TestHolderServesHelloAndStop(t *testing.T) {
	fake := holderFakeChild(t)
	work := chdirTemp(t)
	root := shortStateDir(t)
	hc := startHolderCommand(t, root, "s1", fake, "alpha", "beta")
	conn := hc.dial()

	answer := conn.hello()
	if answer.Type != holder.TypeHello || answer.Version != holder.WireVersion || answer.Session != "s1" {
		t.Fatalf("answer to hello = %+v, want a hello for session s1 at version %d", answer, holder.WireVersion)
	}
	if got := awaitFile(t, filepath.Join(work, "fakeclaude.args")); got != "alpha beta\n" {
		t.Errorf("child arguments = %q, want %q", got, "alpha beta\n")
	}

	if stopped := conn.stop(); stopped.Type != holder.TypeStopped {
		t.Fatalf("answer to stop = %+v, want stopped", stopped)
	}
	if res := hc.result(); res.code != exitOK {
		t.Fatalf("exit status %d, want %d; stderr %q", res.code, exitOK, res.stderr)
	}
	exitPath := filepath.Join(sessionDirOf(t, root, "s1"), holder.ExitName)
	data, err := os.ReadFile(filepath.Clean(exitPath))
	if err != nil {
		t.Fatalf("read exit file: %v", err)
	}
	var exit holder.Exit
	if err = json.Unmarshal(data, &exit); err != nil {
		t.Fatalf("decode exit file: %v", err)
	}
	if exit.Reason != holder.ReasonStopped {
		t.Errorf("exit reason %q, want %q", exit.Reason, holder.ReasonStopped)
	}
}

// TestSecondHolderRefusedWhileFirstServes runs a second holder for a session whose first holder is running. The second
// exits 1 with the lock error on stderr, and the first keeps serving until its own stop.
func TestSecondHolderRefusedWhileFirstServes(t *testing.T) {
	fake := holderFakeChild(t)
	chdirTemp(t)
	root := shortStateDir(t)
	first := startHolderCommand(t, root, "s1", fake, "alpha")
	conn := first.dial()
	if answer := conn.hello(); answer.Type != holder.TypeHello || answer.Session != "s1" {
		t.Fatalf("first holder's answer to hello = %+v, want a hello for session s1", answer)
	}

	second := startHolderCommand(t, root, "s1", fake, "beta")
	res := second.result()
	if res.code != exitFailure {
		t.Fatalf("second holder exit status %d, want %d; stderr %q", res.code, exitFailure, res.stderr)
	}
	if !strings.HasPrefix(res.stderr, "orrery holder: ") {
		t.Errorf("stderr %q, want it to start with %q", res.stderr, "orrery holder: ")
	}
	if !strings.Contains(res.stderr, "another holder holds the session's lock") {
		t.Errorf("stderr %q, want the lock error", res.stderr)
	}

	again := first.dial()
	if answer := again.hello(); answer.Type != holder.TypeHello || answer.Session != "s1" {
		t.Fatalf("first holder's answer after the refusal = %+v, want a hello for session s1", answer)
	}
	if stopped := again.stop(); stopped.Type != holder.TypeStopped {
		t.Fatalf("answer to stop = %+v, want stopped", stopped)
	}
	if res := first.result(); res.code != exitOK {
		t.Fatalf("first holder exit status %d, want %d; stderr %q", res.code, exitOK, res.stderr)
	}
}

// TestHolderUsageErrorsExitTwo refuses each command line that lacks --session, names a bad session, has no --, or has
// no PATH. Each exits 2 with the usage text and creates no holder directory.
func TestHolderUsageErrorsExitTwo(t *testing.T) {
	fake := holderFakeChild(t)
	chdirTemp(t)
	cases := []struct {
		name string
		args func(root string) []string
	}{
		{"missing session", func(root string) []string {
			return []string{"holder", "--state-dir", root, "--", fake}
		}},
		{"bad session name", func(root string) []string {
			return []string{"holder", "--state-dir", root, "--session", "../escape", "--", fake}
		}},
		{"no separator", func(root string) []string {
			return []string{"holder", "--state-dir", root, "--session", "s1", fake}
		}},
		{"no path", func(root string) []string {
			return []string{"holder", "--state-dir", root, "--session", "s1", "--"}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := shortStateDir(t)
			res := awaitOrrery(t, startOrrery(t, io.Discard, tc.args(root)...))
			if res.code != exitUsage {
				t.Errorf("exit status %d, want %d; stderr %q", res.code, exitUsage, res.stderr)
			}
			if !strings.HasPrefix(res.stderr, "orrery: holder: ") {
				t.Errorf("stderr %q, want a usage error naming holder", res.stderr)
			}
			if !strings.HasSuffix(res.stderr, usageText) {
				t.Errorf("stderr does not end with the usage text: %q", res.stderr)
			}
			if _, err := os.Stat(filepath.Join(root, "holders")); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("holders directory check: %v, want it not to exist", err)
			}
		})
	}
}

// TestHolderDefaultStateDirIsHoldersDir runs a holder without --state-dir. Its session sits in the holders directory
// the holder package names for XDG_STATE_HOME.
func TestHolderDefaultStateDirIsHoldersDir(t *testing.T) {
	fake := holderFakeChild(t)
	chdirTemp(t)
	root := shortStateDir(t)
	t.Setenv("XDG_STATE_HOME", root)
	holders, err := holder.HoldersDir(func(key string) string {
		if key == "XDG_STATE_HOME" {
			return root
		}
		return ""
	})
	if err != nil {
		t.Fatalf("HoldersDir: %v", err)
	}
	socket, err := holder.SocketPath(filepath.Join(holders, "s1"))
	if err != nil {
		t.Fatalf("SocketPath: %v", err)
	}
	hc := &holderCommand{t: t, ch: startOrrery(t, io.Discard, "holder", "--session", "s1", "--", fake), socket: socket}
	t.Cleanup(hc.stopIfServing)
	conn := hc.dial()
	if answer := conn.hello(); answer.Type != holder.TypeHello || answer.Session != "s1" {
		t.Fatalf("answer to hello = %+v, want a hello for session s1", answer)
	}
	if stopped := conn.stop(); stopped.Type != holder.TypeStopped {
		t.Fatalf("answer to stop = %+v, want stopped", stopped)
	}
	if res := hc.result(); res.code != exitOK {
		t.Fatalf("exit status %d, want %d; stderr %q", res.code, exitOK, res.stderr)
	}
}

// TestUsageTextListsHolder lists the holder subcommand and its arguments in the usage text, with its description
// aligned under the others.
func TestUsageTextListsHolder(t *testing.T) {
	want := "\n  holder --session NAME [--state-dir DIR] -- PATH [ARGS...]\n" +
		strings.Repeat(" ", 48) + "run PATH as one session's held child and serve its holder socket\n" +
		strings.Repeat(" ", 48) + "until the child ends\n"
	if !strings.Contains(usageText, want) {
		t.Errorf("usage text does not list holder as %q:\n%s", want, usageText)
	}
}
