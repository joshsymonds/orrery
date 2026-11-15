package store

import (
	"context"
	"reflect"
	"testing"
)

// integrationBase is the base commit the integration tests create their epics at. It is a synthetic sha.
const integrationBase = "0123456789abcdef0123456789abcdef01234567"

// TestCreateEpicStoresStoredBaseEqualToBase reads a created epic through EpicFlow, the read the interpreter decides
// from: its stored base is its base and it has no candidate. The Epic read is pinned by
// TestCreatedEpicReadsBaseGatesListsAndItemsInOrder.
func TestCreateEpicStoresStoredBaseEqualToBase(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	const id = "orrery-1a2b3c4/epic-integrate"
	row := fullEpic(id)
	row.Base = integrationBase
	createEpic(t, s, row)

	want := fullEpic(id)
	want.Base = integrationBase
	want.StoredBase = integrationBase
	want.Decisions = seededDecisions()

	flow, err := s.EpicFlow(ctx, id)
	if err != nil {
		t.Fatalf("EpicFlow: %v", err)
	}
	if !reflect.DeepEqual(flow.Epic, want) {
		t.Errorf("EpicFlow epic after CreateEpic differs:\n got %+v\nwant %+v", flow.Epic, want)
	}
}

// TestPlannerTaskReadsEmptyBatch reads the tasks a planner turn stored, each with no batch, then a batch set on one.
func TestPlannerTaskReadsEmptyBatch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	const id = claimEpic
	row := fullEpic(id)
	row.Base = integrationBase
	createEpic(t, s, row)
	addPendingTrigger(t, s)
	if _, err := s.ClaimTurn(ctx, id, claimedAt()); err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	if err := s.CompleteTurn(ctx, id, 1, plannedTurn(), completedAt()); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}

	got, err := s.Epic(ctx, id)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	if len(got.Tasks) != 2 {
		t.Fatalf("tasks after CompleteTurn = %d, want 2", len(got.Tasks))
	}
	for _, task := range got.Tasks {
		if task.Batch != "" {
			t.Errorf("task %d batch = %q, want empty", task.Seq, task.Batch)
		}
	}

	const batch = "plan/S1/integrate"
	if _, err := s.db.ExecContext(ctx, `UPDATE epic_tasks SET batch = ? WHERE epic = ? AND seq = 1`,
		batch, id); err != nil {
		t.Fatalf("set batch of task 1: %v", err)
	}
	got, err = s.Epic(ctx, id)
	if err != nil {
		t.Fatalf("Epic after batch: %v", err)
	}
	if read := got.Tasks[0].Batch; read != batch {
		t.Errorf("task 1 batch read back = %q, want %q", read, batch)
	}
}
