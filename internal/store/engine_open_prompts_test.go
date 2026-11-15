package store

import (
	"context"
	"testing"
)

func holderPromptsAfterTurnEnd(t *testing.T, s *Store, id string) int {
	t.Helper()
	n, err := s.HolderPromptsAfterTurnEnd(context.Background(), id)
	if err != nil {
		t.Fatalf("HolderPromptsAfterTurnEnd %s: %v", id, err)
	}
	return n
}

func TestHolderPromptsAfterTurnEndCountsWritesSinceLaunch(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)

	if err := s.ApplyEngine(ctx, []EngineOp{
		EngineAdd{ID: "e1", Dir: "/a", State: "idle"},
		EngineAdd{ID: "e2", Dir: "/b", State: "idle"},
		EnginePrompt{ID: "e1", Text: "before", WriteID: "w0"},
		EngineLaunch{ID: "e1"},
		EnginePrompt{ID: "e1", Text: "first", WriteID: "w1"},
		EnginePrompt{ID: "e2", Text: "other session", WriteID: "x1"},
		EnginePrompt{ID: "e1", Text: "no write id"},
		EnginePrompt{ID: "e1", Text: "second", WriteID: "w2"},
	}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}

	if got := holderPromptsAfterTurnEnd(t, s, "e1"); got != 2 {
		t.Fatalf("after launch with no turn end: got %d, want 2 (the written prompts after the launch)", got)
	}
}

func TestHolderPromptsAfterTurnEndCountsWritesAfterNewestTurnEnd(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)

	if err := s.ApplyEngine(ctx, []EngineOp{
		EngineAdd{ID: "e1", Dir: "/a", State: "idle"},
		EngineAdd{ID: "e2", Dir: "/b", State: "idle"},
		EngineLaunch{ID: "e1"},
		EnginePrompt{ID: "e1", Text: "first", WriteID: "w1"},
		EnginePrompt{ID: "e1", Text: "second", WriteID: "w2"},
	}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine launch: %v", err)
	}
	if got := holderPromptsAfterTurnEnd(t, s, "e1"); got != 2 {
		t.Fatalf("before any turn end: got %d, want 2", got)
	}

	steps := []struct {
		name string
		ops  []EngineOp
		want int
	}{
		{
			name: "turn end of the session leaves no prompt after it",
			ops:  []EngineOp{EngineTurnEnded{ID: "e1", Subtype: "success"}},
			want: 0,
		},
		{
			name: "written prompt after the turn end counts, one without a write id does not",
			ops: []EngineOp{
				EnginePrompt{ID: "e1", Text: "third", WriteID: "w3"},
				EnginePrompt{ID: "e1", Text: "no write id"},
			},
			want: 1,
		},
		{
			name: "another session's turn end does not move this session's newest turn end",
			ops:  []EngineOp{EngineTurnEnded{ID: "e2", Subtype: "success"}},
			want: 1,
		},
		{
			name: "a later written prompt counts beside the earlier one",
			ops:  []EngineOp{EnginePrompt{ID: "e1", Text: "fourth", WriteID: "w4"}},
			want: 2,
		},
		{
			name: "a second turn end leaves no prompt after it",
			ops:  []EngineOp{EngineTurnEnded{ID: "e1", Subtype: "success"}},
			want: 0,
		},
	}
	for _, step := range steps {
		if err := s.ApplyEngine(ctx, step.ops, engineNowAt()); err != nil {
			t.Fatalf("%s: ApplyEngine: %v", step.name, err)
		}
		if got := holderPromptsAfterTurnEnd(t, s, "e1"); got != step.want {
			t.Fatalf("%s: got %d, want %d", step.name, got, step.want)
		}
	}
}

func TestHolderPromptsAfterTurnEndUnknownIDCountsZero(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)

	if err := s.ApplyEngine(ctx, []EngineOp{
		EngineAdd{ID: "e1", Dir: "/a", State: "idle"},
		EngineLaunch{ID: "e1"},
		EnginePrompt{ID: "e1", Text: "first", WriteID: "w1"},
	}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}

	if got := holderPromptsAfterTurnEnd(t, s, "missing"); got != 0 {
		t.Fatalf("unknown id: got %d, want 0", got)
	}
}
