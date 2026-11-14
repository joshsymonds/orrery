package planner

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/agents"
	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/store"
)

// waitForStarts waits until the runner has started n planner steps.
func waitForStarts(t *testing.T, steps *fakeSteps, n int) {
	t.Helper()
	waitFor(t, fmt.Sprintf("%d planner steps to start", n), func() bool { return len(steps.started()) == n })
}

func TestRunnerFailedEndStoresReasonCappedAndFreesTrigger(t *testing.T) {
	t.Parallel()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	steps := newFakeSteps()
	var reads atomic.Int64
	runRunner(t, runnerConfig(s, steps, agentReturning(runnerAgent, nil, &reads)))
	waitForStarts(t, steps, 1)
	waitFor(t, "step-1 recorded on turn 1", func() bool { return stepOneOnTurnOne(t, s) })

	steps.end("step-1", store.StepRow{End: "checks_failed", Reason: strings.Repeat("x", 2000)})
	waitForStarts(t, steps, 2)

	turn := epicOf(t, s).Turns[0]
	if turn.State != "failed" {
		t.Errorf("turn 1 state = %q, want failed", turn.State)
	}
	if got := len(turn.Reason); got != store.TurnReasonMax {
		t.Errorf("turn 1 reason is %d bytes, want it capped at %d", got, store.TurnReasonMax)
	}
	if !strings.HasPrefix(turn.Reason, "checks_failed: xxx") {
		t.Errorf("turn 1 reason = %.40q..., want it to start with the end and reason", turn.Reason)
	}
	triggers, err := s.TurnTriggers(context.Background(), runnerEpicID, 2)
	if err != nil {
		t.Fatalf("TurnTriggers: %v", err)
	}
	if len(triggers) != 1 || triggers[0].ID != "created" {
		t.Errorf("triggers of turn 2 = %+v, want the created trigger freed and claimed again", triggers)
	}
}

func TestRunnerThreeFailingTurnsInARowParkTheEpic(t *testing.T) {
	t.Parallel()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	steps := newFakeSteps()
	var reads atomic.Int64
	runRunner(t, runnerConfig(s, steps, agentReturning(runnerAgent, nil, &reads)))

	for n := 1; n <= 3; n++ {
		waitForStarts(t, steps, n)
		steps.end(fmt.Sprintf("step-%d", n), store.StepRow{End: "checks_failed", Reason: "tasks[0] wrong"})
	}
	waitFor(t, "the epic to be parked", func() bool { return epicOf(t, s).Parked != "" })

	epic := epicOf(t, s)
	const want = "3 planner turns failed in a row: checks_failed: tasks[0] wrong"
	if epic.Parked != want {
		t.Errorf("epic Parked = %q, want %q", epic.Parked, want)
	}
	if len(epic.Turns) != 3 {
		t.Fatalf("turns = %d, want 3", len(epic.Turns))
	}
	for i, turn := range epic.Turns {
		if turn.State != "failed" {
			t.Errorf("turn %d state = %q, want failed", i+1, turn.State)
		}
	}
	if n := len(steps.started()); n != 3 {
		t.Errorf("steps started after the park = %d, want 3: a parked epic gets no fourth turn", n)
	}
}

func TestRunnerStoppedStepFreesTurnAndPausesEpic(t *testing.T) {
	t.Parallel()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	steps := newFakeSteps()
	var reads atomic.Int64
	runRunner(t, runnerConfig(s, steps, agentReturning(runnerAgent, nil, &reads)))
	waitForStarts(t, steps, 1)
	waitFor(t, "step-1 recorded on turn 1", func() bool { return stepOneOnTurnOne(t, s) })

	steps.end("step-1", store.StepRow{End: "stopped"})
	waitFor(t, "the epic to be paused", func() bool { return epicOf(t, s).Paused })

	epic := epicOf(t, s)
	turn := epic.Turns[0]
	if turn.State != "free" || turn.Reason != "stopped" {
		t.Errorf("turn 1 = state %q reason %q, want free stopped", turn.State, turn.Reason)
	}
	if n := len(steps.started()); n != 1 {
		t.Errorf("steps started = %d, want 1: a stopped step frees its turn and pauses the epic", n)
	}
}

func TestRunnerRejectedRateLimitFreesAndPausesCheckFailedStep(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	steps := newFakeSteps()
	var reads atomic.Int64
	runRunner(t, runnerConfig(s, steps, agentReturning(runnerAgent, nil, &reads)))
	waitForStarts(t, steps, 1)
	waitFor(t, "step-1 recorded on turn 1", func() bool { return stepOneOnTurnOne(t, s) })

	if err := s.ApplyEngine(ctx, []store.EngineOp{store.EngineRateLimited{
		ID: "step-1", Status: "rejected", Kind: "five_hour", ResetsAt: time.Unix(1700018000, 0),
	}}, claimedAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}
	steps.end("step-1", store.StepRow{End: "checks_failed", Reason: "tasks[0] wrong"})
	waitFor(t, "the epic to be paused", func() bool { return epicOf(t, s).Paused })

	epic := epicOf(t, s)
	turn := epic.Turns[0]
	const want = "five_hour rate limit, resets 2023-11-15T03:13:20Z"
	if turn.State != "free" || turn.Reason != want {
		t.Errorf("turn 1 = state %q reason %q, want free %q", turn.State, turn.Reason, want)
	}
	if n := len(steps.started()); n != 1 {
		t.Errorf("steps started = %d, want 1: a rejected limit frees the turn with no failed turn", n)
	}
}

func TestRunnerEngineRestartedFreesTurnAndNextPassStartsNewTurn(t *testing.T) {
	t.Parallel()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	steps := newFakeSteps()
	var reads atomic.Int64
	runRunner(t, runnerConfig(s, steps, agentReturning(runnerAgent, nil, &reads)))
	waitForStarts(t, steps, 1)
	waitFor(t, "step-1 recorded on turn 1", func() bool { return stepOneOnTurnOne(t, s) })

	steps.end("step-1", store.StepRow{End: "engine_restarted"})
	waitForStarts(t, steps, 2)
	waitFor(t, "step-2 recorded on turn 2", func() bool {
		turns := epicOf(t, s).Turns
		return len(turns) == 2 && turns[1].Step == "step-2"
	})

	epic := epicOf(t, s)
	if turn := epic.Turns[0]; turn.State != "free" || turn.Reason != "engine_restarted" {
		t.Errorf("turn 1 = state %q reason %q, want free engine_restarted", turn.State, turn.Reason)
	}
	if epic.Paused || epic.Parked != "" {
		t.Errorf("epic Paused = %v Parked = %q, want neither: a restart frees without pause or park",
			epic.Paused, epic.Parked)
	}
}

func TestRunnerStepCapRefusalFreesTurnAndRerunsNextPass(t *testing.T) {
	t.Parallel()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	steps := newFakeSteps()
	steps.startErr = fmt.Errorf("%w: 2 live steps, step cap 2", enginehost.ErrCapReached)
	var reads atomic.Int64
	runRunner(t, runnerConfig(s, steps, agentReturning(runnerAgent, nil, &reads)))

	waitFor(t, "turn 2 to be freed and claimed again", func() bool {
		turns := epicOf(t, s).Turns
		return len(turns) >= 2 && turns[1].State == "free"
	})
	epic := epicOf(t, s)
	for i := range 2 {
		if turn := epic.Turns[i]; turn.State != "free" || turn.Reason != "step cap full" {
			t.Errorf("turn %d = state %q reason %q, want free step cap full", i+1, turn.State, turn.Reason)
		}
	}
	if epic.Paused || epic.Parked != "" {
		t.Errorf("epic Paused = %v Parked = %q, want neither: a full step cap frees without pause or park",
			epic.Paused, epic.Parked)
	}
}

func TestRunnerRefusedAgentStartFreesTurnAndParksEpicWithErrorText(t *testing.T) {
	t.Parallel()
	refusals := []struct {
		name string
		err  error
	}{
		{name: "unresolved agent", err: fmt.Errorf("resolve agent of step step-1: %w", agents.ErrUnresolved)},
		{name: "bad agent", err: enginehost.ErrBadAgent},
	}
	for _, refusal := range refusals {
		t.Run(refusal.name, func(t *testing.T) {
			t.Parallel()
			s := openRunnerStore(t)
			createRunnerEpic(t, s)
			steps := newFakeSteps()
			steps.startErr = refusal.err
			var reads atomic.Int64
			runRunner(t, runnerConfig(s, steps, agentReturning(runnerAgent, nil, &reads)))

			want := "start planner step: " + refusal.err.Error()
			waitFor(t, "the epic to be parked with the refusal", func() bool { return epicOf(t, s).Parked == want })

			epic := epicOf(t, s)
			if len(epic.Turns) != 1 || epic.Turns[0].State != "free" || epic.Turns[0].Reason != want {
				t.Errorf("turns = %+v, want one free turn with reason %q", epic.Turns, want)
			}
			if n := len(steps.started()); n != 0 {
				t.Errorf("steps started = %d, want 0", n)
			}
		})
	}
}
