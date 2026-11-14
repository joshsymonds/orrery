package planner

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// waitFailures is how many Waits on a planner step fail while the step is still open.
const waitFailures = 3

// flakyWaitSteps is a fakeSteps whose first waitFailures Waits return an error. It counts every Wait.
type flakyWaitSteps struct {
	*fakeSteps
	waits atomic.Int64
}

func (f *flakyWaitSteps) Wait(
	ctx context.Context, ids []string, timeout time.Duration,
) (*store.StepRow, []store.StepRow, error) {
	if f.waits.Add(1) <= waitFailures {
		return nil, nil, errors.New("synthetic wait failure")
	}
	return f.fakeSteps.Wait(ctx, ids, timeout)
}

func TestRunnerWaitErrorKeepsTurnRunningUntilStepEnds(t *testing.T) {
	t.Parallel()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	steps := &flakyWaitSteps{fakeSteps: newFakeSteps()}
	var reads atomic.Int64
	runRunner(t, Config{
		Store: s, Steps: steps, Agent: agentReturning(runnerAgent, nil, &reads), Tick: runnerTick, Now: claimedAt,
		Resolve: testResolver{},
	})
	waitFor(t, "a planner step to start", func() bool { return len(steps.started()) == 1 })
	waitFor(t, "step-1 recorded on turn 1", func() bool { return stepOneOnTurnOne(t, s) })

	// The turn must not end while its step is open, whatever the failed waits return.
	waitFor(t, "the failed waits to be consumed", func() bool {
		return steps.waits.Load() > waitFailures || epicOf(t, s).Turns[0].State != "running"
	})
	if got := epicOf(t, s).Turns[0].State; got != "running" {
		t.Errorf("turn 1 state after %d failed waits = %q, want running", waitFailures, got)
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
