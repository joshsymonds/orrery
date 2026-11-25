package kinds

import (
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// The Result an ended integrate run of rollingBatch records for the design tests: one composing task 1, the design task,
// and task 3, the other dependency of task 2.
const designDepsComposed = `{"base":"base-1","candidate":"cand-1",` +
	`"composed":[{"task":1,"commit":"c1"},{"task":3,"commit":"c3"}],"leftOut":[]}`

// TestDesignTaskDepsTaskWithNoDependsOnRuns checks that a design task holds no task that does not name it: a task with
// no depends_on runs while a design task of its turn is pending, whichever turn planned the design task.
func TestDesignTaskDepsTaskWithNoDependsOnRuns(t *testing.T) {
	t.Parallel()
	cases := []decideCase{
		{
			"task with no depends_on runs beside a pending design task of its turn",
			rollingNode(2),
			rollingRows([]store.EpicTask{
				{Seq: 1, Turn: 1, Status: taskOpen, Kind: store.TaskKindDesign},
				{Seq: 2, Turn: 1, Status: taskOpen},
			}, nil),
			Action{Kind: ActRun},
		},
		{
			"task with no depends_on runs beside a failed design task it does not name",
			rollingNode(2),
			rollingRows([]store.EpicTask{
				{Seq: 1, Turn: 1, Status: taskFailed, Kind: store.TaskKindDesign},
				{Seq: 2, Turn: 1, Status: taskOpen},
			}, nil),
			Action{Kind: ActRun},
		},
		{
			"design task with no depends_on runs beside another pending design task",
			rollingNode(2),
			rollingRows([]store.EpicTask{
				{Seq: 1, Turn: 1, Status: taskOpen, Kind: store.TaskKindDesign},
				{Seq: 2, Turn: 1, Status: taskOpen, Kind: store.TaskKindDesign},
			}, nil),
			Action{Kind: ActRun},
		},
	}
	checkDecisions(t, Build{}, cases)
}

// TestDesignTaskDepsDesignDependingOnATaskOfItsTurn checks the plan that once deadlocked: a design task that depends on
// a task of its own turn waits for that task, and that task, naming nothing, runs at once.
func TestDesignTaskDepsDesignDependingOnATaskOfItsTurn(t *testing.T) {
	t.Parallel()
	tasks := []store.EpicTask{
		{Seq: 1, Turn: 1, Status: taskOpen},
		{Seq: 2, Turn: 1, Status: taskOpen, Kind: store.TaskKindDesign, DependsOn: []int64{1}},
		{Seq: 3, Turn: 1, Status: taskOpen, DependsOn: []int64{2}},
	}
	cases := []decideCase{
		{"the task the design task depends on runs", rollingNode(1), rollingRows(tasks, nil), Action{Kind: ActRun}},
		{"the design task waits for it", rollingNode(2), rollingRows(tasks, nil), Action{Kind: ActNone}},
		{"a task naming the design task waits for it", rollingNode(3), rollingRows(tasks, nil), Action{Kind: ActNone}},
	}
	checkDecisions(t, Build{}, cases)
}

// TestDesignTaskDepsNamedDesignTaskHolds checks that a task naming a design task in depends_on waits until that design
// task is integrated, as it waits for any task it names, and also for its other dependencies.
func TestDesignTaskDepsNamedDesignTaskHolds(t *testing.T) {
	t.Parallel()
	integratedDesign := store.EpicTask{
		Seq: 1, Turn: 1, Status: taskDone, Kind: store.TaskKindDesign, Batch: rollingBatch,
	}
	cases := []decideCase{
		{
			"pending named design task gives none",
			rollingNode(2),
			rollingRows([]store.EpicTask{
				{Seq: 1, Turn: 1, Status: taskOpen, Kind: store.TaskKindDesign},
				{Seq: 2, Turn: 1, Status: taskOpen, DependsOn: []int64{1}},
			}, nil),
			Action{Kind: ActNone},
		},
		{
			"named design task claimed by a running integrate run gives none",
			rollingNode(2),
			rollingRows([]store.EpicTask{
				integratedDesign,
				{Seq: 2, Turn: 1, Status: taskOpen, DependsOn: []int64{1}},
			}, []store.NodeRun{rollingBatchRun(store.NodeRunning, "")}),
			Action{Kind: ActNone},
		},
		{
			"named design task integrated by an ended run that composed it runs",
			rollingNode(2),
			rollingRows([]store.EpicTask{
				integratedDesign,
				{Seq: 2, Turn: 1, Status: taskOpen, DependsOn: []int64{1}},
			}, []store.NodeRun{rollingBatchRun(runEnded, rollingComposed)}),
			Action{Kind: ActRun},
		},
		{
			"integrated named design task with a pending other dependency gives none",
			rollingNode(2),
			rollingRows([]store.EpicTask{
				integratedDesign,
				{Seq: 2, Turn: 1, Status: taskOpen, DependsOn: []int64{1, 3}},
				{Seq: 3, Turn: 1, Status: taskOpen},
			}, []store.NodeRun{rollingBatchRun(runEnded, rollingComposed)}),
			Action{Kind: ActNone},
		},
		{
			"integrated named design task and integrated other dependency run",
			rollingNode(2),
			rollingRows([]store.EpicTask{
				integratedDesign,
				{Seq: 2, Turn: 1, Status: taskOpen, DependsOn: []int64{1, 3}},
				{Seq: 3, Turn: 1, Status: taskDone, Batch: rollingBatch},
			}, []store.NodeRun{rollingBatchRun(runEnded, designDepsComposed)}),
			Action{Kind: ActRun},
		},
		{
			"started task naming a pending design task is not held",
			rollingNode(2),
			rollingRows([]store.EpicTask{
				{Seq: 1, Turn: 1, Status: taskOpen, Kind: store.TaskKindDesign},
				{Seq: 2, Turn: 1, Status: taskOpen, DependsOn: []int64{1}, Build: store.TaskBuild{Stage: "implement"}},
			}, nil),
			Action{Kind: ActRun},
		},
	}
	checkDecisions(t, Build{}, cases)
}

// TestDesignTaskDepsLostNamedDesignEndsAsGap checks that a design task that failed, ended as a gap or was stranded ends
// as a gap each task naming it in depends_on, as a lost dependency does.
func TestDesignTaskDepsLostNamedDesignEndsAsGap(t *testing.T) {
	t.Parallel()
	stranded := rollingRows(
		[]store.EpicTask{
			{Seq: 1, Turn: 1, Status: taskDone, Kind: store.TaskKindDesign, Batch: rollingBatch},
			{Seq: 2, Turn: 1, Status: taskOpen, DependsOn: []int64{1}},
		},
		[]store.NodeRun{rollingFailedBatch(rollingComposed)},
	)
	stranded.Stranded = []int64{1}
	cases := []decideCase{
		{
			"failed named design task gaps the task",
			rollingNode(2),
			rollingRows([]store.EpicTask{
				{Seq: 1, Turn: 1, Status: taskFailed, Kind: store.TaskKindDesign},
				{Seq: 2, Turn: 1, Status: taskOpen, DependsOn: []int64{1}},
			}, nil),
			Action{Kind: ActGap, Reason: "dependency T1 failed"},
		},
		{
			"named design task that ended as a gap gaps the task",
			rollingNode(2),
			rollingRows([]store.EpicTask{
				{Seq: 1, Turn: 1, Status: taskGap, Kind: store.TaskKindDesign},
				{Seq: 2, Turn: 1, Status: taskOpen, DependsOn: []int64{1}},
			}, nil),
			Action{Kind: ActGap, Reason: "dependency T1 ended as a gap"},
		},
		{
			"named design task stranded by a failed batch gaps the task",
			rollingNode(2),
			stranded,
			Action{
				Kind:   ActGap,
				Reason: "dependency T1 was composed by a failed batch whose work the base does not hold",
			},
		},
	}
	checkDecisions(t, Build{}, cases)
}
