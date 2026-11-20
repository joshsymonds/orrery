package planner

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/agents"
	"github.com/joshsymonds/orrery/internal/store"
)

// nodeParkText is the node park the retry tests start their epic under: a failed workflow node, which the planner
// answers.
const nodeParkText = "workflow node verify failed: exit 1"

// retryingStore is a Store whose first ClaimTurn runs the owner's RetryEpic once: right after the claim lands, or,
// with beforeClaim, right before it. Either way the runner claims and frees a turn it decided on before the retry, as
// a park turn does whose claim or free commits after the owner's retry.
type retryingStore struct {
	*store.Store
	beforeClaim bool
	once        sync.Once
	retryErr    error
}

func (r *retryingStore) ClaimTurn(ctx context.Context, epic string, at time.Time) (store.EpicTurn, error) {
	retry := func() {
		r.once.Do(func() {
			r.retryErr = r.Store.RetryEpic(ctx, epic, store.Maker{By: store.MakerOwner}, time.Unix(1700000000, 0))
		})
	}
	if r.beforeClaim {
		retry()
	}
	turn, err := r.Store.ClaimTurn(ctx, epic, at)
	if err == nil {
		retry()
	}
	return turn, err
}

// noStepSteps is a fakeSteps that stores no step, so a recovered turn with no recorded step is restarted.
type noStepSteps struct {
	*fakeSteps
}

func (noStepSteps) Status(_ context.Context, id string) (store.StepRow, error) {
	return store.StepRow{}, fmt.Errorf("step %s: %w", id, store.ErrNoStep)
}

// nodeParkedRunnerStore opens the runner store with the runner epic parked by a failed node, a planner park that wakes
// the planner.
func nodeParkedRunnerStore(t *testing.T) *store.Store {
	t.Helper()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	if err := s.ParkEpic(
		context.Background(),
		runnerEpicID,
		store.ParkClassNode,
		nodeParkText,
		claimedAt(),
	); err != nil {
		t.Fatalf("ParkEpic: %v", err)
	}
	return s
}

// requireRetryStands fails unless the runner epic is unparked with no open park question, and its one turn ended free
// with reason after the owner's retry of that turn.
func requireRetryStands(t *testing.T, s *store.Store, reason string) {
	t.Helper()
	epic := epicOf(t, s)
	if epic.Parked != "" || epic.ParkClass != "" {
		t.Errorf("park after a retry since the claim = %q class %q, want unparked: the retry prevails", epic.Parked,
			epic.ParkClass)
	}
	for _, question := range epic.Questions {
		if question.Label == store.ParkLabel && question.State == "open" {
			t.Errorf("open park question Q%d %q, want none", question.Seq, question.Text)
		}
	}
	if len(epic.Turns) != 1 {
		t.Fatalf("turns = %d, want 1", len(epic.Turns))
	}
	if turn := epic.Turns[0]; turn.State != "free" || turn.Reason != reason {
		t.Errorf("turn 1 = state %q reason %q, want free %q", turn.State, turn.Reason, reason)
	}
}

// TestRunnerParkTurnYieldsToARetrySinceItsClaim checks that each runner path that frees a claimed turn with a park
// leaves the epic unparked when the owner retried it after the claim: a pass that decides a park on a role error, a
// start refused for an agent that does not resolve, and a restart that finds the role error. A pass's park also
// yields to a retry that lands between its decision and its claim.
func TestRunnerParkTurnYieldsToARetrySinceItsClaim(t *testing.T) {
	t.Parallel()
	roleErr := errors.New("no role file /home/user/.config/orrery/roles.toml")
	unresolved := fmt.Errorf("resolve agent %q: %w", runnerAgent, agents.ErrUnresolved)

	for _, beforeClaim := range []bool{false, true} {
		t.Run(fmt.Sprintf("pass decides a park, retry before the claim %t", beforeClaim), func(t *testing.T) {
			t.Parallel()
			s := nodeParkedRunnerStore(t)
			retrying := &retryingStore{Store: s, beforeClaim: beforeClaim}
			var reads atomic.Int64
			cfg := runnerConfig(s, newFakeSteps(), agentReturning("", roleErr, &reads))
			cfg.Store = retrying
			runner := New(cfg)
			runner.Pass(context.Background())
			runner.WaitTurns()
			if retrying.retryErr != nil {
				t.Fatalf("RetryEpic: %v", retrying.retryErr)
			}
			requireRetryStands(t, s, roleErr.Error())
		})
	}

	t.Run("start refused", func(t *testing.T) {
		t.Parallel()
		s := nodeParkedRunnerStore(t)
		retrying := &retryingStore{Store: s}
		steps := newFakeSteps()
		steps.startErr = unresolved
		var reads atomic.Int64
		cfg := runnerConfig(s, steps, agentReturning(runnerAgent, nil, &reads))
		cfg.Store = retrying
		runner := New(cfg)
		runner.Pass(context.Background())
		runner.WaitTurns()
		if retrying.retryErr != nil {
			t.Fatalf("RetryEpic: %v", retrying.retryErr)
		}
		requireRetryStands(t, s, "start planner step: "+unresolved.Error())
	})

	t.Run("restart finds the role error", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		s := nodeParkedRunnerStore(t)
		if _, err := s.ClaimTurn(ctx, runnerEpicID, claimedAt()); err != nil {
			t.Fatalf("ClaimTurn: %v", err)
		}
		if err := s.RetryEpic(
			ctx,
			runnerEpicID,
			store.Maker{By: store.MakerOwner},
			time.Unix(1700000000, 0),
		); err != nil {
			t.Fatalf("RetryEpic: %v", err)
		}
		var reads atomic.Int64
		runner := New(runnerConfig(s, noStepSteps{newFakeSteps()}, agentReturning("", roleErr, &reads)))
		runner.Recover(ctx)
		runner.WaitTurns()
		requireRetryStands(t, s, roleErr.Error())
	})
}
