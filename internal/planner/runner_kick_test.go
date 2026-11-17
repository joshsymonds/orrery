package planner

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/store"
)

// kickedText is the owner's text the kick test sends the runner epic.
const kickedText = "Plan the sqlite store before the JSON one."

// emptyPlan is a planner return with no tasks and no questions. It completes a turn, which consumes the triggers the
// turn claimed.
const emptyPlan = `{"status":"done","decisions":[],"tasks":[],"outline":"nothing new","questions":[],"outcome":"work"}`

// kickSteps is a Steps whose steps end as done as soon as they start, so each pass's turns settle before WaitTurns
// returns. The first step ends with validPlan, and every later step with emptyPlan.
type kickSteps struct {
	*fakeSteps
}

// Start starts the step on the embedded fakeSteps and ends it as done with the return of its turn.
func (s kickSteps) Start(ctx context.Context, start enginehost.StepStart) (string, error) {
	id, err := s.fakeSteps.Start(ctx, start)
	if err != nil {
		return "", err
	}
	result := emptyPlan
	if len(s.started()) == 1 {
		result = validPlan
	}
	s.end(id, store.StepRow{End: "done", Result: result})
	return id, nil
}

func TestEpicKickIsClaimedByTheNextPlannerPassAndItsTextShownInThePrompt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	steps := newFakeSteps()
	var reads atomic.Int64
	runner := New(runnerConfig(s, kickSteps{steps}, agentReturning(runnerAgent, nil, &reads)))

	runner.Pass(ctx)
	runner.WaitTurns()
	if n := len(steps.started()); n != 1 {
		t.Fatalf("steps started by the first pass = %d, want 1", n)
	}
	if got := epicOf(t, s).Turns[0].State; got != "succeeded" {
		t.Fatalf("turn 1 state = %q, want succeeded", got)
	}

	if err := s.KickEpic(ctx, runnerEpicID, kickedText, claimedAt()); err != nil {
		t.Fatalf("KickEpic: %v", err)
	}
	runner.Pass(ctx)
	runner.WaitTurns()

	starts := steps.started()
	if len(starts) != 2 {
		t.Fatalf("steps started by the pass after the kick = %d in all, want 2", len(starts))
	}
	if prompt := starts[1].Prompt; !strings.Contains(prompt, "- owner-kick:1: owner kick: "+kickedText) {
		t.Errorf("prompt of turn 2 lacks the kick trigger with its text; prompt is:\n%s", prompt)
	}
	triggers, err := s.TurnTriggers(ctx, runnerEpicID, 2)
	if err != nil {
		t.Fatalf("TurnTriggers: %v", err)
	}
	if len(triggers) != 1 || triggers[0].ID != "owner-kick:1" || triggers[0].State != "consumed" {
		t.Errorf("triggers of turn 2 = %+v, want the kick trigger claimed and consumed by turn 2", triggers)
	}
}
