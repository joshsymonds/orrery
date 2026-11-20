package planner

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/store"
)

// recoverSteps is the Steps of a runner restarted over a store: Start, Wait and the ends come from fakeSteps, and Status
// reads the step rows a previous orreryd left in read.
type recoverSteps struct {
	*fakeSteps
	read *store.Store
}

func (r recoverSteps) Status(ctx context.Context, id string) (store.StepRow, error) {
	return r.read.Step(ctx, id)
}

// recoverConfig returns the Config of a runner over s, whose steps are steps and whose step rows are read from s.
func recoverConfig(s *store.Store, steps *fakeSteps, agent func() (string, error)) Config {
	cfg := runnerConfig(s, steps, agent)
	cfg.Steps = recoverSteps{fakeSteps: steps, read: s}
	return cfg
}

// refuseOnceSteps is the Steps of a restarted runner whose first Start is refused with err, as an engine refuses a start,
// and whose later Starts reach the fake steps. Status and the other calls are recoverSteps'.
type refuseOnceSteps struct {
	recoverSteps
	err     error
	refused atomic.Bool
}

// Start refuses the first call with err and passes every later call to the fake steps.
func (r *refuseOnceSteps) Start(ctx context.Context, start enginehost.StepStart) (string, error) {
	if r.refused.CompareAndSwap(false, true) {
		return "", r.err
	}
	return r.recoverSteps.Start(ctx, start)
}

// claimTurnLeftRunning claims turn 1 of the runner epic as a previous orreryd left it running. An empty step leaves the
// turn with no step. Otherwise the step row is stored with result as its return and end as its end, each when not
// empty, and turn 1 records it as its step.
func claimTurnLeftRunning(t *testing.T, s *store.Store, step, result, end string) {
	t.Helper()
	ctx := context.Background()
	if _, err := s.ClaimTurn(ctx, runnerEpicID, claimedAt()); err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	if step == "" {
		return
	}
	ops := []store.EngineOp{store.StepAdd{
		ID: step, Agent: runnerAgent, Prompt: "synthetic prompt", Schema: Schema, Workspace: runnerRepo,
		ReadOnly: true, Epic: runnerEpicID,
	}}
	if result != "" {
		ops = append(ops, store.StepReturn{ID: step, Result: result})
	}
	if end != "" {
		ops = append(ops, store.StepEnd{ID: step, End: end})
	}
	if err := s.ApplyEngine(ctx, ops, claimedAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}
	if err := s.SetTurnStep(ctx, runnerEpicID, 1, step); err != nil {
		t.Fatalf("SetTurnStep: %v", err)
	}
}

func TestRecoveryStartsStepOfTurnWithNoStepAndKeepsItsTriggers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	claimTurnLeftRunning(t, s, "", "", "")
	steps := newFakeSteps()
	var reads atomic.Int64
	runner := runRunner(t, recoverConfig(s, steps, agentReturning(runnerAgent, nil, &reads)))

	waitForStarts(t, steps, 1)
	waitFor(t, "step-1 recorded on turn 1", func() bool {
		turns := epicOf(t, s).Turns
		return len(turns) == 1 && turns[0].Step == "step-1"
	})
	if got := steps.started()[0].ID; got != runnerTurnStepID {
		t.Errorf("step id = %q, want %q", got, runnerTurnStepID)
	}

	passRunner(t, runner, 3)
	if turns := epicOf(t, s).Turns; len(turns) != 1 || turns[0].State != "running" {
		t.Errorf("turns while the step runs = %+v, want one running turn: no second turn is claimed", turns)
	}
	triggers, err := s.TurnTriggers(ctx, runnerEpicID, 1)
	if err != nil {
		t.Fatalf("TurnTriggers: %v", err)
	}
	if len(triggers) != 1 || triggers[0].ID != "created" || triggers[0].State != "claimed" {
		t.Errorf("triggers of turn 1 = %+v, want the created trigger still claimed by turn 1", triggers)
	}
}

func TestRecoveryStartsStepOfTurnWithNoStepAndSettlesItByItsDoneEnd(t *testing.T) {
	t.Parallel()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	claimTurnLeftRunning(t, s, "", "", "")
	steps := newFakeSteps()
	var reads atomic.Int64
	runner := runRunner(t, recoverConfig(s, steps, agentReturning(runnerAgent, nil, &reads)))

	waitForStarts(t, steps, 1)
	waitFor(t, "step-1 recorded on turn 1", func() bool { return epicOf(t, s).Turns[0].Step == "step-1" })
	steps.end("step-1", store.StepRow{End: "done", Result: validPlan})
	waitFor(t, "turn 1 to succeed once its step ends", func() bool {
		return epicOf(t, s).Turns[0].State == "succeeded"
	})
	passRunner(t, runner, 3)

	if n := len(steps.started()); n != 1 {
		t.Errorf("steps started = %d, want 1: the turn's step starts once and no second turn is claimed", n)
	}
	epic := epicOf(t, s)
	if len(epic.Turns) != 1 {
		t.Errorf("turns = %d, want 1: the done end settles turn 1 and claims no second turn", len(epic.Turns))
	}
	wantTasks := []store.EpicTask{{
		Seq: 1, Turn: 1, Title: "Build it", Deliverable: "the runner", Files: []string{"internal/planner/runner.go"},
		Requirements: []string{"R1"}, Gate: "check", Evidence: runnerEvidenceItem(), Status: "open", Version: 1,
	}}
	if !reflect.DeepEqual(epic.Tasks, wantTasks) {
		t.Errorf("tasks = %+v, want %+v", epic.Tasks, wantTasks)
	}
}

func TestRecoveryFreesTurnWhenStepCapRefusesItsStartAndNextPassReRunsIt(t *testing.T) {
	t.Parallel()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	claimTurnLeftRunning(t, s, "", "", "")
	steps := newFakeSteps()
	refusing := &refuseOnceSteps{recoverSteps: recoverSteps{fakeSteps: steps, read: s}, err: enginehost.ErrCapReached}
	var reads atomic.Int64
	runRunner(t, runnerConfig(s, refusing, agentReturning(runnerAgent, nil, &reads)))

	waitFor(t, "step-1 recorded on turn 2 after turn 1 is freed", func() bool {
		turns := epicOf(t, s).Turns
		return len(turns) == 2 && turns[1].Step == "step-1"
	})
	epic := epicOf(t, s)
	if turn := epic.Turns[0]; turn.State != "free" || turn.Reason != "step cap full" {
		t.Errorf("turn 1 = state %q reason %q, want free step cap full", turn.State, turn.Reason)
	}
	if got, want := steps.started()[0].ID, turnStepID(runnerEpicID, 2); got != want {
		t.Errorf("step id of turn 2 = %q, want %q", got, want)
	}
}

func TestRecoveryParksEpicWhenStartRefusesItsAgent(t *testing.T) {
	t.Parallel()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	claimTurnLeftRunning(t, s, "", "", "")
	steps := newFakeSteps()
	refusing := &refuseOnceSteps{
		recoverSteps: recoverSteps{fakeSteps: steps, read: s}, err: enginehost.ErrBadAgent,
	}
	var reads atomic.Int64
	runRunner(t, runnerConfig(s, refusing, agentReturning(runnerAgent, nil, &reads)))

	// The start's error text is wrapped by startStep, as a claimed turn's is.
	want := "start planner step: " + enginehost.ErrBadAgent.Error()
	waitFor(t, "the epic to be parked with the refusal", func() bool {
		return epicOf(t, s).Parked == want
	})
	turns := epicOf(t, s).Turns
	if len(turns) != 1 || turns[0].State != "free" || turns[0].Reason != want {
		t.Errorf("turns = %+v, want one turn freed with %q", turns, want)
	}
}

func TestRecoveryParksEpicWhenPlannerRoleFails(t *testing.T) {
	t.Parallel()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	claimTurnLeftRunning(t, s, "", "", "")
	steps := newFakeSteps()
	roleErr := errors.New("planner role: no agent")
	var reads atomic.Int64
	runRunner(t, recoverConfig(s, steps, agentReturning("", roleErr, &reads)))

	waitFor(t, "the epic to be parked with the role error", func() bool {
		return epicOf(t, s).Parked == roleErr.Error()
	})
	if n := len(steps.started()); n != 0 {
		t.Errorf("steps started for a parked epic = %d, want 0", n)
	}
	turns := epicOf(t, s).Turns
	if len(turns) != 1 || turns[0].State != "free" || turns[0].Reason != roleErr.Error() {
		t.Errorf("turns = %+v, want one turn freed with the role error", turns)
	}
	triggers, err := s.TurnTriggers(context.Background(), runnerEpicID, 0)
	if err != nil {
		t.Fatalf("TurnTriggers: %v", err)
	}
	if len(triggers) != 1 || triggers[0].ID != "created" || triggers[0].State != "pending" {
		t.Errorf("pending triggers = %+v, want the created trigger pending again", triggers)
	}
}

func TestRecoveryReRunsTurnWhoseStepEndedEngineRestartedWithoutFailure(t *testing.T) {
	t.Parallel()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	claimTurnLeftRunning(t, s, "old-step-1", "", "engine_restarted")
	steps := newFakeSteps()
	var reads atomic.Int64
	runRunner(t, recoverConfig(s, steps, agentReturning(runnerAgent, nil, &reads)))

	waitForStarts(t, steps, 1)
	waitFor(t, "step-1 recorded on turn 2", func() bool {
		turns := epicOf(t, s).Turns
		return len(turns) == 2 && turns[1].Step == "step-1"
	})

	epic := epicOf(t, s)
	if turn := epic.Turns[0]; turn.State != "free" || turn.Reason != "engine_restarted" {
		t.Errorf("turn 1 = state %q reason %q, want free engine_restarted", turn.State, turn.Reason)
	}
	for _, turn := range epic.Turns {
		if turn.State == "failed" {
			t.Errorf("turn %d failed, want no failed turn: a restart frees the turn", turn.Seq)
		}
	}
	if epic.Paused || epic.Parked != "" {
		t.Errorf("epic Paused = %v Parked = %q, want neither: a restart frees without pause or park",
			epic.Paused, epic.Parked)
	}
}

func TestRecoveryCompletesTurnWhoseStepEndedDoneWithValidReturn(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	claimTurnLeftRunning(t, s, "old-step-1", validPlan, "done")
	steps := newFakeSteps()
	var reads atomic.Int64
	runner := runRunner(t, recoverConfig(s, steps, agentReturning(runnerAgent, nil, &reads)))

	waitFor(t, "turn 1 to succeed", func() bool { return epicOf(t, s).Turns[0].State == "succeeded" })
	passRunner(t, runner, 3)

	if n := len(steps.started()); n != 0 {
		t.Errorf("steps started = %d, want 0: a completed turn is not rerun", n)
	}
	epic := epicOf(t, s)
	wantTasks := []store.EpicTask{{
		Seq: 1, Turn: 1, Title: "Build it", Deliverable: "the runner", Files: []string{"internal/planner/runner.go"},
		Requirements: []string{"R1"}, Gate: "check", Evidence: runnerEvidenceItem(), Status: "open", Version: 1,
	}}
	if !reflect.DeepEqual(epic.Tasks, wantTasks) {
		t.Errorf("tasks = %+v, want %+v", epic.Tasks, wantTasks)
	}
	triggers, err := s.TurnTriggers(ctx, runnerEpicID, 1)
	if err != nil {
		t.Fatalf("TurnTriggers: %v", err)
	}
	if len(triggers) != 1 || triggers[0].State != "consumed" {
		t.Errorf("triggers of turn 1 = %+v, want one consumed trigger", triggers)
	}
}

func TestRecoveryWaitsForOpenStepAndSettlesTurnWhenItEnds(t *testing.T) {
	t.Parallel()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	claimTurnLeftRunning(t, s, "old-step-1", "", "")
	steps := newFakeSteps()
	steps.ended["old-step-1"] = make(chan store.StepRow, 1)
	var reads atomic.Int64
	runner := runRunner(t, recoverConfig(s, steps, agentReturning(runnerAgent, nil, &reads)))

	passRunner(t, runner, 3)
	if n := len(steps.started()); n != 0 {
		t.Errorf("steps started while the open step runs = %d, want 0", n)
	}
	if turns := epicOf(t, s).Turns; len(turns) != 1 || turns[0].State != "running" {
		t.Errorf("turns while the open step runs = %+v, want one running turn", turns)
	}

	steps.end("old-step-1", store.StepRow{End: "done", Result: validPlan})
	waitFor(t, "turn 1 to succeed once its step ends", func() bool {
		return epicOf(t, s).Turns[0].State == "succeeded"
	})
	if n := len(epicOf(t, s).Turns); n != 1 {
		t.Errorf("turns after the open step ended = %d, want 1", n)
	}
}
