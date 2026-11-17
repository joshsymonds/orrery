package store_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

func TestNeedsFilesFailTaskStoresThePathsWithTheFailure(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, raw := openBuildStore(ctx, t)
	if err := s.SetTaskBuild(ctx, buildEpic, 1, store.TaskBuild{}, openBuildOf()); err != nil {
		t.Fatalf("SetTaskBuild: %v", err)
	}

	paths := []string{"internal/other/second.go", "internal/other/first.go"}
	if err := s.FailTask(ctx, buildEpic, 1, openBuildOf(), "returned needs_context: stuck", paths,
		writtenAt()); err != nil {
		t.Fatalf("FailTask: %v", err)
	}

	task := buildTaskOf(ctx, t, s)
	if task.Status != "failed" || task.Build.Failure != "returned needs_context: stuck" {
		t.Errorf("task status and failure = %q and %q, want failed and the reason", task.Status, task.Build.Failure)
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

func TestNeedsFilesFailTaskWithNoPathsStoresAnEmptyArray(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, raw := openBuildStore(ctx, t)
	if err := s.SetTaskBuild(ctx, buildEpic, 1, store.TaskBuild{}, openBuildOf()); err != nil {
		t.Fatalf("SetTaskBuild: %v", err)
	}

	if err := s.FailTask(ctx, buildEpic, 1, openBuildOf(), "red failed", nil, writtenAt()); err != nil {
		t.Fatalf("FailTask: %v", err)
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

func TestNeedsFilesRefusedFailTaskStoresNoPaths(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, raw := openBuildStore(ctx, t)
	before := storedState(t, raw)

	// The stored build is zero, so an open build names a different one and the write is refused.
	err := s.FailTask(ctx, buildEpic, 1, openBuildOf(), "returned needs_context: stuck", []string{"a.go"}, writtenAt())
	if !errors.Is(err, store.ErrTaskMoved) {
		t.Fatalf("FailTask with a stale build err = %v, want ErrTaskMoved", err)
	}
	if !reflect.DeepEqual(before, storedState(t, raw)) {
		t.Errorf("FailTask with a stale build changed the store")
	}
}
