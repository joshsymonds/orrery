package interpret

import (
	"testing"

	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/workflow"
)

// fixLoopPath is the path of the integrate run of the first round's plan of fixLoop, under its settle point 2: the
// run a retry set running again after that plan ended.
const fixLoopPath = "refine@1/plan/S2/integrate"

// fixLoop returns a loop of two rounds whose body is a plan that builds and then integrates, as the built-in epic's
// correct loop and its fix plan are.
func fixLoop() workflow.Node {
	settings := workflow.Loop{Max: 2, Until: workflow.Outcome{Node: "plan", Name: "built"}, Spent: "park"}
	return loopNode(settings, planNodeWithChecks(batchNode()))
}

// fixLoopRows returns the rows of fixLoop in its second round: the first round's plan ended complete after turn 2, the
// second round's plan runs from mark 2, and the runs given.
func fixLoopRows(runs ...store.NodeRun) kinds.Rows {
	return rowsOf(
		[]store.EpicTurn{turnAt(1, "work"), turnAt(2, "complete")},
		[]store.EpicTask{doneTask(1, 1), doneTask(2, 1)},
		append([]store.NodeRun{
			running(loopOf(2, 0)),
			ended(startOf("refine@1/plan", "plan", "plan", 1), "complete"),
			withMark(running(startOf("refine@2/plan", "plan", "plan", 2)), 2),
		}, runs...)...,
	)
}

// orphanBatch returns the integrate run at fixLoopPath, running at attempt 1 on revision c1 in round 1.
func orphanBatch() store.NodeRun {
	run := attempted(running(startOf(fixLoopPath, "integrate", "integrate", 1)), 1)
	run.Revision = "c1"
	return run
}

// afterFlow returns a plan that builds and integrates, followed by the node after of kind after.
func afterFlow() []workflow.Node {
	return []workflow.Node{planNodeWithChecks(batchNode()), kindNode("after", "after")}
}

// afterRows returns the rows of afterFlow once its plan ended complete and its after node runs, with the runs given.
func afterRows(runs ...store.NodeRun) kinds.Rows {
	return rowsOf(
		[]store.EpicTurn{turnAt(1, "complete")},
		[]store.EpicTask{doneTask(1, 1), doneTask(2, 1)},
		append([]store.NodeRun{
			ended(startOf("plan", "plan", "plan", 0), "complete"),
			running(startOf("after", "after", "after", 0)),
		}, runs...)...,
	)
}

// afterKinds returns the kinds of afterFlow: batch as the integrate kind, and an after kind that ends passed.
func afterKinds(batch kinds.Kind) map[string]kinds.Kind {
	reg := integrateKinds(batch)
	reg["after"] = scripted{0: {Kind: kinds.ActEnd, Outcome: "passed"}}
	return reg
}

// TestDecidePlanEndsOnlyWithItsBodyAtRest: a plan whose kind would end it waits while the planner action its last turn
// returned beside complete is pending, and while a run of an older settle walk runs, so no run under it is left to run
// once it has ended.
func TestDecidePlanEndsOnlyWithItsBodyAtRest(t *testing.T) {
	t.Parallel()
	complete := []store.EpicTurn{turnAt(1, "work"), turnAt(2, "complete")}
	pendingAction := rowsOf(complete, []store.EpicTask{doneTask(1, 1), doneTask(2, 2)}, runningPlan(1),
		ended(attempted(startOf("plan/S2/integrate", "integrate", "integrate", 0), 1), "failed"),
	)
	pendingAction.Actions = 1
	decideCases(t, []decideCase{
		{
			name: "a complete plan waits while its turn's retry of the failed batch is pending",
			flow: planWithChecks(batchNode()),
			rows: pendingAction,
			reg:  integrateKinds(scripted{}),
			want: []Action{{Kind: ActRun, Path: "plan", NodeKind: "plan"}},
		},
		{
			name: "a complete plan waits while a check of an older settle walk runs",
			flow: planWithChecks(kindNode("check", "gate")),
			rows: rowsOf(complete, []store.EpicTask{doneTask(1, 1), doneTask(2, 2)}, runningPlan(0),
				attempted(running(startOf("plan/S1/check", "check", "gate", 0)), 1),
				ended(startOf("plan/S2/check", "check", "gate", 0), "passed"),
			),
			reg:  planKinds(scripted{}),
			want: []Action{{Kind: ActRun, Path: "plan", NodeKind: "plan"}},
		},
	})
}

// TestDecideOrphanedRuns: a run that runs under a run that has ended, as a retry of a failed batch under an ended plan
// leaves it, is decided by its kind as its plan would have decided it, and the walk waits while it runs. A run with
// nothing to decide it ends. A parked run under an ended plan is no orphan.
func TestDecideOrphanedRuns(t *testing.T) {
	t.Parallel()
	batch := batchNode()
	orphanTrigger := func(outcome, output string) *store.NodeTrigger {
		return &store.NodeTrigger{
			ID:    "workflow:" + store.NodeRunID(decideEpic, fixLoopPath, 1, "integrated attempt 1"),
			Cause: "workflow node " + fixLoopPath + " " + outcome + ": " + output,
		}
	}
	decideCases(t, []decideCase{
		{
			name: "the rerun of a batch under the last round's ended plan starts its Done gate, and the walk waits",
			flow: []workflow.Node{fixLoop()},
			rows: fixLoopRows(orphanBatch()),
			reg: integrateKinds(scripted{1: {
				Kind: kinds.ActStart, IDs: []string{"done"}, Items: []string{"lint"},
			}}),
			want: []Action{{
				Kind: ActWork, Work: kinds.ActStart, IDs: []string{"done"}, Items: []string{"lint"}, Path: fixLoopPath,
				NodeKind: "integrate", Def: batch, Run: orphanBatch(),
			}},
		},
		{
			name: "the rerun's advance carries the trigger its passed batch writes",
			flow: []workflow.Node{fixLoop()},
			rows: fixLoopRows(orphanBatch()),
			reg:  integrateKinds(scripted{1: {Kind: kinds.ActAdvance, Output: "R1 green"}}),
			want: []Action{{
				Kind: ActWork, Work: kinds.ActAdvance, Output: "R1 green", Trigger: orphanTrigger("passed", "R1 green"),
				Path: fixLoopPath, NodeKind: "integrate", Def: batch, Run: orphanBatch(),
			}},
		},
		{
			name: "the rerun's end is its batch, which wakes the planner",
			flow: []workflow.Node{fixLoop()},
			rows: fixLoopRows(orphanBatch()),
			reg:  integrateKinds(scripted{1: {Kind: kinds.ActEnd, Outcome: "failed", Output: "Done gate lint failed"}}),
			want: []Action{{
				Kind: ActBatch, Path: fixLoopPath, Attempt: 1, Outcome: "failed", Output: "Done gate lint failed",
				Trigger: orphanTrigger("failed", "Done gate lint failed"), Run: orphanBatch(),
			}},
		},
		{
			name: "a waiting rerun holds the walk with no action",
			flow: afterFlow(),
			rows: afterRows(batchRun("c1")),
			reg:  afterKinds(scripted{}),
			want: []Action{},
		},
		{
			name: "a failed check rerun under an ended plan ends with the trigger that wakes the planner",
			flow: planWithChecks(kindNode("check", "gate")),
			rows: rowsOf([]store.EpicTurn{turnAt(1, "complete")}, []store.EpicTask{doneTask(2, 1)},
				ended(startOf("plan", "plan", "plan", 0), "complete"),
				attempted(running(startOf("plan/S2/check", "check", "gate", 0)), 1),
			),
			reg: planKinds(scripted{0: {Kind: kinds.ActEnd, Outcome: "failed", Output: "boom"}}),
			want: []Action{{
				Kind: ActEnd,
				Ends: []store.NodeEnd{{Path: "plan/S2/check", Outcome: "failed", Output: "boom"}},
				Trigger: &store.NodeTrigger{
					ID:    "workflow:" + store.NodeRunID(decideEpic, "plan/S2/check", 0, "failed attempt 1"),
					Cause: "workflow node plan/S2/check failed: boom",
				},
			}},
		},
		{
			name: "a build child running under an ended plan ends with no outcome",
			flow: afterFlow(),
			rows: afterRows(running(childOf("plan/build/T2", 0, 2, 1, 2))),
			reg:  afterKinds(scripted{}),
			want: []Action{{Kind: ActEnd, Ends: []store.NodeEnd{{Path: "plan/build/T2"}}}},
		},
		{
			name: "a parked batch under an ended plan is no orphan, so the walk goes on",
			flow: afterFlow(),
			rows: afterRows(parked(batchRun("c1"))),
			reg:  afterKinds(scripted{}),
			want: []Action{{Kind: ActEnd, Ends: []store.NodeEnd{{Path: "after", Outcome: "passed"}}}},
		},
	})
}
