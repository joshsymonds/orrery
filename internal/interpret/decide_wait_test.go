package interpret

import (
	"slices"
	"testing"

	"github.com/joshsymonds/orrery/internal/build"
	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/workflow"
)

// latePath is the path of the integrate run of the late walk of task 2 of the plan at "plan".
const latePath = "plan/L2/integrate"

// waitingTask returns open task seq of turn 1 whose build waits on after.
func waitingTask(seq int64, after string) store.EpicTask {
	task := openTask(seq, 1)
	task.Build = store.TaskBuild{Stage: build.StageImplement, Held: build.HeldWaiting, After: after}
	return task
}

// claimed returns done task seq of turn 1 claimed by the batch at path.
func claimed(seq int64, path string) store.EpicTask {
	task := doneTask(seq, 1)
	task.Batch = path
	return task
}

// waited returns done task 2 of turn 1 that waited on after and no batch has claimed.
func waited(after string) store.EpicTask {
	task := doneTask(2, 1)
	task.Build.After = after
	return task
}

// startsAt reports whether acts start a node run at path.
func startsAt(acts []Action, path string) bool {
	return slices.ContainsFunc(acts, func(act Action) bool {
		return act.Kind == ActStart && slices.ContainsFunc(act.Runs, func(run store.NodeRun) bool {
			return run.Path == path
		})
	})
}

// ends reports whether acts end the plan.
func endsPlan(acts []Action) bool {
	return slices.ContainsFunc(acts, func(act Action) bool {
		return slices.ContainsFunc(act.Ends, func(end store.NodeEnd) bool { return end.Path == "plan" })
	})
}

func TestDecideSettleWalkDoesNotWaitForAWaitingTask(t *testing.T) {
	t.Parallel()
	flow := workflow.Workflow{Name: "decide", Nodes: planWithChecks(batchNode())}
	turns := []store.EpicTurn{turnAt(1, "work")}
	child := running(childOf("plan/build/T2", 0, 2, 1, 2))
	reg := integrateKinds(scripted{})
	waitingRows := rowsOf(turns, []store.EpicTask{doneTask(1, 1), waitingTask(2, "1")}, runningPlan(0), child)
	if acts := Decide(flow, waitingRows, reg); !startsAt(acts, batchPath) {
		t.Errorf("with T2 waiting on T1, Decide = %+v, want the integrate walk at %s started", acts, batchPath)
	}
	building := openTask(2, 1)
	building.Build = store.TaskBuild{Stage: build.StageImplement, After: "1"}
	buildingRows := rowsOf(turns, []store.EpicTask{doneTask(1, 1), building}, runningPlan(0), child)
	if acts := Decide(flow, buildingRows, reg); startsAt(acts, batchPath) {
		t.Errorf("with T2 building, Decide = %+v, want no integrate walk", acts)
	}
	asked := openTask(2, 1)
	asked.Build = store.TaskBuild{Stage: build.StageOrchestrate, Held: build.HeldAsked, Question: 4}
	askedRows := rowsOf(turns, []store.EpicTask{doneTask(1, 1), asked}, runningPlan(0), child)
	if acts := Decide(flow, askedRows, reg); startsAt(acts, batchPath) {
		t.Errorf("with T2 waiting on the owner, Decide = %+v, want no integrate walk", acts)
	}
}

func TestDecideLateWalkIntegratesATaskAcceptedAfterTheLatestWalk(t *testing.T) {
	t.Parallel()
	flow := workflow.Workflow{Name: "decide", Nodes: planWithChecks(batchNode())}
	completeTurns := []store.EpicTurn{turnAt(1, "complete")}
	integrated := ended(batchRun("c1"), "passed")
	late := attempted(running(startOf(latePath, "integrate", "integrate", 0)), 1)
	reg := integrateKinds(scripted{0: {Kind: kinds.ActPrepare, Tasks: []int64{2}}})
	checked := planWithChecks(kindNode("check", "gate"), batchNode())
	cases := []struct {
		name      string
		nodes     []workflow.Node
		rows      kinds.Rows
		lateStart bool
		planEnds  bool
	}{
		{
			name: "a waited task accepted after the latest walk's batch starts a late walk",
			rows: rowsOf(completeTurns, []store.EpicTask{claimed(1, batchPath), waited("1")},
				runningPlan(0), integrated),
			lateStart: true,
		},
		{
			name: "a late walk waits while another task builds",
			rows: rowsOf(completeTurns, []store.EpicTask{claimed(1, batchPath), waited("1"), openTask(3, 1)},
				runningPlan(0), integrated, running(childOf("plan/build/T3", 0, 3, 1, 3))),
		},
		{
			name: "a late walk does not wait for a task that itself waits",
			rows: rowsOf(
				completeTurns,
				[]store.EpicTask{waitingTask(1, "2"), waited("3"), claimed(3, "plan/S3/integrate")},
				runningPlan(0),
				ended(startOf("plan/S3/integrate", "integrate", "integrate", 0), "passed"),
				running(childOf("plan/build/T1", 0, 1, 1, 1)),
			),
			lateStart: true,
		},
		{
			name: "a done task that never waited starts no late walk, and the plan ends",
			rows: rowsOf(completeTurns, []store.EpicTask{claimed(1, batchPath), doneTask(2, 1)},
				runningPlan(0), integrated),
			planEnds: true,
		},
		{
			name:  "no late walk starts when the latest walk's integrate node never ran",
			nodes: checked,
			rows: rowsOf(completeTurns, []store.EpicTask{doneTask(1, 1), waited("1")}, runningPlan(0),
				ended(startOf("plan/S2/check", "check", "gate", 0), "failed")),
			planEnds: true,
		},
		{
			name: "a running late walk keeps the plan from ending",
			rows: rowsOf(completeTurns, []store.EpicTask{claimed(1, batchPath), waited("1")},
				runningPlan(0), integrated, late),
		},
		{
			name: "an ended late walk that claimed its task lets the plan end",
			rows: rowsOf(completeTurns, []store.EpicTask{claimed(1, batchPath), func() store.EpicTask {
				task := waited("1")
				task.Batch = latePath
				return task
			}()}, runningPlan(0), integrated, ended(late, "passed")),
			planEnds: true,
		},
		{
			name:  "an ended late walk that did not claim its task starts no other",
			nodes: checked,
			rows: rowsOf(completeTurns, []store.EpicTask{claimed(1, batchPath), waited("1")},
				runningPlan(0), ended(startOf("plan/S2/check", "check", "gate", 0), "passed"), integrated,
				ended(startOf("plan/L2/check", "check", "gate", 0), "failed")),
			planEnds: true,
		},
	}
	for _, tc := range cases {
		nodes := tc.nodes
		if nodes == nil {
			nodes = flow.Nodes
		}
		acts := Decide(workflow.Workflow{Name: flow.Name, Nodes: nodes}, tc.rows, reg)
		if got := startsAt(acts, latePath); got != tc.lateStart {
			t.Errorf("%s: late walk started %v, want %v; actions %+v", tc.name, got, tc.lateStart, acts)
		}
		if got := endsPlan(acts); got != tc.planEnds {
			t.Errorf("%s: plan ended %v, want %v; actions %+v", tc.name, got, tc.planEnds, acts)
		}
	}
}

func TestDecideLateWalkRunsItsIntegrateNodeAsAnyWalkDoes(t *testing.T) {
	t.Parallel()
	late := attempted(running(startOf(latePath, "integrate", "integrate", 0)), 1)
	rows := rowsOf([]store.EpicTurn{turnAt(1, "work")}, []store.EpicTask{claimed(1, batchPath), waited("1")},
		runningPlan(0), ended(batchRun("c1"), "passed"), late)
	acts := Decide(workflow.Workflow{Name: "decide", Nodes: planWithChecks(batchNode())}, rows,
		integrateKinds(scripted{0: {Kind: kinds.ActPrepare, Tasks: []int64{2}}}))
	if !slices.ContainsFunc(acts, func(act Action) bool {
		return act.Kind == ActWork && act.Path == latePath && slices.Equal(act.Tasks, []int64{2})
	}) {
		t.Errorf("Decide = %+v, want the late walk's integrate run to prepare its batch of T2", acts)
	}
}

// TestDecideLateWalkHoldsANewerSettleWalkAndIsDecidedEveryPass: while late walk L3 runs, a turn adds T7, which is
// accepted. The settle walk at S7 does not start until L3 has ended, and L3's integrate run is decided on each pass
// meanwhile, so the two batches never race onto the branch.
func TestDecideLateWalkHoldsANewerSettleWalkAndIsDecidedEveryPass(t *testing.T) {
	t.Parallel()
	const late3 = "plan/L3/integrate"
	flow := workflow.Workflow{Name: "decide", Nodes: planWithChecks(batchNode())}
	turns := []store.EpicTurn{turnAt(1, "work"), turnAt(2, "work")}
	three := doneTask(3, 1)
	three.Build.After = "2"
	seven := doneTask(7, 2)
	running3 := attempted(running(startOf(late3, "integrate", "integrate", 0)), 1)
	settled3 := ended(startOf("plan/S3/integrate", "integrate", "integrate", 0), "passed")
	reg := integrateKinds(scripted{0: {Kind: kinds.ActPrepare, Tasks: []int64{3}}})
	rows := rowsOf(turns, []store.EpicTask{claimed(2, "plan/S3/integrate"), three, seven}, runningPlan(0),
		settled3, running3)
	acts := Decide(flow, rows, reg)
	if startsAt(acts, "plan/S7/integrate") {
		t.Errorf("S7 started while L3 runs; actions %+v", acts)
	}
	if !slices.ContainsFunc(acts, func(act Action) bool { return act.Kind == ActWork && act.Path == late3 }) {
		t.Errorf("L3 was not decided while S7 waits; actions %+v", acts)
	}
	rows = rowsOf(turns, []store.EpicTask{claimed(2, "plan/S3/integrate"), claimed(3, late3), seven}, runningPlan(0),
		settled3, ended(running3, "passed"))
	if acts := Decide(flow, rows, reg); !startsAt(acts, "plan/S7/integrate") {
		t.Errorf("S7 did not start once L3 ended; actions %+v", acts)
	}
}
