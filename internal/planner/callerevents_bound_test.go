package planner

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/joshsymonds/orrery/internal/store"
)

// boundEpicLine is the heading of the epic the bound tests give their events, whose title is short, so it is whole.
const boundEpicLine = "Epic orrery-bound/epic (Bound the event)"

// boundEntry returns the invented epic the bound tests give their events, in the released phase.
func boundEntry() store.EpicAttention {
	return store.EpicAttention{Epic: "orrery-bound/epic", Title: "Bound the event", Phase: "released"}
}

// longQuestion returns question 1 with label, 5000 runes of text, thirty choices of 1000 runes each, and a
// recommendation of choice c1 whose why is 2000 runes.
func longQuestion(label string) store.EpicQuestion {
	choices := make([]store.EpicChoice, 30)
	for i := range choices {
		choices[i] = store.EpicChoice{ID: fmt.Sprintf("c%d", i+1), Does: strings.Repeat("d", 1000)}
	}
	return store.EpicQuestion{
		Seq: 1, Label: label, Text: strings.Repeat("q", 5000), Choices: choices,
		Recommended: "c1", Confidence: "medium", Why: strings.Repeat("w", 2000),
	}
}

// boundLastQ1 is the last line of a question or park body of question 1.
const boundLastQ1 = "epic_get with part question and question Q1 reads it whole; epic_answer answers it."

// boundChoice is the line of choice c<i> of longQuestion as the bound tests expect it: its 1000 runes cut to 160.
func boundChoice(i int) string {
	return fmt.Sprintf("  c%d: %s…", i, strings.Repeat("d", 160))
}

func TestQuestionEventBodyBoundedEpicByPart(t *testing.T) {
	t.Parallel()
	kind, body := questionEventOf(boundEntry(), longQuestion("which-store"))
	if kind != store.CallerKindQuestion {
		t.Fatalf("kind = %q, want %q", kind, store.CallerKindQuestion)
	}
	want := strings.Join([]string{
		boundEpicLine + " asks a question.",
		"Q1 which-store: " + strings.Repeat("q", 600) + "…",
		boundChoice(1),
		boundChoice(2),
		"  and 28 more choices",
		"  other: answer in your own words",
		"  recommended: c1 (medium): " + strings.Repeat("w", 200) + "…",
		boundLastQ1,
	}, "\n")
	if len(body) > eventBodyBytes {
		t.Fatalf("body is %d bytes, want at most %d", len(body), eventBodyBytes)
	}
	if body != want {
		t.Fatalf("body =\n%s\nwant\n%s", body, want)
	}
}

func TestParkEventBodyBoundedEpicByPart(t *testing.T) {
	t.Parallel()
	entry := boundEntry()
	entry.Parked = strings.Repeat("r", 5000)
	question := longQuestion(store.ParkLabel)
	kind, body := questionEventOf(entry, question)
	if kind != store.CallerKindPark {
		t.Fatalf("kind = %q, want %q", kind, store.CallerKindPark)
	}
	want := strings.Join([]string{
		"Epic orrery-bound/epic (Bound the event) is parked.",
		"Q1 park:",
		boundChoice(1),
		boundChoice(2),
		boundChoice(3),
		boundChoice(4),
		"  and 26 more choices",
		"  other: answer in your own words",
		"  recommended: c1 (medium): " + strings.Repeat("w", 200) + "…",
		"parked: " + strings.Repeat("r", 292) + "…",
		boundLastQ1,
	}, "\n")
	if len(body) > eventBodyBytes {
		t.Fatalf("body is %d bytes, want at most %d", len(body), eventBodyBytes)
	}
	if body != want {
		t.Fatalf("body =\n%s\nwant\n%s", body, want)
	}
}

func TestParkEventBodyWithoutChoicesEpicByPart(t *testing.T) {
	t.Parallel()
	entry := boundEntry()
	entry.Parked = "the reason"
	_, body := questionEventOf(entry, store.EpicQuestion{Seq: 1, Label: store.ParkLabel})
	want := strings.Join([]string{
		"Epic orrery-bound/epic (Bound the event) is parked.",
		"parked: the reason",
		"Q1 park:",
		"  other: answer in your own words",
		boundLastQ1,
	}, "\n")
	if body != want {
		t.Fatalf("body =\n%s\nwant\n%s", body, want)
	}
}

func TestQuestionEventBodyCutFurtherOnRunesEpicByPart(t *testing.T) {
	t.Parallel()
	// Multi-byte text with no choice: 600 runes of text are 1800 bytes, so the text is cut further to fit.
	question := store.EpicQuestion{Seq: 2, Label: "which-store", Text: strings.Repeat("界", 5000)}
	kind, body := questionEventOf(boundEntry(), question)
	if kind != store.CallerKindQuestion {
		t.Fatalf("kind = %q, want %q", kind, store.CallerKindQuestion)
	}
	if len(body) > eventBodyBytes {
		t.Fatalf("body is %d bytes, want at most %d", len(body), eventBodyBytes)
	}
	if !utf8.ValidString(body) {
		t.Fatalf("body is not valid UTF-8: %q", body)
	}
	lines := strings.Split(body, "\n")
	if len(lines) != 4 {
		t.Fatalf("body has %d lines, want 4 (epic, head, other, last):\n%s", len(lines), body)
	}
	if lines[0] != boundEpicLine+" asks a question." {
		t.Fatalf("epic line = %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "Q2 which-store: ") || !strings.HasSuffix(lines[1], "…") {
		t.Fatalf("head line = %q, want the text cut to an ellipsis", lines[1])
	}
	if want := "epic_get with part question and question Q2 reads it whole; epic_answer answers it."; lines[3] != want {
		t.Fatalf("last line = %q, want %q", lines[3], want)
	}
}

func TestPhaseEventBodyReportEpicByPart(t *testing.T) {
	t.Parallel()
	for _, phase := range []string{"released", "done"} {
		entry := boundEntry()
		entry.Phase = phase
		want := boundEpicLine + " is " + phase + ".\nepic_get with part report reads its report."
		if got := phaseEventBody(entry); got != want {
			t.Fatalf("phaseEventBody(%s) = %q, want %q", phase, got, want)
		}
	}
}
