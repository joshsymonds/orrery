package tui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// stampedSettings is the settings file of the synthetic stamp. Its env holds a loopback base URL
// and a value with a single quote and a format, which the claude window's command must carry
// unchanged.
const stampedSettings = `{"env":{"ANTHROPIC_BASE_URL":"http://127.0.0.1:9/ctx/example","ORRERY_NOTE":"it's #{session_name}"}}`

// recordingClaude puts a synthetic claude first on PATH. It records each argument, NUL-terminated,
// in the returned marker file, and then runs until its window is closed.
func recordingClaude(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	marker := filepath.Join(t.TempDir(), "argv")
	script := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\0' \"$a\"; done > '" + marker + "'\nexec sleep 300\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o755); err != nil {
		t.Fatalf("write synthetic claude: %v", err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return marker
}

// recordedArgv returns the arguments the synthetic claude recorded in marker. It fails while the
// marker is absent, which is before claude has started.
func recordedArgv(marker string) ([]string, error) {
	data, err := os.ReadFile(marker)
	if err != nil {
		return nil, fmt.Errorf("read argv: %w", err)
	}
	if len(data) == 0 {
		return nil, nil
	}
	return strings.Split(strings.TrimSuffix(string(data), "\x00"), "\x00"), nil
}

// stampedTree builds the action layout with the cursor pane's directory, home/work/alpha, below
// the synthetic home. HOME is that home, and the folder above the cursor directory, home/work,
// holds settings as its .claude/settings.json. A nil settings writes no stamp.
func stampedTree(t *testing.T, settings *string) *actionTree {
	t.Helper()
	layout := actionLayout()
	layout[1].dir = "home/work/alpha"
	a := newActionTree(t, layout)
	home := a.dir("home")
	t.Setenv("HOME", home)
	if settings != nil {
		folder := filepath.Join(home, "work", ".claude")
		if err := os.MkdirAll(folder, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", folder, err)
		}
		if err := os.WriteFile(filepath.Join(folder, "settings.json"), []byte(*settings), 0o644); err != nil {
			t.Fatalf("write stamp: %v", err)
		}
	}
	return a
}

func TestClaudeWindowRunsWithStampSettings(t *testing.T) {
	marker := recordingClaude(t)
	settings := stampedSettings
	want := []string{"--settings", stampedSettings}
	a := stampedTree(t, &settings)
	run := a.start(a.invoking)
	run.send("j", "c")
	waitFor(t, "claude window runs with the stamp's settings", func() error {
		count, err := a.windowCount("s2")
		if err != nil {
			return err
		}
		if count != 2 {
			return fmt.Errorf("s2 has %d windows, want 2", count)
		}
		got, err := recordedArgv(marker)
		if err != nil {
			return err
		}
		if !slices.Equal(got, want) {
			return fmt.Errorf("claude argv = %q, want %q", got, want)
		}
		return nil
	})
	waitFor(t, "invoking client unchanged", func() error { return a.check("s1") })
	run.assertOpen()
	run.send("q")
	run.closed()
}

func TestClaudeWindowRunsWithoutArgumentsWithoutStamp(t *testing.T) {
	marker := recordingClaude(t)
	a := stampedTree(t, nil)
	run := a.start(a.invoking)
	run.send("j", "c")
	waitFor(t, "claude window runs with no arguments", func() error {
		count, err := a.windowCount("s2")
		if err != nil {
			return err
		}
		if count != 2 {
			return fmt.Errorf("s2 has %d windows, want 2", count)
		}
		got, err := recordedArgv(marker)
		if err != nil {
			return err
		}
		if len(got) != 0 {
			return fmt.Errorf("claude argv = %q, want none", got)
		}
		return nil
	})
	waitFor(t, "invoking client unchanged", func() error { return a.check("s1") })
	run.assertOpen()
	run.send("q")
	run.closed()
}

func TestClaudeWindowOpensNothingBelowUnparseableStamp(t *testing.T) {
	marker := recordingClaude(t)
	settings := `{"env":`
	a := stampedTree(t, &settings)
	run := a.start(a.invoking)
	run.send("j", "c")
	waitFor(t, "error on status line", func() error {
		if !strings.Contains(run.out.String(), "error: ") {
			return errors.New("no error line rendered")
		}
		return nil
	})
	count, err := a.windowCount("s2")
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("s2 has %d windows, want 1", count)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("claude ran (stat marker: %v), want no claude", err)
	}
	run.assertOpen()
	run.send("q")
	run.closed()
}
