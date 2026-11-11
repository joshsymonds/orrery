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
