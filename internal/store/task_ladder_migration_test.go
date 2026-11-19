package store

import (
	"context"
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// TestTaskLadderMigrationStepsDownAndUp checks the migration that adds the task ladder's tables. Its Down drops both
// tables and leaves the epic's task rows, files and versions as they were, and its Up restores both tables empty with
// those rows unchanged. The seeded task 1 is mid-ladder, at attempt 3 after a rescue version, and its rungs are written
// under the migration between the Up and the Down.
func TestTaskLadderMigrationStepsDownAndUp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, provider := storeAtVersion(t, 63)
	seedEpic(t, s.db.DB)
	if _, err := s.db.ExecContext(ctx, `INSERT INTO epic_tasks
		(epic, seq, turn, title, deliverable, status, gate, stage, base, attempt, rerun, step, failure)
		VALUES (?, 1, 1, 'Mid ladder', 'the first change', 'open', 'check', 'implement', 'b68d2a2', 3, 1, 'step-9',
		'returned blocked: the deliverable is unclear')`, seededEpic); err != nil {
		t.Fatalf("insert task: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO epic_task_files (epic, task, position, path)
		VALUES (?, 1, 0, 'a.go'), (?, 1, 1, 'b.go')`, seededEpic, seededEpic); err != nil {
		t.Fatalf("insert files: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO epic_task_versions
		(epic, task, version, source, reason, deliverable, created)
		VALUES (?, 1, 2, 'rescue', 'the rescue spec', 'the rescued deliverable', 1700000000)`, seededEpic); err != nil {
		t.Fatalf("insert version: %v", err)
	}
	if _, err := provider.UpByOne(ctx); err != nil {
		t.Fatalf("Up of 00064: %v", err)
	}
	insertLadder(t, s, []sqlcgen.InsertEpicTaskRungParams{
		{Epic: seededEpic, Task: 1, Rung: 1, Kind: RungAttempt, Created: 1700000100},
		{Epic: seededEpic, Task: 1, Rung: 2, Kind: RungAttempt, MovedBy: MovedByUnit, Cause: "the build exited 1",
			Step: "step-7", Created: 1700000200},
		{Epic: seededEpic, Task: 1, Rung: 3, Kind: RungRescue, MovedBy: MovedByFinding, Cause: "the claim failed",
			Gate: "review", Assertion: "the claim", Step: "step-8", Created: 1700000300},
	}, []sqlcgen.InsertEpicTaskRungOutcomeParams{
		{Epic: seededEpic, Task: 1, Rung: 1, Outcome: RungFailed, Reason: "the first build failed", Ended: 1700000150},
		{Epic: seededEpic, Task: 1, Rung: 2, Outcome: RungFailed, Reason: "the build failed again", Ended: 1700000250},
	})
	tasks := rowsOf(t, s.db.DB, "epic_tasks")
	files := rowsOf(t, s.db.DB, "epic_task_files")
	versions := rowsOf(t, s.db.DB, "epic_task_versions")

	if _, err := provider.Down(ctx); err != nil {
		t.Fatalf("Down of 00064: %v", err)
	}
	names := tableNames(t, s)
	if names["epic_task_rungs"] || names["epic_task_rung_outcomes"] {
		t.Errorf("task ladder tables after Down: rungs %t, outcomes %t, want both absent",
			names["epic_task_rungs"], names["epic_task_rung_outcomes"])
	}
	if got := rowsOf(t, s.db.DB, "epic_tasks"); !reflect.DeepEqual(got, tasks) {
		t.Errorf("epic_tasks after Down = %v, want %v", got, tasks)
	}
	if got := rowsOf(t, s.db.DB, "epic_task_files"); !reflect.DeepEqual(got, files) {
		t.Errorf("epic_task_files after Down = %v, want %v", got, files)
	}
	if got := rowsOf(t, s.db.DB, "epic_task_versions"); !reflect.DeepEqual(got, versions) {
		t.Errorf("epic_task_versions after Down = %v, want %v", got, versions)
	}

	if _, err := provider.UpByOne(ctx); err != nil {
		t.Fatalf("Up of 00064 again: %v", err)
	}
	names = tableNames(t, s)
	if !names["epic_task_rungs"] || !names["epic_task_rung_outcomes"] {
		t.Fatalf("task ladder tables after Up: rungs %t, outcomes %t, want both present",
			names["epic_task_rungs"], names["epic_task_rung_outcomes"])
	}
	if got := rowsOf(t, s.db.DB, "epic_task_rungs"); len(got) != 0 {
		t.Errorf("epic_task_rungs after Up = %v, want none", got)
	}
	if got := rowsOf(t, s.db.DB, "epic_task_rung_outcomes"); len(got) != 0 {
		t.Errorf("epic_task_rung_outcomes after Up = %v, want none", got)
	}
	if got := rowsOf(t, s.db.DB, "epic_tasks"); !reflect.DeepEqual(got, tasks) {
		t.Errorf("epic_tasks after Up = %v, want %v", got, tasks)
	}
	if got := rowsOf(t, s.db.DB, "epic_task_files"); !reflect.DeepEqual(got, files) {
		t.Errorf("epic_task_files after Up = %v, want %v", got, files)
	}
	if got := rowsOf(t, s.db.DB, "epic_task_versions"); !reflect.DeepEqual(got, versions) {
		t.Errorf("epic_task_versions after Up = %v, want %v", got, versions)
	}
}
