package interpret

import (
	"reflect"
	"slices"
	"testing"
	"time"

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

// decideNow is the time of the runner's clock the sync cases decide at.
func decideNow() time.Time {
	return time.Unix(1700005000, 0)
}

// quietRows returns the rows of an epic at a quiet point: turn 1 planned tasks 1 and 2, the batch of the settle walk of
// task 2 claimed both and passed at c1, and the stored base is c1. The plan runs, since turn 1 did not end the plan.
// The epic's last read of origin's main, a minute before the rows' clock, found origin's commit o1 ahead of c1.
func quietRows() kinds.Rows {
	batch := ended(attempted(startOf(batchPath, "integrate", "integrate", 0), 0), outcomePassed)
	batch.Revision = "c1"
	rows := rowsOf(
		[]store.EpicTurn{turnAt(1, "work")},
		[]store.EpicTask{claimed(1, batchPath), claimed(2, batchPath)},
		runningPlan(0), batch,
	)
	rows.Epic.StoredBase = "c1"
	rows.Now = decideNow()
	rows.Epic.OriginRead = store.OriginRead{
		Base: "c1", Commit: "o1", State: originAhead, At: decideNow().Add(-time.Minute),
	}
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
// unbatched, the epic is free and its read of origin's main, which still stands, found origin's commit ahead of the
// stored base, the plan starts its sync run at <plan path>/M<highest turn> and gives the plan no run, so the planner
// gets no turn before the sync.
func TestDecideSyncStartsAtAQuietPointAndHoldsThePlanner(t *testing.T) {
	t.Parallel()
	got := Decide(syncFlow(), quietRows(), syncKinds())
	want := []Action{{Kind: ActStart, Runs: []store.NodeRun{syncRun("")}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Decide() = %+v, want %+v", got, want)
	}
}

// TestDecideSyncReadsOriginWhenTheReadIsStale: at a quiet point a sync is due, and the epic's read of origin's main no
// longer stands: there is none, it was read for another stored base, the sync interval has passed since it, or it is
// later than the rows' clock, which a clock set back leaves. The plan reads origin's main and holds the plan while it
// does, so neither the planner nor a sync starts on the read that no longer stands. A read just inside the interval
// still stands.
func TestDecideSyncReadsOriginWhenTheReadIsStale(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		apply func(read *store.OriginRead)
	}{
		{"no read", func(read *store.OriginRead) { *read = store.OriginRead{} }},
		{"a read for another stored base", func(read *store.OriginRead) { read.Base = "c0" }},
		{"a read the interval has passed since", func(read *store.OriginRead) {
			read.At = decideNow().Add(-syncInterval)
		}},
		{"a read later than the clock", func(read *store.OriginRead) { read.At = decideNow().Add(time.Second) }},
		{"a failed read the interval has passed since", func(read *store.OriginRead) {
			read.State, read.Commit, read.At = originFailed, "", decideNow().Add(-syncInterval-time.Second)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rows := quietRows()
			tc.apply(&rows.Epic.OriginRead)
			if got := Decide(syncFlow(), rows, syncKinds()); !reflect.DeepEqual(got, []Action{{Kind: ActReadOrigin}}) {
				t.Errorf("Decide() = %+v, want only the read of origin's main", got)
			}
		})
	}
	rows := quietRows()
	rows.Epic.OriginRead.At = decideNow().Add(-syncInterval + time.Second)
	if got := Decide(syncFlow(), rows, syncKinds()); !startsAt(got, syncPathOne) {
		t.Errorf("Decide() with a read inside the interval = %+v, want the sync started", got)
	}
}

// TestDecideSyncStartsOnlyOnAReadAhead: a read that stands and found origin's commit in the stored base, lacking the
// change base, with no origin, failed, or taken by a sync's prepare starts no sync and stores no run, and the plan
// keeps its run. Nor does a read ahead whose commit an earlier sync tried. A commit an earlier sync skipped is tried
// again.
func TestDecideSyncStartsOnlyOnAReadAhead(t *testing.T) {
	t.Parallel()
	tried := ended(store.NodeRun{Path: "plan/M0", Kind: "sync"}, outcomeFailed)
	tried.Result = `{"origin":"o1","base":"c0","conflicts":["a.go"]}`
	skipped := ended(store.NodeRun{Path: "plan/M0", Kind: "sync"}, "skipped")
	skipped.Result = `{"origin":"o1","base":"c1","merge":"m1"}`
	cases := []struct {
		name  string
		apply func(rows *kinds.Rows)
	}{
		{"origin's commit is in the stored base", func(rows *kinds.Rows) { rows.Epic.OriginRead.State = originHeld }},
		{"origin's commit lacks the change base", func(rows *kinds.Rows) { rows.Epic.OriginRead.State = originApart }},
		{"the repository has no origin", func(rows *kinds.Rows) {
			rows.Epic.OriginRead.State, rows.Epic.OriginRead.Commit = originNone, ""
		}},
		{"the read failed", func(rows *kinds.Rows) {
			rows.Epic.OriginRead.State, rows.Epic.OriginRead.Commit = originFailed, ""
		}},
		{"a sync's prepare took origin's commit", func(rows *kinds.Rows) {
			rows.Epic.OriginRead.State = originTaken
		}},
		{"an earlier sync tried origin's commit", func(rows *kinds.Rows) { rows.Runs = append(rows.Runs, tried) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rows := quietRows()
			tc.apply(&rows)
			got := Decide(syncFlow(), rows, syncKinds())
			if startsAt(got, syncPathOne) || !runsPlan(got) || slices.ContainsFunc(got, func(act Action) bool {
				return act.Kind == ActReadOrigin
			}) {
				t.Errorf("Decide() = %+v, want the plan's run and no sync nor read", got)
			}
		})
	}
	rows := quietRows()
	rows.Runs = append(rows.Runs, skipped)
	if got := Decide(syncFlow(), rows, syncKinds()); !startsAt(got, syncPathOne) || runsPlan(got) {
		t.Errorf("Decide() after a skipped sync of origin's commit = %+v, want the sync started again", got)
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

// TestDecideSyncEndTellsWhatTheKindTells: a sync run that ends failed ends with its output told, and one that ends
// skipped with nothing to tell tells nothing. Neither writes a trigger, so neither wakes the planner a second time.
func TestDecideSyncEndTellsWhatTheKindTells(t *testing.T) {
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
// waits, an action a planner turn returned and an evidence amendment under way among it, while the epic is paused or
// parked, an owner's park or a planner's, while it has ended or is closed, its review froze or it holds the release
// slot, before any batch under the plan passed, and once the sync of the highest turn is stored.
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
		{"an evidence amendment is capturing", func(rows *kinds.Rows) {
			rows.Epic.Amendments = []store.EvidenceAmendment{{Seq: 1, State: store.AmendmentCapturing}}
		}},
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

// TestDecideSyncKeyIsTheHighestTurnThatIsNotFree: turns that ended free, as a full step cap's refusals and a restart's
// cut turns end, key no sync of their own, so the quiet point after them is still that of turn 1, whose sync is stored,
// and nothing starts or reads origin's main while the planner's turns keep being freed. A turn that failed is a turn
// the planner ran, and keys a sync of its own.
func TestDecideSyncKeyIsTheHighestTurnThatIsNotFree(t *testing.T) {
	t.Parallel()
	rows := quietRows()
	rows.Epic.Turns = append(rows.Epic.Turns,
		store.EpicTurn{Seq: 2, State: turnFree}, store.EpicTurn{Seq: 3, State: turnFree})
	rows.Runs = append(rows.Runs, ended(syncRun(""), "skipped"))
	rows.Epic.OriginRead = store.OriginRead{}
	got := Decide(syncFlow(), rows, syncKinds())
	if !runsPlan(got) || slices.ContainsFunc(got, func(act Action) bool {
		return act.Kind == ActReadOrigin || act.Kind == ActStart
	}) {
		t.Errorf("Decide() after freed turns = %+v, want the plan's run and no sync nor read", got)
	}
	rows.Epic.Turns = append(rows.Epic.Turns, store.EpicTurn{Seq: 4, State: "failed"})
	if got = Decide(syncFlow(), rows, syncKinds()); !reflect.DeepEqual(got, []Action{{Kind: ActReadOrigin}}) {
		t.Errorf("Decide() after a failed turn = %+v, want the read of origin's main for the sync of turn 4", got)
	}
}
