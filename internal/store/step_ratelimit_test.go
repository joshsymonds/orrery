package store

import (
	"context"
	"testing"
	"time"
)

func TestStepRateLimitReadsRejectedRowAtOrAfterThePrompt(t *testing.T) {
	for _, tc := range []struct {
		name  string
		after time.Duration
	}{
		{name: "at the prompt's second", after: 0},
		{name: "after the prompt", after: time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			s := openSyncStore(t)
			rowAt := engineNowAt().Add(tc.after)

			if err := s.ApplyEngine(ctx, []EngineOp{
				EnginePrompt{ID: "eng-a", Text: "go"},
			}, engineNowAt()); err != nil {
				t.Fatalf("ApplyEngine prompt: %v", err)
			}
			if err := s.ApplyEngine(ctx, []EngineOp{
				EngineRateLimited{
					ID: "eng-a", Status: "rejected", Kind: "five_hour", ResetsAt: time.Unix(1_700_018_000, 700_000_000),
				},
			}, rowAt); err != nil {
				t.Fatalf("ApplyEngine rate limit: %v", err)
			}

			got, ok, err := s.StepRateLimit(ctx, "eng-a")
			if err != nil {
				t.Fatalf("StepRateLimit: %v", err)
			}
			if !ok {
				t.Fatal("StepRateLimit eng-a: found false, want true")
			}
			if got.Status != "rejected" || got.Kind != "five_hour" {
				t.Fatalf("StepRateLimit eng-a: got status %q kind %q, want rejected five_hour", got.Status, got.Kind)
			}
			if !got.ResetsAt.Equal(time.Unix(1_700_018_000, 0)) {
				t.Fatalf("StepRateLimit eng-a ResetsAt: got %v, want whole second %v",
					got.ResetsAt, time.Unix(1_700_018_000, 0))
			}
			if !got.At.Equal(rowAt) {
				t.Fatalf("StepRateLimit eng-a At: got %v, want %v", got.At, rowAt)
			}
		})
	}
}

func TestStepRateLimitBeforeTheNewestPromptReadsFalse(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)
	newest := engineNowAt()

	if err := s.ApplyEngine(ctx, []EngineOp{
		EnginePrompt{ID: "eng-a", Text: "first"},
	}, newest.Add(-time.Minute)); err != nil {
		t.Fatalf("ApplyEngine first prompt: %v", err)
	}
	if err := s.ApplyEngine(ctx, []EngineOp{
		EnginePrompt{ID: "eng-a", Text: "second"},
	}, newest); err != nil {
		t.Fatalf("ApplyEngine newest prompt: %v", err)
	}
	if err := s.ApplyEngine(ctx, []EngineOp{
		EngineRateLimited{ID: "eng-a", Status: "rejected", Kind: "five_hour", ResetsAt: time.Unix(1_700_018_000, 0)},
	}, newest.Add(-time.Second)); err != nil {
		t.Fatalf("ApplyEngine rate limit: %v", err)
	}

	got, ok, err := s.StepRateLimit(ctx, "eng-a")
	if err != nil {
		t.Fatalf("StepRateLimit: %v", err)
	}
	if ok {
		t.Fatalf("StepRateLimit eng-a: found true with %+v, want false", got)
	}
	if got != (RateLimit{}) {
		t.Fatalf("StepRateLimit eng-a: got %+v, want the zero value", got)
	}
}

func TestStepRateLimitOfNotRejectedRowReadsFalse(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)

	if err := s.ApplyEngine(ctx, []EngineOp{
		EnginePrompt{ID: "eng-a", Text: "go"},
	}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine prompt: %v", err)
	}
	if err := s.ApplyEngine(ctx, []EngineOp{
		EngineRateLimited{ID: "eng-a", Status: "allowed", Kind: "five_hour", ResetsAt: time.Unix(1_700_018_000, 0)},
	}, engineNowAt().Add(time.Minute)); err != nil {
		t.Fatalf("ApplyEngine rate limit: %v", err)
	}

	got, ok, err := s.StepRateLimit(ctx, "eng-a")
	if err != nil {
		t.Fatalf("StepRateLimit: %v", err)
	}
	if ok {
		t.Fatalf("StepRateLimit eng-a: found true with %+v, want false", got)
	}
	if got != (RateLimit{}) {
		t.Fatalf("StepRateLimit eng-a: got %+v, want the zero value", got)
	}
}

func TestStepRateLimitOfSessionWithNoRowReadsFalse(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)

	if err := s.ApplyEngine(ctx, []EngineOp{
		EnginePrompt{ID: "eng-a", Text: "go"},
	}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine prompt: %v", err)
	}

	got, ok, err := s.StepRateLimit(ctx, "eng-a")
	if err != nil {
		t.Fatalf("StepRateLimit: %v", err)
	}
	if ok {
		t.Fatalf("StepRateLimit eng-a: found true with %+v, want false", got)
	}
	if got != (RateLimit{}) {
		t.Fatalf("StepRateLimit eng-a: got %+v, want the zero value", got)
	}
}

func TestStepRateLimitOfSessionWithNoPromptReadsTrue(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)

	if err := s.ApplyEngine(ctx, []EngineOp{
		EngineRateLimited{ID: "eng-a", Status: "rejected", Kind: "seven_day", ResetsAt: time.Unix(1_700_600_000, 0)},
	}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine rate limit: %v", err)
	}

	got, ok, err := s.StepRateLimit(ctx, "eng-a")
	if err != nil {
		t.Fatalf("StepRateLimit: %v", err)
	}
	if !ok {
		t.Fatal("StepRateLimit eng-a: found false, want true")
	}
	if got.Status != "rejected" || got.Kind != "seven_day" {
		t.Fatalf("StepRateLimit eng-a: got status %q kind %q, want rejected seven_day", got.Status, got.Kind)
	}
	if !got.At.Equal(engineNowAt()) {
		t.Fatalf("StepRateLimit eng-a At: got %v, want %v", got.At, engineNowAt())
	}
}
