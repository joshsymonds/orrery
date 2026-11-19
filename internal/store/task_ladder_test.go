package store

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// insertLadder stores each rung and each outcome through the sqlcgen queries, in the order given, and fails the test
// when a query writes no row.
func insertLadder(t *testing.T, s *Store, rungs []sqlcgen.InsertEpicTaskRungParams,
	outcomes []sqlcgen.InsertEpicTaskRungOutcomeParams,
) {
	t.Helper()
	ctx := context.Background()
	queries := sqlcgen.New(s.db)
	for _, rung := range rungs {
		n, err := queries.InsertEpicTaskRung(ctx, rung)
		if err != nil {
			t.Fatalf("insert rung %d of task %d: %v", rung.Rung, rung.Task, err)
		}
		if n != 1 {
			t.Fatalf("insert rung %d of task %d wrote %d rows, want 1", rung.Rung, rung.Task, n)
		}
	}
	for _, outcome := range outcomes {
		n, err := queries.InsertEpicTaskRungOutcome(ctx, outcome)
		if err != nil {
			t.Fatalf("insert outcome of rung %d of task %d: %v", outcome.Rung, outcome.Task, err)
		}
		if n != 1 {
			t.Fatalf("insert outcome of rung %d of task %d wrote %d rows, want 1", outcome.Rung, outcome.Task, n)
		}
	}
}

// TestTaskLadderRungsRead checks that the epic read gives each task its ladder rows in rung order, with their outcomes.
// An open rung reads with no outcome, a task with no rows reads with nil Rungs, and a second outcome for a rung writes
// no row and leaves the first in place.
func TestTaskLadderRungsRead(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	const id = "orrery-1a2b3c4/task-ladder"
	createEpic(t, s, fullEpic(id))
	if _, err := s.db.ExecContext(ctx, `INSERT INTO epic_tasks (epic, seq, turn, title, deliverable, status)
		VALUES (?, 1, 1, 'Climbs', 'the first change', 'open'), (?, 2, 1, 'Never climbs', 'the second change', 'open')`,
		id, id); err != nil {
		t.Fatalf("insert tasks: %v", err)
	}
	// The rung written last is inserted first, so the read is the one that orders the rows.
	insertLadder(t, s, []sqlcgen.InsertEpicTaskRungParams{
		{Epic: id, Task: 1, Rung: 3, Kind: RungRescue, MovedBy: MovedByFinding, Cause: "the claim failed",
			Gate: "review", Assertion: "the claim", Step: "step-6", Created: 1700000300},
		{Epic: id, Task: 1, Rung: 1, Kind: RungAttempt, Created: 1700000100},
		{Epic: id, Task: 1, Rung: 2, Kind: RungAttempt, MovedBy: MovedByUnit, Cause: "the build exited 1",
			Step: "step-4", Created: 1700000200},
	}, []sqlcgen.InsertEpicTaskRungOutcomeParams{
		{Epic: id, Task: 1, Rung: 2, Outcome: RungFailed, Reason: "the build failed again", Ended: 1700000250},
		{Epic: id, Task: 1, Rung: 1, Outcome: RungFailed, Reason: "the first build failed", Ended: 1700000150},
	})
	wantRungs := []TaskRung{
		{
			Rung: 1, Kind: RungAttempt, Created: time.Unix(1700000100, 0),
			Outcome: RungFailed, Reason: "the first build failed", Ended: time.Unix(1700000150, 0),
		},
		{
			Rung: 2, Kind: RungAttempt, MovedBy: MovedByUnit, Cause: "the build exited 1", Step: "step-4",
			Created: time.Unix(1700000200, 0), Outcome: RungFailed, Reason: "the build failed again",
			Ended: time.Unix(1700000250, 0),
		},
		{
			Rung: 3, Kind: RungRescue, MovedBy: MovedByFinding, Cause: "the claim failed", Gate: "review",
			Assertion: "the claim", Step: "step-6", Created: time.Unix(1700000300, 0),
		},
	}

	got, err := s.Epic(ctx, id)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	if len(got.Tasks) != 2 {
		t.Fatalf("Epic has %d tasks, want 2", len(got.Tasks))
	}
	if !reflect.DeepEqual(got.Tasks[0].Rungs, wantRungs) {
		t.Errorf("task 1 rungs:\n got %+v\nwant %+v", got.Tasks[0].Rungs, wantRungs)
	}
	if got.Tasks[1].Rungs != nil {
		t.Errorf("task 2 rungs = %+v, want nil for a task that never climbed", got.Tasks[1].Rungs)
	}

	// The open rung 3 has no outcome, and the write of a second outcome for rung 1 writes no row and leaves the first.
	n, err := sqlcgen.New(s.db).InsertEpicTaskRungOutcome(ctx, sqlcgen.InsertEpicTaskRungOutcomeParams{
		Epic: id, Task: 1, Rung: 1, Outcome: RungAccepted, Reason: "the second outcome", Ended: 1700000900,
	})
	if err != nil {
		t.Fatalf("insert second outcome of rung 1: %v", err)
	}
	if n != 0 {
		t.Errorf("second outcome of rung 1 wrote %d rows, want 0", n)
	}
	got, err = s.Epic(ctx, id)
	if err != nil {
		t.Fatalf("Epic after second outcome: %v", err)
	}
	if !reflect.DeepEqual(got.Tasks[0].Rungs, wantRungs) {
		t.Errorf("task 1 rungs after second outcome:\n got %+v\nwant %+v", got.Tasks[0].Rungs, wantRungs)
	}
}
