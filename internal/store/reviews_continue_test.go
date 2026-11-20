package store

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
	"time"
)

// TestReviewsContinueAddTaskReviewStoresUndeterminedWithItsReason: a review gate that gave no verdict is stored as
// undetermined with the reason it ended so, and the build moves on to the next gate in the same write.
func TestReviewsContinueAddTaskReviewStoresUndeterminedWithItsReason(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := plannedBuildEpic(t)
	review := TaskBuild{Stage: "review", Base: buildBase, Attempt: 1}
	if err := s.SetTaskBuild(ctx, buildEpic, 1, TaskBuild{}, review); err != nil {
		t.Fatalf("SetTaskBuild: %v", err)
	}
	next := TaskBuild{Stage: "review", Base: buildBase, Attempt: 1, GatePos: 1}
	stored := TaskReview{
		Attempt: 1, Position: 0, Gate: "check", Step: "review-1-0", Verdict: "undetermined",
		Reason: "gate_failed: limit ran out",
	}
	if err := s.AddTaskReview(ctx, buildEpic, 1, review, next, stored); err != nil {
		t.Fatalf("AddTaskReview of an undetermined review: %v", err)
	}
	task := buildTaskOf(t, s, 1)
	if task.Build != next {
		t.Errorf("build after AddTaskReview = %+v, want %+v", task.Build, next)
	}
	if want := []TaskReview{stored}; !reflect.DeepEqual(task.Reviews, want) {
		t.Errorf("reviews after AddTaskReview = %+v, want %+v", task.Reviews, want)
	}
}

// TestReviewsContinueSetTaskBuildNotesStoresTheNotesAndTells: a write stores the task's whole list of notes, the build
// it moves to and the tells it adds, in one write, and a stale build writes none of them.
func TestReviewsContinueSetTaskBuildNotesStoresTheNotesAndTells(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := plannedBuildEpic(t)
	from := buildTaskOf(t, s, 1).Build
	to := TaskBuild{Stage: "implement", Base: buildBase, Attempt: 1}
	notes := []string{"T1 red: the conclusion holds at the base (exit 0)"}
	at := time.Unix(1700000500, 0)
	if err := s.SetTaskBuildNotes(ctx, buildEpic, 1, from, to, notes, notes, at); err != nil {
		t.Fatalf("SetTaskBuildNotes: %v", err)
	}
	task := buildTaskOf(t, s, 1)
	if task.Build != to {
		t.Errorf("build after SetTaskBuildNotes = %+v, want %+v", task.Build, to)
	}
	if !reflect.DeepEqual(task.Notes, notes) {
		t.Errorf("notes after SetTaskBuildNotes = %q, want %q", task.Notes, notes)
	}
	if got := queryRows(
		t,
		s.db.DB,
		`SELECT text FROM epic_tells WHERE epic = ? ORDER BY seq`,
		buildEpic,
	); !reflect.DeepEqual(
		got,
		[][]any{{notes[0]}},
	) {
		t.Errorf("tells after SetTaskBuildNotes = %v, want the note told once", got)
	}

	stale := TaskBuild{Stage: "red", Base: buildBase}
	err := s.SetTaskBuildNotes(ctx, buildEpic, 1, stale, stale, []string{"stale"}, []string{"stale"}, at)
	if !errors.Is(err, ErrTaskMoved) {
		t.Fatalf("SetTaskBuildNotes from a stale build: err = %v, want ErrTaskMoved", err)
	}
	if got := buildTaskOf(t, s, 1).Notes; !reflect.DeepEqual(got, notes) {
		t.Errorf("notes after a stale SetTaskBuildNotes = %q, want %q", got, notes)
	}
}

// TestReviewsContinueMigration00078StepsDownAndUp: 00078 keeps the reviews that have a holds or fails verdict through
// its Down, drops the undetermined ones, which the earlier schema refuses, and its Up accepts undetermined reviews
// again with their reasons.
func TestReviewsContinueMigration00078StepsDownAndUp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, provider := storeAtVersion(t, 78)
	for _, row := range []struct {
		position              int64
		gate, verdict, reason string
	}{
		{0, "check", "fails", ""},
		{1, "lint", "undetermined", "gate_failed: limit ran out"},
	} {
		if _, err := s.db.DB.ExecContext(ctx,
			`INSERT INTO epic_task_reviews (epic, task, attempt, position, gate, step, verdict, reason)
			VALUES (?, 1, 1, ?, ?, ?, ?, ?)`,
			buildEpic, row.position, row.gate, "review-step", row.verdict, row.reason,
		); err != nil {
			t.Fatalf("insert review %d of 00078: %v", row.position, err)
		}
	}

	if _, err := provider.Down(ctx); err != nil {
		t.Fatalf("Down of 00078: %v", err)
	}
	if got := queryRows(
		t,
		s.db.DB,
		`SELECT position, verdict FROM epic_task_reviews ORDER BY position`,
	); !reflect.DeepEqual(
		got,
		[][]any{{int64(0), "fails"}},
	) {
		t.Errorf("reviews after Down = %v, want the fails review alone", got)
	}

	if _, err := provider.UpByOne(ctx); err != nil {
		t.Fatalf("Up of 00078: %v", err)
	}
	if _, err := s.db.DB.ExecContext(ctx,
		`INSERT INTO epic_task_reviews (epic, task, attempt, position, gate, step, verdict, reason)
		VALUES (?, 1, 1, 2, 'security', 'review-step', 'undetermined', 'gate_failed: limit ran out')`, buildEpic,
	); err != nil {
		t.Fatalf("insert an undetermined review after Up: %v", err)
	}
	if got := queryRows(
		t,
		s.db.DB,
		`SELECT verdict, reason FROM epic_task_reviews WHERE position = 2`,
	); !reflect.DeepEqual(
		got,
		[][]any{{"undetermined", "gate_failed: limit ran out"}},
	) {
		t.Errorf("undetermined review after Up = %v, want its reason", got)
	}
}

// queryRows returns the rows query gives on db, each as its column values in column order.
func queryRows(t *testing.T, db *sql.DB, query string, args ...any) [][]any {
	t.Helper()
	rows, err := db.Query(query, args...)
	if err != nil {
		t.Fatalf("query %s: %v", query, err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatalf("columns of %s: %v", query, err)
	}
	var out [][]any
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			t.Fatalf("scan %s: %v", query, err)
		}
		out = append(out, values)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate %s: %v", query, err)
	}
	return out
}
