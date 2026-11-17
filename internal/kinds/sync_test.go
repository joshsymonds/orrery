package kinds

import (
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

var _ Kind = Sync{}

// syncRunPath is the path of the sync run the tests decide, and syncMerged the result of a prepare that merged origin's
// commit o1 into the stored base base-1 as m1. syncHead is the line that names that merge.
const (
	syncRunPath = "plan/M1"
	syncMerged  = `{"origin":"o1","base":"base-1","merge":"m1"}`
	syncHead    = "merge m1 of origin's commit o1 into the stored base base-1"
)

// syncNode returns the sync run at syncRunPath of the test run at attempt 1, at revision with result.
func syncNode(revision, result string) Node {
	return Node{Run: store.NodeRun{
		ID: testRunID, Path: syncRunPath, Kind: "sync", Attempt: 1, State: store.NodeRunning, Revision: revision,
		Result: result,
	}}
}

// greenBatch returns the integrate run that passed at the stored base base-1, and its capture of R1, which passed: R1 is
// green at the stored base.
func greenBatch() (store.NodeRun, store.EvidenceRun) {
	run := store.NodeRun{
		ID: "int-run", Kind: "integrate", Attempt: 1, State: store.NodeEnded, Outcome: outcomePassed,
		Revision: "base-1",
	}
	capture := store.EvidenceRun{
		ID: store.NodeWorkID("int-run", 1, "capture R1"), State: runEnded, Verdict: evidence.VerdictHolds, Passed: true,
	}
	return run, capture
}

// syncRows returns the rows of an epic at rest at the stored base base-1, where R1 is green, with the sync's own checks
// and captures.
func syncRows(checks []store.NodeCheck, captured ...store.EvidenceRun) Rows {
	batch, green := greenBatch()
	return Rows{
		Epic: integrateEpic(), Runs: []store.NodeRun{batch}, Checks: checks, Evidence: append(captured, green),
	}
}

// syncTried returns an ended sync run at path with result.
func syncTried(path, result string) store.NodeRun {
	return store.NodeRun{Path: path, Kind: "sync", State: store.NodeEnded, Result: result}
}

func TestSyncPrepareAndUnmerged(t *testing.T) {
	t.Parallel()
	tried := Rows{Runs: []store.NodeRun{
		syncTried("plan/M0", `{"origin":"o0","base":"b","conflicts":["a.go"]}`),
		syncTried("plan/M2", `{"origin":"o2","base":"b","contained":true}`),
		syncTried("fix/M3", `{"origin":"o3","base":"b","merge":"m3"}`),
		syncTried("fix/M4", `not json`),
		{Path: "fix/M5", Kind: "sync", State: store.NodeRunning},
		{Path: "plan/S1/integrate", Kind: "integrate", Result: `{"origin":"o9","merge":"m9"}`},
	}}
	checkDecisions(t, Sync{}, []decideCase{
		{"paused epic gives none", syncNode("", ""), Rows{Epic: store.EpicRow{Paused: true}}, Action{Kind: ActNone}},
		{"parked epic gives none", syncNode("", ""), Rows{Epic: store.EpicRow{Parked: "held"}}, Action{Kind: ActNone}},
		{
			"prepare names the commits other syncs tried",
			syncNode("", ""), tried,
			Action{Kind: ActPrepare, IDs: []string{"plan/M0", "fix/M3"}, Items: []string{"o0", "o3"}},
		},
		{
			"a result that does not decode ends skipped", syncNode("", "{"), Rows{},
			syncEnd("skipped", "sync result is not a JSON object: unexpected end of JSON input"),
		},
		{
			"no origin ends skipped", syncNode("", `{"noOrigin":true,"base":"b"}`), Rows{},
			syncEnd("skipped", "the repository has no remote origin"),
		},
		{
			"an error ends skipped", syncNode("", `{"error":"fetch failed","base":"b"}`), Rows{},
			syncEnd("skipped", "the sync did not run: fetch failed"),
		},
		{
			"a commit the stored base holds ends skipped",
			syncNode("", `{"origin":"o1","base":"b","contained":true}`), Rows{},
			syncEnd("skipped", "origin's commit o1 is in the stored base b"),
		},
		{
			"a commit another sync tried ends skipped",
			syncNode("", `{"origin":"o1","base":"b","triedBy":"plan/M0"}`), Rows{},
			syncEnd("skipped", "origin's commit o1 was tried by plan/M0"),
		},
		{
			"a merge that conflicts ends failed with its paths",
			syncNode("", `{"origin":"o1","base":"b","conflicts":["a.go","b.go"]}`), Rows{},
			syncEnd("failed", "merge of origin's commit o1 into the stored base b conflicts in a.go, b.go"),
		},
		{
			"a result with no merge ends skipped", syncNode("", `{"origin":"o1","base":"b"}`), Rows{},
			syncEnd("skipped", "the sync result names no merge"),
		},
	})
}

func TestSyncChecksAtTheMerge(t *testing.T) {
	t.Parallel()
	node := syncNode("m1", syncMerged)
	captured := []store.EvidenceRun{greenCapture("capture R1", true), greenCapture("capture R2", true)}
	checkDecisions(t, Sync{}, []decideCase{
		{
			"the first Done gate starts at the merge", node, syncRows(nil),
			Action{Kind: ActStart, IDs: []string{workID("done 1")}, Items: []string{"just check"}},
		},
		{
			"a running gate waits", node, syncRows([]store.NodeCheck{runningCheck("done 1")}),
			Action{Kind: ActNone},
		},
		{
			"a failed gate ends failed", node, syncRows([]store.NodeCheck{failedCheck("done 1")}),
			syncEnd("failed", syncHead+"\nDone gate just check failed"),
		},
		{
			"a gate lost twice ends failed and parks nothing", node,
			syncRows([]store.NodeCheck{lostCheck("done 1"), lostCheck("done 1 rerun")}),
			syncEnd("failed", syncHead+"\nsync plan/M1: Done gate just check lost at a restart twice"),
		},
		{
			"passed gates start the captures", node, syncRows(gatesPassed()),
			Action{Kind: ActCapture, IDs: []string{workID("capture R1"), workID("capture R2")},
				Items: []string{"R1", "R2"}},
		},
		{
			"a capture lost twice ends failed", node,
			syncRows(gatesPassed(), lostCapture("capture R1"), lostCapture("capture R1 rerun"),
				greenCapture("capture R2", true)),
			syncEnd("failed", syncHead+"\nsync plan/M1: capture R1 lost at a restart twice"),
		},
		{
			"a capture that regressed from green ends failed", node,
			syncRows(gatesPassed(), greenCapture("capture R1", false), greenCapture("capture R2", true)),
			syncEnd("failed", syncHead+"\nR1 regressed from green at the stored base base-1"),
		},
		{
			"passed checks advance to the merge", node, syncRows(gatesPassed(), captured...),
			Action{Kind: ActAdvance, Output: "merged origin's commit o1 into the stored base base-1 as m1\n" +
				"R1 green\nR2 green\nR3 not yet green"},
		},
	})
}

func TestSyncReproducesTestItemsAtTheMerge(t *testing.T) {
	t.Parallel()
	node := syncNode("m1", syncMerged)
	captured := []store.EvidenceRun{
		greenCapture("capture R1", true), greenCapture("capture R2", true), greenCapture("capture R3", true),
	}
	rows := Rows{Epic: reproEpic(), Checks: gatesPassed(), Evidence: captured}
	lost := rows
	lost.Checks = append(gatesPassed(), lostCheck("repro R1"), lostCheck("repro R1 rerun"), passedCheck("repro R3"))
	checkDecisions(t, Sync{}, []decideCase{
		{
			"settled captures start the reproductions", node, rows,
			Action{Kind: ActRepro, IDs: []string{workID("repro R1"), workID("repro R3")}, Items: []string{"R1", "R3"}},
		},
		{
			"a reproduction lost twice ends failed", node, lost,
			syncEnd("failed", syncHead+"\nsync plan/M1: reproduction R1 lost at a restart twice"),
		},
	})
}

func TestSyncDoesNotAdvanceAnEpicThatMoved(t *testing.T) {
	t.Parallel()
	node := syncNode("m1", syncMerged)
	captured := []store.EvidenceRun{greenCapture("capture R1", true), greenCapture("capture R2", true)}
	turning := syncRows(gatesPassed(), captured...)
	turning.Epic.Turns = []store.EpicTurn{{Seq: 2, State: "running"}}
	moved := syncRows(gatesPassed(), captured...)
	moved.Epic.StoredBase = "base-2"
	checkDecisions(t, Sync{}, []decideCase{
		{
			"a planner turn that started ends it skipped", node, turning,
			syncEnd("skipped", syncHead+"\nnot advanced: planner turn 2 is running"),
		},
		{
			"a stored base that moved ends it skipped", node, moved,
			syncEnd("skipped", syncHead+"\nnot advanced: the stored base moved from base-1 to base-2"),
		},
	})
}

func TestBusy(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		rows Rows
		want string
	}{
		{"at rest", Rows{Epic: store.EpicRow{
			Tasks: []store.EpicTask{{Seq: 1, Status: taskDone, Batch: "plan/S1/integrate"}, {Seq: 2, Status: "failed"}},
			Turns: []store.EpicTurn{{Seq: 1, State: turnSucceeded}},
		}, Runs: []store.NodeRun{{Path: "plan/S1/integrate", Kind: "integrate", State: store.NodeEnded}}}, ""},
		{"a candidate", Rows{Epic: store.EpicRow{Candidate: "c1"}}, "the epic has the candidate c1"},
		{"a running turn", Rows{Epic: store.EpicRow{Turns: []store.EpicTurn{{Seq: 3, State: turnRunning}}}},
			"planner turn 3 is running"},
		{"an open task", Rows{Epic: store.EpicRow{Tasks: []store.EpicTask{{Seq: 4, Status: taskOpen}}}},
			"task T4 is open"},
		{"an unbatched task", Rows{Epic: store.EpicRow{Tasks: []store.EpicTask{{Seq: 5, Status: taskDone}}}},
			"task T5 is accepted and no batch has claimed it"},
		{"a running batch", Rows{Runs: []store.NodeRun{{Path: "plan/S2/integrate", Kind: "integrate",
			State: store.NodeRunning}}}, "integrate run plan/S2/integrate is running"},
		{"a parked release", Rows{Runs: []store.NodeRun{{Path: "release", Kind: "release",
			State: store.NodeParked}}}, "release run release is parked"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := Busy(tc.rows); got != tc.want {
				t.Errorf("Busy() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestSyncAtTheStoredBaseIsItsGreen: once a sync passed and moved the stored base to its merge, the captures the sync took
// there are the Requirements' green at the stored base, and its reproductions their reproductions, so the epic is built
// at the merge with no batch after it.
func TestSyncAtTheStoredBaseIsItsGreen(t *testing.T) {
	t.Parallel()
	sync := store.NodeRun{ID: testRunID, Kind: "sync", Attempt: 1, Outcome: outcomePassed, Revision: "m1"}
	epic := reproEpic()
	epic.StoredBase = "m1"
	red := store.NodeRun{ID: "red-run", Kind: "evidence", Attempt: 1, Outcome: outcomePassed}
	redRun := func(id string) store.EvidenceRun {
		return store.EvidenceRun{
			ID: store.NodeWorkID("red-run", 1, id), Phase: evidence.PhaseRed, Revision: "base-1", State: runEnded,
			Passed: true,
		}
	}
	rows := Rows{
		Epic: epic, Runs: []store.NodeRun{red, sync},
		Evidence: []store.EvidenceRun{
			redRun("R1"), redRun("R2"), redRun("R3"),
			greenCapture("capture R1", true), greenCapture("capture R2", true), greenCapture("capture R3", true),
		},
		Checks: []store.NodeCheck{
			{ID: workID("repro R1"), State: store.NodeEnded, Exit: 1},
			{ID: workID("repro R3"), State: store.NodeEnded, Exit: 1},
		},
	}
	if got := GreenAt(rows, "m1"); !reflect.DeepEqual(got, map[string]bool{"R1": true, "R2": true, "R3": true}) {
		t.Errorf("GreenAt(m1) = %v, want every Requirement green", got)
	}
	if got := Unbuilt(rows); got != nil {
		t.Errorf("Unbuilt() = %q, want none", got)
	}
}
