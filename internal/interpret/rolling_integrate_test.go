package interpret

import (
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/workflow"
)

// rollingBatchPath is the path of the integrate run of the settle walk of T1 under the plan at "plan".
const rollingBatchPath = "plan/S1/integrate"

// rollingFlow is the flow of the rolling integrate cases: one plan whose body builds each task and then integrates.
func rollingFlow() workflow.Workflow {
	return workflow.Workflow{Name: "decide", Nodes: planWithChecks(batchNode())}
}

// rollingBatch returns the integrate run at rollingBatchPath, at attempt 1, running.
func rollingBatch() store.NodeRun {
	return attempted(running(startOf(rollingBatchPath, "integrate", "integrate", 0)), 1)
}

// TestRollingIntegrateStartsWhileATaskBuilds: T1 is done and no batch claims it, and T2 is still open. The walk of T1
// starts at its settle point, and its integrate run is under the plan, while T2 starts its build.
func TestRollingIntegrateStartsWhileATaskBuilds(t *testing.T) {
	t.Parallel()
	rows := rowsOf([]store.EpicTurn{turnAt(1, "work")}, []store.EpicTask{doneTask(1, 1), openTask(2, 1)},
		runningPlan(0))
	acts := Decide(rollingFlow(), rows, integrateKinds(scripted{}))
	if !startsAt(acts, rollingBatchPath) {
		t.Errorf("Decide = %+v, want the integrate walk at %s started while T2 builds", acts, rollingBatchPath)
	}
	if !startsAt(acts, "plan/build/T2") {
		t.Errorf("Decide = %+v, want T2's build started alongside the walk", acts)
	}
}

// TestRollingIntegrateOneRunAtATime: an integrate run under the plan that runs or is parked holds every new walk, even
// while a done task is unclaimed. The stray runs are integrate runs under the plan that no task's walk started, so only
// the rule that one run holds the plan stops the walk. Once the run has ended, the walk of that task starts.
func TestRollingIntegrateOneRunAtATime(t *testing.T) {
	t.Parallel()
	turns := []store.EpicTurn{turnAt(1, "work")}
	tasks := []store.EpicTask{claimed(1, rollingBatchPath), doneTask(2, 1)}
	const stray = "plan/S9/integrate"
	strayBatch := attempted(running(startOf(stray, "integrate", "integrate", 0)), 1)
	strayTasks := []store.EpicTask{claimed(1, stray), doneTask(2, 1)}
	cases := []struct {
		name  string
		tasks []store.EpicTask
		batch store.NodeRun
	}{
		{name: "a running integrate run of a walk", tasks: tasks, batch: rollingBatch()},
		{name: "a parked integrate run of a walk", tasks: tasks, batch: parked(rollingBatch())},
		{name: "a running integrate run no walk started", tasks: strayTasks, batch: strayBatch},
		{name: "a parked integrate run no walk started", tasks: strayTasks, batch: parked(strayBatch)},
	}
	for _, tc := range cases {
		acts := Decide(rollingFlow(), rowsOf(turns, tc.tasks, runningPlan(0), tc.batch), integrateKinds(scripted{}))
		if startsIntegrate(acts) {
			t.Errorf("%s: Decide = %+v, want no new walk started while it holds the plan", tc.name, acts)
		}
	}
	finished := ended(rollingBatch(), outcomePassed)
	acts := Decide(rollingFlow(), rowsOf(turns, tasks, runningPlan(0), finished), integrateKinds(scripted{}))
	if !startsAt(acts, "plan/S2/integrate") {
		t.Errorf("with the integrate run ended, Decide = %+v, want the walk of T2 started at %s", acts,
			"plan/S2/integrate")
	}
}

// TestRollingIntegrateRunningBatchHoldsNoStart: while an integrate run under the plan runs, an open task that has no
// child run is started, and an open task whose child ended is rebuilt.
func TestRollingIntegrateRunningBatchHoldsNoStart(t *testing.T) {
	t.Parallel()
	turns := []store.EpicTurn{turnAt(1, "work")}
	tasks := []store.EpicTask{claimed(1, rollingBatchPath), openTask(2, 1), openTask(3, 1)}
	runs := []store.NodeRun{
		runningPlan(0), rollingBatch(), ended(childOf("plan/build/T3", 0, 3, 1, 3), outcomePassed),
	}
	acts := Decide(rollingFlow(), rowsOf(turns, tasks, runs...), integrateKinds(scripted{}))
	if !startsAt(acts, "plan/build/T2") {
		t.Errorf("Decide = %+v, want T2, which has no child run, started while the batch runs", acts)
	}
	rebuilt := false
	for _, act := range acts {
		if act.Kind == ActRebuild && act.Path == "plan/build/T3" && act.Task == 3 {
			rebuilt = true
		}
	}
	if !rebuilt {
		t.Errorf("Decide = %+v, want T3, whose child run ended, rebuilt while the batch runs", acts)
	}
}

// TestRollingIntegrateSettles: the plan settles once no walk is unfinished and no done task of the plan is unclaimed,
// and the kind of the plan then ends it. A done task no batch claims keeps it from settling, and so does a walk that
// runs.
func TestRollingIntegrateSettles(t *testing.T) {
	t.Parallel()
	turns := []store.EpicTurn{turnAt(1, "work"), turnAt(2, "complete")}
	claimedT2 := doneTask(2, 2)
	claimedT2.Batch = batchPath
	cases := []struct {
		name  string
		tasks []store.EpicTask
		runs  []store.NodeRun
		ends  bool
	}{
		{
			name:  "settled with no unfinished walk and every done task claimed",
			tasks: []store.EpicTask{doneTask(1, 1), claimedT2},
			runs:  []store.NodeRun{ended(batchRun("c1"), outcomePassed)},
			ends:  true,
		},
		{
			name:  "not settled while a done task is unclaimed",
			tasks: []store.EpicTask{doneTask(1, 1), doneTask(2, 2)},
			runs:  []store.NodeRun{ended(batchRun("c1"), outcomePassed)},
		},
		{
			name:  "not settled while a walk is unfinished",
			tasks: []store.EpicTask{doneTask(1, 1), claimedT2},
			runs:  []store.NodeRun{batchRun("c1")},
		},
	}
	for _, tc := range cases {
		rows := rowsOf(turns, tc.tasks, append([]store.NodeRun{runningPlan(1)}, tc.runs...)...)
		acts := Decide(rollingFlow(), rows, integrateKinds(scripted{}))
		if got := endsPlan(acts); got != tc.ends {
			t.Errorf("%s: plan ended %v, want %v; actions %+v", tc.name, got, tc.ends, acts)
		}
	}
}
