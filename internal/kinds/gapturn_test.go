package kinds

import (
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// TestConcludeEndsOnlyDeclaredGapsGapTurn: one review, unchanged, gives a gap round while the planner has not declared its
// gap, and ends ended_with_gaps once the latest turn declares it. The declaration alone flips the decision, and the
// question carries the planner's reason after the gap's own.
func TestConcludeEndsOnlyDeclaredGapsGapTurn(t *testing.T) {
	t.Parallel()
	rows := concludeRows(parsimonyReview())
	if got := (Conclude{}).Decide(nodeWith(testRev, false), rows); got.Kind != ActGapRound {
		t.Fatalf("Decide() before the declaration = %+v, want a gap round", got)
	}

	rows.Unresolvable = []store.GapDeclaration{{
		Gap: "reviewer:test_parsimony", Reason: "budget lines are main's", Recommended: store.ChoiceRelease,
	}}
	got := Conclude{}.Decide(nodeWith(testRev, false), rows)
	if got.Kind != ActEnd || got.Phase != phaseEndedWithGaps {
		t.Fatalf("Decide() after the declaration = %+v, want an end ended_with_gaps", got)
	}
	wantGaps := []string{"reviewer test_parsimony: " + parsimonyGapReason + " (planner: budget lines are main's)"}
	if !reflect.DeepEqual(got.Question.Gaps, wantGaps) {
		t.Errorf("question gaps = %q, want %q", got.Question.Gaps, wantGaps)
	}
	if got.Question.Recommended != store.ChoiceRelease {
		t.Errorf("question recommends %q, want %q", got.Question.Recommended, store.ChoiceRelease)
	}
}
