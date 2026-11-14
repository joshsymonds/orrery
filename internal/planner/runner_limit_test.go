package planner

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// errSyntheticStop is the error a fakeSteps' Stop fails with while its stopFailures remain.
var errSyntheticStop = errors.New("synthetic stop failure")

// limitedSteps is a fakeSteps whose step's row shows it limited while limited is set. Each Wait that times out returns
// that row, and each Wait is counted in waits. A Wait times out after a runner tick rather than after waitInterval, so
// a test does not wait a minute.
type limitedSteps struct {
	*fakeSteps
	limited atomic.Bool
	waits   atomic.Int64
}

func (l *limitedSteps) Wait(
	ctx context.Context, ids []string, _ time.Duration,
) (*store.StepRow, []store.StepRow, error) {
	l.waits.Add(1)
	ended, rows, err := l.fakeSteps.Wait(ctx, ids, runnerTick)
	if ended != nil || err != nil {
		return ended, rows, err
	}
	return nil, []store.StepRow{{ID: ids[0], LoopLimited: l.limited.Load()}}, nil
}

// newLimitedSteps returns a limitedSteps whose step is not limited.
func newLimitedSteps() *limitedSteps {
	return &limitedSteps{fakeSteps: newFakeSteps()}
}

// limitRunnerConfig returns the Config of a runner over s and steps, at the runner tick and claimed time.
func limitRunnerConfig(s *store.Store, steps *limitedSteps, reads *atomic.Int64) Config {
	return Config{
		Store: s, Steps: steps, Agent: agentReturning(runnerAgent, nil, reads), Tick: runnerTick, Now: claimedAt,
	}
}

func TestRunnerStopsLimitedStepOnceAndFreesTurnAndPausesEpic(t *testing.T) {
	t.Parallel()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	steps := newLimitedSteps()
	var reads atomic.Int64
	runRunner(t, limitRunnerConfig(s, steps, &reads))
	waitForStarts(t, steps.fakeSteps, 1)
	waitFor(t, "step-1 recorded on turn 1", func() bool { return stepOneOnTurnOne(t, s) })

	steps.limited.Store(true)
	waitFor(t, "the epic to be paused", func() bool { return epicOf(t, s).Paused })

	epic := epicOf(t, s)
	if turn := epic.Turns[0]; turn.State != "free" || turn.Reason != "stopped" {
		t.Errorf("turn 1 = state %q reason %q, want free stopped", turn.State, turn.Reason)
	}
	if want := []string{"step-1"}; !reflect.DeepEqual(steps.stopCalls(), want) {
		t.Errorf("Stop calls = %q, want %q: a limited step is stopped once", steps.stopCalls(), want)
	}
	if n := len(steps.started()); n != 1 {
		t.Errorf("steps started = %d, want 1: a limited step costs no new turn", n)
	}
}

func TestRunnerLimitedStepWithRejectedRateLimitPausesWithLimitReason(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	steps := newLimitedSteps()
	var reads atomic.Int64
	runRunner(t, limitRunnerConfig(s, steps, &reads))
	waitForStarts(t, steps.fakeSteps, 1)
	waitFor(t, "step-1 recorded on turn 1", func() bool { return stepOneOnTurnOne(t, s) })

	if err := s.ApplyEngine(ctx, []store.EngineOp{store.EngineRateLimited{
		ID: "step-1", Status: "rejected", Kind: "five_hour", ResetsAt: time.Unix(1700018000, 0),
	}}, claimedAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}
	steps.limited.Store(true)
	waitFor(t, "the epic to be paused", func() bool { return epicOf(t, s).Paused })

	const want = "five_hour rate limit, resets 2023-11-15T03:13:20Z"
	if turn := epicOf(t, s).Turns[0]; turn.State != "free" || turn.Reason != want {
		t.Errorf("turn 1 = state %q reason %q, want free %q", turn.State, turn.Reason, want)
	}
	if want := []string{"step-1"}; !reflect.DeepEqual(steps.stopCalls(), want) {
		t.Errorf("Stop calls = %q, want %q", steps.stopCalls(), want)
	}
}

func TestRunnerLimitedStepWithRejectedRateLimitWithoutResetPausesWithUnknownResetReason(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	steps := newLimitedSteps()
	var reads atomic.Int64
	runRunner(t, limitRunnerConfig(s, steps, &reads))
	waitForStarts(t, steps.fakeSteps, 1)
	waitFor(t, "step-1 recorded on turn 1", func() bool { return stepOneOnTurnOne(t, s) })

	if err := s.ApplyEngine(ctx, []store.EngineOp{store.EngineRateLimited{
		ID: "step-1", Status: "rejected", Kind: "five_hour",
	}}, claimedAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}
	steps.limited.Store(true)
	waitFor(t, "the epic to be paused", func() bool { return epicOf(t, s).Paused })

	const want = "five_hour rate limit, reset time unknown"
	if turn := epicOf(t, s).Turns[0]; turn.State != "free" || turn.Reason != want {
		t.Errorf("turn 1 = state %q reason %q, want free %q", turn.State, turn.Reason, want)
	}
}

func TestRunnerNotLimitedStepIsNeverStopped(t *testing.T) {
	t.Parallel()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	steps := newLimitedSteps()
	var reads atomic.Int64
	runRunner(t, limitRunnerConfig(s, steps, &reads))
	waitForStarts(t, steps.fakeSteps, 1)
	waitFor(t, "step-1 recorded on turn 1", func() bool { return stepOneOnTurnOne(t, s) })

	waitFor(t, "three timed-out waits of the open step", func() bool { return steps.waits.Load() >= 3 })
	if got := steps.stopCalls(); len(got) != 0 {
		t.Errorf("Stop calls = %q, want none: the step's row is not limited", got)
	}
	if got := epicOf(t, s).Turns[0].State; got != "running" {
		t.Errorf("turn 1 state = %q, want running", got)
	}

	steps.end("step-1", store.StepRow{End: "done", Result: validPlan})
	waitFor(t, "turn 1 to succeed", func() bool { return epicOf(t, s).Turns[0].State == "succeeded" })
	if got := steps.stopCalls(); len(got) != 0 {
		t.Errorf("Stop calls after the step returned = %q, want none", got)
	}
}

func TestRunnerFailedStopIsTriedAgainWhileRowStaysLimited(t *testing.T) {
	t.Parallel()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	steps := newLimitedSteps()
	steps.stopFailures = 1
	var reads atomic.Int64
	runRunner(t, limitRunnerConfig(s, steps, &reads))
	waitForStarts(t, steps.fakeSteps, 1)
	waitFor(t, "step-1 recorded on turn 1", func() bool { return stepOneOnTurnOne(t, s) })

	steps.limited.Store(true)
	waitFor(t, "the epic to be paused", func() bool { return epicOf(t, s).Paused })

	if got := len(steps.stopCalls()); got != 2 {
		t.Errorf("Stop calls = %d, want 2: the first fails, and the wait goes on to stop the limited step again", got)
	}
	if turn := epicOf(t, s).Turns[0]; turn.State != "free" || turn.Reason != "stopped" {
		t.Errorf("turn 1 = state %q reason %q, want free stopped", turn.State, turn.Reason)
	}
}
