package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// claimedAt is the second the claim tests stamp on a turn.
func claimedAt() time.Time {
	return time.Unix(1700000500, 0)
}

// claimEpic is the epic the claim tests create.
const claimEpic = "orrery-1a2b3c4/epic-store"

// addPendingTrigger stores a second pending trigger, t2, on claimEpic, as a later planner trigger would.
func addPendingTrigger(t *testing.T, s *Store) {
	t.Helper()
	if err := sqlcgen.New(s.db).InsertEpicTrigger(context.Background(), sqlcgen.InsertEpicTriggerParams{
		Epic: claimEpic, ID: "t2", Cause: "test trigger", State: "pending", Turn: 0, Created: epicCreated().Unix(),
	}); err != nil {
		t.Fatalf("insert trigger t2 of %s: %v", claimEpic, err)
	}
}

func TestClaimTurnClaimsEveryPendingTriggerInOneTurn(t *testing.T) {
	ctx := context.Background()
	s := openEpicStore(t)
	const id = claimEpic
	createEpic(t, s, fullEpic(id))
	addPendingTrigger(t, s)

	turn, err := s.ClaimTurn(ctx, id, claimedAt())
	if err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	want := EpicTurn{
		Epic: id, Seq: 1, State: "running", Seen: int64(len(seededDecisions())), Started: claimedAt(),
	}
	if !reflect.DeepEqual(turn, want) {
		t.Errorf("ClaimTurn = %+v, want %+v", turn, want)
	}

	wantTriggers := [][]any{
		{id, createdTriggerID, createdTriggerCause, "claimed", int64(1), int64(1700000000)},
		{id, "t2", "test trigger", "claimed", int64(1), int64(1700000000)},
	}
	if got := rowsOf(t, s.db, "epic_triggers"); !reflect.DeepEqual(got, wantTriggers) {
		t.Errorf("epic_triggers after ClaimTurn = %v, want %v", got, wantTriggers)
	}
}

func TestClaimTurnRefusesWhileATurnRunsAndWritesNothing(t *testing.T) {
	ctx := context.Background()
	s := openEpicStore(t)
	const id = claimEpic
	createEpic(t, s, fullEpic(id))
	if _, err := s.ClaimTurn(ctx, id, claimedAt()); err != nil {
		t.Fatalf("first ClaimTurn: %v", err)
	}
	addPendingTrigger(t, s)
	turnsBefore := rowsOf(t, s.db, "epic_turns")
	triggersBefore := rowsOf(t, s.db, "epic_triggers")

	if _, err := s.ClaimTurn(ctx, id, claimedAt().Add(time.Minute)); !errors.Is(err, ErrNotClaimable) {
		t.Fatalf("second ClaimTurn err = %v, want ErrNotClaimable", err)
	}
	if got := rowsOf(t, s.db, "epic_turns"); !reflect.DeepEqual(got, turnsBefore) {
		t.Errorf("epic_turns after refused ClaimTurn = %v, want %v", got, turnsBefore)
	}
	if got := rowsOf(t, s.db, "epic_triggers"); !reflect.DeepEqual(got, triggersBefore) {
		t.Errorf("epic_triggers after refused ClaimTurn = %v, want %v", got, triggersBefore)
	}
}

func TestClaimTurnRefusesPausedAndParkedEpicsAndWritesNothing(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name  string
		epic  string
		setup string
	}{
		{name: "paused", epic: "orrery-1a2b3c4/paused", setup: `UPDATE epics SET paused = 1 WHERE id = ?`},
		{name: "parked", epic: "orrery-1a2b3c4/parked", setup: `UPDATE epics SET parked = 'needs a human' WHERE id = ?`},
	} {
		s := openEpicStore(t)
		createEpic(t, s, fullEpic(tc.epic))
		if _, err := s.db.Exec(tc.setup, tc.epic); err != nil {
			t.Fatalf("set %s: %v", tc.name, err)
		}
		triggersBefore := rowsOf(t, s.db, "epic_triggers")

		if _, err := s.ClaimTurn(ctx, tc.epic, claimedAt()); !errors.Is(err, ErrNotClaimable) {
			t.Errorf("ClaimTurn of %s epic err = %v, want ErrNotClaimable", tc.name, err)
		}
		if got := rowsOf(t, s.db, "epic_turns"); len(got) != 0 {
			t.Errorf("%s: epic_turns after refused ClaimTurn = %v, want none", tc.name, got)
		}
		if got := rowsOf(t, s.db, "epic_triggers"); !reflect.DeepEqual(got, triggersBefore) {
			t.Errorf("%s: epic_triggers after refused ClaimTurn = %v, want %v", tc.name, got, triggersBefore)
		}
	}
}

func TestClaimTurnRefusesEpicWithNoPendingTrigger(t *testing.T) {
	ctx := context.Background()
	s := openEpicStore(t)
	const id = claimEpic
	createEpic(t, s, fullEpic(id))
	if _, err := s.ClaimTurn(ctx, id, claimedAt()); err != nil {
		t.Fatalf("first ClaimTurn: %v", err)
	}
	if _, err := s.db.Exec(`UPDATE epic_turns SET state = 'succeeded' WHERE epic = ?`, id); err != nil {
		t.Fatalf("finish turn: %v", err)
	}
	turnsBefore := rowsOf(t, s.db, "epic_turns")

	if _, err := s.ClaimTurn(ctx, id, claimedAt()); !errors.Is(err, ErrNotClaimable) {
		t.Fatalf("ClaimTurn with no pending trigger err = %v, want ErrNotClaimable", err)
	}
	if got := rowsOf(t, s.db, "epic_turns"); !reflect.DeepEqual(got, turnsBefore) {
		t.Errorf("epic_turns after refused ClaimTurn = %v, want %v", got, turnsBefore)
	}
}

func TestClaimTurnUnknownEpicIsErrNoEpic(t *testing.T) {
	ctx := context.Background()
	s := openEpicStore(t)

	if _, err := s.ClaimTurn(ctx, "orrery-1a2b3c4/missing", claimedAt()); !errors.Is(err, ErrNoEpic) {
		t.Fatalf("ClaimTurn of unknown epic err = %v, want ErrNoEpic", err)
	}
	if got := rowsOf(t, s.db, "epic_turns"); len(got) != 0 {
		t.Errorf("epic_turns after unknown ClaimTurn = %v, want none", got)
	}
}

func TestClaimTurnNumbersTurnsAndSeesTheLatestDecision(t *testing.T) {
	ctx := context.Background()
	s := openEpicStore(t)
	const id = claimEpic
	createEpic(t, s, fullEpic(id))
	if _, err := s.ClaimTurn(ctx, id, claimedAt()); err != nil {
		t.Fatalf("first ClaimTurn: %v", err)
	}
	if _, err := s.db.Exec(`UPDATE epic_turns SET state = 'succeeded' WHERE epic = ?`, id); err != nil {
		t.Fatalf("finish turn: %v", err)
	}
	if _, err := s.AnswerEpic(ctx, id, "D3", "Answered after turn 1", claimedAt()); err != nil {
		t.Fatalf("AnswerEpic: %v", err)
	}
	addPendingTrigger(t, s)

	turn, err := s.ClaimTurn(ctx, id, claimedAt().Add(time.Minute))
	if err != nil {
		t.Fatalf("second ClaimTurn: %v", err)
	}
	if turn.Seq != 2 {
		t.Errorf("second turn Seq = %d, want 2", turn.Seq)
	}
	if want := int64(len(seededDecisions()) + 1); turn.Seen != want {
		t.Errorf("second turn Seen = %d, want %d", turn.Seen, want)
	}
	wantTrigger := []any{id, "t2", "test trigger", "claimed", int64(2), int64(1700000000)}
	if got := rowsOf(t, s.db, "epic_triggers"); !reflect.DeepEqual(got[len(got)-1], wantTrigger) {
		t.Errorf("last epic_trigger after second ClaimTurn = %v, want %v", got[len(got)-1], wantTrigger)
	}
}

func TestPlanStatesListsEpicsWithPendingTriggersInIDOrder(t *testing.T) {
	ctx := context.Background()
	s := openEpicStore(t)
	const claimed, late, early = "orrery-1a2b3c4/claimed", "orrery-1a2b3c4/late", "orrery-1a2b3c4/early"
	createEpic(t, s, fullEpic(late))
	createEpic(t, s, fullEpic(claimed))
	createEpic(t, s, fullEpic(early))
	if _, err := s.ClaimTurn(ctx, claimed, claimedAt()); err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}

	got, err := s.PlanStates(ctx)
	if err != nil {
		t.Fatalf("PlanStates: %v", err)
	}
	want := []EpicPlanState{
		{Epic: early, Pending: 1},
		{Epic: late, Pending: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("PlanStates = %+v, want %+v", got, want)
	}
}

func TestPlanStatesReportsPauseParkAndRunningTurn(t *testing.T) {
	ctx := context.Background()
	s := openEpicStore(t)
	const id = claimEpic
	createEpic(t, s, fullEpic(id))
	if _, err := s.ClaimTurn(ctx, id, claimedAt()); err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	addPendingTrigger(t, s)

	got, err := s.PlanStates(ctx)
	if err != nil {
		t.Fatalf("PlanStates: %v", err)
	}
	want := []EpicPlanState{{Epic: id, Running: true, Pending: 1}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("PlanStates with a running turn = %+v, want %+v", got, want)
	}

	if _, err := s.db.Exec(`UPDATE epics SET paused = 1, parked = 'needs a human' WHERE id = ?`, id); err != nil {
		t.Fatalf("set paused and parked: %v", err)
	}
	got, err = s.PlanStates(ctx)
	if err != nil {
		t.Fatalf("PlanStates: %v", err)
	}
	want = []EpicPlanState{{Epic: id, Paused: true, Parked: "needs a human", Running: true, Pending: 1}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("PlanStates of paused and parked epic = %+v, want %+v", got, want)
	}
}

func TestSetTurnStepStoresTheStepOnARunningTurn(t *testing.T) {
	ctx := context.Background()
	s := openEpicStore(t)
	const id = claimEpic
	createEpic(t, s, fullEpic(id))
	if _, err := s.ClaimTurn(ctx, id, claimedAt()); err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}

	if err := s.SetTurnStep(ctx, id, 1, "step-1"); err != nil {
		t.Fatalf("SetTurnStep: %v", err)
	}
	want := [][]any{{
		id, int64(1), "step-1", "running", "", int64(len(seededDecisions())), "", "", int64(1700000500), int64(0),
		int64(0), int64(0), int64(0),
	}}
	if got := rowsOf(t, s.db, "epic_turns"); !reflect.DeepEqual(got, want) {
		t.Errorf("epic_turns after SetTurnStep = %v, want %v", got, want)
	}
}

func TestSetTurnStepRefusesATurnThatIsNotRunning(t *testing.T) {
	ctx := context.Background()
	s := openEpicStore(t)
	const id = claimEpic
	createEpic(t, s, fullEpic(id))
	if _, err := s.ClaimTurn(ctx, id, claimedAt()); err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	if _, err := s.db.Exec(`UPDATE epic_turns SET state = 'succeeded' WHERE epic = ?`, id); err != nil {
		t.Fatalf("finish turn: %v", err)
	}
	turnsBefore := rowsOf(t, s.db, "epic_turns")

	if err := s.SetTurnStep(ctx, id, 1, "step-1"); !errors.Is(err, ErrTurnNotRunning) {
		t.Errorf("SetTurnStep of finished turn err = %v, want ErrTurnNotRunning", err)
	}
	if err := s.SetTurnStep(ctx, id, 9, "step-9"); !errors.Is(err, ErrTurnNotRunning) {
		t.Errorf("SetTurnStep of missing turn err = %v, want ErrTurnNotRunning", err)
	}
	if got := rowsOf(t, s.db, "epic_turns"); !reflect.DeepEqual(got, turnsBefore) {
		t.Errorf("epic_turns after refused SetTurnStep = %v, want %v", got, turnsBefore)
	}
}
