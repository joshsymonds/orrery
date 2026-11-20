package planner

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// gapReturnOf parses raw, a planner return, and fails the test when it does not parse.
func gapReturnOf(t *testing.T, raw string) Return {
	t.Helper()
	ret, err := Parse(json.RawMessage(raw))
	if err != nil {
		t.Fatalf("Parse(%s) error = %v", raw, err)
	}
	return ret
}

// returnWithUnresolvable is a planner return with no tasks, actions or questions whose unresolvable string is text.
func returnWithUnresolvable(t *testing.T, text string) Return {
	t.Helper()
	quoted, err := json.Marshal(text)
	if err != nil {
		t.Fatalf("json.Marshal(%q) error = %v", text, err)
	}
	return gapReturnOf(t, `{"status":"done","decisions":[],"tasks":[],"outline":"o","questions":[],"actions":[],`+
		`"unresolvable":`+string(quoted)+`,"outcome":"wait"}`)
}

// rerunOfTestParsimonyRaw is a planner return whose only action reruns the verdict of test_parsimony.
const rerunOfTestParsimonyRaw = `{"status":"done","decisions":[],"tasks":[],"outline":"o","questions":[],` +
	`"actions":[{"kind":"rerun_review","args":"{\"reviewer\":\"test_parsimony\"}","reason":"the cause is fixed"}],` +
	`"outcome":"wait"}`

func TestParseReadsRerunReviewAndUnresolvable(t *testing.T) {
	t.Parallel()
	raw := `{"status":"done","decisions":[],"tasks":[],"outline":"o","questions":[],` +
		`"actions":[{"kind":"rerun_review","args":"{\"reviewer\":\"test_parsimony\"}","reason":"the cause is fixed"}],` +
		`"unresolvable":"[{\"gap\":\"reviewer:test_parsimony\",\"reason\":\"the fix needs the owner\",` +
		`\"recommended\":\"release\"},{\"gap\":\"task:T2\",\"reason\":\"the contract conflicts\",` +
		`\"recommended\":\"correct\"}]",` +
		`"outcome":"wait"}`
	ret := gapReturnOf(t, raw)

	wantEntries := []Unresolvable{
		{Gap: "reviewer:test_parsimony", Reason: "the fix needs the owner", Recommended: "release"},
		{Gap: "task:T2", Reason: "the contract conflicts", Recommended: "correct"},
	}
	if !reflect.DeepEqual(ret.Unresolvable, wantEntries) {
		t.Errorf("Parse(%s) Unresolvable = %+v, want %+v", raw, ret.Unresolvable, wantEntries)
	}

	plan := planOf(ret)
	wantAction := store.EpicActionInput{
		Kind: store.ActionRerunReview, Reviewer: "test_parsimony", Reason: "the cause is fixed",
	}
	if !reflect.DeepEqual(plan.Actions, []store.EpicActionInput{wantAction}) {
		t.Errorf("planOf(Parse(%s)) Actions = %+v, want %+v", raw, plan.Actions, []store.EpicActionInput{wantAction})
	}
	wantGaps := []store.GapDeclaration{
		{Gap: "reviewer:test_parsimony", Reason: "the fix needs the owner", Recommended: "release"},
		{Gap: "task:T2", Reason: "the contract conflicts", Recommended: "correct"},
	}
	if !reflect.DeepEqual(plan.Unresolvable, wantGaps) {
		t.Errorf("planOf(Parse(%s)) Unresolvable = %+v, want %+v", raw, plan.Unresolvable, wantGaps)
	}
}

func TestCheckRefusesRerunReviewWithoutReviewer(t *testing.T) {
	t.Parallel()
	raw := `{"status":"done","decisions":[],"tasks":[],"outline":"o","questions":[],` +
		`"actions":[{"kind":"rerun_review","args":"{}","reason":"the cause is fixed"}],"outcome":"wait"}`
	checkLines(t, checkPlan(gapReturnOf(t, raw), nil), `actions[0] "rerun_review" reviewer: empty`)
}

func TestCheckRefusesMalformedUnresolvable(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		text string
		want []string
	}{
		{
			name: "an empty reason",
			text: `[{"gap":"reviewer:test_parsimony","reason":"","recommended":"release"}]`,
			want: []string{`unresolvable[0] reason: empty`},
		},
		{
			name: "a recommendation that is not release or correct",
			text: `[{"gap":"task:T2","reason":"the contract conflicts","recommended":"close"}]`,
			want: []string{`unresolvable[0] recommended: not release or correct`},
		},
		{
			name: "a gap an earlier entry names",
			text: `[{"gap":"review","reason":"a verdict is wrong","recommended":"correct"},` +
				`{"gap":"review","reason":"the same verdict again","recommended":"correct"}]`,
			want: []string{`unresolvable[1] gap: names a gap an earlier entry names`},
		},
		{
			name: "a string that is not a JSON array",
			text: `not a JSON array`,
			want: []string{`unresolvable: not a JSON array of objects with gap, reason and recommended`},
		},
		{
			name: "the JSON null",
			text: `null`,
			want: []string{`unresolvable: not a JSON array of objects with gap, reason and recommended`},
		},
		{
			name: "data after the JSON array",
			text: `[] x`,
			want: []string{`unresolvable: not a JSON array of objects with gap, reason and recommended`},
		},
		{
			name: "an entry with a key other than gap, reason and recommended",
			text: `[{"gap":"task:T2","reason":"the contract conflicts","recommended":"correct","note":"x"}]`,
			want: []string{`unresolvable: not a JSON array of objects with gap, reason and recommended`},
		},
		{
			name: "an entry whose gap is not a string",
			text: `[{"gap":2,"reason":"the contract conflicts","recommended":"correct"}]`,
			want: []string{`unresolvable: not a JSON array of objects with gap, reason and recommended`},
		},
		{
			name: "well-formed entries",
			text: `[{"gap":"reviewer:test_parsimony","reason":"the fix needs the owner","recommended":"release"},` +
				`{"gap":"task:T2","reason":"the contract conflicts","recommended":"correct"}]`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkLines(t, checkPlan(returnWithUnresolvable(t, tc.text), nil), tc.want...)
		})
	}
}

func TestRerunReviewOfSpentGapRefusedGapBound(t *testing.T) {
	t.Parallel()
	const line = `actions[0] "rerun_review" reviewer: gap reviewer:test_parsimony has had its 2 planner turns; ` +
		`declare it in unresolvable instead`
	epic := store.EpicRow{}
	turns := []store.GapTurn{
		{Gap: "reviewer:test_parsimony", Turn: 1, Reason: "the first turn"},
		{Gap: "reviewer:test_parsimony", Turn: 2, Reason: "the second turn"},
	}
	check := returnCheck(context.Background(), epic, store.Review{}, testResolver{}, turns)
	checkLines(t, check(json.RawMessage(rerunOfTestParsimonyRaw)), line)

	oneTurn := returnCheck(context.Background(), epic, store.Review{}, testResolver{}, turns[:1])
	checkLines(t, oneTurn(json.RawMessage(rerunOfTestParsimonyRaw)))
}
