package enginehost

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/gates"
	"github.com/joshsymonds/orrery/internal/store"
)

// stepCommandGateData returns a command gate named unit that runs argv with a limit of five minutes and holds pool_a
// twice and pool_b once, the pools newStepGateFixture declares.
func stepCommandGateData(argv []string) gates.Gate {
	return gates.Gate{
		Name:  "unit",
		Argv:  argv,
		Limit: 300 * time.Second,
		Needs: []gates.Need{{Pool: "pool_a", Count: 2}, {Pool: "pool_b", Count: 1}},
	}
}

// stepCommandGateStart returns a valid writing start in workspace that runs gate given as data.
func stepCommandGateStart(workspace string, gate *gates.Gate) StepStart {
	start := stepStartWriting(workspace)
	start.CommandGate = gate
	return start
}

// stepCommandGateSessions is the stepStartSessions a command gate test drives. Its Continue records each message and
// succeeds, so a return the gate fails is sent back and the test can read the send.
type stepCommandGateSessions struct {
	*stepStartSessions

	sendMu    sync.Mutex
	continued []StepPrompt
}

// Continue records prompt and returns nil.
func (f *stepCommandGateSessions) Continue(_ context.Context, _ string, prompt StepPrompt) error {
	f.sendMu.Lock()
	defer f.sendMu.Unlock()
	f.continued = append(f.continued, prompt)
	return nil
}

// continuedCount returns how many messages Continue has been sent.
func (f *stepCommandGateSessions) continuedCount() int {
	f.sendMu.Lock()
	defer f.sendMu.Unlock()
	return len(f.continued)
}

// stepCommandGateSubmit starts a writing step whose command gate runs script under sh, commits the owned path in the
// step's worktree, and hands the step its done return naming that commit. It returns the fixture, the sessions the step
// drives and the step id.
func stepCommandGateSubmit(t *testing.T, script string) (*stepStartFixture, *stepCommandGateSessions, string) {
	t.Helper()
	f := newStepGateFixture(t)
	sessions := &stepCommandGateSessions{stepStartSessions: f.sessions}
	f.steps.sessions = sessions
	f.steps.pools = &fakeGatePools{}
	shell := stepGateShell(t)
	linked := stepWorktreeTestAddLinked(t, stepGitTestRepo(t))
	gate := stepCommandGateData([]string{shell, "-c", script})

	id, err := f.steps.Start(t.Context(), stepCommandGateStart(linked, &gate))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	commit := stepGitTestCommit(t, linked, map[string]string{stepDoneOwned: "owned\n"})
	run, ok := f.sessions.first(t).Observer.(*stepRun)
	if !ok {
		t.Fatalf("StartStep Observer is %T, want *stepRun", f.sessions.first(t).Observer)
	}
	run.Return(stepDoneReturn(commit))
	return f, sessions, id
}

func TestStepsStartCommandGateStoresTheGateAsData(t *testing.T) {
	t.Parallel()
	f := newStepGateFixture(t)
	shell := stepGateShell(t)
	linked := stepWorktreeTestAddLinked(t, stepGitTestRepo(t))
	commit := stepGitTestRun(t, linked, "rev-parse", "HEAD")
	gate := stepCommandGateData([]string{shell, "-c", "exit 0"})
	start := stepCommandGateStart(linked, &gate)

	id, err := f.steps.Start(t.Context(), start)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	want := store.StepAdd{
		ID:         id,
		Agent:      start.Agent,
		Prompt:     start.Prompt,
		Schema:     start.Schema,
		Workspace:  linked,
		Revision:   commit,
		DoneValue:  stepLoopDone,
		Owned:      []string{stepDoneOwned},
		Gate:       []string{shell, "-c", "exit 0"},
		GateName:   "unit",
		GateSource: stepGateSourceData,
		GateNeeds:  []store.StepNeed{{Pool: "pool_a", Count: 2}, {Pool: "pool_b", Count: 1}},
		GateLimit:  300 * time.Second,
		ModelLimit: stepDefaultModelLimit,
	}
	if got := stepGateAdd(t, f.sessions.first(t)); !reflect.DeepEqual(got, want) {
		t.Fatalf("StepAdd = %#v, want %#v", got, want)
	}
}

func TestStepsCommandGateDoneReturnEndsDoneWhenGateExitsZero(t *testing.T) {
	t.Parallel()
	f, _, id := stepCommandGateSubmit(t, "exit 0")

	stepStartAwait(t, engineOf(f.steps), "step end", func() bool {
		row, err := f.store.Step(t.Context(), id)
		return err == nil && row.End != ""
	})
	row, err := f.store.Step(t.Context(), id)
	if err != nil {
		t.Fatalf("Step: %v", err)
	}
	if row.End != stepLoopEndDone || row.Status != stepLoopDone {
		t.Fatalf("end = %q status %q, want done DONE", row.End, row.Status)
	}
	if gate := stepDoneCheck(t, row.Checks, stepLoopGateName); !gate.OK {
		t.Errorf("gate check = %+v, want passing", gate)
	}
}

func TestStepsCommandGateDoneReturnSendsBackWhenGateExitsOne(t *testing.T) {
	t.Parallel()
	f, sessions, id := stepCommandGateSubmit(t, "exit 1")

	stepStartAwait(t, engineOf(f.steps), "return sent back", func() bool { return sessions.continuedCount() == 1 })
	row, err := f.store.Step(t.Context(), id)
	if err != nil {
		t.Fatalf("Step: %v", err)
	}
	if row.End != "" {
		t.Fatalf("end = %q, want the step open after a failed gate", row.End)
	}
	gate := stepDoneCheck(t, stepDoneRoundChecks(row.Checks, 1), stepLoopGateName)
	if gate.OK || !strings.Contains(gate.Detail, "gate exited 1") {
		t.Errorf("round 1 gate check = %+v, want a failing exit 1", gate)
	}
}

func TestStepsStartCommandGateRefusalsStoreNothing(t *testing.T) {
	t.Parallel()
	// Each case names a gate that would otherwise resolve or start, so the refusal is what the test checks.
	cases := []struct {
		name   string
		mutate func(t *testing.T, start *StepStart)
	}{
		{name: "gate name in the project file", mutate: func(t *testing.T, s *StepStart) {
			t.Helper()
			stepGateWriteFile(t, filepath.Join(s.Workspace, ".orrery", "project.toml"),
				fmt.Sprintf("[gates.unit]\nargv = [%q, \"-c\", \"exit 0\"]\nlimit = 120\n", stepGateShell(t)))
			s.GateName = "unit"
		}},
		{name: "gate name and gates file", mutate: func(t *testing.T, s *StepStart) {
			t.Helper()
			gatesFile := filepath.Join(t.TempDir(), "gates.toml")
			stepGateWriteFile(t, gatesFile, stepGateFileText(stepGateShell(t), "exit 0", 300))
			s.GateName = "unit"
			s.GatesFile = gatesFile
		}},
		{name: "agent gate", mutate: func(_ *testing.T, s *StepStart) {
			agent := stepAgentGate("reviewer")
			s.AgentGate = &agent
			s.ReadOnly = true
			s.Owned = nil
			s.Agent = ""
			s.Schema = ""
			s.DoneValue = ""
		}},
		{name: "read-only start", mutate: func(_ *testing.T, s *StepStart) {
			s.ReadOnly = true
			s.Owned = nil
			s.DoneValue = ""
		}},
		{name: "no name", mutate: func(_ *testing.T, s *StepStart) { s.CommandGate.Name = "" }},
		{name: "no argv", mutate: func(_ *testing.T, s *StepStart) { s.CommandGate.Argv = nil }},
		{name: "gate agent", mutate: func(_ *testing.T, s *StepStart) { s.CommandGate.Agent = "reviewer" }},
		{name: "gate assertions", mutate: func(_ *testing.T, s *StepStart) {
			s.CommandGate.Assertions = []gates.Assertion{{Text: "the change is small", Kind: gates.KindMust}}
		}},
		{name: "zero limit", mutate: func(_ *testing.T, s *StepStart) { s.CommandGate.Limit = 0 }},
		{name: "limit under a second", mutate: func(_ *testing.T, s *StepStart) {
			s.CommandGate.Limit = 500 * time.Millisecond
		}},
		{name: "undeclared pool", mutate: func(_ *testing.T, s *StepStart) {
			s.CommandGate.Needs = []gates.Need{{Pool: "pool_missing", Count: 1}}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newStepGateFixture(t)
			linked := stepWorktreeTestAddLinked(t, stepGitTestRepo(t))
			gate := stepCommandGateData([]string{stepGateShell(t), "-c", "exit 0"})
			start := stepCommandGateStart(linked, &gate)
			tc.mutate(t, &start)

			id, err := f.steps.Start(t.Context(), start)
			checkStepStartRefused(t, f, id, err, ErrBadGate)
		})
	}
}
