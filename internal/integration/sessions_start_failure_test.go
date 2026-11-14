package integration_test

import (
	"net"
	"strings"
	"testing"
)

// sessionStartFailureLog is the prefix orreryd's error carries when the session socket cannot be listened on.
const sessionStartFailureLog = "session socket"

// TestOrrerydExitsWhenSessionSocketIsHeld proves that orreryd with --sessions ends with a non-zero exit and the session
// socket's error when sessions.sock cannot be listened on. A live listener already holds the path, so the session
// service fails to start, and orreryd must stop serving hooks rather than run on without its sessions.
func TestOrrerydExitsWhenSessionSocketIsHeld(t *testing.T) {
	t.Parallel()
	f := newEngineFixture(t)
	held, err := net.Listen("unix", f.sessions)
	if err != nil {
		t.Fatalf("hold session socket: %v", err)
	}
	t.Cleanup(func() { _ = held.Close() })

	args := []string{
		"-L", f.server, "--socket", f.socket, "--claude", f.fakeClaude,
		"--sessions", "--session-shell", f.shell,
	}
	daemon := startBinary(t, "orreryd", f.env, args...)
	out := daemon.exitOutcome(t)
	if out.code == 0 {
		t.Fatalf("exit 0, want a non-zero exit when the session socket is held; stderr %q", out.stderr)
	}
	if !strings.Contains(out.stderr, sessionStartFailureLog) {
		t.Fatalf("stderr %q, want the session socket error", out.stderr)
	}
}
