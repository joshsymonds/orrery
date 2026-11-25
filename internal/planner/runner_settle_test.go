package planner

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// errSettleWrite is the error a faultyStore fails a turn-ending write with.
var errSettleWrite = errors.New("synthetic store write failure")

// faultyStore is a Store whose write method fails with err on each of its first failures calls, then delegates to the
// real store. It counts every call of write in calls, and notifies changes after each count.
type faultyStore struct {
	*store.Store
	changes  *changeNotice
	write    string
	err      error
	failures int64
	calls    atomic.Int64
}

// fault counts a call of method and returns the error that call fails with, or nil when the call delegates. It notifies
// changes after the count, so a waiter for the count checks again.
func (f *faultyStore) fault(method string) error {
	if method != f.write {
		return nil
	}
	n := f.calls.Add(1)
	f.changes.notify()
	if n <= f.failures {
		return f.err
	}
	return nil
}

func (f *faultyStore) CompleteTurn(
	ctx context.Context,
	epic string,
	turn int64,
	plan store.EpicPlan,
	at time.Time,
) error {
	if err := f.fault("CompleteTurn"); err != nil {
		return err
	}
	return f.Store.CompleteTurn(ctx, epic, turn, plan, at)
}

func (f *faultyStore) FailTurn(ctx context.Context, epic string, turn int64, reason string, at time.Time) error {
	if err := f.fault("FailTurn"); err != nil {
		return err
	}
	return f.Store.FailTurn(ctx, epic, turn, reason, at)
}

func (f *faultyStore) FreeTurn(ctx context.Context, epic string, turn int64, free store.TurnFree, at time.Time) error {
	if err := f.fault("FreeTurn"); err != nil {
		return err
	}
	return f.Store.FreeTurn(ctx, epic, turn, free, at)
}

func (f *faultyStore) RateLimit(ctx context.Context, session string) (store.RateLimit, bool, error) {
	if err := f.fault("RateLimit"); err != nil {
		return store.RateLimit{}, false, err
	}
	return f.Store.RateLimit(ctx, session)
}

// faultyRunnerConfig returns the Config of a runner over s whose store is faulty, with its steps and agent.
func faultyRunnerConfig(s *store.Store, faulty *faultyStore, steps *fakeSteps, reads *atomic.Int64) Config {
	cfg := runnerConfig(s, steps, agentReturning(runnerAgent, nil, reads))
	cfg.Store = faulty
	return cfg
}

func TestRunnerRetriesCompleteTurnWriteUntilItLands(t *testing.T) {
	t.Parallel()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	steps := newFakeSteps()
	steps.watchStore(s)
	faulty := &faultyStore{
		Store:    s,
		changes:  &steps.changeNotice,
		write:    "CompleteTurn",
		err:      errSettleWrite,
		failures: 1,
	}
	var reads atomic.Int64
	runRunner(t, faultyRunnerConfig(s, faulty, steps, &reads))
	waitForStarts(t, steps, 1)
	waitFor(t, &steps.changeNotice, "step-1 recorded on turn 1", func() bool { return stepOneOnTurnOne(t, s) })

	steps.end("step-1", store.StepRow{End: "done", Result: validPlan})
	waitFor(
		t,
		&steps.changeNotice,
		"turn 1 to succeed",
		func() bool { return epicOf(t, s).Turns[0].State == "succeeded" },
	)

	if got := faulty.calls.Load(); got != 2 {
		t.Errorf("CompleteTurn calls = %d, want 2: one failed write and its retry", got)
	}
	epic := epicOf(t, s)
	if want := []string{"use the store"}; !reflect.DeepEqual(epic.Turns[0].Decisions, want) {
		t.Errorf("turn 1 decisions = %q, want %q", epic.Turns[0].Decisions, want)
	}
	wantTasks := []store.EpicTask{{
		Seq: 1, Turn: 1, Title: "Build it", Deliverable: "the runner", Files: []string{"internal/planner/runner.go"},
		Requirements: []string{"R1"}, Gate: "check", Evidence: runnerEvidenceItem(), Status: "open", Version: 1,
	}}
	if !reflect.DeepEqual(epic.Tasks, wantTasks) {
		t.Errorf("tasks = %+v, want %+v", epic.Tasks, wantTasks)
	}
	triggers, err := s.TurnTriggers(context.Background(), runnerEpicID, 1)
	if err != nil {
		t.Fatalf("TurnTriggers: %v", err)
	}
	if len(triggers) != 1 || triggers[0].State != "consumed" {
		t.Errorf("triggers of turn 1 = %+v, want one consumed trigger", triggers)
	}
	if n := len(steps.started()); n != 1 {
		t.Errorf("steps started = %d, want 1: a failed completion write does not rerun the turn", n)
	}
}

func TestRunnerRetriesFailTurnWriteAndFreesTrigger(t *testing.T) {
	t.Parallel()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	steps := newFakeSteps()
	steps.watchStore(s)
	faulty := &faultyStore{Store: s, changes: &steps.changeNotice, write: "FailTurn", err: errSettleWrite, failures: 1}
	var reads atomic.Int64
	runner := runRunner(t, faultyRunnerConfig(s, faulty, steps, &reads))
	waitForStarts(t, steps, 1)
	waitFor(t, &steps.changeNotice, "step-1 recorded on turn 1", func() bool { return stepOneOnTurnOne(t, s) })

	const reason = `tasks[0] "Build it" requirements[0]: not a Requirement of the epic`
	steps.end("step-1", store.StepRow{End: "checks_failed", Reason: reason})
	waitFor(t, &steps.changeNotice, "turn 1 to fail", func() bool { return epicOf(t, s).Turns[0].State == "failed" })
	runner.Pass(t.Context())
	waitForStarts(t, steps, 2)

	if got := faulty.calls.Load(); got != 2 {
		t.Errorf("FailTurn calls = %d, want 2: one failed write and its retry", got)
	}
	turns := epicOf(t, s).Turns
	if turns[0].State != "failed" {
		t.Errorf("turn 1 state = %q, want failed", turns[0].State)
	}
	if want := "checks_failed: " + reason; turns[0].Reason != want {
		t.Errorf("turn 1 reason = %q, want %q", turns[0].Reason, want)
	}
	triggers, err := s.TurnTriggers(context.Background(), runnerEpicID, 2)
	if err != nil {
		t.Fatalf("TurnTriggers: %v", err)
	}
	if len(triggers) != 1 || triggers[0].ID != "created" {
		t.Errorf("triggers of turn 2 = %+v, want the created trigger freed and claimed again", triggers)
	}
}

func TestRunnerStopsRetryingTurnWriteOnTurnNotRunning(t *testing.T) {
	t.Parallel()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	// Every CompleteTurn call fails with ErrTurnNotRunning, which is more calls than this test's passes reach.
	steps := newFakeSteps()
	steps.watchStore(s)
	faulty := &faultyStore{
		Store:    s,
		changes:  &steps.changeNotice,
		write:    "CompleteTurn",
		err:      store.ErrTurnNotRunning,
		failures: 100,
	}
	var reads atomic.Int64
	runner := runRunner(t, faultyRunnerConfig(s, faulty, steps, &reads))
	waitForStarts(t, steps, 1)
	waitFor(t, &steps.changeNotice, "step-1 recorded on turn 1", func() bool { return stepOneOnTurnOne(t, s) })

	steps.end("step-1", store.StepRow{End: "done", Result: validPlan})
	waitFor(t, &steps.changeNotice, "the first CompleteTurn call", func() bool { return faulty.calls.Load() == 1 })
	passRunner(t, runner, 3)

	if got := faulty.calls.Load(); got != 1 {
		t.Errorf("CompleteTurn calls = %d, want 1: ErrTurnNotRunning ends the retry", got)
	}
	if got := epicOf(t, s).Turns[0].State; got != "running" {
		t.Errorf("turn 1 state = %q, want running: the faulty store never wrote", got)
	}
}

func TestRunnerStopsRetryingTurnWriteWhenItsContextEnds(t *testing.T) {
	t.Parallel()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	steps := newFakeSteps()
	steps.watchStore(s)
	faulty := &faultyStore{
		Store:    s,
		changes:  &steps.changeNotice,
		write:    "FailTurn",
		err:      errSettleWrite,
		failures: 100,
	}
	var reads atomic.Int64
	runner := New(faultyRunnerConfig(s, faulty, steps, &reads))
	ctx, cancel := context.WithCancel(t.Context())
	runner.Pass(ctx)
	waitForStarts(t, steps, 1)
	waitFor(t, &steps.changeNotice, "step-1 recorded on turn 1", func() bool { return stepOneOnTurnOne(t, s) })

	steps.end("step-1", store.StepRow{End: "checks_failed", Reason: "tasks[0] wrong"})
	waitFor(t, &steps.changeNotice, "FailTurn to be retried", func() bool { return faulty.calls.Load() >= 2 })

	cancel()
	// A retry of the turn write that never stopped would keep WaitTurns from returning, which the test binary's timeout
	// ends.
	runner.WaitTurns()
	if got := epicOf(t, s).Turns[0].State; got != "running" {
		t.Errorf("turn 1 state = %q, want running: the retry wrote nothing", got)
	}
}

func TestRunnerRetriesRateLimitReadUntilItLands(t *testing.T) {
	t.Parallel()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	steps := newFakeSteps()
	steps.watchStore(s)
	faulty := &faultyStore{Store: s, changes: &steps.changeNotice, write: "RateLimit", err: errSettleWrite, failures: 1}
	var reads atomic.Int64
	runRunner(t, faultyRunnerConfig(s, faulty, steps, &reads))
	waitForStarts(t, steps, 1)
	waitFor(t, &steps.changeNotice, "step-1 recorded on turn 1", func() bool { return stepOneOnTurnOne(t, s) })

	steps.end("step-1", store.StepRow{End: "done", Result: validPlan})
	waitFor(
		t,
		&steps.changeNotice,
		"turn 1 to succeed",
		func() bool { return epicOf(t, s).Turns[0].State == "succeeded" },
	)

	if got := faulty.calls.Load(); got != 2 {
		t.Errorf("RateLimit calls = %d, want 2: one failed read and its retry", got)
	}
	if n := len(steps.started()); n != 1 {
		t.Errorf("steps started = %d, want 1: a failed rate-limit read does not rerun the turn", n)
	}
}
