package interpret

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// stepCapStore is the store the step cap test gives the runner. Its StartNodes counts each call and writes nothing, so
// the epic's first decision, starting its plan run, is decided again on every step. A call past the step limit is an
// error, so a pass that ignored the limit fails the count rather than looping.
type stepCapStore struct {
	Store
	starts int
}

// StartNodes counts the call and writes nothing. It returns an error once the limit has been passed.
func (s *stepCapStore) StartNodes(
	_ context.Context, _ string, _ []store.NodeRun, _ *store.NodeTrigger, _ time.Time,
) error {
	s.starts++
	if s.starts > maxSteps {
		return errors.New("start past the step limit")
	}
	return nil
}

// TestEpicStallWakesNoStallWhenTheStepCapRefusesAStart checks that a pass whose step limit refuses a further step start
// gives its epic no stall: the walk stops at maxSteps and counts the epic as started, so the stall check does not read
// it. Without that, the epic has no running plan run and would be parked at once with a stall question.
func TestEpicStallWakesNoStallWhenTheStepCapRefusesAStart(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunStore(t)
	createRunEpic(t, s, planBuildPin())
	steps := &stepCapStore{Store: s}
	runner := New(Config{
		Store: steps, Planner: &fakePlanner{known: []string{runEpic}}, Builder: &fakeBuilder{}, Now: runClock,
	})
	runner.Pass(ctx)

	if hasRunningPlan(runsOf(t, s)) {
		t.Fatal("a plan run is running: the wrapper writes nothing, so the epic must have none")
	}
	if steps.starts != maxSteps {
		t.Errorf("StartNodes calls = %d, want %d: the step limit refuses the start after the last step",
			steps.starts, maxSteps)
	}
	if got := stallCausesOf(t, s); len(got) != 0 {
		t.Errorf("stall causes = %q, want none: the pass reached the step limit, so the epic is not stall-checked", got)
	}
	if _, ok, err := s.LatestStall(ctx, runEpic); err != nil || ok {
		t.Errorf("LatestStall reports a stall = %v, %v; want no stall row", ok, err)
	}
	if row := epicOf(t, s); row.Parked != "" {
		t.Errorf("epic parked with %q, want it not parked", row.Parked)
	}
}
