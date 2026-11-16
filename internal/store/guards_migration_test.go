package store

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
)

// guardsEpic is the accepted epic at base main that the guard tests plan the tasks of.
const guardsEpic = "orrery-1a2b3c4/epic-guards"

// TestPlannedTestChangesReadBackInPositionOrder plans a task with test changes out of path order and a task without
// any, and reads both back through Epic: each task keeps its paths in the order the plan gave them, and the task
// without any has none.
func TestPlannedTestChangesReadBackInPositionOrder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	row := fullEpic(guardsEpic)
	row.Base = "main"
	createEpic(t, s, row)
	if _, err := s.ClaimTurn(ctx, guardsEpic, claimedAt()); err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	plan := plannedTurn()
	plan.Tasks[0].TestChanges = []string{"internal/store/zeta_test.go", "internal/store/alpha_test.go"}
	plan.Tasks[1].TestChanges = nil
	if err := s.CompleteTurn(ctx, guardsEpic, 1, plan, completedAt()); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}

	got, err := s.Epic(ctx, guardsEpic)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	if len(got.Tasks) != 2 {
		t.Fatalf("tasks read back = %d, want 2", len(got.Tasks))
	}
	want := [][]string{{"internal/store/zeta_test.go", "internal/store/alpha_test.go"}, nil}
	for i, task := range got.Tasks {
		if !reflect.DeepEqual(task.TestChanges, want[i]) {
			t.Errorf("task %d test changes = %v, want %v", task.Seq, task.TestChanges, want[i])
		}
	}
}

// TestSetTaskBuildStoresGuardAndRefusesAnotherGuard writes a build with a guard refusal and reads it back as the
// stored build. A write from the same build with another guard is ErrTaskMoved and leaves the build as it was.
func TestSetTaskBuildStoresGuardAndRefusesAnotherGuard(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := plannedBuildEpic(t)

	from := buildTaskOf(t, s, 1).Build
	to := TaskBuild{Stage: "red", Base: buildBase, Guard: "refused: the test changes no test"}
	if err := s.SetTaskBuild(ctx, buildEpic, 1, from, to); err != nil {
		t.Fatalf("SetTaskBuild: %v", err)
	}
	if got := buildTaskOf(t, s, 1).Build; got != to {
		t.Errorf("build after SetTaskBuild = %+v, want %+v", got, to)
	}

	before := rowsOf(t, s.db.DB, "epic_tasks")
	stale := to
	stale.Guard = "refused: another refusal"
	err := s.SetTaskBuild(ctx, buildEpic, 1, stale, TaskBuild{Stage: "implement", Base: buildBase, Guard: ""})
	if !errors.Is(err, ErrTaskMoved) {
		t.Fatalf("SetTaskBuild from another guard: err = %v, want ErrTaskMoved", err)
	}
	if got := rowsOf(t, s.db.DB, "epic_tasks"); !reflect.DeepEqual(got, before) {
		t.Errorf("epic_tasks after refused SetTaskBuild = %v, want %v", got, before)
	}
}

// TestGuardsMigrationGivesVersion37TasksNoGuardOrTestChanges stores a task at migration 37 and reads it back after
// Open migrates the store: its guard is empty and it has no test changes. Down to 37 removes the guard column and the
// test changes table, and Up restores them, so the epic reads back as it did before.
func TestGuardsMigrationGivesVersion37TasksNoGuardOrTestChanges(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")

	const id = seededEpic
	fixture := openStoreAtVersion(t, path, 37)
	seedEpic(t, fixture)
	if _, err := fixture.ExecContext(ctx, `INSERT INTO epic_tasks (epic, seq, turn, title, deliverable, status)
		VALUES (?, 1, 1, 'Build it', 'the migration', 'open')`, id); err != nil {
		fixture.Close()
		t.Fatalf("seed task: %v", err)
	}
	if err := fixture.Close(); err != nil {
		t.Fatalf("close fixture: %v", err)
	}

	s, err := Open(ctx, path, noSync())
	if err != nil {
		t.Fatalf("Open at version 37: %v", err)
	}
	defer s.Close()

	before, err := s.Epic(ctx, id)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	if len(before.Tasks) != 1 {
		t.Fatalf("tasks after Open = %d, want 1", len(before.Tasks))
	}
	if guard := before.Tasks[0].Build.Guard; guard != "" {
		t.Errorf("guard after Open = %q, want empty", guard)
	}
	if changes := before.Tasks[0].TestChanges; len(changes) != 0 {
		t.Errorf("test changes after Open = %v, want none", changes)
	}

	provider := migrationProvider(t, s.db.DB)
	if _, err := provider.DownTo(ctx, 37); err != nil {
		t.Fatalf("DownTo 37: %v", err)
	}
	if hasAny(columnsOf(t, s, "epic_tasks"), "guard") {
		t.Errorf("epic_tasks columns after Down = %v, want no guard", columnsOf(t, s, "epic_tasks"))
	}
	if tableNames(t, s)["epic_task_test_changes"] {
		t.Errorf("epic_task_test_changes after Down: present, want absent")
	}

	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if !tableNames(t, s)["epic_task_test_changes"] {
		t.Errorf("epic_task_test_changes after Up: absent, want present")
	}
	after, err := s.Epic(ctx, id)
	if err != nil {
		t.Fatalf("Epic after Up: %v", err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Errorf("epic after Down and Up = %+v, want %+v", after, before)
	}
}
