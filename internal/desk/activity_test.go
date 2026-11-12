package desk

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/joshsymonds/orrery/internal/tmuxctl"
	"github.com/joshsymonds/orrery/internal/tmuxtest"
)

// noConfig stands in for the user's tmux config so a test server never reads it.
const noConfig = "/dev/null"

func TestNewestActivity(t *testing.T) {
	tests := []struct {
		name string
		out  string
		want int64
		ok   bool
	}{
		{name: "several clients", out: "1700000100\n1700000300\n1700000200\n", want: 1700000300, ok: true},
		{name: "one client", out: "1700000042\n", want: 1700000042, ok: true},
		{name: "no clients", out: "", want: 0, ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok, err := newestActivity(tt.out)
			if err != nil {
				t.Fatalf("newestActivity(%q): %v", tt.out, err)
			}
			if got != tt.want || ok != tt.ok {
				t.Fatalf("newestActivity(%q) = %d, %t; want %d, %t", tt.out, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestNewestActivityMalformed(t *testing.T) {
	for _, out := range []string{"yesterday\n", "1700000000 extra\n", "1700000000\nnot-a-time\n"} {
		if _, _, err := newestActivity(out); err == nil {
			t.Fatalf("newestActivity(%q): got nil error, want one", out)
		}
	}
}

func TestNewestInputFailedTmuxCall(t *testing.T) {
	client, err := tmuxctl.New(tmuxctl.Server{Socket: filepath.Join(t.TempDir(), "missing.sock")})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	input, ok, err := NewestInput(t.Context(), client)
	if err == nil {
		t.Fatalf("NewestInput on a missing server: got nil error, want one")
	}
	if ok || input != 0 {
		t.Fatalf("NewestInput on a missing server = %d, %t; want 0, false", input, ok)
	}
}

func TestNewestInputNoClientOnRealServer(t *testing.T) {
	ctx := t.Context()
	server := tmuxctl.Server{Name: fmt.Sprintf("orrery-desk-activity-%d", os.Getpid())}
	client, err := tmuxctl.New(server)
	if err != nil {
		t.Fatalf("New(%+v): %v", server, err)
	}
	t.Cleanup(func() {
		if err := tmuxtest.Stop(t, server); err != nil {
			t.Errorf("kill-server: %v", err)
		}
	})
	if _, err := client.Run(ctx, "-f", noConfig, "new-session", "-d", "-s", "s1", "sleep 300"); err != nil {
		t.Fatalf("new-session: %v", err)
	}

	input, ok, err := NewestInput(ctx, client)
	if err != nil {
		t.Fatalf("NewestInput: %v", err)
	}
	if ok || input != 0 {
		t.Fatalf("NewestInput with no client attached = %d, %t; want 0, false", input, ok)
	}
}
