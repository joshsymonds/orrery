package store

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// buildEpic is the accepted epic at base main that the build tests plan the tasks of.
const buildEpic = "orrery-1a2b3c4/epic-build"

// buildBase is the revision the build tests give a task's build as its base.
const buildBase = "b68d2a24e4bfc11691be94b7acdce4340bce6cac"

// plannedBuildEpic stores buildEpic accepted at base main and plans its tasks 1 and 2 with plannedTurn.
func plannedBuildEpic(t *testing.T) *Store {
	t.Helper()
	ctx := context.Background()
	s := openEpicStore(t)
	row := fullEpic(buildEpic)
	row.Base = "main"
	createEpic(t, s, row)
	if _, err := s.ClaimTurn(ctx, buildEpic, claimedAt()); err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	if err := s.CompleteTurn(ctx, buildEpic, 1, plannedTurn(), completedAt()); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
	return s
}

// buildTaskOf returns task seq of buildEpic as the store reads it.
func buildTaskOf(t *testing.T, s *Store, seq int64) EpicTask {
	t.Helper()
	row, err := s.Epic(context.Background(), buildEpic)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	for _, task := range row.Tasks {
		if task.Seq == seq {
			return task
		}
	}
	t.Fatalf("epic %s has no task %d", buildEpic, seq)
	return EpicTask{}
}

// addBuildRun stores a captured run id of buildEpic in phase, as RunTask does before a build names it. Its spec is
// the evidence plannedBuildEpic stored for task 1, the item the run is under.
func addBuildRun(t *testing.T, s *Store, id, phase string) {
	t.Helper()
	if err := s.AddEvidenceRun(context.Background(), EvidenceRun{
		ID: id, Epic: buildEpic, Item: "T1", Revision: "abc1234", Phase: phase, Source: "captured",
		Spec: plannedTurn().Tasks[0].Evidence, Created: runCreatedAt(),
	}); err != nil {
		t.Fatalf("AddEvidenceRun %s: %v", id, err)
	}
}

func TestPlannedTaskReadsBackWithZeroBuildNoReviewsAndNoRuns(t *testing.T) {
	t.Parallel()
	s := plannedBuildEpic(t)

	row, err := s.Epic(context.Background(), buildEpic)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	if len(row.Tasks) != 2 {
		t.Fatalf("tasks = %d, want 2", len(row.Tasks))
	}
	for _, task := range row.Tasks {
		if task.Build != (TaskBuild{}) {
			t.Errorf("task %d build = %+v, want the zero TaskBuild", task.Seq, task.Build)
		}
		if task.Reviews != nil || task.Red != nil || task.Green != nil {
			t.Errorf("task %d reviews = %v, red = %v, green = %v; want none, nil, nil",
				task.Seq, task.Reviews, task.Red, task.Green)
		}
	}
}

func TestSetTaskBuildStoresEveryFieldFromTheReadBuild(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := plannedBuildEpic(t)
	addBuildRun(t, s, "red-run", "red")
	addBuildRun(t, s, "green-run", "green")

	from := buildTaskOf(t, s, 1).Build
	to := TaskBuild{
		Stage: "implement", Base: buildBase, Attempt: 2, Rerun: 1, NoVerdict: 1, Held: "parked",
		Step: "step-implement-2", RedRun: "red-run", GreenRun: "green-run", Revision: "abc1234",
		Failure: "red: the conclusion holds at the base", Review: "reviewed",
		Refs: "refs/heads/main=abc1234", GatePos: 1, FindingPos: 2,
	}
	if err := s.SetTaskBuild(ctx, buildEpic, 1, from, to); err != nil {
		t.Fatalf("SetTaskBuild: %v", err)
	}

	task := buildTaskOf(t, s, 1)
	if task.Build != to {
		t.Errorf("build after SetTaskBuild = %+v, want %+v", task.Build, to)
	}
	if task.Red == nil || task.Red.ID != "red-run" || task.Green == nil || task.Green.ID != "green-run" {
		t.Errorf("red = %v, green = %v; want the red-run and green-run runs", task.Red, task.Green)
	}

	before := rowsOf(t, s.db.DB, "epic_tasks")
	err := s.SetTaskBuild(ctx, buildEpic, 1, from, TaskBuild{Stage: "green", Base: buildBase})
	if !errors.Is(err, ErrTaskMoved) {
		t.Fatalf("SetTaskBuild from the old build: err = %v, want ErrTaskMoved", err)
	}
	if got := rowsOf(t, s.db.DB, "epic_tasks"); !reflect.DeepEqual(got, before) {
		t.Errorf("epic_tasks after refused SetTaskBuild = %v, want %v", got, before)
	}
}

func TestSetTaskBuildOnDoneFailedOrUnknownTaskIsErrTaskMoved(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := plannedBuildEpic(t)
	if _, err := s.fixtureExec(ctx,
		`UPDATE epic_tasks SET status = 'done' WHERE epic = ? AND seq = 1`, buildEpic); err != nil {
		t.Fatalf("mark task 1 done: %v", err)
	}
	if _, err := s.fixtureExec(ctx,
		`UPDATE epic_tasks SET status = 'failed' WHERE epic = ? AND seq = 2`, buildEpic); err != nil {
		t.Fatalf("mark task 2 failed: %v", err)
	}

	before := rowsOf(t, s.db.DB, "epic_tasks")
	for _, seq := range []int64{1, 2, 9} {
		err := s.SetTaskBuild(ctx, buildEpic, seq, TaskBuild{}, TaskBuild{Stage: "red", Base: buildBase})
		if !errors.Is(err, ErrTaskMoved) {
			t.Errorf("SetTaskBuild on task %d: err = %v, want ErrTaskMoved", seq, err)
		}
	}
	if got := rowsOf(t, s.db.DB, "epic_tasks"); !reflect.DeepEqual(got, before) {
		t.Errorf("epic_tasks after refused SetTaskBuild calls = %v, want %v", got, before)
	}
}

// insertBuildTask stores task seq of epic as an open task, as a planner return does.
func insertBuildTask(t *testing.T, s *Store, epic string, seq int64) {
	t.Helper()
	if err := s.fixtureQueries().InsertEpicTask(context.Background(), sqlcgen.InsertEpicTaskParams{
		Epic: epic, Seq: seq, Turn: 1, Title: "Build it", Deliverable: "the build", Gate: "check",
	}); err != nil {
		t.Fatalf("insert task %d of %s: %v", seq, epic, err)
	}
}

func TestBuildEpicsListsEpicsWithBaseAndAnOpenTaskUnlessEnded(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	const (
		zeta       = "orrery-1a2b3c4/zeta"
		alpha      = "orrery-1a2b3c4/alpha"
		correcting = "orrery-1a2b3c4/correcting"
		noBase     = "orrery-1a2b3c4/no-base"
		onlyDone   = "orrery-1a2b3c4/only-done"
		noTasks    = "orrery-1a2b3c4/no-tasks"
	)
	endedPhases := []string{"met_at_base", "clean", "ended_with_gaps", "released", "done", "stopped"}
	stored := func(id, base, phase string) {
		row := fullEpic(id)
		row.Base, row.Phase = base, phase
		createEpic(t, s, row)
	}
	stored(zeta, "main", "accepted")
	stored(alpha, "main", "accepted")
	stored(correcting, "main", "correcting")
	stored(noBase, "", "accepted")
	stored(onlyDone, "main", "accepted")
	stored(noTasks, "main", "accepted")
	for _, id := range []string{zeta, alpha, correcting, noBase, onlyDone} {
		insertBuildTask(t, s, id, 1)
	}
	for _, phase := range endedPhases {
		id := "orrery-1a2b3c4/ended-" + phase
		stored(id, "main", phase)
		insertBuildTask(t, s, id, 1)
	}
	if _, err := s.fixtureExec(ctx,
		`UPDATE epic_tasks SET status = 'done' WHERE epic = ?`, onlyDone); err != nil {
		t.Fatalf("mark task of %s done: %v", onlyDone, err)
	}

	got, err := s.BuildEpics(ctx)
	if err != nil {
		t.Fatalf("BuildEpics: %v", err)
	}
	if want := []string{alpha, correcting, zeta}; !reflect.DeepEqual(got, want) {
		t.Errorf("BuildEpics = %v, want %v", got, want)
	}
}

func TestReviewsAndFindingsReadBackInAttemptAndPositionOrder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := plannedBuildEpic(t)

	// The rows go in out of order; the reads must sort reviews by attempt then position and findings by position.
	reviews := []struct {
		attempt, position   int64
		gate, step, verdict string
	}{
		{2, 1, "check", "review-2-1", "holds"},
		{1, 2, "race", "review-1-2", "fails"},
		{1, 1, "check", "review-1-1", "holds"},
	}
	for _, r := range reviews {
		if _, err := s.fixtureExec(ctx, `INSERT INTO epic_task_reviews
			(epic, task, generation, attempt, position, gate, step, verdict) VALUES (?, 1, 0, ?, ?, ?, ?, ?)`,
			buildEpic, r.attempt, r.position, r.gate, r.step, r.verdict); err != nil {
			t.Fatalf("insert review %d of attempt %d: %v", r.position, r.attempt, err)
		}
	}
	findingB := TaskFinding{
		Position: 2, ID: "F-B", Claim: "second claim", Citation: "b.go:20", Location: "b.go", Evidence: "log two",
		VerifyBy: "read b.go", Step: "verify-1-2-2", Result: "confirmed", Reason: "seen in b.go",
	}
	findingA := TaskFinding{
		Position: 1, ID: "F-A", Claim: "first claim", Citation: "a.go:10", Location: "a.go", Evidence: "log one",
		VerifyBy: "read a.go", Step: "verify-1-2-1", Result: "dropped", Reason: "not in a.go",
	}
	for _, f := range []TaskFinding{findingB, findingA} {
		if _, err := s.fixtureExec(ctx, `INSERT INTO epic_task_findings
			(epic, task, generation, attempt, review, position, id, claim, citation, location, evidence, verify_by, step,
			result, reason)
			VALUES (?, 1, 0, 1, 2, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			buildEpic, f.Position, f.ID, f.Claim, f.Citation, f.Location, f.Evidence, f.VerifyBy, f.Step, f.Result,
			f.Reason); err != nil {
			t.Fatalf("insert finding %s: %v", f.ID, err)
		}
	}

	want := []TaskReview{
		{Attempt: 1, Position: 1, Gate: "check", Step: "review-1-1", Verdict: "holds"},
		{
			Attempt: 1, Position: 2, Gate: "race", Step: "review-1-2", Verdict: "fails",
			Findings: []TaskFinding{findingA, findingB},
		},
		{Attempt: 2, Position: 1, Gate: "check", Step: "review-2-1", Verdict: "holds"},
	}
	if got := buildTaskOf(t, s, 1).Reviews; !reflect.DeepEqual(got, want) {
		t.Errorf("reviews of task 1 = %+v, want %+v", got, want)
	}
	if got := buildTaskOf(t, s, 2).Reviews; got != nil {
		t.Errorf("reviews of task 2 = %+v, want none", got)
	}
}

func TestTaskBuildNamingStoredRunsReadsThemAsRedAndGreen(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := plannedBuildEpic(t)
	addBuildRun(t, s, "red-run", "red")
	addBuildRun(t, s, "green-run", "green")

	to := TaskBuild{Stage: "green", Base: buildBase, RedRun: "red-run", GreenRun: "green-run"}
	if err := s.SetTaskBuild(ctx, buildEpic, 1, TaskBuild{}, to); err != nil {
		t.Fatalf("SetTaskBuild: %v", err)
	}

	task := buildTaskOf(t, s, 1)
	wantRed, err := s.EvidenceRun(ctx, "red-run")
	if err != nil {
		t.Fatalf("EvidenceRun red-run: %v", err)
	}
	wantGreen, err := s.EvidenceRun(ctx, "green-run")
	if err != nil {
		t.Fatalf("EvidenceRun green-run: %v", err)
	}
	if task.Red == nil || !reflect.DeepEqual(*task.Red, wantRed) {
		t.Errorf("red = %+v, want %+v", task.Red, wantRed)
	}
	if task.Green == nil || !reflect.DeepEqual(*task.Green, wantGreen) {
		t.Errorf("green = %+v, want %+v", task.Green, wantGreen)
	}
	if other := buildTaskOf(t, s, 2); other.Red != nil || other.Green != nil {
		t.Errorf("task 2 red = %v, green = %v; want nil, nil", other.Red, other.Green)
	}
}

func TestBuildColumnsRefuseValuesOutsideTheirSets(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := plannedBuildEpic(t)
	refused := []struct {
		name, stmt string
		args       []any
	}{
		{"task stage", `UPDATE epic_tasks SET stage = 'bogus' WHERE epic = ? AND seq = 1`, []any{buildEpic}},
		{"task held", `UPDATE epic_tasks SET held = 'paused' WHERE epic = ? AND seq = 1`, []any{buildEpic}},
		{"task review", `UPDATE epic_tasks SET review = 'maybe' WHERE epic = ? AND seq = 1`, []any{buildEpic}},
		{"epic branch", `UPDATE epics SET branch = 'bogus' WHERE id = ?`, []any{buildEpic}},
		{
			"review verdict",
			`INSERT INTO epic_task_reviews (epic, task, generation, attempt, position, gate, step, verdict)
			VALUES (?, 1, 0, 1, 1, 'check', 'review-1-1', 'maybe')`,
			[]any{buildEpic},
		},
		{
			"finding result",
			`INSERT INTO epic_task_findings
			(epic, task, generation, attempt, review, position, id, claim, citation, location, evidence, verify_by,
			result)
			VALUES (?, 1, 0, 1, 1, 1, 'F-1', 'claim', 'a.go:1', 'a.go', 'log', 'read a.go', 'pending')`,
			[]any{buildEpic},
		},
	}
	for _, r := range refused {
		_, err := s.fixtureExec(ctx, r.stmt, r.args...)
		if err == nil || !strings.Contains(err.Error(), "CHECK constraint failed") {
			t.Errorf("%s: err = %v, want the CHECK refusal", r.name, err)
		}
	}
}
