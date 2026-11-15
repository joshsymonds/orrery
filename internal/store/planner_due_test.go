package store

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// The ids of the epic due rows of claimEpic the tests expect, for dueResetA and dueResetB. Each is the lowercase
// sha256 of the namespace, kind, target and reset joined with NUL, computed outside the package.
const (
	dueEpicA = "9da4e0bc4ef153be5af1f874b756d0e847f1a97ef4d94ac4b1fa9fbeb81065b1"
	dueEpicB = "929bf115b5538f71684f6e9fd55186f58b826beb8d6ed5cdc52c101f54d1bd0b"
)

// claimTurnOf claims a turn of claimEpic and returns it, failing the test on error.
func claimTurnOf(t *testing.T, s *Store) EpicTurn {
	t.Helper()
	turn, err := s.ClaimTurn(context.Background(), claimEpic, claimedAt())
	if err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	return turn
}

// freeLimitOf frees turn of claimEpic as a usage limit that pauses the epic and resets at resets, or with no reset
// time when resets is zero.
func freeLimitOf(t *testing.T, s *Store, turn EpicTurn, resets time.Time) {
	t.Helper()
	free := TurnFree{Reason: "usage limit", Pause: true, Limit: true, Resets: resets}
	if err := s.FreeTurn(context.Background(), claimEpic, turn.Seq, free, failedAt()); err != nil {
		t.Fatalf("FreeTurn of turn %d: %v", turn.Seq, err)
	}
}

// limitTurnThenResume claims a turn of claimEpic, frees it as a usage limit that resets at resets, and resumes the
// epic.
func limitTurnThenResume(t *testing.T, s *Store, resets time.Time) {
	t.Helper()
	freeLimitOf(t, s, claimTurnOf(t, s), resets)
	if err := s.ResumeEpic(context.Background(), claimEpic); err != nil {
		t.Fatalf("ResumeEpic: %v", err)
	}
}

// dueRowCount returns how many due rows s stores in any state.
func dueRowCount(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	if err := s.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM due_resumes`).Scan(&n); err != nil {
		t.Fatalf("count due rows: %v", err)
	}
	return n
}

// pauseSourceOf returns the pause source of claimEpic as the store reads it.
func pauseSourceOf(t *testing.T, s *Store) string {
	t.Helper()
	head, err := sqlcgen.New(s.db).GetEpic(context.Background(), claimEpic)
	if err != nil {
		t.Fatalf("GetEpic %s: %v", claimEpic, err)
	}
	return head.PauseSource
}

// ownerPausedRunningTurnOf creates claimEpic with a planned task, adds a trigger, and makes an owner pause on the
// task while a second turn runs. It returns the running turn.
func ownerPausedRunningTurnOf(t *testing.T, s *Store) EpicTurn {
	t.Helper()
	ctx := context.Background()
	createEpic(t, s, fullEpic(claimEpic))
	first := claimTurnOf(t, s)
	if err := s.CompleteTurn(ctx, claimEpic, first.Seq, plannedTurn(), completedAt()); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
	addPendingTrigger(t, s)
	turn := claimTurnOf(t, s)
	if err := s.StopTask(ctx, claimEpic, 1, TaskBuild{}, TaskBuild{Held: "stopped"}); err != nil {
		t.Fatalf("StopTask: %v", err)
	}
	return turn
}

func TestLimitedTurnWithAKnownResetPausesWithSourceLimitAndStoresTheEpicDueRow(t *testing.T) {
	t.Parallel()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	freeLimitOf(t, s, claimTurnOf(t, s), time.Unix(dueResetA, 0))

	if paused, _ := epicFlags(t, s); paused != 1 {
		t.Errorf("paused after a limit with a known reset = %d, want 1", paused)
	}
	if got := pauseSourceOf(t, s); got != "limit" {
		t.Errorf("pause source after a limit with a known reset = %q, want limit", got)
	}
	if got := dueIDs(t, s); !reflect.DeepEqual(got, []string{dueEpicA}) {
		t.Fatalf("due rows after a limit with a known reset = %v, want [%s]", got, dueEpicA)
	}
	row, err := sqlcgen.New(s.db).GetDueResume(context.Background(), dueEpicA)
	if err != nil {
		t.Fatalf("GetDueResume: %v", err)
	}
	want := sqlcgen.DueResume{ID: dueEpicA, Kind: "epic", Target: claimEpic, Resets: dueResetA, State: "due"}
	if row != want {
		t.Errorf("GetDueResume = %+v, want %+v", row, want)
	}
}

func TestLimitedTurnWithoutAResetTimePausesWithSourceLimitAndStoresNoDueRow(t *testing.T) {
	t.Parallel()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	freeLimitOf(t, s, claimTurnOf(t, s), time.Time{})

	if paused, _ := epicFlags(t, s); paused != 1 {
		t.Errorf("paused after a limit with no reset = %d, want 1", paused)
	}
	if got := pauseSourceOf(t, s); got != "limit" {
		t.Errorf("pause source after a limit with no reset = %q, want limit", got)
	}
	if got := dueIDs(t, s); len(got) != 0 {
		t.Errorf("due rows after a limit with no reset = %v, want none", got)
	}
}

func TestStoppedTurnPausesWithSourceOwnerAndStoresNoDueRow(t *testing.T) {
	t.Parallel()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	turn := claimTurnOf(t, s)
	stopped := TurnFree{Reason: "stopped", Pause: true}
	if err := s.FreeTurn(context.Background(), claimEpic, turn.Seq, stopped, failedAt()); err != nil {
		t.Fatalf("FreeTurn: %v", err)
	}

	if got := pauseSourceOf(t, s); got != "owner" {
		t.Errorf("pause source after a stopped turn = %q, want owner", got)
	}
	if got := dueIDs(t, s); len(got) != 0 {
		t.Errorf("due rows after a stopped turn = %v, want none", got)
	}
}

func TestStopTaskPausesWithSourceOwner(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	turn := claimTurnOf(t, s)
	if err := s.CompleteTurn(ctx, claimEpic, turn.Seq, plannedTurn(), completedAt()); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}

	if err := s.StopTask(ctx, claimEpic, 1, TaskBuild{}, TaskBuild{Held: "stopped"}); err != nil {
		t.Fatalf("StopTask: %v", err)
	}

	if paused, _ := epicFlags(t, s); paused != 1 {
		t.Errorf("paused after StopTask = %d, want 1", paused)
	}
	if got := pauseSourceOf(t, s); got != "owner" {
		t.Errorf("pause source after StopTask = %q, want owner", got)
	}
}

func TestOwnerPauseMadeWhileTurnRunsWinsOverALimitWithAKnownReset(t *testing.T) {
	t.Parallel()
	s := openEpicStore(t)
	turn := ownerPausedRunningTurnOf(t, s)
	freeLimitOf(t, s, turn, time.Unix(dueResetA, 0))

	if paused, _ := epicFlags(t, s); paused != 1 {
		t.Errorf("paused after an owner pause and a limit = %d, want 1", paused)
	}
	if got := pauseSourceOf(t, s); got != "owner" {
		t.Errorf("pause source after an owner pause and a limit = %q, want owner", got)
	}
	if got := dueIDs(t, s); len(got) != 0 {
		t.Errorf("due rows after an owner pause and a limit = %v, want none", got)
	}
}

func TestOwnerPauseMadeWhileTurnRunsKeepsItsSourceOverALimitWithoutAResetTime(t *testing.T) {
	t.Parallel()
	s := openEpicStore(t)
	turn := ownerPausedRunningTurnOf(t, s)
	freeLimitOf(t, s, turn, time.Time{})

	if got := pauseSourceOf(t, s); got != "owner" {
		t.Errorf("pause source after an owner pause and a limit with no reset = %q, want owner", got)
	}
	if got := dueIDs(t, s); len(got) != 0 {
		t.Errorf("due rows after an owner pause and a limit with no reset = %v, want none", got)
	}
}

func TestEmptySourcePauseMadeWhileTurnRunsWinsOverALimitWithAKnownReset(t *testing.T) {
	t.Parallel()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	turn := claimTurnOf(t, s)
	// A pause stored before pause sources were recorded has an empty source.
	if err := sqlcgen.New(s.db).PauseEpic(context.Background(), claimEpic); err != nil {
		t.Fatalf("PauseEpic query: %v", err)
	}
	freeLimitOf(t, s, turn, time.Unix(dueResetA, 0))

	if paused, _ := epicFlags(t, s); paused != 1 {
		t.Errorf("paused after an empty-source pause and a limit = %d, want 1", paused)
	}
	if got := pauseSourceOf(t, s); got != "" {
		t.Errorf("pause source after an empty-source pause and a limit = %q, want empty", got)
	}
	if got := dueIDs(t, s); len(got) != 0 {
		t.Errorf("due rows after an empty-source pause and a limit = %v, want none", got)
	}
}

func TestResumedEpicLimitedAtAnEarlierOrEqualResetAddsNoSecondDueRow(t *testing.T) {
	t.Parallel()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	limitTurnThenResume(t, s, time.Unix(dueResetA, 0))

	limitTurnThenResume(t, s, time.Unix(dueResetA-3600, 0))
	if got := dueRowCount(t, s); got != 1 {
		t.Fatalf("due rows stored after an earlier reset = %d, want only the first", got)
	}

	limitTurnThenResume(t, s, time.Unix(dueResetA, 0))
	if got := dueRowCount(t, s); got != 1 {
		t.Fatalf("due rows stored after an equal reset = %d, want only the first", got)
	}
	if state := dueStateOf(t, s, dueEpicA); state != "done" {
		t.Fatalf("state of the first row = %q, want done by its resume", state)
	}
}

func TestResumedEpicLimitedAtALaterResetAddsItsDueRow(t *testing.T) {
	t.Parallel()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	limitTurnThenResume(t, s, time.Unix(dueResetA, 0))

	limitTurnThenResume(t, s, time.Unix(dueResetB, 0))
	if got := dueRowCount(t, s); got != 2 {
		t.Fatalf("due rows stored after a later reset = %d, want two", got)
	}
	if state := dueStateOf(t, s, dueEpicB); state != "done" {
		t.Fatalf("state of the later row = %q, want done by its resume", state)
	}
}
