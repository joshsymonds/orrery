package enginehost

import (
	"slices"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// TestSendBackEarlierTurnPlanShowsTheReopenedTask checks that the plan node a client is shown lists, as its tasks, a
// reopened task of an earlier turn that the plan rebuilt, beside the tasks of its own turns, and not an untouched task
// of that earlier turn.
func TestSendBackEarlierTurnPlanShowsTheReopenedTask(t *testing.T) {
	t.Parallel()
	fix := store.NodeRun{Path: "correct@1/fix", Node: "fix", Kind: kindPlan, State: store.NodeRunning, Mark: 1}
	flow := store.EpicFlow{
		Epic: store.EpicRow{
			Turns: []store.EpicTurn{{Seq: 1}, {Seq: 2}},
			Tasks: []store.EpicTask{
				{Seq: 1, Turn: 1, Status: "open", Generation: 1},
				{Seq: 2, Turn: 1, Status: "done"},
			},
		},
		Runs: []store.NodeRun{
			{Path: "plan", Kind: kindPlan, State: store.NodeEnded},
			{Path: "plan/build/T1", Kind: kindBuild, Task: 1, State: store.NodeEnded},
			{Path: "plan/build/T2", Kind: kindBuild, Task: 2, State: store.NodeEnded},
			fix,
			{Path: "correct@1/fix/rebuild/T1", Kind: kindBuild, Task: 1, State: store.NodeRunning},
		},
	}
	message, err := epicNodeMessage(flow, fix)
	if err != nil {
		t.Fatalf("epicNodeMessage: %v", err)
	}
	if !slices.Equal(message.Turns, []int64{2}) || !slices.Equal(message.Tasks, []int64{1}) {
		t.Errorf("fix plan turns %v and tasks %v, want [2] and [1]: it rebuilds T1 alone", message.Turns,
			message.Tasks)
	}
}
