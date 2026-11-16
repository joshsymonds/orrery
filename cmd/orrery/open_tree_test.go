package main

import (
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// lineWriter sends each write on lines, so a test waits for a writer's first line without polling.
type lineWriter struct {
	lines chan string
}

// Write sends p on lines.
func (w lineWriter) Write(p []byte) (int, error) {
	w.lines <- string(p)
	return len(p), nil
}

// TestOpenAttachedTreeWritesPopupError opens the tree on a client tty the server does not list, as when a client is
// gone before its popup opens. The popup command fails, and its error reaches stderr, so a tree that never came says
// why. The wait for the line ends when tmux answers the popup command.
func TestOpenAttachedTreeWritesPopupError(t *testing.T) {
	t.Parallel()
	tmuxServer := serveNewTmux(t)
	serveStartPane(t, tmuxServer)
	client, err := tmuxctl.New(tmuxServer)
	if err != nil {
		t.Fatalf("tmuxctl.New: %v", err)
	}
	stderr := lineWriter{lines: make(chan string, 1)}
	if err = openAttachedTree(t.Context(), client, "/dev/orrery-test-no-such-client", stderr); err != nil {
		t.Fatalf("openAttachedTree: %v", err)
	}
	if line := <-stderr.lines; !strings.HasPrefix(line, openTreeErrorPrefix) || !strings.HasSuffix(line, "\n") {
		t.Fatalf("stderr line = %q, want one line starting %q", line, openTreeErrorPrefix)
	}
}
