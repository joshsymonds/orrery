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
	grant := store.TaskVersionInput{Source: store.SourceGrant, Files: []string{"c.go", "b.go"}}
	if err := s.GrantTaskFiles(ctx, buildEpic, 1, openBuildOf(), grantedBuild(), grant, writtenAt()); err != nil {
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

func TestGrantTaskFilesAddsAFileAnotherOpenTaskOwns(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, raw := openBuildStore(ctx, t)
	addRawTask(t, raw, 2, "open", "b.go")
	if err := s.SetTaskBuild(ctx, buildEpic, 1, store.TaskBuild{}, openBuildOf()); err != nil {
		t.Fatalf("SetTaskBuild: %v", err)
	}
	grant := store.TaskVersionInput{Source: store.SourceGrant, Files: []string{"c.go", "b.go"}}
	if err := s.GrantTaskFiles(ctx, buildEpic, 1, openBuildOf(), grantedBuild(), grant, writtenAt()); err != nil {
		t.Fatalf("GrantTaskFiles of b.go, which open T2 owns: %v", err)
	}
	if want := []string{"a.go", "c.go", "b.go"}; !reflect.DeepEqual(buildTaskOf(ctx, t, s).Files, want) {
		t.Errorf("Files of task 1 = %v, want %v", buildTaskOf(ctx, t, s).Files, want)
	}
}

func TestGrantTaskFilesRefusesAStaleBuildAndWritesNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, raw := openBuildStore(ctx, t)
	before := storedState(t, raw)
	grant := store.TaskVersionInput{Source: store.SourceGrant, Files: []string{"c.go"}}
	err := s.GrantTaskFiles(ctx, buildEpic, 1, openBuildOf(), grantedBuild(), grant, writtenAt())
	if !errors.Is(err, store.ErrTaskMoved) {
		t.Fatalf("GrantTaskFiles with a stale build err = %v, want ErrTaskMoved", err)
	}
	if !reflect.DeepEqual(before, storedState(t, raw)) {
		t.Errorf("a stale grant changed the store")
	}
}
