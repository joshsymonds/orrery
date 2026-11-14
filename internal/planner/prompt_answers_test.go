package planner

import (
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// TestPromptTiesAnswerToItsQuestionAndListsOnlyNotesAsNewNotes gives an epic a note and an answer to Q1, both recorded
// after turn 1 saw decision 1. The answer renders under Decisions tied to Q1 and is absent from New notes, which lists
// only the note; the note renders as `- id: text`.
func TestPromptTiesAnswerToItsQuestionAndListsOnlyNotesAsNewNotes(t *testing.T) {
	t.Parallel()
	epic := store.EpicRow{
		ID: "epic-4",
		Decisions: []store.EpicDecision{
			{Seq: 1, ID: "D1", Text: "seed one"},
			{Seq: 2, ID: "note-2", Text: "keep the old schema"},
			{Seq: 3, ID: "ans-3", Text: "use the sqlite store", Question: 1},
		},
		Turns: []store.EpicTurn{
			{Seq: 1, State: "failed", Reason: "first failure", Seen: 1},
			{Seq: 2, State: "running"},
		},
		Questions: []store.EpicQuestion{{Seq: 1, Turn: 1, Text: "which store?", State: "answered"}},
	}
	prompt := Prompt(epic, 2, nil)
	inOrder(t, prompt,
		"## Decisions",
		"- D1: seed one",
		"- note-2: keep the old schema",
		"- ans-3 (answers Q1): use the sqlite store",
		"## New notes",
	)
	want := "## New notes\n- note-2: keep the old schema\n\n## Open questions\nNone.\n"
	if !strings.Contains(prompt, want) {
		t.Fatalf("prompt lacks %q: New notes must list only notes, not answers; prompt is:\n%s", want, prompt)
	}
}
