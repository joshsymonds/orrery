// Package tmuxtest gives test files one way to find and stop the tmux servers they start. It is imported only by tests.
package tmuxtest

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

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

// Stop kills the server with kill-server, then removes its socket file: server.Socket when set, else SocketPath(server.Name).
// A missing socket file is not an error, and any other removal failure fails t. Stop returns kill-server's error, so
// the caller decides whether a server that was already gone is a failure. A Server naming neither Name nor Socket is
// refused before any tmux command runs, because the zero Server would select tmux's default server.
func Stop(t *testing.T, server tmuxctl.Server) error {
	t.Helper()
	if server.Name == "" && server.Socket == "" {
		return errors.New("tmuxtest: stop: server needs a Name or a Socket")
	}
	client, err := tmuxctl.New(server)
	if err != nil {
		return fmt.Errorf("tmuxtest: stop: %w", err)
	}
	_, killErr := client.Run(context.Background(), "kill-server")

	socket := server.Socket
	if socket == "" {
		socket = SocketPath(server.Name)
	}
	if removeErr := os.Remove(socket); removeErr != nil && !errors.Is(removeErr, fs.ErrNotExist) {
		t.Errorf("tmuxtest: remove socket %s: %v", socket, removeErr)
	}
	if killErr != nil {
		return fmt.Errorf("tmuxtest: stop: %w", killErr)
	}
	return nil
}
