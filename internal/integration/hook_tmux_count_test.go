package integration_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// tmuxCountScript is a tmux that appends its argv, on one line with its newlines made spaces, to the file CALLS, then
// runs the real tmux REAL. The test puts the quoted paths in place of the two names.
const tmuxCountScript = `#!/bin/sh
printf '%s\n' "$(printf '%s ' "$@" | tr '\n' ' ')" >> CALLS
exec REAL "$@"
`

// hookCalls is what one hook's tmux invocations on its pane were, by kind.
type hookCalls struct {
	reads   int // reads of the pane's state options
	writes  int // invocations that set or unset options, other than a mint of the pane's id
	viewing int // reads of the server's clients, which a done's viewing check makes
}

// TestHookTmuxInvocations counts the tmux processes orreryd runs for each hook through a counting tmux on its PATH:
// each hook reads its pane's state once and writes every option it changes in one invocation, and a done checks its
// pane's viewing once, for its notice and its seen mark together. The mirror's reads of the pane are not counted: one
// runs in the hook's write when the mirror lock is free and in an invocation of its own when a tidy holds it, and a
// tidy reads the pane on its own schedule. The store is open before the first hook, which mints the pane's id and is not
// counted. The judge delay is an hour, so no routing decision reads tmux while the test counts, and no hook names a
// transcript, so no summary runs.
func TestHookTmuxInvocations(t *testing.T) {
	t.Parallel()
	client, name := newServer(t)
	pane := startPane(t, client, "sleep 300")
	dir := t.TempDir()
	realTmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatalf("find tmux: %v", err)
	}
	bin := filepath.Join(dir, "bin")
	if err = os.Mkdir(bin, 0o700); err != nil {
		t.Fatalf("make bin: %v", err)
	}
	calls := filepath.Join(dir, "tmux.calls")
	script := strings.NewReplacer("CALLS", shellQuote(calls), "REAL", shellQuote(realTmux)).Replace(tmuxCountScript)
	writeFile(t, filepath.Join(bin, "tmux"), script, 0o700)
	socket := filepath.Join(dir, "orreryd.sock")
	daemon := startBinary(t, "orreryd",
		scrubbedEnv("XDG_STATE_HOME="+t.TempDir(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH")),
		"-L", name, "--socket", socket, "--judge-delay", "1h")
	daemon.waitForListener(t, socket)
	daemon.waitForStoreOpen(t)

	project := t.TempDir()
	sendHook(t, socket, pane, project, map[string]any{"hook_event_name": "UserPromptSubmit"})
	for _, step := range []struct {
		hook map[string]any
		want hookCalls
	}{
		{map[string]any{"hook_event_name": "Stop"}, hookCalls{reads: 1, writes: 1, viewing: 1}},
		{map[string]any{"hook_event_name": "UserPromptSubmit"}, hookCalls{reads: 1, writes: 1}},
		{map[string]any{"hook_event_name": "PreToolUse", "tool_use_id": "toolu_1", "tool_name": "Bash"},
			hookCalls{reads: 1, writes: 1}},
		{map[string]any{"hook_event_name": "PostToolUse", "tool_use_id": "toolu_1", "tool_name": "Bash"},
			hookCalls{reads: 1, writes: 1}},
		{map[string]any{"hook_event_name": "Stop"}, hookCalls{reads: 1, writes: 1, viewing: 1}},
	} {
		before := len(tmuxLines(t, calls))
		sendHook(t, socket, pane, project, step.hook)
		got := countHookCalls(tmuxLines(t, calls)[before:], pane)
		if got != step.want {
			t.Errorf("%s made %+v tmux invocations, want %+v; calls:\n%s",
				step.hook["hook_event_name"], got, step.want, strings.Join(tmuxLines(t, calls)[before:], "\n"))
		}
	}
	wantExit(t, daemon.stop(t), 0)
}

// tmuxLines returns the invocations the counting tmux logged at path, one per line.
func tmuxLines(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read tmux calls: %v", err)
	}
	return strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
}

// countHookCalls counts the invocations among lines that name pane, by kind. A tidy's list-panes names no pane, and a
// mirror read is no kind counted.
func countHookCalls(lines []string, pane string) hookCalls {
	var got hookCalls
	for _, line := range lines {
		if !strings.Contains(" "+line+" ", " "+pane+" ") {
			continue
		}
		switch {
		case strings.Contains(line, "set-option") && !strings.Contains(line, "@orrery_id "):
			got.writes++
		case strings.Contains(line, "list-clients"):
			got.viewing++
		case strings.Contains(line, "#{@orrery_state}") && !strings.Contains(line, "#{@orrery_id}"):
			got.reads++
		}
	}
	return got
}
