package kinds

import (
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// reproItem returns a test item, which names its test files, so a Requirement that names it is a test item.
func reproItem(conclusion string) evidence.Item {
	item := evidenceItem(conclusion)
	item.Kind = evidence.KindTest
	item.Tests = []string{"repro_test.go"}
	return item
}

// reproEpic returns an epic at base-1 stored at base-1 with the Done gates of integrateEpic. R1 and R3 name test items
// and R2 names a capture item, so only R1 and R3 get a reproduction.
func reproEpic() store.EpicRow {
	return store.EpicRow{
		Base:       "base-1",
		StoredBase: "base-1",
		DoneGates:  []string{"just check", "just lint"},
		Requirements: []store.EpicRequirement{
			{ID: "R1", Text: "the suite passes", Item: reproItem("the suite passes")},
			{ID: "R2", Text: "lint is clean", Item: evidenceItem("lint is clean")},
			{ID: "R3", Text: "the parser reads", Item: reproItem("the parser reads")},
		},
	}
}

func TestIntegrateRepro(t *testing.T) {
	t.Parallel()
	epic := reproEpic()
	captured := []store.EvidenceRun{
		greenCapture("capture R1", true), greenCapture("capture R2", true), greenCapture("capture R3", true),
	}
	// checks returns the Done gates of epic, passed, with extra.
	checks := func(extra ...store.NodeCheck) []store.NodeCheck {
		return append(gatesPassed(), extra...)
	}
	const allGreen = "R1 green\nR2 green\nR3 green"
	cases := []decideCase{
		{
			"captures still running hold back the reproductions",
			batchNode(testRev, composedOne, false),
			Rows{Epic: epic, Checks: gatesPassed(), Evidence: []store.EvidenceRun{
				runningCapture("capture R1"), greenCapture("capture R2", true), greenCapture("capture R3", true),
			}},
			Action{Kind: ActNone},
		},
		{
			"settled captures start the reproduction of every test item at once",
			batchNode(testRev, composedOne, false),
			Rows{Epic: epic, Checks: gatesPassed(), Evidence: captured},
			Action{Kind: ActRepro, IDs: []string{workID("repro R1"), workID("repro R3")}, Items: []string{"R1", "R3"}},
		},
		{
			"missing reproduction starts while another runs",
			batchNode(testRev, composedOne, false),
			Rows{Epic: epic, Checks: checks(runningCheck("repro R1")), Evidence: captured},
			Action{Kind: ActRepro, IDs: []string{workID("repro R3")}, Items: []string{"R3"}},
		},
		{
			"running reproduction waits",
			batchNode(testRev, composedOne, true),
			Rows{Epic: epic, Checks: checks(runningCheck("repro R1"), passedCheck("repro R3")), Evidence: captured},
			Action{Kind: ActNone},
		},
		{
			"reproductions that passed with refs not checked give refs",
			batchNode(testRev, composedOne, false),
			Rows{Epic: epic, Checks: checks(passedCheck("repro R1"), passedCheck("repro R3")), Evidence: captured},
			Action{Kind: ActRefs},
		},
		{
			"reproductions that passed advance once the refs are checked",
			batchNode(testRev, composedOne, true),
			Rows{Epic: epic, Checks: checks(passedCheck("repro R1"), passedCheck("repro R3")), Evidence: captured},
			Action{Kind: ActAdvance, Output: allGreen},
		},
		{
			"failed reproduction still advances and adds no cause",
			batchNode(testRev, composedOne, true),
			Rows{Epic: epic, Checks: checks(failedCheck("repro R1"), passedCheck("repro R3")), Evidence: captured},
			Action{Kind: ActAdvance, Output: allGreen},
		},
		{
			"timed out reproduction still advances and adds no cause",
			batchNode(testRev, composedOne, true),
			Rows{Epic: epic, Checks: checks(timedOutCheck("repro R1"), passedCheck("repro R3")), Evidence: captured},
			Action{Kind: ActAdvance, Output: allGreen},
		},
		{
			"reproduction lost at a restart reruns once",
			batchNode(testRev, composedOne, true),
			Rows{Epic: epic, Checks: checks(lostCheck("repro R1"), passedCheck("repro R3")), Evidence: captured},
			Action{Kind: ActRepro, IDs: []string{workID("repro R1 rerun")}, Items: []string{"R1"}},
		},
		{
			"rerun of a lost reproduction waits while it runs",
			batchNode(testRev, composedOne, true),
			Rows{Epic: epic, Checks: checks(
				lostCheck("repro R1"), runningCheck("repro R1 rerun"), passedCheck("repro R3"),
			), Evidence: captured},
			Action{Kind: ActNone},
		},
		{
			"rerun of a lost reproduction lost again parks",
			batchNode(testRev, composedOne, true),
			Rows{Epic: epic, Checks: checks(
				lostCheck("repro R1"), lostCheck("repro R1 rerun"), passedCheck("repro R3"),
			), Evidence: captured},
			Action{Kind: ActPark, Output: "integrate plan/S1/integrate: reproduction R1 lost at a restart twice"},
		},
		{
			"rerun that passed stands for its lost reproduction",
			batchNode(testRev, composedOne, true),
			Rows{Epic: epic, Checks: checks(
				lostCheck("repro R1"), passedCheck("repro R1 rerun"), passedCheck("repro R3"),
			), Evidence: captured},
			Action{Kind: ActAdvance, Output: allGreen},
		},
	}
	checkDecisions(t, Integrate{}, cases)
}

func TestUnbuiltReproduction(t *testing.T) {
	t.Parallel()
	integrated := store.NodeRun{
		ID:       "int-run",
		Kind:     "integrate",
		Outcome:  outcomePassed,
		Revision: "base-1",
		Attempt:  1,
	}
	redRun := store.NodeRun{ID: "red-run", Kind: "evidence", Outcome: outcomePassed, Attempt: 1}
	red := store.EvidenceRun{
		ID: store.NodeWorkID("red-run", 1, "R1"), Phase: evidence.PhaseRed, Revision: "base-1",
		State: runEnded, Verdict: evidence.VerdictFails, Passed: true,
	}
	green := store.EvidenceRun{
		ID: store.NodeWorkID("int-run", 1, "capture R1"), Phase: evidence.PhaseGreen, State: runEnded,
		Verdict: evidence.VerdictHolds, Passed: true,
	}
	notGreen := green
	notGreen.Verdict, notGreen.Passed = evidence.VerdictFails, false
	reproID := store.NodeWorkID("int-run", 1, "repro R1")
	rerunID := store.NodeWorkID("int-run", 1, "repro R1 rerun")
	const disagree = "R1 judge and exit disagree at the epic's base base-1: " +
		"red was judged fails, but its reproduction with the candidate's tests exited 0"
	const noRepro = "R1 has no reproduction at the epic's base base-1"
	r1 := store.EpicRequirement{ID: "R1", Text: "the suite passes", Item: reproItem("the suite passes")}
	// rows returns the epic with R1 red at its base and green at its stored base, with checks on the integrate run.
	rows := func(checks ...store.NodeCheck) Rows {
		return Rows{
			Epic:     store.EpicRow{Base: "base-1", StoredBase: "base-1", Requirements: []store.EpicRequirement{r1}},
			Runs:     []store.NodeRun{redRun, integrated},
			Evidence: []store.EvidenceRun{red, green},
			Checks:   checks,
		}
	}
	cases := []struct {
		name string
		rows Rows
		want []string
	}{
		{
			"reproduction that exited 0 and did not time out names the disagreement",
			rows(store.NodeCheck{ID: reproID, State: store.NodeEnded, Exit: 0}),
			[]string{disagree},
		},
		{
			"reproduction that exited 1 is red's exit holding",
			rows(store.NodeCheck{ID: reproID, State: store.NodeEnded, Exit: 1}),
			nil,
		},
		{
			"reproduction that timed out is red's exit holding",
			rows(store.NodeCheck{ID: reproID, State: store.NodeEnded, TimedOut: true}),
			nil,
		},
		{
			"reproduction that exited 0 with another run's exit 1 is red's exit holding",
			Rows{
				Epic: store.EpicRow{Base: "base-1", StoredBase: "base-1", Requirements: []store.EpicRequirement{r1}},
				Runs: []store.NodeRun{
					redRun, integrated,
					{ID: "int-run-2", Kind: "integrate", Outcome: outcomePassed, Revision: "base-1", Attempt: 1},
				},
				Evidence: []store.EvidenceRun{red, green},
				Checks: []store.NodeCheck{
					{ID: reproID, State: store.NodeEnded, Exit: 0},
					{ID: store.NodeWorkID("int-run-2", 1, "repro R1"), State: store.NodeEnded, Exit: 1},
				},
			},
			nil,
		},
		{
			"no reproduction names that none ran",
			rows(),
			[]string{noRepro},
		},
		{
			"reproduction that did not run names why",
			rows(store.NodeCheck{ID: reproID, State: store.NodeEnded, NotRun: "no grant"}),
			[]string{noRepro + ": no grant"},
		},
		{
			"rerun of a lost reproduction decides it",
			rows(
				store.NodeCheck{ID: reproID, State: store.NodeEnded, NotRun: store.LostAtRestart},
				store.NodeCheck{ID: rerunID, State: store.NodeEnded, Exit: 0},
			),
			[]string{disagree},
		},
		{
			"not green at the stored base names only that",
			Rows{
				Epic: store.EpicRow{
					Base:         "base-1",
					StoredBase:   "base-1",
					Requirements: []store.EpicRequirement{r1},
				},
				Runs:     []store.NodeRun{redRun, integrated},
				Evidence: []store.EvidenceRun{red, notGreen},
				Checks:   []store.NodeCheck{{ID: reproID, State: store.NodeEnded, Exit: 0}},
			},
			[]string{"R1 is not green at the stored base base-1"},
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

func TestDisagreement(t *testing.T) {
	t.Parallel()
	integrated := store.NodeRun{
		ID:       "int-run",
		Kind:     "integrate",
		Outcome:  outcomePassed,
		Revision: "base-1",
		Attempt:  1,
	}
	const disagree = "R1 judge and exit disagree at the epic's base base-1: " +
		"red was judged fails, but its reproduction with the candidate's tests exited 0"
	r1 := store.EpicRequirement{ID: "R1", Text: "the suite passes", Item: reproItem("the suite passes")}
	r2 := store.EpicRequirement{ID: "R2", Text: "lint is clean", Item: evidenceItem("lint is clean")}
	epic := store.EpicRow{
		Base: "base-1", StoredBase: "base-1", Requirements: []store.EpicRequirement{r1, r2},
	}
	exit0 := store.NodeCheck{ID: store.NodeWorkID("int-run", 1, "repro R1"), State: store.NodeEnded}
	cases := []struct {
		name string
		rows Rows
		id   string
		want string
	}{
		{
			"reproduction that exited 0 names the disagreement",
			Rows{Epic: epic, Runs: []store.NodeRun{integrated}, Checks: []store.NodeCheck{exit0}},
			"R1",
			disagree,
		},
		{
			"reproduction that exited 1 gives nothing",
			Rows{Epic: epic, Runs: []store.NodeRun{integrated}, Checks: []store.NodeCheck{
				{ID: exit0.ID, State: store.NodeEnded, Exit: 1},
			}},
			"R1",
			"",
		},
		{
			"reproduction that timed out gives nothing",
			Rows{Epic: epic, Runs: []store.NodeRun{integrated}, Checks: []store.NodeCheck{
				{ID: exit0.ID, State: store.NodeEnded, TimedOut: true},
			}},
			"R1",
			"",
		},
		{
			"reproduction that did not run gives nothing",
			Rows{Epic: epic, Runs: []store.NodeRun{integrated}, Checks: []store.NodeCheck{
				{ID: exit0.ID, State: store.NodeEnded, NotRun: "no grant"},
			}},
			"R1",
			"",
		},
		{
			"no reproduction gives nothing",
			Rows{Epic: epic, Runs: []store.NodeRun{integrated}},
			"R1",
			"",
		},
		{
			"reproduction of a run that did not pass gives nothing",
			Rows{Epic: epic, Runs: []store.NodeRun{{
				ID: "int-run", Kind: "integrate", Outcome: outcomeFailed, Revision: "base-1", Attempt: 1,
			}}, Checks: []store.NodeCheck{exit0}},
			"R1",
			"",
		},
		{
			"reproduction at another revision gives nothing",
			Rows{Epic: epic, Runs: []store.NodeRun{{
				ID: "int-run", Kind: "integrate", Outcome: outcomePassed, Revision: "base-0", Attempt: 1,
			}}, Checks: []store.NodeCheck{exit0}},
			"R1",
			"",
		},
		{
			"Requirement that names a capture item gives nothing",
			Rows{Epic: epic, Runs: []store.NodeRun{integrated}, Checks: []store.NodeCheck{
				{ID: store.NodeWorkID("int-run", 1, "repro R2"), State: store.NodeEnded},
			}},
			"R2",
			"",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := Disagreement(tc.rows, tc.id); got != tc.want {
				t.Errorf("Disagreement() = %q, want %q", got, tc.want)
			}
		})
	}
}
