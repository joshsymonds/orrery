package summary_test

import (
	"path/filepath"
	"testing"

	"github.com/joshsymonds/orrery/internal/protocol"
)

// TestStepMemoryExcluded checks that a pane summary call names the user CLAUDE.md under claudeMdExcludes, the one the
// claude under its HOME and CLAUDE_CONFIG_DIR loads, and that a call whose path cannot resolve runs no claude.
func TestStepMemoryExcluded(t *testing.T) {
	t.Run("a pane summary call's settings name it", func(t *testing.T) {
		r := newRig(t, nil, 0)
		configDir := t.TempDir()
		t.Setenv("CLAUDE_CONFIG_DIR", configDir)
		r.place(t, "reply", validReply)
		if err := r.s.Summarize(t.Context(), r.job(false)); err != nil {
			t.Fatalf("Summarize: %v", err)
		}
		memory := filepath.Join(configDir, "CLAUDE.md")
		want := argvFor(
			protocol.DefaultSummaryModel,
			`{"disableAllHooks":true,"env":{"ANTHROPIC_BASE_URL":"`+exampleBaseURL+`"},"claudeMdExcludes":["`+memory+`"]}`,
			exampleSystem,
		)
		if got := onlyCall(t, r.bin).args; got != want {
			t.Fatalf("argv = %q, want %q", got, want)
		}
	})

	t.Run("a pane summary call whose user CLAUDE.md path cannot resolve runs no claude", func(t *testing.T) {
		r := newRig(t, nil, 0)
		t.Setenv("HOME", "")
		t.Setenv("CLAUDE_CONFIG_DIR", "")
		r.place(t, "reply", validReply)
		if err := r.s.Summarize(t.Context(), r.job(false)); err == nil {
			t.Fatal("Summarize with no HOME returned nil error, want a refusal naming the user CLAUDE.md")
		}
		if calls := recordedCalls(t, r.bin); len(calls) != 0 {
			t.Fatalf("claude ran %d times, want none", len(calls))
		}
	})
}
