package build

import (
	"context"
	"slices"
	"testing"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/store"
)

// The agents the role files of the rung 4 tests name. They are invented, and each role names its own agent.
const (
	orchAttemptImplementer  = "rung-implementer"
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

func TestTaskLadderOrchestratorAttemptRunsOrchestratorAgent(t *testing.T) {
	t.Parallel()
	setup := orchAttemptSetup(t,
		"orchestrator = \""+orchAttemptOrchestrator+"\"\nplanner = \""+orchAttemptPlanner+"\"\n")
	for _, attempt := range []int64{3, 5} {
		if got := orchAttemptStart(t, setup, attempt).Agent; got != orchAttemptImplementer {
			t.Errorf("attempt %d start Agent = %q, want the implementer's agent %q",
				attempt, got, orchAttemptImplementer)
		}
	}
	start := orchAttemptStart(t, setup, store.OrchestratorRung)
	if start.Agent != orchAttemptOrchestrator {
		t.Errorf("attempt 4 start Agent = %q, want the orchestrator's agent %q",
			start.Agent, orchAttemptOrchestrator)
	}
	task := startTask(setup.base)
	if !slices.Equal(start.Owned, task.Files) {
		t.Errorf("attempt 4 start Owned = %v, want the task's files %v", start.Owned, task.Files)
	}
	if start.CommandGate == nil || start.CommandGate.Name != task.Gate {
		t.Errorf("attempt 4 start CommandGate = %+v, want the task's gate %s", start.CommandGate, task.Gate)
	}
	if want := ImplementWorktree(setup.stateDir, setup.epic.ID, task.Seq); start.Workspace != want {
		t.Errorf("attempt 4 start Workspace = %s, want the task's implement worktree %s", start.Workspace, want)
	}
}

func TestTaskLadderOrchestratorAttemptFallsBackToPlanner(t *testing.T) {
	t.Parallel()
	setup := orchAttemptSetup(t, "planner = \""+orchAttemptPlanner+"\"\n")
	if got := orchAttemptStart(t, setup, store.OrchestratorRung).Agent; got != orchAttemptPlanner {
		t.Errorf("attempt 4 start Agent = %q, want the planner's agent %q when the role file has no orchestrator key",
			got, orchAttemptPlanner)
	}
}
