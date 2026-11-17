package planner

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// TestOrchestratorParkPlannerParkStartsATurnAndRoutesNoNotice checks that a planner park leaves its epic claimable: one
// pass of the runner starts the planner's turn through the step fakes, and one attention pass routes no notice, since
// the planner, not the owner, answers the park. The epic first ends a turn with no park, so attention has an ended,
// untold turn to judge, and an owner park reported as the epic's park would route a notice on it.
func TestOrchestratorParkPlannerParkStartsATurnAndRoutesNoNotice(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
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
	t.Cleanup(func() {
		cancel()
		runner.WaitTurns()
	})

	runner.Pass(ctx)
	waitForStarts(t, steps, 1)

	router := &fakeAttentionRouter{}
	attentionOver(s, router).pass(ctx)
	if len(router.notices) != 0 {
		t.Errorf("notices after one attention pass = %d, want none: a planner park is the planner's to answer",
			len(router.notices))
	}
}
