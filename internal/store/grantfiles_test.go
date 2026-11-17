package store_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// addRawTask adds task seq of buildEpic with status, owning files, through raw.
func addRawTask(t *testing.T, raw *sql.DB, seq int64, status string, files ...string) {
	t.Helper()
	if _, err := raw.Exec(
		`INSERT INTO epic_tasks (epic, seq, turn, title, deliverable, status) VALUES (?, ?, 1, ?, ?, ?)`,
		buildEpic,
		seq,
		"another task",
		"another change",
		status,
	); err != nil {
		t.Fatalf("insert task %d: %v", seq, err)
	}
	for i, file := range files {
		if _, err := raw.Exec(`INSERT INTO epic_task_files (epic, task, position, path) VALUES (?, ?, ?, ?)`,
			buildEpic, seq, i, file); err != nil {
			t.Fatalf("insert file %s of task %d: %v", file, seq, err)
		}
	}
}

// grantedBuild is the build GrantTaskFiles moves task 1 to from openBuildOf: its next rerun.
func grantedBuild() store.TaskBuild {
	build := openBuildOf()
	build.Rerun++
	return build
}

func TestGrantTaskFilesAddsTheFilesAfterTheTasksOwnAndMovesTheBuild(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, raw := openBuildStore(ctx, t)
	addRawTask(t, raw, 2, "failed", "b.go")
	if err := s.SetTaskBuild(ctx, buildEpic, 1, store.TaskBuild{}, openBuildOf()); err != nil {
		t.Fatalf("SetTaskBuild: %v", err)
	}
	if err := s.GrantTaskFiles(ctx, buildEpic, 1, openBuildOf(), grantedBuild(), []string{"c.go", "b.go"}); err != nil {
		t.Fatalf("GrantTaskFiles: %v", err)
	}
	task := buildTaskOf(ctx, t, s)
	if want := []string{"a.go", "c.go", "b.go"}; !reflect.DeepEqual(task.Files, want) {
		t.Errorf("Files = %v, want %v: the task's own, then the granted ones in order", task.Files, want)
	}
	if task.Build != grantedBuild() {
		t.Errorf("Build = %+v, want %+v", task.Build, grantedBuild())
	}
}

func TestGrantTaskFilesRefusesAFileAnotherOpenTaskOwnsAndWritesNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, raw := openBuildStore(ctx, t)
	addRawTask(t, raw, 2, "open", "b.go")
	if err := s.SetTaskBuild(ctx, buildEpic, 1, store.TaskBuild{}, openBuildOf()); err != nil {
		t.Fatalf("SetTaskBuild: %v", err)
	}
	before := storedState(t, raw)
	err := s.GrantTaskFiles(ctx, buildEpic, 1, openBuildOf(), grantedBuild(), []string{"c.go", "b.go"})
	var owned store.FileOwnedError
	if !errors.As(err, &owned) || owned != (store.FileOwnedError{File: "b.go", Task: 2}) {
		t.Fatalf("GrantTaskFiles err = %v, want b.go owned by T2", err)
	}
	if got := err.Error(); got != "b.go is owned by open task T2" {
		t.Errorf("error = %q, want it to name the file and the task", got)
	}
	if !reflect.DeepEqual(before, storedState(t, raw)) {
		t.Errorf("a refused grant changed the store")
	}
}

func TestGrantTaskFilesRefusesAStaleBuildAndWritesNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, raw := openBuildStore(ctx, t)
	before := storedState(t, raw)
	err := s.GrantTaskFiles(ctx, buildEpic, 1, openBuildOf(), grantedBuild(), []string{"c.go"})
	if !errors.Is(err, store.ErrTaskMoved) {
		t.Fatalf("GrantTaskFiles with a stale build err = %v, want ErrTaskMoved", err)
	}
	if !reflect.DeepEqual(before, storedState(t, raw)) {
		t.Errorf("a stale grant changed the store")
	}
}
