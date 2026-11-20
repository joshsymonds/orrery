package planner

import (
	"context"
	"fmt"
	"reflect"
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
	waitFor(
		t,
		&steps.changeNotice,
		fmt.Sprintf("%d planner steps to start", n),
		func() bool { return len(steps.started()) == n },
	)
}

func TestRunnerFailedEndStoresReasonCappedAndFreesTrigger(t *testing.T) {
	t.Parallel()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	steps := newFakeSteps()
	steps.watchStore(s)
	var reads atomic.Int64
	runner := runRunner(t, runnerConfig(s, steps, agentReturning(runnerAgent, nil, &reads)))
	waitForStarts(t, steps, 1)
	waitFor(t, &steps.changeNotice, "step-1 recorded on turn 1", func() bool { return stepOneOnTurnOne(t, s) })

	steps.end("step-1", store.StepRow{End: "checks_failed", Reason: strings.Repeat("x", 2000)})
	waitFor(t, &steps.changeNotice, "turn 1 to fail", func() bool { return epicOf(t, s).Turns[0].State == "failed" })
	runner.Pass(t.Context())
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

func TestPlannerInfoThreeFailingTurnsInARowLeaveTheEpicUnparked(t *testing.T) {
	t.Parallel()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	steps := newFakeSteps()
	steps.watchStore(s)
	var reads atomic.Int64
	runner := runRunner(t, runnerConfig(s, steps, agentReturning(runnerAgent, nil, &reads)))

	for n := 1; n <= 3; n++ {
		waitForStarts(t, steps, n)
		steps.end(fmt.Sprintf("step-%d", n), store.StepRow{End: "checks_failed", Reason: "tasks[0] wrong"})
		waitFor(
			t,
			&steps.changeNotice,
			fmt.Sprintf("turn %d to fail", n),
			func() bool { return epicOf(t, s).Turns[n-1].State == "failed" },
		)
		runner.Pass(t.Context())
	}
	waitForStarts(t, steps, 4)

	if parked := epicOf(t, s).Parked; parked != "" {
		t.Errorf("epic Parked after three failed turns = %q, want unparked", parked)
	}
	history, err := s.EpicHistory(context.Background(), runnerEpicID)
	if err != nil {
		t.Fatalf("EpicHistory: %v", err)
	}
	texts := make([]string, 0, len(history.Tells))
	for _, tell := range history.Tells {
		texts = append(texts, tell.Text)
	}
	want := []string{
		"planner turn 1 failed: checks_failed: tasks[0] wrong",
		"planner turn 2 failed: checks_failed: tasks[0] wrong",
		"planner turn 3 failed: checks_failed: tasks[0] wrong",
	}
	if !reflect.DeepEqual(texts, want) {
		t.Errorf("tells after three failed turns = %q, want %q", texts, want)
	}
}

func TestRunnerStoppedStepFreesTurnAndPausesEpic(t *testing.T) {
	t.Parallel()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	steps := newFakeSteps()
	steps.watchStore(s)
	var reads atomic.Int64
	runRunner(t, runnerConfig(s, steps, agentReturning(runnerAgent, nil, &reads)))
	waitForStarts(t, steps, 1)
	waitFor(t, &steps.changeNotice, "step-1 recorded on turn 1", func() bool { return stepOneOnTurnOne(t, s) })

	steps.end("step-1", store.StepRow{End: "stopped"})
	waitFor(t, &steps.changeNotice, "the epic to be paused", func() bool { return epicOf(t, s).Paused })

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
	steps.watchStore(s)
	var reads atomic.Int64
	runRunner(t, runnerConfig(s, steps, agentReturning(runnerAgent, nil, &reads)))
	waitForStarts(t, steps, 1)
	waitFor(t, &steps.changeNotice, "step-1 recorded on turn 1", func() bool { return stepOneOnTurnOne(t, s) })

	if err := s.ApplyEngine(ctx, []store.EngineOp{store.EngineRateLimited{
		ID: "step-1", Status: "rejected", Kind: "five_hour", ResetsAt: time.Unix(1700018000, 0),
	}}, claimedAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}
	steps.end("step-1", store.StepRow{End: "checks_failed", Reason: "tasks[0] wrong"})
	waitFor(t, &steps.changeNotice, "the epic to be paused", func() bool { return epicOf(t, s).Paused })

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
	steps.watchStore(s)
	var reads atomic.Int64
	runner := runRunner(t, runnerConfig(s, steps, agentReturning(runnerAgent, nil, &reads)))
	waitForStarts(t, steps, 1)
	waitFor(t, &steps.changeNotice, "step-1 recorded on turn 1", func() bool { return stepOneOnTurnOne(t, s) })

	steps.end("step-1", store.StepRow{End: "engine_restarted"})
	waitFor(t, &steps.changeNotice, "turn 1 to be freed", func() bool { return epicOf(t, s).Turns[0].State == "free" })
	runner.Pass(t.Context())
	waitForStarts(t, steps, 2)
	waitFor(t, &steps.changeNotice, "step-2 recorded on turn 2", func() bool {
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
	steps.watchStore(s)
	steps.startErr = fmt.Errorf("%w: 2 live steps, step cap 2", enginehost.ErrCapReached)
	var reads atomic.Int64
	runner := runRunner(t, runnerConfig(s, steps, agentReturning(runnerAgent, nil, &reads)))
	waitFor(t, &steps.changeNotice, "turn 1 to be freed", func() bool {
		turns := epicOf(t, s).Turns
		return len(turns) >= 1 && turns[0].State == "free"
	})
	runner.Pass(t.Context())
	waitFor(t, &steps.changeNotice, "turn 2 to be freed and claimed again", func() bool {
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
			steps.watchStore(s)
			steps.startErr = refusal.err
			var reads atomic.Int64
			runRunner(t, runnerConfig(s, steps, agentReturning(runnerAgent, nil, &reads)))

			want := "start planner step: " + refusal.err.Error()
			waitFor(
				t,
				&steps.changeNotice,
				"the epic to be parked with the refusal",
				func() bool { return epicOf(t, s).Parked == want },
			)

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
