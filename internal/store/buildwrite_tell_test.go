package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

// moveTellsOf returns the texts of the epic_tells rows of claimEpic in seq order.
func moveTellsOf(ctx context.Context, t *testing.T, s *Store) []string {
	t.Helper()
	rows, err := s.db.QueryContext(ctx, `SELECT text FROM epic_tells WHERE epic = ? ORDER BY seq`, claimEpic)
	if err != nil {
		t.Fatalf("query tells of %s: %v", claimEpic, err)
	}
	defer rows.Close()
	var texts []string
	for rows.Next() {
		var text string
		if err := rows.Scan(&text); err != nil {
			t.Fatalf("scan tell of %s: %v", claimEpic, err)
		}
		texts = append(texts, text)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read tells of %s: %v", claimEpic, err)
	}
	return texts
}

// storedBuildAndTellsOf returns claimEpic as the store reads it and the texts of its tells, so a test can compare the
// store before and after a write.
func storedBuildAndTellsOf(ctx context.Context, t *testing.T, s *Store) (EpicRow, []string) {
	t.Helper()
	row, err := s.Epic(ctx, claimEpic)
	if err != nil {
		t.Fatalf("Epic %s: %v", claimEpic, err)
	}
	return row, moveTellsOf(ctx, t, s)
}

// openPlannedClaimStore returns a store whose claimEpic has its task 1 planned and open with a zero build.
func openPlannedClaimStore(t *testing.T) *Store {
	t.Helper()
	ctx := context.Background()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	turn := claimTurnOf(t, s)
	if err := s.CompleteTurn(ctx, claimEpic, turn.Seq, plannedTurn(), completedAt()); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
	return s
}

func TestSetTaskBuildTellingStoresTheMoveAndEveryTellInOneWrite(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openPlannedClaimStore(t)
	to := TaskBuild{Stage: "implement", Base: "base-rev", Attempt: 1, Step: "step-1"}
	tells := []string{
		"T1 implementer moved refs/heads/feature (a to b)",
		"T1 implementer moved refs/heads/topic (c to d)",
	}
	at := time.Unix(1700000900, 0)

	if err := s.SetTaskBuildTelling(ctx, claimEpic, 1, TaskBuild{}, to, tells, at); err != nil {
		t.Fatalf("SetTaskBuildTelling: %v", err)
	}

	row, err := s.Epic(ctx, claimEpic)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	if got := row.Tasks[0].Build; got != to {
		t.Errorf("task build = %+v, want %+v", got, to)
	}
	if got := moveTellsOf(ctx, t, s); !reflect.DeepEqual(got, tells) {
		t.Errorf("tells = %q, want %q", got, tells)
	}
}

func TestSetTaskBuildTellingOnAStaleBuildStoresNeitherAndIsErrTaskMoved(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openPlannedClaimStore(t)
	before, beforeTells := storedBuildAndTellsOf(ctx, t, s)

	// The stored build is zero, so an open build names a different one.
	stale := TaskBuild{Stage: "implement", Base: "base-rev", Attempt: 1, Step: "step-1"}
	to := TaskBuild{Stage: "green", Base: "base-rev"}
	tells := []string{"T1 implementer moved refs/heads/feature (a to b)"}
	err := s.SetTaskBuildTelling(ctx, claimEpic, 1, stale, to, tells, time.Unix(1700000900, 0))
	if !errors.Is(err, ErrTaskMoved) {
		t.Fatalf("SetTaskBuildTelling with a stale build err = %v, want ErrTaskMoved", err)
	}

	after, afterTells := storedBuildAndTellsOf(ctx, t, s)
	if !reflect.DeepEqual(before, after) {
		t.Errorf("SetTaskBuildTelling with a stale build changed the epic")
	}
	if !reflect.DeepEqual(afterTells, beforeTells) {
		t.Errorf("tells after a stale SetTaskBuildTelling = %q, want %q", afterTells, beforeTells)
	}
}

func TestStopTaskClosesTheEpicsDueRowsAndPausesAsOwner(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	first := claimTurnOf(t, s)
	if err := s.CompleteTurn(ctx, claimEpic, first.Seq, plannedTurn(), completedAt()); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
	addPendingTrigger(t, s)
	turn := claimTurnOf(t, s)
	freeLimitOf(t, s, turn, time.Unix(dueResetA, 0))
	if got := dueStateOf(t, s, dueEpicA); got != "due" {
		t.Fatalf("state of the due row before StopTask = %q, want due", got)
	}

	if err := s.StopTask(ctx, claimEpic, 1, TaskBuild{}, TaskBuild{Held: "stopped"}); err != nil {
		t.Fatalf("StopTask: %v", err)
	}

	if got := dueStateOf(t, s, dueEpicA); got != "done" {
		t.Errorf("state of the due row after StopTask = %q, want done", got)
	}
	if paused, _ := epicFlags(t, s); paused != 1 {
		t.Errorf("paused after StopTask = %d, want 1", paused)
	}
	if got := pauseSourceOf(t, s); got != "owner" {
		t.Errorf("pause source after StopTask = %q, want owner", got)
	}
}

func TestParkTaskWithTellsStoresTheParkAndEveryTellInOneWrite(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openPlannedClaimStore(t)
	to := TaskBuild{Stage: "implement", Base: "base-rev", Attempt: 1, Step: "step-1", Held: "parked"}
	const reason = "T1 implementer moved refs/heads/main (a to b)"
	tells := []string{"T1 implementer moved refs/heads/feature (c to d)"}
	at := time.Unix(1700000900, 0)

	if err := s.ParkTask(ctx, claimEpic, 1, TaskBuild{}, to, ParkClassRefMove, reason, at, tells...); err != nil {
		t.Fatalf("ParkTask: %v", err)
	}

	row, err := s.Epic(ctx, claimEpic)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	if row.Parked != reason {
		t.Errorf("epic park = %q, want %q", row.Parked, reason)
	}
	if got := row.Tasks[0].Build; got != to {
		t.Errorf("task build = %+v, want %+v", got, to)
	}
	want := append([]string{reason}, tells...)
	if got := moveTellsOf(ctx, t, s); !reflect.DeepEqual(got, want) {
		t.Errorf("tells = %q, want %q", got, want)
	}
}

func TestParkTaskWithTellsOnAStaleBuildStoresNeitherAndIsErrTaskMoved(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openPlannedClaimStore(t)
	before, beforeTells := storedBuildAndTellsOf(ctx, t, s)

	// The stored build is zero, so an open build names a different one.
	stale := TaskBuild{Stage: "implement", Base: "base-rev", Attempt: 1, Step: "step-1"}
	to := TaskBuild{Stage: "implement", Base: "base-rev", Attempt: 1, Step: "step-1", Held: "parked"}
	tells := []string{"T1 implementer moved refs/heads/feature (c to d)"}
	err := s.ParkTask(
		ctx,
		claimEpic,
		1,
		stale,
		to,
		ParkClassRefMove,
		"the branch exists",
		time.Unix(1700000900, 0),
		tells...)
	if !errors.Is(err, ErrTaskMoved) {
		t.Fatalf("ParkTask with a stale build err = %v, want ErrTaskMoved", err)
	}

	after, afterTells := storedBuildAndTellsOf(ctx, t, s)
	if !reflect.DeepEqual(before, after) {
		t.Errorf("ParkTask with a stale build changed the epic")
	}
	if !reflect.DeepEqual(afterTells, beforeTells) {
		t.Errorf("tells after a stale ParkTask = %q, want %q", afterTells, beforeTells)
	}
}
