package planner

import (
	"context"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/store"
)

// closeActionPlan is a planner return with no task or question that closes its epic as abandoned.
const closeActionPlan = `{"status":"done","decisions":[],"tasks":[],"outline":"close the epic","questions":[],` +
	`"actions":[{"kind":"close_epic","args":"{\"close\":\"abandoned\"}",` +
	`"reason":"the epic is no longer wanted"}],"outcome":"complete"}`

// TestOrchestratorActionCheckRefusesAnActionWithoutItsNode checks that Check refuses a retry_node action that names no node,
// with the line naming the node field.
func TestOrchestratorActionCheckRefusesAnActionWithoutItsNode(t *testing.T) {
	t.Parallel()
	ret := Return{Actions: []EngineAction{{Kind: store.ActionRetryNode, Reason: "the node is parked"}}}
	checkLines(t, checkPlan(ret, nil), `actions[0] "retry_node" node: empty`)
}

// TestOrchestratorActionIsCarriedOutByTheNextPass checks that a turn's return stores its action, and that the next pass
// carries the action out: the open task fails, and the action is recorded as a decision that holds the planner's reason.
func TestOrchestratorActionIsCarriedOutByTheNextPass(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	steps := newFakeSteps()
	var reads atomic.Int64
	runner := New(runnerConfig(s, steps, agentReturning(runnerAgent, nil, &reads)))
	t.Cleanup(func() {
		cancel()
		runner.WaitTurns()
	})

	// validPlan with one more field: an action that fails task 1, the task validPlan plans.
	failTaskPlan := strings.Replace(validPlan, `"outcome":"work"}`,
		`"actions":[{"kind":"fail_task","args":"{\"task\":1}",`+
			`"reason":"the build cannot reach the store"}],"outcome":"work"}`, 1)
	turn, err := s.ClaimTurn(ctx, runnerEpicID, claimedAt())
	if err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	if err = runner.complete(ctx, runnerEpicID, turn.Seq, failTaskPlan); err != nil {
		t.Fatalf("complete turn 1: %v", err)
	}
	runner.Pass(ctx)

	epic := epicOf(t, s)
	if status := epic.Tasks[0].Status; status != taskFailed {
		t.Errorf("status of task 1 after the pass = %q, want %q", status, taskFailed)
	}
	want := []store.EpicDecision{{
		Seq: 1, ID: "action-1-1", Text: "planner fail task 1: the build cannot reach the store", Time: claimedAt(),
	}}
	if !reflect.DeepEqual(epic.Decisions, want) {
		t.Errorf("decisions after the pass = %+v, want %+v", epic.Decisions, want)
	}
}

// TestOrchestratorActionCloseStopsTheRunningStep checks that a close action the next pass carries out ends the running
// turn, and stops the step that turn runs through Steps.Stop.
func TestOrchestratorActionCloseStopsTheRunningStep(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	steps := newFakeSteps()
	var reads atomic.Int64
	runner := New(runnerConfig(s, steps, agentReturning(runnerAgent, nil, &reads)))

	first, err := s.ClaimTurn(ctx, runnerEpicID, claimedAt())
	if err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	if err = runner.complete(ctx, runnerEpicID, first.Seq, closeActionPlan); err != nil {
		t.Fatalf("complete turn 1: %v", err)
	}
	// A second trigger lets turn 2 run while the close is still pending, since the pass carries actions out first.
	if err = s.EndNodesWaking(ctx, runnerEpicID, nil,
		store.NodeTrigger{ID: "answer-close", Cause: "a trigger after the close"}, claimedAt()); err != nil {
		t.Fatalf("EndNodesWaking: %v", err)
	}
	second, err := s.ClaimTurn(ctx, runnerEpicID, claimedAt())
	if err != nil {
		t.Fatalf("ClaimTurn of turn 2: %v", err)
	}
	stepID, err := steps.Start(ctx, enginehost.StepStart{Epic: runnerEpicID})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err = s.SetTurnStep(ctx, runnerEpicID, second.Seq, stepID); err != nil {
		t.Fatalf("SetTurnStep: %v", err)
	}

	runner.Pass(ctx)
	if got, want := steps.stopCalls(), []string{stepID}; !reflect.DeepEqual(got, want) {
		t.Errorf("stopped steps = %q, want %q", got, want)
	}
}
