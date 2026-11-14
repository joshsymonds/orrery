package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// completedAt is the second the completion tests stamp on a finished turn.
func completedAt() time.Time {
	return time.Unix(1700000900, 0)
}

// plannedTurn is a valid planner return: two decisions, two tasks of two files and two Requirements each, one
// question, and the outline and outcome of a turn that works.
func plannedTurn() EpicPlan {
	return EpicPlan{
		Decisions: []string{"Store the plan in rows", "Keep decisions append only"},
		Tasks: []EpicTaskInput{
			{
				Title: "Write the store", Deliverable: "CompleteTurn stores the plan",
				Files: []string{"a.go", "b.go"}, Requirements: []string{"R5", "R6"}, Check: "just check",
			},
			{
				Title: "Read the store", Deliverable: "Reads see the plan",
				Files: []string{"c.go", "d.go"}, Requirements: []string{"R7", "R8"}, Check: "go test ./internal/store",
			},
		},
		Outline:   "Store, then read",
		Questions: []string{"Keep the old tasks?"},
		Outcome:   "work",
	}
}

// planTables returns the tables one planner turn writes.
func planTables() []string {
	return []string{
		"epic_turns", "epic_turn_decisions", "epic_tasks", "epic_task_files", "epic_task_requirements",
		"epic_questions", "epic_triggers",
	}
}

// planRowsOf returns the rows of every table a planner turn writes, by table name.
func planRowsOf(t *testing.T, s *Store) map[string][][]any {
	t.Helper()
	out := make(map[string][][]any)
	for _, table := range planTables() {
		out[table] = rowsOf(t, s.db, table)
	}
	return out
}

func TestCompleteTurnStoresEveryRowAndConsumesItsTriggers(t *testing.T) {
	ctx := context.Background()
	s := openEpicStore(t)
	const id = claimEpic
	createEpic(t, s, fullEpic(id))
	addPendingTrigger(t, s)
	if _, err := s.ClaimTurn(ctx, id, claimedAt()); err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}

	if err := s.CompleteTurn(ctx, id, 1, plannedTurn(), completedAt()); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}

	want := map[string][][]any{
		"epic_turn_decisions": {
			{id, int64(1), int64(1), "Store the plan in rows"},
			{id, int64(1), int64(2), "Keep decisions append only"},
		},
		"epic_tasks": {
			{id, int64(1), int64(1), "Write the store", "CompleteTurn stores the plan", "just check", "open"},
			{id, int64(2), int64(1), "Read the store", "Reads see the plan", "go test ./internal/store", "open"},
		},
		"epic_task_files": {
			{id, int64(1), int64(1), "a.go"},
			{id, int64(1), int64(2), "b.go"},
			{id, int64(2), int64(1), "c.go"},
			{id, int64(2), int64(2), "d.go"},
		},
		"epic_task_requirements": {
			{id, int64(1), int64(1), "R5"},
			{id, int64(1), int64(2), "R6"},
			{id, int64(2), int64(1), "R7"},
			{id, int64(2), int64(2), "R8"},
		},
		"epic_questions": {
			{id, int64(1), int64(1), "Keep the old tasks?", "open"},
		},
		"epic_turns": {
			{id, int64(1), "", "succeeded", "", int64(len(seededDecisions())), "Store, then read", "work",
				int64(1700000500), int64(1700000900), int64(0), int64(0), int64(0)},
		},
		"epic_triggers": {
			{id, createdTriggerID, createdTriggerCause, "consumed", int64(1), int64(1700000000)},
			{id, "t2", "test trigger", "consumed", int64(1), int64(1700000000)},
		},
	}
	got := planRowsOf(t, s)
	for _, table := range planTables() {
		if !reflect.DeepEqual(got[table], want[table]) {
			t.Errorf("%s after CompleteTurn = %v, want %v", table, got[table], want[table])
		}
	}
}

func TestSecondCompleteTurnContinuesTaskAndQuestionSeqs(t *testing.T) {
	ctx := context.Background()
	s := openEpicStore(t)
	const id = claimEpic
	createEpic(t, s, fullEpic(id))
	addPendingTrigger(t, s)
	if _, err := s.ClaimTurn(ctx, id, claimedAt()); err != nil {
		t.Fatalf("first ClaimTurn: %v", err)
	}
	if err := s.CompleteTurn(ctx, id, 1, plannedTurn(), completedAt()); err != nil {
		t.Fatalf("first CompleteTurn: %v", err)
	}

	if err := sqlcgen.New(s.db).InsertEpicTrigger(ctx, sqlcgen.InsertEpicTriggerParams{
		Epic: id, ID: "t3", Cause: "later trigger", State: "pending", Turn: 0, Created: epicCreated().Unix(),
	}); err != nil {
		t.Fatalf("insert trigger t3: %v", err)
	}
	if _, err := s.ClaimTurn(ctx, id, claimedAt().Add(time.Minute)); err != nil {
		t.Fatalf("second ClaimTurn: %v", err)
	}
	later := EpicPlan{
		Tasks: []EpicTaskInput{{
			Title: "Third task", Deliverable: "Seq continues", Files: []string{"e.go"}, Requirements: []string{"R9"},
			Check: "just check",
		}},
		Questions: []string{"Is the second turn right?"},
		Outcome:   "wait",
	}
	if err := s.CompleteTurn(ctx, id, 2, later, completedAt().Add(time.Minute)); err != nil {
		t.Fatalf("second CompleteTurn: %v", err)
	}

	want := map[string][][]any{
		"epic_turn_decisions": {
			{id, int64(1), int64(1), "Store the plan in rows"},
			{id, int64(1), int64(2), "Keep decisions append only"},
		},
		"epic_tasks": {
			{id, int64(1), int64(1), "Write the store", "CompleteTurn stores the plan", "just check", "open"},
			{id, int64(2), int64(1), "Read the store", "Reads see the plan", "go test ./internal/store", "open"},
			{id, int64(3), int64(2), "Third task", "Seq continues", "just check", "open"},
		},
		"epic_task_files": {
			{id, int64(1), int64(1), "a.go"},
			{id, int64(1), int64(2), "b.go"},
			{id, int64(2), int64(1), "c.go"},
			{id, int64(2), int64(2), "d.go"},
			{id, int64(3), int64(1), "e.go"},
		},
		"epic_task_requirements": {
			{id, int64(1), int64(1), "R5"},
			{id, int64(1), int64(2), "R6"},
			{id, int64(2), int64(1), "R7"},
			{id, int64(2), int64(2), "R8"},
			{id, int64(3), int64(1), "R9"},
		},
		"epic_questions": {
			{id, int64(1), int64(1), "Keep the old tasks?", "open"},
			{id, int64(2), int64(2), "Is the second turn right?", "open"},
		},
		"epic_turns": {
			{id, int64(1), "", "succeeded", "", int64(len(seededDecisions())), "Store, then read", "work",
				int64(1700000500), int64(1700000900), int64(0), int64(0), int64(0)},
			{id, int64(2), "", "succeeded", "", int64(len(seededDecisions())), "", "wait",
				int64(1700000560), int64(1700000960), int64(0), int64(0), int64(0)},
		},
		"epic_triggers": {
			{id, createdTriggerID, createdTriggerCause, "consumed", int64(1), int64(1700000000)},
			{id, "t2", "test trigger", "consumed", int64(1), int64(1700000000)},
			{id, "t3", "later trigger", "consumed", int64(2), int64(1700000000)},
		},
	}
	got := planRowsOf(t, s)
	for _, table := range planTables() {
		if !reflect.DeepEqual(got[table], want[table]) {
			t.Errorf("%s after second CompleteTurn = %v, want %v", table, got[table], want[table])
		}
	}
}

func TestCompleteTurnRefusesATurnThatIsNotRunningAndWritesNothing(t *testing.T) {
	ctx := context.Background()
	s := openEpicStore(t)
	const id = claimEpic
	createEpic(t, s, fullEpic(id))
	if _, err := s.ClaimTurn(ctx, id, claimedAt()); err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	if err := s.CompleteTurn(ctx, id, 1, plannedTurn(), completedAt()); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
	before := planRowsOf(t, s)

	if err := s.CompleteTurn(ctx, id, 1, plannedTurn(), completedAt()); !errors.Is(err, ErrTurnNotRunning) {
		t.Errorf("CompleteTurn of succeeded turn err = %v, want ErrTurnNotRunning", err)
	}
	if err := s.CompleteTurn(ctx, id, 9, plannedTurn(), completedAt()); !errors.Is(err, ErrTurnNotRunning) {
		t.Errorf("CompleteTurn of missing turn err = %v, want ErrTurnNotRunning", err)
	}
	if got := planRowsOf(t, s); !reflect.DeepEqual(got, before) {
		t.Errorf("plan rows after refused CompleteTurn = %v, want %v", got, before)
	}
}

func TestFailedCompleteTurnLeavesNoTaskQuestionOrConsumedTrigger(t *testing.T) {
	ctx := context.Background()
	s := openEpicStore(t)
	const id = claimEpic
	createEpic(t, s, fullEpic(id))
	addPendingTrigger(t, s)
	if _, err := s.ClaimTurn(ctx, id, claimedAt()); err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	// A decision already stored at position 2 makes the second decision of the return collide with it.
	const storedFirst = `INSERT INTO epic_turn_decisions (epic, turn, position, text) VALUES (?, 1, 2, 'stored first')`
	if _, err := s.db.Exec(storedFirst, id); err != nil {
		t.Fatalf("insert colliding decision: %v", err)
	}
	before := planRowsOf(t, s)

	if err := s.CompleteTurn(ctx, id, 1, plannedTurn(), completedAt()); err == nil {
		t.Fatalf("CompleteTurn with a colliding decision: err = nil, want a write failure")
	}
	if got := planRowsOf(t, s); !reflect.DeepEqual(got, before) {
		t.Errorf("plan rows after failed CompleteTurn = %v, want %v", got, before)
	}
	if got := rowsOf(t, s.db, "epic_tasks"); len(got) != 0 {
		t.Errorf("epic_tasks after failed CompleteTurn = %v, want none", got)
	}
}
