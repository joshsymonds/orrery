package planner

import (
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// The tests here drive a Runner's turns through the step subscription. Each end or hold reaches the runner as the fake
// Steps offers it, and each retry runs only when the test fires its timer by hand, so no test waits on a real clock.

// TestStepEndPushPlannerSettlesWhenStepEndIsOffered covers a turn whose step's end is offered: the turn settles with the
// plan the step returned.
func TestStepEndPushPlannerSettlesWhenStepEndIsOffered(t *testing.T) {
	t.Parallel()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	steps := newFakeSteps()
	var reads atomic.Int64
	cfg := runnerConfig(s, steps, agentReturning(runnerAgent, nil, &reads))
	cfg.NewTimer = newHandTimers().newTimer
	runRunner(t, cfg)
	waitForStarts(t, steps, 1)
	waitFor(t, "step-1 recorded on turn 1", func() bool { return stepOneOnTurnOne(t, s) })

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

// TestStepEndPushPlannerStopsHeldStepOnceAtHold covers a step whose hold is offered: the runner stops it once, at the
// hold. The epic is paused only after the turn has freed, and the turn frees only after its step's await has returned,
// so no Stop follows the check.
func TestStepEndPushPlannerStopsHeldStepOnceAtHold(t *testing.T) {
	t.Parallel()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	steps := newLimitedSteps()
	var reads atomic.Int64
	cfg := limitRunnerConfig(s, steps, &reads)
	cfg.NewTimer = newHandTimers().newTimer
	runRunner(t, cfg)
	waitForStarts(t, steps.fakeSteps, 1)
	waitFor(t, "step-1 recorded on turn 1", func() bool { return stepOneOnTurnOne(t, s) })

	steps.hold()
	waitFor(t, "the epic to be paused", func() bool { return epicOf(t, s).Paused })

	if want := []string{"step-1"}; !reflect.DeepEqual(steps.stopCalls(), want) {
		t.Errorf("Stop calls = %q, want %q: a held step is stopped once, at the hold", steps.stopCalls(), want)
	}
	if turn := epicOf(t, s).Turns[0]; turn.State != "free" || turn.Reason != "stopped" {
		t.Errorf("turn 1 = state %q reason %q, want free stopped", turn.State, turn.Reason)
	}
}

// TestStepEndPushPlannerRetriesFailedStopOnlyWhenTimerFires covers a failed Stop of a held step: it is not tried again
// while the retry timer has not fired, and it is tried again when the timer fires.
func TestStepEndPushPlannerRetriesFailedStopOnlyWhenTimerFires(t *testing.T) {
	t.Parallel()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	steps := newLimitedSteps()
	steps.stopFailures = 1
	timers := newHandTimers()
	var reads atomic.Int64
	cfg := limitRunnerConfig(s, steps, &reads)
	cfg.NewTimer = timers.newTimer
	runRunner(t, cfg)
	waitForStarts(t, steps.fakeSteps, 1)
	waitFor(t, "step-1 recorded on turn 1", func() bool { return stepOneOnTurnOne(t, s) })

	// The failed Stop arms the one retry timer, and the runner then waits for that timer alone: nothing else in the await
	// stops the step, so the Stop calls recorded when the timer is armed are the ones before the timer fires.
	steps.hold()
	waitFor(t, "the retry timer of the failed stop to be armed", func() bool { return timers.count() == 1 })
	if want := []string{"step-1"}; !reflect.DeepEqual(steps.stopCalls(), want) {
		t.Fatalf("Stop calls before the timer fires = %q, want %q: a failed stop is not tried again",
			steps.stopCalls(), want)
	}

	timers.fire(t, 0)
	waitFor(t, "the epic to be paused", func() bool { return epicOf(t, s).Paused })
	if want := []string{"step-1", "step-1"}; !reflect.DeepEqual(steps.stopCalls(), want) {
		t.Errorf("Stop calls = %q, want %q: the timer's retry stops the held step", steps.stopCalls(), want)
	}
	if turn := epicOf(t, s).Turns[0]; turn.State != "free" || turn.Reason != "stopped" {
		t.Errorf("turn 1 = state %q reason %q, want free stopped", turn.State, turn.Reason)
	}
}

// TestStepEndPushPlannerRetriesFailedSubscribeWhenTimerFires covers a failed Subscribe: the runner subscribes again only
// when the retry timer fires, and the turn then settles once its step's end is offered.
func TestStepEndPushPlannerRetriesFailedSubscribeWhenTimerFires(t *testing.T) {
	t.Parallel()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	steps := &flakyWaitSteps{fakeSteps: newFakeSteps()}
	timers := newHandTimers()
	var reads atomic.Int64
	cfg := runnerConfig(s, steps, agentReturning(runnerAgent, nil, &reads))
	cfg.NewTimer = timers.newTimer
	runRunner(t, cfg)

	for n := 1; n <= waitFailures; n++ {
		waitFor(t, "the retry timer of a failed subscribe to be armed", func() bool { return timers.count() == n })
		if got := steps.subscribes.Load(); got != int64(n) {
			t.Fatalf("Subscribe calls before timer %d fires = %d, want %d: a failed subscribe waits for its timer",
				n, got, n)
		}
		timers.fire(t, n-1)
	}
	waitFor(t, "the subscribe that succeeds", func() bool { return steps.subscribes.Load() == waitFailures+1 })
	waitForStarts(t, steps.fakeSteps, 1)
	waitFor(t, "step-1 recorded on turn 1", func() bool { return stepOneOnTurnOne(t, s) })

	steps.end("step-1", store.StepRow{End: "done", Result: validPlan})
	waitFor(t, "turn 1 to succeed", func() bool { return epicOf(t, s).Turns[0].State == "succeeded" })
}
