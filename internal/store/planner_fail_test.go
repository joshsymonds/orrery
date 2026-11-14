package store

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// failedAt is the second the failure and free tests stamp on an ended turn.
func failedAt() time.Time {
	return time.Unix(1700000700, 0)
}

// parkPrefix is the text a third failed turn in a row stores before its own reason.
const parkPrefix = "3 planner turns failed in a row: "

// triggerRows returns the epic_triggers rows of claimEpic: the created trigger and t2, each in state and turn.
func triggerRows(state string, turn int64) [][]any {
	return [][]any{
		{claimEpic, createdTriggerID, createdTriggerCause, state, turn, int64(1700000000)},
		{claimEpic, "t2", "test trigger", state, turn, int64(1700000000)},
	}
}

// claimAndFail claims a turn of claimEpic and fails it with reason.
func claimAndFail(t *testing.T, s *Store, reason string) {
	t.Helper()
	turn, err := s.ClaimTurn(context.Background(), claimEpic, claimedAt())
	if err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	if err := s.FailTurn(context.Background(), claimEpic, turn.Seq, reason, failedAt()); err != nil {
		t.Fatalf("FailTurn of turn %d: %v", turn.Seq, err)
	}
}

// claimAndFree claims a turn of claimEpic and ends it free with reason.
func claimAndFree(t *testing.T, s *Store, reason string) {
	t.Helper()
	turn, err := s.ClaimTurn(context.Background(), claimEpic, claimedAt())
	if err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	if err := s.FreeTurn(context.Background(), claimEpic, turn.Seq, TurnFree{Reason: reason}, failedAt()); err != nil {
		t.Fatalf("FreeTurn of turn %d: %v", turn.Seq, err)
	}
}

// epicFlags reads the paused flag and the park reason of claimEpic.
func epicFlags(t *testing.T, s *Store) (int64, string) {
	t.Helper()
	var paused int64
	var parked string
	err := s.db.QueryRow(`SELECT paused, parked FROM epics WHERE id = ?`, claimEpic).Scan(&paused, &parked)
	if err != nil {
		t.Fatalf("read paused and parked of %s: %v", claimEpic, err)
	}
	return paused, parked
}

// storedTurnReason reads the reason of turn seq of epic id.
func storedTurnReason(t *testing.T, s *Store, id string, seq int64) string {
	t.Helper()
	var reason string
	err := s.db.QueryRow(`SELECT reason FROM epic_turns WHERE epic = ? AND seq = ?`, id, seq).Scan(&reason)
	if err != nil {
		t.Fatalf("read reason of turn %d of %s: %v", seq, id, err)
	}
	return reason
}

func TestFailTurnStoresTheReasonAndFreesItsTriggersToPending(t *testing.T) {
	ctx := context.Background()
	s := openEpicStore(t)
	const id = claimEpic
	createEpic(t, s, fullEpic(id))
	addPendingTrigger(t, s)
	if _, err := s.ClaimTurn(ctx, id, claimedAt()); err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}

	if err := s.FailTurn(ctx, id, 1, "return had no outline", failedAt()); err != nil {
		t.Fatalf("FailTurn: %v", err)
	}

	wantTurns := [][]any{
		{id, int64(1), "", "failed", "return had no outline", int64(len(seededDecisions())), "", "",
			int64(1700000500), int64(1700000700), int64(0), int64(0), int64(0)},
	}
	if got := rowsOf(t, s.db, "epic_turns"); !reflect.DeepEqual(got, wantTurns) {
		t.Errorf("epic_turns after FailTurn = %v, want %v", got, wantTurns)
	}
	if got := rowsOf(t, s.db, "epic_triggers"); !reflect.DeepEqual(got, triggerRows("pending", 0)) {
		t.Errorf("epic_triggers after FailTurn = %v, want %v", got, triggerRows("pending", 0))
	}

	turn, err := s.ClaimTurn(ctx, id, claimedAt().Add(time.Minute))
	if err != nil {
		t.Fatalf("ClaimTurn after FailTurn: %v", err)
	}
	if turn.Seq != 2 {
		t.Errorf("ClaimTurn after FailTurn seq = %d, want 2", turn.Seq)
	}
	if got := rowsOf(t, s.db, "epic_triggers"); !reflect.DeepEqual(got, triggerRows("claimed", 2)) {
		t.Errorf("epic_triggers after ClaimTurn of turn 2 = %v, want %v", got, triggerRows("claimed", 2))
	}
}

func TestFailTurnCapsTheReasonAt1024Bytes(t *testing.T) {
	ctx := context.Background()
	s := openEpicStore(t)
	const id = claimEpic
	createEpic(t, s, fullEpic(id))
	addPendingTrigger(t, s)
	if _, err := s.ClaimTurn(ctx, id, claimedAt()); err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}

	if err := s.FailTurn(ctx, id, 1, strings.Repeat("x", 2000), failedAt()); err != nil {
		t.Fatalf("FailTurn: %v", err)
	}

	if got := storedTurnReason(t, s, id, 1); got != strings.Repeat("x", 1024) {
		t.Errorf("stored reason of a 2000-byte FailTurn has %d bytes, want 1024 bytes of x", len(got))
	}
}

func TestFailTurnCapsTheReasonAtARuneBoundary(t *testing.T) {
	ctx := context.Background()
	s := openEpicStore(t)
	const id = claimEpic
	createEpic(t, s, fullEpic(id))
	addPendingTrigger(t, s)
	if _, err := s.ClaimTurn(ctx, id, claimedAt()); err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}

	// Each 界 is three bytes, so byte 1024 falls inside the 342nd rune: 341 runes, 1023 bytes, are kept.
	if err := s.FailTurn(ctx, id, 1, strings.Repeat("界", 700), failedAt()); err != nil {
		t.Fatalf("FailTurn: %v", err)
	}

	if got := storedTurnReason(t, s, id, 1); got != strings.Repeat("界", 341) {
		t.Errorf("stored reason of a 2100-byte FailTurn = %d bytes, want 341 runes of 界 (1023 bytes)", len(got))
	}
}

func TestThreeFailedTurnsInARowParkTheEpicWithTheStoredReason(t *testing.T) {
	s := openEpicStore(t)
	const id = claimEpic
	createEpic(t, s, fullEpic(id))
	addPendingTrigger(t, s)

	claimAndFail(t, s, "first failure")
	claimAndFail(t, s, "second failure")
	if _, parked := epicFlags(t, s); parked != "" {
		t.Fatalf("parked after two failed turns = %q, want none", parked)
	}

	claimAndFail(t, s, "third failure")
	if _, parked := epicFlags(t, s); parked != parkPrefix+"third failure" {
		t.Errorf("parked after three failed turns = %q, want %q", parked, parkPrefix+"third failure")
	}
}

func TestParkedReasonIsCappedAt1024BytesWithItsPrefix(t *testing.T) {
	s := openEpicStore(t)
	const id = claimEpic
	createEpic(t, s, fullEpic(id))
	addPendingTrigger(t, s)

	claimAndFail(t, s, "first failure")
	claimAndFail(t, s, "second failure")
	claimAndFail(t, s, strings.Repeat("x", 2000))

	// The 33-byte prefix leaves 991 bytes of the stored 1024-byte reason inside the 1024-byte park reason.
	if _, parked := epicFlags(t, s); parked != parkPrefix+strings.Repeat("x", 991) {
		t.Errorf("parked after three failed turns has %d bytes, want the prefix and 991 bytes of x", len(parked))
	}
}

func TestFreeTurnBetweenFailuresNeitherResetsNorCountsTowardsThree(t *testing.T) {
	s := openEpicStore(t)
	const id = claimEpic
	createEpic(t, s, fullEpic(id))
	addPendingTrigger(t, s)

	claimAndFail(t, s, "first failure")
	claimAndFail(t, s, "second failure")
	claimAndFree(t, s, "free turn")
	if _, parked := epicFlags(t, s); parked != "" {
		t.Fatalf("parked after two failed turns and a free turn = %q, want none", parked)
	}

	claimAndFail(t, s, "fourth failure")
	if _, parked := epicFlags(t, s); parked != parkPrefix+"fourth failure" {
		t.Errorf("parked after a free turn and a third failure = %q, want %q", parked,
			parkPrefix+"fourth failure")
	}
}

func TestSucceededTurnResetsTheFailureCount(t *testing.T) {
	ctx := context.Background()
	s := openEpicStore(t)
	const id = claimEpic
	createEpic(t, s, fullEpic(id))
	addPendingTrigger(t, s)

	claimAndFail(t, s, "first failure")
	claimAndFail(t, s, "second failure")
	turn, err := s.ClaimTurn(ctx, id, claimedAt())
	if err != nil {
		t.Fatalf("ClaimTurn of the succeeding turn: %v", err)
	}
	if err := s.CompleteTurn(ctx, id, turn.Seq, plannedTurn(), completedAt()); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
	if err := sqlcgen.New(s.db).InsertEpicTrigger(ctx, sqlcgen.InsertEpicTriggerParams{
		Epic: id, ID: "t3", Cause: "later trigger", State: "pending", Turn: 0, Created: epicCreated().Unix(),
	}); err != nil {
		t.Fatalf("insert trigger t3: %v", err)
	}

	claimAndFail(t, s, "fourth failure")
	claimAndFail(t, s, "fifth failure")
	if _, parked := epicFlags(t, s); parked != "" {
		t.Fatalf("parked after a succeeded turn and two failed turns = %q, want none", parked)
	}

	claimAndFail(t, s, "sixth failure")
	if _, parked := epicFlags(t, s); parked != parkPrefix+"sixth failure" {
		t.Errorf("parked after three failed turns since the success = %q, want %q", parked,
			parkPrefix+"sixth failure")
	}
}

func TestFreeTurnFreesItsTriggersAndStoresItsReason(t *testing.T) {
	ctx := context.Background()
	s := openEpicStore(t)
	const id = claimEpic
	createEpic(t, s, fullEpic(id))
	addPendingTrigger(t, s)
	if _, err := s.ClaimTurn(ctx, id, claimedAt()); err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}

	if err := s.FreeTurn(ctx, id, 1, TurnFree{Reason: "rate limited"}, failedAt()); err != nil {
		t.Fatalf("FreeTurn: %v", err)
	}

	wantTurns := [][]any{
		{id, int64(1), "", "free", "rate limited", int64(len(seededDecisions())), "", "",
			int64(1700000500), int64(1700000700), int64(0), int64(0), int64(0)},
	}
	if got := rowsOf(t, s.db, "epic_turns"); !reflect.DeepEqual(got, wantTurns) {
		t.Errorf("epic_turns after FreeTurn = %v, want %v", got, wantTurns)
	}
	if got := rowsOf(t, s.db, "epic_triggers"); !reflect.DeepEqual(got, triggerRows("pending", 0)) {
		t.Errorf("epic_triggers after FreeTurn = %v, want %v", got, triggerRows("pending", 0))
	}
	if paused, parked := epicFlags(t, s); paused != 0 || parked != "" {
		t.Errorf("epic after FreeTurn with no Pause or Park: paused = %d, parked = %q, want 0 and empty",
			paused, parked)
	}
	if turn, err := s.ClaimTurn(ctx, id, claimedAt().Add(time.Minute)); err != nil || turn.Seq != 2 {
		t.Errorf("ClaimTurn after FreeTurn = seq %d, %v, want seq 2 and no error", turn.Seq, err)
	}
}

func TestFreeTurnWithPausePausesTheEpic(t *testing.T) {
	ctx := context.Background()
	s := openEpicStore(t)
	const id = claimEpic
	createEpic(t, s, fullEpic(id))
	if _, err := s.ClaimTurn(ctx, id, claimedAt()); err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}

	if err := s.FreeTurn(ctx, id, 1, TurnFree{Reason: "stopped", Pause: true}, failedAt()); err != nil {
		t.Fatalf("FreeTurn: %v", err)
	}

	if paused, parked := epicFlags(t, s); paused != 1 || parked != "" {
		t.Errorf("epic after FreeTurn with Pause: paused = %d, parked = %q, want 1 and empty", paused, parked)
	}
	if _, err := s.ClaimTurn(ctx, id, claimedAt().Add(time.Minute)); !errors.Is(err, ErrNotClaimable) {
		t.Errorf("ClaimTurn of a paused epic err = %v, want ErrNotClaimable", err)
	}
}

func TestFreeTurnWithParkParksTheEpic(t *testing.T) {
	ctx := context.Background()
	s := openEpicStore(t)
	const id = claimEpic
	createEpic(t, s, fullEpic(id))
	if _, err := s.ClaimTurn(ctx, id, claimedAt()); err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}

	free := TurnFree{Reason: "agent unresolved", Park: "no planner role"}
	if err := s.FreeTurn(ctx, id, 1, free, failedAt()); err != nil {
		t.Fatalf("FreeTurn: %v", err)
	}

	if paused, parked := epicFlags(t, s); paused != 0 || parked != "no planner role" {
		t.Errorf("epic after FreeTurn with Park: paused = %d, parked = %q, want 0 and %q", paused, parked,
			"no planner role")
	}
}

func TestFailAndFreeTurnRefuseATurnThatIsNotRunningAndWriteNothing(t *testing.T) {
	ctx := context.Background()
	s := openEpicStore(t)
	const id = claimEpic
	createEpic(t, s, fullEpic(id))
	if _, err := s.ClaimTurn(ctx, id, claimedAt()); err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	if err := s.CompleteTurn(ctx, id, 1, plannedTurn(), completedAt()); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
	before := planRowsOf(t, s)
	pausedBefore, parkedBefore := epicFlags(t, s)

	calls := map[string]func() error{
		"FailTurn of succeeded turn": func() error {
			return s.FailTurn(ctx, id, 1, "late", failedAt())
		},
		"FailTurn of missing turn": func() error {
			return s.FailTurn(ctx, id, 9, "late", failedAt())
		},
		"FreeTurn of succeeded turn": func() error {
			return s.FreeTurn(ctx, id, 1, TurnFree{Reason: "late", Pause: true, Park: "late"}, failedAt())
		},
		"FreeTurn of missing turn": func() error {
			return s.FreeTurn(ctx, id, 9, TurnFree{Reason: "late", Pause: true, Park: "late"}, failedAt())
		},
	}
	for name, call := range calls {
		if err := call(); !errors.Is(err, ErrTurnNotRunning) {
			t.Errorf("%s err = %v, want ErrTurnNotRunning", name, err)
		}
	}

	if got := planRowsOf(t, s); !reflect.DeepEqual(got, before) {
		t.Errorf("plan rows after refused turn ends = %v, want %v", got, before)
	}
	if paused, parked := epicFlags(t, s); paused != pausedBefore || parked != parkedBefore {
		t.Errorf("epic after refused turn ends: paused = %d, parked = %q, want %d and %q", paused, parked,
			pausedBefore, parkedBefore)
	}
}

func TestParkEpicParksTheEpicWithItsReason(t *testing.T) {
	ctx := context.Background()
	s := openEpicStore(t)
	const id = claimEpic
	createEpic(t, s, fullEpic(id))

	if err := s.ParkEpic(ctx, id, "needs a human"); err != nil {
		t.Fatalf("ParkEpic: %v", err)
	}

	if paused, parked := epicFlags(t, s); paused != 0 || parked != "needs a human" {
		t.Errorf("epic after ParkEpic: paused = %d, parked = %q, want 0 and %q", paused, parked, "needs a human")
	}
	if _, err := s.ClaimTurn(ctx, id, claimedAt()); !errors.Is(err, ErrNotClaimable) {
		t.Errorf("ClaimTurn of a parked epic err = %v, want ErrNotClaimable", err)
	}
}

func TestParkEpicCapsTheReasonAt1024Bytes(t *testing.T) {
	ctx := context.Background()
	s := openEpicStore(t)
	const id = claimEpic
	createEpic(t, s, fullEpic(id))

	if err := s.ParkEpic(ctx, id, strings.Repeat("y", 2000)); err != nil {
		t.Fatalf("ParkEpic: %v", err)
	}

	if _, parked := epicFlags(t, s); parked != strings.Repeat("y", 1024) {
		t.Errorf("parked reason of a 2000-byte ParkEpic has %d bytes, want 1024 bytes of y", len(parked))
	}
}

func TestParkEpicOfUnknownEpicIsErrNoEpic(t *testing.T) {
	s := openEpicStore(t)
	if err := s.ParkEpic(context.Background(), "orrery-1a2b3c4/missing", "reason"); !errors.Is(err, ErrNoEpic) {
		t.Fatalf("ParkEpic of unknown epic err = %v, want ErrNoEpic", err)
	}
}
