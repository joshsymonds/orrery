package enginehost

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/gates"
	"github.com/joshsymonds/orrery/internal/store"
)

// stepDataGateStart returns a read-only start of the repository at workspace that runs gate given as data. It carries no
// agent, schema or done value, which the gate supplies, and names no gate and no gates file.
func stepDataGateStart(workspace string, gate gates.Gate) StepStart {
	start := stepStartReadOnly(workspace, "HEAD")
	start.Agent = ""
	start.Schema = ""
	start.Prompt = "review the change"
	start.AgentGate = &gate
	return start
}

func TestStepsStartDataAgentGateRunsItsAgentSchemaAndPrompt(t *testing.T) {
	t.Parallel()
	f := newStepStartFixture(t)
	repo := stepGitTestRepo(t)
	commit := stepGitTestRun(t, repo, "rev-parse", "HEAD")
	gate := stepAgentGate("reviewer")
	start := stepDataGateStart(repo, gate)

	id, err := f.steps.Start(t.Context(), start)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	step := f.sessions.first(t)
	prompt := gates.Prompt(gate, start.Prompt)
	worktree := filepath.Join(f.worktrees, id)
	if step.Agent != "reviewer" {
		t.Errorf("StepSession agent = %q, want reviewer", step.Agent)
	}
	if step.Schema != gates.VerdictSchema {
		t.Errorf("StepSession schema is not the verdict schema")
	}
	if step.Prompt != prompt {
		t.Errorf("StepSession prompt = %q, want %q", step.Prompt, prompt)
	}
	if step.Dir != worktree {
		t.Errorf("StepSession dir = %q, want the read-only worktree %q", step.Dir, worktree)
	}
	want := store.StepAdd{
		ID:         id,
		Agent:      "reviewer",
		Prompt:     prompt,
		Schema:     gates.VerdictSchema,
		Workspace:  repo,
		Revision:   commit,
		Worktree:   worktree,
		DoneValue:  "done",
		ReadOnly:   true,
		GateName:   "review",
		GateSource: "data",
		GateLimit:  300 * time.Second,
		ModelLimit: stepDefaultModelLimit,
	}
	if got := stepGateAdd(t, step); !reflect.DeepEqual(got, want) {
		t.Fatalf("StepAdd = %#v, want %#v", got, want)
	}
	_, run := stepAgentGateRun(t, f)
	if run.agentGate == nil {
		t.Fatal("step run agent gate is nil, want the data agent gate")
	}
	if !reflect.DeepEqual(*run.agentGate, gate) {
		t.Errorf("step run agent gate = %+v, want %+v", *run.agentGate, gate)
	}
	if len(run.gate) != 0 {
		t.Errorf("step run gate = %q, want no argv", run.gate)
	}
}

func TestStepsStartDataAgentGateReadsNoProjectFile(t *testing.T) {
	t.Parallel()
	f := newStepStartFixture(t)
	// The project file does not parse. A data gate comes from the start, so its start must not read the file.
	repo := stepGitTestRepo(t)
	stepGateWriteFile(t, filepath.Join(repo, ".orrery", "project.toml"), "[gates.review\n")

	if _, err := f.steps.Start(t.Context(), stepDataGateStart(repo, stepAgentGate("reviewer"))); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if n := f.sessions.startCalls(); n != 1 {
		t.Fatalf("StartStep calls = %d, want 1", n)
	}
	if got := stepGateAdd(t, f.sessions.first(t)); got.GateName != "review" || got.GateSource != "data" {
		t.Errorf("stored gate name %q source %q, want review from data", got.GateName, got.GateSource)
	}
}

func TestStepsStartDataAgentGateRefusedInAWritingStep(t *testing.T) {
	t.Parallel()
	f := newStepStartFixture(t)
	linked := stepWorktreeTestAddLinked(t, stepGitTestRepo(t))
	start := stepStartWriting(linked)
	start.Agent = ""
	start.Schema = ""
	start.DoneValue = ""
	gate := stepAgentGate("reviewer")
	start.AgentGate = &gate

	id, err := f.steps.Start(t.Context(), start)
	checkStepStartRefused(t, f, id, err, ErrBadGate)
}

func TestStepsStartDataAgentGateRefusesANamedGateOrFile(t *testing.T) {
	t.Parallel()
	// Each case names a gate that would resolve without the refusal, so the refusal is what the test checks.
	cases := []struct {
		name   string
		mutate func(t *testing.T, start *StepStart)
	}{
		{
			name: "gate name in the project file",
			mutate: func(t *testing.T, start *StepStart) {
				t.Helper()
				stepGateWriteFile(t, filepath.Join(start.Workspace, ".orrery", "project.toml"),
					stepAgentGateText("reviewer"))
				start.GateName = "review"
			},
		},
		{
			name: "gate name and gates file",
			mutate: func(t *testing.T, start *StepStart) {
				t.Helper()
				gatesFile := filepath.Join(t.TempDir(), "gates.toml")
				stepGateWriteFile(t, gatesFile, stepAgentGateText("reviewer"))
				start.GateName = "review"
				start.GatesFile = gatesFile
			},
		},
		{
			name: "gates file",
			mutate: func(t *testing.T, start *StepStart) {
				t.Helper()
				gatesFile := filepath.Join(t.TempDir(), "gates.toml")
				stepGateWriteFile(t, gatesFile, stepAgentGateText("reviewer"))
				start.GatesFile = gatesFile
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newStepStartFixture(t)
			start := stepDataGateStart(stepGitTestRepo(t), stepAgentGate("reviewer"))
			tc.mutate(t, &start)

			id, err := f.steps.Start(t.Context(), start)
			checkStepStartRefused(t, f, id, err, ErrBadGate)
		})
	}
}

func TestStepsStartDataAgentGateRefusesABadGate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		mutate func(*gates.Gate)
	}{
		{name: "empty agent", mutate: func(g *gates.Gate) { g.Agent = "" }},
		{name: "no assertion", mutate: func(g *gates.Gate) { g.Assertions = nil }},
		{name: "empty name", mutate: func(g *gates.Gate) { g.Name = "" }},
		{name: "zero limit", mutate: func(g *gates.Gate) { g.Limit = 0 }},
		{name: "limit under a second", mutate: func(g *gates.Gate) { g.Limit = 500 * time.Millisecond }},
		{name: "argv", mutate: func(g *gates.Gate) { g.Argv = []string{"/bin/sh", "-c", "exit 0"} }},
		{name: "needs", mutate: func(g *gates.Gate) { g.Needs = []gates.Need{{Pool: "gate", Count: 1}} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newStepStartFixture(t)
			gate := stepAgentGate("reviewer")
			tc.mutate(&gate)

			id, err := f.steps.Start(t.Context(), stepDataGateStart(stepGitTestRepo(t), gate))
			checkStepStartRefused(t, f, id, err, ErrBadGate)
		})
	}
}
