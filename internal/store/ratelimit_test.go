package store

import (
	"context"
	"testing"
	"time"
)

func TestEngineRateLimitedStoresTheRowAndRateLimitReadsItBack(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)

	if err := s.ApplyEngine(ctx, []EngineOp{
		EngineRateLimited{
			ID: "eng-a", Status: "rejected", Kind: "five_hour", ResetsAt: time.Unix(1_700_018_000, 700_000_000),
		},
	}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}

	got, ok, err := s.RateLimit(ctx, "eng-a")
	if err != nil {
		t.Fatalf("RateLimit: %v", err)
	}
	if !ok {
		t.Fatal("RateLimit eng-a: found false, want true")
	}
	if got.Status != "rejected" || got.Kind != "five_hour" {
		t.Fatalf("RateLimit eng-a: got status %q kind %q, want rejected five_hour", got.Status, got.Kind)
	}
	if !got.ResetsAt.Equal(time.Unix(1_700_018_000, 0)) {
		t.Fatalf("RateLimit eng-a ResetsAt: got %v, want whole second %v", got.ResetsAt, time.Unix(1_700_018_000, 0))
	}
	if !got.At.Equal(engineNowAt()) {
		t.Fatalf("RateLimit eng-a At: got %v, want %v", got.At, engineNowAt())
	}
}

func TestEngineRateLimitedSecondOpReplacesTheRow(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)
	later := engineNowAt().Add(time.Minute)

	if err := s.ApplyEngine(ctx, []EngineOp{
		EngineRateLimited{ID: "eng-a", Status: "rejected", Kind: "five_hour", ResetsAt: time.Unix(1_700_018_000, 0)},
	}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine first: %v", err)
	}
	if err := s.ApplyEngine(ctx, []EngineOp{
		EngineRateLimited{ID: "eng-a", Status: "rejected", Kind: "seven_day", ResetsAt: time.Unix(1_700_600_000, 0)},
	}, later); err != nil {
		t.Fatalf("ApplyEngine second: %v", err)
	}

	got, ok, err := s.RateLimit(ctx, "eng-a")
	if err != nil {
		t.Fatalf("RateLimit: %v", err)
	}
	if !ok {
		t.Fatal("RateLimit eng-a: found false, want true")
	}
	if got.Kind != "seven_day" {
		t.Fatalf("RateLimit eng-a kind: got %q, want seven_day", got.Kind)
	}
	if !got.ResetsAt.Equal(time.Unix(1_700_600_000, 0)) {
		t.Fatalf("RateLimit eng-a ResetsAt: got %v, want %v", got.ResetsAt, time.Unix(1_700_600_000, 0))
	}
	if !got.At.Equal(later) {
		t.Fatalf("RateLimit eng-a At: got %v, want %v", got.At, later)
	}
}

func TestRateLimitOfSessionWithNoneReadsFalse(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)

	if err := s.ApplyEngine(ctx, []EngineOp{
		EngineRateLimited{ID: "eng-a", Status: "rejected", Kind: "five_hour", ResetsAt: time.Unix(1_700_018_000, 0)},
	}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}

	got, ok, err := s.RateLimit(ctx, "eng-b")
	if err != nil {
		t.Fatalf("RateLimit eng-b: %v", err)
	}
	if ok {
		t.Fatalf("RateLimit eng-b: found true with %+v, want false", got)
	}
	if got != (RateLimit{}) {
		t.Fatalf("RateLimit eng-b: got %+v, want the zero value", got)
	}
}

func TestEngineRateLimitedAppendsNoEvent(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)

	if err := s.ApplyEngine(ctx, []EngineOp{
		EngineRateLimited{ID: "eng-a", Status: "rejected", Kind: "five_hour", ResetsAt: time.Unix(1_700_018_000, 0)},
	}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}

	if got := eventsOf(t, s); len(got) != 0 {
		t.Fatalf("events: got %d, want 0: %+v", len(got), got)
	}
}
