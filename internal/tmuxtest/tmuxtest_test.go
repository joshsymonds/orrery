package tmuxtest_test

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/tmuxctl"
	"github.com/joshsymonds/orrery/internal/tmuxtest"
)

// noConfig keeps the user's tmux config out of every test server.
const noConfig = "/dev/null"

// newName returns a unique private server name, so no test shares a server with another.
func newName(t *testing.T) string {
	t.Helper()
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatalf("random server name: %v", err)
	}
	return fmt.Sprintf("orrery-tmuxtest-%d-%x", os.Getpid(), suffix)
}

// startServer starts a detached session on server and stops that server at cleanup.
func startServer(t *testing.T, server tmuxctl.Server) *tmuxctl.Client {
	t.Helper()
	client, err := tmuxctl.New(server)
	if err != nil {
		t.Fatalf("New(%+v): %v", server, err)
	}
	if _, err := client.Run(t.Context(), "-f", noConfig, "new-session", "-d", "-s", "s1", "sleep 300"); err != nil {
		t.Fatalf("new-session: %v", err)
	}
	t.Cleanup(func() { _ = tmuxtest.Stop(t, server) })
	return client
}

func TestStopStarted(t *testing.T) {
	name := newName(t)
	server := tmuxctl.Server{Name: name}
	client := startServer(t, server)

	out, err := client.Run(t.Context(), "display-message", "-p", "#{socket_path}")
	if err != nil {
		t.Fatalf("socket_path: %v", err)
	}
	computed := tmuxtest.SocketPath(name)
	if want := strings.TrimSpace(out); computed != want {
		t.Fatalf("SocketPath(%q) = %q, want tmux socket_path %q", name, computed, want)
	}
	if _, err := os.Stat(computed); err != nil {
		t.Fatalf("socket file before Stop: %v", err)
	}

	if err := tmuxtest.Stop(t, server); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if _, err := os.Stat(computed); !os.IsNotExist(err) {
		t.Fatalf("socket file after Stop: stat err = %v, want not exist", err)
	}
	if _, err := client.Run(t.Context(), "list-sessions"); err == nil {
		t.Fatal("server still answers after Stop")
	}
}

// heldExitScript is a pane's command whose hangup it holds, as a held fake claude can still be writing its files when
// the hangup lands. It takes a directory as $1. Its HUP trap writes hup there, then exits only once release exists. It
// writes ready once the trap is set. It waits on a background sleep, and sh's wait returns when a trapped signal
// comes, so the trap runs at once.
const heldExitScript = `d=$1
trap ': > "$d/hup"; until [ -e "$d/release" ]; do sleep 0.05; done; exit 0' HUP
: > "$d/ready"
while :; do sleep 1000 & wait $!; done`

// TestStopWaitsForPaneProcesses stops a server whose pane holds its exit after the hangup. Stop has not returned while
// the pane's process holds it, and once the process is released and Stop returns, the process is gone: nothing a pane
// ran writes after Stop.
func TestStopWaitsForPaneProcesses(t *testing.T) {
	server := tmuxctl.Server{Name: newName(t)}
	client, err := tmuxctl.New(server)
	if err != nil {
		t.Fatalf("New(%+v): %v", server, err)
	}
	dir := t.TempDir()
	if _, err = client.Run(t.Context(), "-f", noConfig, "new-session", "-d", "-s", "s1", "sh", "-c",
		heldExitScript, "sh", dir); err != nil {
		t.Fatalf("new-session: %v", err)
	}
	out, err := client.Run(t.Context(), "list-panes", "-t", "s1", "-F", "#{pane_pid}")
	if err != nil {
		t.Fatalf("list-panes: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		t.Fatalf("pane_pid %q: %v", out, err)
	}
	release := filepath.Join(dir, "release")
	t.Cleanup(func() {
		_ = os.WriteFile(release, nil, 0o600)
		_ = syscall.Kill(pid, syscall.SIGKILL)
		_ = tmuxtest.Stop(t, server)
	})
	awaitFile(t, filepath.Join(dir, "ready"), pid, nil)

	stopped := make(chan error, 1)
	go func() { stopped <- tmuxtest.Stop(t, server) }()
	awaitFile(t, filepath.Join(dir, "hup"), pid, stopped)
	select {
	case err = <-stopped:
		t.Fatalf("Stop returned %v while the pane's process held its exit", err)
	default:
	}
	if err = os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatalf("release the pane's process: %v", err)
	}
	if err = <-stopped; err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if _, err = os.Stat(filepath.Join("/proc", strconv.Itoa(pid))); !os.IsNotExist(err) {
		t.Fatalf("pane process %d is still in the process table after Stop: stat err = %v", pid, err)
	}
}

// awaitFile waits until path exists, which the pane's process pid writes. It has no deadline of its own, and fails at
// once when pid has left the process table first, or when Stop's result arrives on stopped first.
func awaitFile(t *testing.T, path string, pid int, stopped <-chan error) {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if _, err := os.Stat(filepath.Join("/proc", strconv.Itoa(pid))); os.IsNotExist(err) {
			t.Fatalf("pane process %d exited before it wrote %s", pid, path)
		}
		select {
		case err := <-stopped:
			t.Fatalf("Stop returned %v before the pane's process wrote %s", err, path)
		case <-ticker.C:
		}
	}
}

func TestStopNeverStarted(t *testing.T) {
	name := newName(t)
	if err := tmuxtest.Stop(t, tmuxctl.Server{Name: name}); err == nil {
		t.Fatal("Stop on a never-started server returned nil, want the kill-server error")
	}
	if _, err := os.Stat(tmuxtest.SocketPath(name)); !os.IsNotExist(err) {
		t.Fatalf("socket file for never-started server: stat err = %v, want not exist", err)
	}
}

func TestStopAlreadyDead(t *testing.T) {
	name := newName(t)
	server := tmuxctl.Server{Name: name}
	client := startServer(t, server)

	if _, err := client.Run(t.Context(), "kill-server"); err != nil {
		t.Fatalf("direct kill-server: %v", err)
	}
	computed := tmuxtest.SocketPath(name)
	if _, err := os.Stat(computed); err != nil {
		t.Fatalf("kill-server should leave the socket file behind: %v", err)
	}

	if err := tmuxtest.Stop(t, server); err == nil {
		t.Fatal("Stop on a dead server returned nil, want the kill-server error")
	}
	if _, err := os.Stat(computed); !os.IsNotExist(err) {
		t.Fatalf("socket file after Stop: stat err = %v, want not exist", err)
	}
}

func TestStopZeroServer(t *testing.T) {
	// An empty PATH makes any tmux invocation fail, so a refusal is the only way Stop can return the expected text.
	t.Setenv("PATH", t.TempDir())
	err := tmuxtest.Stop(t, tmuxctl.Server{})
	if err == nil {
		t.Fatal("Stop on a zero Server returned nil, want the refusal")
	}
	if !strings.Contains(err.Error(), "needs a Name or a Socket") {
		t.Fatalf("Stop on a zero Server: error %q, want the refusal", err)
	}
}

func TestStopSocketFlag(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "orrery.sock")
	server := tmuxctl.Server{Socket: socket}
	startServer(t, server)

	if _, err := os.Stat(socket); err != nil {
		t.Fatalf("socket file before Stop: %v", err)
	}
	if err := tmuxtest.Stop(t, server); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if _, err := os.Stat(socket); !os.IsNotExist(err) {
		t.Fatalf("socket file after Stop: stat err = %v, want not exist", err)
	}
}
