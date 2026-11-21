package interpret

import (
	"fmt"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/store"
)

// composedRun returns the running integrate run at batchPath, at attempt 1 on revision c1, whose result composes the
// tasks with the seqs given, as a batch's result does.
func composedRun(seqs ...int64) store.NodeRun {
	composed := make([]string, 0, len(seqs))
	for _, seq := range seqs {
		composed = append(composed, fmt.Sprintf(`{"task":%d,"commit":"c%d"}`, seq, seq))
	}
	run := batchRun("c1")
	run.Result = fmt.Sprintf(`{"composed":[%s]}`, strings.Join(composed, ","))
	return run
}

// claimedBy returns a done task seq of turn 1 that the batch at batchPath claims.
func claimedBy(seq int64) store.EpicTask {
	return store.EpicTask{Seq: seq, Turn: 1, Status: "done", Batch: batchPath}
}

// wakeRows returns the rows of a plan running from mark 0 in its turn 1, with the tasks and the runs given.
func wakeRows(tasks []store.EpicTask, runs ...store.NodeRun) kinds.Rows {
	return rowsOf([]store.EpicTurn{turnAt(1, "work")}, tasks, append([]store.NodeRun{runningPlan(0)}, runs...)...)
}

// TestPlannerWakePassedBatchLeavingOpenTask: a passed batch that leaves a task open ends with no trigger, so it starts
// no planner turn.
func TestPlannerWakePassedBatchLeavingOpenTask(t *testing.T) {
	t.Parallel()
	batch := batchNode()
	plan := Action{Kind: ActRun, Path: "plan", NodeKind: "plan"}
	decideCases(t, []decideCase{
		{
			name: "passed batch with an open task ends with its output and no trigger",
			flow: planWithChecks(batch),
			rows: wakeRows([]store.EpicTask{claimedBy(1), openTask(2, 1)}, composedRun(1)),
			reg:  integrateKinds(scripted{0: {Kind: kinds.ActEnd, Outcome: "passed", Output: "R1 green"}}),
			want: []Action{plan, {
				Kind: ActBatch, Path: batchPath, Attempt: 1, Outcome: "passed", Output: "R1 green",
				Run: composedRun(1),
			}},
		},
	})
}

// TestPlannerWakePassedBatchLeavingUnclaimedDoneTask: a passed batch that leaves a done task no batch claims ends with
// no trigger, so it starts no planner turn.
func TestPlannerWakePassedBatchLeavingUnclaimedDoneTask(t *testing.T) {
	t.Parallel()
	batch := batchNode()
	plan := Action{Kind: ActRun, Path: "plan", NodeKind: "plan"}
	decideCases(t, []decideCase{
		{
			name: "passed batch with an unclaimed done task ends with its output and no trigger",
			flow: planWithChecks(batch),
			rows: wakeRows([]store.EpicTask{claimedBy(1), doneTask(2, 1)}, composedRun(1)),
			reg:  integrateKinds(scripted{0: {Kind: kinds.ActEnd, Outcome: "passed", Output: "R1 green"}}),
			want: []Action{plan, {
				Kind: ActBatch, Path: batchPath, Attempt: 1, Outcome: "passed", Output: "R1 green",
				Run: composedRun(1),
			}},
		},
	})
}

// TestPlannerWakePassedBatchLeavingNone: a passed batch that leaves no work behind writes its trigger, so it starts a
// planner turn. Its end and its advance both do, and a gap task beside the composed ones leaves nothing behind.
func TestPlannerWakePassedBatchLeavingNone(t *testing.T) {
	t.Parallel()
	batch := batchNode()
	plan := Action{Kind: ActRun, Path: "plan", NodeKind: "plan"}
	gap := store.EpicTask{Seq: 1, Turn: 1, Status: "gap"}
	decideCases(t, []decideCase{
		{
			name: "passed batch that composes every task ends with its output and the trigger that wakes the planner",
			flow: planWithChecks(batch),
			rows: wakeRows([]store.EpicTask{claimedBy(1), claimedBy(2)}, composedRun(1, 2)),
			reg:  integrateKinds(scripted{0: {Kind: kinds.ActEnd, Outcome: "passed", Output: "R1 green"}}),
			want: []Action{plan, {
				Kind: ActBatch, Path: batchPath, Attempt: 1, Outcome: "passed", Output: "R1 green",
				Trigger: batchTrigger("passed", "R1 green"), Run: composedRun(1, 2),
			}},
		},
		{
			name: "advance that composes every task carries the trigger that wakes the planner",
			flow: planWithChecks(batch),
			rows: wakeRows([]store.EpicTask{claimedBy(1), claimedBy(2)}, composedRun(1, 2)),
			reg:  integrateKinds(scripted{0: {Kind: kinds.ActAdvance, Output: "R1 green"}}),
			want: []Action{plan, {
				Kind: ActWork, Work: kinds.ActAdvance, Output: "R1 green", Trigger: batchTrigger("passed", "R1 green"),
				Path: batchPath, NodeKind: "integrate", Def: batch, Run: composedRun(1, 2),
			}},
		},
		{
			name: "passed batch beside a gap task ends with its output and the trigger that wakes the planner",
			flow: planWithChecks(batch),
			rows: wakeRows([]store.EpicTask{gap, claimedBy(2)}, composedRun(2)),
			reg:  integrateKinds(scripted{0: {Kind: kinds.ActEnd, Outcome: "passed", Output: "R1 green"}}),
			want: []Action{plan, {
				Kind: ActBatch, Path: batchPath, Attempt: 1, Outcome: "passed", Output: "R1 green",
				Trigger: batchTrigger("passed", "R1 green"), Run: composedRun(2),
			}},
		},
	})
}

// TestPlannerWakeAdvanceLeavingOpenTask: an integrate advance that leaves a task open carries no trigger, so it starts
// no planner turn.
func TestPlannerWakeAdvanceLeavingOpenTask(t *testing.T) {
	t.Parallel()
	batch := batchNode()
	plan := Action{Kind: ActRun, Path: "plan", NodeKind: "plan"}
	decideCases(t, []decideCase{
		{
			name: "advance with an open task gives work that moves the epic, with no trigger",
			flow: planWithChecks(batch),
			rows: wakeRows([]store.EpicTask{claimedBy(1), openTask(2, 1)}, composedRun(1)),
			reg:  integrateKinds(scripted{0: {Kind: kinds.ActAdvance, Output: "R1 green"}}),
			want: []Action{plan, {
				Kind: ActWork, Work: kinds.ActAdvance, Output: "R1 green",
				Path: batchPath, NodeKind: "integrate", Def: batch, Run: composedRun(1),
			}},
		},
	})
}

// TestPlannerWakeFailedBatch: a failed batch ends with its trigger whatever work it leaves, so it starts a planner turn
// even while a task is open.
func TestPlannerWakeFailedBatch(t *testing.T) {
	t.Parallel()
	batch := batchNode()
	plan := Action{Kind: ActRun, Path: "plan", NodeKind: "plan"}
	decideCases(t, []decideCase{
		{
			name: "failed batch with an open task ends with its trigger",
			flow: planWithChecks(batch),
			rows: wakeRows([]store.EpicTask{claimedBy(1), openTask(2, 1)}, composedRun(1)),
			reg:  integrateKinds(scripted{0: {Kind: kinds.ActEnd, Outcome: "failed", Output: "Done gate lint failed"}}),
			want: []Action{plan, {
				Kind: ActBatch, Path: batchPath, Attempt: 1, Outcome: "failed", Output: "Done gate lint failed",
				Trigger: batchTrigger("failed", "Done gate lint failed"), Run: composedRun(1),
			}},
		},
	})
}
