package integration_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
	"github.com/joshsymonds/orrery/internal/waittest"
)

// printHeldScript is a claude whose one-shot runs, which save no session, such as a step judge's and a summary's, append
// a line with their pid and argv, its newlines made spaces, to print.pids beside it and then become a sleep that only a signal ends, as a slow real run is when systemd stops
// orreryd. Every other run, a session's among them, is the engine's fake claude, which the script's one argument names.
const printHeldScript = `#!/bin/sh
D=$(dirname "$0")
for arg in "$@"; do
	if [ "$arg" = --no-session-persistence ]; then
		printf '%%s\n' "$$ $(printf '%%s' "$*" | tr '\n' ' ')" >> "$D/print.pids"
		cat > /dev/null
		exec sleep 1000
	fi
done
exec %s "$@"
`

// TestOrrerydStopEndsWithJudgeAndSummaryInFlight stops orreryd as systemd does, with a step's judge run and a pane's
// summary run both in flight: SIGTERM to orreryd and to every process of its unit, which takes both runs, while the
// step's session runs on under its holder's scope. The forced order: the step's turn returns done, so its loop calls
// the judge, which holds; a hook starts the pane's summary run, which holds; only then does the stop come. orreryd must
// exit 0, having logged every shutdown phase it started done.
func TestOrrerydStopEndsWithJudgeAndSummaryInFlight(t *testing.T) {
	t.Parallel()
	r := newStepReadoptRig(t)
	claude := filepath.Join(r.f.root, "claude-held")
	writeFile(t, claude, fmt.Sprintf(printHeldScript, shellQuote(r.f.fakeClaude)), 0o700)
	r.f.fakeClaude = claude
	pids := filepath.Join(r.f.root, "print.pids")
	daemon := r.f.startOrreryd(t, true, "--holder-scope", r.scope)
	t.Cleanup(func() { clearHolders(t, r.stateDir) })

	resp, err := r.steps().StartStep(t.Context(), connect.NewRequest(&v1.StartStepRequest{
		Agent: stepReadoptAgent, Prompt: stepReadoptPrompt, Schema: stepReadoptSchema, Workspace: r.worktree,
	}))
	if err != nil {
		t.Fatalf("start step: %v", err)
	}
	id := resp.Msg.GetId()
	stepReadoptTouch(t, r.turnRelease)
	waittest.Until(t, daemon.done, "the step's judge run", func() bool {
		return r.step(t, id).GetStage() == store.StepStageJudge && heldRun(t, pids, "--json-schema")
	})

	client, err := tmuxctl.New(tmuxctl.Server{Name: r.f.server})
	if err != nil {
		t.Fatalf("tmux client: %v", err)
	}
	out, err := client.Run(t.Context(), "list-panes", "-t", "s1", "-F", "#{pane_id}")
	if err != nil {
		t.Fatalf("list-panes: %v", err)
	}
	dir := t.TempDir()
	transcript := filepath.Join(dir, "transcript.jsonl")
	writeFile(t, transcript, exampleTranscript, 0o600)
	sendHook(t, r.f.socket, strings.TrimSpace(out), dir, map[string]any{
		"hook_event_name": "UserPromptSubmit",
		"transcript_path": transcript,
	})
	waittest.Until(t, daemon.done, "the pane's summary run", func() bool { return heldRun(t, pids, "--system-prompt") })

	daemon.asked.Store(true)
	if err = daemon.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("SIGTERM orreryd: %v", err)
	}
	// systemd signals every process of the unit that still runs; a one-shot run, such as a step watch's, may have
	// ended on its own by now.
	for _, run := range heldRuns(t, pids) {
		if err = syscall.Kill(run, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
			t.Fatalf("SIGTERM run %d: %v", run, err)
		}
	}
	wantExit(t, daemon.exitOutcome(t), 0)
	logged := daemon.stderr.String()
	if starts, dones := strings.Count(logged, "step=start"), strings.Count(logged, "step=done"); starts == 0 ||
		starts != dones {
		t.Errorf("shutdown phases started %d, done %d, want every phase that started done; log %s",
			starts, dones, daemon.logPath)
	}
}

// heldLines returns the lines print.pids at path holds, one per held one-shot run, in the order the runs started: the
// run's pid, then its argv.
func heldLines(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil || strings.TrimSpace(string(raw)) == "" {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(raw)), "\n")
}

// heldRuns returns the pid of every held one-shot run print.pids at path records.
func heldRuns(t *testing.T, path string) []int {
	t.Helper()
	lines := heldLines(t, path)
	runs := make([]int, 0, len(lines))
	for _, line := range lines {
		field, _, _ := strings.Cut(line, " ")
		pid, convErr := strconv.Atoi(field)
		if convErr != nil {
			t.Fatalf("held run line %q: %v", line, convErr)
		}
		runs = append(runs, pid)
	}
	return runs
}

// heldRun reports whether print.pids at path records a held run whose argv holds flag: --json-schema for a haiku call,
// such as a step judge's, and --system-prompt for a summary run.
func heldRun(t *testing.T, path, flag string) bool {
	t.Helper()
	for _, line := range heldLines(t, path) {
		if strings.Contains(" "+line+" ", " "+flag+" ") {
			return true
		}
	}
	return false
}
