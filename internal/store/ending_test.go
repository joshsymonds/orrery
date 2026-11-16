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

	if err := s.EndEpic(ctx, integrateEpic, "clean", "Epic ended clean.", nil, endingEndedAt()); err != nil {
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

	if err := s.EndEpic(
		ctx,
		integrateEpic,
		"ended_with_gaps",
		"Epic ended with 1 gap.",
		nil,
		endingEndedAt(),
	); err != nil {
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
	if err := s.EndEpic(ctx, integrateEpic, "ended_with_gaps", "Epic ended with 1 gap.", nil, endingEndedAt()); err != nil {
		t.Fatalf("EndEpic: %v", err)
	}

	if err := s.EndEpic(ctx, integrateEpic, "ended_with_gaps", "Epic ended with 2 gaps.", nil, endingEndedAt()); err != nil {
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

// TestEndEpicEndsTheRunsItNamesInTheSameWrite checks that EndEpic ends each running node run it names, passed with its
// output, in the write that ends the epic, and that a repeat in the phase the epic is already in still ends a run it
// names that is running.
func TestEndEpicEndsTheRunsItNamesInTheSameWrite(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	endingFreezeReview(t, s)
	startIntegrateRuns(t, s,
		NodeRun{Path: "conclude", ID: "id-conclude", Node: "conclude", Kind: "conclude", Attempt: 1},
		NodeRun{Path: "conclude-again", ID: "id-conclude-again", Node: "conclude-again", Kind: "conclude", Attempt: 1})

	ends := []NodeEnd{{Path: "conclude", Outcome: "passed", Output: "Epic ended clean."}}
	if err := s.EndEpic(ctx, integrateEpic, "clean", "Epic ended clean.", ends, endingEndedAt()); err != nil {
		t.Fatalf("EndEpic: %v", err)
	}
	if run := integrateRun(t, s, "conclude"); run.State != NodeEnded || run.Outcome != "passed" ||
		run.Output != "Epic ended clean." {
		t.Errorf("conclude run after EndEpic = %+v, want ended passed with the ending body", run)
	}
	if run := integrateRun(t, s, "conclude-again"); run.State != NodeRunning {
		t.Errorf("conclude-again run after EndEpic = %+v, want running: EndEpic named only conclude", run)
	}

	again := []NodeEnd{{Path: "conclude-again", Outcome: "passed", Output: "Epic ended clean."}}
	if err := s.EndEpic(ctx, integrateEpic, "clean", "Epic ended clean.", again, endingEndedAt()); err != nil {
		t.Fatalf("repeat EndEpic: %v", err)
	}
	if run := integrateRun(t, s, "conclude-again"); run.State != NodeEnded || run.Outcome != "passed" {
		t.Errorf("conclude-again run after a repeat in the same phase = %+v, want ended passed", run)
	}
	if got := integrateTells(t, s); len(got) != 0 {
		t.Errorf("tells after a repeat = %q, want none: a clean ending tells nothing", got)
	}
}
