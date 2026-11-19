package store_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// needsFilesClimb climbs task 1 of buildEpic off from one rung with cause, storing paths as the task's needs files.
func needsFilesClimb(ctx context.Context, s *store.Store, from store.TaskBuild, cause string, paths []string) error {
	to := store.ClimbedBuild(from, store.RungOf(from)+1, cause)
	move := store.RungMove{MovedBy: store.MovedByUnit, Cause: cause, NeedsFiles: paths}
	return s.ClimbTask(ctx, buildEpic, 1, from, to, move, nil, nil, writtenAt())
}

func TestNeedsFilesClimbStoresThePathsWithTheCause(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, raw := openBuildStore(ctx, t)
	if err := s.SetTaskBuild(ctx, buildEpic, 1, store.TaskBuild{}, openBuildOf()); err != nil {
		t.Fatalf("SetTaskBuild: %v", err)
	}

	paths := []string{"internal/other/second.go", "internal/other/first.go"}
	if err := needsFilesClimb(ctx, s, openBuildOf(), "returned needs_context: stuck", paths); err != nil {
		t.Fatalf("ClimbTask: %v", err)
	}

	task := buildTaskOf(ctx, t, s)
	if task.Status != "open" || task.Build.Failure != "returned needs_context: stuck" {
		t.Errorf("task status and failure = %q and %q, want open and the reason", task.Status, task.Build.Failure)
	}
	if !reflect.DeepEqual(task.NeedsFiles, paths) {
		t.Errorf("NeedsFiles = %v, want %v in the order named", task.NeedsFiles, paths)
	}
	want := [][]any{{`["internal/other/second.go","internal/other/first.go"]`}}
	got := rawRows(t, raw, `SELECT needs_files FROM epic_tasks WHERE epic = ? AND seq = 1`, buildEpic)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("stored needs_files = %v, want %v", got, want)
	}
}

func TestNeedsFilesClimbWithNoPathsStoresAnEmptyArray(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, raw := openBuildStore(ctx, t)
	if err := s.SetTaskBuild(ctx, buildEpic, 1, store.TaskBuild{}, openBuildOf()); err != nil {
		t.Fatalf("SetTaskBuild: %v", err)
	}

	if err := needsFilesClimb(ctx, s, openBuildOf(), "red failed", nil); err != nil {
		t.Fatalf("ClimbTask: %v", err)
	}

	if task := buildTaskOf(ctx, t, s); len(task.NeedsFiles) != 0 {
		t.Errorf("NeedsFiles = %v, want none", task.NeedsFiles)
	}
	want := [][]any{{"[]"}}
	got := rawRows(t, raw, `SELECT needs_files FROM epic_tasks WHERE epic = ? AND seq = 1`, buildEpic)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("stored needs_files = %v, want %v", got, want)
	}
}

func TestNeedsFilesRefusedClimbStoresNoPaths(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, raw := openBuildStore(ctx, t)
	before := storedState(t, raw)

	// The stored build is zero, so an open build names a different one and the write is refused.
	err := needsFilesClimb(ctx, s, openBuildOf(), "returned needs_context: stuck", []string{"a.go"})
	if !errors.Is(err, store.ErrTaskMoved) {
		t.Fatalf("ClimbTask with a stale build err = %v, want ErrTaskMoved", err)
	}
	if !reflect.DeepEqual(before, storedState(t, raw)) {
		t.Errorf("ClimbTask with a stale build changed the store")
	}
}
