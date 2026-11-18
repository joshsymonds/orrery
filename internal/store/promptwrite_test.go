package store

import (
	"context"
	"testing"
)

// TestWithdrawQueuedFindsAStoredOwnerPrompt stores an owner prompt and a prompt with no prompt id, then reads the owner
// prompt back by its prompt id: PromptWrite returns its write id, text and sender, found.
func TestWithdrawQueuedFindsAStoredOwnerPrompt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openSyncStore(t)

	if err := s.ApplyEngine(ctx, []EngineOp{
		EngineAdd{ID: "e1", Dir: "/a", State: "idle"},
		EnginePrompt{ID: "e1", Text: "hold this", PromptID: "p1", Sender: PromptSenderOwner, WriteID: "w1"},
		EnginePrompt{ID: "e1", Text: "bare", WriteID: "w2"},
	}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}

	got, found, err := s.PromptWrite(ctx, "e1", "p1")
	if err != nil {
		t.Fatalf("PromptWrite: %v", err)
	}
	want := PromptWrite{WriteID: "w1", Text: "hold this", Sender: PromptSenderOwner}
	if !found || got != want {
		t.Fatalf("PromptWrite(p1) = %+v, found %t, want %+v found", got, found, want)
	}
}

// TestWithdrawQueuedUnknownPromptIsNotFound reads, from a session that holds one prompt, a prompt id no prompt of that
// session carries, and the prompt id of another session's prompt: both are not found, with no error.
func TestWithdrawQueuedUnknownPromptIsNotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openSyncStore(t)

	if err := s.ApplyEngine(ctx, []EngineOp{
		EngineAdd{ID: "e1", Dir: "/a", State: "idle"},
		EngineAdd{ID: "e2", Dir: "/b", State: "idle"},
		EnginePrompt{ID: "e1", Text: "held", PromptID: "p1", Sender: PromptSenderOwner, WriteID: "w1"},
		EnginePrompt{ID: "e2", Text: "elsewhere", PromptID: "p9", Sender: PromptSenderOwner, WriteID: "x1"},
	}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}

	for _, promptID := range []string{"no-such-prompt", "p9"} {
		got, found, err := s.PromptWrite(ctx, "e1", promptID)
		if err != nil {
			t.Fatalf("PromptWrite(e1, %s): %v", promptID, err)
		}
		if found {
			t.Fatalf("PromptWrite(e1, %s) = %+v, want not found", promptID, got)
		}
	}
}
