package build

import (
	"context"
	"slices"
	"testing"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/store"
)

// The agents the role files of the rung 3 tests name. They are invented, and each role names its own agent.
const (
	orchAttemptImplementer  = "rung-implementer"
	orchAttemptAttempt      = "rung-attempt"
	orchAttemptOrchestrator = "rung-orchestrator"
	orchAttemptPlanner      = "rung-planner"
)

// orchAttemptSetup returns newStartSetup's setup with its role file rewritten to the implementer and verifier of
// newStartSetup and the lines given, as the orchestrator pass tests write their role files.
func orchAttemptSetup(t *testing.T, lines string) startSetup {
	t.Helper()
	setup := newStartSetup(t, readoptCommandGate("check"))
	writeReadoptFile(t, setup.rolesPath,
		"implementer = \""+orchAttemptImplementer+"\"\nverifier = \"claude-verify\"\n"+lines)
	return setup
}

// orchAttemptStart starts the implement step of startTask at attempt, and returns the one start it recorded.
func orchAttemptStart(t *testing.T, setup startSetup, attempt int64) enginehost.StepStart {
	t.Helper()
	task := startTask(setup.base)
	task.Build.Attempt = attempt
	id := StepID(setup.epic.ID, task.Seq, StageImplement, attempt, task.Build.Rerun, 0, 0)
	steps := &startSteps{}
	if _, err := startRunner(setup, steps, &startEvidence{}).startStep(
		context.Background(), setup.epic, View{Task: task},
		Action{Kind: ActStartStep, Unit: StageImplement, ID: id},
	); err != nil {
		t.Fatalf("startStep(implement) at attempt %d = %v, want nil", attempt, err)
	}
	return onlyStart(t, steps)
}

func TestRungThreeWritesRunsAttemptAgent(t *testing.T) {
	t.Parallel()
	setup := orchAttemptSetup(t, "attempt = \""+orchAttemptAttempt+"\"\n"+
		"orchestrator = \""+orchAttemptOrchestrator+"\"\nplanner = \""+orchAttemptPlanner+"\"\n")
	for _, attempt := range []int64{1, 2, 4, 5} {
		if got := orchAttemptStart(t, setup, attempt).Agent; got != orchAttemptImplementer {
			t.Errorf("attempt %d start Agent = %q, want the implementer's agent %q",
				attempt, got, orchAttemptImplementer)
		}
	}
	start := orchAttemptStart(t, setup, store.OrchestratorRung)
	if start.Agent != orchAttemptAttempt {
		t.Errorf("attempt 3 start Agent = %q, want the attempt agent %q", start.Agent, orchAttemptAttempt)
	}
	task := startTask(setup.base)
	if !slices.Equal(start.Owned, task.Files) {
		t.Errorf("attempt 3 start Owned = %v, want the task's files %v", start.Owned, task.Files)
	}
	if start.CommandGate == nil || start.CommandGate.Name != task.Gate {
		t.Errorf("attempt 3 start CommandGate = %+v, want the task's gate %s", start.CommandGate, task.Gate)
	}
	if want := ImplementWorktree(
		setup.stateDir,
		setup.epic.ID,
		store.EpicTask{Seq: task.Seq},
	); start.Workspace != want {
		t.Errorf("attempt 3 start Workspace = %s, want the task's implement worktree %s", start.Workspace, want)
	}
}

func TestRungThreeWritesFallsBackToImplementer(t *testing.T) {
	t.Parallel()
	setup := orchAttemptSetup(t,
		"orchestrator = \""+orchAttemptOrchestrator+"\"\nplanner = \""+orchAttemptPlanner+"\"\n")
	if got := orchAttemptStart(t, setup, store.OrchestratorRung).Agent; got != orchAttemptImplementer {
		t.Errorf("attempt 3 start Agent = %q, want the implementer's agent %q when the role file has no attempt key",
			got, orchAttemptImplementer)
	}
}

func TestRungThreeWritesNeverResolvesOrchestratorOrPlanner(t *testing.T) {
	t.Parallel()
	setup := orchAttemptSetup(t, "orchestrator = 5\nplanner = true\n")
	if got := orchAttemptStart(t, setup, store.OrchestratorRung).Agent; got != orchAttemptImplementer {
		t.Errorf("attempt 3 start Agent = %q, want the implementer's agent %q, never the orchestrator's or planner's",
			got, orchAttemptImplementer)
	}
}
