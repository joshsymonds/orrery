package planner

import (
	"context"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// runnerTurnStepID is the id turn 1 of runnerEpicID starts its step under: the lowercase hex sha256 of the planner
// namespace, the epic and the turn seq, cut to 32 digits.
const runnerTurnStepID = "f4b187b037542e3e7e3e723a78064575"

// claimTurnKilledBeforeRecord claims turn 1 of the runner epic as a previous orreryd left it running when it was killed
// between starting its step and recording it: turn 1 has no step, and the step row is stored under id, with result and
// end each when not empty.
func claimTurnKilledBeforeRecord(t *testing.T, s *store.Store, id, result, end string) {
	t.Helper()
	ctx := context.Background()
	if _, err := s.ClaimTurn(ctx, runnerEpicID, claimedAt()); err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	ops := []store.EngineOp{store.StepAdd{
		ID: id, Agent: runnerAgent, Prompt: "synthetic prompt", Schema: Schema, Workspace: runnerRepo,
		ReadOnly: true, Epic: runnerEpicID,
	}}
	if result != "" {
		ops = append(ops, store.StepReturn{ID: id, Result: result})
	}
	if end != "" {
		ops = append(ops, store.StepEnd{ID: id, End: end})
	}
	if err := s.ApplyEngine(ctx, ops, claimedAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}
}

func TestRunnerStartsPlannerStepUnderIDOfEpicAndTurnSeq(t *testing.T) {
	t.Parallel()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	steps := newFakeSteps()
	var reads atomic.Int64
	runRunner(t, runnerConfig(s, steps, agentReturning(runnerAgent, nil, &reads)))

	waitFor(t, "a planner step to start", func() bool { return len(steps.started()) == 1 })
	if got := steps.started()[0].ID; got != runnerTurnStepID {
		t.Errorf("step id = %q, want %q", got, runnerTurnStepID)
	}
}

func TestRecoveryRecordsStepStoredUnderDerivedIDAndCompletesTurnByItsDoneEnd(t *testing.T) {
	t.Parallel()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	claimTurnKilledBeforeRecord(t, s, runnerTurnStepID, validPlan, "done")
	steps := newFakeSteps()
	var reads atomic.Int64
	runRunner(t, recoverConfig(s, steps, agentReturning(runnerAgent, nil, &reads)))

	waitFor(t, "turn 1 to succeed", func() bool { return epicOf(t, s).Turns[0].State == "succeeded" })
	if got := epicOf(t, s).Turns[0].Step; got != runnerTurnStepID {
		t.Errorf("turn 1 step = %q, want %q recorded by recovery", got, runnerTurnStepID)
	}
	passesAtSuccess := reads.Load()
	waitFor(t, "three more passes", func() bool { return reads.Load() >= passesAtSuccess+3 })
	if n := len(steps.started()); n != 0 {
		t.Errorf("steps started = %d, want 0: recovery settles the stored step and starts none", n)
	}
	if got := steps.stopCalls(); len(got) != 0 {
		t.Errorf("steps stopped = %q, want none: turn 1 was not flagged to stop", got)
	}
}

func TestRecoveryWaitsForOpenStepStoredUnderDerivedIDAndSettlesTurnWhenItEnds(t *testing.T) {
	t.Parallel()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	claimTurnKilledBeforeRecord(t, s, runnerTurnStepID, "", "")
	steps := newFakeSteps()
	steps.ended[runnerTurnStepID] = make(chan store.StepRow, 1)
	var reads atomic.Int64
	runRunner(t, recoverConfig(s, steps, agentReturning(runnerAgent, nil, &reads)))

	waitFor(t, "step recorded on turn 1", func() bool { return epicOf(t, s).Turns[0].Step == runnerTurnStepID })
	passesAtStart := reads.Load()
	waitFor(t, "three passes while the open step runs", func() bool { return reads.Load() >= passesAtStart+3 })
	if n := len(steps.started()); n != 0 {
		t.Errorf("steps started while the open step runs = %d, want 0", n)
	}
	if turns := epicOf(t, s).Turns; len(turns) != 1 || turns[0].State != "running" {
		t.Errorf("turns while the open step runs = %+v, want one running turn", turns)
	}
	if got := steps.stopCalls(); len(got) != 0 {
		t.Errorf("steps stopped = %q, want none: turn 1 was not flagged to stop", got)
	}

	steps.end(runnerTurnStepID, store.StepRow{End: "done", Result: validPlan})
	waitFor(t, "turn 1 to succeed once its step ends", func() bool {
		return epicOf(t, s).Turns[0].State == "succeeded"
	})
}

func TestRecoveryStopsOpenStepStoredUnderDerivedIDWhenTurnWasFlaggedBeforeRecord(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	claimTurnKilledBeforeRecord(t, s, runnerTurnStepID, "", "")
	step, err := s.StopEpic(ctx, runnerEpicID)
	if err != nil {
		t.Fatalf("StopEpic: %v", err)
	}
	if step != "" {
		t.Fatalf("StopEpic step = %q, want none: turn 1 has no recorded step", step)
	}
	steps := newFakeSteps()
	steps.ended[runnerTurnStepID] = make(chan store.StepRow, 1)
	var reads atomic.Int64
	runRunner(t, recoverConfig(s, steps, agentReturning(runnerAgent, nil, &reads)))

	waitFor(t, "turn 1 to end", func() bool { return epicOf(t, s).Turns[0].State == "free" })
	if got, want := steps.stopCalls(), []string{runnerTurnStepID}; !reflect.DeepEqual(got, want) {
		t.Errorf("steps stopped = %q, want %q", got, want)
	}
	epic := epicOf(t, s)
	if got := epic.Turns[0].Reason; got != "stopped" {
		t.Errorf("turn 1 reason = %q, want stopped", got)
	}
	if !epic.Paused {
		t.Errorf("epic Paused = false, want true")
	}
	if n := len(steps.started()); n != 0 {
		t.Errorf("steps started = %d, want 0: recovery settles the stored step and starts none", n)
	}
}

func TestRecoveryFreesTurnWhoseStoredStepHasAnotherID(t *testing.T) {
	t.Parallel()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	claimTurnKilledBeforeRecord(t, s, "old-step-1", validPlan, "done")
	steps := newFakeSteps()
	var reads atomic.Int64
	runRunner(t, recoverConfig(s, steps, agentReturning(runnerAgent, nil, &reads)))

	waitFor(t, "turn 1 to be freed", func() bool { return epicOf(t, s).Turns[0].State == "free" })
	epic := epicOf(t, s)
	if turn := epic.Turns[0]; turn.Reason != "no step" || turn.Step != "" {
		t.Errorf("turn 1 = reason %q step %q, want reason no step and no step recorded", turn.Reason, turn.Step)
	}
}
