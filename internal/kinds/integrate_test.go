package kinds

import (
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

var _ Kind = Integrate{}

// batchPath is the path of the integrate run the tests decide, and the Results an integrate run records: one commit
// composed and nothing left out, one composed with a task left out, and nothing composed.
const (
	batchPath       = "plan/S1/integrate"
	composedOne     = `{"base":"base-1","candidate":"cand-1","composed":[{"task":1,"commit":"c1"}],"leftOut":[]}`
	composedLeftOut = `{"base":"base-1","candidate":"cand-1","composed":[{"task":1,"commit":"c1"}],` +
		`"leftOut":[{"task":2,"reason":"T2 overlaps T1 at a.go"}]}`
	nothingComposed = `{"base":"base-1","candidate":"base-1","composed":[],` +
		`"leftOut":[{"task":1,"reason":"T1 was built on base-0, not on the batch base base-1"}]}`
)

// batchNode returns the integrate run at batchPath of the test run at attempt 1, at revision with result, and
// refsChecked when its refs were compared.
func batchNode(revision, result string, refsChecked bool) Node {
	return Node{Run: store.NodeRun{
		ID: testRunID, Path: batchPath, Kind: "integrate", Attempt: 1,
		Revision: revision, Result: result, RefsChecked: refsChecked,
	}}
}

// integrateEpic returns an epic at base-1 stored at base-1, with the Done gates just check and just lint, the
// Requirements R1 and R2 that name evidence items, and R3 that names none. Its task T1, the one composedOne composes,
// claims R1 and contributes to R2, so a batch of it captures both.
func integrateEpic() store.EpicRow {
	return store.EpicRow{
		Base:       "base-1",
		StoredBase: "base-1",
		DoneGates:  []string{"just check", "just lint"},
		Requirements: []store.EpicRequirement{
			{ID: "R1", Text: "the suite passes", Item: evidenceItem("the suite passes")},
			{ID: "R2", Text: "lint is clean", Item: evidenceItem("lint is clean")},
			{ID: "R3", Text: "the docs read well"},
		},
		Tasks: []store.EpicTask{batchTask(1, []string{"R1"}, []string{"R2"})},
	}
}

// batchTask returns the done task seq that the integrate run at batchPath claimed, claiming requirements and
// contributing to contributes.
func batchTask(seq int64, requirements, contributes []string) store.EpicTask {
	return store.EpicTask{
		Seq: seq, Status: taskDone, Batch: batchPath, Requirements: requirements, Contributes: contributes,
	}
}

// passedCheck returns an ended check of the work part of the test run that exited 0.
func passedCheck(part string) store.NodeCheck {
	return store.NodeCheck{ID: workID(part), State: store.NodeEnded}
}

// failedCheck returns an ended check of the work part of the test run that exited 1.
func failedCheck(part string) store.NodeCheck {
	return store.NodeCheck{ID: workID(part), State: store.NodeEnded, Exit: 1}
}

// timedOutCheck returns an ended check of the work part of the test run that timed out.
func timedOutCheck(part string) store.NodeCheck {
	return store.NodeCheck{ID: workID(part), State: store.NodeEnded, TimedOut: true}
}

// runningCheck returns a check of the work part of the test run that is still running.
func runningCheck(part string) store.NodeCheck {
	return store.NodeCheck{ID: workID(part), State: store.NodeRunning}
}

// lostCheck returns an ended check of the work part of the test run that was lost at a restart.
func lostCheck(part string) store.NodeCheck {
	return store.NodeCheck{ID: workID(part), State: store.NodeEnded, NotRun: store.LostAtRestart}
}

// greenCapture returns an ended green capture of the work part of the test run that passed or not.
func greenCapture(part string, passed bool) store.EvidenceRun {
	verdict := evidence.VerdictFails
	if passed {
		verdict = evidence.VerdictHolds
	}
	return store.EvidenceRun{
		ID: workID(part), Phase: evidence.PhaseGreen, State: runEnded, Verdict: verdict, Passed: passed,
	}
}

// runningCapture returns a capture of the work part of the test run that is still capturing.
func runningCapture(part string) store.EvidenceRun {
	return store.EvidenceRun{ID: workID(part), Phase: evidence.PhaseGreen, State: "capturing"}
}

// lostCapture returns an ended capture of the work part of the test run that was lost at a restart.
func lostCapture(part string) store.EvidenceRun {
	return store.EvidenceRun{
		ID: workID(part), Phase: evidence.PhaseGreen, State: runEnded, NotCaptured: store.LostAtRestart,
	}
}

// gatesPassed returns the checks of both Done gates of integrateEpic, passed.
func gatesPassed() []store.NodeCheck {
	return []store.NodeCheck{passedCheck("done 1"), passedCheck("done 2")}
}

func TestIntegratePrepare(t *testing.T) {
	t.Parallel()
	claimedElsewhere := store.EpicTask{Seq: 1, Status: taskDone, Batch: "plan/S2/integrate"}
	cases := []decideCase{
		{
			"paused epic gives none",
			batchNode("", "", false),
			Rows{Epic: store.EpicRow{Paused: true, Tasks: []store.EpicTask{{Seq: 1, Status: taskDone}}}},
			Action{Kind: ActNone},
		},
		{
			"parked epic gives none",
			batchNode("", "", false),
			Rows{Epic: store.EpicRow{
				Parked: "waiting on the operator", Tasks: []store.EpicTask{{Seq: 1, Status: taskDone}},
			}},
			Action{Kind: ActNone},
		},
		{"no tasks ends passed with the note", batchNode("", "", false), Rows{},
			Action{Kind: ActEnd, Outcome: outcomePassed, Output: "no accepted task to integrate"}},
		{
			"open task is not accepted",
			batchNode("", "", false),
			Rows{Epic: store.EpicRow{Tasks: []store.EpicTask{{Seq: 1, Status: taskOpen}}}},
			Action{Kind: ActEnd, Outcome: outcomePassed, Output: "no accepted task to integrate"},
		},
		{
			"accepted tasks prepare in seq order",
			batchNode("", "", false),
			Rows{Epic: store.EpicRow{Tasks: []store.EpicTask{
				{Seq: 3, Status: taskDone}, {Seq: 1, Status: taskDone}, {Seq: 2, Status: taskOpen},
			}}},
			Action{Kind: ActPrepare, Tasks: []int64{1, 3}},
		},
		{
			"tasks the run claimed are its batch, not the unclaimed accepted ones",
			batchNode("", "", false),
			Rows{Epic: store.EpicRow{Tasks: []store.EpicTask{
				{Seq: 1, Status: taskDone, Batch: batchPath}, {Seq: 2, Status: taskDone},
			}}},
			Action{Kind: ActPrepare, Tasks: []int64{1}},
		},
		{
			"claimed tasks prepare in seq order",
			batchNode("", "", false),
			Rows{Epic: store.EpicRow{Tasks: []store.EpicTask{
				{Seq: 4, Status: taskDone, Batch: batchPath}, {Seq: 2, Status: taskDone, Batch: batchPath},
			}}},
			Action{Kind: ActPrepare, Tasks: []int64{2, 4}},
		},
		{
			"tasks another run claimed are not accepted",
			batchNode("", "", false),
			Rows{Epic: store.EpicRow{Tasks: []store.EpicTask{claimedElsewhere}}},
			Action{Kind: ActEnd, Outcome: outcomePassed, Output: "no accepted task to integrate"},
		},
	}
	checkDecisions(t, Integrate{}, cases)
}

func TestIntegrateBatch(t *testing.T) {
	t.Parallel()
	epic := integrateEpic()
	cases := []decideCase{
		{
			"result that is not JSON ends failed",
			batchNode(testRev, "", false),
			Rows{},
			Action{Kind: ActEnd, Outcome: outcomeFailed,
				Output: "integrate result is not a JSON compose result: unexpected end of JSON input"},
		},
		{
			"nothing composed ends failed with the left-out lines",
			batchNode(testRev, nothingComposed, false),
			Rows{Epic: epic},
			Action{
				Kind: ActEnd, Outcome: outcomeFailed, Output: "T1 was built on base-0, not on the batch base base-1",
			},
		},
		{
			"first Done gate not started starts its check",
			batchNode(testRev, composedOne, false),
			Rows{Epic: epic},
			Action{Kind: ActStart, IDs: []string{workID("done 1")}, Items: []string{"just check"}},
		},
		{
			"running Done gate waits",
			batchNode(testRev, composedOne, false),
			Rows{Epic: epic, Checks: []store.NodeCheck{runningCheck("done 1")}},
			Action{Kind: ActNone},
		},
		{
			"second Done gate starts once the first passed",
			batchNode(testRev, composedOne, false),
			Rows{Epic: epic, Checks: []store.NodeCheck{passedCheck("done 1")}},
			Action{Kind: ActStart, IDs: []string{workID("done 2")}, Items: []string{"just lint"}},
		},
		{
			"Done gate lost at a restart reruns once",
			batchNode(testRev, composedOne, false),
			Rows{Epic: epic, Checks: []store.NodeCheck{lostCheck("done 1")}},
			Action{Kind: ActStart, IDs: []string{workID("done 1 rerun")}, Items: []string{"just check"}},
		},
		{
			"rerun of a lost Done gate waits while it runs",
			batchNode(testRev, composedOne, false),
			Rows{Epic: epic, Checks: []store.NodeCheck{lostCheck("done 1"), runningCheck("done 1 rerun")}},
			Action{Kind: ActNone},
		},
		{
			"rerun of a lost Done gate lost again parks",
			batchNode(testRev, composedOne, false),
			Rows{Epic: epic, Checks: []store.NodeCheck{lostCheck("done 1"), lostCheck("done 1 rerun")}},
			Action{Kind: ActPark, Output: "integrate plan/S1/integrate: Done gate just check lost at a restart twice"},
		},
		{
			"rerun that passed stands for its lost Done gate",
			batchNode(testRev, composedOne, false),
			Rows{Epic: epic, Checks: []store.NodeCheck{lostCheck("done 1"), passedCheck("done 1 rerun")}},
			Action{Kind: ActStart, IDs: []string{workID("done 2")}, Items: []string{"just lint"}},
		},
		{
			"failed Done gate ends red with the captures skipped",
			batchNode(testRev, composedLeftOut, true),
			Rows{Epic: epic, Checks: []store.NodeCheck{failedCheck("done 1")}},
			Action{Kind: ActEnd, Outcome: outcomeFailed,
				Output: "Done gate just check failed\nT2 overlaps T1 at a.go"},
		},
		{
			"failed Done gate checks refs before it ends red",
			batchNode(testRev, composedOne, false),
			Rows{Epic: epic, Checks: []store.NodeCheck{failedCheck("done 1")}},
			Action{Kind: ActRefs},
		},
		{
			"timed out Done gate fails",
			batchNode(testRev, composedOne, true),
			Rows{Epic: epic, Checks: []store.NodeCheck{passedCheck("done 1"), timedOutCheck("done 2")}},
			Action{Kind: ActEnd, Outcome: outcomeFailed, Output: "Done gate just lint failed"},
		},
		{
			"Done gate that did not run fails",
			batchNode(testRev, composedOne, true),
			Rows{Epic: epic, Checks: []store.NodeCheck{{
				ID: workID("done 1"), State: store.NodeEnded, NotRun: "no gate named just check",
			}}},
			Action{Kind: ActEnd, Outcome: outcomeFailed, Output: "Done gate just check failed"},
		},
		{
			"captures that have no run start together",
			batchNode(testRev, composedOne, false),
			Rows{Epic: epic, Checks: gatesPassed()},
			Action{Kind: ActCapture,
				IDs:   []string{workID("capture R1"), workID("capture R2")},
				Items: []string{"R1", "R2"}},
		},
		{
			"missing capture starts while another waits",
			batchNode(testRev, composedOne, false),
			Rows{Epic: epic, Checks: gatesPassed(), Evidence: []store.EvidenceRun{runningCapture("capture R2")}},
			Action{Kind: ActCapture, IDs: []string{workID("capture R1")}, Items: []string{"R1"}},
		},
		{
			"running capture waits before refs",
			batchNode(testRev, composedOne, false),
			Rows{Epic: epic, Checks: gatesPassed(), Evidence: []store.EvidenceRun{
				greenCapture("capture R1", true), runningCapture("capture R2"),
			}},
			Action{Kind: ActNone},
		},
		{
			"capture lost at a restart reruns once",
			batchNode(testRev, composedOne, true),
			Rows{Epic: epic, Checks: gatesPassed(), Evidence: []store.EvidenceRun{
				lostCapture("capture R1"), greenCapture("capture R2", true),
			}},
			Action{Kind: ActCapture, IDs: []string{workID("capture R1 rerun")}, Items: []string{"R1"}},
		},
		{
			"rerun of a lost capture waits while it runs",
			batchNode(testRev, composedOne, true),
			Rows{Epic: epic, Checks: gatesPassed(), Evidence: []store.EvidenceRun{
				lostCapture("capture R1"), runningCapture("capture R1 rerun"), greenCapture("capture R2", true),
			}},
			Action{Kind: ActNone},
		},
		{
			"rerun of a lost capture lost again parks",
			batchNode(testRev, composedOne, true),
			Rows{Epic: epic, Checks: gatesPassed(), Evidence: []store.EvidenceRun{
				lostCapture("capture R1"), lostCapture("capture R1 rerun"), greenCapture("capture R2", true),
			}},
			Action{Kind: ActPark, Output: "integrate plan/S1/integrate: capture R1 lost at a restart twice"},
		},
		{
			"rerun that passed stands for its lost capture and the epic advances",
			batchNode(testRev, composedOne, true),
			Rows{Epic: epic, Checks: gatesPassed(), Evidence: []store.EvidenceRun{
				lostCapture("capture R1"), greenCapture("capture R1 rerun", true), greenCapture("capture R2", true),
			}},
			Action{Kind: ActAdvance, Output: "R1 green\nR2 green\nR3 not yet green"},
		},
		{
			"capture not captured for another reason fails red",
			batchNode(testRev, composedOne, true),
			Rows{Epic: epic, Checks: gatesPassed(), Evidence: []store.EvidenceRun{
				{
					ID: workID("capture R1"), Phase: evidence.PhaseGreen, State: runEnded,
					NotCaptured: "killed at its limit",
				},
				greenCapture("capture R2", true),
			}},
			Action{Kind: ActEnd, Outcome: outcomeFailed, Output: "R1 capture failed"},
		},
		{
			"capture with no verdict fails red",
			batchNode(testRev, composedOne, true),
			Rows{Epic: epic, Checks: gatesPassed(), Evidence: []store.EvidenceRun{
				{ID: workID("capture R1"), Phase: evidence.PhaseGreen, State: runEnded},
				greenCapture("capture R2", true),
			}},
			Action{Kind: ActEnd, Outcome: outcomeFailed, Output: "R1 capture failed"},
		},
		{
			"capture that does not hold advances when nothing regressed",
			batchNode(testRev, composedOne, true),
			Rows{Epic: epic, Checks: gatesPassed(), Evidence: []store.EvidenceRun{
				greenCapture("capture R1", false), greenCapture("capture R2", true),
			}},
			Action{Kind: ActAdvance, Output: "R1 not yet green\nR2 green\nR3 not yet green"},
		},
		{
			"Requirement green at the stored base that no longer holds fails red",
			batchNode(testRev, composedOne, true),
			Rows{
				Epic:   epic,
				Checks: gatesPassed(),
				Runs: []store.NodeRun{{
					ID: "old-run", Kind: "integrate", Outcome: outcomePassed, Revision: "base-1", Attempt: 1,
				}},
				Evidence: []store.EvidenceRun{
					{
						ID: store.NodeWorkID("old-run", 1, "capture R1"), Phase: evidence.PhaseGreen,
						State: runEnded, Verdict: evidence.VerdictHolds, Passed: true,
					},
					greenCapture("capture R1", false),
					greenCapture("capture R2", true),
				},
			},
			Action{Kind: ActEnd, Outcome: outcomeFailed, Output: "R1 regressed from green at the stored base base-1"},
		},
		{
			"advance lists every Requirement and the left-out lines",
			batchNode(testRev, composedLeftOut, true),
			Rows{Epic: epic, Checks: gatesPassed(), Evidence: []store.EvidenceRun{
				greenCapture("capture R1", true), greenCapture("capture R2", true),
			}},
			Action{Kind: ActAdvance,
				Output: "R1 green\nR2 green\nR3 not yet green\nT2 overlaps T1 at a.go"},
		},
		{
			"gates and captures passed with refs not checked gives refs",
			batchNode(testRev, composedOne, false),
			Rows{Epic: epic, Checks: gatesPassed(), Evidence: []store.EvidenceRun{
				greenCapture("capture R1", true), greenCapture("capture R2", true),
			}},
			Action{Kind: ActRefs},
		},
	}
	checkDecisions(t, Integrate{}, cases)
}

func TestGreenAt(t *testing.T) {
	t.Parallel()
	integrated := store.NodeRun{
		ID:       "int-run",
		Kind:     "integrate",
		Outcome:  outcomePassed,
		Revision: "base-1",
		Attempt:  1,
	}
	greenR1 := store.EvidenceRun{
		ID: store.NodeWorkID("int-run", 1, "capture R1"), Phase: evidence.PhaseGreen,
		State: runEnded, Verdict: evidence.VerdictHolds, Passed: true,
	}
	cases := []struct {
		name     string
		rows     Rows
		revision string
		want     map[string]bool
	}{
		{
			"passed capture of a passed integrate run at the revision is green",
			Rows{Epic: integrateEpic(), Runs: []store.NodeRun{integrated}, Evidence: []store.EvidenceRun{greenR1}},
			"base-1",
			map[string]bool{"R1": true},
		},
		{
			"rerun takes the place of a lost capture",
			Rows{Epic: integrateEpic(), Runs: []store.NodeRun{integrated}, Evidence: []store.EvidenceRun{
				{ID: store.NodeWorkID("int-run", 1, "capture R1"), NotCaptured: store.LostAtRestart, State: runEnded},
				{
					ID: store.NodeWorkID("int-run", 1, "capture R1 rerun"), Phase: evidence.PhaseGreen,
					State: runEnded, Verdict: evidence.VerdictHolds, Passed: true,
				},
			}},
			"base-1",
			map[string]bool{"R1": true},
		},
		{
			"lost capture without a rerun is not green",
			Rows{Epic: integrateEpic(), Runs: []store.NodeRun{integrated}, Evidence: []store.EvidenceRun{
				{ID: store.NodeWorkID("int-run", 1, "capture R1"), NotCaptured: store.LostAtRestart, State: runEnded},
			}},
			"base-1",
			map[string]bool{},
		},
		{
			"capture that did not pass is not green",
			Rows{Epic: integrateEpic(), Runs: []store.NodeRun{integrated}, Evidence: []store.EvidenceRun{
				{
					ID: store.NodeWorkID("int-run", 1, "capture R1"), Phase: evidence.PhaseGreen, State: runEnded,
					Verdict: evidence.VerdictFails,
				},
			}},
			"base-1",
			map[string]bool{},
		},
		{
			"failed integrate run is not green",
			Rows{Epic: integrateEpic(), Runs: []store.NodeRun{{
				ID: "int-run", Kind: "integrate", Outcome: outcomeFailed, Revision: "base-1", Attempt: 1,
			}}, Evidence: []store.EvidenceRun{greenR1}},
			"base-1",
			map[string]bool{},
		},
		{
			"integrate run at another revision is not green",
			Rows{Epic: integrateEpic(), Runs: []store.NodeRun{integrated}, Evidence: []store.EvidenceRun{greenR1}},
			"cand-1",
			map[string]bool{},
		},
		{
			"capture of another attempt is not green",
			Rows{Epic: integrateEpic(), Runs: []store.NodeRun{{
				ID: "int-run", Kind: "integrate", Outcome: outcomePassed, Revision: "base-1", Attempt: 2,
			}}, Evidence: []store.EvidenceRun{greenR1}},
			"base-1",
			map[string]bool{},
		},
		{
			"capture of a run that is not an integrate run is not green",
			Rows{Epic: integrateEpic(), Runs: []store.NodeRun{{
				ID: "int-run", Kind: "evidence", Outcome: outcomePassed, Revision: "base-1", Attempt: 1,
			}}, Evidence: []store.EvidenceRun{greenR1}},
			"base-1",
			map[string]bool{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := GreenAt(tc.rows, tc.revision); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("GreenAt() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestUnbuilt(t *testing.T) {
	t.Parallel()
	red := func(part string, passed bool) store.EvidenceRun {
		return store.EvidenceRun{
			ID: store.NodeWorkID("red-run", 1, part), Phase: evidence.PhaseRed, Revision: "base-1",
			State: runEnded, Verdict: evidence.VerdictFails, Passed: passed,
		}
	}
	redRun := store.NodeRun{ID: "red-run", Kind: "evidence", Outcome: outcomePassed, Attempt: 1}
	integrated := store.NodeRun{
		ID:       "int-run",
		Kind:     "integrate",
		Outcome:  outcomePassed,
		Revision: "base-1",
		Attempt:  1,
	}
	green := func(part string) store.EvidenceRun {
		return store.EvidenceRun{
			ID: store.NodeWorkID("int-run", 1, "capture "+part), Phase: evidence.PhaseGreen,
			State: runEnded, Verdict: evidence.VerdictHolds, Passed: true,
		}
	}
	r1 := store.EpicRequirement{ID: "R1", Text: "the suite passes", Item: evidenceItem("the suite passes")}
	r2 := store.EpicRequirement{ID: "R2", Text: "lint is clean", Item: evidenceItem("lint is clean")}
	bare := store.EpicRequirement{ID: "R3", Text: "the docs read well"}
	cases := []struct {
		name string
		rows Rows
		want []string
	}{
		{"nothing to prove is built", Rows{}, nil},
		{
			"red at base and green at the stored base is built",
			Rows{
				Epic: store.EpicRow{
					Base:         "base-1",
					StoredBase:   "base-1",
					Requirements: []store.EpicRequirement{r1},
				},
				Runs:     []store.NodeRun{redRun, integrated},
				Evidence: []store.EvidenceRun{red("R1", true), green("R1")},
			},
			nil,
		},
		{
			"requirement that names no item is named",
			Rows{
				Epic: store.EpicRow{Base: "base-1", StoredBase: "base-1", Requirements: []store.EpicRequirement{bare}},
			},
			[]string{"R3 names no evidence item"},
		},
		{
			"green at the stored base without a red at the base is built",
			Rows{
				Epic: store.EpicRow{
					Base:         "base-1",
					StoredBase:   "base-1",
					Requirements: []store.EpicRequirement{r1},
				},
				Runs:     []store.NodeRun{redRun, integrated},
				Evidence: []store.EvidenceRun{green("R1")},
			},
			nil,
		},
		{
			"requirement with no red at the base and no green is named for its green",
			Rows{Epic: store.EpicRow{Base: "base-1", StoredBase: "base-1", Requirements: []store.EpicRequirement{r1}}},
			[]string{"R1 is not green at the stored base base-1"},
		},
		{
			"red that did not pass does not count toward green",
			Rows{
				Epic: store.EpicRow{
					Base:         "base-1",
					StoredBase:   "base-1",
					Requirements: []store.EpicRequirement{r1},
				},
				Runs:     []store.NodeRun{redRun},
				Evidence: []store.EvidenceRun{red("R1", false)},
			},
			[]string{"R1 is not green at the stored base base-1"},
		},
		{
			"red in an evidence run that failed does not count toward green",
			Rows{
				Epic: store.EpicRow{Base: "base-1", StoredBase: "base-1", Requirements: []store.EpicRequirement{r1}},
				Runs: []store.NodeRun{{
					ID: "red-run", Kind: "evidence", Outcome: outcomeFailed, Attempt: 1,
				}},
				Evidence: []store.EvidenceRun{red("R1", true)},
			},
			[]string{"R1 is not green at the stored base base-1"},
		},
		{
			"red at another revision does not count toward green",
			Rows{
				Epic: store.EpicRow{Base: "base-1", StoredBase: "base-1", Requirements: []store.EpicRequirement{r1}},
				Runs: []store.NodeRun{redRun},
				Evidence: []store.EvidenceRun{{
					ID: store.NodeWorkID("red-run", 1, "R1"), Phase: evidence.PhaseRed, Revision: "base-0",
					State: runEnded, Verdict: evidence.VerdictFails, Passed: true,
				}},
			},
			[]string{"R1 is not green at the stored base base-1"},
		},
		{
			"red without green at the stored base is named",
			Rows{
				Epic: store.EpicRow{
					Base:         "base-1",
					StoredBase:   "base-1",
					Requirements: []store.EpicRequirement{r1},
				},
				Runs:     []store.NodeRun{redRun},
				Evidence: []store.EvidenceRun{red("R1", true)},
			},
			[]string{"R1 is not green at the stored base base-1"},
		},
		{
			"each Requirement not green at the stored base gets its own line in contract order",
			Rows{
				Epic: store.EpicRow{
					Base: "base-1", StoredBase: "base-1",
					Requirements: []store.EpicRequirement{r1, bare, r2},
				},
				Runs:     []store.NodeRun{redRun},
				Evidence: []store.EvidenceRun{red("R2", true)},
			},
			[]string{
				"R1 is not green at the stored base base-1",
				"R3 names no evidence item",
				"R2 is not green at the stored base base-1",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := Unbuilt(tc.rows); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Unbuilt() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMetAtBase(t *testing.T) {
	t.Parallel()
	node := Node{Run: store.NodeRun{ID: testRunID, Kind: "evidence", Attempt: 1}}
	holds := func(part string) store.EvidenceRun {
		return store.EvidenceRun{
			ID: workID(part), Phase: evidence.PhaseRed, State: runEnded,
			Verdict: evidence.VerdictHolds, Passed: false,
		}
	}
	passed := func(part string) store.EvidenceRun {
		return store.EvidenceRun{
			ID: workID(part), Phase: evidence.PhaseRed, State: runEnded,
			Verdict: evidence.VerdictFails, Passed: true,
		}
	}
	r1 := store.EpicRequirement{ID: "R1", Text: "the suite passes", Item: evidenceItem("the suite passes")}
	r2 := store.EpicRequirement{ID: "R2", Text: "lint is clean", Item: evidenceItem("lint is clean")}
	bare := store.EpicRequirement{ID: "R3", Text: "the docs read well"}
	cases := []struct {
		name string
		rows Rows
		want []string
	}{
		{"no red run is not met", Rows{Epic: store.EpicRow{Requirements: []store.EpicRequirement{r1}}}, nil},
		{
			"red run whose verdict holds is met",
			Rows{
				Epic:     store.EpicRow{Requirements: []store.EpicRequirement{r1}},
				Evidence: []store.EvidenceRun{holds("R1")},
			},
			[]string{"R1"},
		},
		{
			"red run whose verdict fails is not met",
			Rows{
				Epic: store.EpicRow{Requirements: []store.EpicRequirement{r1}},
				Evidence: []store.EvidenceRun{{
					ID: workID("R1"), Phase: evidence.PhaseRed, State: runEnded, Verdict: evidence.VerdictFails,
				}},
			},
			nil,
		},
		{
			"green run whose verdict holds is not a red run",
			Rows{
				Epic: store.EpicRow{Requirements: []store.EpicRequirement{r1}},
				Evidence: []store.EvidenceRun{{
					ID: workID("R1"), Phase: evidence.PhaseGreen, State: runEnded, Verdict: evidence.VerdictHolds,
				}},
			},
			nil,
		},
		{
			"red run of another attempt is not met",
			Rows{
				Epic: store.EpicRow{Requirements: []store.EpicRequirement{r1}},
				Evidence: []store.EvidenceRun{{
					ID: store.NodeWorkID(testRunID, 2, "R1"), Phase: evidence.PhaseRed, State: runEnded,
					Verdict: evidence.VerdictHolds,
				}},
			},
			nil,
		},
		{
			"met Requirements come in contract order",
			Rows{
				Epic:     store.EpicRow{Requirements: []store.EpicRequirement{r1, bare, r2}},
				Evidence: []store.EvidenceRun{holds("R2"), holds("R1")},
			},
			[]string{"R1", "R2"},
		},
		{
			"Requirement that names no item is never met",
			Rows{
				Epic:     store.EpicRow{Requirements: []store.EpicRequirement{bare}},
				Evidence: []store.EvidenceRun{holds("R3")},
			},
			nil,
		},
		{
			"met Requirement beside a passed red is met",
			Rows{
				Epic:     store.EpicRow{Requirements: []store.EpicRequirement{r1, r2}},
				Evidence: []store.EvidenceRun{holds("R1"), passed("R2")},
			},
			[]string{"R1"},
		},
		{
			"met Requirement beside a red that gave no verdict is not met, so the node fails as it did",
			Rows{
				Epic: store.EpicRow{Requirements: []store.EpicRequirement{r1, r2}},
				Evidence: []store.EvidenceRun{holds("R1"), {
					ID: workID("R2"), Phase: evidence.PhaseRed, State: runEnded,
				}},
			},
			nil,
		},
		{
			"met Requirement beside a Requirement with no red run is not met",
			Rows{
				Epic:     store.EpicRow{Requirements: []store.EpicRequirement{r1, r2}},
				Evidence: []store.EvidenceRun{holds("R1")},
			},
			nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := MetAtBase(node, tc.rows); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("MetAtBase() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestIntegrateCapturesOnlyTheRequirementsItsBatchTouched decides a batch whose one composed task, T1, claims R2 while
// R1 belongs to a task another batch integrated. Only R2 is captured at the candidate. R1 is no cause and reads green
// when it is green at the stored base, and not yet green when it is not. A result whose composed tasks do not decode
// captures every Requirement that names an item.
func TestIntegrateCapturesOnlyTheRequirementsItsBatchTouched(t *testing.T) {
	t.Parallel()
	epic := integrateEpic()
	epic.Tasks = []store.EpicTask{
		batchTask(1, []string{"R2"}, nil),
		{Seq: 2, Status: taskDone, Batch: "plan/S0/integrate", Requirements: []string{"R1"}},
	}
	earlier := store.NodeRun{
		ID: "earlier-run", Kind: "integrate", Outcome: outcomePassed, Revision: "base-1", Attempt: 1,
	}
	earlierR1 := store.EvidenceRun{
		ID: store.NodeWorkID("earlier-run", 1, "capture R1"), Phase: evidence.PhaseGreen,
		State: runEnded, Verdict: evidence.VerdictHolds, Passed: true,
	}
	const undecodable = `{"base":"base-1","candidate":"cand-1","composed":["c1"],"leftOut":[]}`
	cases := []decideCase{
		{
			"only the Requirement the batch's task claims is captured",
			batchNode(testRev, composedOne, false),
			Rows{Epic: epic, Checks: gatesPassed()},
			Action{Kind: ActCapture, IDs: []string{workID("capture R2")}, Items: []string{"R2"}},
		},
		{
			"a Requirement the batch left alone keeps its green at the stored base",
			batchNode(testRev, composedOne, true),
			Rows{
				Epic: epic, Checks: gatesPassed(), Runs: []store.NodeRun{earlier},
				Evidence: []store.EvidenceRun{earlierR1, greenCapture("capture R2", true)},
			},
			Action{Kind: ActAdvance, Output: "R1 green at the stored base base-1; no task of this batch changes it, " +
				"so it was not captured again\nR2 green\nR3 not yet green"},
		},
		{
			"a Requirement the batch left alone that is not green at the stored base is no cause",
			batchNode(testRev, composedOne, true),
			Rows{Epic: epic, Checks: gatesPassed(), Evidence: []store.EvidenceRun{greenCapture("capture R2", true)}},
			Action{Kind: ActAdvance, Output: "R1 not yet green\nR2 green\nR3 not yet green"},
		},
		{
			"a result whose composed tasks do not decode captures every Requirement",
			batchNode(testRev, undecodable, false),
			Rows{Epic: epic, Checks: gatesPassed()},
			Action{
				Kind:  ActCapture,
				IDs:   []string{workID("capture R1"), workID("capture R2")},
				Items: []string{"R1", "R2"},
			},
		},
	}
	checkDecisions(t, Integrate{}, cases)
}

// TestGreenAtCarriesWhatABatchDidNotCapture reads green at candidates whose integrate runs captured only part of the
// Requirements. A Requirement a run did not capture is green when it is green at the base the run's result names, down
// a chain of such bases; one the run captured and that did not pass is not, whatever the base held; and bases that lead
// back to each other end the walk.
func TestGreenAtCarriesWhatABatchDidNotCapture(t *testing.T) {
	t.Parallel()
	passed := func(id, revision, base string) store.NodeRun {
		result := ""
		if base != "" {
			result = `{"base":"` + base + `","candidate":"` + revision + `","composed":[{"task":1,"commit":"c1"}],` +
				`"leftOut":[]}`
		}
		return store.NodeRun{
			ID: id, Kind: "integrate", Outcome: outcomePassed, Revision: revision, Attempt: 1, Result: result,
		}
	}
	capture := func(run, requirement string, ok bool) store.EvidenceRun {
		verdict := evidence.VerdictFails
		if ok {
			verdict = evidence.VerdictHolds
		}
		return store.EvidenceRun{
			ID: store.NodeWorkID(run, 1, "capture "+requirement), Phase: evidence.PhaseGreen,
			State: runEnded, Verdict: verdict, Passed: ok,
		}
	}
	cases := []struct {
		name     string
		runs     []store.NodeRun
		evidence []store.EvidenceRun
		revision string
		want     map[string]bool
	}{
		{
			"an uncaptured Requirement carries its green from the run's base",
			[]store.NodeRun{passed("first", "base-1", ""), passed("second", "cand-1", "base-1")},
			[]store.EvidenceRun{capture("first", "R1", true), capture("second", "R2", true)},
			"cand-1",
			map[string]bool{"R1": true, "R2": true},
		},
		{
			"green carries down a chain of bases",
			[]store.NodeRun{
				passed("first", "base-1", ""), passed("second", "cand-1", "base-1"),
				passed("third", "cand-2", "cand-1"),
			},
			[]store.EvidenceRun{capture("first", "R1", true), capture("second", "R2", true)},
			"cand-2",
			map[string]bool{"R1": true, "R2": true},
		},
		{
			"a captured Requirement that did not pass is not green, though its base held it",
			[]store.NodeRun{passed("first", "base-1", ""), passed("second", "cand-1", "base-1")},
			[]store.EvidenceRun{capture("first", "R1", true), capture("second", "R1", false)},
			"cand-1",
			map[string]bool{},
		},
		{
			"an uncaptured Requirement not green at the base is not green",
			[]store.NodeRun{passed("first", "base-1", ""), passed("second", "cand-1", "base-1")},
			[]store.EvidenceRun{capture("second", "R2", true)},
			"cand-1",
			map[string]bool{"R2": true},
		},
		{
			"bases that lead back to each other end the walk",
			[]store.NodeRun{passed("first", "cand-1", "cand-2"), passed("second", "cand-2", "cand-1")},
			nil,
			"cand-1",
			map[string]bool{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rows := Rows{Epic: integrateEpic(), Runs: tc.runs, Evidence: tc.evidence}
			if got := GreenAt(rows, tc.revision); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("GreenAt() = %v, want %v", got, tc.want)
			}
		})
	}
}
