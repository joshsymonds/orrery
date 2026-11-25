package kinds

import (
	"fmt"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// conflictLeftOut is the result of a batch that composed task 1 onto the candidate cand-1 and left task 2 out for a merge
// conflict at b.go. conflictNothingComposed is the result of a batch that composed nothing and left task 1 out for one.
const (
	conflictLeftOut = `{"base":"base-1","candidate":"cand-1","composed":[{"task":1,"commit":"c1"}],` +
		`"leftOut":[{"task":2,"reason":"T2 conflicts with the candidate at b.go","conflicts":["b.go"]}]}`
	conflictNothingComposed = `{"base":"base-1","candidate":"base-1","composed":[],` +
		`"leftOut":[{"task":1,"reason":"T1 conflicts with the base at b.go","conflicts":["b.go"]}]}`
)

// conflictEpic returns integrateEpic with tasks 1 to 3, each built on its own revision, and tasks 1 and 2 claimed by the
// batch.
func conflictEpic() store.EpicRow {
	epic := integrateEpic()
	epic.Tasks = []store.EpicTask{
		{Seq: 1, Status: taskDone, Batch: batchPath, Build: store.TaskBuild{Revision: "rev-1"}},
		{Seq: 2, Status: taskDone, Batch: batchPath, Build: store.TaskBuild{Revision: "rev-2"}},
		{Seq: 3, Status: taskDone, Build: store.TaskBuild{Revision: "rev-3"}},
	}
	return epic
}

// roundWork returns the id of part of the work of the first round of task 2 of the test batch, which that round's checks
// and captures run under, named by the batch's run, the task and the round.
func roundWork(part string) string {
	return store.NodeWorkID(testRunID+"-task-2-1", 1, part)
}

// stepWork returns the id of the step of round of task seq of the test batch.
func stepWork(seq, round int64) string {
	return workID(fmt.Sprintf("task integrator T%d %d", seq, round))
}

// recordedRound returns the recorded round of task 2 of the test batch at round with verdict, head and outcome, and the
// failure it records when it failed.
func recordedRound(round int64, verdict, head, outcome, failure string) store.IntegrateRound {
	return store.IntegrateRound{
		Path: batchPath, Attempt: 1, Task: 2, Round: round, Step: stepWork(2, round), Verdict: verdict,
		Head: head, Outcome: outcome, Failure: failure,
	}
}

// roundChecksPassed returns the Done gates of the first round of task 2 at its commit, passed.
func roundChecksPassed() []store.NodeCheck {
	return []store.NodeCheck{
		{ID: roundWork("done 1"), State: store.NodeEnded},
		{ID: roundWork("done 2"), State: store.NodeEnded},
	}
}

// roundCapturesPassed returns the captures of R1 and R2 of the first round of task 2 at its commit, passed.
func roundCapturesPassed() []store.EvidenceRun {
	return []store.EvidenceRun{
		{
			ID:      roundWork("capture R1"),
			Phase:   evidence.PhaseGreen,
			State:   runEnded,
			Verdict: evidence.VerdictHolds,
			Passed:  true,
		},
		{
			ID:      roundWork("capture R2"),
			Phase:   evidence.PhaseGreen,
			State:   runEnded,
			Verdict: evidence.VerdictHolds,
			Passed:  true,
		},
	}
}

// TestConflictIntegratorStartsRoundOneAtTheCandidate: a conflicting task with no round starts its round 1 at the batch's
// candidate, with task 2's revision and no failure. A round whose step runs waits, and one whose step ended is recorded.
// A paused epic starts no round. A round after a failed one starts with that failure.
func TestConflictIntegratorStartsRoundOneAtTheCandidate(t *testing.T) {
	t.Parallel()
	started := Action{
		Kind: ActTaskIntegrator, IDs: []string{stepWork(2, 1)}, Items: []string{TaskStart, "1", "cand-1", "rev-2"},
		Tasks: []int64{2},
	}
	cases := []decideCase{
		{"no round and no step starts round 1", batchNode("cand-1", conflictLeftOut, true),
			Rows{Epic: conflictEpic()}, started},
		{"a round whose step runs waits", batchNode("cand-1", conflictLeftOut, true),
			Rows{Epic: conflictEpic(), Steps: []store.StepRow{{ID: stepWork(2, 1)}}}, Action{Kind: ActNone}},
		{
			"a round whose step ended is recorded",
			batchNode("cand-1", conflictLeftOut, true),
			Rows{Epic: conflictEpic(), Steps: []store.StepRow{{ID: stepWork(2, 1), End: "done"}}},
			Action{
				Kind:  ActTaskIntegrator,
				IDs:   []string{stepWork(2, 1)},
				Items: []string{TaskRecord, "1"},
				Tasks: []int64{2},
			},
		},
		{"a paused epic starts no round", batchNode("cand-1", conflictLeftOut, true),
			Rows{Epic: pausedConflictEpic()}, Action{Kind: ActNone}},
		{
			"a round after a failed round starts with its failure",
			batchNode("cand-1", conflictLeftOut, true),
			Rows{Epic: conflictEpic(), Rounds: []store.IntegrateRound{
				recordedRound(1, RoundMerged, "m1", outcomeFailed, "Done gate just check failed on commit m1"),
			}},
			Action{
				Kind:   ActTaskIntegrator,
				IDs:    []string{stepWork(2, 2)},
				Items:  []string{TaskStart, "2", "cand-1", "rev-2"},
				Tasks:  []int64{2},
				Output: "Done gate just check failed on commit m1",
			},
		},
	}
	checkDecisions(t, Integrate{}, cases)
}

// pausedConflictEpic returns conflictEpic paused.
func pausedConflictEpic() store.EpicRow {
	epic := conflictEpic()
	epic.Paused = true
	return epic
}

// TestConflictIntegratorRoundChecksAtItsCommit: a merged round's Done gates run at its commit under ids of the round's
// own, then its captures there, and a gate or capture that fails ends the round as a failure with its detail. Once they
// all pass, the round composes its task.
func TestConflictIntegratorRoundChecksAtItsCommit(t *testing.T) {
	t.Parallel()
	merged := []store.IntegrateRound{recordedRound(1, RoundMerged, "m1", "", "")}
	gateStarted := Action{
		Kind:     ActStart,
		IDs:      []string{roundWork("done 1")},
		Items:    []string{"just check"},
		Revision: "m1",
	}
	captures := Action{
		Kind: ActCapture, IDs: []string{roundWork("capture R1"), roundWork("capture R2")},
		Items: []string{"R1", "R2"}, Revision: "m1",
	}
	gateFailed := []store.NodeCheck{{
		ID: roundWork("done 1"), State: store.NodeEnded, Exit: 1, Stdout: "boom",
	}}
	notCaptured := []store.EvidenceRun{
		{
			ID: roundWork("capture R1"), Phase: evidence.PhaseGreen, State: runEnded,
			NotCaptured: "the capture did not start",
		},
		{
			ID: roundWork("capture R2"), Phase: evidence.PhaseGreen, State: runEnded,
			Verdict: evidence.VerdictHolds, Passed: true,
		},
	}
	cases := []decideCase{
		{"the first gate starts at the commit", batchNode("cand-1", conflictLeftOut, true),
			Rows{Epic: conflictEpic(), Rounds: merged}, gateStarted},
		{"a gate that fails fails the round with its output", batchNode("cand-1", conflictLeftOut, true),
			Rows{Epic: conflictEpic(), Rounds: merged, Checks: gateFailed},
			Action{
				Kind: ActTaskIntegrator, Items: []string{TaskFail, "1"}, Tasks: []int64{2},
				Output: "Done gate just check failed on commit m1: it exited 1.\nstdout tail:\nboom",
			}},
		{"passed gates start the captures at the commit", batchNode("cand-1", conflictLeftOut, true),
			Rows{Epic: conflictEpic(), Rounds: merged, Checks: roundChecksPassed()}, captures},
		{"a capture that did not run fails the round with its reason", batchNode("cand-1", conflictLeftOut, true),
			Rows{Epic: conflictEpic(), Rounds: merged, Checks: roundChecksPassed(), Evidence: notCaptured},
			Action{
				Kind: ActTaskIntegrator, Items: []string{TaskFail, "1"}, Tasks: []int64{2},
				Output: "The capture of R1 did not run on commit m1: the capture did not start.",
			}},
		{"a round whose checks pass composes its task at its commit", batchNode("cand-1", conflictLeftOut, true),
			Rows{Epic: conflictEpic(), Rounds: merged, Checks: roundChecksPassed(), Evidence: roundCapturesPassed()},
			Action{Kind: ActTaskIntegrator, Items: []string{TaskCompose, "1", "m1"}, Tasks: []int64{2}}},
	}
	checkDecisions(t, Integrate{}, cases)
}

// TestConflictIntegratorRoundsEndAtTheirLimit: a clash or an ended step ends the task's rounds at once, and a failed third
// round ends them too, so the task is left out and the batch goes on at its candidate, where its Done gate starts.
func TestConflictIntegratorRoundsEndAtTheirLimit(t *testing.T) {
	t.Parallel()
	gateAtCandidate := Action{Kind: ActStart, IDs: []string{workID("done 1")}, Items: []string{"just check"}}
	failedRounds := []store.IntegrateRound{
		recordedRound(1, RoundMerged, "m1", outcomeFailed, "one"),
		recordedRound(2, RoundMerged, "m2", outcomeFailed, "two"),
		recordedRound(3, RoundMerged, "m3", outcomeFailed, "three"),
	}
	cases := []decideCase{
		{"a clash ends the rounds, and the batch goes on", batchNode("cand-1", conflictLeftOut, true),
			Rows{Epic: conflictEpic(), Rounds: []store.IntegrateRound{recordedRound(1, RoundClash, "", "", "")}},
			gateAtCandidate},
		{"an ended step ends the rounds, and the batch goes on", batchNode("cand-1", conflictLeftOut, true),
			Rows{Epic: conflictEpic(), Rounds: []store.IntegrateRound{recordedRound(1, RoundEnded, "", "", "")}},
			gateAtCandidate},
		{"a failed third round leaves the task out, and no fourth round starts",
			batchNode("cand-1", conflictLeftOut, true), Rows{Epic: conflictEpic(), Rounds: failedRounds},
			gateAtCandidate},
	}
	checkDecisions(t, Integrate{}, cases)
}

// TestConflictIntegratorNothingComposedWaitsForItsRounds: a batch that composed nothing starts its conflicting task's
// round 1 before it ends failed, and once that round clashes the batch ends failed with the task's reason.
func TestConflictIntegratorNothingComposedWaitsForItsRounds(t *testing.T) {
	t.Parallel()
	cases := []decideCase{
		{
			"round 1 starts before the nothing-composed end",
			batchNode("base-1", conflictNothingComposed, true),
			Rows{Epic: conflictEpic()},
			Action{
				Kind:  ActTaskIntegrator,
				IDs:   []string{stepWork(1, 1)},
				Items: []string{TaskStart, "1", "base-1", "rev-1"},
				Tasks: []int64{1},
			},
		},
		{
			"a clash leaves nothing composed, and the batch ends failed",
			batchNode("base-1", conflictNothingComposed, true),
			Rows{Epic: conflictEpic(), Rounds: []store.IntegrateRound{
				{Path: batchPath, Attempt: 1, Task: 1, Round: 1, Step: stepWork(1, 1), Verdict: RoundClash},
			}},
			Action{Kind: ActEnd, Outcome: outcomeFailed, Output: "T1 conflicts with the base at b.go"},
		},
	}
	checkDecisions(t, Integrate{}, cases)
}

// TestConflictIntegratorNextTaskStartsAtTheComposedCandidate: once a round composed its task, the batch's result lists it
// as composed, and the next conflicting task's round starts at the commit the composed task left the candidate on.
func TestConflictIntegratorNextTaskStartsAtTheComposedCandidate(t *testing.T) {
	t.Parallel()
	composedTwo := `{"base":"base-1","candidate":"cand-1","composed":[{"task":1,"commit":"c1"},{"task":2,"commit":"m1"}],` +
		`"leftOut":[{"task":3,"reason":"T3 conflicts with the candidate at b.go","conflicts":["b.go"]}]}`
	cases := []decideCase{
		{"the next conflicting task starts round 1 at the composed commit", batchNode("m1", composedTwo, true),
			Rows{Epic: conflictEpic()},
			Action{
				Kind: ActTaskIntegrator, IDs: []string{stepWork(3, 1)}, Items: []string{TaskStart, "1", "m1", "rev-3"},
				Tasks: []int64{3},
			}},
	}
	checkDecisions(t, Integrate{}, cases)
}
