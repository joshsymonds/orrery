package kinds

import (
	"fmt"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// unbuiltTurns returns turns 1 to highest, each succeeded with outcome work.
func unbuiltTurns(highest int64) []store.EpicTurn {
	turns := make([]store.EpicTurn, 0, highest)
	for seq := int64(1); seq <= highest; seq++ {
		turns = append(turns, store.EpicTurn{Seq: seq, State: "succeeded", Outcome: "work"})
	}
	return turns
}

// unbuiltRows returns the rows of an epic with turns 1 to highest, tasks and node runs, whose first plan run has ended.
func unbuiltRows(highest int64, tasks []store.EpicTask, runs ...store.NodeRun) Rows {
	return Rows{
		Epic: store.EpicRow{ID: "orrery-1a2b3c4/unbuilt", Turns: unbuiltTurns(highest), Tasks: tasks},
		Runs: append([]store.NodeRun{{Path: "plan", Kind: kindPlan, State: store.NodeEnded}}, runs...),
	}
}

// unbuiltChild returns the build child of task seq under the first plan run, ended as that plan's end ended it.
func unbuiltChild(seq int64) store.NodeRun {
	return store.NodeRun{Path: fmt.Sprintf("plan/build/T%d", seq), Kind: kindBuild, Task: seq, State: store.NodeEnded}
}

// TestPlanBuildsUnbuiltMark checks the mark a new plan run starts with: the epic's highest turn when every open task
// at generation 0 has a build node run, and one below the turn of the earliest open task at generation 0 that no build
// node run has built, so that task is the new plan's while a task an earlier plan built of an earlier turn is not.
func TestPlanBuildsUnbuiltMark(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		rows Rows
		want int64
		// planned is the seq of a task the new plan holds at the mark, and earlier one it does not, 0 for none.
		planned, earlier int64
	}{
		{name: "an epic with no turn starts at 0", rows: Rows{}, want: 0},
		{
			name: "every open task has a build run, so the mark is the highest turn",
			rows: unbuiltRows(3, []store.EpicTask{
				{Seq: 1, Turn: 1, Status: "done"}, {Seq: 2, Turn: 2, Status: "open"},
			}, unbuiltChild(1), unbuiltChild(2)),
			want: 3, earlier: 2,
		},
		{
			name: "a task planned while no plan ran lowers the mark below its turn",
			rows: unbuiltRows(4, []store.EpicTask{
				{Seq: 1, Turn: 1, Status: "done"}, {Seq: 2, Turn: 3, Status: "open"},
			}, unbuiltChild(1)),
			want: 2, planned: 2, earlier: 1,
		},
		{
			name: "the earliest unbuilt task decides the mark",
			rows: unbuiltRows(5, []store.EpicTask{
				{Seq: 1, Turn: 1, Status: "done"}, {Seq: 2, Turn: 4, Status: "open"}, {Seq: 3, Turn: 2, Status: "open"},
			}, unbuiltChild(1)),
			want: 1, planned: 3, earlier: 1,
		},
		{
			name: "a task whose build ran under an ended plan is not unbuilt",
			rows: unbuiltRows(3, []store.EpicTask{{Seq: 1, Turn: 2, Status: "open"}},
				unbuiltChild(1)),
			want: 3, earlier: 1,
		},
		{
			name: "a reopened task, which its generation builds, is not unbuilt",
			rows: unbuiltRows(3, []store.EpicTask{{Seq: 1, Turn: 2, Status: "open", Generation: 1}}),
			want: 3,
		},
		{
			name: "a closed task with no build run is not unbuilt",
			rows: unbuiltRows(4, []store.EpicTask{
				{Seq: 1, Turn: 1, Status: "done"},
				{Seq: 2, Turn: 2, Status: "failed"},
				{Seq: 3, Turn: 3, Status: "gap"},
			}),
			want: 4,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mark := PlanMark(tc.rows)
			if mark != tc.want {
				t.Fatalf("PlanMark = %d, want %d", mark, tc.want)
			}
			plan := store.NodeRun{Path: "fix", Kind: kindPlan, State: store.NodeRunning, Mark: mark}
			for _, task := range tc.rows.Epic.Tasks {
				switch task.Seq {
				case tc.planned:
					if !PlanTask(task, plan, tc.rows.Runs) {
						t.Errorf("PlanTask(T%d) at mark %d = false, want the new plan to hold it", task.Seq, mark)
					}
				case tc.earlier:
					if PlanTask(task, plan, tc.rows.Runs) {
						t.Errorf("PlanTask(T%d) at mark %d = true, want it the earlier plan's alone", task.Seq, mark)
					}
				}
			}
		})
	}
}

// TestPlanTaskHoldsATaskItBuilt covers a task whose turn was still running when the plan started: the plan's mark
// counted that turn, so the task's turn is at the mark, and the plan built the task once the turn stored it. The plan
// holds it, so its settle walk integrates it, while a task of the same turn that an earlier plan built stays that plan's.
func TestPlanTaskHoldsATaskItBuilt(t *testing.T) {
	t.Parallel()
	plan := store.NodeRun{Path: "release/A3/fix", Kind: kindPlan, State: store.NodeRunning, Mark: 16}
	built := store.EpicTask{Seq: 27, Turn: 16, Status: "done"}
	earlier := store.EpicTask{Seq: 26, Turn: 16, Status: "done"}
	runs := []store.NodeRun{
		plan,
		{Path: "release/A3/fix/rebuild/T27", Kind: kindBuild, Task: 27, State: store.NodeEnded},
		{Path: "release/A2/fix/rebuild/T26", Kind: kindBuild, Task: 26, State: store.NodeEnded},
	}
	if !PlanTask(built, plan, runs) {
		t.Error("PlanTask(T27) = false, want the plan that built it to hold it")
	}
	if PlanTask(earlier, plan, runs) {
		t.Error("PlanTask(T26) = true, want it the earlier plan's alone")
	}
}
