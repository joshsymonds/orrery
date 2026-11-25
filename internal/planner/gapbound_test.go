package planner

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// gapBoundTurns are the two planner turns the gap of test_parsimony's reviewer was given: turn 1, and turn 2 after it.
func gapBoundTurns() []store.GapTurn {
	return []store.GapTurn{
		{Gap: "reviewer:test_parsimony", Turn: 1, Reason: "the first turn"},
		{Gap: "reviewer:test_parsimony", Turn: 2, Reason: "the second turn"},
	}
}

// gapBoundTrigger returns the trigger with id whose cause is cause.
func gapBoundTrigger(id, cause string) store.EpicTrigger {
	return store.EpicTrigger{ID: id, Cause: cause}
}

// gapBoundRefusals returns the refusals of the rerun_review return of test_parsimony, checked against turns as the
// planner checks a turn with the gap turns it is given.
func gapBoundRefusals(turns []store.GapTurn) []string {
	check := returnCheck(context.Background(), store.EpicRow{}, store.Review{}, testResolver{}, turns, false)
	return check(json.RawMessage(rerunOfTestParsimonyRaw))
}

// TestSecondGapTurnMayRerunReviewerGapBound: a gap of test_parsimony's reviewer that has had turn 1 and turn 2 gives
// the planner turn woken by its gap trigger no refusal for rerun_review, since the turn being checked is turn 2 and
// turn 1 is the only turn given before it.
func TestSecondGapTurnMayRerunReviewerGapBound(t *testing.T) {
	t.Parallel()
	triggers := []store.EpicTrigger{gapBoundTrigger(
		"gap:x", "gap reviewer:test_parsimony: reviewer test_parsimony: gave no verdict twice\nverdicts:",
	)}
	checkLines(t, gapBoundRefusals(priorGapTurns(gapBoundTurns(), triggers)))
}

// TestThirdTryAtSpentGapRefusedGapBound: a turn woken by no gap trigger for test_parsimony's reviewer, or by one of
// another reviewer's gap, is checked against both turns the gap was given, and refuses its rerun_review with the line
// that sends the gap to the owner.
func TestThirdTryAtSpentGapRefusedGapBound(t *testing.T) {
	t.Parallel()
	const line = `actions[0] "rerun_review" reviewer: gap reviewer:test_parsimony has had its 2 planner turns; ` +
		`declare it in unresolvable instead`
	cases := []struct {
		name     string
		triggers []store.EpicTrigger
	}{
		{name: "no trigger"},
		{
			name: "a gap trigger of another reviewer",
			triggers: []store.EpicTrigger{gapBoundTrigger(
				"gap:y", "gap reviewer:floor: reviewer floor: gave no verdict twice\nverdicts:",
			)},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkLines(t, gapBoundRefusals(priorGapTurns(gapBoundTurns(), tc.triggers)), line)
		})
	}
}

// TestPriorGapTurnsDropsOnlyTheCheckedTurnGapBound: the turn woken by test_parsimony's gap trigger drops that gap's
// highest turn, turn 2, and keeps the first turn of its gap and every turn of another gap, in order.
func TestPriorGapTurnsDropsOnlyTheCheckedTurnGapBound(t *testing.T) {
	t.Parallel()
	turns := []store.GapTurn{
		{Gap: "reviewer:test_parsimony", Turn: 1, Reason: "the first turn"},
		{Gap: "reviewer:floor", Turn: 1, Reason: "floor's turn"},
		{Gap: "reviewer:test_parsimony", Turn: 2, Reason: "the second turn"},
		{Gap: "task:T2", Turn: 1, Reason: "a task's turn"},
	}
	triggers := []store.EpicTrigger{gapBoundTrigger(
		"gap:x", "gap reviewer:test_parsimony: reviewer test_parsimony: gave no verdict twice\nverdicts:",
	)}
	want := []store.GapTurn{turns[0], turns[1], turns[3]}
	if got := priorGapTurns(turns, triggers); !reflect.DeepEqual(got, want) {
		t.Errorf("priorGapTurns() = %+v, want %+v", got, want)
	}
}
