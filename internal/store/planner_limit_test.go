package store

import (
	"context"
	"testing"
)

// freedLimitTurnOf claims a turn of claimEpic, frees it with free, and returns the turn as stored.
func freedLimitTurnOf(t *testing.T, free TurnFree) EpicTurn {
	t.Helper()
	ctx := context.Background()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	turn, err := s.ClaimTurn(ctx, claimEpic, claimedAt())
	if err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	if err := s.FreeTurn(ctx, claimEpic, turn.Seq, free, failedAt()); err != nil {
		t.Fatalf("FreeTurn: %v", err)
	}
	epic, err := s.Epic(ctx, claimEpic)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	if len(epic.Turns) != 1 {
		t.Fatalf("turns of %s = %d, want exactly one", claimEpic, len(epic.Turns))
	}
	return epic.Turns[0]
}

func TestFreeTurnAsALimitWithNoResetTimeReadsBackLimitedWithZeroResets(t *testing.T) {
	t.Parallel()
	got := freedLimitTurnOf(t, TurnFree{Reason: "usage limit", Pause: true, Limit: true})

	if !got.Limited {
		t.Errorf("Limited of a limit with no reset time = false, want true")
	}
	if !got.Resets.IsZero() {
		t.Errorf("Resets of a limit with no reset time = %v, want zero", got.Resets)
	}
}

func TestFreeTurnAsALimitWithAResetTimeReadsBackLimitedAndTheReset(t *testing.T) {
	t.Parallel()
	got := freedLimitTurnOf(t, TurnFree{Reason: "usage limit", Pause: true, Limit: true, Resets: resetsAt()})

	if !got.Limited {
		t.Errorf("Limited of a limit with a reset time = false, want true")
	}
	if !got.Resets.Equal(resetsAt()) {
		t.Errorf("Resets of a limit with a reset time = %v, want %v", got.Resets, resetsAt())
	}
}

func TestFreeTurnWithoutLimitReadsBackNeitherLimitedNorAReset(t *testing.T) {
	t.Parallel()
	got := freedLimitTurnOf(t, TurnFree{Reason: "stopped", Pause: true})

	if got.Limited {
		t.Errorf("Limited of a turn freed without Limit = true, want false")
	}
	if !got.Resets.IsZero() {
		t.Errorf("Resets of a turn freed without Limit or a reset = %v, want zero", got.Resets)
	}
}
