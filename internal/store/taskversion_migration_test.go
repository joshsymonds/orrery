package store

import (
	"context"
	"reflect"
	"testing"
)

// TestTaskVersionMigrationStepsDownAndUp checks the migration that adds epic_task_versions. Its Down drops the table
// and leaves the epic's task rows and files as they were, and its Up restores an empty table with those rows
// unchanged. The seeded task 1 is open mid-attempt, so its build state is what the Down and Up must keep.
func TestTaskVersionMigrationStepsDownAndUp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, provider := storeAtVersion(t, 62)
	seedEpic(t, s.db.DB)
	if _, err := s.fixtureExec(ctx, `INSERT INTO epic_tasks
		(epic, seq, turn, title, deliverable, status, gate, stage, base, attempt, rerun, step, failure)
		VALUES (?, 1, 1, 'Mid attempt', 'the first change', 'open', 'check', 'implement', 'b68d2a2', 2, 1, 'step-7',
		'returned blocked: the deliverable is unclear'),
		(?, 2, 1, 'Second', 'the second change', 'done', 'check', '', '', 0, 0, '', '')`,
		seededEpic, seededEpic); err != nil {
		t.Fatalf("insert tasks: %v", err)
	}
	if _, err := s.fixtureExec(ctx, `INSERT INTO epic_task_files (epic, task, position, path)
		VALUES (?, 1, 0, 'a.go'), (?, 1, 1, 'b.go'), (?, 2, 0, 'c.go')`,
		seededEpic, seededEpic, seededEpic); err != nil {
		t.Fatalf("insert files: %v", err)
	}
	if _, err := provider.UpByOne(ctx); err != nil {
		t.Fatalf("Up of 00063: %v", err)
	}
	if _, err := s.fixtureExec(ctx, `INSERT INTO epic_task_versions
		(epic, task, version, source, reason, deliverable, created)
		VALUES (?, 1, 2, 'orchestrator', 'the spec moved', 'the new deliverable', 1700000000)`, seededEpic); err != nil {
		t.Fatalf("insert version: %v", err)
	}
	tasks := rowsOf(t, s.db.DB, "epic_tasks")
	files := rowsOf(t, s.db.DB, "epic_task_files")

	if _, err := provider.Down(ctx); err != nil {
		t.Fatalf("Down of 00063: %v", err)
	}
	if tableNames(t, s)["epic_task_versions"] {
		t.Errorf("epic_task_versions after Down: present, want absent")
	}
	if got := rowsOf(t, s.db.DB, "epic_tasks"); !reflect.DeepEqual(got, tasks) {
		t.Errorf("epic_tasks after Down = %v, want %v", got, tasks)
	}
	if got := rowsOf(t, s.db.DB, "epic_task_files"); !reflect.DeepEqual(got, files) {
		t.Errorf("epic_task_files after Down = %v, want %v", got, files)
	}

	if _, err := provider.UpByOne(ctx); err != nil {
		t.Fatalf("Up of 00063 again: %v", err)
	}
	if !tableNames(t, s)["epic_task_versions"] {
		t.Fatalf("epic_task_versions after Up: absent, want present")
	}
	if got := rowsOf(t, s.db.DB, "epic_task_versions"); len(got) != 0 {
		t.Errorf("epic_task_versions after Up = %v, want none", got)
	}
	if got := rowsOf(t, s.db.DB, "epic_tasks"); !reflect.DeepEqual(got, tasks) {
		t.Errorf("epic_tasks after Up = %v, want %v", got, tasks)
	}
	if got := rowsOf(t, s.db.DB, "epic_task_files"); !reflect.DeepEqual(got, files) {
		t.Errorf("epic_task_files after Up = %v, want %v", got, files)
	}
}
