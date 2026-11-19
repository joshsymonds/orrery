package store

import (
	"context"
	"reflect"
	"testing"
)

// TestQuietRestartQuietSessionsListsOnlyUncleared seeds four sessions and checks QuietSessions lists the two whose
// newest engine_interrupt is newer than their newest owner prompt: one with an owner prompt before the interrupt, and
// one with only event prompts after it, so no owner prompt at all. It does not list the session whose owner prompt is
// newer than its interrupt, nor the step session that holds an interrupt.
func TestQuietRestartQuietSessionsListsOnlyUncleared(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openSyncStore(t)
	ops := []EngineOp{
		EngineAdd{ID: "interrupt-newer", Dir: "/a", State: "working"},
		EnginePrompt{ID: "interrupt-newer", Text: "first", PromptID: "p1", Sender: PromptSenderOwner, WriteID: "w1"},
		EngineInterrupt{ID: "interrupt-newer", Turning: true},
		EngineAdd{ID: "owner-newer", Dir: "/b", State: "working"},
		EngineInterrupt{ID: "owner-newer", Turning: true},
		EnginePrompt{ID: "owner-newer", Text: "after", PromptID: "p2", Sender: PromptSenderOwner, WriteID: "w2"},
		EngineAdd{ID: "event-only", Dir: "/c", State: "working"},
		EngineInterrupt{ID: "event-only", Turning: true},
		EnginePrompt{ID: "event-only", Text: "event", PromptID: "e1", Sender: PromptSenderEvent, WriteID: "w3"},
		EngineAdd{ID: "step", Dir: "/d", State: "working", Kind: KindStep},
		EngineInterrupt{ID: "step", Turning: true},
	}
	if err := s.ApplyEngine(ctx, ops, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}

	got, err := s.QuietSessions(ctx)
	if err != nil {
		t.Fatalf("QuietSessions: %v", err)
	}
	if want := []string{"event-only", "interrupt-newer"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("QuietSessions = %q, want %q", got, want)
	}
}
