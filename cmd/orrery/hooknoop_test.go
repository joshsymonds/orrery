package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
)

// hookNoopInput is a Stop event, the stdin of a plain `orrery hook` that a Claude Code pane runs.
const hookNoopInput = `{"hook_event_name":"Stop","session_id":"session-7"}`

// hookNoopSentinel is what the sentinel connection of TestHookNoopMakesNoConnection sends, so that a connection the hook
// made is told from the sentinel.
const hookNoopSentinel = "sentinel"

// listenHookNoop listens on a fresh unix socket in the test's temp directory, the daemon socket that `--socket` names.
func listenHookNoop(t *testing.T) (string, net.Listener) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "orreryd.sock")
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "unix", path)
	if err != nil {
		t.Fatalf("listen %s: %v", path, err)
	}
	t.Cleanup(func() {
		if err := ln.Close(); err != nil {
			t.Errorf("close daemon socket: %v", err)
		}
	})
	return path, ln
}

// runHookNoop runs `orrery --socket SOCKET hook` in a pane, with stdin as its standard input, and returns its exit code
// and what it wrote to stdout and stderr.
func runHookNoop(t *testing.T, socket string, stdin *os.File) (int, string, string) {
	t.Helper()
	getenv := envFrom(map[string]string{
		"TMUX_PANE":          "%7",
		"ANTHROPIC_BASE_URL": "https://proxy.example.com/ctx/example-context",
	})
	var stdout, stderr bytes.Buffer
	code := run([]string{"--socket", socket, "hook"}, stdin, &stdout, &stderr, getenv)
	return code, stdout.String(), stderr.String()
}

// TestHookNoopReadsStdinToEnd checks that plain `orrery hook` reads its stdin to the end.
func TestHookNoopReadsStdinToEnd(t *testing.T) {
	t.Parallel()
	socket, _ := listenHookNoop(t)
	stdin := hookStdin(t, hookNoopInput)
	runHookNoop(t, socket, stdin)
	pos, err := stdin.Seek(0, io.SeekCurrent)
	if err != nil {
		t.Fatalf("seek stdin: %v", err)
	}
	if pos != int64(len(hookNoopInput)) {
		t.Fatalf("hook read %d of %d bytes of stdin, want all of them", pos, len(hookNoopInput))
	}
}

// TestHookNoopExitsZeroSilently checks that plain `orrery hook` exits 0 and writes nothing to stdout or stderr.
func TestHookNoopExitsZeroSilently(t *testing.T) {
	t.Parallel()
	socket, _ := listenHookNoop(t)
	code, stdout, stderr := runHookNoop(t, socket, hookStdin(t, hookNoopInput))
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr %q", code, stderr)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want nothing", stdout)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want nothing", stderr)
	}
}

// TestHookNoopMakesNoConnection checks that plain `orrery hook` connects to no daemon socket. The hook runs first, then a
// sentinel connects and sends hookNoopSentinel. Connections queue in the order they arrive, so a connection the hook made
// would be accepted before the sentinel, and its read would not be the sentinel.
func TestHookNoopMakesNoConnection(t *testing.T) {
	t.Parallel()
	socket, ln := listenHookNoop(t)
	runHookNoop(t, socket, hookStdin(t, hookNoopInput))

	var dialer net.Dialer
	sentinel, err := dialer.DialContext(t.Context(), "unix", socket)
	if err != nil {
		t.Fatalf("dial the sentinel: %v", err)
	}
	if _, err = sentinel.Write([]byte(hookNoopSentinel)); err != nil {
		t.Fatalf("write the sentinel: %v", err)
	}
	if err = sentinel.Close(); err != nil {
		t.Fatalf("close the sentinel: %v", err)
	}

	conn, err := ln.Accept()
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Errorf("close accepted connection: %v", err)
		}
	})
	got, err := io.ReadAll(conn)
	if err != nil {
		t.Fatalf("read the first connection: %v", err)
	}
	if string(got) != hookNoopSentinel {
		t.Fatalf("the daemon's first connection carried %q, want the sentinel %q: the hook connected", got, hookNoopSentinel)
	}
}
