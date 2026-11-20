package kinds

import (
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// parsimonyGapReason is the reason the test_parsimony reviewer gives its gap verdict in the gap round tests.
const parsimonyGapReason = "the test pins the output"

// parsimonyReview returns a review of testRev whose test_parsimony reviewer gives a gap verdict for parsimonyGapReason.
func parsimonyReview() store.Review {
	return store.Review{Frozen: testRev, Verdicts: []store.ReviewVerdict{{
		Position: 1, Reviewer: "test_parsimony", Verdict: "gap", Reason: parsimonyGapReason,
	}}}
}

// TestConcludeStartsGapRoundForLiveGaps: a gap that no planner turn was given yet is live, so the conclusion gives it a
// gap round of its own.
func TestConcludeStartsGapRoundForLiveGaps(t *testing.T) {
	t.Parallel()
	got := Conclude{}.Decide(nodeWith(testRev, false), concludeRows(parsimonyReview()))
	want := Action{Kind: ActGapRound, Gaps: []Gap{{
		Key: "reviewer:test_parsimony", Reason: "reviewer test_parsimony: " + parsimonyGapReason,
	}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Decide() = %+v, want %+v", got, want)
	}
}

// TestConcludeLeavesDeclaredAndSpentGapsOutOfGapRound: a gap the latest turn declared and a gap given two planner turns
// are not live, so the round gives a turn to the third gap alone.
func TestConcludeLeavesDeclaredAndSpentGapsOutOfGapRound(t *testing.T) {
	t.Parallel()
	review := store.Review{Frozen: testRev, Verdicts: []store.ReviewVerdict{
		{Position: 1, Reviewer: "declared", Verdict: "gap", Reason: "declared reason"},
		{Position: 2, Reviewer: "spent", Verdict: "gap", Reason: "spent reason"},
		{Position: 3, Reviewer: "live", Verdict: "gap", Reason: "live reason"},
	}}
	rows := concludeRows(review)
	rows.Unresolvable = []store.GapDeclaration{{
		Gap: "reviewer:declared", Reason: "declared reason", Recommended: store.ChoiceRelease,
	}}
	rows.GapTurns = []store.GapTurn{
		{Gap: "reviewer:spent", Turn: 1, Reason: "first turn"},
		{Gap: "reviewer:spent", Turn: 2, Reason: "second turn"},
	}
	got := Conclude{}.Decide(nodeWith(testRev, false), rows)
	want := Action{Kind: ActGapRound, Gaps: []Gap{{Key: "reviewer:live", Reason: "reviewer live: live reason"}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Decide() = %+v, want %+v", got, want)
	}
}

// TestConcludeEndsWithGapsOnceNoGapIsLive: once the gap has had its two planner turns, the conclusion ends with gaps,
// with the body and a question that lists each turn's reason and recommends correct. A gap declared with release gets
// the question that recommends release.
func TestConcludeEndsWithGapsOnceNoGapIsLive(t *testing.T) {
	t.Parallel()
	body := EndingBody(phaseEndedWithGaps, []string{"reviewer test_parsimony: " + parsimonyGapReason}, nil)
	spent := concludeRows(parsimonyReview())
	spent.GapTurns = []store.GapTurn{
		{Gap: "reviewer:test_parsimony", Turn: 1, Reason: "first answer"},
		{Gap: "reviewer:test_parsimony", Turn: 2, Reason: "second answer"},
	}
	declared := concludeRows(parsimonyReview())
	declared.Unresolvable = []store.GapDeclaration{{
		Gap: "reviewer:test_parsimony", Reason: "the owner decides", Recommended: store.ChoiceRelease,
	}}
	tests := []struct {
		name string
		rows Rows
		want store.GapsQuestion
	}{
		{
			name: "spent gap asks correct with each turn's reason",
			rows: spent,
			want: store.GapsQuestion{
				Gaps: []string{"reviewer test_parsimony: " + parsimonyGapReason +
					" (turn 1: first answer; turn 2: second answer)"},
				Recommended: store.ChoiceCorrect,
				Why:         "correct for: reviewer:test_parsimony",
			},
		},
		{
			name: "gap declared with release asks release",
			rows: declared,
			want: store.GapsQuestion{
				Gaps: []string{"reviewer test_parsimony: " + parsimonyGapReason +
					" (planner: the owner decides)"},
				Recommended: store.ChoiceRelease,
				Why:         "the planner recommends release for every gap",
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Conclude{}.Decide(nodeWith(testRev, false), tc.rows)
			want := Action{
				Kind: ActEnd, Outcome: outcomePassed, Phase: phaseEndedWithGaps, Output: body, Question: tc.want,
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("Decide() = %+v, want %+v", got, want)
			}
		})
	}
}

// TestConcludeHoldsGapRoundWhilePaused: a paused epic with a live gap gives no action, so no gap round starts while the
// owner has paused it.
func TestConcludeHoldsGapRoundWhilePaused(t *testing.T) {
	t.Parallel()
	rows := concludeRows(parsimonyReview())
	rows.Epic.Paused = true
	got := Conclude{}.Decide(nodeWith(testRev, false), rows)
	if want := (Action{Kind: ActNone}); !reflect.DeepEqual(got, want) {
		t.Errorf("Decide() = %+v, want %+v", got, want)
	}
}
