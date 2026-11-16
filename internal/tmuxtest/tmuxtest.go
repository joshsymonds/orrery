// Package tmuxtest gives test files one way to find and stop the tmux servers they start. It is imported only by tests.
package tmuxtest

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// SocketPath returns the socket path tmux gives a -L name server. The directory is the symlink-resolved absolute
// TMUX_TMPDIR when it is set and resolves, else the resolved /tmp, else /tmp; the socket is tmux-<uid>/<name> there.
func SocketPath(name string) string {
	dir := resolveDir(os.Getenv("TMUX_TMPDIR"))
	if dir == "" {
		dir = resolveDir("/tmp")
	}
	if dir == "" {
		dir = "/tmp"
	}
	return filepath.Join(dir, fmt.Sprintf("tmux-%d", os.Getuid()), name)
}

// resolveDir returns the absolute, symlink-resolved path of dir, or "" when dir is empty or does not resolve.
func resolveDir(dir string) string {
	if dir == "" {
		return ""
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return ""
	}
	return resolved
}

// paneExitLimit bounds how long Stop waits for a pane's process group to exit after kill-server before it SIGKILLs the
// group. It is a fallback for a pane process that ignores its hangup, not a bound any test asserts.
const paneExitLimit = 2 * time.Second

// Stop kills the server with kill-server, removes its socket file: server.Socket when set, else SocketPath(server.Name),
// and returns once the process group of every pane the server ran is gone. kill-server returns while the panes'
// processes are still ending on their hangup, and one still running, such as a held fake claude, can write into the
// test's TempDir while it is removed. A group still alive after paneExitLimit is SIGKILLed; the wait for a killed group
// has no deadline of its own. A missing socket file is not an error, and any other removal failure fails t. Stop
// returns kill-server's error, so the caller decides whether a server that was already gone is a failure. A Server
// naming neither Name nor Socket is refused before any tmux command runs, because the zero Server would select tmux's
// default server.
func Stop(t *testing.T, server tmuxctl.Server) error {
	t.Helper()
	if server.Name == "" && server.Socket == "" {
		return errors.New("tmuxtest: stop: server needs a Name or a Socket")
	}
	client, err := tmuxctl.New(server)
	if err != nil {
		return fmt.Errorf("tmuxtest: stop: %w", err)
	}
	groups := paneGroups(client)
	_, killErr := client.Run(context.Background(), "kill-server")

	socket := server.Socket
	if socket == "" {
		socket = SocketPath(server.Name)
	}
	if removeErr := os.Remove(socket); removeErr != nil && !errors.Is(removeErr, fs.ErrNotExist) {
		t.Errorf("tmuxtest: remove socket %s: %v", socket, removeErr)
	}
	for _, pgid := range groups {
		if WaitGroupGone(pgid, time.After(paneExitLimit)) {
			continue
		}
		if killGroupErr := syscall.Kill(-pgid, syscall.SIGKILL); killGroupErr != nil &&
			!errors.Is(killGroupErr, syscall.ESRCH) {
			t.Errorf("tmuxtest: kill process group %d: %v", pgid, killGroupErr)
		}
		WaitGroupGone(pgid, nil)
	}
	if killErr != nil {
		return fmt.Errorf("tmuxtest: stop: %w", killErr)
	}
	return nil
}

// paneGroups returns the process group of each pane on client's server, or none when the server does not answer. A
// pane's command leads its own group, so the group id is the pane's pid.
func paneGroups(client *tmuxctl.Client) []int {
	out, err := client.Run(context.Background(), "list-panes", "-a", "-F", "#{pane_pid}")
	if err != nil {
		return nil
	}
	var groups []int
	for _, field := range strings.Fields(out) {
		pgid, parseErr := strconv.Atoi(field)
		// A pane with no process reports 0, and kill(-0) would signal the test's own group.
		if parseErr == nil && pgid > 1 {
			groups = append(groups, pgid)
		}
	}
	return groups
}

// WaitGroupGone reports whether process group pgid has no process left before giveUp fires. A nil giveUp never
// fires, so the wait lasts until the group is gone.
func WaitGroupGone(pgid int, giveUp <-chan time.Time) bool {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for !errors.Is(syscall.Kill(-pgid, 0), syscall.ESRCH) {
		select {
		case <-giveUp:
			return false
		case <-ticker.C:
		}
	}
	return true
}
