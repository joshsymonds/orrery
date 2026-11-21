package planner

import (
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// summaryDecisionsEpic is an epic with seed decisions D1 and D2 by owner, answer ans-3 to Q1 with sqlite, which has a
// second sentence, and note n-4 by orreryd, with turns as its turns.
func summaryDecisionsEpic(turns ...store.EpicTurn) store.EpicRow {
	return store.EpicRow{
		ID: "epic-6",
		Decisions: []store.EpicDecision{
			{Seq: 1, ID: "D1", Text: "seed one", Maker: store.Maker{By: store.MakerOwner}},
			{Seq: 2, ID: "D2", Text: "seed two", Maker: store.Maker{By: store.MakerOwner}},
			{
				Seq: 3, ID: "ans-3", Text: "use the sqlite store. Keep the cache.", Question: 1,
				Maker: store.Maker{By: store.MakerConversation, Session: "sess-9"},
			},
			{Seq: 4, ID: "n-4", Text: "keep the old schema", Maker: store.Maker{By: store.MakerOrreryd}},
		},
		Questions: []store.EpicQuestion{{Seq: 1, Turn: 1, Text: "which store?", State: "answered", Choice: "sqlite"}},
		Turns:     turns,
	}
}

// summaryDecisionsEpicLines are the lines of the epic decisions of summaryDecisionsEpic, each rendered in full.
var summaryDecisionsEpicLines = []string{
	"- D1 (by owner): seed one",
	"- D2 (by owner): seed two",
	"- ans-3 (answers Q1 with sqlite, by conversation sess-9): use the sqlite store. Keep the cache.",
	"- n-4 (by orreryd): keep the old schema",
}

// summaryDecisionsSection returns the lines of the Decisions section of prompt, and fails the test when it has none.
func summaryDecisionsSection(t *testing.T, prompt string) []string {
	t.Helper()
	_, rest, found := strings.Cut(prompt, "\n## Decisions\n")
	if !found {
		t.Fatalf("prompt has no Decisions section:\n%s", prompt)
	}
	section, _, _ := strings.Cut(rest, "\n## ")
	return strings.Split(strings.TrimSuffix(section, "\n"), "\n")
}

// assertSummaryDecisions fails the test unless the Decisions section of prompt has exactly the lines want.
func assertSummaryDecisions(t *testing.T, prompt string, want []string) {
	t.Helper()
	if got := summaryDecisionsSection(t, prompt); !slices.Equal(got, want) {
		t.Fatalf("Decisions section = %q, want %q", got, want)
	}
}

// TestSummaryDecisionsKeepEpicDecisionsInFull: the seed decisions, the answer and the note render in full with their
// makers on a turn-1 prompt, and again on a later turn, where an older turn's multi-sentence decision is cut to its
// first sentence.
func TestSummaryDecisionsKeepEpicDecisionsInFull(t *testing.T) {
	t.Parallel()
	first := summaryDecisionsEpic(store.EpicTurn{Seq: 1, State: "running"})
	assertSummaryDecisions(t, promptOf(t, first, 1, nil), summaryDecisionsEpicLines)

	later := summaryDecisionsEpic(
		store.EpicTurn{Seq: 1, State: "failed", Decisions: []string{"Turn one kept. Dropped tail."}},
		store.EpicTurn{Seq: 2, State: "failed", Decisions: []string{"Turn two kept. Whole tail."}},
		store.EpicTurn{Seq: 3, State: "running"},
	)
	assertSummaryDecisions(t, promptOf(t, later, 3, nil), slices.Concat(summaryDecisionsEpicLines, []string{
		"- turn 1: Turn one kept.",
		"- turn 2: Turn two kept. Whole tail.",
	}))
}

// TestSummaryDecisionsRenderLatestEarlierTurnInFull: the latest turn before the current one renders each of its
// decisions in full, an older turn renders its first sentence, and the current turn's own decisions render nothing.
func TestSummaryDecisionsRenderLatestEarlierTurnInFull(t *testing.T) {
	t.Parallel()
	epic := summaryDecisionsEpic(
		store.EpicTurn{Seq: 1, State: "failed", Decisions: []string{"Older one. Cut here."}},
		store.EpicTurn{
			Seq: 2, State: "failed",
			Decisions: []string{"Latest first. Latest second.", "Latest third."},
		},
		store.EpicTurn{Seq: 3, State: "running", Decisions: []string{"Current turn. Not shown."}},
	)
	assertSummaryDecisions(t, promptOf(t, epic, 3, nil), slices.Concat(summaryDecisionsEpicLines, []string{
		"- turn 1: Older one.",
		"- turn 2: Latest first. Latest second.",
		"- turn 2: Latest third.",
	}))
}

// TestSummaryDecisionsCutOlderTurnsToFirstSentence: an older turn's decision renders as `- turn n: ` and only its
// first sentence. A period inside a word or followed by a digit does not end a sentence, a first line without a period
// is the whole sentence, and a sentence ends at the first newline.
func TestSummaryDecisionsCutOlderTurnsToFirstSentence(t *testing.T) {
	t.Parallel()
	epic := summaryDecisionsEpic(
		store.EpicTurn{Seq: 1, State: "failed", Decisions: []string{
			"Keep the store. Drop the cache.",
			"Pin v1.5 here. Then more.",
			"Name the table\nthen more text. Really.",
			"No period on this line\nsecond line. Tail.",
			"Ends the line.\nNext sentence.",
			"Only one sentence.",
		}},
		store.EpicTurn{Seq: 2, State: "failed", Decisions: []string{"Latest. Stays whole."}},
	)
	assertSummaryDecisions(t, promptOf(t, epic, 3, nil), slices.Concat(summaryDecisionsEpicLines, []string{
		"- turn 1: Keep the store.",
		"- turn 1: Pin v1.5 here.",
		"- turn 1: Name the table",
		"- turn 1: No period on this line",
		"- turn 1: Ends the line.",
		"- turn 1: Only one sentence.",
		"- turn 2: Latest. Stays whole.",
	}))
}

// TestSummaryDecisionsKeepTurnBeforeAnEmptyTurnInFull: when the latest turn before the current one recorded no
// decisions, the latest turn that did is the latest deciding turn, so it renders in full while the turn before it
// is cut.
func TestSummaryDecisionsKeepTurnBeforeAnEmptyTurnInFull(t *testing.T) {
	t.Parallel()
	epic := summaryDecisionsEpic(
		store.EpicTurn{Seq: 1, State: "failed", Decisions: []string{"Old one. Cut."}},
		store.EpicTurn{Seq: 2, State: "failed", Decisions: []string{"Turn two keeps. Its tail."}},
		store.EpicTurn{Seq: 3, State: "failed"},
	)
	assertSummaryDecisions(t, promptOf(t, epic, 4, nil), slices.Concat(summaryDecisionsEpicLines, []string{
		"- turn 1: Old one.",
		"- turn 2: Turn two keeps. Its tail.",
	}))
}
