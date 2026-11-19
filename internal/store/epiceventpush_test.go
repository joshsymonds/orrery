package store

import (
	"context"
	"errors"
	"slices"
	"testing"
)

// hookRecord is what a counting caller event hook saw: how many times it ran, and on each run the value its fresh read
// returned.
type hookRecord[T any] struct {
	calls int
	seen  []T
}

// recordCallerEvents sets a hook on s that counts its runs and records what read returns on each run. read reads the
// store freshly, so each recorded value is what the hook saw of the write that ran it.
func recordCallerEvents[T any](t *testing.T, s *Store, read func() (T, error)) *hookRecord[T] {
	t.Helper()
	record := &hookRecord[T]{}
	s.OnCallerEvent(func() {
		record.calls++
		value, err := read()
		if err != nil {
			t.Errorf("fresh read in the caller event hook: %v", err)
		}
		record.seen = append(record.seen, value)
	})
	return record
}

// openQuestionCount reads how many questions of claimEpic are open, freshly.
func openQuestionCount(ctx context.Context, s *Store) (int, error) {
	questions, err := s.OpenQuestions(ctx, claimEpic)
	return len(questions), err
}

// epicPhase reads the phase of claimEpic, freshly.
func epicPhase(ctx context.Context, s *Store) (string, error) {
	row, err := s.Epic(ctx, claimEpic)
	return row.Phase, err
}

// claimPlannerTurn stores claimEpic with a second pending trigger and claims its turn, so CompleteTurn can store a
// return.
func claimPlannerTurn(t *testing.T, s *Store) {
	t.Helper()
	createEpic(t, s, fullEpic(claimEpic))
	addPendingTrigger(t, s)
	if _, err := s.ClaimTurn(context.Background(), claimEpic, claimedAt()); err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
}

func TestEpicEventPushStorePlannerQuestionCallsHookOnceAfterCommit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	claimPlannerTurn(t, s)
	record := recordCallerEvents(t, s, func() (int, error) { return openQuestionCount(ctx, s) })

	if err := s.CompleteTurn(ctx, claimEpic, 1, plannedTurn(), completedAt()); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
	if record.calls != 1 || !slices.Equal(record.seen, []int{1}) {
		t.Errorf("hook calls = %d, fresh reads = %v; want one call that reads one open question",
			record.calls, record.seen)
	}
}

func TestEpicEventPushStoreParkQuestionCallsHookOnceAfterCommit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	record := recordCallerEvents(t, s, func() (int, error) { return openQuestionCount(ctx, s) })

	if err := s.ParkEpic(ctx, claimEpic, ParkClassBranch, "credit ran out", completedAt()); err != nil {
		t.Fatalf("ParkEpic: %v", err)
	}
	if record.calls != 1 || !slices.Equal(record.seen, []int{1}) {
		t.Errorf("hook calls = %d, fresh reads = %v; want one call that reads one open question",
			record.calls, record.seen)
	}
}

func TestEpicEventPushStorePhaseSetCallsHookOnceAfterCommit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	record := recordCallerEvents(t, s, func() (string, error) { return epicPhase(ctx, s) })

	if err := s.EndEpic(ctx, claimEpic, phaseBuilt, "built", nil, completedAt()); err != nil {
		t.Fatalf("EndEpic: %v", err)
	}
	if record.calls != 1 || !slices.Equal(record.seen, []string{phaseBuilt}) {
		t.Errorf("hook calls = %d, fresh reads = %v; want one call that reads phase %s",
			record.calls, record.seen, phaseBuilt)
	}
}

func TestEpicEventPushStoreReleaseStopCallsHookOnceAfterCommit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	startRelease(t, s, []EpicReleaseStep{{Step: "push tag", Target: "origin", Undo: "delete tag"}})
	record := recordCallerEvents(t, s, func() (string, error) { return epicPhase(ctx, s) })

	if err := s.StopRelease(ctx, claimEpic, "release", 1, "publish rejected", claimedAt()); err != nil {
		t.Fatalf("StopRelease: %v", err)
	}
	if record.calls != 1 || !slices.Equal(record.seen, []string{PhaseStopped}) {
		t.Errorf("hook calls = %d, fresh reads = %v; want one call that reads phase %s",
			record.calls, record.seen, PhaseStopped)
	}
}

func TestEpicEventPushStoreRolledBackWriteCallsNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	claimPlannerTurn(t, s)
	record := recordCallerEvents(t, s, func() (int, error) { return openQuestionCount(ctx, s) })

	// The first question is inserted, which marks the write; the second replaces Q99, which the epic does not hold, so
	// the write fails and rolls back.
	plan := plannedTurn()
	plan.Questions = append(plan.Questions, EpicQuestionInput{
		Label: "second", Text: "Second?", Choices: plan.Questions[0].Choices,
		Recommended: "keep", Why: "The second question replaces Q99.", Confidence: "low", Replaces: "Q99",
	})
	if err := s.CompleteTurn(ctx, claimEpic, 1, plan, completedAt()); !errors.Is(err, ErrBadQuestion) {
		t.Fatalf("CompleteTurn err = %v, want ErrBadQuestion", err)
	}
	if record.calls != 0 {
		t.Errorf("hook calls = %d after a rolled back write, want 0", record.calls)
	}
	if n, err := openQuestionCount(ctx, s); err != nil || n != 0 {
		t.Errorf("open questions after the rolled back write = %d (err %v), want 0", n, err)
	}

	// The turn is still running, so its valid return now commits and calls the hook once. That call shows the hook is
	// live, so the calls counted above are not the calls of a hook that never runs.
	if err := s.CompleteTurn(ctx, claimEpic, 1, plannedTurn(), completedAt()); err != nil {
		t.Fatalf("CompleteTurn after the rolled back write: %v", err)
	}
	if record.calls != 1 || !slices.Equal(record.seen, []int{1}) {
		t.Errorf("hook calls = %d, fresh reads = %v; want one call after the commit that reads one open question",
			record.calls, record.seen)
	}
}

func TestEpicEventPushStoreNilHookStopsCalls(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	record := recordCallerEvents(t, s, func() (string, error) { return epicPhase(ctx, s) })

	if err := s.EndEpic(ctx, claimEpic, phaseBuilt, "built", nil, completedAt()); err != nil {
		t.Fatalf("EndEpic with the hook: %v", err)
	}
	s.OnCallerEvent(nil)
	if err := s.EndEpic(ctx, claimEpic, PhaseStopped, "stopped", nil, completedAt()); err != nil {
		t.Fatalf("EndEpic after the hook is cleared: %v", err)
	}
	if record.calls != 1 || !slices.Equal(record.seen, []string{phaseBuilt}) {
		t.Errorf("hook calls = %d, fresh reads = %v; want the one call before the nil hook, reading %s",
			record.calls, record.seen, phaseBuilt)
	}
	if phase, err := epicPhase(ctx, s); err != nil || phase != PhaseStopped {
		t.Errorf("phase after the cleared hook = %q (err %v), want %s", phase, err, PhaseStopped)
	}
}
