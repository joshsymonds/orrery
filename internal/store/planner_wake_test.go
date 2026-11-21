package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

// startAndEndTurn starts claimEpic's first turn, which claims the epic's created trigger, and ends that turn. The epic
// then has no pending trigger and no running turn, so no planner turn can start until a trigger is added.
func startAndEndTurn(t *testing.T, s *Store) {
	t.Helper()
	ctx := context.Background()
	if _, err := s.ClaimTurn(ctx, claimEpic, claimedAt()); err != nil {
		t.Fatalf("first ClaimTurn: %v", err)
	}
	if _, err := s.fixtureExec(ctx, `UPDATE epic_turns SET state = 'succeeded' WHERE epic = ?`, claimEpic); err != nil {
		t.Fatalf("finish turn: %v", err)
	}
}

// TestPlannerWakePark: a park that wakes the planner adds its pending trigger, so a planner turn can start on the
// parked epic.
func TestPlannerWakePark(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	startAndEndTurn(t, s)
	if _, err := s.ClaimTurn(ctx, claimEpic, claimedAt()); !errors.Is(err, ErrNotClaimable) {
		t.Fatalf("ClaimTurn with no pending trigger err = %v, want ErrNotClaimable", err)
	}

	const reason = "epic not built: R1 is not green at the stored base s1"
	trigger := NodeTrigger{ID: "workflow:unbuilt", Cause: reason}
	if err := s.ParkEpicWaking(ctx, claimEpic, reason, trigger, claimedAt()); err != nil {
		t.Fatalf("ParkEpicWaking: %v", err)
	}

	turn, err := s.ClaimTurn(ctx, claimEpic, claimedAt().Add(time.Minute))
	if err != nil {
		t.Fatalf("ClaimTurn after the park: %v", err)
	}
	if turn.Seq != 2 {
		t.Errorf("turn started after the park Seq = %d, want 2", turn.Seq)
	}
}

// TestPlannerWakeOwnerKick: an owner kick adds its pending trigger, so a planner turn can start on the kicked epic.
func TestPlannerWakeOwnerKick(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	startAndEndTurn(t, s)
	if _, err := s.ClaimTurn(ctx, claimEpic, claimedAt()); !errors.Is(err, ErrNotClaimable) {
		t.Fatalf("ClaimTurn with no pending trigger err = %v, want ErrNotClaimable", err)
	}

	if err := s.KickEpic(ctx, claimEpic, "Try the sqlite plan", Maker{By: MakerOwner}, claimedAt()); err != nil {
		t.Fatalf("KickEpic: %v", err)
	}

	turn, err := s.ClaimTurn(ctx, claimEpic, claimedAt().Add(time.Minute))
	if err != nil {
		t.Fatalf("ClaimTurn after the kick: %v", err)
	}
	if turn.Seq != 2 {
		t.Errorf("turn started after the kick Seq = %d, want 2", turn.Seq)
	}
}
