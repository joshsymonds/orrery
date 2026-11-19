package server

import (
	"context"
	"slices"
	"testing"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// countingFacts is a stepFactsReader that counts its calls and keeps the ids of the newest one. It has no step rows.
type countingFacts struct {
	calls int
	ids   []string
}

// StepFacts counts the call and keeps its ids.
func (f *countingFacts) StepFacts(_ context.Context, ids []string) (map[string]store.StepFacts, error) {
	f.calls++
	f.ids = ids
	return map[string]store.StepFacts{}, nil
}

// TestStepEndFactsOneReadPerPage gives withStepFacts a page of two step-end items naming step a, one naming step b, and
// other items. It asks the store once, for a and b, each once. A page with no step-end item asks it nothing.
func TestStepEndFactsOneReadPerPage(t *testing.T) {
	t.Parallel()
	page := []*orreryv1.ConversationItem{
		{Kind: "reply", Text: "reply"},
		{Kind: "prompt", PromptId: "step:a"},
		{Kind: "prompt", PromptId: "p-owner"},
		{Kind: "prompt", PromptId: "step:a"},
		{Kind: "prompt", PromptId: "step:b"},
	}
	reader := &countingFacts{}
	if err := withStepFacts(t.Context(), reader, page); err != nil {
		t.Fatalf("withStepFacts: %v", err)
	}
	if reader.calls != 1 || !slices.Equal(reader.ids, []string{"a", "b"}) {
		t.Fatalf("StepFacts calls = %d with ids %v, want 1 call with a and b", reader.calls, reader.ids)
	}

	none := &countingFacts{}
	noStep := []*orreryv1.ConversationItem{{Kind: "reply", Text: "reply"}, {Kind: "prompt", PromptId: "p-owner"}}
	if err := withStepFacts(t.Context(), none, noStep); err != nil {
		t.Fatalf("withStepFacts without step ends: %v", err)
	}
	if none.calls != 0 {
		t.Fatalf("StepFacts calls = %d for a page with no step end, want none", none.calls)
	}
}
