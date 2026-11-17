package planner

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/store"
)

// passOnlySecondEpicID is a second epic with a pending created trigger, beside runnerEpicID.
const passOnlySecondEpicID = "orrery-1a2b3c4/planner-second"

// immediateSteps is a Steps that ends each step as checks_failed as soon as it starts. Every turn a pass claims then
// settles, so WaitTurns returns only after each start the pass made is recorded.
type immediateSteps struct {
	*fakeSteps
}

// Start starts the step on the embedded fakeSteps and ends it as checks_failed.
func (s immediateSteps) Start(ctx context.Context, start enginehost.StepStart) (string, error) {
	id, err := s.fakeSteps.Start(ctx, start)
	if err == nil {
		s.end(id, store.StepRow{End: "checks_failed", Reason: "synthetic end"})
	}
	return id, err
}

// createPassOnlyEpic stores id with the Requirements and fields of runnerEpicID, so its created trigger is pending.
func createPassOnlyEpic(t *testing.T, s *store.Store, id string) {
	t.Helper()
	if err := s.CreateEpic(context.Background(), store.EpicRow{
		ID: id, Repository: runnerRepo, Title: "Plan the second runner", Approved: "approved text",
		Intent: "plan the next tasks", Care: "serious", Approach: "one planner turn", ReleaseThen: "release it",
		Checks: "just check", Phase: "accepted", Created: claimedAt().Add(-time.Minute),
		Changed: claimedAt().Add(-time.Minute),
		Requirements: []store.EpicRequirement{
			{ID: "R1", Text: "a trigger is claimed", Evidence: "runner test", Kind: "test"},
			{ID: "R2", Text: "a pending trigger gets a turn", Evidence: "runner test", Kind: "test"},
		},
	}); err != nil {
		t.Fatalf("CreateEpic %s: %v", id, err)
	}
}

// storedEpicOf returns the stored epic id.
func storedEpicOf(t *testing.T, s *store.Store, id string) store.EpicRow {
	t.Helper()
	epic, err := s.Epic(context.Background(), id)
	if err != nil {
		t.Fatalf("Epic %s: %v", id, err)
	}
	return epic
}

func TestPassOnlyDecidesOnlyTheOpenEpic(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	createPassOnlyEpic(t, s, passOnlySecondEpicID)
	steps := newFakeSteps()
	var reads atomic.Int64
	runner := New(runnerConfig(s, immediateSteps{steps}, agentReturning(runnerAgent, nil, &reads)))

	runner.PassOnly(ctx, func(epic string) bool { return epic == passOnlySecondEpicID })
	runner.WaitTurns()

	starts := steps.started()
	if len(starts) != 1 {
		t.Fatalf("steps started = %d, want 1: a pass decides only the open epic", len(starts))
	}
	if want := turnStepID(passOnlySecondEpicID, 1); starts[0].ID != want {
		t.Errorf("step ID = %q, want the turn 1 step id of the open epic %q", starts[0].ID, want)
	}
	if starts[0].Epic != passOnlySecondEpicID {
		t.Errorf("step Epic = %q, want %q", starts[0].Epic, passOnlySecondEpicID)
	}
	if n := len(storedEpicOf(t, s, passOnlySecondEpicID).Turns); n != 1 {
		t.Errorf("turns of the open epic = %d, want 1", n)
	}
	if n := len(storedEpicOf(t, s, runnerEpicID).Turns); n != 0 {
		t.Errorf("turns of the closed epic = %d, want 0", n)
	}
	triggers, err := s.TurnTriggers(ctx, runnerEpicID, 0)
	if err != nil {
		t.Fatalf("TurnTriggers: %v", err)
	}
	if len(triggers) != 1 || triggers[0].State != "pending" {
		t.Errorf("triggers of the closed epic = %+v, want the created trigger still pending", triggers)
	}
}

func TestPassOnlyWithNoOpenEpicStartsNothing(t *testing.T) {
	t.Parallel()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	createPassOnlyEpic(t, s, passOnlySecondEpicID)
	steps := newFakeSteps()
	var reads atomic.Int64
	runner := New(runnerConfig(s, immediateSteps{steps}, agentReturning(runnerAgent, nil, &reads)))

	runner.PassOnly(context.Background(), func(string) bool { return false })
	runner.WaitTurns()

	if n := len(steps.started()); n != 0 {
		t.Errorf("steps started = %d, want 0 when no epic is open", n)
	}
	for _, id := range []string{runnerEpicID, passOnlySecondEpicID} {
		if n := len(storedEpicOf(t, s, id).Turns); n != 0 {
			t.Errorf("turns of epic %s = %d, want 0 when no epic is open", id, n)
		}
	}
}

// TestPassOnlyCarriesOutTheActionsOfAnEpicItDoesNotDecide checks that a pass that decides no epic still carries out
// every pending action. The interpreter passes the planner only for the epics whose plan run was given its turn, and
// gives an epic's plan none while its sync of origin's main is due or runs, so the epic's actions must not wait on a
// turn of it: the action fails its task, and no turn is claimed.
func TestPassOnlyCarriesOutTheActionsOfAnEpicItDoesNotDecide(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	steps := newFakeSteps()
	var reads atomic.Int64
	runner := New(runnerConfig(s, immediateSteps{steps}, agentReturning(runnerAgent, nil, &reads)))
	failTaskPlan := strings.Replace(validPlan, `"outcome":"work"}`,
		`"actions":[{"kind":"fail_task","node":"","task":1,"close":"","by":"","undo_none":[],`+
			`"reason":"the build cannot reach the store"}],"outcome":"work"}`, 1)
	turn, err := s.ClaimTurn(ctx, runnerEpicID, claimedAt())
	if err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	if err = runner.complete(ctx, runnerEpicID, turn.Seq, failTaskPlan); err != nil {
		t.Fatalf("complete turn 1: %v", err)
	}

	runner.PassOnly(ctx, func(string) bool { return false })
	runner.WaitTurns()

	epic := storedEpicOf(t, s, runnerEpicID)
	if status := epic.Tasks[0].Status; status != taskFailed {
		t.Errorf("status of task 1 = %q, want %q: the pending action is carried out", status, taskFailed)
	}
	if len(epic.Turns) != 1 || len(steps.started()) != 0 {
		t.Errorf("turns = %d and steps started = %d, want 1 and 0: an epic the pass does not decide gets no turn",
			len(epic.Turns), len(steps.started()))
	}
	pending, err := s.PendingActions(ctx)
	if err != nil {
		t.Fatalf("PendingActions: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("pending actions = %+v, want none", pending)
	}
}
