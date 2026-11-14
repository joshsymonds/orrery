package integration_test

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// readyDatagram is the one datagram orreryd sends systemd once it serves.
const readyDatagram = "READY=1"

// readyFailedLog is the message orreryd logs when its READY=1 send fails.
const readyFailedLog = "notify systemd, serving anyway"

// drainLimit is how long a drain waits for a datagram once the queue is empty. A drain runs after the daemon exits, so
// every datagram the daemon sent is already queued.
const drainLimit = 100 * time.Millisecond

// notifySocket is a unixgram socket a test binds as NOTIFY_SOCKET, the way systemd's notify socket receives READY=1.
type notifySocket struct {
	path string
	conn *net.UnixConn
}

// bindNotifySocket binds a notify socket at a path under dir and closes it at cleanup.
func bindNotifySocket(t *testing.T, dir string) *notifySocket {
	t.Helper()
	path := filepath.Join(dir, "notify.sock")
	conn, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: path, Net: "unixgram"})
	if err != nil {
		t.Fatalf("bind notify socket: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Errorf("close notify socket: %v", closeErr)
		}
	})
	return &notifySocket{path: path, conn: conn}
}

// read returns the next datagram, failing the test unless one arrives within waitLimit.
func (s *notifySocket) read(t *testing.T) string {
	t.Helper()
	if err := s.conn.SetReadDeadline(time.Now().Add(waitLimit)); err != nil {
		t.Fatalf("set notify read deadline: %v", err)
	}
	datagram, err := s.readOne()
	if err != nil {
		t.Fatalf("read notify datagram: %v", err)
	}
	return datagram
}

// drain returns every datagram still queued on the socket, in arrival order. It stops at the first read that finds
// the queue empty for drainLimit.
func (s *notifySocket) drain(t *testing.T) []string {
	t.Helper()
	var got []string
	for {
		if err := s.conn.SetReadDeadline(time.Now().Add(drainLimit)); err != nil {
			t.Fatalf("set notify drain deadline: %v", err)
		}
		datagram, err := s.readOne()
		if errors.Is(err, os.ErrDeadlineExceeded) {
			return got
		}
		if err != nil {
			t.Fatalf("drain notify socket: %v", err)
		}
		got = append(got, datagram)
	}
}

// readOne reads one datagram under the socket's current deadline.
func (s *notifySocket) readOne() (string, error) {
	buf := make([]byte, 512)
	n, _, err := s.conn.ReadFromUnix(buf)
	return string(buf[:n]), err
}

// assertAccepts fails the test unless the unix socket at path accepts a connection now.
func assertAccepts(t *testing.T, path string) {
	t.Helper()
	conn, err := net.Dial("unix", path)
	if err != nil {
		t.Fatalf("%s refused a connection: %v", path, err)
	}
	if err = conn.Close(); err != nil {
		t.Fatalf("close probe connection to %s: %v", path, err)
	}
}

// notifyEnv returns the fixture's environment with NOTIFY_SOCKET set to path.
func (f *engineFixture) notifyEnv(path string) []string {
	return append(slices.Clone(f.env), "NOTIFY_SOCKET="+path)
}

// startReady starts orreryd on the fixture's tmux server with env, running claude, and with the session service when
// sessions is set. It waits only for the daemon socket, so a test that reads READY=1 does not wait for sessions.sock.
func (f *engineFixture) startReady(t *testing.T, env []string, claude string, sessions bool) *orreryProcess {
	t.Helper()
	args := []string{"-L", f.server, "--socket", f.socket, "--claude", claude}
	if sessions {
		args = append(args, "--sessions", "--session-shell", f.shell)
	}
	daemon := startBinary(t, "orreryd", env, args...)
	daemon.waitForListener(t, "unix", f.socket)
	return daemon
}

// hasEnvVar reports whether recorded, a NAME=value line per variable, holds the variable name.
func hasEnvVar(recorded, name string) bool {
	return slices.ContainsFunc(strings.Split(recorded, "\n"), func(line string) bool {
		return strings.HasPrefix(line, name+"=")
	})
}

// TestOrrerydReadyWithSessions proves that orreryd with --sessions sends READY=1 once, and that sessions.sock accepts a
// connection when the datagram arrives.
func TestOrrerydReadyWithSessions(t *testing.T) {
	t.Parallel()
	f := newEngineFixture(t)
	notify := bindNotifySocket(t, f.root)
	daemon := f.startReady(t, f.notifyEnv(notify.path), f.fakeClaude, true)

	if got := notify.read(t); got != readyDatagram {
		t.Fatalf("datagram %q, want %q", got, readyDatagram)
	}
	assertAccepts(t, f.sessions)
	wantExit(t, daemon.stop(t), 0)
	if extra := notify.drain(t); len(extra) != 0 {
		t.Fatalf("datagrams after %s: %q, want none", readyDatagram, extra)
	}
}

// TestOrrerydReadyWithoutSessions proves that orreryd without --sessions sends READY=1 once after its store opens, and
// once after the store fails to open.
func TestOrrerydReadyWithoutSessions(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		store   string
		wantLog string
	}{
		{name: "working store", wantLog: storeOpenLog},
		{name: "store that fails to open", store: corruptStoreBytes, wantLog: storeUnavailableLog},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newEngineFixture(t)
			if tc.store != "" {
				if err := os.MkdirAll(filepath.Dir(f.dbPath), 0o700); err != nil {
					t.Fatalf("make store directory: %v", err)
				}
				writeFile(t, f.dbPath, tc.store, 0o600)
			}
			notify := bindNotifySocket(t, f.root)
			daemon := f.startReady(t, f.notifyEnv(notify.path), f.fakeClaude, false)

			if got := notify.read(t); got != readyDatagram {
				t.Fatalf("datagram %q, want %q", got, readyDatagram)
			}
			out := daemon.stop(t)
			wantExit(t, out, 0)
			if !strings.Contains(out.stderr, tc.wantLog) {
				t.Fatalf("stderr %q, want %q", out.stderr, tc.wantLog)
			}
			if extra := notify.drain(t); len(extra) != 0 {
				t.Fatalf("datagrams after %s: %q, want none", readyDatagram, extra)
			}
		})
	}
}

// TestOrrerydReadyFailedSendServesAnyway proves that a READY=1 send to a socket nobody listens on is logged, and that
// orreryd keeps serving.
func TestOrrerydReadyFailedSendServesAnyway(t *testing.T) {
	t.Parallel()
	f := newEngineFixture(t)
	missing := filepath.Join(f.root, "no-listener.sock")
	daemon := f.startReady(t, f.notifyEnv(missing), f.fakeClaude, false)

	waitUntil(t, "the failed READY=1 log", func() bool {
		return strings.Contains(daemon.stderr.String(), readyFailedLog)
	})
	daemon.waitForListener(t, "unix", f.socket)
	wantExit(t, daemon.stop(t), 0)
}

// TestOrrerydReadyEmptySocketSendsNothing proves that an empty NOTIFY_SOCKET names no socket: orreryd serves and
// logs no failed send.
func TestOrrerydReadyEmptySocketSendsNothing(t *testing.T) {
	t.Parallel()
	f := newEngineFixture(t)
	daemon := f.startReady(t, f.notifyEnv(""), f.fakeClaude, false)
	daemon.waitForStoreOpen(t)

	out := daemon.stop(t)
	wantExit(t, out, 0)
	if strings.Contains(out.stderr, readyFailedLog) {
		t.Fatalf("stderr %q: an empty NOTIFY_SOCKET must send nothing", out.stderr)
	}
}

// TestOrrerydReadyFailedSessionsStartSendsNothing proves that a --sessions orreryd whose session socket cannot be
// listened on exits without sending READY=1.
func TestOrrerydReadyFailedSessionsStartSendsNothing(t *testing.T) {
	t.Parallel()
	f := newEngineFixture(t)
	held, err := net.Listen("unix", f.sessions)
	if err != nil {
		t.Fatalf("hold session socket: %v", err)
	}
	t.Cleanup(func() { _ = held.Close() })
	notify := bindNotifySocket(t, f.root)

	args := []string{
		"-L", f.server, "--socket", f.socket, "--claude", f.fakeClaude,
		"--sessions", "--session-shell", f.shell,
	}
	daemon := startBinary(t, "orreryd", f.notifyEnv(notify.path), args...)
	out := daemon.exitOutcome(t)
	if out.code == 0 {
		t.Fatalf("exit 0, want a non-zero exit when the session socket is held; stderr %q", out.stderr)
	}
	if extra := notify.drain(t); len(extra) != 0 {
		t.Fatalf("datagrams from a failed sessions start: %q, want none", extra)
	}
}

// TestOrrerydReadyKeepsNotifySocketFromChildren proves that a session's claude does not inherit NOTIFY_SOCKET, though
// orreryd was started with it set.
func TestOrrerydReadyKeepsNotifySocketFromChildren(t *testing.T) {
	t.Parallel()
	f := newEngineFixture(t)
	notify := bindNotifySocket(t, f.root)
	dir := f.sessionDir(t, "wrapped")
	recorded := filepath.Join(dir, "child.env")
	wrapper := filepath.Join(f.root, "claude-wrapper.sh")
	writeFile(t, wrapper, "#!/bin/sh\nenv > '"+recorded+"'\nexec '"+f.fakeClaude+"' \"$@\"\n", 0o700)
	daemon := f.startReady(t, f.notifyEnv(notify.path), wrapper, true)
	daemon.waitForStoreOpen(t)
	daemon.waitForListener(t, "unix", f.sessions)

	id := f.start(t, dir, "hello")
	waitUntil(t, "the wrapper to record its environment", func() bool {
		raw, err := os.ReadFile(recorded)
		return err == nil && hasEnvVar(string(raw), "HOME")
	})
	raw, err := os.ReadFile(recorded)
	if err != nil {
		t.Fatalf("read recorded environment: %v", err)
	}
	if hasEnvVar(string(raw), "NOTIFY_SOCKET") {
		t.Fatalf("session claude inherited NOTIFY_SOCKET; environment %q", raw)
	}
	f.stopSession(t, id, dir)
	wantExit(t, daemon.stop(t), 0)
}
