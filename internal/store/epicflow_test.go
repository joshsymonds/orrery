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
