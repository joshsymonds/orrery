package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

// TestEpicFlowReadsEpicRunsAndPendingOfOneState checks that EpicFlow returns the epic Epic returns, the node runs
// NodeRuns returns, and the pending triggers of its epic only, as each trigger is claimed or consumed.
func TestEpicFlowReadsEpicRunsAndPendingOfOneState(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	const id = "orrery-1a2b3c4/epic-flow"
	const other = "orrery-1a2b3c4/epic-flow-other"
	createEpic(t, s, fullEpic(id))
	createEpic(t, s, fullEpic(other))

	runs := []NodeRun{{Path: "plan", ID: "id-plan", Node: "plan", Kind: "plan"}}
	first := NodeTrigger{ID: "workflow:id-one", Cause: "workflow node one started"}
	second := NodeTrigger{ID: "workflow:id-two", Cause: "workflow node two started"}
	third := NodeTrigger{ID: "workflow:id-three", Cause: "workflow node three started"}
	if err := s.StartNodes(ctx, id, runs, &first, epicCreated()); err != nil {
		t.Fatalf("StartNodes of first trigger: %v", err)
	}
	if err := s.StartNodes(ctx, id, nil, &second, epicCreated()); err != nil {
		t.Fatalf("StartNodes of second trigger: %v", err)
	}
	if err := s.StartNodes(ctx, other, nil, &third, epicCreated()); err != nil {
		t.Fatalf("StartNodes of other epic's trigger: %v", err)
	}

	checkFlow := func(want int) {
		t.Helper()
		flow, err := s.EpicFlow(ctx, id)
		if err != nil {
			t.Fatalf("EpicFlow: %v", err)
		}
		epic, err := s.Epic(ctx, id)
		if err != nil {
			t.Fatalf("Epic: %v", err)
		}
		nodes, err := s.NodeRuns(ctx, id)
		if err != nil {
			t.Fatalf("NodeRuns: %v", err)
		}
		if len(nodes) != 1 {
			t.Fatalf("NodeRuns = %+v, want the plan run", nodes)
		}
		if !reflect.DeepEqual(flow.Epic, epic) {
			t.Errorf("EpicFlow epic = %+v, want the epic Epic returns %+v", flow.Epic, epic)
		}
		if !reflect.DeepEqual(flow.Runs, nodes) {
			t.Errorf("EpicFlow runs = %+v, want the runs NodeRuns returns %+v", flow.Runs, nodes)
		}
		if flow.Pending != want {
			t.Errorf("EpicFlow pending = %d, want %d", flow.Pending, want)
		}
	}

	// CreateEpic leaves its own trigger pending, so the epic counts it with the two started above, and never the other
	// epic's trigger.
	checkFlow(3)
	if _, err := s.db.ExecContext(
		ctx, `UPDATE epic_triggers SET state = 'claimed', turn = 3 WHERE epic = ? AND id = ?`, id, first.ID,
	); err != nil {
		t.Fatalf("claim first trigger: %v", err)
	}
	checkFlow(2)
	if _, err := s.db.ExecContext(
		ctx, `UPDATE epic_triggers SET state = 'consumed', turn = 3 WHERE epic = ? AND id = ?`, id, second.ID,
	); err != nil {
		t.Fatalf("consume second trigger: %v", err)
	}
	checkFlow(1)
}

// TestEpicFlowOfUnknownEpicIsErrNoEpic checks that EpicFlow of an id no epic has returns an error matching ErrNoEpic.
func TestEpicFlowOfUnknownEpicIsErrNoEpic(t *testing.T) {
	t.Parallel()
	s := openEpicStore(t)
	if _, err := s.EpicFlow(context.Background(), "orrery-1a2b3c4/missing"); !errors.Is(err, ErrNoEpic) {
		t.Fatalf("EpicFlow of unknown id err = %v, want ErrNoEpic", err)
	}
}

// TestEpicFlowReadsReviewAndLabeledStepsOfItsReviewRuns checks that EpicFlow returns the review EpicReview returns, and
// after the agent steps exactly the steps labeled with its epic and the path of one of its review or closure runs, each
// once, in the order of its runs.
func TestEpicFlowReadsReviewAndLabeledStepsOfItsReviewRuns(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	const id = "orrery-1a2b3c4/epic-flow-review"
	const other = "orrery-1a2b3c4/epic-flow-review-other"
	createEpic(t, s, fullEpic(id))

	const agentRunID = "id-agent"
	runs := []NodeRun{
		{Path: "agent", ID: agentRunID, Node: "agent", Kind: "agent", Attempt: 1},
		{Path: "review", ID: "id-review", Node: "review", Kind: "review", Attempt: 1},
		{Path: "correct@1/closure", ID: "id-closure", Node: "closure", Kind: "closure", Attempt: 1},
	}
	if err := s.StartNodes(ctx, id, runs, nil, epicCreated()); err != nil {
		t.Fatalf("StartNodes: %v", err)
	}
	if err := s.FreezeReview(ctx, id, "review", 1, "rev-frozen"); err != nil {
		t.Fatalf("FreezeReview: %v", err)
	}
	ops := []EngineOp{
		stepAddOf(NodeWorkID(agentRunID, 1, "step")),
		labelledStepOf("step-agent-path", id, "agent"),
		labelledStepOf("step-review-a", id, "review"),
		labelledStepOf("step-review-b", id, "review"),
		labelledStepOf("step-closure", id, "correct@1/closure"),
		labelledStepOf("step-other-epic", other, "review"),
		labelledStepOf("step-no-run", id, "plan"),
	}
	if err := s.ApplyEngine(ctx, ops, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}

	flow, err := s.EpicFlow(ctx, id)
	if err != nil {
		t.Fatalf("EpicFlow: %v", err)
	}
	review, err := s.EpicReview(ctx, id)
	if err != nil {
		t.Fatalf("EpicReview: %v", err)
	}
	if flow.Review.Frozen != "rev-frozen" || !reflect.DeepEqual(flow.Review, review) {
		t.Errorf("EpicFlow review = %+v, want the review EpicReview returns %+v", flow.Review, review)
	}
	want := []string{NodeWorkID(agentRunID, 1, "step"), "step-closure", "step-review-a", "step-review-b"}
	if got := stepIDsOf(flow.Steps); !reflect.DeepEqual(got, want) {
		t.Fatalf("EpicFlow steps = %v, want %v", got, want)
	}
	for _, row := range flow.Steps {
		got, err := s.Step(ctx, row.ID)
		if err != nil {
			t.Fatalf("Step %s: %v", row.ID, err)
		}
		if !reflect.DeepEqual(row, got) {
			t.Errorf("EpicFlow step %s = %+v, want the row Step returns %+v", row.ID, row, got)
		}
	}
}

// TestEpicFlowWithoutReviewHasNoLabeledSteps checks that EpicFlow of an epic whose review has not started returns the
// zero Review, and no labeled step even when it is labeled with the path of a run that is not a review or closure.
func TestEpicFlowWithoutReviewHasNoLabeledSteps(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	const id = "orrery-1a2b3c4/epic-flow-no-review"
	createEpic(t, s, fullEpic(id))
	runs := []NodeRun{{Path: "plan", ID: "id-plan", Node: "plan", Kind: "plan", Attempt: 1}}
	if err := s.StartNodes(ctx, id, runs, nil, epicCreated()); err != nil {
		t.Fatalf("StartNodes: %v", err)
	}
	if err := s.ApplyEngine(ctx, []EngineOp{labelledStepOf("step-plan", id, "plan")}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}

	flow, err := s.EpicFlow(ctx, id)
	if err != nil {
		t.Fatalf("EpicFlow: %v", err)
	}
	if !reflect.DeepEqual(flow.Review, Review{}) {
		t.Errorf("EpicFlow review = %+v, want the zero Review", flow.Review)
	}
	if len(flow.Steps) != 0 {
		t.Errorf("EpicFlow steps = %v, want none", stepIDsOf(flow.Steps))
	}
}
