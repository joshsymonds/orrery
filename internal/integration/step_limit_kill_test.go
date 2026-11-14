package integration_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
)

// The test in this file SIGKILLs orreryd after a writing step's turn is limited. The LIMIT prompt makes the fake claude
// end its turn at a seven_day usage limit, which frees its process and holds the step open. The test restarts orreryd on
// the same store and state directory, checks that the step still reads open and limited with the limit's kind and reset,
// and that no fake claude was respawned. It then resumes the step, whose fake claude holds its turn until the release
// exists, and checks that the step ends done with its gate run once.

// limitKillPrompt is the prompt that makes the fake claude end its turn at a usage limit.
const limitKillPrompt = "LIMIT"

// limitKillResets is the reset, in unix seconds, of the seven_day limit the LIMIT turn reports.
const limitKillResets = 1900000000

// TestStepLimitSigkillKeepsLimitedStepAndResumesDone proves that a writing step whose turn ends at a usage limit stays
// limited across a SIGKILL and restart of orreryd, with its limit's kind and reset, and that a resume after the restart
// ends the step done with its gate run once.
func TestStepLimitSigkillKeepsLimitedStepAndResumesDone(t *testing.T) {
	t.Parallel()
	r := newStepReadoptRig(t)
	daemon := r.f.startOrreryd(t, true, "--holder-scope", r.scope)
	id := r.startLimitKillStep(t)
	t.Cleanup(func() { clearHolder(t, filepath.Join(r.stateDir, "holders", id)) })
	t.Cleanup(func() { clearHolder(t, r.gateHolder(t, id)) })
	waitUntil(t, "step "+id+" to be limited", func() bool { return r.step(t, id).GetLimited() })
	// The limit frees the fake claude before the kill. Checking after the restart would not show that: a kill closes the
	// fake's stdin, and the fake exits at once.
	waitGone(t, fakeClaudePID(t, r.worktree))

	killOrreryd(t, daemon)
	restarted := r.f.startOrreryd(t, true, "--holder-scope", r.scope)
	step := r.step(t, id)
	if end := step.GetEnd(); end != "" {
		t.Fatalf("step %s ended %q after the restart, want it open", id, end)
	}
	if !step.GetLimited() {
		t.Fatalf("step %s is not limited after the restart, want it limited", id)
	}
	if kind := step.GetLimitKind(); kind != "seven_day" {
		t.Fatalf("limit kind of step %s = %q after the restart, want seven_day", id, kind)
	}
	if resets := step.GetLimitResets(); resets != limitKillResets {
		t.Fatalf("limit reset of step %s = %d after the restart, want %d", id, resets, limitKillResets)
	}
	if starts := limitKillStarts(t, r.worktree); len(starts) != 1 {
		t.Fatalf("fake claude starts after the restart = %q, want only the start before the kill", starts)
	}

	// The turn is released before the resume, so the resumed turn returns at once and the gate is the next thing it runs.
	stepReadoptWrite(t, filepath.Join(r.worktree, "fakeclaude.next"), stepReadoptPrompt)
	stepReadoptTouch(t, r.turnRelease)
	if _, err := r.steps().ResumeStep(t.Context(), connect.NewRequest(&v1.ResumeStepRequest{Id: id})); err != nil {
		t.Fatalf("resume step %s: %v", id, err)
	}
	waitUntil(t, "the gate to start", func() bool { return gateRunCount(t, r.gateRuns) > 0 })
	if n := gateRunCount(t, r.gateRuns); n != 1 {
		t.Fatalf("gate runs of step %s = %d, want 1", id, n)
	}
	stepReadoptTouch(t, r.gateRelease)
	r.requireDone(t, r.awaitEnd(t, id))
	if n := gateRunCount(t, r.gateRuns); n != 1 {
		t.Fatalf("gate runs of step %s = %d after it ended, want 1", id, n)
	}
	starts := limitKillStarts(t, r.worktree)
	if len(starts) != 2 || !strings.Contains(starts[1], "--resume session_1") {
		t.Fatalf("fake claude starts = %q, want two, the second with --resume session_1", starts)
	}
	wantExit(t, restarted.stop(t), 0)
}

// startLimitKillStep starts the rig's writing step with the LIMIT prompt and returns its id.
func (r *stepReadoptRig) startLimitKillStep(t *testing.T) string {
	t.Helper()
	resp, err := r.steps().StartStep(t.Context(), connect.NewRequest(&v1.StartStepRequest{
		Agent:     stepReadoptAgent,
		Prompt:    limitKillPrompt,
		Schema:    stepReadoptSchema,
		Workspace: r.worktree,
		GateName:  stepReadoptGate,
		GatesFile: r.gatesFile,
	}))
	if err != nil {
		t.Fatalf("start step: %v", err)
	}
	return resp.Msg.GetId()
}

// limitKillStarts returns the argument line of each start of the fake claude in dir, one line per start.
func limitKillStarts(t *testing.T, dir string) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "fakeclaude.args"))
	if err != nil {
		t.Fatalf("read fake claude args: %v", err)
	}
	return strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
}
