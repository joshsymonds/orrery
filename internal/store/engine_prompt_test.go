package store

import (
	"context"
	"testing"
)

func TestEnginePromptStoresPromptID(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)

	ops := []EngineOp{
		EngineAdd{ID: "e1", Dir: "/a", State: "idle"},
		EnginePrompt{ID: "e1", Text: "hi", PromptID: "p1"},
		EnginePrompt{ID: "e1", Text: "bye"},
	}
	if err := s.ApplyEngine(ctx, ops, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}

	events := eventsOf(t, s)
	if len(events) != 3 {
		t.Fatalf("events: got %d, want 3", len(events))
	}
	if got := payloadOf(t, events[1].Payload); got["text"] != "hi" || got["promptId"] != "p1" || len(got) != 2 {
		t.Fatalf("prompt with id payload: got %v, want text hi and promptId p1", got)
	}
	if got := events[2].Payload; got != `{"text":"bye"}` {
		t.Fatalf("prompt without id payload: got %s, want {\"text\":\"bye\"}", got)
	}
}

func TestEnginePromptSent(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)

	ops := []EngineOp{
		EngineAdd{ID: "e1", Dir: "/a", State: "idle"},
		EngineAdd{ID: "e2", Dir: "/b", State: "idle"},
		EnginePrompt{ID: "e1", Text: "hi", PromptID: "p1"},
		EnginePrompt{ID: "e1", Text: "no id"},
	}
	if err := s.ApplyEngine(ctx, ops, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}

	cases := []struct {
		name     string
		session  string
		promptID string
		want     bool
	}{
		{name: "sent prompt", session: "e1", promptID: "p1", want: true},
		{name: "other prompt id in same session", session: "e1", promptID: "p2", want: false},
		{name: "same prompt id in other session", session: "e2", promptID: "p1", want: false},
		{name: "empty prompt id", session: "e1", promptID: "", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := s.EnginePromptSent(ctx, tc.session, tc.promptID)
			if err != nil {
				t.Fatalf("EnginePromptSent(%s, %q): %v", tc.session, tc.promptID, err)
			}
			if got != tc.want {
				t.Fatalf("EnginePromptSent(%s, %q): got %v, want %v", tc.session, tc.promptID, got, tc.want)
			}
		})
	}
}

func TestEnginePromptFailureRecordsFailedPrompt(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)

	ops := []EngineOp{
		EngineAdd{ID: "s1", Dir: "/a", State: "idle"},
		EngineAdd{ID: "s2", Dir: "/b", State: "idle"},
		EnginePromptFailed{ID: "s1", PromptID: "p1", Reason: "send failed"},
	}
	if err := s.ApplyEngine(ctx, ops, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}

	if got := len(eventsOf(t, s)); got != 2 {
		t.Fatalf("events: got %d, want 2 (the two adds; the failure marker appends none)", got)
	}

	cases := []struct {
		name       string
		session    string
		promptID   string
		wantReason string
		wantFailed bool
	}{
		{name: "failed prompt", session: "s1", promptID: "p1", wantReason: "send failed", wantFailed: true},
		{name: "other prompt id in same session", session: "s1", promptID: "p2"},
		{name: "same prompt id in other session", session: "s2", promptID: "p1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reason, failed, err := s.EnginePromptFailure(ctx, tc.session, tc.promptID)
			if err != nil {
				t.Fatalf("EnginePromptFailure(%s, %q): %v", tc.session, tc.promptID, err)
			}
			if reason != tc.wantReason || failed != tc.wantFailed {
				t.Fatalf("EnginePromptFailure(%s, %q): got (%q, %v), want (%q, %v)",
					tc.session, tc.promptID, reason, failed, tc.wantReason, tc.wantFailed)
			}
		})
	}
}

func TestEnginePromptFailureKeepsFirstReason(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)

	ops := []EngineOp{
		EngineAdd{ID: "s1", Dir: "/a", State: "idle"},
		EnginePromptFailed{ID: "s1", PromptID: "p1", Reason: "send failed"},
		EnginePromptFailed{ID: "s1", PromptID: "p1", Reason: "timed out"},
	}
	if err := s.ApplyEngine(ctx, ops, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}

	reason, failed, err := s.EnginePromptFailure(ctx, "s1", "p1")
	if err != nil {
		t.Fatalf("EnginePromptFailure: %v", err)
	}
	if !failed || reason != "send failed" {
		t.Fatalf("EnginePromptFailure: got (%q, %v), want (%q, true)", reason, failed, "send failed")
	}
}
