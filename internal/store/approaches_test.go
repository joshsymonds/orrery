package store

import (
	"errors"
	"reflect"
	"testing"
)

// approachesDesign is the design the approach tests record their approaches and choices on.
const approachesDesign = "design-approaches"

// approachOf returns a synthetic approach of approachesDesign named name, recommended or not, stamped at
// designLogStart.
func approachOf(name string, recommended bool) DesignApproach {
	return DesignApproach{
		Design: approachesDesign, Name: name, Shape: "a scout beside the design", Cost: "one more step",
		Wins: "answers the question", Loses: "adds a step", Reconsider: "if the scout fails",
		Research: []string{"step-one", "step-two"}, Recommended: recommended, Reason: "Synthetic reason.",
		Session: "session-one", At: designLogStart,
	}
}

// TestRecordsApproachesStoresTwoWithOneRecommended checks that RecordApproach numbers each design's approaches from 1
// in the order they are recorded, that DesignApproaches and the Reader read every field of each back in seq order, and
// that the one approach recorded as recommended reads as the recommendation.
func TestRecordsApproachesStoresTwoWithOneRecommended(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, path := openDesignLogStore(t)
	first, err := s.RecordApproach(ctx, approachOf("Keep the scout", false))
	if err != nil {
		t.Fatalf("RecordApproach first: %v", err)
	}
	second, err := s.RecordApproach(ctx, approachOf("Drop the scout", true))
	if err != nil {
		t.Fatalf("RecordApproach second: %v", err)
	}
	if first.Seq != 1 || second.Seq != 2 {
		t.Fatalf("approach seqs = %d and %d, want 1 and 2", first.Seq, second.Seq)
	}

	want := []DesignApproach{first, second}
	got, err := s.DesignApproaches(ctx, approachesDesign)
	if err != nil {
		t.Fatalf("DesignApproaches: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DesignApproaches = %+v, want %+v", got, want)
	}
	if got[0].Recommended || !got[1].Recommended {
		t.Fatalf("recommended = %v and %v, want the second alone", got[0].Recommended, got[1].Recommended)
	}
	read, err := NewReader(path).DesignApproaches(ctx, approachesDesign)
	if err != nil {
		t.Fatalf("Reader.DesignApproaches: %v", err)
	}
	if !reflect.DeepEqual(read, want) {
		t.Fatalf("Reader.DesignApproaches = %+v, want %+v", read, want)
	}
	none, err := s.DesignApproaches(ctx, "design-none")
	if err != nil || len(none) != 0 {
		t.Fatalf("DesignApproaches of a design with none = %+v, %v, want none", none, err)
	}
}

// TestRecordsApproachesNewestRecommendationWins checks that when more than one approach is recorded as recommended, the
// newest of them is the recommendation, and the others read as not recommended, even one recorded after it.
func TestRecordsApproachesNewestRecommendationWins(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, _ := openDesignLogStore(t)
	for _, approach := range []DesignApproach{
		approachOf("Keep the scout", true),
		approachOf("Drop the scout", true),
		approachOf("Rewrite the scout", false),
	} {
		if _, err := s.RecordApproach(ctx, approach); err != nil {
			t.Fatalf("RecordApproach %s: %v", approach.Name, err)
		}
	}
	got, err := s.DesignApproaches(ctx, approachesDesign)
	if err != nil {
		t.Fatalf("DesignApproaches: %v", err)
	}
	recommended := []bool{got[0].Recommended, got[1].Recommended, got[2].Recommended}
	if !reflect.DeepEqual(recommended, []bool{false, true, false}) {
		t.Fatalf("recommended = %v, want only the newest recommended approach, seq 2", recommended)
	}
}

// TestRecordsApproachesChoicesReadNewest checks that ChooseApproach numbers each design's choices from 1 and appends
// them, that DesignApproachChoice reads the newest one with its maker and its evidence through the Store and the
// Reader, and that a design with no choice is ErrNoApproachChoice.
func TestRecordsApproachesChoicesReadNewest(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, path := openDesignLogStore(t)
	if _, err := s.DesignApproachChoice(ctx, approachesDesign); !errors.Is(err, ErrNoApproachChoice) {
		t.Fatalf("DesignApproachChoice before any choice = %v, want ErrNoApproachChoice", err)
	}
	if _, err := NewReader(path).DesignApproachChoice(ctx, approachesDesign); !errors.Is(err, ErrNoApproachChoice) {
		t.Fatalf("Reader.DesignApproachChoice before any choice = %v, want ErrNoApproachChoice", err)
	}

	byOwner, err := s.ChooseApproach(ctx, ApproachChoice{
		Design: approachesDesign, Approach: 1, Words: "Keep it.", Maker: StageMakerOwnerConversation,
		Quote: "Keep the scout.", Session: "session-one", At: designLogStart,
	})
	if err != nil {
		t.Fatalf("ChooseApproach by owner_conversation: %v", err)
	}
	byConversation, err := s.ChooseApproach(ctx, ApproachChoice{
		Design: approachesDesign, Approach: 2, Words: "Drop it.", Maker: StageMakerConversation,
		MakerReason: "The owner delegated the pick.", Delegation: "Pick the one that answers fast.",
		Session: "session-two", At: designLogStart + 5,
	})
	if err != nil {
		t.Fatalf("ChooseApproach by conversation: %v", err)
	}
	if byOwner.Seq != 1 || byConversation.Seq != 2 {
		t.Fatalf("choice seqs = %d and %d, want 1 and 2", byOwner.Seq, byConversation.Seq)
	}

	got, err := s.DesignApproachChoice(ctx, approachesDesign)
	if err != nil {
		t.Fatalf("DesignApproachChoice: %v", err)
	}
	if !reflect.DeepEqual(got, byConversation) {
		t.Fatalf("DesignApproachChoice = %+v, want the newest choice %+v", got, byConversation)
	}
	read, err := NewReader(path).DesignApproachChoice(ctx, approachesDesign)
	if err != nil {
		t.Fatalf("Reader.DesignApproachChoice: %v", err)
	}
	if !reflect.DeepEqual(read, byConversation) {
		t.Fatalf("Reader.DesignApproachChoice = %+v, want %+v", read, byConversation)
	}
	rows, err := s.fixtureQueries().ListDesignApproachChoices(ctx, approachesDesign)
	if err != nil || len(rows) != 2 {
		t.Fatalf("stored choices = %d rows, %v, want both kept", len(rows), err)
	}
}
