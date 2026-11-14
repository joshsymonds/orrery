package store

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestAttentionNeverPairsANewLatestTurnWithOpenQuestionsItLacks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	const id = claimEpic
	createEpic(t, s, fullEpic(id))
	addPendingTrigger(t, s)
	if _, err := s.ClaimTurn(ctx, id, claimedAt()); err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	if err := s.CompleteTurn(ctx, id, 1, plannedTurn(), completedAt()); err != nil {
		t.Fatalf("CompleteTurn of turn 1: %v", err)
	}
	insertPendingTrigger(t, s, id, "t3", "later trigger")
	if _, err := s.ClaimTurn(ctx, id, claimedAt().Add(time.Minute)); err != nil {
		t.Fatalf("second ClaimTurn: %v", err)
	}

	later := EpicPlan{Questions: []string{"Is the second turn right?", "Which module?"}, Outcome: "wait"}
	fired := false
	got, err := s.attention(ctx, func(epic string) {
		if epic != id || fired {
			return
		}
		fired = true
		if err := s.CompleteTurn(ctx, id, 2, later, completedAt().Add(time.Minute)); err != nil {
			t.Fatalf("CompleteTurn of turn 2 between the reads: %v", err)
		}
	})
	if err != nil {
		t.Fatalf("attention: %v", err)
	}
	if !fired {
		t.Fatalf("the hook never ran for %s, so the write between the reads did not happen", id)
	}
	if len(got) != 1 {
		t.Fatalf("attention = %+v, want one entry for %s", got, id)
	}

	allOpen := []EpicQuestion{
		{Seq: 1, Turn: 1, Text: "Keep the old tasks?", State: "open"},
		{Seq: 2, Turn: 2, Text: "Is the second turn right?", State: "open"},
		{Seq: 3, Turn: 2, Text: "Which module?", State: "open"},
	}
	entry := got[0]
	if entry.Latest.Seq != 1 && !reflect.DeepEqual(entry.Open, allOpen) {
		t.Errorf("Latest is turn %d asking %d questions but Open = %+v, want turn 1 as Latest or Open = %+v",
			entry.Latest.Seq, entry.LatestAsked, entry.Open, allOpen)
	}

	next, err := s.Attention(ctx)
	if err != nil {
		t.Fatalf("next Attention: %v", err)
	}
	if len(next) != 1 {
		t.Fatalf("next Attention = %+v, want one entry for %s", next, id)
	}
	if next[0].Latest.Seq != 2 || next[0].LatestAsked != 2 {
		t.Errorf("next pass Latest = turn %d asking %d, want turn 2 asking 2", next[0].Latest.Seq, next[0].LatestAsked)
	}
}
