package store

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// insertPendingTrigger stores a pending trigger with id and cause on epic, as a later planner trigger would.
func insertPendingTrigger(t *testing.T, s *Store, epic, id, cause string) {
	t.Helper()
	if err := sqlcgen.New(s.db).InsertEpicTrigger(context.Background(), sqlcgen.InsertEpicTriggerParams{
		Epic: epic, ID: id, Cause: cause, State: "pending", Turn: 0, Created: epicCreated().Unix(),
	}); err != nil {
		t.Fatalf("insert trigger %s of %s: %v", id, epic, err)
	}
}

func TestEpicReadsItsTurnsTasksAndQuestions(t *testing.T) {
	ctx := context.Background()
	s := openEpicStore(t)
	const id = claimEpic
	createEpic(t, s, fullEpic(id))
	addPendingTrigger(t, s)
	if _, err := s.ClaimTurn(ctx, id, claimedAt()); err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	if err := s.SetTurnStep(ctx, id, 1, "step-1"); err != nil {
		t.Fatalf("SetTurnStep: %v", err)
	}
	if err := s.CompleteTurn(ctx, id, 1, plannedTurn(), completedAt()); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}

	got, err := s.Epic(ctx, id)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	want := fullEpic(id)
	want.Decisions = seededDecisions()
	want.Turns = []EpicTurn{{
		Epic: id, Seq: 1, Step: "step-1", State: "succeeded", Seen: int64(len(seededDecisions())),
		Outline: "Store, then read", Outcome: "work", Decisions: plannedTurn().Decisions,
		Started: claimedAt(), Ended: completedAt(),
	}}
	want.Tasks = []EpicTask{
		{
			Seq: 1, Turn: 1, Title: "Write the store", Deliverable: "CompleteTurn stores the plan",
			Files: []string{"a.go", "b.go"}, Requirements: []string{"R5", "R6"}, Gate: "check",
			Evidence: evidence.Item{
				Capture: []string{"scripts/check"}, Limit: 1800, Artifacts: []string{"out/report.txt"},
				Conclusion: "the store keeps the plan", Judge: "the store judge",
			},
			Status: "open",
		},
		{
			Seq: 2, Turn: 1, Title: "Read the store", Deliverable: "Reads see the plan",
			Files: []string{"c.go", "d.go"}, Requirements: []string{"R7", "R8"}, Gate: "race",
			Evidence: evidence.Item{
				Supplied: []string{"docs/read.md"}, Conclusion: "reads see the plan", Judge: "the read judge",
			},
			Status: "open",
		},
	}
	want.Questions = []EpicQuestion{{Seq: 1, Turn: 1, Text: "Keep the old tasks?", State: "open"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Epic after a completed turn:\n got %+v\nwant %+v", got, want)
	}
}

func TestPausedAndParkedEpicsReadTheirReasonsFromEpicAndEpics(t *testing.T) {
	ctx := context.Background()
	s := openEpicStore(t)
	const active, paused, parked = "orrery-1a2b3c4/active", "orrery-1a2b3c4/paused", "orrery-1a2b3c4/parked"
	for _, id := range []string{active, paused, parked} {
		createEpic(t, s, fullEpic(id))
	}
	if _, err := s.ClaimTurn(ctx, paused, claimedAt()); err != nil {
		t.Fatalf("ClaimTurn of paused epic: %v", err)
	}
	if err := s.FreeTurn(ctx, paused, 1, TurnFree{Reason: "rate limited", Pause: true},
		time.Unix(1700000560, 0)); err != nil {
		t.Fatalf("FreeTurn of paused epic: %v", err)
	}
	if err := s.ParkEpic(ctx, parked, "needs a human"); err != nil {
		t.Fatalf("ParkEpic: %v", err)
	}

	heads, err := s.Epics(ctx)
	if err != nil {
		t.Fatalf("Epics: %v", err)
	}
	listed := make(map[string]EpicRow, len(heads))
	for _, head := range heads {
		listed[head.ID] = head
	}
	for _, tc := range []struct {
		id     string
		paused bool
		parked string
	}{
		{id: active},
		{id: paused, paused: true},
		{id: parked, parked: "needs a human"},
	} {
		got, err := s.Epic(ctx, tc.id)
		if err != nil {
			t.Fatalf("Epic %s: %v", tc.id, err)
		}
		if got.Paused != tc.paused || got.Parked != tc.parked {
			t.Errorf("Epic %s paused=%t parked=%q, want paused=%t parked=%q",
				tc.id, got.Paused, got.Parked, tc.paused, tc.parked)
		}
		head, ok := listed[tc.id]
		if !ok {
			t.Fatalf("Epics lacks %s", tc.id)
		}
		if head.Paused != tc.paused || head.Parked != tc.parked {
			t.Errorf("Epics %s paused=%t parked=%q, want paused=%t parked=%q",
				tc.id, head.Paused, head.Parked, tc.paused, tc.parked)
		}
	}
}

func TestRunningTurnsListsOnlyRunningTurnsAcrossEpics(t *testing.T) {
	ctx := context.Background()
	s := openEpicStore(t)
	const alpha, beta, gamma = "orrery-1a2b3c4/alpha", "orrery-1a2b3c4/beta", "orrery-1a2b3c4/gamma"
	for _, id := range []string{alpha, beta, gamma} {
		createEpic(t, s, fullEpic(id))
	}

	// Alpha's turn 1 succeeds, so only its turn 2 runs.
	if _, err := s.ClaimTurn(ctx, alpha, claimedAt()); err != nil {
		t.Fatalf("ClaimTurn of alpha: %v", err)
	}
	if err := s.CompleteTurn(ctx, alpha, 1, plannedTurn(), completedAt()); err != nil {
		t.Fatalf("CompleteTurn of alpha: %v", err)
	}
	insertPendingTrigger(t, s, alpha, "t2", "test trigger")
	if _, err := s.ClaimTurn(ctx, alpha, time.Unix(1700000560, 0)); err != nil {
		t.Fatalf("second ClaimTurn of alpha: %v", err)
	}
	if err := s.SetTurnStep(ctx, alpha, 2, "alpha-step"); err != nil {
		t.Fatalf("SetTurnStep of alpha: %v", err)
	}

	if _, err := s.ClaimTurn(ctx, beta, claimedAt()); err != nil {
		t.Fatalf("ClaimTurn of beta: %v", err)
	}
	if err := s.SetTurnStep(ctx, beta, 1, "beta-step"); err != nil {
		t.Fatalf("SetTurnStep of beta: %v", err)
	}

	// Gamma's only turn failed, so it runs nothing.
	if _, err := s.ClaimTurn(ctx, gamma, claimedAt()); err != nil {
		t.Fatalf("ClaimTurn of gamma: %v", err)
	}
	if err := s.FailTurn(ctx, gamma, 1, "boom", completedAt()); err != nil {
		t.Fatalf("FailTurn of gamma: %v", err)
	}

	got, err := s.RunningTurns(ctx)
	if err != nil {
		t.Fatalf("RunningTurns: %v", err)
	}
	want := []EpicTurn{
		{
			Epic: alpha, Seq: 2, Step: "alpha-step", State: "running", Seen: int64(len(seededDecisions())),
			Started: time.Unix(1700000560, 0),
		},
		{
			Epic: beta, Seq: 1, Step: "beta-step", State: "running", Seen: int64(len(seededDecisions())),
			Started: claimedAt(),
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("RunningTurns:\n got %+v\nwant %+v", got, want)
	}
}

func TestTurnTriggersReturnsTheTriggersOfThatTurnOrderedByID(t *testing.T) {
	ctx := context.Background()
	s := openEpicStore(t)
	const id = claimEpic
	createEpic(t, s, fullEpic(id))
	insertPendingTrigger(t, s, id, "a1", "earlier trigger")
	addPendingTrigger(t, s)
	if _, err := s.ClaimTurn(ctx, id, claimedAt()); err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	if err := s.CompleteTurn(ctx, id, 1, plannedTurn(), completedAt()); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
	insertPendingTrigger(t, s, id, "z9", "later trigger")
	if _, err := s.ClaimTurn(ctx, id, time.Unix(1700000560, 0)); err != nil {
		t.Fatalf("second ClaimTurn: %v", err)
	}

	gotFirst, err := s.TurnTriggers(ctx, id, 1)
	if err != nil {
		t.Fatalf("TurnTriggers of turn 1: %v", err)
	}
	wantFirst := []EpicTrigger{
		{ID: "a1", Cause: "earlier trigger", State: "consumed", Turn: 1, Created: epicCreated()},
		{ID: createdTriggerID, Cause: createdTriggerCause, State: "consumed", Turn: 1, Created: epicCreated()},
		{ID: "t2", Cause: "test trigger", State: "consumed", Turn: 1, Created: epicCreated()},
	}
	if !reflect.DeepEqual(gotFirst, wantFirst) {
		t.Errorf("TurnTriggers of turn 1:\n got %+v\nwant %+v", gotFirst, wantFirst)
	}

	gotSecond, err := s.TurnTriggers(ctx, id, 2)
	if err != nil {
		t.Fatalf("TurnTriggers of turn 2: %v", err)
	}
	wantSecond := []EpicTrigger{
		{ID: "z9", Cause: "later trigger", State: "claimed", Turn: 2, Created: epicCreated()},
	}
	if !reflect.DeepEqual(gotSecond, wantSecond) {
		t.Errorf("TurnTriggers of turn 2:\n got %+v\nwant %+v", gotSecond, wantSecond)
	}

	gotNone, err := s.TurnTriggers(ctx, id, 3)
	if err != nil {
		t.Fatalf("TurnTriggers of turn 3: %v", err)
	}
	if gotNone != nil {
		t.Errorf("TurnTriggers of a turn with none = %+v, want nil", gotNone)
	}
}
