package store

import (
	"context"
	"reflect"
	"testing"
)

// ownFile gives task 1 of climbEpic the file path, as its planner did.
func ownFile(ctx context.Context, t *testing.T, s *Store, path string) {
	t.Helper()
	if _, err := s.fixtureExec(ctx, `INSERT INTO epic_task_files (epic, task, position, path)
		VALUES (?, 1, 0, ?)`, climbEpic, path); err != nil {
		t.Fatalf("own %s: %v", path, err)
	}
}

// refusedBy returns the decision that refused path for task 1 of climbEpic, read from the store.
func refusedBy(ctx context.Context, t *testing.T, s *Store, path string) string {
	t.Helper()
	var decision string
	if err := s.db.QueryRowContext(ctx,
		`SELECT decision FROM epic_task_refused WHERE epic = ? AND task = 1 AND path = ?`,
		climbEpic, path).Scan(&decision); err != nil {
		t.Fatalf("read refusal of %s: %v", path, err)
	}
	return decision
}

// TestTaskRepairsGrantRerunStoresTheRefusedFilesWithItsDecision checks a rerun carried out with a decision that refused
// a file: the file is read back with the task as refused, the granted file joins the task's files, and the refused file
// does not.
func TestTaskRepairsGrantRerunStoresTheRefusedFilesWithItsDecision(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	from := climbFrom()
	seedClimbTask(t, s, from)
	ownFile(ctx, t, s, "a.go")
	to := from
	to.Rerun = 1
	decision := TaskDecision{ID: "T1-request-3", Text: "T1 grant b.go", Refused: []string{"c.go"}}
	version := TaskVersionInput{Source: SourceOrchestrator, Files: []string{"b.go"}}
	if err := s.RerunTask(ctx, climbEpic, 1, from, to, version, nil, decision, climbedAt()); err != nil {
		t.Fatalf("RerunTask: %v", err)
	}
	task := climbTaskOf(ctx, t, s, 1)
	if want := []string{"c.go"}; !reflect.DeepEqual(task.Refused, want) {
		t.Errorf("Refused = %v, want %v", task.Refused, want)
	}
	if want := []string{"a.go", "b.go"}; !reflect.DeepEqual(task.Files, want) {
		t.Errorf("Files = %v, want %v", task.Files, want)
	}
	if got := refusedBy(ctx, t, s, "c.go"); got != "T1-request-3" {
		t.Errorf("c.go refused by %q, want T1-request-3", got)
	}
}

// TestTaskRepairsGrantClimbStoresTheRefusedFilesWithItsDecision checks a fail decision that refused a file, carried out
// by a climb: the file is read back with the task as refused, next to the needs files the climb stores.
func TestTaskRepairsGrantClimbStoresTheRefusedFilesWithItsDecision(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	from := climbFrom()
	seedClimbTask(t, s, from)
	move := RungMove{MovedBy: MovedByFailDecision, Cause: climbCause, NeedsFiles: []string{"c.go"}}
	decision := TaskDecision{ID: "T1-request-2", Text: "T1 fail", Refused: []string{"c.go"}}
	to := ClimbedBuild(from, RungOf(from)+1, climbCause)
	if err := s.ClimbTask(ctx, climbEpic, 1, from, to, move, nil, &decision, climbedAt()); err != nil {
		t.Fatalf("ClimbTask: %v", err)
	}
	task := climbTaskOf(ctx, t, s, 1)
	if want := []string{"c.go"}; !reflect.DeepEqual(task.Refused, want) || !reflect.DeepEqual(task.NeedsFiles, want) {
		t.Errorf("Refused %v and NeedsFiles %v, want %v and %v", task.Refused, task.NeedsFiles, want, want)
	}
}

// TestTaskRepairsGrantRefusalKeepsTheFirstDecisionThatRefusedIt checks a file refused again by a later decision: the
// refusal stays with the decision that refused it first, and the file is listed once.
func TestTaskRepairsGrantRefusalKeepsTheFirstDecisionThatRefusedIt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	from := climbFrom()
	seedClimbTask(t, s, from)
	first := from
	first.Rerun = 1
	if err := s.RerunTask(ctx, climbEpic, 1, from, first, TaskVersionInput{Source: SourceOrchestrator}, nil,
		TaskDecision{ID: "T1-request-1", Text: "T1 rerun", Refused: []string{"c.go"}}, climbedAt()); err != nil {
		t.Fatalf("first RerunTask: %v", err)
	}
	second := first
	second.Rerun = 2
	if err := s.RerunTask(ctx, climbEpic, 1, first, second, TaskVersionInput{Source: SourceOrchestrator}, nil,
		TaskDecision{ID: "T1-request-2", Text: "T1 rerun again", Refused: []string{"c.go"}}, climbedAt()); err != nil {
		t.Fatalf("second RerunTask: %v", err)
	}
	if got := refusedBy(ctx, t, s, "c.go"); got != "T1-request-1" {
		t.Errorf("c.go refused by %q, want the first decision T1-request-1", got)
	}
	if got := climbTaskOf(ctx, t, s, 1).Refused; !reflect.DeepEqual(got, []string{"c.go"}) {
		t.Errorf("Refused = %v, want [c.go] once", got)
	}
}

// TestTaskRepairsGrantGrantedFileIsNoLongerRefused checks a later decision that adds a refused file: the task owns it
// then, so it is no longer refused.
func TestTaskRepairsGrantGrantedFileIsNoLongerRefused(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	from := climbFrom()
	seedClimbTask(t, s, from)
	refusing := from
	refusing.Rerun = 1
	if err := s.RerunTask(ctx, climbEpic, 1, from, refusing, TaskVersionInput{Source: SourceOrchestrator}, nil,
		TaskDecision{ID: "T1-request-1", Text: "T1 rerun", Refused: []string{"c.go"}}, climbedAt()); err != nil {
		t.Fatalf("refusing RerunTask: %v", err)
	}
	granting := refusing
	granting.Rerun = 2
	version := TaskVersionInput{Source: SourceOrchestrator, Files: []string{"c.go"}}
	if err := s.RerunTask(ctx, climbEpic, 1, refusing, granting, version, nil,
		TaskDecision{ID: "T1-request-2", Text: "T1 grant c.go"}, climbedAt()); err != nil {
		t.Fatalf("granting RerunTask: %v", err)
	}
	task := climbTaskOf(ctx, t, s, 1)
	if len(task.Refused) != 0 {
		t.Errorf("Refused = %v, want none once c.go is granted", task.Refused)
	}
	if want := []string{"c.go"}; !reflect.DeepEqual(task.Files, want) {
		t.Errorf("Files = %v, want %v", task.Files, want)
	}
}

// TestTaskRepairsGrantRefusedOwnedFileWritesNothing checks that a decision refusing a file the task owns is an error,
// and that nothing it carries out is stored: the build does not move and no decision is recorded.
func TestTaskRepairsGrantRefusedOwnedFileWritesNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	from := climbFrom()
	seedClimbTask(t, s, from)
	ownFile(ctx, t, s, "a.go")
	to := from
	to.Rerun = 1
	decision := TaskDecision{ID: "T1-request-1", Text: "T1 rerun", Refused: []string{"a.go"}}
	if err := s.RerunTask(ctx, climbEpic, 1, from, to, TaskVersionInput{Source: SourceOrchestrator}, nil, decision,
		climbedAt()); err == nil {
		t.Fatal("RerunTask refusing an owned file = nil, want an error")
	}
	task := climbTaskOf(ctx, t, s, 1)
	if task.Build != from || len(task.Refused) != 0 {
		t.Errorf("build %+v and refused %v, want the build unmoved and nothing refused", task.Build, task.Refused)
	}
}

// TestTaskRepairsGrantMigrationWidensMovedByAndSourceAndStepsDown checks migration 00073: before it, a rung's moved_by
// and a version's source refuse send_back, after it both accept it, and every row stored before it is kept as it was.
// Its Down removes the rows that only send_back wrote, and keeps the rest.
func TestTaskRepairsGrantMigrationWidensMovedByAndSourceAndStepsDown(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, provider := storeAtVersion(t, 72)
	if _, err := s.fixtureExec(ctx, `INSERT INTO epic_task_rungs (epic, task, rung, kind, moved_by, created)
		VALUES ('repairs', 1, 2, 'attempt', 'unit', 1700000000)`); err != nil {
		t.Fatalf("insert rung: %v", err)
	}
	if _, err := s.fixtureExec(ctx, `INSERT INTO epic_task_versions (epic, task, version, source, reason, created)
		VALUES ('repairs', 1, 2, 'orchestrator', 'the spec moved', 1700000000)`); err != nil {
		t.Fatalf("insert version: %v", err)
	}
	rungs := rowsOf(t, s.db.DB, "epic_task_rungs")
	versions := rowsOf(t, s.db.DB, "epic_task_versions")
	if _, err := s.fixtureExec(ctx, `INSERT INTO epic_task_rungs (epic, task, rung, kind, moved_by, created)
		VALUES ('repairs', 1, 3, 'rescue', 'send_back', 1700000001)`); err == nil {
		t.Fatal("a send_back rung before migration 00073 = nil, want the check to refuse it")
	}

	if _, err := provider.UpByOne(ctx); err != nil {
		t.Fatalf("Up of 00073: %v", err)
	}
	if got := rowsOf(t, s.db.DB, "epic_task_rungs"); !reflect.DeepEqual(got, rungs) {
		t.Errorf("epic_task_rungs after Up = %v, want %v", got, rungs)
	}
	if got := rowsOf(t, s.db.DB, "epic_task_versions"); !reflect.DeepEqual(got, versions) {
		t.Errorf("epic_task_versions after Up = %v, want %v", got, versions)
	}
	if !tableNames(t, s)["epic_task_refused"] {
		t.Fatal("epic_task_refused after Up: absent, want present")
	}
	if _, err := s.fixtureExec(ctx, `INSERT INTO epic_task_rungs (epic, task, rung, kind, moved_by, created)
		VALUES ('repairs', 1, 3, 'rescue', 'send_back', 1700000001)`); err != nil {
		t.Fatalf("send_back rung after Up: %v", err)
	}
	if _, err := s.fixtureExec(ctx, `INSERT INTO epic_task_versions (epic, task, version, source, reason, created)
		VALUES ('repairs', 1, 3, 'send_back', 'the send-back', 1700000001)`); err != nil {
		t.Fatalf("send_back version after Up: %v", err)
	}

	if _, err := provider.Down(ctx); err != nil {
		t.Fatalf("Down of 00073: %v", err)
	}
	if tableNames(t, s)["epic_task_refused"] {
		t.Error("epic_task_refused after Down: present, want absent")
	}
	if got := rowsOf(t, s.db.DB, "epic_task_rungs"); !reflect.DeepEqual(got, rungs) {
		t.Errorf("epic_task_rungs after Down = %v, want %v", got, rungs)
	}
	if got := rowsOf(t, s.db.DB, "epic_task_versions"); !reflect.DeepEqual(got, versions) {
		t.Errorf("epic_task_versions after Down = %v, want %v", got, versions)
	}
}
