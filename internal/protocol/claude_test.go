package protocol_test

import (
	"testing"

	"github.com/joshsymonds/orrery/internal/protocol"
)

func TestOptionCommandName(t *testing.T) {
	if protocol.OptionCommand != "@orrery_command" {
		t.Fatalf("OptionCommand = %q, want @orrery_command", protocol.OptionCommand)
	}
}

// TestClaudeGone pins the liveness rule: a recorded name that is not a shell must still be the pane's foreground, and
// with no such record a shell in the foreground means Claude is gone.
func TestClaudeGone(t *testing.T) {
	cases := []struct {
		name              string
		current, recorded string
		want              bool
	}{
		{"recorded claude still runs", "claude", "claude", false},
		{"recorded node still runs", "node", "node", false},
		{"recorded claude died to its shell", "zsh", "claude", true},
		{"recorded claude died, the shell now runs vim", "vim", "claude", true},
		{"no record, a shell in the foreground", "zsh", "", true},
		{"no record, a login shell's name", "bash", "", true},
		{"no record, a program in the foreground", "cat", "", false},
		{"no record, claude in the foreground", "claude", "", false},
		{"a recorded shell is no record: shell in the foreground", "zsh", "zsh", true},
		{"a recorded shell is no record: claude in the foreground", "claude", "zsh", false},
		{"pane whose command tmux cannot read", "", "claude", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := protocol.ClaudeGone(tc.current, tc.recorded); got != tc.want {
				t.Fatalf("ClaudeGone(%q, %q) = %v, want %v", tc.current, tc.recorded, got, tc.want)
			}
		})
	}
}
