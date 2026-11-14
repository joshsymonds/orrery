package enginehost

import (
	"strings"
	"syscall"
	"testing"

	"github.com/joshsymonds/orrery/internal/engine/session"
)

// TestInitNamesAgent covers the rule the init event's agents list must meet for a step to run its agent.
func TestInitNamesAgent(t *testing.T) {
	cases := []struct {
		name   string
		agents []string
		agent  string
		want   bool
	}{
		{name: "listed name", agents: []string{"claude", "reviewer"}, agent: "reviewer", want: true},
		{name: "scoped entry for its name", agents: []string{"dev:reviewer"}, agent: "reviewer", want: true},
		{name: "scoped name listed whole", agents: []string{"dev:reviewer"}, agent: "dev:reviewer", want: true},
		{name: "name absent", agents: []string{"claude", "general-purpose"}, agent: "reviewer", want: false},
		{name: "suffix without colon boundary", agents: []string{"devreviewer"}, agent: "reviewer", want: false},
		{name: "web-fetch listed", agents: []string{"web-fetch"}, agent: "web-fetch", want: false},
		{name: "empty list", agents: []string{}, agent: "reviewer", want: false},
		{name: "nil list", agents: nil, agent: "reviewer", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := initNamesAgent(tc.agents, tc.agent); got != tc.want {
				t.Fatalf("initNamesAgent(%q, %q) = %v, want %v", tc.agents, tc.agent, got, tc.want)
			}
		})
	}
}

// TestStepStartedWithoutItsAgentInInitEndsFailed runs a writing step on a real Manager whose fake claude's init omits the
// step's agent. The step must end failed with a reason naming the agent, and its claude process must be gone.
func TestStepStartedWithoutItsAgentInInitEndsFailed(t *testing.T) {
	t.Parallel()
	f := newRefusalFixture(t, 1)
	workspace := stepWorktreeTestAddLinked(t, stepGitTestRepo(t))
	start := stepStartWriting(workspace)
	start.Prompt = "do it NOAGENT"
	id, err := refusalStart(t, f.steps, start)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	stepStartAwait(t, "step "+id+" to end", func() bool {
		row, err := f.steps.Status(t.Context(), id)
		return err == nil && row.End != ""
	})
	row, err := f.steps.Status(t.Context(), id)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if row.End != stepLoopEndFailed {
		t.Fatalf("end = %q, want %q; reason %q", row.End, stepLoopEndFailed, row.Reason)
	}
	if !strings.Contains(row.Reason, "implementer") {
		t.Fatalf("reason = %q, want it to name the agent implementer", row.Reason)
	}
	requireGone(t, fakePID(t, workspace))
}

// TestStepWhoseInitListsItsAgentKeepsRunning runs a step on a real Manager whose fake claude's init lists the step's
// agent. The step must keep running, and its process must still be the one Stop ends.
func TestStepWhoseInitListsItsAgentKeepsRunning(t *testing.T) {
	t.Parallel()
	m, _, _ := newHandOffManager(t, 1, &fakeSink{})
	dir := t.TempDir()
	obs := &stepObserver{}
	err := m.StartStep(t.Context(), StepSession{
		ID: stepA, Dir: dir, Prompt: "hi STALL", Agent: "reviewer", Schema: stepSchema, Observer: obs,
	})
	if err != nil {
		t.Fatalf("StartStep: %v", err)
	}
	// Text arrives after the init event, which the pump hands to the observer before any later event.
	waitUntil(t, "the step's first text delta", func() bool { return hasTextDelta(obs) })
	if reasons := obs.exited(); len(reasons) != 0 {
		t.Fatalf("step exited with %q, want it running", reasons)
	}
	pid := fakePID(t, dir)
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("fake claude %d is not running: %v", pid, err)
	}
	if err := m.Stop(t.Context(), stepA); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	requireGone(t, pid)
}

// hasTextDelta reports whether obs has received a text delta.
func hasTextDelta(obs *stepObserver) bool {
	for _, event := range obs.sequence() {
		if _, ok := event.(session.TextDelta); ok {
			return true
		}
	}
	return false
}
