package store

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

// ladderRungOneRed is the build of an open task in red at its base before its first attempt, which is on rung 1.
func ladderRungOneRed() TaskBuild {
	return TaskBuild{Stage: "red", Base: "base-rev"}
}

// TestTaskLadderClimbFromRedBeforeFirstAttempt checks that a climb from a build at attempt 0 in red is from rung 1: it
// writes rung 1 as an attempt ended failed and rung 2 moved by the unit, with no rung 0. A target of rung 1 is an error
// that writes nothing, and RungOf reads rung 1 for attempt 0 and the attempt for any other.
func TestTaskLadderClimbFromRedBeforeFirstAttempt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	from := ladderRungOneRed()
	seedClimbTask(t, s, from)

	if got := RungOf(TaskBuild{Attempt: 0}); got != 1 {
		t.Errorf("RungOf(attempt 0) = %d, want 1", got)
	}
	if got := RungOf(TaskBuild{Attempt: 4}); got != 4 {
		t.Errorf("RungOf(attempt 4) = %d, want 4", got)
	}

	refused := RungMove{MovedBy: MovedByUnit, Cause: climbCause, To: 1}
	if err := s.ClimbTask(ctx, climbEpic, 1, from, ClimbedBuild(from, 1, climbCause), refused, nil, nil,
		climbedAt()); err == nil {
		t.Fatal("ClimbTask to rung 1 from attempt 0 = nil, want an error")
	}
	if got := climbTaskOf(ctx, t, s, 1); got.Rungs != nil || !reflect.DeepEqual(got.Build, from) {
		t.Errorf("after a refused climb: rungs %+v build %+v, want none and the build unchanged", got.Rungs, got.Build)
	}

	move := RungMove{MovedBy: MovedByUnit, Cause: climbCause, Step: "step-1"}
	to := ClimbedBuild(from, 2, move.Cause)
	if err := s.ClimbTask(ctx, climbEpic, 1, from, to, move, nil, nil, climbedAt()); err != nil {
		t.Fatalf("ClimbTask: %v", err)
	}
	got := climbTaskOf(ctx, t, s, 1)
	wantRungs := []TaskRung{
		{Rung: 1, Kind: RungAttempt, Created: climbedAt(), Outcome: RungFailed, Reason: climbCause, Ended: climbedAt()},
		{Rung: 2, Kind: RungAttempt, MovedBy: MovedByUnit, Cause: climbCause, Step: "step-1", Created: climbedAt()},
	}
	if !reflect.DeepEqual(got.Rungs, wantRungs) {
		t.Errorf("rungs:\n got %+v\nwant %+v", got.Rungs, wantRungs)
	}
	if !reflect.DeepEqual(got.Build, to) {
		t.Errorf("build = %+v, want %+v", got.Build, to)
	}
}

// TestTaskLadderClimbStoresNeedsFilesAndCapsCause checks that a climb stores the files its move names as the task's
// needs files, and that a later climb with none stores none. ClimbCause cuts a cause to TaskDecisionMax bytes when it
// is decided, and to TurnReasonMax bytes otherwise.
func TestTaskLadderClimbStoresNeedsFilesAndCapsCause(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	from := climbFrom()
	seedClimbTask(t, s, from)

	files := []string{"internal/other/a.go"}
	move := RungMove{MovedBy: MovedByUnit, Cause: climbCause, NeedsFiles: files}
	climbed := ClimbedBuild(from, 2, move.Cause)
	if err := s.ClimbTask(ctx, climbEpic, 1, from, climbed, move, nil, nil, climbedAt()); err != nil {
		t.Fatalf("ClimbTask with needs files: %v", err)
	}
	if got := climbTaskOf(ctx, t, s, 1).NeedsFiles; !reflect.DeepEqual(got, files) {
		t.Errorf("NeedsFiles after the climb = %v, want %v", got, files)
	}

	next := RungMove{MovedBy: MovedByUnit, Cause: climbCause}
	again := ClimbedBuild(climbed, 3, next.Cause)
	if err := s.ClimbTask(ctx, climbEpic, 1, climbed, again, next, nil, nil, climbedAt()); err != nil {
		t.Fatalf("ClimbTask with no needs files: %v", err)
	}
	if got := climbTaskOf(ctx, t, s, 1).NeedsFiles; len(got) != 0 {
		t.Errorf("NeedsFiles after a climb with none = %v, want none", got)
	}

	long := strings.Repeat("c", TaskDecisionMax+100)
	if got := ClimbCause(long, true); len(got) != TaskDecisionMax {
		t.Errorf("ClimbCause decided = %d bytes, want %d", len(got), TaskDecisionMax)
	}
	if got := ClimbCause(long, false); len(got) != TurnReasonMax {
		t.Errorf("ClimbCause undecided = %d bytes, want %d", len(got), TurnReasonMax)
	}
}
