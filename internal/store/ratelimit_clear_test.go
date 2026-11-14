package store

import (
	"context"
	"testing"
	"time"
)

func TestEngineRateLimitClearedMakesRateLimitAndStepRateLimitReadFalse(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)

	if err := s.ApplyEngine(ctx, []EngineOp{
		EnginePrompt{ID: "eng-a", Text: "go"},
	}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine prompt: %v", err)
	}
	if err := s.ApplyEngine(ctx, []EngineOp{
		EngineRateLimited{ID: "eng-a", Status: "rejected", Kind: "five_hour", ResetsAt: time.Unix(1_700_018_000, 0)},
	}, engineNowAt().Add(time.Minute)); err != nil {
		t.Fatalf("ApplyEngine rate limit: %v", err)
	}
	if _, ok, err := s.StepRateLimit(ctx, "eng-a"); err != nil || !ok {
		t.Fatalf("StepRateLimit eng-a before clear: found %v, err %v; want true before the clear", ok, err)
	}

	if err := s.ApplyEngine(ctx, []EngineOp{
		EngineRateLimitCleared{ID: "eng-a"},
	}, engineNowAt().Add(2*time.Minute)); err != nil {
		t.Fatalf("ApplyEngine clear: %v", err)
	}

	got, ok, err := s.RateLimit(ctx, "eng-a")
	if err != nil {
		t.Fatalf("RateLimit eng-a: %v", err)
	}
	if ok {
		t.Fatalf("RateLimit eng-a: found true with %+v after the clear, want false", got)
	}
	if got != (RateLimit{}) {
		t.Fatalf("RateLimit eng-a: got %+v after the clear, want the zero value", got)
	}

	stepGot, stepOK, err := s.StepRateLimit(ctx, "eng-a")
	if err != nil {
		t.Fatalf("StepRateLimit eng-a: %v", err)
	}
	if stepOK {
		t.Fatalf("StepRateLimit eng-a: found true with %+v after the clear, want false", stepGot)
	}
	if stepGot != (RateLimit{}) {
		t.Fatalf("StepRateLimit eng-a: got %+v after the clear, want the zero value", stepGot)
	}
}

func TestEngineRateLimitClearedLeavesAnotherSessionsRowUntouched(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)

	if err := s.ApplyEngine(ctx, []EngineOp{
		EngineRateLimited{ID: "eng-a", Status: "rejected", Kind: "five_hour", ResetsAt: time.Unix(1_700_018_000, 0)},
		EngineRateLimited{ID: "eng-b", Status: "rejected", Kind: "seven_day", ResetsAt: time.Unix(1_700_600_000, 0)},
	}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}

	if err := s.ApplyEngine(ctx, []EngineOp{
		EngineRateLimitCleared{ID: "eng-a"},
	}, engineNowAt().Add(time.Minute)); err != nil {
		t.Fatalf("ApplyEngine clear: %v", err)
	}

	got, ok, err := s.RateLimit(ctx, "eng-b")
	if err != nil {
		t.Fatalf("RateLimit eng-b: %v", err)
	}
	if !ok {
		t.Fatal("RateLimit eng-b: found false after clearing eng-a, want true")
	}
	if got.Status != "rejected" || got.Kind != "seven_day" {
		t.Fatalf("RateLimit eng-b: got status %q kind %q, want rejected seven_day", got.Status, got.Kind)
	}
	if !got.ResetsAt.Equal(time.Unix(1_700_600_000, 0)) {
		t.Fatalf("RateLimit eng-b ResetsAt: got %v, want %v", got.ResetsAt, time.Unix(1_700_600_000, 0))
	}
	if !got.At.Equal(engineNowAt()) {
		t.Fatalf("RateLimit eng-b At: got %v, want %v", got.At, engineNowAt())
	}
}

func TestEngineRateLimitClearedOfSessionWithNoRowIsNotAnError(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)

	if err := s.ApplyEngine(ctx, []EngineOp{
		EngineRateLimitCleared{ID: "eng-a"},
	}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine clear of session with no row: %v", err)
	}

	got, ok, err := s.RateLimit(ctx, "eng-a")
	if err != nil {
		t.Fatalf("RateLimit eng-a: %v", err)
	}
	if ok {
		t.Fatalf("RateLimit eng-a: found true with %+v, want false", got)
	}
}

func TestEngineRateLimitedAfterClearStoresANewRow(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)

	if err := s.ApplyEngine(ctx, []EngineOp{
		EngineRateLimited{ID: "eng-a", Status: "rejected", Kind: "five_hour", ResetsAt: time.Unix(1_700_018_000, 0)},
		EngineRateLimitCleared{ID: "eng-a"},
	}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine limit then clear: %v", err)
	}

	later := engineNowAt().Add(time.Hour)
	if err := s.ApplyEngine(ctx, []EngineOp{
		EngineRateLimited{ID: "eng-a", Status: "rejected", Kind: "seven_day", ResetsAt: time.Unix(1_700_600_000, 0)},
	}, later); err != nil {
		t.Fatalf("ApplyEngine limit after clear: %v", err)
	}

	got, ok, err := s.RateLimit(ctx, "eng-a")
	if err != nil {
		t.Fatalf("RateLimit eng-a: %v", err)
	}
	if !ok {
		t.Fatal("RateLimit eng-a: found false after a new limit, want true")
	}
	if got.Status != "rejected" || got.Kind != "seven_day" {
		t.Fatalf("RateLimit eng-a: got status %q kind %q, want rejected seven_day", got.Status, got.Kind)
	}
	if !got.ResetsAt.Equal(time.Unix(1_700_600_000, 0)) {
		t.Fatalf("RateLimit eng-a ResetsAt: got %v, want %v", got.ResetsAt, time.Unix(1_700_600_000, 0))
	}
	if !got.At.Equal(later) {
		t.Fatalf("RateLimit eng-a At: got %v, want %v", got.At, later)
	}
}

func TestEngineRateLimitClearedAppendsNoEvent(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)

	if err := s.ApplyEngine(ctx, []EngineOp{
		EngineRateLimited{ID: "eng-a", Status: "rejected", Kind: "five_hour", ResetsAt: time.Unix(1_700_018_000, 0)},
		EngineRateLimitCleared{ID: "eng-a"},
	}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}

	if got := eventsOf(t, s); len(got) != 0 {
		t.Fatalf("events: got %d, want 0: %+v", len(got), got)
	}
}
