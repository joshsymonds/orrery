package kinds

import (
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// regressedLine is the cause of R1 when R1 was green at the stored base base-1 and its capture exited 0 and was judged
// fails for the reason the output shows TestX failing.
const regressedLine = "R1 regressed from green at the stored base base-1: its capture exited 0 and was judged fails: " +
	"the output shows TestX failing"

// judgedFailing returns an ended capture of id that exited 0 and was judged fails, for the reason regressedLine names.
func judgedFailing(id string) store.EvidenceRun {
	return store.EvidenceRun{
		ID: id, Phase: evidence.PhaseGreen, State: runEnded, Verdict: evidence.VerdictFails,
		JudgeReason: "the output shows TestX failing",
	}
}

// greenBatch returns the integrate run that passed at the stored base base-1, and its capture of R1, which passed: R1
// is green at the stored base.
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

// restingRows returns the rows of an epic at rest at the stored base base-1, where R1 is green, with the checks and the
// captures of the run being decided.
func restingRows(checks []store.NodeCheck, captured ...store.EvidenceRun) Rows {
	batch, green := greenBatch()
	return Rows{
		Epic: integrateEpic(), Runs: []store.NodeRun{batch}, Checks: checks, Evidence: append(captured, green),
	}
}

// TestJudgeCarryRegressionNamesReason: a Requirement green at the stored base whose capture is judged fails names the
// judge's reason in its regression, so the integrate fails with that line, and the release stops with it once its last
// round fails.
func TestJudgeCarryRegressionNamesReason(t *testing.T) {
	t.Parallel()
	t.Run("integrate", func(t *testing.T) {
		t.Parallel()
		rows := restingRows(gatesPassed(), judgedFailing(workID("capture R1")), greenCapture("capture R2", true))
		checkDecisions(t, Integrate{}, []decideCase{{
			"regression names the reason", batchNode(testRev, composedOne, true), rows,
			Action{Kind: ActEnd, Outcome: outcomeFailed, Output: regressedLine},
		}})
	})
	t.Run("release", func(t *testing.T) {
		t.Parallel()
		var releases []store.ReleaseRun
		var checks []store.NodeCheck
		var runs []store.EvidenceRun
		for round := int64(1); round <= integratorRounds; round++ {
			releases = append(releases, integratorRound(round, "merge-1", ""))
			checks = append(checks, store.NodeCheck{ID: roundWorkID(round, "done 1"), State: store.NodeEnded})
			runs = append(runs, judgedFailing(roundWorkID(round, "capture R1")))
		}
		batch, green := greenBatch()
		rows := integratorRows(releases, checks, runs, nil)
		rows.Epic.StoredBase = "base-1"
		rows.Runs = []store.NodeRun{batch}
		rows.Evidence = append(rows.Evidence, green)
		checkDecisions(t, Release{}, []decideCase{{
			"last round's failure names the reason", releaseNode(1, "", integratorResult), rows,
			Action{Kind: ActStop, Output: regressedLine + " at merge-1"},
		}})
	})
}

// TestJudgeCarryHoldsWithReasonProceeds: a capture that holds and passed proceeds with its judge's reason, as one that
// holds without one does, so the integrate advances with R1 green.
func TestJudgeCarryHoldsWithReasonProceeds(t *testing.T) {
	t.Parallel()
	held := greenCapture("capture R1", true)
	held.JudgeReason = "the output shows TestX passing"
	rows := restingRows(gatesPassed(), held, greenCapture("capture R2", true))
	checkDecisions(t, Integrate{}, []decideCase{{
		"held capture advances", batchNode(testRev, composedOne, true), rows,
		Action{Kind: ActAdvance, Output: "R1 green\nR2 green\nR3 not yet green"},
	}})
}
