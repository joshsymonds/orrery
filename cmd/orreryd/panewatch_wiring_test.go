package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/deskstream"
	"github.com/joshsymonds/orrery/internal/notice"
	"github.com/joshsymonds/orrery/internal/paneregistry"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
	"github.com/joshsymonds/orrery/internal/waittest"
)

// Invented session facts. The registry entry, its session and its working directory are the test's own.
const (
	paneWatchSession = "synthetic-session"
	paneWatchCwd     = "/synthetic/project"
	paneWatchEntry   = "synthetic-entry.json"
)

// paneWatchEnv returns the getenv of a serve whose HOME is home, with state and config directories of the test's own.
func paneWatchEnv(t *testing.T, home string) func(string) string {
	t.Helper()
	env := map[string]string{
		"HOME":            home,
		"XDG_STATE_HOME":  t.TempDir(),
		"XDG_CONFIG_HOME": t.TempDir(),
	}
	return func(key string) string { return env[key] }
}

// paneWatchServe runs serve with HOME set to home, on a socket of the test's own, until the test ends. It returns the
// socket, and a channel that closes once serve has returned. The test fails when serve returns an error.
func paneWatchServe(t *testing.T, home string) (string, <-chan struct{}) {
	t.Helper()
	getenv := paneWatchEnv(t, home)
	socket := filepath.Join(t.TempDir(), "orreryd.sock")
	opts, err := parseFlags([]string{"--socket", socket}, getenv)
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	served := make(chan error, 1)
	go func() { served <- serve(ctx, opts, slog.New(slog.DiscardHandler), getenv) }()
	ended, result := waittest.Closed(served)
	t.Cleanup(func() {
		cancel()
		if err := <-result; err != nil {
			t.Errorf("serve: %v", err)
		}
	})
	return socket, ended
}

// paneWatchDesk connects to the desk stream of the daemon on socket once it listens, reads the stream's hello, and
// returns a channel that takes each later line. The channel closes when the stream ends.
func paneWatchDesk(t *testing.T, socket string, ended <-chan struct{}) <-chan deskstream.Line {
	t.Helper()
	var conn net.Conn
	waittest.Until(t, ended, "the desk stream listens", func() bool {
		c, err := net.Dial("unix", deskstream.SocketPath(socket))
		if err == nil {
			conn = c
		}
		return err == nil
	})
	t.Cleanup(func() { _ = conn.Close() })
	decoder := json.NewDecoder(conn)
	var hello deskstream.Line
	if err := decoder.Decode(&hello); err != nil {
		t.Fatalf("read desk hello: %v", err)
	}
	if hello.Kind != deskstream.KindHello {
		t.Fatalf("first desk line kind = %q, want %q", hello.Kind, deskstream.KindHello)
	}
	lines := make(chan deskstream.Line, 16)
	go func() {
		defer close(lines)
		for {
			var line deskstream.Line
			if err := decoder.Decode(&line); err != nil {
				return
			}
			lines <- line
		}
	}()
	return lines
}

// TestPaneWatchWiringCreatesSessionsDir checks that serve creates the session registry directory under an invented HOME
// when it is absent, with mode 0700.
func TestPaneWatchWiringCreatesSessionsDir(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".claude", "sessions")
	_, ended := paneWatchServe(t, home)
	waittest.Until(t, ended, "the session registry directory exists", func() bool {
		_, err := os.Stat(dir)
		return err == nil
	})
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat %s: %v", dir, err)
	}
	if !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Errorf("%s is a directory: %v with mode %v, want a directory with mode 0700",
			dir, info.IsDir(), info.Mode().Perm())
	}
}

// paneWatchSessionProcess starts a process that stands for a Claude session in pane of client's tmux server. Its
// environment is the one a session has in a pane: TMUX names the server's socket, its pid and session 0, and TMUX_PANE
// names the pane. The process ends when the test does, and its pid is returned.
func paneWatchSessionProcess(t *testing.T, client *tmuxctl.Client, pane string) int {
	t.Helper()
	socket, err := client.Run(t.Context(), "display", "-p", "#{socket_path}")
	if err != nil {
		t.Fatalf("display socket_path: %v", err)
	}
	serverPID, err := client.Run(t.Context(), "display", "-p", "#{pid}")
	if err != nil {
		t.Fatalf("display pid: %v", err)
	}
	cmd := exec.Command("sleep", "300")
	cmd.Env = []string{
		"TMUX=" + strings.TrimSpace(socket) + "," + strings.TrimSpace(serverPID) + ",0",
		"TMUX_PANE=" + pane,
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start session process: %v", err)
	}
	t.Cleanup(func() {
		if err := cmd.Process.Kill(); err != nil {
			t.Errorf("kill session process: %v", err)
		}
		// The killed process exits with a signal, which is how it ends, so only a failure to wait is reported.
		var exit *exec.ExitError
		if err := cmd.Wait(); err != nil && !errors.As(err, &exit) {
			t.Errorf("wait for session process: %v", err)
		}
	})
	return cmd.Process.Pid
}

// TestPaneWatchWiringEntryReachesUpdates checks that a registry entry written under an invented HOME reaches the daemon's
// pane updates. The entry's process is a session in a pane of a tmux server the test starts, and the pane reads working
// once serve has applied the update.
func TestPaneWatchWiringEntryReachesUpdates(t *testing.T) {
	_, client, pane := startPane(t, t.TempDir())
	pid := paneWatchSessionProcess(t, client, pane)
	entry, err := json.Marshal(paneregistry.Entry{
		PID:        pid,
		SessionID:  paneWatchSession,
		Cwd:        paneWatchCwd,
		Entrypoint: paneregistry.EntrypointCLI,
		Status:     paneregistry.StatusBusy,
	})
	if err != nil {
		t.Fatalf("marshal entry: %v", err)
	}
	home := t.TempDir()
	dir := filepath.Join(home, ".claude", "sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("create %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, paneWatchEntry), entry, 0o600); err != nil {
		t.Fatalf("write entry: %v", err)
	}
	_, ended := paneWatchServe(t, home)
	waittest.Until(t, ended, "the pane reads working", func() bool {
		state, ok, err := client.PaneOption(t.Context(), pane, protocol.OptionState)
		return err == nil && ok && state == string(protocol.StateWorking)
	})
}

// TestPaneWatchWiringUnreadableEntryNotices checks that a registry entry the pane watch cannot parse reaches the desk as
// a notice titled with the session registry and bodied with the entry's path and its parse error.
func TestPaneWatchWiringUnreadableEntryNotices(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".claude", "sessions", paneWatchEntry)
	// orreryd makes the registry directory as its pane watch starts, which can be after the first write below, so the
	// test makes it first; orreryd's make of a directory already there succeeds.
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("make the registry directory: %v", err)
	}
	socket, ended := paneWatchServe(t, home)
	lines := paneWatchDesk(t, socket, ended)
	var got deskstream.Line
	// The watch reports an entry only once it fails to read twice, and it may not be watching yet, so each check writes
	// the unparsable entry again until the notice arrives.
	waittest.Until(t, ended, "the registry notice reaches the desk", func() bool {
		if err := os.WriteFile(path, []byte("synthetic not an entry"), 0o600); err != nil {
			t.Fatalf("write entry: %v", err)
		}
		select {
		case line, open := <-lines:
			if !open {
				t.Fatalf("the desk stream ended before the registry notice")
			}
			if line.Kind != deskstream.KindNotice {
				return false
			}
			got = line
			return true
		default:
			return false
		}
	})
	if got.Title != "Claude session registry" {
		t.Errorf("notice title = %q, want %q", got.Title, "Claude session registry")
	}
	if !strings.Contains(got.Body, path) {
		t.Errorf("notice body %q does not name %s", got.Body, path)
	}
	if !strings.Contains(got.Body, "paneregistry: entry is not a JSON object") {
		t.Errorf("notice body %q does not name the parse error", got.Body)
	}
}

// TestPaneWatchWiringRegistryNoticeKey checks that the notice of an unreadable registry file is keyed as an alert named
// by the file's base name, is firing, and pushes a summary that names the file and the error.
func TestPaneWatchWiringRegistryNoticeKey(t *testing.T) {
	path := "/synthetic/home/.claude/sessions/" + paneWatchEntry
	cause := errors.New("synthetic parse error")
	got := registryNotice(path, cause)
	if want := (notice.Key{Alert: "claude registry " + paneWatchEntry}); got.Key != want {
		t.Errorf("key = %+v, want %+v", got.Key, want)
	}
	if got.State != "firing" {
		t.Errorf("state = %q, want firing", got.State)
	}
	note, err := got.Push(t.Context())
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	if !strings.Contains(note.Summary, path) || !strings.Contains(note.Summary, cause.Error()) {
		t.Errorf("push summary %q does not name %s and %q", note.Summary, path, cause)
	}
}
