package kinds

import (
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/workflow"
)

// environmentReason ends the reason a run parks with when its last run exits store.EnvironmentExit.
const environmentReason = "an environment result, not a verdict"

// environmentCheck returns an ended check of the work part of the test run that exited store.EnvironmentExit.
func environmentCheck(part string) store.NodeCheck {
	return store.NodeCheck{ID: workID(part), State: store.NodeEnded, Exit: store.EnvironmentExit}
}

// environmentCapture returns an ended green capture of the work part of the test run that exited store.EnvironmentExit,
// as enginehost records it: not captured, with its exit.
func environmentCapture(part string) store.EvidenceRun {
	return store.EvidenceRun{
		ID: workID(part), Phase: evidence.PhaseGreen, State: runEnded, Exit: store.EnvironmentExit,
		NotCaptured: store.NotCapturedEnvironment,
	}
}

// environmentRunOf returns an ended green evidence run of the test run's part that exited store.EnvironmentExit.
func environmentRunOf(part string) store.EvidenceRun {
	run := environmentCapture(part)
	run.Item = part
	return run
}

// failedCapture returns an ended green capture of the work part of the test run that exited 1 and failed its conclusion.
func failedCapture(part string) store.EvidenceRun {
	return store.EvidenceRun{
		ID: workID(part), Phase: evidence.PhaseGreen, State: runEnded, Verdict: evidence.VerdictFails, Exit: 1,
	}
}

// gateNode returns a gate or command node of kind at the test run's revision, at path plan/S1/unit, with refsChecked when
// its refs were compared.
func gateNode(kind string, refsChecked bool) Node {
	return Node{
		Def: workflow.Node{Kind: kind},
		Run: store.NodeRun{
			ID: testRunID, Path: "plan/S1/unit", Kind: kind, Attempt: 1, State: store.NodeRunning,
			Revision: testRev, RefsChecked: refsChecked,
		},
	}
}

func TestIntegrateEnvironmentExitDoneGates(t *testing.T) {
	t.Parallel()
	epic := integrateEpic()
	cases := []decideCase{
		{
			"Done gate that exits 75 reruns",
			batchNode(testRev, composedOne, false),
			Rows{Epic: epic, Checks: []store.NodeCheck{environmentCheck("done 1")}},
			Action{Kind: ActStart, IDs: []string{workID("done 1 rerun")}, Items: []string{"just check"}},
		},
		{
			"rerun that exits 75 reruns once more",
			batchNode(testRev, composedOne, false),
			Rows{Epic: epic, Checks: []store.NodeCheck{environmentCheck("done 1"), environmentCheck("done 1 rerun")}},
			Action{Kind: ActStart, IDs: []string{workID("done 1 rerun rerun")}, Items: []string{"just check"}},
		},
		{
			"rerun that exits 75 waits while it runs",
			batchNode(testRev, composedOne, false),
			Rows{Epic: epic, Checks: []store.NodeCheck{environmentCheck("done 1"), runningCheck("done 1 rerun")}},
			Action{Kind: ActNone},
		},
		{
			"Done gate that exits 75 and whose rerun passes moves to the next gate",
			batchNode(testRev, composedOne, false),
			Rows{Epic: epic, Checks: []store.NodeCheck{environmentCheck("done 1"), passedCheck("done 1 rerun")}},
			Action{Kind: ActStart, IDs: []string{workID("done 2")}, Items: []string{"just lint"}},
		},
		{
			"Done gate that exits 75 on its last run parks",
			batchNode(testRev, composedOne, false),
			Rows{Epic: epic, Checks: []store.NodeCheck{
				environmentCheck("done 1"), environmentCheck("done 1 rerun"), environmentCheck("done 1 rerun rerun"),
			}},
			Action{
				Kind: ActPark,
				Output: "integrate plan/S1/integrate: Done gate just check exited 75 on its last run: " +
					environmentReason,
			},
		},
		{
			"Done gate lost at a restart and then exiting 75 reruns on the same budget",
			batchNode(testRev, composedOne, false),
			Rows{Epic: epic, Checks: []store.NodeCheck{lostCheck("done 1"), environmentCheck("done 1 rerun")}},
			Action{Kind: ActStart, IDs: []string{workID("done 1 rerun rerun")}, Items: []string{"just check"}},
		},
		{
			"Done gate that exits 75 and times out fails at once",
			batchNode(testRev, composedOne, true),
			Rows{Epic: epic, Checks: []store.NodeCheck{{
				ID: workID("done 1"), State: store.NodeEnded, Exit: store.EnvironmentExit, TimedOut: true,
			}}},
			Action{Kind: ActEnd, Outcome: outcomeFailed, Output: "Done gate just check failed"},
		},
		{
			"Done gate that exits 1 is not rerun once its triage finds a change",
			batchNode(testRev, composedOne, true),
			Rows{
				Epic: epic, Checks: []store.NodeCheck{failedCheck("done 1")},
				Triages: []store.GateTriage{changeTriaged(workID("done 1"))},
			},
			Action{Kind: ActEnd, Outcome: outcomeFailed, Output: "Done gate just check failed; " + changeNote},
		},
	}
	checkDecisions(t, Integrate{}, cases)
}

func TestIntegrateEnvironmentExitCaptures(t *testing.T) {
	t.Parallel()
	epic := integrateEpic()
	cases := []decideCase{
		{
			"capture that exits 75 reruns",
			batchNode(testRev, composedOne, true),
			Rows{Epic: epic, Checks: gatesPassed(), Evidence: []store.EvidenceRun{
				environmentCapture("capture R1"), greenCapture("capture R2", true),
			}},
			Action{Kind: ActCapture, IDs: []string{workID("capture R1 rerun")}, Items: []string{"R1"}},
		},
		{
			"rerun that exits 75 reruns once more",
			batchNode(testRev, composedOne, true),
			Rows{Epic: epic, Checks: gatesPassed(), Evidence: []store.EvidenceRun{
				environmentCapture("capture R1"), environmentCapture("capture R1 rerun"),
				greenCapture("capture R2", true),
			}},
			Action{Kind: ActCapture, IDs: []string{workID("capture R1 rerun rerun")}, Items: []string{"R1"}},
		},
		{
			"rerun that exits 75 waits while it runs",
			batchNode(testRev, composedOne, true),
			Rows{Epic: epic, Checks: gatesPassed(), Evidence: []store.EvidenceRun{
				environmentCapture("capture R1"), runningCapture("capture R1 rerun"), greenCapture("capture R2", true),
			}},
			Action{Kind: ActNone},
		},
		{
			"capture that exits 75 on its last run parks",
			batchNode(testRev, composedOne, true),
			Rows{Epic: epic, Checks: gatesPassed(), Evidence: []store.EvidenceRun{
				environmentCapture("capture R1"), environmentCapture("capture R1 rerun"),
				environmentCapture("capture R1 rerun rerun"), greenCapture("capture R2", true),
			}},
			Action{Kind: ActPark, Output: "integrate plan/S1/integrate: capture R1 exited 75 on its last run: " +
				environmentReason},
		},
		{
			"rerun that passes stands for its capture that exited 75",
			batchNode(testRev, composedOne, true),
			Rows{Epic: epic, Checks: gatesPassed(), Evidence: []store.EvidenceRun{
				environmentCapture("capture R1"),
				greenCapture("capture R1 rerun", true),
				greenCapture("capture R2", true),
			}},
			Action{Kind: ActAdvance, Output: "R1 green\nR2 green\nR3 not yet green"},
		},
		{
			"capture that exits 1 is not rerun and is not green",
			batchNode(testRev, composedOne, true),
			Rows{Epic: epic, Checks: gatesPassed(), Evidence: []store.EvidenceRun{
				failedCapture("capture R1"),
				greenCapture("capture R2", true),
			}},
			Action{Kind: ActAdvance, Output: "R1 not yet green\nR2 green\nR3 not yet green"},
		},
	}
	checkDecisions(t, Integrate{}, cases)
}

// environmentCheckCases returns the decisions a gate or command node of kind makes for a check that exits 75.
func environmentCheckCases(kind string) []decideCase {
	return []decideCase{
		{
			"check that exits 75 reruns",
			gateNode(kind, false),
			Rows{Checks: []store.NodeCheck{environmentCheck("check")}},
			Action{Kind: ActStart, IDs: []string{workID("check rerun")}},
		},
		{
			"rerun that exits 75 reruns once more",
			gateNode(kind, false),
			Rows{Checks: []store.NodeCheck{environmentCheck("check"), environmentCheck("check rerun")}},
			Action{Kind: ActStart, IDs: []string{workID("check rerun rerun")}},
		},
		{
			"rerun that exits 75 waits while it runs",
			gateNode(kind, false),
			Rows{Checks: []store.NodeCheck{environmentCheck("check"), runningCheck("check rerun")}},
			Action{Kind: ActNone},
		},
		{
			"rerun that passes passes without a second rerun",
			gateNode(kind, true),
			Rows{Checks: []store.NodeCheck{environmentCheck("check"), passedCheck("check rerun")}},
			Action{Kind: ActEnd, Outcome: outcomePassed},
		},
		{
			"check that exits 75 on its last run parks",
			gateNode(kind, false),
			Rows{Checks: []store.NodeCheck{
				environmentCheck("check"), environmentCheck("check rerun"), environmentCheck("check rerun rerun"),
			}},
			Action{Kind: ActPark, Output: kind + " plan/S1/unit exited 75 on its last run: " +
				environmentReason},
		},
		{
			"check that exits 75 and times out fails at once",
			gateNode(kind, true),
			Rows{Checks: []store.NodeCheck{{
				ID: workID("check"), State: store.NodeEnded, Exit: store.EnvironmentExit, TimedOut: true,
			}}},
			Action{Kind: ActEnd, Outcome: outcomeFailed},
		},
		{
			"check that exits 1 is not rerun once its triage finds a change",
			gateNode(kind, true),
			Rows{
				Checks:  []store.NodeCheck{failedCheck("check")},
				Triages: []store.GateTriage{changeTriaged(workID("check"))},
			},
			Action{Kind: ActEnd, Outcome: outcomeFailed, Output: changeNote},
		},
	}
}

func TestGateEnvironmentExit(t *testing.T) {
	t.Parallel()
	checkDecisions(t, Gate{}, environmentCheckCases("gate"))
}

func TestCommandEnvironmentExit(t *testing.T) {
	t.Parallel()
	checkDecisions(t, Command{}, environmentCheckCases("command"))
}

func TestEvidenceEnvironmentExit(t *testing.T) {
	t.Parallel()
	r1 := store.EpicRequirement{ID: "R1", Text: "the suite passes", Item: evidenceItem("the suite passes")}
	epic := store.EpicRow{Requirements: []store.EpicRequirement{r1}}
	cases := []decideCase{
		{
			"capture that exits 75 reruns",
			nodeWith(testRev, false),
			Rows{Epic: epic, Evidence: []store.EvidenceRun{environmentRunOf("R1")}},
			Action{Kind: ActStart, IDs: []string{workID("R1 rerun")}, Items: []string{"R1"}},
		},
		{
			"capture that exits 75 on its last run parks",
			nodeWith(testRev, false),
			Rows{Epic: epic, Evidence: []store.EvidenceRun{
				environmentRunOf("R1"), environmentRunOf("R1 rerun"), environmentRunOf("R1 rerun rerun"),
			}},
			Action{Kind: ActPark, Output: "capture R1 exited 75 on its last run: " + environmentReason},
		},
		{
			"rerun that passes passes the evidence node",
			nodeWith(testRev, true),
			Rows{Epic: epic, Evidence: []store.EvidenceRun{
				environmentRunOf("R1"),
				{ID: workID("R1 rerun"), Item: "R1", Phase: evidence.PhaseGreen, State: "ended", Passed: true},
			}},
			Action{Kind: ActEnd, Outcome: outcomePassed, Output: "R1 green: passed"},
		},
		{
			"capture that exits 1 fails at once",
			nodeWith(testRev, true),
			Rows{Epic: epic, Evidence: []store.EvidenceRun{
				failedCapture("R1"),
			}},
			Action{Kind: ActEnd, Outcome: outcomeFailed, Output: "R1 green: failed"},
		},
	}
	checkDecisions(t, Evidence{}, cases)
}

// TestRerunsAfterEnvironmentExit: with no triage rerun spent, the rule reruns as it always did, by position. A triage
// rerun does not count toward the reruns an exit of 75 gets, and a triaged check reruns within its budget.
func TestRerunsAfterEnvironmentExit(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		k       int
		outcome chainOutcome
		spent   triageSpent
		want    bool
	}{
		{"first run lost at a restart reruns", 0, chainLost, triageSpent{}, true},
		{"rerun lost at a restart does not rerun again", 1, chainLost, triageSpent{}, false},
		{"first run exiting 75 reruns", 0, chainEnvironment, triageSpent{}, true},
		{"first rerun exiting 75 reruns once more", 1, chainEnvironment, triageSpent{}, true},
		{"last run exiting 75 does not rerun", 2, chainEnvironment, triageSpent{}, false},
		{"run that ended otherwise does not rerun", 0, chainEnded, triageSpent{}, false},
		{"running run does not rerun", 1, chainRunning, triageSpent{}, false},
		{"run exiting 75 after a triage rerun keeps its own two reruns", 2, chainEnvironment, triageSpent{flake: 1},
			true},
		{"third run exiting 75 past a triage rerun does not rerun", 3, chainEnvironment, triageSpent{flake: 1}, false},
		{"untriaged check does not rerun", 0, chainTriage, triageSpent{}, false},
		{"first environment verdict reruns", 0, chainTriagedEnvironment, triageSpent{}, true},
		{"second environment verdict reruns", 1, chainTriagedEnvironment, triageSpent{environment: 1}, true},
		{"third environment verdict does not rerun", 2, chainTriagedEnvironment, triageSpent{environment: 2}, false},
		{"first flake verdict reruns", 0, chainTriagedFlake, triageSpent{}, true},
		{"second flake verdict does not rerun", 1, chainTriagedFlake, triageSpent{flake: 1}, false},
		{"flake verdict after two environment reruns reruns", 2, chainTriagedFlake, triageSpent{environment: 2}, true},
	}
	for _, tc := range cases {
		if got := rerunsAfter(tc.k, tc.outcome, tc.spent); got != tc.want {
			t.Errorf("%s: rerunsAfter(%d, %d, %+v) = %v, want %v", tc.name, tc.k, tc.outcome, tc.spent, got, tc.want)
		}
	}
}
