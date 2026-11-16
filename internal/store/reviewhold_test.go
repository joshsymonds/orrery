package store

import (
	"context"
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// reviewHoldStore returns a store whose integrateEpic has its review frozen at rev-7 by a running review run, so
// EpicReview reads the holds of that review.
func reviewHoldStore(t *testing.T) *Store {
	t.Helper()
	s := openIntegrateStore(t)
	startIntegrateRuns(t, s, NodeRun{Path: "review", ID: "id-review", Node: "review", Kind: "review", Attempt: 1})
	if err := s.FreezeReview(
		context.Background(),
		integrateEpic,
		"review",
		1,
		"rev-7",
		integrateEndedAt(),
	); err != nil {
		t.Fatalf("FreezeReview of epic %s: %v", integrateEpic, err)
	}
	return s
}

// requireReviewHoldPaused fails unless integrateEpic is paused as wantPaused and, when it is paused, its pause source
// is the owner's.
func requireReviewHoldPaused(t *testing.T, s *Store, wantPaused bool) {
	t.Helper()
	if got := integrateEpicRow(t, s).Paused; got != wantPaused {
		t.Errorf("epic %s paused = %t, want %t", integrateEpic, got, wantPaused)
	}
	if !wantPaused {
		return
	}
	head, err := sqlcgen.New(s.db).GetEpic(context.Background(), integrateEpic)
	if err != nil {
		t.Fatalf("GetEpic %s: %v", integrateEpic, err)
	}
	if head.PauseSource != pauseOwner {
		t.Errorf("pause source of epic %s = %q, want %q", integrateEpic, head.PauseSource, pauseOwner)
	}
}

// requireReviewHoldTells fails unless the tells of integrateEpic are want, in seq order.
func requireReviewHoldTells(t *testing.T, s *Store, want []string) {
	t.Helper()
	if got := integrateTells(t, s); !reflect.DeepEqual(got, want) {
		t.Errorf("tells of epic %s = %q, want %q", integrateEpic, got, want)
	}
}

// TestHoldReviewStepStoresHoldPausesAsOwnerAndTellsOnce checks that a stopped review step of an unpaused epic is held
// in the review, the epic is paused as its owner's, and the reason is added as one tell.
func TestHoldReviewStepStoresHoldPausesAsOwnerAndTellsOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := reviewHoldStore(t)

	if err := s.HoldReviewStep(ctx, integrateEpic, "id-review", "review stopped", integrateParkedAt()); err != nil {
		t.Fatalf("HoldReviewStep: %v", err)
	}
	requireReview(t, s, Review{Frozen: "rev-7", Holds: []string{"id-review"}})
	requireReviewHoldPaused(t, s, true)
	requireReviewHoldTells(t, s, []string{"review stopped"})
}

// TestHoldReviewStepRepeatChangesNothing checks that a repeat of the hold of a step already held adds no tell and no
// second hold, and that after the epic is resumed the repeat does not pause it again.
func TestHoldReviewStepRepeatChangesNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := reviewHoldStore(t)
	if err := s.HoldReviewStep(ctx, integrateEpic, "id-review", "review stopped", integrateParkedAt()); err != nil {
		t.Fatalf("HoldReviewStep: %v", err)
	}

	if err := s.HoldReviewStep(ctx, integrateEpic, "id-review", "review stopped again",
		integrateEndedAt()); err != nil {
		t.Fatalf("repeat HoldReviewStep while paused: %v", err)
	}
	requireReview(t, s, Review{Frozen: "rev-7", Holds: []string{"id-review"}})
	requireReviewHoldPaused(t, s, true)
	requireReviewHoldTells(t, s, []string{"review stopped"})

	if err := s.ResumeEpic(ctx, integrateEpic); err != nil {
		t.Fatalf("ResumeEpic: %v", err)
	}
	if err := s.HoldReviewStep(ctx, integrateEpic, "id-review", "review stopped again",
		integrateEndedAt()); err != nil {
		t.Fatalf("repeat HoldReviewStep after ResumeEpic: %v", err)
	}
	requireReview(t, s, Review{Frozen: "rev-7", Holds: []string{"id-review"}})
	requireReviewHoldPaused(t, s, false)
	requireReviewHoldTells(t, s, []string{"review stopped"})
}

// TestHoldReviewStepSecondStepPausesOnce checks that a second stopped review step pauses the epic once more, as its
// owner's, with its reason as a tell, after the first step's pause was resumed.
func TestHoldReviewStepSecondStepPausesOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := reviewHoldStore(t)
	if err := s.HoldReviewStep(ctx, integrateEpic, "id-review", "review stopped", integrateParkedAt()); err != nil {
		t.Fatalf("HoldReviewStep: %v", err)
	}
	if err := s.ResumeEpic(ctx, integrateEpic); err != nil {
		t.Fatalf("ResumeEpic: %v", err)
	}

	if err := s.HoldReviewStep(ctx, integrateEpic, "id-review-2", "second stopped", integrateEndedAt()); err != nil {
		t.Fatalf("HoldReviewStep of a second step: %v", err)
	}
	requireReview(t, s, Review{Frozen: "rev-7", Holds: []string{"id-review", "id-review-2"}})
	requireReviewHoldPaused(t, s, true)
	requireReviewHoldTells(t, s, []string{"review stopped", "second stopped"})
}
