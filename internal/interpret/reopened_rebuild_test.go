package interpret

import (
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// reopenedBuildPath is the path of the build child run of task 1 under the plan at "plan".
const reopenedBuildPath = "plan/build/T1"

// reopenedBuild returns the build child run of task 1, the first task of turn 1, at attempt 0 in state, as its fan-out
// started it.
func reopenedBuild(state string) store.NodeRun {
	run := childOf(reopenedBuildPath, 0, 1, 1, 1)
	run.State = state
	return run
}

// reopenedRebuild is the rebuild of the build child run of task 1, which ended at attempt 0.
func reopenedRebuild() Action {
	return Action{Kind: ActRebuild, Path: reopenedBuildPath, Task: 1}
}

// TestReopenedTaskRebuildRestartsEndedBuildRun checks that an open task whose build child run ended, as a send_back
// leaves a done task it reopens, gets a rebuild of that run under a running plan once its batch has ended, failed or
// passed. A task whose build child run is still running gets a run, and no rebuild.
func TestReopenedTaskRebuildRestartsEndedBuildRun(t *testing.T) {
	t.Parallel()
	turns := []store.EpicTurn{turnAt(1, "work")}
	reopened := []store.EpicTask{openTask(1, 1)}
	plan := Action{Kind: ActRun, Path: "plan", NodeKind: "plan"}
	decideCases(t, []decideCase{
		{
			name: "open task whose build run ended is rebuilt after its batch failed",
			flow: planWithChecks(batchNode()),
			rows: rowsOf(turns, reopened, runningPlan(0), reopenedBuild(store.NodeEnded),
				ended(batchRun("c1"), outcomeFailed)),
			reg:  integrateKinds(scripted{}),
			want: []Action{plan, reopenedRebuild()},
		},
		{
			name: "open task whose build run ended is rebuilt after its batch passed",
			flow: planWithChecks(batchNode()),
			rows: rowsOf(turns, reopened, runningPlan(0), reopenedBuild(store.NodeEnded),
				ended(batchRun("c1"), outcomePassed)),
			reg:  integrateKinds(scripted{}),
			want: []Action{plan, reopenedRebuild()},
		},
		{
			name: "open task whose build run runs gets a run and no rebuild",
			flow: planWithChecks(batchNode()),
			rows: rowsOf(turns, reopened, runningPlan(0), reopenedBuild(store.NodeRunning),
				ended(batchRun("c1"), outcomeFailed)),
			reg: integrateKinds(scripted{}),
			want: []Action{plan, {
				Kind: ActRun, Path: reopenedBuildPath, NodeKind: "build", Task: 1,
			}},
		},
		{
			name: "open task of a turn at the plan's mark is not rebuilt",
			flow: planWithChecks(batchNode()),
			rows: rowsOf(turns, reopened, runningPlan(1), reopenedBuild(store.NodeEnded),
				ended(batchRun("c1"), outcomeFailed)),
			reg:  integrateKinds(scripted{}),
			want: []Action{plan},
		},
	})
}

// TestReopenedTaskRebuildWaitsOnOpenBatch checks that an open task whose build child run ended gets no rebuild while an
// integrate run under its plan is running or parked, as no task starts while a batch holds the fan-out.
func TestReopenedTaskRebuildWaitsOnOpenBatch(t *testing.T) {
	t.Parallel()
	turns := []store.EpicTurn{turnAt(1, "work")}
	reopened := []store.EpicTask{openTask(1, 1)}
	plan := Action{Kind: ActRun, Path: "plan", NodeKind: "plan"}
	decideCases(t, []decideCase{
		{
			name: "running integrate run holds the rebuild",
			flow: planWithChecks(batchNode()),
			rows: rowsOf(turns, reopened, runningPlan(0), reopenedBuild(store.NodeEnded), batchRun("c1")),
			reg:  integrateKinds(scripted{}),
			want: []Action{plan},
		},
		{
			name: "parked integrate run holds the rebuild",
			flow: planWithChecks(batchNode()),
			rows: rowsOf(turns, reopened, runningPlan(0), reopenedBuild(store.NodeEnded), parked(batchRun("c1"))),
			reg:  integrateKinds(scripted{}),
			want: []Action{plan},
		},
	})
}

// TestReopenedTaskRebuildSkipsEndedPlan checks that an open task whose build child run ended under a plan run that has
// ended gets no rebuild: only a running plan fans out.
func TestReopenedTaskRebuildSkipsEndedPlan(t *testing.T) {
	t.Parallel()
	turns := []store.EpicTurn{turnAt(1, "complete")}
	reopened := []store.EpicTask{openTask(1, 1)}
	decideCases(t, []decideCase{{
		name: "ended plan rebuilds no task",
		flow: planWithChecks(batchNode()),
		rows: rowsOf(turns, reopened, withMark(ended(startOf("plan", "plan", "plan", 0), "complete"), 0),
			reopenedBuild(store.NodeEnded), ended(batchRun("c1"), outcomePassed)),
		reg:  integrateKinds(scripted{}),
		want: nil,
	}})
}
