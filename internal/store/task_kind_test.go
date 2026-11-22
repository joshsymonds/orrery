package store

import (
	"context"
	"testing"
)

// TestDesignTaskKindStored checks that a turn storing a design task and an ordinary task reads the design task back with
// the design kind and the ordinary task with the empty kind, and that neither revises a task, since a revision link
// stores zero.
func TestDesignTaskKindStored(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	const id = claimEpic
	createEpic(t, s, fullEpic(id))
	addPendingTrigger(t, s)
	turn, err := s.ClaimTurn(ctx, id, claimedAt())
	if err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	design := dependsOnTask("Design the index")
	design.Kind = TaskKindDesign
	plan := EpicPlan{
		Outline: "Design the index, then build it",
		Tasks:   []EpicTaskInput{design, dependsOnTask("Build the index")},
		Outcome: "work",
	}
	if err = s.CompleteTurn(ctx, id, turn.Seq, plan, completedAt()); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}

	row, err := s.Epic(ctx, id)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	if len(row.Tasks) != 2 {
		t.Fatalf("epic has %d tasks, want 2", len(row.Tasks))
	}
	wantKind := map[int64]string{1: TaskKindDesign, 2: ""}
	for _, task := range row.Tasks {
		if task.Kind != wantKind[task.Seq] {
			t.Errorf("task %d Kind = %q, want %q", task.Seq, task.Kind, wantKind[task.Seq])
		}
		if task.Revises != 0 {
			t.Errorf("task %d Revises = %d, want 0", task.Seq, task.Revises)
		}
	}
}
