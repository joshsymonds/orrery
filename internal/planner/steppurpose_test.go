package planner

import (
	"sync/atomic"
	"testing"
)

// TestStepPurposePlannerTurnStartNamesTurnAndEpic: the planner step turn 1 of runnerEpicID starts carries a purpose that
// names the turn and the epic's slug, the part of its id after the "/".
func TestStepPurposePlannerTurnStartNamesTurnAndEpic(t *testing.T) {
	t.Parallel()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	steps := newFakeSteps()
	steps.watchStore(s)
	var reads atomic.Int64
	runRunner(t, runnerConfig(s, steps, agentReturning(runnerAgent, nil, &reads)))

	waitForStarts(t, steps, 1)
	const want = "Plan turn 1 of planner-runner."
	if got := steps.started()[0].Purpose; got != want {
		t.Errorf("planner turn step purpose = %q, want %q", got, want)
	}
}
