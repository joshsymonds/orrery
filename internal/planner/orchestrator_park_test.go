package planner

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// TestOrchestratorParkPlannerParkStartsATurnAndRoutesNoNotice checks that a planner park leaves its epic claimable: the
// decision over the stored state claims the epic, the claimed turn starts one planner step, and one attention pass
// routes no notice, since the planner, not the owner, answers the park. The epic first ends a turn with no park, so
// attention has an ended, untold turn to judge, and an owner park reported as the epic's park would route a notice on it.
func TestOrchestratorParkPlannerParkStartsATurnAndRoutesNoNotice(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	if err := s.CompleteTurn(ctx, runnerEpicID, claimAttentionTurn(t, s), store.EpicPlan{Outcome: "wait"},
		claimedAt()); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
	if err := s.ParkEpic(
		ctx,
		runnerEpicID,
		store.ParkClassNode,
		"workflow node check failed: boom",
		claimedAt(),
	); err != nil {
		t.Fatalf("ParkEpic: %v", err)
	}
	steps := newFakeSteps()
	var reads atomic.Int64
	runner := New(runnerConfig(s, steps, agentReturning(runnerAgent, nil, &reads)))

	states, err := s.PlanStates(ctx)
	if err != nil {
		t.Fatalf("PlanStates: %v", err)
	}
	var state store.EpicPlanState
	found := false
	for _, st := range states {
		if st.Epic == runnerEpicID {
			state, found = st, true
		}
	}
	if !found {
		t.Fatalf("PlanStates lists no state for epic %s", runnerEpicID)
	}
	action := Decide(State{
		Paused: state.Paused, Parked: state.Parked != "", Running: state.Running, Pending: state.Pending,
	}, nil)
	if action.Kind != ActionClaim {
		t.Fatalf("Decide over the planner-parked epic = %q, want %q", action.Kind, ActionClaim)
	}
	turn, ok := runner.claim(ctx, runnerEpicID)
	if !ok {
		t.Fatalf("claim of the planner-parked epic %s = none, want a turn", runnerEpicID)
	}
	if _, err = runner.startStep(ctx, runnerEpicID, turn, runnerAgent); err != nil {
		t.Fatalf("startStep: %v", err)
	}
	if n := len(steps.started()); n != 1 {
		t.Errorf("planner steps started = %d, want 1", n)
	}

	router := &fakeAttentionRouter{}
	attentionOver(s, router).pass(ctx)
	if len(router.notices) != 0 {
		t.Errorf("notices after one attention pass = %d, want none: a planner park is the planner's to answer",
			len(router.notices))
	}
}
