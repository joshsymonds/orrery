package kinds

import (
	"fmt"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// syncConflicted is the result of a sync whose prepare found origin's commit o1 conflicting with the stored base base-1
// in a.go and b.go. conflictedLine is the line that names that conflict.
const (
	syncConflicted = `{"origin":"o1","base":"base-1","conflicts":["a.go","b.go"]}`
	conflictedLine = "merge of origin's commit o1 into the stored base base-1 conflicts in a.go, b.go"
)

// syncRoundWork returns the id of part of the work of round's commit in the sync's integrator: the checks and the
// captures that walk it run under.
func syncRoundWork(round int64, part string) string {
	return store.NodeWorkID(fmt.Sprintf("%s-integrator-%d", testRunID, round), 1, part)
}

// syncRound returns the recorded round at round of the sync at syncRunPath, at head with outcome, failure and
// explanation.
func syncRound(round int64, head, outcome, failure, explanation string) store.IntegrateRound {
	return store.IntegrateRound{
		Path: syncRunPath, Attempt: 1, Round: round, Step: workID(fmt.Sprintf("integrator %d", round)),
		Head: head, Outcome: outcome, Failure: failure, Explanation: explanation,
	}
}

// syncRoundRows returns the rows of an epic at rest, with the recorded rounds, checks, captures and steps given.
func syncRoundRows(
	rounds []store.IntegrateRound, checks []store.NodeCheck, captured []store.EvidenceRun, steps []store.StepRow,
) Rows {
	return Rows{Epic: integrateEpic(), Rounds: rounds, Checks: checks, Evidence: captured, Steps: steps}
}

// syncGatesPassed returns the two Done gates of round at its commit, both passed.
func syncGatesPassed(round int64) []store.NodeCheck {
	return []store.NodeCheck{
		{ID: syncRoundWork(round, "done 1"), State: store.NodeEnded},
		{ID: syncRoundWork(round, "done 2"), State: store.NodeEnded},
	}
}

// syncGateFailed returns the first Done gate of round at its commit, ended with exit 1.
func syncGateFailed(round int64) store.NodeCheck {
	return store.NodeCheck{ID: syncRoundWork(round, "done 1"), State: store.NodeEnded, Exit: 1}
}

// syncCapturesPassed returns the captures of R1 and R2 of round at its commit, both passed.
func syncCapturesPassed(round int64) []store.EvidenceRun {
	passed := func(part string) store.EvidenceRun {
		return store.EvidenceRun{
			ID: syncRoundWork(round, "capture "+part), Phase: evidence.PhaseGreen, State: runEnded,
			Verdict: evidence.VerdictHolds, Passed: true,
		}
	}
	return []store.EvidenceRun{passed("R1"), passed("R2")}
}

// syncLost returns the Done gate part of round at its commit, lost at a restart.
func syncLost(round int64, part string) store.NodeCheck {
	return store.NodeCheck{ID: syncRoundWork(round, part), State: store.NodeEnded, NotRun: store.LostAtRestart}
}

// TestSyncIntegratorRounds: a conflicting sync's round with no record starts, or is recorded once its step ended, and a
// paused or parked epic starts none. A round recorded passed walks its commit through the Done gates and then the
// captures, and advances to the round's commit once they pass. A gate or capture that fails starts the next round with
// its failure as the next round's Output, at the commit of the round before it.
func TestSyncIntegratorRounds(t *testing.T) {
	t.Parallel()
	node := syncNode("", syncConflicted)
	first := workID("integrator 1")
	passedOne := []store.IntegrateRound{syncRound(1, "m1", outcomePassed, "", "")}
	paused := syncRoundRows(nil, nil, nil, nil)
	paused.Epic.Paused = true
	parked := syncRoundRows(nil, nil, nil, nil)
	parked.Epic.Parked = "held"
	moved := syncRoundRows(passedOne, syncGatesPassed(1), syncCapturesPassed(1), nil)
	moved.Epic.Paused = true
	notCaptured := []store.EvidenceRun{
		{
			ID: syncRoundWork(1, "capture R1"), Phase: evidence.PhaseGreen, State: runEnded,
			NotCaptured: "the capture command is missing",
		},
		syncCapturesPassed(1)[1],
	}
	checkDecisions(t, Sync{}, []decideCase{
		{
			"a stored step is recorded", node,
			syncRoundRows(nil, nil, nil, []store.StepRow{{ID: first, End: "done"}}),
			Action{Kind: ActIntegrator, IDs: []string{first}, Items: []string{SyncRoundRecord, "1"}},
		},
		{"a running step waits", node, syncRoundRows(nil, nil, nil, []store.StepRow{{ID: first}}),
			Action{Kind: ActNone}},
		{"a paused epic starts no round", node, paused, Action{Kind: ActNone}},
		{"a parked epic starts no round", node, parked, Action{Kind: ActNone}},
		{
			"the first gate starts at the round's commit", node,
			syncRoundRows(passedOne, nil, nil, nil),
			Action{Kind: ActStart, IDs: []string{syncRoundWork(1, "done 1")}, Items: []string{"just check"},
				Revision: "m1"},
		},
		{
			"a running gate waits at the round's commit", node,
			syncRoundRows(passedOne, []store.NodeCheck{{ID: syncRoundWork(1, "done 1"), State: store.NodeRunning}},
				nil, nil),
			Action{Kind: ActNone, Revision: "m1"},
		},
		{
			"passed gates start the captures at the round's commit", node,
			syncRoundRows(passedOne, syncGatesPassed(1), nil, nil),
			Action{
				Kind: ActCapture, IDs: []string{syncRoundWork(1, "capture R1"), syncRoundWork(1, "capture R2")},
				Items: []string{"R1", "R2"}, Revision: "m1",
			},
		},
		{
			"passed checks advance to the round's commit", node,
			syncRoundRows(passedOne, syncGatesPassed(1), syncCapturesPassed(1), nil),
			Action{
				Kind: ActAdvance, Output: "merged origin's commit o1 into the stored base base-1 as m1", Revision: "m1",
			},
		},
		{
			"an advance names why the epic moved", node, moved,
			Action{
				Kind: ActAdvance, Output: "merged origin's commit o1 into the stored base base-1 as m1",
				Revision: "m1", Reason: "the epic is paused",
			},
		},
		{
			"a failed gate starts the next round with its output", node,
			syncRoundRows(passedOne, []store.NodeCheck{{
				ID: syncRoundWork(1, "done 1"), State: store.NodeEnded, Exit: 1, Stdout: "boom",
			}}, nil, nil),
			Action{
				Kind: ActIntegrator, IDs: []string{workID("integrator 2")}, Items: []string{SyncRoundStart, "2", "m1"},
				Output: "Done gate just check failed on commit m1: it exited 1.\nstdout tail:\nboom",
			},
		},
		{
			"a failed capture starts the next round with its failure", node,
			syncRoundRows(passedOne, syncGatesPassed(1), notCaptured, nil),
			Action{
				Kind: ActIntegrator, IDs: []string{workID("integrator 2")}, Items: []string{SyncRoundStart, "2", "m1"},
				Output: "The capture of R1 did not run on commit m1: the capture command is missing.",
			},
		},
	})
}

// TestSyncIntegratorRoundsLaterCommits: a round after a failed one walks its own commit, and the next round starts from
// the commit of the round before it. The sync advances to the commit of the round that passed.
func TestSyncIntegratorRoundsLaterCommits(t *testing.T) {
	t.Parallel()
	node := syncNode("", syncConflicted)
	twoRounds := []store.IntegrateRound{
		syncRound(1, "m1", outcomePassed, "", ""),
		syncRound(2, "m2", outcomePassed, "", ""),
	}
	gatesFailedThenPassed := append([]store.NodeCheck{syncGateFailed(1)}, syncGatesPassed(2)...)
	checkDecisions(t, Sync{}, []decideCase{
		{
			"a round that passes after a failed one advances to its own commit", node,
			syncRoundRows(twoRounds, gatesFailedThenPassed, syncCapturesPassed(2), nil),
			Action{
				Kind: ActAdvance, Output: "merged origin's commit o1 into the stored base base-1 as m2", Revision: "m2",
			},
		},
		{
			"the round after two rounds whose gates failed starts at the second's commit", node,
			syncRoundRows(twoRounds, []store.NodeCheck{syncGateFailed(1), syncGateFailed(2)}, nil, nil),
			Action{
				Kind: ActIntegrator, IDs: []string{workID("integrator 3")}, Items: []string{SyncRoundStart, "3", "m2"},
				Output: "Done gate just check failed on commit m2: it exited 1.",
			},
		},
	})
}

// TestSyncIntegratorRoundsEnd: a round recorded skipped ends the sync skipped and untold, and one recorded failed ends
// it failed and told, with its failure and explanation. A round that fails its checks in the last round ends the sync
// failed, told, with its last report. A park of a round's checks ends the sync skipped, told.
func TestSyncIntegratorRoundsEnd(t *testing.T) {
	t.Parallel()
	node := syncNode("", syncConflicted)
	const headerLine = "sync sync:o1@base-1: the sync of origin's commit o1 into the stored base base-1 failed\n"
	refused := conflictedLine + "\nthe merge was refused"
	lastOut := conflictedLine + "\nDone gate just check failed on commit m3: it exited 1.\n" +
		"last report: the merge still conflicts"
	checkDecisions(t, Sync{}, []decideCase{
		{
			"a skipped round ends the sync skipped, untold", node,
			syncRoundRows([]store.IntegrateRound{syncRound(1, "", outcomeSkipped, "", "the engine restarted")}, nil,
				nil, nil),
			Action{
				Kind: ActEnd, Outcome: outcomeSkipped,
				Output: conflictedLine + "\nintegrator round 1 did not finish\nthe engine restarted",
			},
		},
		{
			"a failed round ends the sync failed, told, with its explanation", node,
			syncRoundRows([]store.IntegrateRound{
				syncRound(1, "", outcomeFailed, "the merge was refused", "blocked: a.go"),
			}, nil, nil, nil),
			Action{
				Kind: ActEnd, Outcome: outcomeFailed, Tell: true,
				Output: refused + "\nblocked: a.go",
				Cause:  headerLine + refused + "\nblocked: a.go",
			},
		},
		{
			"a failed round without an explanation ends the sync failed, told", node,
			syncRoundRows([]store.IntegrateRound{
				syncRound(1, "", outcomeFailed, "the merge was refused", ""),
			}, nil, nil, nil),
			Action{
				Kind: ActEnd, Outcome: outcomeFailed, Tell: true,
				Output: refused,
				Cause:  headerLine + refused,
			},
		},
		{
			"a park of a round's gates ends the sync skipped, told", node,
			syncRoundRows(
				[]store.IntegrateRound{syncRound(1, "m1", outcomePassed, "", "")},
				[]store.NodeCheck{syncLost(1, "done 1"), syncLost(1, "done 1 rerun")}, nil, nil,
			),
			syncTold(outcomeSkipped, conflictedLine+"\nsync plan/M1: Done gate just check lost at a restart twice"),
		},
		{
			"a failed last round ends the sync failed with its last report", node,
			syncRoundRows([]store.IntegrateRound{
				syncRound(1, "m1", outcomePassed, "", ""),
				syncRound(2, "m2", outcomePassed, "", ""),
				syncRound(3, "m3", outcomePassed, "", "the merge still conflicts"),
			}, []store.NodeCheck{syncGateFailed(1), syncGateFailed(2), syncGateFailed(3)}, nil, nil),
			Action{
				Kind: ActEnd, Outcome: outcomeFailed, Tell: true,
				Output: lastOut,
				Cause:  headerLine + lastOut,
			},
		},
	})
}
