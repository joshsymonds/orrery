package enginehost

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/joshsymonds/orrery/internal/agents"
	"github.com/joshsymonds/orrery/internal/engine/session"
	"github.com/joshsymonds/orrery/internal/gates"
	"github.com/joshsymonds/orrery/internal/grants"
	"github.com/joshsymonds/orrery/internal/store"
)

// stepAgentGateText is a gates file that defines gate review: an agent gate running agent, with a limit of 300 seconds
// and one must and one must not assertion.
func stepAgentGateText(agent string) string {
	return fmt.Sprintf("[gates.review]\nagent = %q\nlimit = 300\nmust = [\"the change keeps the API\"]\n"+
		"must_not = [\"the change adds a dependency\"]\n", agent)
}

// stepAgentGate is the gate stepAgentGateText defines for agent.
func stepAgentGate(agent string) gates.Gate {
	return gates.Gate{
		Name:  "review",
		Agent: agent,
		Limit: 300 * time.Second,
		Assertions: []gates.Assertion{
			{Text: "the change keeps the API", Kind: gates.KindMust},
			{Text: "the change adds a dependency", Kind: gates.KindMustNot},
		},
	}
}

// stepAgentGateStart returns a read-only start of the repository at workspace that names the review gate from gatesFile
// and carries no agent, schema or done value.
func stepAgentGateStart(workspace, gatesFile string) StepStart {
	start := stepStartReadOnly(workspace, "HEAD")
	start.Agent = ""
	start.Schema = ""
	start.Prompt = "review the change"
	start.GateName = "review"
	start.GatesFile = gatesFile
	return start
}

// checkStepStartRefused checks a start refused with want before anything was stored: an empty id, a StepCommands error
// that maps to invalid_argument, no StartStep call, no step row and no worktree.
func checkStepStartRefused(t *testing.T, f *stepStartFixture, id string, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("Start = %q, %v; want an error matching %v", id, err, want)
	}
	if got := connect.CodeOf(stepCommandError(err)); got != connect.CodeInvalidArgument {
		t.Errorf("refusal code = %v, want %v", got, connect.CodeInvalidArgument)
	}
	if id != "" {
		t.Errorf("Start id = %q on refusal, want empty", id)
	}
	if n := f.sessions.startCalls(); n != 0 {
		t.Errorf("StartStep calls = %d, want 0", n)
	}
	rows, rowsErr := f.store.OpenSteps(t.Context())
	if rowsErr != nil {
		t.Fatalf("OpenSteps: %v", rowsErr)
	}
	if len(rows) != 0 {
		t.Errorf("stored %d step rows on refusal, want none", len(rows))
	}
	if names := refusalWorktrees(t, f.worktrees); len(names) != 0 {
		t.Errorf("worktrees = %v, want none after a refused start", names)
	}
}

func TestStepsStartAgentGateRunsItsAgentSchemaAndPrompt(t *testing.T) {
	t.Parallel()
	f := newStepStartFixture(t)
	repo := stepGitTestRepo(t)
	commit := stepGitTestRun(t, repo, "rev-parse", "HEAD")
	gatesFile := filepath.Join(t.TempDir(), "gates.toml")
	stepGateWriteFile(t, gatesFile, stepAgentGateText("reviewer"))
	start := stepAgentGateStart(repo, gatesFile)

	id, err := f.steps.Start(t.Context(), start)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	step := f.sessions.first(t)
	prompt := gates.Prompt(stepAgentGate("reviewer"), start.Prompt)
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
		GateSource: gates.SourceEpic,
		GateLimit:  300 * time.Second,
		ModelLimit: stepDefaultModelLimit,
	}
	if got := stepGateAdd(t, step); !reflect.DeepEqual(got, want) {
		t.Fatalf("StepAdd = %#v, want %#v", got, want)
	}
	run, ok := step.Observer.(*stepRun)
	if !ok {
		t.Fatalf("StartStep Observer is %T, want *stepRun", step.Observer)
	}
	if len(run.gate) != 0 || !reflect.DeepEqual(run.grant, grants.Request{}) {
		t.Errorf("step run gate = %q grant = %+v, want no argv and no grant", run.gate, run.grant)
	}
	if run.gateLimit != 300*time.Second {
		t.Errorf("step run gate limit = %s, want 5m0s", run.gateLimit)
	}
}

func TestStepsStartAgentGateReadsNoPoolFile(t *testing.T) {
	t.Parallel()
	f := newStepStartFixture(t)
	// The machine pool file does not parse. An agent gate takes no pool member, so its start must not read the file.
	config := t.TempDir()
	stepGateWriteFile(t, filepath.Join(config, "orrery", "pools.toml"), "[pools\n")
	f.steps.environ = []string{"PATH=" + os.Getenv("PATH"), "XDG_CONFIG_HOME=" + config}
	repo := stepGitTestRepo(t)
	gatesFile := filepath.Join(t.TempDir(), "gates.toml")
	stepGateWriteFile(t, gatesFile, stepAgentGateText("reviewer"))

	if _, err := f.steps.Start(t.Context(), stepAgentGateStart(repo, gatesFile)); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if n := f.sessions.startCalls(); n != 1 {
		t.Errorf("StartStep calls = %d, want 1", n)
	}
}

func TestStepsStartAgentGateRefusedInAWritingStep(t *testing.T) {
	t.Parallel()
	f := newStepStartFixture(t)
	linked := stepWorktreeTestAddLinked(t, stepGitTestRepo(t))
	gatesFile := filepath.Join(t.TempDir(), "gates.toml")
	stepGateWriteFile(t, gatesFile, stepAgentGateText("reviewer"))
	start := stepStartWriting(linked)
	start.Agent = ""
	start.Schema = ""
	start.DoneValue = ""
	start.GateName = "review"
	start.GatesFile = gatesFile

	id, err := f.steps.Start(t.Context(), start)
	checkStepStartRefused(t, f, id, err, ErrBadGate)
}

func TestStepsStartAgentGateRefusesCallerAgentSchemaOrDoneValue(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*StepStart)
	}{
		{name: "agent", mutate: func(s *StepStart) { s.Agent = "reviewer" }},
		{name: "schema", mutate: func(s *StepStart) { s.Schema = stepLoopSchema }},
		{name: "done value", mutate: func(s *StepStart) { s.DoneValue = stepLoopDone }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newStepStartFixture(t)
			gatesFile := filepath.Join(t.TempDir(), "gates.toml")
			stepGateWriteFile(t, gatesFile, stepAgentGateText("reviewer"))
			start := stepAgentGateStart(stepGitTestRepo(t), gatesFile)
			tc.mutate(&start)

			id, err := f.steps.Start(t.Context(), start)
			checkStepStartRefused(t, f, id, err, ErrBadGate)
		})
	}
}

func TestStepsStartAgentGateUnresolvedAgentStartsNothing(t *testing.T) {
	t.Parallel()
	f := newRefusalFixture(t, 2)
	repo := stepGitTestRepo(t)
	const agent = "no-such-agent-gate-probe"
	gatesFile := filepath.Join(t.TempDir(), "gates.toml")
	stepGateWriteFile(t, gatesFile, stepAgentGateText(agent))

	id, err := refusalStart(t, f.steps, stepAgentGateStart(repo, gatesFile))
	if !errors.Is(err, agents.ErrUnresolved) {
		t.Fatalf("Start error = %v, want it to match %v", err, agents.ErrUnresolved)
	}
	if got := connect.CodeOf(stepCommandError(err)); got != connect.CodeInvalidArgument {
		t.Errorf("refusal code = %v, want %v", got, connect.CodeInvalidArgument)
	}
	// Checked before refusalCheck, which commits a row of its own.
	rows, rowsErr := f.manager.store.EngineSessions(t.Context())
	if rowsErr != nil {
		t.Fatalf("EngineSessions: %v", rowsErr)
	}
	if len(rows) != 0 {
		t.Errorf("engine rows = %+v, want none after a refused start", rows)
	}
	refusalCheck(t, f, id, err)
	if !strings.Contains(err.Error(), agent) {
		t.Errorf("Start error = %v, want it to name agent %s", err, agent)
	}
	if names := refusalWorktrees(t, f.worktrees); len(names) != 0 {
		t.Errorf("worktrees = %v, want none after a refused start", names)
	}
}

func TestStepsStartNoGateWithoutAgentStartsNothing(t *testing.T) {
	cases := []struct {
		name   string
		schema string
		want   error
	}{
		{name: "no agent and no schema", schema: "", want: ErrBadSchema},
		{name: "no agent", schema: stepLoopSchema, want: ErrBadAgent},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newRefusalFixture(t, 2)
			start := stepStartReadOnly(stepGitTestRepo(t), "HEAD")
			start.Agent = ""
			start.Schema = tc.schema

			id, err := refusalStart(t, f.steps, start)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Start error = %v, want it to match %v", err, tc.want)
			}
			if got := connect.CodeOf(stepCommandError(err)); got != connect.CodeInvalidArgument {
				t.Errorf("refusal code = %v, want %v", got, connect.CodeInvalidArgument)
			}
			// Checked before refusalCheck, which commits a row of its own.
			rows, rowsErr := f.manager.store.EngineSessions(t.Context())
			if rowsErr != nil {
				t.Fatalf("EngineSessions: %v", rowsErr)
			}
			if len(rows) != 0 {
				t.Errorf("engine rows = %+v, want none after a refused start", rows)
			}
			refusalCheck(t, f, id, err)
			if names := refusalWorktrees(t, f.worktrees); len(names) != 0 {
				t.Errorf("worktrees = %v, want none after a refused start", names)
			}
		})
	}
}

func TestStepsStartArgvGateWithoutAgentOrSchemaStartsNothing(t *testing.T) {
	t.Parallel()
	f := newStepGateFixture(t)
	shell := stepGateShell(t)
	gatesFile := filepath.Join(t.TempDir(), "gates.toml")
	stepGateWriteFile(t, gatesFile, stepGateFileText(shell, "exit 0", 300))
	linked := stepWorktreeTestAddLinked(t, stepGitTestRepo(t))
	start := stepGateNamedStart(linked, "unit", gatesFile)
	start.Agent = ""
	start.Schema = ""

	id, err := f.steps.Start(t.Context(), start)
	checkStepStartRefused(t, f, id, err, ErrBadSchema)
}

// stepAgentGateRun returns the stepRun the only started step of f was started with. The test fails if it is not one.
func stepAgentGateRun(t *testing.T, f *stepStartFixture) (StepSession, *stepRun) {
	t.Helper()
	step := f.sessions.first(t)
	run, ok := step.Observer.(*stepRun)
	if !ok {
		t.Fatalf("StartStep Observer is %T, want *stepRun", step.Observer)
	}
	return step, run
}

func TestStepsStartAgentGateHandsItsRunTheResolvedGate(t *testing.T) {
	t.Parallel()
	f := newStepStartFixture(t)
	gatesFile := filepath.Join(t.TempDir(), "gates.toml")
	stepGateWriteFile(t, gatesFile, stepAgentGateText("reviewer"))

	if _, err := f.steps.Start(t.Context(), stepAgentGateStart(stepGitTestRepo(t), gatesFile)); err != nil {
		t.Fatalf("Start: %v", err)
	}

	_, run := stepAgentGateRun(t, f)
	if run.agentGate == nil {
		t.Fatal("step run agent gate is nil, want the review agent gate")
	}
	if want := stepAgentGate("reviewer"); !reflect.DeepEqual(*run.agentGate, want) {
		t.Errorf("step run agent gate = %+v, want %+v", *run.agentGate, want)
	}
}

func TestStepsStartAgentGateFailedAssertionEndsGateFailed(t *testing.T) {
	t.Parallel()
	f := newStepStartFixture(t)
	gatesFile := filepath.Join(t.TempDir(), "gates.toml")
	stepGateWriteFile(t, gatesFile, stepAgentGateText("reviewer"))

	id, err := f.steps.Start(t.Context(), stepAgentGateStart(stepGitTestRepo(t), gatesFile))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	step, _ := stepAgentGateRun(t, f)
	raw := verdictTestJSON(t, "done", []verdictTestEntry{
		verdictTestResult(verdictTestMust, gates.KindMust, "fails"),
		verdictTestResult(verdictTestMustNot, gates.KindMustNot, "holds"),
	}, []verdictTestFinding{verdictTestCiting(verdictTestMust)})
	step.Observer.Return(session.TurnEnded{StructuredOutput: raw})
	stepStartAwait(t, "the step to end", func() bool {
		row, err := f.steps.Status(t.Context(), id)
		return err == nil && row.End != ""
	})

	row, err := f.steps.Status(t.Context(), id)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	const reason = "fails: the change keeps the API"
	if row.End != stepLoopEndGateFailed || row.Status != "done" || row.Reason != reason {
		t.Fatalf("end = %q status %q reason %q, want gate_failed done with reason %q",
			row.End, row.Status, row.Reason, reason)
	}
	if row.Result != string(raw) {
		t.Errorf("result = %s, want the return kept as %s", row.Result, raw)
	}
}

func TestStepsStartGateWithoutAgentLeavesNoAgentGate(t *testing.T) {
	cases := []struct {
		name  string
		start func(t *testing.T) (*stepStartFixture, StepStart)
	}{
		{
			name: "argv gate",
			start: func(t *testing.T) (*stepStartFixture, StepStart) {
				t.Helper()
				f := newStepGateFixture(t)
				gatesFile := filepath.Join(t.TempDir(), "gates.toml")
				stepGateWriteFile(t, gatesFile, stepGateFileText(stepGateShell(t), "exit 0", 300))
				linked := stepWorktreeTestAddLinked(t, stepGitTestRepo(t))
				return f, stepGateNamedStart(linked, "unit", gatesFile)
			},
		},
		{
			name: "no gate",
			start: func(t *testing.T) (*stepStartFixture, StepStart) {
				t.Helper()
				f := newStepStartFixture(t)
				return f, stepStartWriting(stepWorktreeTestAddLinked(t, stepGitTestRepo(t)))
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, start := tc.start(t)

			if _, err := f.steps.Start(t.Context(), start); err != nil {
				t.Fatalf("Start: %v", err)
			}

			if _, run := stepAgentGateRun(t, f); run.agentGate != nil {
				t.Errorf("step run agent gate = %+v, want nil", *run.agentGate)
			}
		})
	}
}
