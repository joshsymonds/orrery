package planner

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/storetest"
)

// busyTurnStore refuses the first refusals records of a turn's step with busy, as a store Litestream holds refuses a
// write, and passes the rest through. It signals refused on each refusal.
type busyTurnStore struct {
	Store
	busy    error
	refused chan struct{}

	mu       sync.Mutex
	refusals int
}

// SetTurnStep refuses the record while refusals remain, else records it.
func (s *busyTurnStore) SetTurnStep(ctx context.Context, epic string, turn int64, step string) error {
	s.mu.Lock()
	refuse := s.refusals > 0
	if refuse {
		s.refusals--
	}
	s.mu.Unlock()
	if refuse {
		s.refused <- struct{}{}
		return s.busy
	}
	return s.Store.SetTurnStep(ctx, epic, turn, step)
}

// TestStartStepRecordsAStepTheStoreRefusesBusyOnceItTakesIt starts the step of a claimed turn whose record the store
// refuses as busy twice. Each refusal waits stepRecordRetry and tries again, and the third try records the step on the
// turn, so the turn does not wait for a restart with no step.
func TestStartStepRecordsAStepTheStoreRefusesBusyOnceItTakesIt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	turn, err := s.ClaimTurn(ctx, runnerEpicID, claimedAt())
	if err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	busy := &busyTurnStore{Store: s, busy: storetest.BusyError(t), refused: make(chan struct{}, 2), refusals: 2}
	waits := make(chan time.Duration, 2)
	tick := make(chan time.Time)
	var reads atomic.Int64
	cfg := runnerConfig(s, newFakeSteps(), agentReturning(runnerAgent, nil, &reads))
	cfg.Store = busy
	cfg.After = func(d time.Duration) <-chan time.Time {
		waits <- d
		return tick
	}
	runner := New(cfg)

	type started struct {
		id  string
		err error
	}
	done := make(chan started, 1)
	go func() {
		id, startErr := runner.startStep(ctx, runnerEpicID, turn, runnerAgent)
		done <- started{id: id, err: startErr}
	}()
	for range 2 {
		<-busy.refused
		if d := <-waits; d != stepRecordRetry {
			t.Fatalf("wait before the next record = %v, want %v", d, stepRecordRetry)
		}
		tick <- time.Time{}
	}
	got := <-done
	if got.err != nil || got.id == "" {
		t.Fatalf("startStep = %q, %v, want a started step", got.id, got.err)
	}
	running, err := s.RunningTurns(ctx)
	if err != nil {
		t.Fatalf("RunningTurns: %v", err)
	}
	if len(running) != 1 || running[0].Seq != turn.Seq || running[0].Step != got.id {
		t.Fatalf("running turns = %+v, want turn %d recording step %s", running, turn.Seq, got.id)
	}
}
