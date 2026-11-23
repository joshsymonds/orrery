package kinds

import (
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// judgeGaveNone is the reason the judge of a capture gives when it gives no verdict on the capture, and carryNote the
// note the line of R1 has when its capture is carried at the stored base base-1.
const (
	judgeGaveNone = "judge gave no verdict twice"
	carryNote     = "R1 green: the judge gave no verdict on its exit-0 capture (" + judgeGaveNone +
		"), so its verdict at the stored base base-1 carries"
)

// noVerdictRun returns an ended capture with the work id that ran and exited 0, yet whose judge gave no verdict.
func noVerdictRun(id string) store.EvidenceRun {
	return store.EvidenceRun{ID: id, Phase: evidence.PhaseGreen, State: runEnded, JudgeReason: judgeGaveNone}
}

// TestJudgeCarryNoVerdictCarries: a capture that ran and exited 0 but whose judge gave no verdict, for a Requirement
// green at the stored base, does not fail its node. Its earlier verdict carries, with a note, so the integrate advances
// and says so, and the release records its candidate.
func TestJudgeCarryNoVerdictCarries(t *testing.T) {
	t.Parallel()
	t.Run("integrate", func(t *testing.T) {
		t.Parallel()
		rows := restingRows(gatesPassed(), noVerdictRun(workID("capture R1")), greenCapture("capture R2", true))
		checkDecisions(t, Integrate{}, []decideCase{{
			"carried capture advances with the note", batchNode(testRev, composedOne, true), rows,
			Action{Kind: ActAdvance, Output: carryNote + "\nR2 green\nR3 not yet green"},
		}})
	})
	t.Run("release", func(t *testing.T) {
		t.Parallel()
		rows := integratorRows(
			[]store.ReleaseRun{integratorRound(1, "merge-1", "")},
			[]store.NodeCheck{{ID: roundWorkID(1, "done 1"), State: store.NodeEnded}},
			[]store.EvidenceRun{noVerdictRun(roundWorkID(1, "capture R1"))}, nil,
		)
		batch, green := greenBatch()
		rows.Epic.StoredBase = "base-1"
		rows.Runs = []store.NodeRun{batch}
		rows.Evidence = append(rows.Evidence, green)
		checkDecisions(t, Release{}, []decideCase{{
			"carried capture records the candidate", releaseNode(1, "", integratorResult), rows,
			Action{Kind: ActRecord, Items: []string{store.ReleasePartCandidate, "merge-1"}, Revision: "merge-1"},
		}})
	})
}

// TestJudgeCarryNoVerdictStillFails: a capture that did not run, timed out or exited non-zero is a cause as today, even
// for a Requirement green at the stored base, so the integrate fails with its capture failed.
func TestJudgeCarryNoVerdictStillFails(t *testing.T) {
	t.Parallel()
	const want = "R1 capture failed\nR1 regressed from green at the stored base base-1"
	didNotRun := noVerdictRun(workID("capture R1"))
	didNotRun.NotCaptured = "the capture command is missing"
	timedOut := noVerdictRun(workID("capture R1"))
	timedOut.TimedOut = true
	exitedNonZero := noVerdictRun(workID("capture R1"))
	exitedNonZero.Exit = 1
	node := batchNode(testRev, composedOne, true)
	checkDecisions(t, Integrate{}, []decideCase{
		{
			"did not run", node,
			restingRows(gatesPassed(), didNotRun, greenCapture("capture R2", true)),
			Action{Kind: ActEnd, Outcome: outcomeFailed, Output: want},
		},
		{
			"timed out", node,
			restingRows(gatesPassed(), timedOut, greenCapture("capture R2", true)),
			Action{Kind: ActEnd, Outcome: outcomeFailed, Output: want},
		},
		{
			"exited non-zero", node,
			restingRows(gatesPassed(), exitedNonZero, greenCapture("capture R2", true)),
			Action{Kind: ActEnd, Outcome: outcomeFailed, Output: want},
		},
	})
}

// TestJudgeCarryGreenAtCountsCarried: GreenAt counts a carried capture of a passed integrate run as green at the
// revision that run ran at, as the stored base the next integrate moves to.
func TestJudgeCarryGreenAtCountsCarried(t *testing.T) {
	t.Parallel()
	batch, green := greenBatch()
	carrying := store.NodeRun{
		ID: "carry-run", Kind: "integrate", Attempt: 1, State: store.NodeEnded, Outcome: outcomePassed,
		Revision: "cand-1",
	}
	rows := Rows{
		Epic:     integrateEpic(),
		Runs:     []store.NodeRun{batch, carrying},
		Evidence: []store.EvidenceRun{green, noVerdictRun(store.NodeWorkID("carry-run", 1, "capture R1"))},
	}
	want := map[string]bool{"R1": true}
	if got := GreenAt(rows, "cand-1"); !reflect.DeepEqual(got, want) {
		t.Errorf("GreenAt(cand-1) = %v, want %v", got, want)
	}
}

// TestLegacySyncRunStaysGreen: a sync run an earlier orreryd stored, which ended passed and moved the stored base to
// its merge, still makes its captures the Requirements' green at that base, and its reproductions count, as an
// integrate run's do. So an epic it built stays built there, with no batch after it.
func TestLegacySyncRunStaysGreen(t *testing.T) {
	t.Parallel()
	legacy := store.NodeRun{ID: testRunID, Kind: kindLegacySync, Attempt: 1, Outcome: outcomePassed, Revision: "m1"}
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
		Epic: epic, Runs: []store.NodeRun{red, legacy},
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
	exitedZero := rows
	exitedZero.Checks = []store.NodeCheck{{ID: workID("repro R1"), State: store.NodeEnded, Exit: 0}}
	if got := Disagreement(exitedZero, "R1"); got == "" {
		t.Error("Disagreement(R1) = none, want the disagreement a legacy sync's exit-0 reproduction shows")
	}
}
