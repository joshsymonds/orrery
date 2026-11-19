package store

import (
	"context"
	"testing"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

func holderPromptsUnanswered(t *testing.T, s *Store, id string) int {
	t.Helper()
	n, err := s.HolderPromptsUnanswered(context.Background(), id)
	if err != nil {
		t.Fatalf("HolderPromptsUnanswered %s: %v", id, err)
	}
	return n
}

func TestHolderPromptsUnansweredCountsWritesSinceLaunch(t *testing.T) {
	t.Parallel()
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

	if got := holderPromptsUnanswered(t, s, "e1"); got != 2 {
		t.Fatalf("after launch with no turn end: got %d, want 2 (the written prompts after the launch)", got)
	}
}

// TestHolderPromptsUnansweredCountsPromptsLaterTurnEndsOmit checks each prompt against the turn ends after it: a prompt
// a later turn end lists is answered, and a prompt every later turn end omits is owed, even when a turn end sits after it.
func TestHolderPromptsUnansweredCountsPromptsLaterTurnEndsOmit(t *testing.T) {
	t.Parallel()
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
	if got := holderPromptsUnanswered(t, s, "e1"); got != 2 {
		t.Fatalf("before any turn end: got %d, want 2", got)
	}

	steps := []struct {
		name string
		ops  []EngineOp
		want int
	}{
		{
			name: "a turn end listing only the first prompt leaves the second owed",
			ops:  []EngineOp{EngineTurnEnded{ID: "e1", Subtype: "success", Answered: []string{"w1"}}},
			want: 1,
		},
		{
			name: "a written prompt after the turn end is owed, one without a write id is not",
			ops: []EngineOp{
				EnginePrompt{ID: "e1", Text: "third", WriteID: "w3"},
				EnginePrompt{ID: "e1", Text: "no write id"},
			},
			want: 2,
		},
		{
			name: "another session's turn end does not answer this session's prompts",
			ops:  []EngineOp{EngineTurnEnded{ID: "e2", Subtype: "success", Answered: []string{"w2", "w3"}}},
			want: 2,
		},
		{
			name: "a later turn end that lists the second prompt leaves the third, which it omits, owed",
			ops:  []EngineOp{EngineTurnEnded{ID: "e1", Subtype: "success", Answered: []string{"w2"}}},
			want: 1,
		},
		{
			name: "a later written prompt is owed beside the omitted one",
			ops:  []EngineOp{EnginePrompt{ID: "e1", Text: "fourth", WriteID: "w4"}},
			want: 2,
		},
		{
			name: "a turn end that lists every owed prompt leaves none owed",
			ops:  []EngineOp{EngineTurnEnded{ID: "e1", Subtype: "success", Answered: []string{"w3", "w4"}}},
			want: 0,
		},
	}
	for _, step := range steps {
		if err := s.ApplyEngine(ctx, step.ops, engineNowAt()); err != nil {
			t.Fatalf("%s: ApplyEngine: %v", step.name, err)
		}
		if got := holderPromptsUnanswered(t, s, "e1"); got != step.want {
			t.Fatalf("%s: got %d, want %d", step.name, got, step.want)
		}
	}
}

// TestHolderPromptsUnansweredNullTurnEndAnswersEarlierPrompts stores a turn end with a NULL answered column, as one
// stored before the column existed. It answers every prompt before it, so only the prompt after it, which the later turn
// end omits, is owed.
func TestHolderPromptsUnansweredNullTurnEndAnswersEarlierPrompts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openSyncStore(t)

	if err := s.ApplyEngine(ctx, []EngineOp{
		EngineAdd{ID: "e1", Dir: "/a", State: "idle"},
		EngineLaunch{ID: "e1"},
		EnginePrompt{ID: "e1", Text: "first", WriteID: "w1"},
		EnginePrompt{ID: "e1", Text: "second", WriteID: "w2"},
	}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}

	payload, err := encodeEnginePayload(EventEngineTurnEnded, engineTurnEndedPayload{Subtype: "success"})
	if err != nil {
		t.Fatalf("encode turn end payload: %v", err)
	}
	if err := s.fixtureQueries().InsertEngineTurnEndedEvent(ctx, sqlcgen.InsertEngineTurnEndedEventParams{
		SessionID: "e1", At: engineNowAt().Unix(), Payload: payload,
	}); err != nil {
		t.Fatalf("insert turn end with NULL answered: %v", err)
	}
	if got := holderPromptsUnanswered(t, s, "e1"); got != 0 {
		t.Fatalf("after a NULL turn end: got %d, want 0 (it answers w1 and w2)", got)
	}

	if err := s.ApplyEngine(ctx, []EngineOp{
		EnginePrompt{ID: "e1", Text: "third", WriteID: "w3"},
		EngineTurnEnded{ID: "e1", Subtype: "success"},
	}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}
	if got := holderPromptsUnanswered(t, s, "e1"); got != 1 {
		t.Fatalf("after a turn end that lists nothing: got %d, want 1 (w3 is omitted, w1 and w2 stay answered)", got)
	}
}

func TestHolderPromptsUnansweredUnknownIDCountsZero(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openSyncStore(t)

	if err := s.ApplyEngine(ctx, []EngineOp{
		EngineAdd{ID: "e1", Dir: "/a", State: "idle"},
		EngineLaunch{ID: "e1"},
		EnginePrompt{ID: "e1", Text: "first", WriteID: "w1"},
	}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}

	if got := holderPromptsUnanswered(t, s, "missing"); got != 0 {
		t.Fatalf("unknown id: got %d, want 0", got)
	}
}
