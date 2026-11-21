package planner

import (
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// onlyDecisionLine returns the one line DecisionLines renders for epic, and fails the test for any other count.
func onlyDecisionLine(t *testing.T, epic store.EpicRow) string {
	t.Helper()
	lines := DecisionLines(epic, 0)
	if len(lines) != 1 {
		t.Fatalf("DecisionLines renders %d lines, want 1: %q", len(lines), lines)
	}
	return lines[0]
}

// TestDecisionMakerPrompt checks that each epic decision's line names its maker: owner, conversation with its session,
// orreryd, or unknown for a decision stored before makers were kept. An answer names its choice before its maker, and a
// note after the last turn's seen point shows its maker in New notes.
func TestDecisionMakerPrompt(t *testing.T) {
	t.Parallel()
	t.Run("owner", func(t *testing.T) {
		t.Parallel()
		epic := store.EpicRow{Decisions: []store.EpicDecision{
			{Seq: 1, ID: "D1", Text: "keep the store", Maker: store.Maker{By: store.MakerOwner}},
		}}
		if got, want := onlyDecisionLine(t, epic), "- D1 (by owner): keep the store"; got != want {
			t.Fatalf("decision line = %q, want %q", got, want)
		}
	})
	t.Run("conversation_with_session", func(t *testing.T) {
		t.Parallel()
		epic := store.EpicRow{Decisions: []store.EpicDecision{
			{
				Seq: 1, ID: "D2", Text: "cache briefly",
				Maker: store.Maker{By: store.MakerConversation, Session: "sess-3"},
			},
		}}
		if got, want := onlyDecisionLine(t, epic), "- D2 (by conversation sess-3): cache briefly"; got != want {
			t.Fatalf("decision line = %q, want %q", got, want)
		}
	})
	t.Run("orreryd", func(t *testing.T) {
		t.Parallel()
		epic := store.EpicRow{Decisions: []store.EpicDecision{
			{Seq: 1, ID: "D3", Text: "step ended", Maker: store.Maker{By: store.MakerOrreryd}},
		}}
		if got, want := onlyDecisionLine(t, epic), "- D3 (by orreryd): step ended"; got != want {
			t.Fatalf("decision line = %q, want %q", got, want)
		}
	})
	t.Run("unknown", func(t *testing.T) {
		t.Parallel()
		epic := store.EpicRow{Decisions: []store.EpicDecision{
			{Seq: 1, ID: "D4", Text: "stored before makers"},
		}}
		if got, want := onlyDecisionLine(t, epic), "- D4 (by unknown): stored before makers"; got != want {
			t.Fatalf("decision line = %q, want %q", got, want)
		}
	})
	t.Run("answer_with_choice", func(t *testing.T) {
		t.Parallel()
		epic := store.EpicRow{
			Questions: []store.EpicQuestion{{Seq: 1, Text: "which store?", State: "answered", Choice: "sqlite"}},
			Decisions: []store.EpicDecision{{
				Seq: 2, ID: "ans-2", Text: "use sqlite", Question: 1,
				Maker: store.Maker{By: store.MakerConversation, Session: "sess-7"},
			}},
		}
		want := "- ans-2 (answers Q1 with sqlite, by conversation sess-7): use sqlite"
		if got := onlyDecisionLine(t, epic); got != want {
			t.Fatalf("decision line = %q, want %q", got, want)
		}
	})
	t.Run("new_note", func(t *testing.T) {
		t.Parallel()
		epic := store.EpicRow{
			ID: "epic-5",
			Decisions: []store.EpicDecision{
				{Seq: 1, ID: "D1", Text: "seed one", Maker: store.Maker{By: store.MakerOwner}},
				{Seq: 2, ID: "n2", Text: "keep the old schema", Maker: store.Maker{By: store.MakerOrreryd}},
			},
			Turns: []store.EpicTurn{
				{Seq: 1, State: "failed", Reason: "first failure", Seen: 1},
				{Seq: 2, State: "running"},
			},
		}
		prompt := promptOf(t, epic, 2, nil)
		want := "## New notes\n- n2 (by orreryd): keep the old schema\n"
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt lacks %q: a note after the seen point must name its maker in New notes; prompt is:\n%s",
				want, prompt)
		}
	})
}
