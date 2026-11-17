package interpret

import (
	"reflect"
	"slices"
	"testing"

	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/workflow"
)

// syncPathOne is the path of the sync run of the plan at "plan" for turn 1.
const syncPathOne = "plan/M1"

// syncFlow is the flow of the sync cases: a plan whose body builds each task and then integrates the batch.
func syncFlow() workflow.Workflow {
	return workflow.Workflow{Name: "decide", Nodes: planWithChecks(batchNode())}
}

// syncKinds returns the kinds of the sync cases: plan and build, a scripted integrate kind that decides nothing, since
// its runs have ended, and the sync kind.
func syncKinds() map[string]kinds.Kind {
	return map[string]kinds.Kind{
		"plan": kinds.Plan{}, "build": kinds.Build{}, "integrate": scripted{}, kindSync: kinds.Sync{},
	}
}

// quietRows returns the rows of an epic at a quiet point: turn 1 planned tasks 1 and 2, the batch of the settle walk of
// task 2 claimed both and passed at c1, and the stored base is c1. The plan runs, since turn 1 did not end the plan.
func quietRows() kinds.Rows {
	batch := ended(attempted(startOf(batchPath, "integrate", "integrate", 0), 0), outcomePassed)
	batch.Revision = "c1"
	rows := rowsOf(
		[]store.EpicTurn{turnAt(1, "work")},
		[]store.EpicTask{claimed(1, batchPath), claimed(2, batchPath)},
		runningPlan(0), batch,
	)
	rows.Epic.StoredBase = "c1"
	return rows
}

// syncRun returns the sync run of the plan at "plan" for turn 1 as its start stores it, in state.
func syncRun(state string) store.NodeRun {
	return store.NodeRun{
		Path: syncPathOne, ID: store.NodeRunID(decideEpic, syncPathOne, 0, "sync"), Node: "sync", Kind: "sync",
		State: state,
	}
}

// runsPlan reports whether acts give the plan at "plan" its run, which hands it to the planner.
func runsPlan(acts []Action) bool {
	return slices.ContainsFunc(acts, func(act Action) bool {
		return act.Kind == ActRun && act.Path == "plan" && act.NodeKind == kindPlan
	})
}

// TestDecideSyncStartsAtAQuietPointAndHoldsThePlanner: once the batch of the latest walk passed, nothing is open or
// unbatched and the epic is free, the plan starts its sync run at <plan path>/M<highest turn> and gives the plan no run,
// so the planner gets no turn before the sync.
func TestDecideSyncStartsAtAQuietPointAndHoldsThePlanner(t *testing.T) {
	t.Parallel()
	got := Decide(syncFlow(), quietRows(), syncKinds())
	want := []Action{{Kind: ActStart, Runs: []store.NodeRun{syncRun("")}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Decide() = %+v, want %+v", got, want)
	}
}

// TestDecideRunningSyncIsDecidedAndHoldsThePlan: a running sync run is decided by the sync kind, here its prepare, which
// names the earlier sync that tried origin's commit; the plan gets no run while it runs, and does not end even when its
// kind would end it.
func TestDecideRunningSyncIsDecidedAndHoldsThePlan(t *testing.T) {
	t.Parallel()
	tried := ended(store.NodeRun{Path: "plan/M0", Kind: "sync"}, outcomeFailed)
	tried.Result = `{"origin":"o1","base":"c0","conflicts":["a.go"]}`
	rows := quietRows()
	rows.Runs = append(rows.Runs, tried, syncRun(store.NodeRunning))
	def := workflow.Node{ID: "sync", Kind: "sync"}
	want := []Action{{
		Kind: ActWork, Work: kinds.ActPrepare, Path: syncPathOne, NodeKind: "sync", Def: def,
		Run: syncRun(store.NodeRunning), IDs: []string{"plan/M0"}, Items: []string{"o1"},
	}}
	if got := Decide(syncFlow(), rows, syncKinds()); !reflect.DeepEqual(got, want) {
		t.Errorf("Decide() = %+v, want %+v", got, want)
	}

	rows.Epic.Turns = []store.EpicTurn{turnAt(1, "work"), turnAt(2, "complete")}
	if got := Decide(syncFlow(), rows, syncKinds()); endsPlan(got) || runsPlan(got) {
		t.Errorf("Decide() = %+v with a complete turn, want the plan held by its running sync", got)
	}
}

// TestDecideSyncEndTellsOnlyAFailure: a sync run that ends failed ends with its output told, and one that ends skipped
// tells nothing. Neither writes a trigger, so neither wakes the planner a second time.
func TestDecideSyncEndTellsOnlyAFailure(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, result, outcome, output, tell string
	}{
		{
			name: "conflict", result: `{"origin":"o1","base":"c1","conflicts":["a.go","b.go"]}`, outcome: "failed",
			output: "merge of origin's commit o1 into the stored base c1 conflicts in a.go, b.go",
			tell: "workflow node plan/M1 failed: merge of origin's commit o1 into the stored base c1 conflicts in " +
				"a.go, b.go",
		},
		{
			name: "contained", result: `{"origin":"o1","base":"c1","contained":true}`, outcome: "skipped",
			output: "origin's commit o1 is in the stored base c1",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			run := syncRun(store.NodeRunning)
			run.Result = tc.result
			rows := quietRows()
			rows.Runs = append(rows.Runs, run)
			want := []Action{{
				Kind: ActSync, Path: syncPathOne, Outcome: tc.outcome, Output: tc.output, Reason: tc.tell,
			}}
			if got := Decide(syncFlow(), rows, syncKinds()); !reflect.DeepEqual(got, want) {
				t.Errorf("Decide() = %+v, want %+v", got, want)
			}
		})
	}
}

// TestDecideSyncAdvanceCarriesNoTrigger: the advance of a sync run is its work, and unlike a batch's it carries no
// trigger, since the batch before it woke the planner already.
func TestDecideSyncAdvanceCarriesNoTrigger(t *testing.T) {
	t.Parallel()
	run := syncRun(store.NodeRunning)
	run.Result = `{"origin":"o1","base":"c1","merge":"m1"}`
	run.Revision = "m1"
	rows := quietRows()
	rows.Runs = append(rows.Runs, run)
	got := Decide(syncFlow(), rows, syncKinds())
	if len(got) != 1 || got[0].Kind != ActWork || got[0].Work != kinds.ActAdvance || got[0].Trigger != nil {
		t.Errorf("Decide() = %+v, want the advance of the sync with no trigger", got)
	}
}

// TestDecideNoSyncAwayFromAQuietPoint: the plan starts no sync, and keeps its run, while anything of the epic moves or
// waits, an action a planner turn returned among it, while the epic is paused or parked, an owner's park or a
// planner's, while it has ended or is closed, its review froze or it holds the release slot, before any batch under the
// plan passed, and once the sync of the highest turn is stored.
func TestDecideNoSyncAwayFromAQuietPoint(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		apply func(rows *kinds.Rows)
	}{
		{"a task is open", func(rows *kinds.Rows) {
			rows.Epic.Tasks = append(rows.Epic.Tasks, openTask(3, 1))
		}},
		{"a task is accepted and unbatched", func(rows *kinds.Rows) {
			rows.Epic.Tasks = append(rows.Epic.Tasks, doneTask(3, 1))
		}},
		{"a task waits on another", func(rows *kinds.Rows) {
			rows.Epic.Tasks = append(rows.Epic.Tasks, waitingTask(3, "1"))
		}},
		{"a planner turn runs", func(rows *kinds.Rows) {
			rows.Epic.Turns = append(rows.Epic.Turns, store.EpicTurn{Seq: 2, State: "running"})
		}},
		{"an integrate run runs", func(rows *kinds.Rows) {
			rows.Runs = append(rows.Runs, running(startOf("plan/S9/integrate", "integrate", "integrate", 0)))
		}},
		{"an integrate run is parked", func(rows *kinds.Rows) {
			rows.Runs = append(rows.Runs, parked(startOf("plan/S9/integrate", "integrate", "integrate", 0)))
		}},
		{"a release run runs", func(rows *kinds.Rows) {
			rows.Runs = append(rows.Runs, running(startOf("release", "release", "release", 0)))
		}},
		{"a planner action is pending", func(rows *kinds.Rows) { rows.Actions = 1 }},
		{"the epic is paused", func(rows *kinds.Rows) { rows.Epic.Paused = true }},
		{"the epic is parked", func(rows *kinds.Rows) { rows.Epic.Parked = "held" }},
		{"the epic is parked for the planner", func(rows *kinds.Rows) {
			rows.Epic.Parked, rows.Epic.ParkClass = "workflow node plan/S2/lint failed", store.ParkClassNode
		}},
		{"the epic is parked at the retry bound", func(rows *kinds.Rows) {
			rows.Epic.Parked, rows.Epic.ParkClass = "workflow node plan/S2/lint failed", store.ParkClassRetryBound
		}},
		{"the epic is closed", func(rows *kinds.Rows) { rows.Epic.Phase = store.PhaseClosedAbandoned }},
		{"the epic has ended", func(rows *kinds.Rows) { rows.Epic.Phase = store.PhaseStopped }},
		{"the epic has a candidate", func(rows *kinds.Rows) { rows.Epic.Candidate = "c2" }},
		{"the review froze", func(rows *kinds.Rows) { rows.Review.Frozen = "c1" }},
		{"the epic holds the release slot", func(rows *kinds.Rows) { rows.Slot = decideEpic }},
		{"no batch under the plan passed", func(rows *kinds.Rows) {
			rows.Runs = []store.NodeRun{runningPlan(0)}
		}},
		{"the batch under the plan claimed no batch", func(rows *kinds.Rows) {
			rows.Runs[1].Revision = ""
		}},
		{"the sync of the highest turn is stored", func(rows *kinds.Rows) {
			rows.Runs = append(rows.Runs, ended(syncRun(""), "skipped"))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rows := quietRows()
			tc.apply(&rows)
			got := Decide(syncFlow(), rows, syncKinds())
			if slices.ContainsFunc(got, func(act Action) bool {
				return act.Kind == ActStart && slices.ContainsFunc(act.Runs, func(run store.NodeRun) bool {
					return run.Kind == kindSync
				})
			}) {
				t.Errorf("Decide() = %+v, want no sync started", got)
			}
			if !runsPlan(got) {
				t.Errorf("Decide() = %+v, want the plan's run, since no sync holds it", got)
			}
		})
	}
}

// TestDecideNoSyncWithoutASyncKindOrAnIntegrateNode: an interpreter that registers no sync kind starts no sync, and
// neither does a plan whose body holds no integrate node.
func TestDecideNoSyncWithoutASyncKindOrAnIntegrateNode(t *testing.T) {
	t.Parallel()
	reg := syncKinds()
	delete(reg, kindSync)
	if got := Decide(syncFlow(), quietRows(), reg); !runsPlan(got) || startsAt(got, syncPathOne) {
		t.Errorf("Decide() with no sync kind = %+v, want the plan's run and no sync", got)
	}
	gated := workflow.Workflow{Name: "decide", Nodes: planWithChecks(kindNode("lint", "gate"))}
	rows := quietRows()
	rows.Runs = append(rows.Runs, ended(startOf("plan/S2/lint", "lint", "gate", 0), outcomePassed))
	if got := Decide(gated, rows, syncKinds()); !runsPlan(got) || startsAt(got, syncPathOne) {
		t.Errorf("Decide() with no integrate node = %+v, want the plan's run and no sync", got)
	}
}

// TestDecideSyncKeyIsTheHighestTurn: a quiet point after a later turn that planned no task is keyed by that turn, so it
// starts a sync of its own even though the sync of an earlier turn is stored.
func TestDecideSyncKeyIsTheHighestTurn(t *testing.T) {
	t.Parallel()
	rows := quietRows()
	rows.Epic.Turns = append(rows.Epic.Turns, turnAt(2, "work"))
	rows.Runs = append(rows.Runs, ended(syncRun(""), "skipped"))
	if got := Decide(syncFlow(), rows, syncKinds()); !startsAt(got, "plan/M2") || runsPlan(got) {
		t.Errorf("Decide() = %+v, want the sync of turn 2 started and the plan held", got)
	}
}
