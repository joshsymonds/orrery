package kinds

import (
	"reflect"
	"strconv"
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

// reproEpic returns an epic at base-1 stored at base-1 with the Done gates of integrateEpic, whose task T1 claims R1 and
// R3 and contributes to R2. R1 and R3 name test items and R2 names a capture item, so only R1 and R3 get a
// reproduction.
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
		Tasks: []store.EpicTask{batchTask(1, []string{"R1", "R3"}, []string{"R2"})},
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
			batchNode(testRev, composedOne(), false),
			Rows{Epic: epic, Checks: gatesPassed(), Evidence: []store.EvidenceRun{
				runningCapture("capture R1"), greenCapture("capture R2", true), greenCapture("capture R3", true),
			}},
			Action{Kind: ActNone},
		},
		{
			"settled captures start the reproduction of every test item at once",
			batchNode(testRev, composedOne(), false),
			Rows{Epic: epic, Checks: gatesPassed(), Evidence: captured},
			Action{Kind: ActRepro, IDs: []string{workID("repro R1"), workID("repro R3")}, Items: []string{"R1", "R3"}},
		},
		{
			"missing reproduction starts while another runs",
			batchNode(testRev, composedOne(), false),
			Rows{Epic: epic, Checks: checks(runningCheck("repro R1")), Evidence: captured},
			Action{Kind: ActRepro, IDs: []string{workID("repro R3")}, Items: []string{"R3"}},
		},
		{
			"running reproduction waits",
			batchNode(testRev, composedOne(), true),
			Rows{Epic: epic, Checks: checks(runningCheck("repro R1"), passedCheck("repro R3")), Evidence: captured},
			Action{Kind: ActNone},
		},
		{
			"reproductions that passed and were ruled on with refs not checked give refs",
			batchNode(testRev, composedOne(), false),
			Rows{
				Epic:     epic,
				Checks:   checks(passedCheck("repro R1"), passedCheck("repro R3")),
				Evidence: captured,
				Rulings: []store.ReproRuling{
					ruled("repro R1", evidence.VerdictHolds),
					ruled("repro R3", evidence.VerdictFails),
				},
			},
			Action{Kind: ActRefs},
		},
		{
			"reproductions that passed and were ruled on advance once the refs are checked",
			batchNode(testRev, composedOne(), true),
			Rows{
				Epic:     epic,
				Checks:   checks(passedCheck("repro R1"), passedCheck("repro R3")),
				Evidence: captured,
				Rulings: []store.ReproRuling{
					ruled("repro R1", evidence.VerdictHolds),
					ruled("repro R3", evidence.VerdictFails),
				},
			},
			Action{Kind: ActAdvance, Output: allGreen},
		},
		{
			"failed reproduction still advances and adds no cause",
			batchNode(testRev, composedOne(), true),
			Rows{
				Epic: epic, Checks: checks(failedCheck("repro R1"), passedCheck("repro R3")), Evidence: captured,
				Rulings: []store.ReproRuling{ruled("repro R3", evidence.VerdictHolds)},
			},
			Action{Kind: ActAdvance, Output: allGreen},
		},
		{
			"timed out reproduction still advances and adds no cause",
			batchNode(testRev, composedOne(), true),
			Rows{
				Epic: epic, Checks: checks(timedOutCheck("repro R1"), passedCheck("repro R3")), Evidence: captured,
				Rulings: []store.ReproRuling{ruled("repro R3", evidence.VerdictHolds)},
			},
			Action{Kind: ActAdvance, Output: allGreen},
		},
		{
			"reproduction lost at a restart reruns once",
			batchNode(testRev, composedOne(), true),
			Rows{Epic: epic, Checks: checks(lostCheck("repro R1"), passedCheck("repro R3")), Evidence: captured},
			Action{Kind: ActRepro, IDs: []string{workID("repro R1 rerun")}, Items: []string{"R1"}},
		},
		{
			"rerun of a lost reproduction waits while it runs",
			batchNode(testRev, composedOne(), true),
			Rows{Epic: epic, Checks: checks(
				lostCheck("repro R1"), runningCheck("repro R1 rerun"), passedCheck("repro R3"),
			), Evidence: captured},
			Action{Kind: ActNone},
		},
		{
			"rerun of a lost reproduction lost again parks",
			batchNode(testRev, composedOne(), true),
			Rows{Epic: epic, Checks: checks(
				lostCheck("repro R1"), lostCheck("repro R1 rerun"), passedCheck("repro R3"),
			), Evidence: captured},
			Action{Kind: ActPark, Output: "integrate plan/S1/integrate: reproduction R1 lost at a restart twice"},
		},
		{
			"rerun that passed stands for its lost reproduction",
			batchNode(testRev, composedOne(), true),
			Rows{
				Epic: epic, Checks: checks(
					lostCheck("repro R1"), passedCheck("repro R1 rerun"), passedCheck("repro R3"),
				), Evidence: captured,
				Rulings: []store.ReproRuling{
					ruled("repro R1 rerun", evidence.VerdictHolds), ruled("repro R3", evidence.VerdictHolds),
				},
			},
			Action{Kind: ActAdvance, Output: allGreen},
		},
	}
	checkDecisions(t, Integrate{}, cases)
}

// ruled returns the ruling of the first judge, which ran under its own step, on the reproduction check of the work part
// of the test run, with verdict and no reason.
func ruled(part, verdict string) store.ReproRuling {
	return store.ReproRuling{Check: workID(part), Judge: 0, Step: "judge-" + part, Verdict: verdict}
}

// judgeStep returns the step of judge number judge of the reproduction of Requirement id of the test run, ended with
// end, or running when end is "".
func judgeStep(id string, judge int64, end string) store.StepRow {
	return store.StepRow{ID: ReproJudgeID(batchNode(testRev, composedOne(), true).Run, id, judge), End: end}
}

// reproJudgeAction returns the ActReproJudge action of mode for judge number judge of the reproduction of Requirement id of
// the test run, whose check is the work part.
func reproJudgeAction(mode, id, part string, judge int64) Action {
	return Action{
		Kind: ActReproJudge, IDs: []string{ReproJudgeID(batchNode(testRev, composedOne(), true).Run, id, judge)},
		Items: []string{mode, id, workID(part), strconv.FormatInt(judge, 10)},
	}
}

// TestIntegrateReproJudges covers the judges of an integrate run's reproductions: a reproduction that ran and exited 0
// without timing out gets a judge, in contract order, under its derived id; the run waits while a judge runs and no
// other needs an action; an ended judge is recorded; a first judge with no verdict starts the second; and the run
// settles once each such reproduction's rulings settle it, whatever they rule. A reproduction that exited non-zero,
// timed out or did not run gets no judge.
func TestIntegrateReproJudges(t *testing.T) {
	t.Parallel()
	epic := reproEpic()
	captured := []store.EvidenceRun{
		greenCapture("capture R1", true), greenCapture("capture R2", true), greenCapture("capture R3", true),
	}
	// rows returns the epic with its captures green, its Done gates passed, checks, rulings and steps.
	rows := func(checks []store.NodeCheck, rulings []store.ReproRuling, steps ...store.StepRow) Rows {
		return Rows{
			Epic: epic, Checks: append(gatesPassed(), checks...), Evidence: captured, Rulings: rulings, Steps: steps,
		}
	}
	passed := []store.NodeCheck{passedCheck("repro R1"), passedCheck("repro R3")}
	r1Passed := []store.NodeCheck{passedCheck("repro R1"), failedCheck("repro R3")}
	silent := ruled("repro R1", "")
	unstarted := store.ReproRuling{Check: workID("repro R1"), Reason: "the judge did not start: refused"}
	secondSilent := store.ReproRuling{Check: workID("repro R1"), Judge: 1, Step: "judge-1", Reason: "overtime"}
	const allGreen = "R1 green\nR2 green\nR3 green"
	cases := []decideCase{
		{
			"a reproduction that exited 0 starts its judge",
			batchNode(testRev, composedOne(), true),
			rows(r1Passed, nil),
			reproJudgeAction(ReproJudgeStart, "R1", "repro R1", 0),
		},
		{
			"judges start in contract order",
			batchNode(testRev, composedOne(), true),
			rows(passed, nil),
			reproJudgeAction(ReproJudgeStart, "R1", "repro R1", 0),
		},
		{
			"a running judge does not hold back another's start",
			batchNode(testRev, composedOne(), true),
			rows(passed, nil, judgeStep("R1", 0, "")),
			reproJudgeAction(ReproJudgeStart, "R3", "repro R3", 0),
		},
		{
			"a running judge does not hold back another's record",
			batchNode(testRev, composedOne(), true),
			rows(passed, nil, judgeStep("R1", 0, ""), judgeStep("R3", 0, "done")),
			reproJudgeAction(ReproJudgeRecord, "R3", "repro R3", 0),
		},
		{
			"a running judge waits once no other judge needs an action",
			batchNode(testRev, composedOne(), true),
			rows(r1Passed, nil, judgeStep("R1", 0, "")),
			Action{Kind: ActNone},
		},
		{
			"an ended judge is recorded",
			batchNode(testRev, composedOne(), true),
			rows(r1Passed, nil, judgeStep("R1", 0, "done")),
			reproJudgeAction(ReproJudgeRecord, "R1", "repro R1", 0),
		},
		{
			"a first judge with no verdict starts the second",
			batchNode(testRev, composedOne(), true),
			rows(r1Passed, []store.ReproRuling{silent}, judgeStep("R1", 0, "overtime")),
			reproJudgeAction(ReproJudgeStart, "R1", "repro R1", 1),
		},
		{
			"an ended second judge is recorded",
			batchNode(testRev, composedOne(), true),
			rows(r1Passed, []store.ReproRuling{silent}, judgeStep("R1", 0, "overtime"), judgeStep("R1", 1, "done")),
			reproJudgeAction(ReproJudgeRecord, "R1", "repro R1", 1),
		},
		{
			"a second judge with no verdict settles as no verdict and adds no cause",
			batchNode(testRev, composedOne(), true),
			rows(r1Passed, []store.ReproRuling{silent, secondSilent}),
			Action{Kind: ActAdvance, Output: allGreen},
		},
		{
			"a judge that did not start settles and adds no cause",
			batchNode(testRev, composedOne(), true),
			rows(r1Passed, []store.ReproRuling{unstarted}),
			Action{Kind: ActAdvance, Output: allGreen},
		},
		{
			"a ruling that the reproduction contradicts red adds no cause",
			batchNode(testRev, composedOne(), false),
			rows(r1Passed, []store.ReproRuling{ruled("repro R1", evidence.VerdictFails)}),
			Action{Kind: ActRefs},
		},
		{
			"a ruling on another reproduction settles nothing",
			batchNode(testRev, composedOne(), true),
			rows(r1Passed, []store.ReproRuling{ruled("repro R3", evidence.VerdictHolds)}),
			reproJudgeAction(ReproJudgeStart, "R1", "repro R1", 0),
		},
		{
			"the rerun of a lost reproduction is the one judged",
			batchNode(testRev, composedOne(), true),
			rows([]store.NodeCheck{
				lostCheck("repro R1"), passedCheck("repro R1 rerun"), failedCheck("repro R3"),
			}, nil),
			reproJudgeAction(ReproJudgeStart, "R1", "repro R1 rerun", 0),
		},
		{
			"reproductions that exited non-zero, timed out or did not run need no judge",
			batchNode(testRev, composedOne(), false),
			rows([]store.NodeCheck{
				failedCheck("repro R1"),
				{ID: workID("repro R3"), State: store.NodeEnded, NotRun: "no grant"},
			}, nil),
			Action{Kind: ActRefs},
		},
		{
			"a reproduction that timed out needs no judge",
			batchNode(testRev, composedOne(), false),
			rows([]store.NodeCheck{timedOutCheck("repro R1"), failedCheck("repro R3")}, nil),
			Action{Kind: ActRefs},
		},
	}
	checkDecisions(t, Integrate{}, cases)
}

func TestReproJudgeIDIsDerivedPerJudgeAndAttempt(t *testing.T) {
	t.Parallel()
	run := batchNode(testRev, composedOne(), true).Run
	first := ReproJudgeID(run, "R1", 0)
	if first != workID("repro judge R1 0") || first == ReproJudgeID(run, "R1", 1) ||
		first == ReproJudgeID(run, "R3", 0) {
		t.Errorf("ReproJudgeID(R1, 0) = %s, want the derived id of that judge alone", first)
	}
	run.Attempt = 2
	if ReproJudgeID(run, "R1", 0) == first {
		t.Error("judge 0 at the run's next attempt has the id of the attempt before, want its own")
	}
}

// TestUnbuiltReproduction checks that no reproduction holds a plan: a missing one, one that exits 0 against a red that
// judged fails, and a rerun of one lost at a restart name nothing. Only a Requirement not green at the stored base is
// named.
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
			"reproduction that exited 0 and did not time out holds nothing",
			rows(store.NodeCheck{ID: reproID, State: store.NodeEnded, Exit: 0}),
			nil,
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
			"no reproduction holds nothing",
			rows(),
			nil,
		},
		{
			"reproduction that did not run holds nothing",
			rows(store.NodeCheck{ID: reproID, State: store.NodeEnded, NotRun: "no grant"}),
			nil,
		},
		{
			"rerun of a lost reproduction decides it",
			rows(
				store.NodeCheck{ID: reproID, State: store.NodeEnded, NotRun: store.LostAtRestart},
				store.NodeCheck{ID: rerunID, State: store.NodeEnded, Exit: 0},
			),
			nil,
		},
		{
			"not green at the stored base names only that, whatever the reproduction",
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

// TestDisagreement checks that only a judge's ruling names a disagreement: a fails ruling on a reproduction at the
// epic's base names the Requirement, the base and the judge's reason, and a holds ruling, a judge with no verdict, a
// judge that did not start and a reproduction with no ruling, whatever it exited, give nothing.
func TestDisagreement(t *testing.T) {
	t.Parallel()
	integrated := store.NodeRun{
		ID:       "int-run",
		Kind:     "integrate",
		Outcome:  outcomePassed,
		Revision: "base-1",
		Attempt:  1,
	}
	const reason = "TestSuite passes on the base: it asserts nothing the base lacks"
	const disagree = "R1: the judge ruled that its reproduction at the epic's base base-1 contradicts red: " + reason
	r1 := store.EpicRequirement{ID: "R1", Text: "the suite passes", Item: reproItem("the suite passes")}
	r2 := store.EpicRequirement{ID: "R2", Text: "lint is clean", Item: evidenceItem("lint is clean")}
	epic := store.EpicRow{
		Base: "base-1", StoredBase: "base-1", Requirements: []store.EpicRequirement{r1, r2},
	}
	exit0 := store.NodeCheck{ID: store.NodeWorkID("int-run", 1, "repro R1"), State: store.NodeEnded}
	// ruling returns the ruling of judge on exit0, under a step of its own, with verdict and reason.
	ruling := func(judge int64, verdict, reason string) store.ReproRuling {
		return store.ReproRuling{
			Check: exit0.ID, Judge: judge, Step: "judge-" + strconv.FormatInt(judge, 10), Verdict: verdict,
			Reason: reason,
		}
	}
	fails := ruling(0, evidence.VerdictFails, reason)
	// ruledRows returns the epic with runs, the reproduction exit0 and rulings.
	ruledRows := func(runs []store.NodeRun, rulings ...store.ReproRuling) Rows {
		return Rows{Epic: epic, Runs: runs, Checks: []store.NodeCheck{exit0}, Rulings: rulings}
	}
	amended := store.EvidenceAmendment{
		Seq: 1, Requirement: "R1", Item: r1.Item, State: store.AmendmentApplied, Base: "base-1", StoredBase: "base-1",
		GreenRun: "green-1", ReproCheck: "repro-1",
	}
	cases := []struct {
		name string
		rows Rows
		id   string
		want string
	}{
		{"a ruling that the reproduction contradicts red names it", ruledRows([]store.NodeRun{integrated}, fails), "R1",
			disagree},
		{
			"a ruling with no reason names the disagreement alone",
			ruledRows([]store.NodeRun{integrated}, ruling(0, evidence.VerdictFails, "")),
			"R1",
			"R1: the judge ruled that its reproduction at the epic's base base-1 contradicts red",
		},
		{
			"a second judge's ruling that it contradicts red names it",
			ruledRows([]store.NodeRun{integrated}, ruling(0, "", "overtime"), ruling(1, evidence.VerdictFails, reason)),
			"R1",
			disagree,
		},
		{
			"a ruling that the reproduction does not contradict red gives nothing",
			ruledRows([]store.NodeRun{integrated}, ruling(0, evidence.VerdictHolds, "the task only renames")),
			"R1",
			"",
		},
		{"a reproduction that exited 0 with no ruling gives nothing", ruledRows([]store.NodeRun{integrated}), "R1", ""},
		{
			"judges that gave no verdict give nothing",
			ruledRows([]store.NodeRun{integrated}, ruling(0, "", "overtime"), ruling(1, "", "engine_restarted")),
			"R1",
			"",
		},
		{
			"a judge that did not start gives nothing",
			ruledRows([]store.NodeRun{integrated}, store.ReproRuling{Check: exit0.ID, Reason: "refused"}),
			"R1",
			"",
		},
		{
			"a ruling that contradicts red beside another run's that does not names it",
			Rows{
				Epic: epic,
				Runs: []store.NodeRun{
					integrated,
					{ID: "int-run-2", Kind: "integrate", Outcome: outcomePassed, Revision: "base-1", Attempt: 1},
				},
				Checks: []store.NodeCheck{
					exit0, {ID: store.NodeWorkID("int-run-2", 1, "repro R1"), State: store.NodeEnded},
				},
				Rulings: []store.ReproRuling{fails, {
					Check: store.NodeWorkID("int-run-2", 1, "repro R1"), Step: "judge-2",
					Verdict: evidence.VerdictHolds,
				}},
			},
			"R1",
			disagree,
		},
		{
			"a ruling on the reproduction of a run that did not pass gives nothing",
			ruledRows([]store.NodeRun{{
				ID: "int-run", Kind: "integrate", Outcome: outcomeFailed, Revision: "base-1", Attempt: 1,
			}}, fails),
			"R1",
			"",
		},
		{
			"a ruling on a reproduction at another revision gives nothing",
			ruledRows([]store.NodeRun{{
				ID: "int-run", Kind: "integrate", Outcome: outcomePassed, Revision: "base-0", Attempt: 1,
			}}, fails),
			"R1",
			"",
		},
		{
			"a ruling on the amendment's reproduction at the stored base names it",
			Rows{
				Epic: store.EpicRow{
					Base: "base-1", StoredBase: "base-1", Requirements: []store.EpicRequirement{r1},
					Amendments: []store.EvidenceAmendment{amended},
				},
				Checks: []store.NodeCheck{{ID: "repro-1", State: store.NodeEnded}},
				Rulings: []store.ReproRuling{{
					Check: "repro-1", Step: "amend-judge", Verdict: evidence.VerdictFails, Reason: reason,
				}},
			},
			"R1",
			disagree,
		},
		{
			"Requirement that names a capture item gives nothing",
			Rows{Epic: epic, Runs: []store.NodeRun{integrated}, Checks: []store.NodeCheck{
				{ID: store.NodeWorkID("int-run", 1, "repro R2"), State: store.NodeEnded},
			}, Rulings: []store.ReproRuling{{
				Check: store.NodeWorkID("int-run", 1, "repro R2"), Step: "judge", Verdict: evidence.VerdictFails,
			}}},
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
