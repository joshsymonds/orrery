package kinds

import (
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// rollingBatch is the path of the integrate run that claims the tasks the rolling tests build on.
const rollingBatch = "plan/S2/integrate"

// The Results an ended integrate run of rollingBatch records: one composing task 1, one composing task 2 and leaving
// task 1 out, and one that does not decode, its composed list being a string.
const (
	rollingComposed = `{"base":"base-1","candidate":"cand-1","composed":[{"task":1,"commit":"c1"}],"leftOut":[]}`
	rollingLeftOut  = `{"base":"base-1","candidate":"cand-2","composed":[{"task":2,"commit":"c2"}],` +
		`"leftOut":[{"task":1,"reason":"T1 overlaps T2 at a.go"}]}`
	rollingUndecodable = `{"base":"base-1","candidate":"cand-1","composed":"task 1"}`
)

// rollingNode returns the build run of the task with seq.
func rollingNode(seq int64) Node {
	return Node{Run: store.NodeRun{ID: testRunID, Kind: kindBuild, Task: seq}}
}

// rollingRows returns the rows of an epic at base-1 with tasks and runs.
func rollingRows(tasks []store.EpicTask, runs []store.NodeRun) Rows {
	return Rows{Epic: store.EpicRow{Base: "base-1", Tasks: tasks}, Runs: runs}
}

// rollingBatchRun returns the integrate run of rollingBatch in state, with result.
func rollingBatchRun(state, result string) store.NodeRun {
	return store.NodeRun{ID: "batch-1", Path: rollingBatch, Kind: kindIntegrate, State: state, Result: result}
}

// TestRollingIntegrateBuildWaitsForDependencies checks that a task that has not started waits for its dependencies
// while one is pending, and runs once they are all integrated or it has no dependency.
func TestRollingIntegrateBuildWaitsForDependencies(t *testing.T) {
	t.Parallel()
	building := store.EpicTask{Seq: 2, Status: taskOpen, DependsOn: []int64{1}}
	claimed := []store.EpicTask{{Seq: 1, Status: taskDone, Batch: rollingBatch}, building}
	cases := []decideCase{
		{
			"open dependency gives none",
			rollingNode(2),
			rollingRows([]store.EpicTask{{Seq: 1, Status: taskOpen}, building}, nil),
			Action{Kind: ActNone},
		},
		{
			"done unclaimed dependency gives none",
			rollingNode(2),
			rollingRows([]store.EpicTask{{Seq: 1, Status: taskDone}, building}, nil),
			Action{Kind: ActNone},
		},
		{
			"dependency claimed by a running integrate run gives none",
			rollingNode(2),
			rollingRows(claimed, []store.NodeRun{rollingBatchRun(store.NodeRunning, "")}),
			Action{Kind: ActNone},
		},
		{
			"dependency claimed by a parked integrate run gives none",
			rollingNode(2),
			rollingRows(claimed, []store.NodeRun{rollingBatchRun(store.NodeParked, "")}),
			Action{Kind: ActNone},
		},
		{
			"dependency claimed by an ended run that composed it runs",
			rollingNode(2),
			rollingRows(claimed, []store.NodeRun{rollingBatchRun(runEnded, rollingComposed)}),
			Action{Kind: ActRun},
		},
		{
			"task with no dependency runs",
			rollingNode(2),
			rollingRows([]store.EpicTask{{Seq: 2, Status: taskOpen}}, nil),
			Action{Kind: ActRun},
		},
		{
			"started task with a pending dependency runs",
			rollingNode(2),
			rollingRows([]store.EpicTask{
				{Seq: 1, Status: taskOpen},
				{Seq: 2, Status: taskOpen, DependsOn: []int64{1}, Build: store.TaskBuild{Stage: "implement"}},
			}, nil),
			Action{Kind: ActRun},
		},
	}
	checkDecisions(t, Build{}, cases)
}

// TestRollingIntegrateBuildGapsOnLostDependency checks that a task that has not started gives a gap naming its first
// lost dependency in DependsOn order, lost whatever the others are doing.
func TestRollingIntegrateBuildGapsOnLostDependency(t *testing.T) {
	t.Parallel()
	building := store.EpicTask{Seq: 2, Status: taskOpen, DependsOn: []int64{1}}
	claimed := []store.EpicTask{{Seq: 1, Status: taskDone, Batch: rollingBatch}, building}
	cases := []decideCase{
		{
			"failed dependency gaps the build",
			rollingNode(2),
			rollingRows([]store.EpicTask{{Seq: 1, Status: taskFailed}, building}, nil),
			Action{Kind: ActGap, Reason: "dependency T1 failed"},
		},
		{
			"dependency that ended as a gap gaps the build",
			rollingNode(2),
			rollingRows([]store.EpicTask{{Seq: 1, Status: taskGap}, building}, nil),
			Action{Kind: ActGap, Reason: "dependency T1 ended as a gap"},
		},
		{
			"dependency left out of its ended batch gaps the build",
			rollingNode(2),
			rollingRows(
				[]store.EpicTask{{Seq: 1, Status: taskDone, Batch: rollingBatch}, building},
				[]store.NodeRun{rollingBatchRun(runEnded, rollingLeftOut)},
			),
			Action{Kind: ActGap, Reason: "dependency T1 was left out of its batch"},
		},
		{
			"dependency claimed by an ended run whose result does not decode gaps the build",
			rollingNode(2),
			rollingRows(claimed, []store.NodeRun{rollingBatchRun(runEnded, rollingUndecodable)}),
			Action{Kind: ActGap, Reason: "dependency T1 was claimed by a batch whose result does not decode"},
		},
		{
			"first lost dependency in DependsOn order names the reason",
			rollingNode(3),
			rollingRows([]store.EpicTask{
				{Seq: 1, Status: taskFailed},
				{Seq: 2, Status: taskGap},
				{Seq: 3, Status: taskOpen, DependsOn: []int64{2, 1}},
			}, nil),
			Action{Kind: ActGap, Reason: "dependency T2 ended as a gap"},
		},
		{
			"lost dependency gaps past a pending one",
			rollingNode(3),
			rollingRows([]store.EpicTask{
				{Seq: 1, Status: taskOpen},
				{Seq: 2, Status: taskFailed},
				{Seq: 3, Status: taskOpen, DependsOn: []int64{1, 2}},
			}, nil),
			Action{Kind: ActGap, Reason: "dependency T2 failed"},
		},
	}
	checkDecisions(t, Build{}, cases)
}
