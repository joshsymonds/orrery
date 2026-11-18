package planner

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/store"
)

// waitFailures is how many Subscribes on a planner step fail before one succeeds.
const waitFailures = 3

// flakyWaitSteps is a fakeSteps whose first waitFailures Subscribes return an error. It counts every Subscribe.
type flakyWaitSteps struct {
	*fakeSteps
	subscribes atomic.Int64
}

func (f *flakyWaitSteps) Subscribe(ctx context.Context, ids []string) (*enginehost.StepSubscription, error) {
	if f.subscribes.Add(1) <= waitFailures {
		return nil, errors.New("synthetic subscribe failure")
	}
	return f.fakeSteps.Subscribe(ctx, ids)
}

func TestRunnerWaitErrorKeepsTurnRunningUntilStepEnds(t *testing.T) {
	t.Parallel()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	steps := &flakyWaitSteps{fakeSteps: newFakeSteps()}
	timers := newHandTimers()
	var reads atomic.Int64
	runRunner(t, Config{
		Store: s, Steps: steps, Agent: agentReturning(runnerAgent, nil, &reads), Tick: runnerTick, Now: claimedAt,
		Resolve: testResolver{}, NewTimer: timers.newTimer,
	})
	// Each failed Subscribe arms a retry, which the test fires; the Subscribe after the last failure succeeds.
	for n := 1; n <= waitFailures; n++ {
		waitFor(t, "a retry timer to be armed", func() bool { return timers.count() == n })
		timers.fire(t, n-1)
	}
	waitFor(t, "a planner step to start", func() bool { return len(steps.started()) == 1 })
	waitFor(t, "step-1 recorded on turn 1", func() bool { return stepOneOnTurnOne(t, s) })

	// The turn must not end while its step is open, whatever the failed subscribes return.
	if got := epicOf(t, s).Turns[0].State; got != "running" {
		t.Errorf("turn 1 state after %d failed subscribes = %q, want running", waitFailures, got)
	}

	passesAtStart := reads.Load()
	waitFor(t, "three more passes", func() bool { return reads.Load() >= passesAtStart+3 })
	if n := len(steps.started()); n != 1 {
		t.Errorf("steps started = %d while turn 1 runs after failed waits, want 1", n)
	}

	steps.end("step-1", store.StepRow{End: "done", Result: validPlan})
	waitFor(t, "turn 1 to succeed", func() bool { return epicOf(t, s).Turns[0].State == "succeeded" })
	epic := epicOf(t, s)
	if want := "one task"; epic.Turns[0].Outline != want {
		t.Errorf("turn 1 outline = %q, want %q", epic.Turns[0].Outline, want)
	}
	if len(epic.Tasks) != 1 || epic.Tasks[0].Title != "Build it" {
		t.Errorf("tasks = %+v, want the one task of the valid plan", epic.Tasks)
	}
}
