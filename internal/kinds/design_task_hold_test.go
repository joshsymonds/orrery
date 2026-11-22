package kinds

import (
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// The Results an ended integrate run of rollingBatch records for the design tests: one composing both design tasks 1
// and 2, and one composing task 1 and task 3, the dependency of task 2.
const (
	designsComposed = `{"base":"base-1","candidate":"cand-1",` +
		`"composed":[{"task":1,"commit":"c1"},{"task":2,"commit":"c2"}],"leftOut":[]}`
	heldComposed = `{"base":"base-1","candidate":"cand-1",` +
		`"composed":[{"task":1,"commit":"c1"},{"task":3,"commit":"c3"}],"leftOut":[]}`
)

// TestDesignTaskHoldWaitsUntilIntegrated checks that a task that has not started waits, while a design task planned in
// its turn is pending, and runs once every such design task is integrated. A design task is held by no design task, and
// a started task is not held.
func TestDesignTaskHoldWaitsUntilIntegrated(t *testing.T) {
	t.Parallel()
	design := store.EpicTask{Seq: 1, Turn: 1, Status: taskOpen, Kind: store.TaskKindDesign}
	building := store.EpicTask{Seq: 2, Turn: 1, Status: taskOpen}
	claimed := []store.EpicTask{
		{Seq: 1, Turn: 1, Status: taskDone, Kind: store.TaskKindDesign, Batch: rollingBatch},
		building,
	}
	cases := []decideCase{
		{
			"pending design task gives none",
			rollingNode(2),
			rollingRows([]store.EpicTask{design, building}, nil),
			Action{Kind: ActNone},
		},
		{
			"design task claimed by a running integrate run gives none",
			rollingNode(2),
			rollingRows(claimed, []store.NodeRun{rollingBatchRun(store.NodeRunning, "")}),
			Action{Kind: ActNone},
		},
		{
			"design task integrated by an ended run that composed it runs",
			rollingNode(2),
			rollingRows(claimed, []store.NodeRun{rollingBatchRun(runEnded, rollingComposed)}),
			Action{Kind: ActRun},
		},
		{
			"integrated design task does not release a task while another design task is pending",
			rollingNode(3),
			rollingRows([]store.EpicTask{
				{Seq: 1, Turn: 1, Status: taskDone, Kind: store.TaskKindDesign, Batch: rollingBatch},
				{Seq: 2, Turn: 1, Status: taskOpen, Kind: store.TaskKindDesign},
				{Seq: 3, Turn: 1, Status: taskOpen},
			}, []store.NodeRun{rollingBatchRun(runEnded, rollingComposed)}),
			Action{Kind: ActNone},
		},
		{
			"every design task of the turn integrated runs",
			rollingNode(3),
			rollingRows([]store.EpicTask{
				{Seq: 1, Turn: 1, Status: taskDone, Kind: store.TaskKindDesign, Batch: rollingBatch},
				{Seq: 2, Turn: 1, Status: taskDone, Kind: store.TaskKindDesign, Batch: rollingBatch},
				{Seq: 3, Turn: 1, Status: taskOpen},
			}, []store.NodeRun{rollingBatchRun(runEnded, designsComposed)}),
			Action{Kind: ActRun},
		},
		{
			"started task is not held by a pending design task",
			rollingNode(2),
			rollingRows([]store.EpicTask{
				design,
				{Seq: 2, Turn: 1, Status: taskOpen, Build: store.TaskBuild{Stage: "implement"}},
			}, nil),
			Action{Kind: ActRun},
		},
		{
			"design task is not held by another design task",
			rollingNode(2),
			rollingRows([]store.EpicTask{
				design,
				{Seq: 2, Turn: 1, Status: taskOpen, Kind: store.TaskKindDesign},
			}, nil),
			Action{Kind: ActRun},
		},
	}
	checkDecisions(t, Build{}, cases)
}

// TestDesignTaskHoldKeepsOwnDependsOn checks that a task held by an integrated design task still waits on its own
// depends_on, and that a pending design task holds it before its depends_on is read.
func TestDesignTaskHoldKeepsOwnDependsOn(t *testing.T) {
	t.Parallel()
	integratedDesign := store.EpicTask{
		Seq: 1, Turn: 1, Status: taskDone, Kind: store.TaskKindDesign, Batch: rollingBatch,
	}
	cases := []decideCase{
		{
			"integrated design task with a pending dependency gives none",
			rollingNode(2),
			rollingRows([]store.EpicTask{
				integratedDesign,
				{Seq: 2, Turn: 1, Status: taskOpen, DependsOn: []int64{3}},
				{Seq: 3, Turn: 1, Status: taskOpen},
			}, []store.NodeRun{rollingBatchRun(runEnded, rollingComposed)}),
			Action{Kind: ActNone},
		},
		{
			"integrated design task with a lost dependency gaps the build",
			rollingNode(2),
			rollingRows([]store.EpicTask{
				integratedDesign,
				{Seq: 2, Turn: 1, Status: taskOpen, DependsOn: []int64{3}},
				{Seq: 3, Turn: 1, Status: taskFailed},
			}, []store.NodeRun{rollingBatchRun(runEnded, rollingComposed)}),
			Action{Kind: ActGap, Reason: "dependency T3 failed"},
		},
		{
			"pending design task holds the build before a lost dependency gaps it",
			rollingNode(2),
			rollingRows([]store.EpicTask{
				{Seq: 1, Turn: 1, Status: taskOpen, Kind: store.TaskKindDesign},
				{Seq: 2, Turn: 1, Status: taskOpen, DependsOn: []int64{3}},
				{Seq: 3, Turn: 1, Status: taskFailed},
			}, nil),
			Action{Kind: ActNone},
		},
		{
			"integrated design task and integrated dependency run",
			rollingNode(2),
			rollingRows([]store.EpicTask{
				integratedDesign,
				{Seq: 2, Turn: 1, Status: taskOpen, DependsOn: []int64{3}},
				{Seq: 3, Turn: 1, Status: taskDone, Batch: rollingBatch},
			}, []store.NodeRun{rollingBatchRun(runEnded, heldComposed)}),
			Action{Kind: ActRun},
		},
	}
	checkDecisions(t, Build{}, cases)
}

// TestDesignTaskHoldNoDesignTaskStartsAsBefore checks that a task whose turn planned no design task starts as it did
// before, and that a design task planned in another turn holds nothing.
func TestDesignTaskHoldNoDesignTaskStartsAsBefore(t *testing.T) {
	t.Parallel()
	cases := []decideCase{
		{
			"task of a turn with no design task runs",
			rollingNode(1),
			rollingRows([]store.EpicTask{{Seq: 1, Turn: 1, Status: taskOpen}}, nil),
			Action{Kind: ActRun},
		},
		{
			"design task of another turn holds nothing",
			rollingNode(2),
			rollingRows([]store.EpicTask{
				{Seq: 1, Turn: 2, Status: taskOpen, Kind: store.TaskKindDesign},
				{Seq: 2, Turn: 1, Status: taskOpen},
			}, nil),
			Action{Kind: ActRun},
		},
		{
			"task of a turn with no design task waits on its dependency as before",
			rollingNode(2),
			rollingRows([]store.EpicTask{
				{Seq: 1, Turn: 2, Status: taskOpen, Kind: store.TaskKindDesign},
				{Seq: 2, Turn: 1, Status: taskOpen, DependsOn: []int64{3}},
				{Seq: 3, Turn: 1, Status: taskOpen},
			}, nil),
			Action{Kind: ActNone},
		},
	}
	checkDecisions(t, Build{}, cases)
}

// TestDesignTaskHoldLostDesignEndsHeldAsGap checks that a design task that failed, ended as a gap or was stranded ends
// each task it held as a gap naming it, as a lost dependency does, whatever the task's own depends_on holds.
func TestDesignTaskHoldLostDesignEndsHeldAsGap(t *testing.T) {
	t.Parallel()
	stranded := rollingRows(
		[]store.EpicTask{
			{Seq: 1, Turn: 1, Status: taskDone, Kind: store.TaskKindDesign, Batch: rollingBatch},
			{Seq: 2, Turn: 1, Status: taskOpen},
		},
		[]store.NodeRun{rollingFailedBatch(rollingComposed)},
	)
	stranded.Stranded = []int64{1}
	cases := []decideCase{
		{
			"failed design task gaps the task it held",
			rollingNode(2),
			rollingRows([]store.EpicTask{
				{Seq: 1, Turn: 1, Status: taskFailed, Kind: store.TaskKindDesign},
				{Seq: 2, Turn: 1, Status: taskOpen},
			}, nil),
			Action{Kind: ActGap, Reason: "dependency T1 failed"},
		},
		{
			"design task that ended as a gap gaps the task it held",
			rollingNode(2),
			rollingRows([]store.EpicTask{
				{Seq: 1, Turn: 1, Status: taskGap, Kind: store.TaskKindDesign},
				{Seq: 2, Turn: 1, Status: taskOpen},
			}, nil),
			Action{Kind: ActGap, Reason: "dependency T1 ended as a gap"},
		},
		{
			"design task stranded by a failed batch gaps the task it held",
			rollingNode(2),
			stranded,
			Action{
				Kind:   ActGap,
				Reason: "dependency T1 was composed by a failed batch whose work the base does not hold",
			},
		},
		{
			"every task the lost design task held gaps naming it",
			rollingNode(3),
			rollingRows([]store.EpicTask{
				{Seq: 1, Turn: 1, Status: taskFailed, Kind: store.TaskKindDesign},
				{Seq: 2, Turn: 1, Status: taskOpen},
				{Seq: 3, Turn: 1, Status: taskOpen},
			}, nil),
			Action{Kind: ActGap, Reason: "dependency T1 failed"},
		},
		{
			"first lost design task in seq order names the reason",
			rollingNode(4),
			rollingRows([]store.EpicTask{
				{Seq: 1, Turn: 1, Status: taskOpen, Kind: store.TaskKindDesign},
				{Seq: 2, Turn: 1, Status: taskFailed, Kind: store.TaskKindDesign},
				{Seq: 3, Turn: 1, Status: taskGap, Kind: store.TaskKindDesign},
				{Seq: 4, Turn: 1, Status: taskOpen},
			}, nil),
			Action{Kind: ActGap, Reason: "dependency T2 failed"},
		},
		{
			"lost design task gaps the build ahead of a pending dependency",
			rollingNode(2),
			rollingRows([]store.EpicTask{
				{Seq: 1, Turn: 1, Status: taskGap, Kind: store.TaskKindDesign},
				{Seq: 2, Turn: 1, Status: taskOpen, DependsOn: []int64{3}},
				{Seq: 3, Turn: 1, Status: taskOpen},
			}, nil),
			Action{Kind: ActGap, Reason: "dependency T1 ended as a gap"},
		},
	}
	checkDecisions(t, Build{}, cases)
}
