package session

import (
	"testing"
	"time"
)

// rateLimitLine is a rate_limit_event line of a session, with the rate_limit_info body given.
func rateLimitLine(info string) []byte {
	return []byte(`{"type":"rate_limit_event","uuid":"00000000-0000-4000-8000-000000000002",` +
		`"session_id":"00000000-0000-4000-8000-000000000001","rate_limit_info":` + info + `}`)
}

// requireOneRateLimit fails the test unless events is exactly one RateLimit and returns it.
func requireOneRateLimit(t *testing.T, events []Event) RateLimit {
	t.Helper()
	if len(events) != 1 {
		t.Fatalf("events = %#v, want exactly one RateLimit", events)
	}
	limit, ok := events[0].(RateLimit)
	if !ok {
		t.Fatalf("event = %#v, want RateLimit", events[0])
	}
	return limit
}

func TestRejectedRateLimitEventYieldsRateLimitWithKindAndReset(t *testing.T) {
	t.Parallel()
	tr := newTranslator()
	line := rateLimitLine(`{"status":"rejected","resetsAt":1700000000.75,"rateLimitType":"five_hour"}`)
	limit := requireOneRateLimit(t, tr.translate(line))
	if limit.Status != "rejected" {
		t.Errorf("Status = %q, want rejected", limit.Status)
	}
	if limit.Kind != "five_hour" {
		t.Errorf("Kind = %q, want five_hour", limit.Kind)
	}
	want := time.Unix(1700000000, 0)
	if !limit.ResetsAt.Equal(want) {
		t.Errorf("ResetsAt = %v, want %v (fraction truncated)", limit.ResetsAt, want)
	}
}

func TestAllowedWarningRateLimitEventWithoutFieldsYieldsEmptyKindAndZeroReset(t *testing.T) {
	t.Parallel()
	tr := newTranslator()
	line := rateLimitLine(`{"status":"allowed_warning","utilization":0.5}`)
	limit := requireOneRateLimit(t, tr.translate(line))
	if limit.Status != "allowed_warning" {
		t.Errorf("Status = %q, want allowed_warning", limit.Status)
	}
	if limit.Kind != "" {
		t.Errorf("Kind = %q, want empty", limit.Kind)
	}
	if !limit.ResetsAt.IsZero() {
		t.Errorf("ResetsAt = %v, want zero time", limit.ResetsAt)
	}
}
