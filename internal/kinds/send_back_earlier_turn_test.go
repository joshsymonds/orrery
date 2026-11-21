package kinds

import (
	"slices"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// earlierTurnRoundRows returns the rows of a correction round whose only work is T1 of turn 1, which a send_back of the
// round's turn 2 reopened and the round's fix plan, started after turn 1, rebuilt: T1 is at generation 1 with a build
// child under correct@1/fix. T2 of turn 1 is untouched, built by the first plan alone.
func earlierTurnRoundRows() Rows {
	return Rows{
		Epic: store.EpicRow{
			ID: "orrery-1a2b3c4/earlier", Phase: "accepted",
			Turns: []store.EpicTurn{
				{Seq: 1, State: "succeeded", Outcome: "complete"}, {Seq: 2, State: "succeeded", Outcome: "work"},
			},
			Tasks: []store.EpicTask{
				{Seq: 1, Turn: 1, Status: "done", Generation: 1},
				{Seq: 2, Turn: 1, Status: "done"},
			},
		},
		Runs: []store.NodeRun{
			{Path: "plan", Kind: kindPlan, State: store.NodeEnded},
			{Path: "plan/build/T1", Kind: "build", Task: 1, State: store.NodeEnded},
			{Path: "plan/build/T2", Kind: "build", Task: 2, State: store.NodeEnded},
			{Path: "correct@1/fix", Kind: kindPlan, State: store.NodeEnded, Mark: 1, Round: 1},
			{Path: "correct@1/fix/rebuild/T1", Kind: "build", Task: 1, State: store.NodeEnded, Round: 1},
			{Path: "correct@1/closure", Kind: "closure", State: store.NodeRunning, Round: 1},
		},
	}
}

// TestSendBackEarlierTurnClosureCountsTheReopenedTask checks that a correction round whose only work is a reopened task
// of an earlier turn, rebuilt by the round's fix plan, records that task as one the round ran, and not the untouched
// task of the same turn.
func TestSendBackEarlierTurnClosureCountsTheReopenedTask(t *testing.T) {
	t.Parallel()
	if got := closureTasks(earlierTurnRoundRows(), "correct@1/closure"); !slices.Equal(got, []int64{1}) {
		t.Errorf("closure tasks = %v, want [1]: the round rebuilt T1 alone", got)
	}
}
