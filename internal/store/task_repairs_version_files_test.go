package store_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// versionFileState returns the rows a version write that must change nothing leaves alone: the build write tables, and
// the files and versions of the tasks.
func versionFileState(t *testing.T, raw *sql.DB) map[string][][]any {
	t.Helper()
	state := storedState(t, raw)
	state["epic_task_files"] = rawRows(t, raw, `SELECT * FROM epic_task_files ORDER BY epic, task, position`)
	state["epic_task_versions"] = rawRows(t, raw, `SELECT * FROM epic_task_versions ORDER BY epic, task, version`)
	return state
}

// rerunWithCheckingFiles reruns task 1 of buildEpic to openBuildOf with a version that changes its evidence to
// checkingEvidence and its files: it adds b.go and removes a.go. The version is stored checking, so the task's files
// stay a.go until it settles.
func rerunWithCheckingFiles(ctx context.Context, t *testing.T, s *store.Store) {
	t.Helper()
	item := checkingEvidence()
	version := store.TaskVersionInput{
		Source: store.SourceOrchestrator, Step: "step-2", Reason: "the evidence and files move", Evidence: &item,
		Files: []string{"b.go"}, Remove: []string{"a.go"},
	}
	err := s.RerunTask(
		ctx,
		buildEpic,
		1,
		store.TaskBuild{},
		openBuildOf(),
		version,
		nil,
		requestDecision(),
		writtenAt(),
	)
	if err != nil {
		t.Fatalf("RerunTask: %v", err)
	}
}

func TestTaskRepairsVersionFilesRerunAddsAndRemovesFiles(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openBuildStore(ctx, t)
	first := store.TaskVersionInput{Source: store.SourceOrchestrator, Files: []string{"b.go"}}
	if err := s.RerunTask(ctx, buildEpic, 1, store.TaskBuild{}, openBuildOf(), first, nil, requestDecision(),
		writtenAt()); err != nil {
		t.Fatalf("first RerunTask: %v", err)
	}

	rerun := openBuildOf()
	rerun.Rerun = 1
	second := store.TaskVersionInput{
		Source: store.SourceOrchestrator,
		Files:  []string{"c.go"},
		Remove: []string{"a.go"},
	}
	decision := store.TaskDecision{ID: "T1-request-2", Text: "T1 rerun: a.go is the wrong file"}
	if err := s.RerunTask(ctx, buildEpic, 1, openBuildOf(), rerun, second, nil, decision, writtenAt()); err != nil {
		t.Fatalf("second RerunTask: %v", err)
	}

	task := buildTaskOf(ctx, t, s)
	if want := []string{"b.go", "c.go"}; !reflect.DeepEqual(task.Files, want) {
		t.Errorf("Files = %v, want the old list minus a.go plus c.go: %v", task.Files, want)
	}
	if len(task.Versions) != 2 {
		t.Fatalf("Versions = %+v, want the two versions the reruns stored", task.Versions)
	}
	got := task.Versions[1]
	if want := []string{"b.go", "c.go"}; got.Files == nil || !reflect.DeepEqual(*got.Files, want) {
		t.Errorf("version %d Files = %v, want the task's whole list %v", got.Version, got.Files, want)
	}
}

func TestTaskRepairsVersionFilesCheckingVersionKeepsFilesUntilSettledApplied(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openBuildStore(ctx, t)
	rerunWithCheckingFiles(ctx, t, s)

	task := buildTaskOf(ctx, t, s)
	if want := []string{"a.go"}; !reflect.DeepEqual(task.Files, want) {
		t.Errorf("Files = %v while the version checks, want the task's files unchanged %v", task.Files, want)
	}
	checking, ok := task.Checking()
	if want := []string{"b.go"}; !ok || checking.Files == nil || !reflect.DeepEqual(*checking.Files, want) {
		t.Errorf("Checking() = %+v, %t, want the version with the whole list %v", checking, ok, want)
	}

	settledAt := time.Unix(1700000900, 0)
	err := s.SettleTaskVersion(ctx, buildEpic, 1, openBuildOf(), openBuildOf(), store.TaskVersionSettle{
		Version: 2, State: store.VersionApplied, RedRun: checkingRedRun,
	}, settledAt)
	if err != nil {
		t.Fatalf("SettleTaskVersion: %v", err)
	}
	settled := buildTaskOf(ctx, t, s)
	if want := []string{"b.go"}; !reflect.DeepEqual(settled.Files, want) {
		t.Errorf("Files = %v after the applied settle, want the version's list %v", settled.Files, want)
	}
}

func TestTaskRepairsVersionFilesRefusedSettleKeepsFiles(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openBuildStore(ctx, t)
	rerunWithCheckingFiles(ctx, t, s)

	const refusal = "red: the conclusion holds at the base"
	err := s.SettleTaskVersion(ctx, buildEpic, 1, openBuildOf(), openBuildOf(), store.TaskVersionSettle{
		Version: 2, State: store.VersionRefused, RedRun: checkingRedRun, Refusal: refusal,
	}, writtenAt())
	if err != nil {
		t.Fatalf("SettleTaskVersion: %v", err)
	}

	task := buildTaskOf(ctx, t, s)
	if want := []string{"a.go"}; !reflect.DeepEqual(task.Files, want) {
		t.Errorf("Files = %v after the refused settle, want the task's files unchanged %v", task.Files, want)
	}
	if len(task.Versions) != 1 || task.Versions[0].State != store.VersionRefused {
		t.Errorf("Versions = %+v, want the one version refused", task.Versions)
	}
}

func TestTaskRepairsVersionFilesSettleRefusesAFileAnotherOpenTaskNowOwns(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, raw := openBuildStore(ctx, t)
	addRawTask(t, raw, 3, "open")
	item := checkingEvidence()
	version := store.TaskVersionInput{
		Source: store.SourceOrchestrator, Step: "step-2", Reason: "the evidence and files move", Evidence: &item,
		Files: []string{"c.go"},
	}
	err := s.RerunTask(ctx, buildEpic, 1, store.TaskBuild{}, openBuildOf(), version, nil, requestDecision(),
		writtenAt())
	if err != nil {
		t.Fatalf("RerunTask: %v", err)
	}

	// Task 1 does not own c.go while its version checks, so task 3 can be granted it before the settle.
	grant := store.TaskVersionInput{Source: store.SourceGrant, Files: []string{"c.go"}}
	granted := store.TaskBuild{Stage: "implement", Base: buildBase, Attempt: 1, Step: "step-1"}
	if err = s.GrantTaskFiles(ctx, buildEpic, 3, store.TaskBuild{}, granted, grant, writtenAt()); err != nil {
		t.Fatalf("GrantTaskFiles of task 3: %v", err)
	}

	err = s.SettleTaskVersion(ctx, buildEpic, 1, openBuildOf(), openBuildOf(), store.TaskVersionSettle{
		Version: 2, State: store.VersionApplied, RedRun: checkingRedRun,
	}, time.Unix(1700000900, 0))
	var owned store.FileOwnedError
	if !errors.As(err, &owned) || owned != (store.FileOwnedError{File: "c.go", Task: 3}) {
		t.Fatalf("SettleTaskVersion err = %v, want c.go owned by T3", err)
	}

	task := buildTaskOf(ctx, t, s)
	if want := []string{"a.go"}; !reflect.DeepEqual(task.Files, want) {
		t.Errorf("Files = %v after the refused settle, want the task's files unchanged %v", task.Files, want)
	}
	if checking, ok := task.Checking(); !ok || checking.Version != 2 {
		t.Errorf("Checking() = %+v, %t, want version 2 still checking", checking, ok)
	}
}

func TestTaskRepairsVersionFilesRemoveOfUnownedFileWritesNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, raw := openBuildStore(ctx, t)
	before := versionFileState(t, raw)
	version := store.TaskVersionInput{Source: store.SourceOrchestrator, Remove: []string{"z.go"}}
	err := s.RerunTask(ctx, buildEpic, 1, store.TaskBuild{}, openBuildOf(), version, nil, requestDecision(),
		writtenAt())
	if err == nil {
		t.Fatal("RerunTask removing z.go, which task 1 does not own, err = nil, want an error")
	}
	if !reflect.DeepEqual(before, versionFileState(t, raw)) {
		t.Errorf("a removal of an unowned file changed the store")
	}
}

func TestTaskRepairsVersionFilesLeavingNoFilesWritesNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, raw := openBuildStore(ctx, t)
	before := versionFileState(t, raw)
	version := store.TaskVersionInput{Source: store.SourceOrchestrator, Remove: []string{"a.go"}}
	err := s.RerunTask(ctx, buildEpic, 1, store.TaskBuild{}, openBuildOf(), version, nil, requestDecision(),
		writtenAt())
	if err == nil {
		t.Fatal("RerunTask removing a.go, the task's only file, err = nil, want an error")
	}
	if !reflect.DeepEqual(before, versionFileState(t, raw)) {
		t.Errorf("a version leaving the task no files changed the store")
	}
}
