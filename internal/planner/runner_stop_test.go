package planner

import (
	"context"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/store"
)

// holdingSteps is a fakeSteps whose Start reports that it was entered, then waits for release before it starts the
// step. A Start whose context ends first returns that error.
type holdingSteps struct {
	*fakeSteps
	entered chan struct{}
	release chan struct{}
}

func (h *holdingSteps) Start(ctx context.Context, start enginehost.StepStart) (string, error) {
	h.entered <- struct{}{}
	select {
	case <-h.release:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	return h.fakeSteps.Start(ctx, start)
}

func TestRunnerStopsStartedStepWhenTurnWasFlaggedBeforeItHadAStep(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	steps := &holdingSteps{fakeSteps: newFakeSteps(), entered: make(chan struct{}, 1), release: make(chan struct{})}
	var reads atomic.Int64
	runRunner(t, runnerConfig(s, steps, agentReturning(runnerAgent, nil, &reads)))
	releaseStart := sync.OnceFunc(func() { close(steps.release) })
	t.Cleanup(releaseStart)

	select {
	case <-steps.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the planner step to be started")
	}
	step, err := s.StopEpic(ctx, runnerEpicID)
	if err != nil {
		t.Fatalf("StopEpic: %v", err)
	}
	if step != "" {
		t.Fatalf("StopEpic step = %q, want none: the turn has no step yet", step)
	}
	releaseStart()

	waitFor(t, "turn 1 to end", func() bool { return epicOf(t, s).Turns[0].State == "free" })
	if got, want := steps.stopped(), []string{"step-1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("steps stopped = %q, want %q", got, want)
	}
	epic := epicOf(t, s)
	if got := epic.Turns[0].Reason; got != "stopped" {
		t.Errorf("turn 1 reason = %q, want stopped", got)
	}
	if !epic.Paused {
		t.Errorf("epic Paused = false, want true")
	}
	// A pending trigger belongs to turn 0; the freed turn 1 keeps none.
	triggers, err := s.TurnTriggers(ctx, runnerEpicID, 0)
	if err != nil {
		t.Fatalf("TurnTriggers: %v", err)
	}
	if len(triggers) != 1 || triggers[0].ID != "created" || triggers[0].State != "pending" {
		t.Errorf("pending triggers = %+v, want the created trigger pending again", triggers)
	}

	passesAtFree := reads.Load()
	waitFor(t, "three more passes", func() bool { return reads.Load() >= passesAtFree+3 })
	if n := len(steps.started()); n != 1 {
		t.Errorf("steps started = %d after the stop, want 1", n)
	}
}

func TestRunnerDoesNotStopStepOfTurnWithoutStopFlag(t *testing.T) {
	t.Parallel()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	steps := newFakeSteps()
	var reads atomic.Int64
	runRunner(t, runnerConfig(s, steps, agentReturning(runnerAgent, nil, &reads)))
	waitFor(t, "a planner step to start", func() bool { return len(steps.started()) == 1 })
	waitFor(t, "step-1 recorded on turn 1", func() bool { return stepOneOnTurnOne(t, s) })

	steps.end("step-1", store.StepRow{End: "done", Result: validPlan})
	waitFor(t, "turn 1 to succeed", func() bool { return epicOf(t, s).Turns[0].State == "succeeded" })
	if got := steps.stopped(); len(got) != 0 {
		t.Errorf("steps stopped = %q, want none: turn 1 was not flagged to stop", got)
	}
}
