package planner

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// closesRolePara is the paragraph role.md gives the planner on naming the findings a question's choice closes.
const closesRolePara = "When a listed finding should not be corrected by a code change, such as a change the " +
	"contract or an owner decision needs, and you ask the owner whether to keep the change, name the finding in the " +
	"question: each choice that keeps the change lists the finding's id in closes, and its does names the finding and " +
	"says it closes with no code change. A choice that plans a correction closes nothing. Once the owner answers with a " +
	"choice that closes a finding, the round's closure check closes that finding with the answer as its reason and " +
	"starts no verifier for it, so plan no task for it. A choice closes only findings the ledger trigger lists as " +
	"open, and only in a correction round; the check refuses any other id, and a choice whose does does not name each " +
	"finding it closes."

// closesReview returns a review whose ledger holds conformance-1 and test_parsimony-1, both confirmed and open, and
// security-1, confirmed and closed in round 1, with round 1 run.
func closesReview() store.Review {
	return store.Review{
		Frozen: "frozen-1",
		Ledger: []store.LedgerFinding{
			{ID: "conformance-1", Result: "confirmed"},
			{ID: "security-1", Result: "confirmed", Closures: []store.Closure{
				{Finding: "security-1", Round: 1, Step: "close-1", Result: "closed"},
			}},
			{ID: "test_parsimony-1", Result: "confirmed"},
			{ID: "perf-1", Result: "dropped"},
		},
		Rounds: []store.ReviewRound{{Round: 1, Outcome: "failed"}},
	}
}

// closesPlan returns a planner return, as JSON, that asks one review-finding-scope question whose keep-fix choice closes
// closes and does does.
func closesPlan(t *testing.T, does, closes string) json.RawMessage {
	t.Helper()
	choices, err := json.Marshal([]Choice{
		{ID: "keep-fix", Does: does, Closes: closes},
		{ID: "revert-fix", Does: "Plan a task that restores the test.", Closes: ""},
	})
	if err != nil {
		t.Fatalf("marshal choices: %v", err)
	}
	return json.RawMessage(`{"status":"done","decisions":[],"tasks":[],"outline":"o","questions":[` +
		`{"label":"review-finding-scope","text":"Keep the edit?","choices":` + string(choices) + `,` +
		`"recommended":"keep-fix","why":"w","catastrophe":"contract-cannot-hold","confidence":"high",` +
		`"replaces":""}],"actions":[],"outcome":"wait"}`)
}

func TestClosesOfSplitsTheIdsAChoiceNames(t *testing.T) {
	t.Parallel()
	cases := []struct {
		closes string
		want   []string
	}{
		{"", nil},
		{"   ", nil},
		{"conformance-1", []string{"conformance-1"}},
		{"conformance-1, test_parsimony-1", []string{"conformance-1", "test_parsimony-1"}},
		{" conformance-1 ,test_parsimony-1 ", []string{"conformance-1", "test_parsimony-1"}},
		{"conformance-1,,test_parsimony-1", []string{"conformance-1", "", "test_parsimony-1"}},
	}
	for _, tc := range cases {
		if got := closesOf(tc.closes); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("closesOf(%q) = %q, want %q", tc.closes, got, tc.want)
		}
	}
}

func TestCheckClosesRefusesAChoiceClosingWhatNoClosureRoundCloses(t *testing.T) {
	t.Parallel()
	open := []string{"conformance-1", "test_parsimony-1"}
	question := validQuestion("review-finding-scope")
	question.Choices[0].Does = "Keep it and close conformance-1 and security-1."
	question.Choices[0].Closes = "conformance-1, security-1"
	ret := Return{Questions: []Question{question}}
	t.Run("a choice closes findings only in a correction round", func(t *testing.T) {
		t.Parallel()
		checkLines(t, checkCloses(ret, "building", open),
			`questions[0] "review-finding-scope" choices[0].closes: a choice closes findings only in a correction `+
				`round; this epic is in phase building`)
	})
	t.Run("an id that is not an open finding is refused with the open findings", func(t *testing.T) {
		t.Parallel()
		checkLines(t, checkCloses(ret, phaseCorrecting, open),
			`questions[0] "review-finding-scope" choices[0].closes[1]: security-1 is not an open finding of the `+
				`ledger (open: conformance-1, test_parsimony-1)`)
	})
	t.Run("with no open finding the refusal says so", func(t *testing.T) {
		t.Parallel()
		checkLines(t, checkCloses(ret, phaseCorrecting, nil),
			`questions[0] "review-finding-scope" choices[0].closes[0]: conformance-1 is not an open finding of the `+
				`ledger (no finding is open)`,
			`questions[0] "review-finding-scope" choices[0].closes[1]: security-1 is not an open finding of the `+
				`ledger (no finding is open)`)
	})
	t.Run("open findings pass, and a choice closing none is not looked at", func(t *testing.T) {
		t.Parallel()
		passing := validQuestion("review-finding-scope")
		passing.Choices[0].Does = "Keep it and close conformance-1 and test_parsimony-1."
		passing.Choices[0].Closes = "conformance-1,test_parsimony-1"
		checkLines(t, checkCloses(Return{Questions: []Question{passing}}, phaseCorrecting, open))
		checkLines(t, checkCloses(Return{Questions: []Question{validQuestion("plain")}}, "building", nil))
	})
}

func TestReturnCheckChecksTheFindingsAChoiceClosesAgainstTheReview(t *testing.T) {
	t.Parallel()
	epic := store.EpicRow{Phase: phaseCorrecting, Requirements: []store.EpicRequirement{{ID: "R1"}}}
	check := returnCheck(context.Background(), epic, closesReview(), testResolver{})
	const keep = "Keep the app.test.tsx edit and close conformance-1 with no code change."
	cases := []struct {
		name string
		plan json.RawMessage
		want []string
	}{
		{"a choice closing an open finding it names passes", closesPlan(t, keep, "conformance-1"), nil},
		{
			"a finding closed in an earlier round is not open",
			closesPlan(t, "Keep it and close security-1.", "security-1"),
			[]string{`questions[0] "review-finding-scope" choices[0].closes[0]: security-1 is not an open finding ` +
				`of the ledger (open: conformance-1, test_parsimony-1)`},
		},
		{
			"a finding the choice's does does not name is refused by the store's rule",
			closesPlan(t, keep, "conformance-1, test_parsimony-1"),
			[]string{`questions[0] "review-finding-scope" choices[0].closes[1]: test_parsimony-1 is not named in does`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := check(tc.plan); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("returnCheck() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPlanOfCarriesTheFindingsAChoiceCloses(t *testing.T) {
	t.Parallel()
	const keep = "Keep the edit and close conformance-1 and test_parsimony-1."
	ret, err := Parse(closesPlan(t, keep, "conformance-1, test_parsimony-1"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := []store.EpicChoice{
		{ID: "keep-fix", Does: keep, Closes: []string{"conformance-1", "test_parsimony-1"}},
		{ID: "revert-fix", Does: "Plan a task that restores the test."},
	}
	if got := planOf(ret).Questions[0].Choices; !reflect.DeepEqual(got, want) {
		t.Errorf("planOf choices = %+v, want %+v", got, want)
	}
}

func TestChoiceLinesNameTheFindingsAChoiceCloses(t *testing.T) {
	t.Parallel()
	question := store.EpicQuestion{
		Seq: 2, Turn: 22, Label: "review-finding-scope", Text: "Keep the edit?", State: "open",
		Choices: []store.EpicChoice{
			{ID: "keep-fix", Does: "Keep it and close conformance-1.", Closes: []string{"conformance-1"}},
			{ID: "revert-fix", Does: "Restore the test."},
		},
	}
	wantPrompt := []string{
		"- Q2 (turn 22, review-finding-scope): Keep the edit?",
		"  keep-fix: Keep it and close conformance-1. (closes conformance-1)",
		"  revert-fix: Restore the test.",
	}
	if got := questionLines([]store.EpicQuestion{question}); !reflect.DeepEqual(got, wantPrompt) {
		t.Errorf("questionLines = %q, want %q", got, wantPrompt)
	}
	wantAdvice := []string{
		"  keep-fix: Keep it and close conformance-1. (closes conformance-1)",
		"  revert-fix: Restore the test.",
		"  other: answer in your own words",
	}
	if got := adviceLines(question); !reflect.DeepEqual(got, wantAdvice) {
		t.Errorf("adviceLines = %q, want %q", got, wantAdvice)
	}
}

func TestRoleTellsThePlannerToNameTheFindingsAQuestionCloses(t *testing.T) {
	t.Parallel()
	if !strings.Contains(role, closesRolePara) {
		t.Error("role text lacks the paragraph on naming the findings a choice closes")
	}
	const field = `and closes, the ids of the open ledger findings that choosing it settles with no code change, ` +
		`separated by commas, and "" for every other choice;`
	if !strings.Contains(role, field) {
		t.Error("role text does not describe a choice's closes field")
	}
}
