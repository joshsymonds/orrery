package open

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestStepMemoryExcluded checks that the picker's intent call names the user CLAUDE.md under claudeMdExcludes, the one
// the claude under its CLAUDE_CONFIG_DIR loads, that a call whose path cannot resolve runs no claude, and that the
// claude argv a pane launcher builds through stampedArgv carries no claudeMdExcludes.
func TestStepMemoryExcluded(t *testing.T) {
	t.Run("the picker's intent call's settings name it", func(t *testing.T) {
		configDir := t.TempDir()
		t.Setenv("HOME", t.TempDir())
		t.Setenv("CLAUDE_CONFIG_DIR", configDir)
		root := t.TempDir()
		makeDirs(t, root, 1)
		claude := installClaude(t, "pane d1\n", 0)

		if _, err := Intent(t.Context(), intentConfig(newClient(t), claude, root), "example text"); err != nil {
			t.Fatalf("Intent: %v", err)
		}
		runs := claudeRuns(t, claude)
		if len(runs) != 1 {
			t.Fatalf("claude ran %d times, want 1", len(runs))
		}
		want := `{"disableAllHooks":true,"claudeMdExcludes":["` + filepath.Join(configDir, "CLAUDE.md") + `"]}`
		if got := runs[0].args[slices.Index(runs[0].args, "--settings")+1]; got != want {
			t.Fatalf("intent --settings = %s, want %s", got, want)
		}
	})

	t.Run("the picker's intent call whose user CLAUDE.md path cannot resolve runs no claude", func(t *testing.T) {
		t.Setenv("HOME", "")
		t.Setenv("CLAUDE_CONFIG_DIR", "")
		root := t.TempDir()
		makeDirs(t, root, 1)
		claude := installClaude(t, "pane d1\n", 0)

		_, err := Intent(t.Context(), intentConfig(newClient(t), claude, root), "example text")
		if err == nil || !strings.Contains(err.Error(), "user memory") {
			t.Fatalf("Intent with no HOME: err = %v, want a refusal naming the user memory", err)
		}
		if runs := claudeRuns(t, claude); len(runs) != 0 {
			t.Fatalf("claude ran %d times, want none", len(runs))
		}
	})

	t.Run("a pane launcher's settings carry no claudeMdExcludes", func(t *testing.T) {
		home, stamped, _ := stampedHome(t)
		argv, err := stampedArgv(Config{Home: home}, stamped, claudeCommand("claude", ""))
		if err != nil {
			t.Fatalf("stampedArgv: %v", err)
		}
		if want := []string{"claude", "--settings", stampEnvJSON}; !slices.Equal(argv, want) {
			t.Fatalf("pane claude argv = %q, want %q", argv, want)
		}
	})
}
