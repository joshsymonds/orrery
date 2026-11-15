package cli_test

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/cli"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
	"github.com/joshsymonds/orrery/internal/tmuxtest"
)

// panesServerName returns a unique private server name, so no test shares a server with another.
func panesServerName(t *testing.T) string {
	t.Helper()
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatalf("random server name: %v", err)
	}
	return fmt.Sprintf("orrery-panes-%d-%x", os.Getpid(), suffix)
}

// panesNewServer returns a client for a unique private server name and kills that server at cleanup.
func panesNewServer(t *testing.T) *tmuxctl.Client {
	t.Helper()
	name := panesServerName(t)
	client, err := tmuxctl.New(tmuxctl.Server{Name: name})
	if err != nil {
		t.Fatalf("New(Name=%q): %v", name, err)
	}
	t.Cleanup(func() {
		if err := tmuxtest.Stop(t, tmuxctl.Server{Name: name}); err != nil {
			t.Errorf("kill-server: %v", err)
		}
	})
	return client
}

func TestPanesStampsHostAndOrreryOptions(t *testing.T) {
	t.Parallel()
	client := panesNewServer(t)
	if _, err := client.Run(t.Context(), "-f", "/dev/null", "new-session", "-d", "-s", "example",
		"sleep 300"); err != nil {
		t.Fatalf("new-session: %v", err)
	}
	out, err := client.Run(t.Context(), "list-panes", "-t", "example", "-F", "#{pane_id}")
	if err != nil {
		t.Fatalf("list-panes: %v", err)
	}
	pane := strings.TrimSpace(out)
	if err := client.SetPaneOption(t.Context(), pane, protocol.OptionID, "example-orrery-id"); err != nil {
		t.Fatalf("set %s: %v", protocol.OptionID, err)
	}
	if err := client.SetPaneOption(t.Context(), pane, protocol.OptionTranscript,
		"/home/user/project/example.jsonl"); err != nil {
		t.Fatalf("set %s: %v", protocol.OptionTranscript, err)
	}
	if err := client.SetPaneOption(t.Context(), pane, protocol.OptionTurnStart, "1700000500"); err != nil {
		t.Fatalf("set %s: %v", protocol.OptionTurnStart, err)
	}

	host, err := os.Hostname()
	if err != nil {
		t.Fatalf("Hostname: %v", err)
	}
	panes, err := cli.Panes(t.Context(), client)
	if err != nil {
		t.Fatalf("Panes: %v", err)
	}
	if len(panes) != 1 {
		t.Fatalf("Panes returned %d panes, want 1: %+v", len(panes), panes)
	}
	got := panes[0]
	if got.ID != pane {
		t.Errorf("pane ID = %q, want %q", got.ID, pane)
	}
	if got.Host != host {
		t.Errorf("pane host = %q, want os.Hostname() %q", got.Host, host)
	}
	if got.OrreryID != "example-orrery-id" {
		t.Errorf("OrreryID = %q, want %q", got.OrreryID, "example-orrery-id")
	}
	if got.Transcript != "/home/user/project/example.jsonl" {
		t.Errorf("Transcript = %q, want %q", got.Transcript, "/home/user/project/example.jsonl")
	}
	if got.TurnStart != 1700000500 {
		t.Errorf("TurnStart = %d, want 1700000500", got.TurnStart)
	}
}

func TestPanesNotRunningReturnsNoPanes(t *testing.T) {
	t.Parallel()
	client, err := tmuxctl.New(tmuxctl.Server{Name: panesServerName(t)})
	if err != nil {
		t.Fatalf("New(Name): %v", err)
	}

	panes, err := cli.Panes(t.Context(), client)
	if err != nil {
		t.Fatalf("Panes on a server that is not running: %v", err)
	}
	if len(panes) != 0 {
		t.Fatalf("Panes on a server that is not running = %+v, want no panes", panes)
	}
}

func TestPanesReturnsOtherTmuxErrors(t *testing.T) {
	t.Parallel()
	// A socket path under a regular file makes tmux fail with "(Not a directory)", which is not a server
	// that is not running.
	parent := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(parent, nil, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	client, err := tmuxctl.New(tmuxctl.Server{Socket: filepath.Join(parent, "orrery.sock")})
	if err != nil {
		t.Fatalf("New(Socket): %v", err)
	}

	panes, err := cli.Panes(t.Context(), client)
	if err == nil {
		t.Fatal("Panes with an unreachable server path: want error, got nil")
	}
	if !strings.Contains(err.Error(), "error connecting to") {
		t.Fatalf("error %q does not carry the tmux connection error", err)
	}
	if len(panes) != 0 {
		t.Fatalf("Panes returned %+v with an error, want no panes", panes)
	}
}
