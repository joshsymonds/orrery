package planner

import (
	"sync/atomic"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// TestCallerlessStartPlannerStep runs a planner turn, and checks that the StepStart the runner hands its Steps carries
// the zero caller. The planner's own steps are in-process starts, so they have no session to end back to and start
// with no caller.
func TestCallerlessStartPlannerStep(t *testing.T) {
	t.Parallel()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	steps := newFakeSteps()
	var reads atomic.Int64
	runRunner(t, runnerConfig(s, steps, agentReturning(runnerAgent, nil, &reads)))

	waitFor(t, "a planner step to start", func() bool { return len(steps.started()) == 1 })
	if got := steps.started()[0].Caller; got != (store.EpicCaller{}) {
		t.Fatalf("planner step caller = %+v, want the zero caller of an in-process start", got)
	}
}
