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

// TestJudgeCarryRegressionNamesReason: a Requirement green at the stored base whose capture is judged fails names the
// judge's reason in its regression, so the integrate and the sync fail with that line, and the release stops with it
// once its last round fails.
func TestJudgeCarryRegressionNamesReason(t *testing.T) {
	t.Parallel()
	t.Run("integrate", func(t *testing.T) {
		t.Parallel()
		rows := syncRows(gatesPassed(), judgedFailing(workID("capture R1")), greenCapture("capture R2", true))
		checkDecisions(t, Integrate{}, []decideCase{{
			"regression names the reason", batchNode(testRev, composedOne, true), rows,
			Action{Kind: ActEnd, Outcome: outcomeFailed, Output: regressedLine},
		}})
	})
	t.Run("sync", func(t *testing.T) {
		t.Parallel()
		rows := syncRows(gatesPassed(), judgedFailing(workID("capture R1")), greenCapture("capture R2", true))
		checkDecisions(t, Sync{}, []decideCase{{
			"regression names the reason", syncNode("m1", syncMerged), rows,
			Action{
				Kind: ActEnd, Outcome: outcomeFailed, Tell: true, Output: syncHead + "\n" + regressedLine,
				Cause: "sync sync:o1@base-1: the sync of origin's commit o1 into the stored base base-1 failed\n" +
					syncHead + "\nR1 was green at the stored base base-1, and its capture on commit m1 exited 0 " +
					"and was judged fails: the output shows TestX failing",
			},
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
// holds without one does, so the integrate and the sync advance with R1 green.
func TestJudgeCarryHoldsWithReasonProceeds(t *testing.T) {
	t.Parallel()
	held := greenCapture("capture R1", true)
	held.JudgeReason = "the output shows TestX passing"
	t.Run("integrate", func(t *testing.T) {
		t.Parallel()
		rows := syncRows(gatesPassed(), held, greenCapture("capture R2", true))
		checkDecisions(t, Integrate{}, []decideCase{{
			"held capture advances", batchNode(testRev, composedOne, true), rows,
			Action{Kind: ActAdvance, Output: "R1 green\nR2 green\nR3 not yet green"},
		}})
	})
	t.Run("sync", func(t *testing.T) {
		t.Parallel()
		rows := syncRows(gatesPassed(), held, greenCapture("capture R2", true))
		checkDecisions(t, Sync{}, []decideCase{{
			"held capture advances to the merge", syncNode("m1", syncMerged), rows,
			Action{Kind: ActAdvance, Output: "merged origin's commit o1 into the stored base base-1 as m1\n" +
				"R1 green\nR2 green\nR3 not yet green"},
		}})
	})
}
