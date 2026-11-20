package planner

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// turnRetryWait is the Retry the turn-write retry test runs its runner at. Its timers are fake, so no test waits on it.
const turnRetryWait = 3 * time.Second

func TestTurnWriteRetryFlowDeadline(t *testing.T) {
	t.Parallel()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	steps := newFakeSteps()
	steps.watchStore(s)
	faulty := &faultyStore{Store: s, changes: &steps.changeNotice, write: "FreeTurn", err: errSettleWrite, failures: 1}
	timers := newHandTimers(&steps.changeNotice)
	var armedFor atomic.Int64
	var reads atomic.Int64
	cfg := runnerConfig(s, steps, agentReturning(runnerAgent, nil, &reads))
	cfg.Store = faulty
	cfg.Retry = turnRetryWait
	cfg.NewTimer = func(d time.Duration) (<-chan time.Time, func() bool) {
		armedFor.CompareAndSwap(0, int64(d))
		return timers.newTimer(d)
	}
	runRunner(t, cfg)
	waitForStarts(t, steps, 1)
	waitFor(t, &steps.changeNotice, "step-1 to be subscribed", func() bool { return steps.subscribed("step-1") })

	// The step stops, so its turn is freed: the first FreeTurn fails with a retryable error.
	steps.end("step-1", store.StepRow{End: "stopped"})
	waitFor(
		t,
		&steps.changeNotice,
		"the retry timer of the failed FreeTurn to be armed",
		func() bool { return timers.count() == 1 },
	)
	if got := time.Duration(armedFor.Load()); got != turnRetryWait {
		t.Errorf("retry timer armed for %v, want the Retry duration %v", got, turnRetryWait)
	}
	if got := faulty.calls.Load(); got != 1 {
		t.Errorf("FreeTurn calls = %d before the timer fires, want 1: the write is not tried again early", got)
	}

	timers.fire(t, 0)
	waitFor(t, &steps.changeNotice, "turn 1 to be freed by the retried FreeTurn", func() bool {
		return epicOf(t, s).Turns[0].State == "free"
	})
	if got := faulty.calls.Load(); got != 2 {
		t.Errorf("FreeTurn calls = %d, want 2: one failed write and exactly one retry when the timer fires", got)
	}
	if got := timers.count(); got != 1 {
		t.Errorf("retry timers armed = %d, want 1: the retried write lands and arms no second timer", got)
	}
}
