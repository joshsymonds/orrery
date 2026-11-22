package integration_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/paneregistry"
	"github.com/joshsymonds/orrery/internal/protocol"
)

// tmuxCountScript is a tmux that appends its argv, on one line with its newlines made spaces, to the file CALLS, then
// runs the real tmux REAL. The test puts the quoted paths in place of the two names.
const tmuxCountScript = `#!/bin/sh
printf '%s\n' "$(printf '%s ' "$@" | tr '\n' ' ')" >> CALLS
exec REAL "$@"
`

// updateCalls is what one registry update's tmux invocations on its pane were, by kind.
type updateCalls struct {
	reads   int // reads of the pane's state options
	writes  int // invocations that set or unset options, other than a mint of the pane's id
	viewing int // reads of the server's clients, which a done's viewing check makes
}

// TestRegistryUpdateTmuxInvocations counts the tmux processes orreryd runs for each session registry update through a
// counting tmux on its PATH: an update reads its pane's state once and writes every option it changes in one
// invocation, and a done checks its pane's viewing once, for its notice and its seen mark together. The mirror's reads
// of the pane are not counted: one runs in the update's write when the mirror lock is free and in an invocation of its
// own when a tidy holds it, and a tidy reads the pane on its own schedule. The store is open and both sessions are bound
// before the first counted update, so the pane ids are minted and not counted. The judge delay is an hour, so no
// routing decision reads tmux while the test counts. The sessions name transcripts that are never written, so no
// summary runs.
//
// Each counted update is followed by an update of a sentinel session in a second pane, whose state changes on every
// call. orreryd applies updates in order, so once the sentinel's state has changed, every invocation of the counted
// update is logged. The sentinel's own invocations name its pane, not the counted one.
func TestRegistryUpdateTmuxInvocations(t *testing.T) {
	t.Parallel()
	client, name := newServer(t)
	pane := startPane(t, client, "sleep 300")
	sentinelPane := splitPane(t, client, pane, "sleep 300")
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
	path := bin + string(os.PathListSeparator) + os.Getenv("PATH")
	daemon := startBinary(t, "orreryd",
		scrubbedEnv("XDG_STATE_HOME="+t.TempDir(), "HOME="+dir, "PATH="+path),
		"-L", name, "--socket", socket, "--judge-delay", "1h")
	daemon.waitForListener(t, socket)
	daemon.waitForStoreOpen(t)

	session := startClaudeSession(t, client, dir, pane, dir)
	sentinel := startClaudeSession(t, client, dir, sentinelPane, dir)
	session.setStatus(t, paneregistry.StatusIdle, "")
	sentinel.setStatus(t, paneregistry.StatusIdle, "")
	waitForOption(t, daemon, client, pane, protocol.OptionClaudeSession, session.sessionID)
	waitForOption(t, daemon, client, sentinelPane, protocol.OptionClaudeSession, sentinel.sessionID)

	// Each counted update sets the status of the pane, and the sentinel alternates between the two states below.
	steps := []struct {
		status string
		want   protocol.State
		calls  updateCalls
	}{
		{paneregistry.StatusBusy, protocol.StateWorking, updateCalls{reads: 1, writes: 1}},
		{paneregistry.StatusIdle, protocol.StateDone, updateCalls{reads: 1, writes: 1, viewing: 1}},
		{paneregistry.StatusBusy, protocol.StateWorking, updateCalls{reads: 1, writes: 1}},
		{paneregistry.StatusIdle, protocol.StateDone, updateCalls{reads: 1, writes: 1, viewing: 1}},
	}
	sentinelSteps := []struct {
		status string
		want   protocol.State
	}{
		{paneregistry.StatusBusy, protocol.StateWorking},
		{paneregistry.StatusIdle, protocol.StateDone},
	}
	for i, step := range steps {
		before := len(tmuxLines(t, calls))
		session.setStatus(t, step.status, "")
		waitForOption(t, daemon, client, pane, protocol.OptionState, string(step.want))
		next := sentinelSteps[i%len(sentinelSteps)]
		sentinel.setStatus(t, next.status, "")
		waitForOption(t, daemon, client, sentinelPane, protocol.OptionState, string(next.want))
		got := countUpdateCalls(tmuxLines(t, calls)[before:], pane)
		if got != step.calls {
			t.Errorf("%s made %+v tmux invocations, want %+v; calls:\n%s",
				step.status, got, step.calls, strings.Join(tmuxLines(t, calls)[before:], "\n"))
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

// countUpdateCalls counts the invocations among lines that name pane, by kind. A tidy's list-panes names no pane, and a
// mirror read is no kind counted.
func countUpdateCalls(lines []string, pane string) updateCalls {
	var got updateCalls
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
