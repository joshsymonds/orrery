package store

import (
	"context"
	"reflect"
	"testing"
	"time"
)

// endingEndedAt is the second the epic ending tests end an epic at.
func endingEndedAt() time.Time {
	return time.Unix(1700002300, 0)
}

// endingFreezeReview starts the review run of integrateEpic and freezes its review at rev-7, so the epic is reviewing
// with a frozen review whose clean revision is still empty.
func endingFreezeReview(t *testing.T, s *Store) {
	t.Helper()
	startIntegrateRuns(t, s, NodeRun{Path: "review", ID: "id-review", Node: "review", Kind: "review", Attempt: 1})
	if err := s.FreezeReview(context.Background(), integrateEpic, "review", 1, "rev-7"); err != nil {
		t.Fatalf("FreezeReview of epic %s: %v", integrateEpic, err)
	}
}

// TestEndEpicCleanStoresStoredBaseAsCleanRevision checks that EndEpic ends a frozen review's epic clean in one write:
// the phase becomes clean, no tell is added, since the release that follows tells the ending, and the review's clean
// revision is the epic's stored base.
func TestEndEpicCleanStoresStoredBaseAsCleanRevision(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	endingFreezeReview(t, s)

	if err := s.EndEpic(ctx, integrateEpic, "clean", "Epic ended clean.", endingEndedAt()); err != nil {
		t.Fatalf("EndEpic: %v", err)
	}
	if row := integrateEpicRow(t, s); row.Phase != "clean" {
		t.Errorf("phase of epic %s after EndEpic = %q, want clean", integrateEpic, row.Phase)
	}
	if got := integrateTells(t, s); len(got) != 0 {
		t.Errorf("tells of epic %s after EndEpic = %q, want none", integrateEpic, got)
	}
	requireReview(t, s, Review{Frozen: "rev-7", Clean: integrationBase})
}

// TestEndEpicGapsSetsPhaseAndTellAndLeavesCleanEmpty checks that EndEpic ends a frozen review's epic ended_with_gaps:
// the phase and the body's tell are set, and the review's clean revision stays empty.
func TestEndEpicGapsSetsPhaseAndTellAndLeavesCleanEmpty(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	endingFreezeReview(t, s)

	if err := s.EndEpic(ctx, integrateEpic, "ended_with_gaps", "Epic ended with 1 gap.", endingEndedAt()); err != nil {
		t.Fatalf("EndEpic: %v", err)
	}
	if row := integrateEpicRow(t, s); row.Phase != "ended_with_gaps" {
		t.Errorf("phase of epic %s after EndEpic = %q, want ended_with_gaps", integrateEpic, row.Phase)
	}
	if got := integrateTells(t, s); !reflect.DeepEqual(got, []string{"Epic ended with 1 gap."}) {
		t.Errorf("tells of epic %s after EndEpic = %q, want one tell %q", integrateEpic, got, "Epic ended with 1 gap.")
	}
	requireReview(t, s, Review{Frozen: "rev-7"})
}

// TestEndEpicInSamePhaseChangesNothing checks that a repeat EndEpic in the phase the epic is already in adds no second
// tell. It repeats ended_with_gaps, a phase that adds a tell, so a repeat that re-added one would show.
func TestEndEpicInSamePhaseChangesNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	endingFreezeReview(t, s)
	if err := s.EndEpic(ctx, integrateEpic, "ended_with_gaps", "Epic ended with 1 gap.", endingEndedAt()); err != nil {
		t.Fatalf("EndEpic: %v", err)
	}

	if err := s.EndEpic(ctx, integrateEpic, "ended_with_gaps", "Epic ended with 2 gaps.", endingEndedAt()); err != nil {
		t.Fatalf("repeat EndEpic: %v", err)
	}
	if row := integrateEpicRow(t, s); row.Phase != "ended_with_gaps" {
		t.Errorf("phase of epic %s after a repeat = %q, want ended_with_gaps", integrateEpic, row.Phase)
	}
	if got := integrateTells(t, s); !reflect.DeepEqual(got, []string{"Epic ended with 1 gap."}) {
		t.Errorf("tells of epic %s after a repeat = %q, want one tell %q", integrateEpic, got, "Epic ended with 1 gap.")
	}
	requireReview(t, s, Review{Frozen: "rev-7"})
}
