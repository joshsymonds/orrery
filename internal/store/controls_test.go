package store

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// requireRefusal fails unless err is a RefusalError whose text is want.
func requireRefusal(t *testing.T, err error, want string) {
	t.Helper()
	var refusal RefusalError
	if !errors.As(err, &refusal) {
		t.Fatalf("err = %v, want RefusalError %q", err, want)
	}
	if string(refusal) != want {
		t.Errorf("RefusalError = %q, want %q", refusal, want)
	}
}

// controlRows reads every row a control may change: the epic, its turns and its triggers.
func controlRows(t *testing.T, s *Store) [][][]any {
	t.Helper()
	return [][][]any{
		rowsOf(t, s.db, "epics"),
		rowsOf(t, s.db, "epic_turns"),
		rowsOf(t, s.db, "epic_triggers"),
	}
}

// storedTurnStop reads the stop flag of turn seq of epic id.
func storedTurnStop(t *testing.T, s *Store, id string, seq int64) int64 {
	t.Helper()
	var stop int64
	err := s.db.QueryRow(`SELECT stop FROM epic_turns WHERE epic = ? AND seq = ?`, id, seq).Scan(&stop)
	if err != nil {
		t.Fatalf("read stop of turn %d of %s: %v", seq, id, err)
	}
	return stop
}

// storedRetried reads the retried seq of claimEpic.
func storedRetried(t *testing.T, s *Store) int64 {
	t.Helper()
	var retried int64
	err := s.db.QueryRow(`SELECT retried FROM epics WHERE id = ?`, claimEpic).Scan(&retried)
	if err != nil {
		t.Fatalf("read retried of %s: %v", claimEpic, err)
	}
	return retried
}

func TestPauseEpicPausesAndRefusesASecondPause(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	addPendingTrigger(t, s)

	if err := s.PauseEpic(ctx, claimEpic); err != nil {
		t.Fatalf("PauseEpic: %v", err)
	}
	if paused, _ := epicFlags(t, s); paused != 1 {
		t.Errorf("paused after PauseEpic = %d, want 1", paused)
	}
	if _, err := s.ClaimTurn(ctx, claimEpic, claimedAt()); !errors.Is(err, ErrNotClaimable) {
		t.Errorf("ClaimTurn of a paused epic err = %v, want ErrNotClaimable", err)
	}

	before := controlRows(t, s)
	requireRefusal(t, s.PauseEpic(ctx, claimEpic), "epic "+claimEpic+" is already paused")
	if got := controlRows(t, s); !reflect.DeepEqual(got, before) {
		t.Errorf("rows after refused PauseEpic = %v, want %v", got, before)
	}
}

func TestResumeEpicClearsThePauseAndRefusesWhatIsNotPaused(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))

	before := controlRows(t, s)
	requireRefusal(t, s.ResumeEpic(ctx, claimEpic), "epic "+claimEpic+" is not paused")
	if got := controlRows(t, s); !reflect.DeepEqual(got, before) {
		t.Errorf("rows after refused ResumeEpic = %v, want %v", got, before)
	}

	if err := s.ParkEpic(ctx, claimEpic, "credit ran out"); err != nil {
		t.Fatalf("ParkEpic: %v", err)
	}
	before = controlRows(t, s)
	requireRefusal(t, s.ResumeEpic(ctx, claimEpic),
		"epic "+claimEpic+" is parked, not paused; retry ends a park")
	if got := controlRows(t, s); !reflect.DeepEqual(got, before) {
		t.Errorf("rows after refused ResumeEpic of a parked epic = %v, want %v", got, before)
	}

	if err := s.PauseEpic(ctx, claimEpic); err != nil {
		t.Fatalf("PauseEpic: %v", err)
	}
	if err := s.ResumeEpic(ctx, claimEpic); err != nil {
		t.Fatalf("ResumeEpic: %v", err)
	}
	if paused, parked := epicFlags(t, s); paused != 0 || parked != "credit ran out" {
		t.Errorf("paused, parked after ResumeEpic = %d, %q; want 0, %q", paused, parked, "credit ran out")
	}
}

func TestStopEpicPausesFlagsTheRunningTurnAndKeepsItsTriggers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	addPendingTrigger(t, s)

	before := controlRows(t, s)
	_, err := s.StopEpic(ctx, claimEpic)
	requireRefusal(t, err, "epic "+claimEpic+" has no running turn")
	if got := controlRows(t, s); !reflect.DeepEqual(got, before) {
		t.Errorf("rows after refused StopEpic = %v, want %v", got, before)
	}

	turn, err := s.ClaimTurn(ctx, claimEpic, claimedAt())
	if err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	if err = s.SetTurnStep(ctx, claimEpic, turn.Seq, "outline the tasks"); err != nil {
		t.Fatalf("SetTurnStep: %v", err)
	}
	step, err := s.StopEpic(ctx, claimEpic)
	if err != nil {
		t.Fatalf("StopEpic: %v", err)
	}
	if step != "outline the tasks" {
		t.Errorf("StopEpic step = %q, want %q", step, "outline the tasks")
	}
	if paused, _ := epicFlags(t, s); paused != 1 {
		t.Errorf("paused after StopEpic = %d, want 1", paused)
	}
	if stop := storedTurnStop(t, s, claimEpic, turn.Seq); stop != 1 {
		t.Errorf("stop of turn %d after StopEpic = %d, want 1", turn.Seq, stop)
	}
	wantTriggers := [][]any{
		{claimEpic, createdTriggerID, createdTriggerCause, "claimed", int64(1), int64(1700000000)},
		{claimEpic, "t2", "test trigger", "claimed", int64(1), int64(1700000000)},
	}
	if got := rowsOf(t, s.db, "epic_triggers"); !reflect.DeepEqual(got, wantTriggers) {
		t.Errorf("epic_triggers after StopEpic = %v, want %v", got, wantTriggers)
	}
}

func TestStopEpicReturnsAnEmptyStepWhileTheTurnHasNone(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	addPendingTrigger(t, s)
	if _, err := s.ClaimTurn(ctx, claimEpic, claimedAt()); err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}

	step, err := s.StopEpic(ctx, claimEpic)
	if err != nil {
		t.Fatalf("StopEpic: %v", err)
	}
	if step != "" {
		t.Errorf("StopEpic step = %q, want empty while the turn has none", step)
	}
}

func TestRetryEpicClearsThePark(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	addPendingTrigger(t, s)
	claimAndFail(t, s, "return had no outline")
	claimAndFail(t, s, "return had no outline")
	if err := s.ParkEpic(ctx, claimEpic, "credit ran out"); err != nil {
		t.Fatalf("ParkEpic: %v", err)
	}

	if err := s.RetryEpic(ctx, claimEpic); err != nil {
		t.Fatalf("RetryEpic: %v", err)
	}
	if _, parked := epicFlags(t, s); parked != "" {
		t.Errorf("parked after RetryEpic = %q, want empty", parked)
	}
	if got := storedRetried(t, s); got != 2 {
		t.Errorf("retried after RetryEpic = %d, want 2, the epic's highest turn seq", got)
	}
	if got := rowsOf(t, s.db, "epic_triggers"); !reflect.DeepEqual(got, triggerRows("pending", 0)) {
		t.Errorf("epic_triggers after RetryEpic = %v, want %v", got, triggerRows("pending", 0))
	}

	before := controlRows(t, s)
	requireRefusal(t, s.RetryEpic(ctx, claimEpic), "epic "+claimEpic+" is not parked")
	if got := controlRows(t, s); !reflect.DeepEqual(got, before) {
		t.Errorf("rows after refused RetryEpic = %v, want %v", got, before)
	}
}

func TestRetriedFailuresDoNotCountTowardAPark(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	addPendingTrigger(t, s)
	for range 3 {
		claimAndFail(t, s, "return had no outline")
	}
	if _, parked := epicFlags(t, s); !strings.HasPrefix(parked, parkPrefix) {
		t.Fatalf("parked after three failed turns = %q, want the park prefix", parked)
	}
	if err := s.RetryEpic(ctx, claimEpic); err != nil {
		t.Fatalf("RetryEpic: %v", err)
	}

	claimAndFail(t, s, "still no outline")
	if _, parked := epicFlags(t, s); parked != "" {
		t.Errorf("parked after one failed turn following retry = %q, want none", parked)
	}
	claimAndFail(t, s, "still no outline")
	claimAndFail(t, s, "still no outline")
	if _, parked := epicFlags(t, s); !strings.HasPrefix(parked, parkPrefix) {
		t.Errorf("parked after three failed turns following retry = %q, want the park prefix", parked)
	}
}

func TestTurnStoppedReadsTheStopFlag(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	addPendingTrigger(t, s)
	turn, err := s.ClaimTurn(ctx, claimEpic, claimedAt())
	if err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}

	stopped, err := s.TurnStopped(ctx, claimEpic, turn.Seq)
	if err != nil {
		t.Fatalf("TurnStopped before StopEpic: %v", err)
	}
	if stopped {
		t.Errorf("TurnStopped before StopEpic = true, want false")
	}
	if _, err = s.StopEpic(ctx, claimEpic); err != nil {
		t.Fatalf("StopEpic: %v", err)
	}
	stopped, err = s.TurnStopped(ctx, claimEpic, turn.Seq)
	if err != nil {
		t.Fatalf("TurnStopped after StopEpic: %v", err)
	}
	if !stopped {
		t.Errorf("TurnStopped after StopEpic = false, want true")
	}
	if _, err = s.TurnStopped(ctx, claimEpic, turn.Seq+1); !errors.Is(err, ErrNoTurn) {
		t.Errorf("TurnStopped of a turn that does not exist err = %v, want ErrNoTurn", err)
	}
}

func TestControlsRefuseAnUnknownEpicWithErrNoEpic(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	const missing = "orrery-1a2b3c4/no-such-epic"
	controls := []struct {
		name string
		run  func() error
	}{
		{"PauseEpic", func() error { return s.PauseEpic(ctx, missing) }},
		{"ResumeEpic", func() error { return s.ResumeEpic(ctx, missing) }},
		{"StopEpic", func() error { _, err := s.StopEpic(ctx, missing); return err }},
		{"RetryEpic", func() error { return s.RetryEpic(ctx, missing) }},
	}
	for _, control := range controls {
		if err := control.run(); !errors.Is(err, ErrNoEpic) {
			t.Errorf("%s of an unknown epic err = %v, want ErrNoEpic", control.name, err)
		}
	}
}
