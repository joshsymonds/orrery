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

// syncRewakeGap is the sync gap the rewake tests give planner turns to.
const syncRewakeGap = "sync:main-a"

// syncTriggersOf returns the stored triggers of integrateEpic whose ids name a sync, as ListEpicTriggers returns them.
func syncTriggersOf(ctx context.Context, t *testing.T, s *Store) []sqlcgen.EpicTrigger {
	t.Helper()
	rows, err := s.fixtureQueries().ListEpicTriggers(ctx, integrateEpic)
	if err != nil {
		t.Fatalf("ListEpicTriggers of epic %s: %v", integrateEpic, err)
	}
	var syncTriggers []sqlcgen.EpicTrigger
	for _, row := range rows {
		if strings.HasPrefix(row.ID, "sync:") {
			syncTriggers = append(syncTriggers, row)
		}
	}
	return syncTriggers
}

// TestRewakeSync checks that RewakeSync stores a sync's pending trigger and its gap turn after one call, that a repeat
// of the call with a trigger id already stored changes nothing, and that a third turn of the same gap is refused with
// ErrGapSpent and adds no trigger.
func TestRewakeSync(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	first := NodeTrigger{ID: "sync:main-a:1", Cause: "sync of main failed at its gate"}
	firstTurn := GapTurn{Gap: syncRewakeGap, Turn: 1, Reason: "the gate failed"}
	if err := s.RewakeSync(ctx, integrateEpic, first, firstTurn, gapsAt()); err != nil {
		t.Fatalf("RewakeSync of turn 1: %v", err)
	}
	wantFirst := []sqlcgen.EpicTrigger{
		{Epic: integrateEpic, ID: first.ID, Cause: first.Cause, State: triggerPending, Created: gapsAt().Unix()},
	}
	if got := syncTriggersOf(ctx, t, s); !reflect.DeepEqual(got, wantFirst) {
		t.Errorf("sync triggers = %+v, want %+v", got, wantFirst)
	}
	wantFirstTurns := []GapTurn{{Gap: syncRewakeGap, Turn: 1, Reason: "the gate failed", Created: gapsAt()}}
	gotFirstTurns, err := s.GapTurns(ctx, integrateEpic)
	if err != nil {
		t.Fatalf("GapTurns: %v", err)
	}
	if !reflect.DeepEqual(gotFirstTurns, wantFirstTurns) {
		t.Errorf("GapTurns = %+v, want %+v", gotFirstTurns, wantFirstTurns)
	}

	later := gapsAt().Add(time.Hour)
	if err := s.RewakeSync(ctx, integrateEpic, NodeTrigger{ID: first.ID, Cause: "a repeat"},
		GapTurn{Gap: syncRewakeGap, Turn: 1, Reason: "a repeat"}, later); err != nil {
		t.Fatalf("repeated RewakeSync of turn 1: %v", err)
	}
	if got := syncTriggersOf(ctx, t, s); !reflect.DeepEqual(got, wantFirst) {
		t.Errorf("sync triggers after a repeat = %+v, want %+v", got, wantFirst)
	}
	gotRepeatTurns, err := s.GapTurns(ctx, integrateEpic)
	if err != nil {
		t.Fatalf("GapTurns after a repeat: %v", err)
	}
	if !reflect.DeepEqual(gotRepeatTurns, wantFirstTurns) {
		t.Errorf("GapTurns after a repeat = %+v, want %+v", gotRepeatTurns, wantFirstTurns)
	}

	second := NodeTrigger{ID: "sync:main-a:2", Cause: "sync of main failed again"}
	if err := s.RewakeSync(ctx, integrateEpic, second,
		GapTurn{Gap: syncRewakeGap, Turn: 2, Reason: "the gate failed again"}, gapsAt()); err != nil {
		t.Fatalf("RewakeSync of turn 2 after the repeat: %v", err)
	}

	third := NodeTrigger{ID: "sync:main-a:3", Cause: "sync of main failed a third time"}
	err = s.RewakeSync(ctx, integrateEpic, third,
		GapTurn{Gap: syncRewakeGap, Turn: 3, Reason: "a third failure"}, gapsAt())
	if !errors.Is(err, ErrGapSpent) {
		t.Fatalf("RewakeSync of turn 3 = %v, want an error matching %v", err, ErrGapSpent)
	}
	wantBoth := []sqlcgen.EpicTrigger{
		{Epic: integrateEpic, ID: first.ID, Cause: first.Cause, State: triggerPending, Created: gapsAt().Unix()},
		{Epic: integrateEpic, ID: second.ID, Cause: second.Cause, State: triggerPending, Created: gapsAt().Unix()},
	}
	if got := syncTriggersOf(ctx, t, s); !reflect.DeepEqual(got, wantBoth) {
		t.Errorf("sync triggers after a refused turn = %+v, want %+v", got, wantBoth)
	}
	wantBothTurns := []GapTurn{
		{Gap: syncRewakeGap, Turn: 1, Reason: "the gate failed", Created: gapsAt()},
		{Gap: syncRewakeGap, Turn: 2, Reason: "the gate failed again", Created: gapsAt()},
	}
	gotBothTurns, err := s.GapTurns(ctx, integrateEpic)
	if err != nil {
		t.Fatalf("GapTurns after a refused turn: %v", err)
	}
	if !reflect.DeepEqual(gotBothTurns, wantBothTurns) {
		t.Errorf("GapTurns after a refused turn = %+v, want %+v", gotBothTurns, wantBothTurns)
	}
}

// TestSyncDeclarationsStandAcrossTurns checks that EpicFlow's SyncDeclarations holds a sync gap a succeeded turn
// declared after a later succeeded turn declared nothing, and holds neither a gap that does not start sync: nor a sync
// gap a turn that did not succeed declared.
func TestSyncDeclarationsStandAcrossTurns(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	const id = claimEpic
	createEpic(t, s, fullEpic(id))
	addPendingTrigger(t, s)

	declaring, err := s.ClaimTurn(ctx, id, claimedAt())
	if err != nil {
		t.Fatalf("ClaimTurn of the declaring turn: %v", err)
	}
	if err := s.CompleteTurn(ctx, id, declaring.Seq, EpicPlan{
		Outcome: "work",
		Unresolvable: []GapDeclaration{
			{Gap: syncRewakeGap, Reason: "the merge needs the owner", Recommended: "release"},
			{Gap: "gap-b", Reason: "a gap that is not a sync", Recommended: "correct"},
		},
	}, completedAt()); err != nil {
		t.Fatalf("CompleteTurn of the declaring turn: %v", err)
	}

	if err := s.fixtureQueries().InsertEpicTrigger(ctx, sqlcgen.InsertEpicTriggerParams{
		Epic: id, ID: "t3", Cause: "test trigger", State: "pending", Turn: 0, Created: epicCreated().Unix(),
	}); err != nil {
		t.Fatalf("insert trigger t3 of %s: %v", id, err)
	}
	failing, err := s.ClaimTurn(ctx, id, claimedAt())
	if err != nil {
		t.Fatalf("ClaimTurn of the failing turn: %v", err)
	}
	if err := s.fixtureQueries().InsertPlannerUnresolvable(ctx, sqlcgen.InsertPlannerUnresolvableParams{
		Epic: id, Turn: failing.Seq, Position: 1, Gap: "sync:main-c", Reason: "a turn that failed", Recommended: "release",
	}); err != nil {
		t.Fatalf("insert declaration of the failing turn: %v", err)
	}
	if err := s.FailTurn(ctx, id, failing.Seq, "the plan broke", completedAt()); err != nil {
		t.Fatalf("FailTurn: %v", err)
	}

	silent, err := s.ClaimTurn(ctx, id, claimedAt())
	if err != nil {
		t.Fatalf("ClaimTurn of the turn that declares nothing: %v", err)
	}
	if err := s.CompleteTurn(ctx, id, silent.Seq, EpicPlan{Outcome: "work"}, completedAt()); err != nil {
		t.Fatalf("CompleteTurn of the turn that declares nothing: %v", err)
	}

	flow, err := s.EpicFlow(ctx, id)
	if err != nil {
		t.Fatalf("EpicFlow: %v", err)
	}
	want := []GapDeclaration{{Gap: syncRewakeGap, Reason: "the merge needs the owner", Recommended: "release"}}
	if !reflect.DeepEqual(flow.SyncDeclarations, want) {
		t.Errorf("EpicFlow SyncDeclarations = %+v, want %+v", flow.SyncDeclarations, want)
	}
}
