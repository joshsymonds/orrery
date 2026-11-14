package planner

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/storetest"
)

// openControlsStore opens a store in a temp dir, as openRunnerStore does, and returns it with the path of its file.
func openControlsStore(t *testing.T) (*store.Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "orrery.db")
	storetest.Seed(t, path)
	s, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return s, path
}

// addPendingTrigger writes a pending trigger id on runnerEpicID beside the store's own writes. The store adds a trigger
// at create and on an answer to an open question, and an open question exists only once a turn has completed, so a
// trigger that arrives while a turn runs is written directly, as TestRunnerClaimsEveryPendingTriggerInOneTurn does.
func addPendingTrigger(t *testing.T, path, id string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close %s: %v", path, err)
		}
	})
	if _, err = db.ExecContext(context.Background(), `INSERT INTO epic_triggers (epic, id, cause, state, turn, created)
		VALUES (?, ?, 'synthetic answer', 'pending', 0, 1700000400)`, runnerEpicID, id); err != nil {
		t.Fatalf("insert trigger %s: %v", id, err)
	}
}

// waitHeldSteps is a fakeSteps whose Wait reports that it was entered, then waits for release before it returns the
// step's end. A Wait whose context ends first returns that error.
type waitHeldSteps struct {
	*fakeSteps
	entered chan struct{}
	release chan struct{}
}

func (h *waitHeldSteps) Wait(
	ctx context.Context, ids []string, timeout time.Duration,
) (*store.StepRow, []store.StepRow, error) {
	h.entered <- struct{}{}
	select {
	case <-h.release:
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	}
	return h.fakeSteps.Wait(ctx, ids, timeout)
}

func TestRunnerPauseDuringTurnLetsTurnFinishAndStartsNoStep(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, path := openControlsStore(t)
	createRunnerEpic(t, s)
	steps := newFakeSteps()
	var reads atomic.Int64
	runRunner(t, runnerConfig(s, steps, agentReturning(runnerAgent, nil, &reads)))
	waitFor(t, "a planner step to start", func() bool { return len(steps.started()) == 1 })
	waitFor(t, "step-1 recorded on turn 1", func() bool { return stepOneOnTurnOne(t, s) })
	// The trigger stays pending once turn 1 ends, so an unpaused runner would claim it and start a second step.
	addPendingTrigger(t, path, "answer:D9")

	if err := s.PauseEpic(ctx, runnerEpicID); err != nil {
		t.Fatalf("PauseEpic: %v", err)
	}
	steps.end("step-1", store.StepRow{End: "done", Result: validPlan})
	waitFor(t, "turn 1 to succeed", func() bool { return epicOf(t, s).Turns[0].State == "succeeded" })

	passesAtEnd := reads.Load()
	waitFor(t, "three more passes", func() bool { return reads.Load() >= passesAtEnd+3 })
	if n := len(steps.started()); n != 1 {
		t.Errorf("steps started = %d after the paused turn ended, want 1", n)
	}
	if n := len(epicOf(t, s).Turns); n != 1 {
		t.Errorf("turns = %d after the paused turn ended, want 1", n)
	}
	triggers, err := s.TurnTriggers(ctx, runnerEpicID, 0)
	if err != nil {
		t.Fatalf("TurnTriggers: %v", err)
	}
	if len(triggers) != 1 || triggers[0].ID != "answer:D9" || triggers[0].State != "pending" {
		t.Errorf("pending triggers = %+v, want answer:D9 still pending", triggers)
	}
}

func TestRunnerResumeLetsRunnerStartStepForPendingTriggers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	if err := s.PauseEpic(ctx, runnerEpicID); err != nil {
		t.Fatalf("PauseEpic: %v", err)
	}
	steps := newFakeSteps()
	var reads atomic.Int64
	runRunner(t, runnerConfig(s, steps, agentReturning(runnerAgent, nil, &reads)))

	passesAtPause := reads.Load()
	waitFor(t, "three passes while paused", func() bool { return reads.Load() >= passesAtPause+3 })
	if n := len(steps.started()); n != 0 {
		t.Errorf("steps started while paused = %d, want 0", n)
	}

	if err := s.ResumeEpic(ctx, runnerEpicID); err != nil {
		t.Fatalf("ResumeEpic: %v", err)
	}
	waitFor(t, "a planner step to start after the resume", func() bool { return len(steps.started()) == 1 })
	waitFor(t, "step-1 recorded on turn 1", func() bool { return stepOneOnTurnOne(t, s) })
	if epic := epicOf(t, s); epic.Paused {
		t.Errorf("epic Paused = true after the resume, want false")
	}
}

func TestRunnerRetryLetsRunnerStartStepForPendingTriggersOfParkedEpic(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	if _, err := s.ClaimTurn(ctx, runnerEpicID, claimedAt()); err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	const park = "planner role: no agent"
	if err := s.FreeTurn(ctx, runnerEpicID, 1, store.TurnFree{Reason: park, Park: park}, claimedAt()); err != nil {
		t.Fatalf("FreeTurn: %v", err)
	}
	steps := newFakeSteps()
	var reads atomic.Int64
	runRunner(t, runnerConfig(s, steps, agentReturning(runnerAgent, nil, &reads)))

	passesAtPark := reads.Load()
	waitFor(t, "three passes while parked", func() bool { return reads.Load() >= passesAtPark+3 })
	if n := len(steps.started()); n != 0 {
		t.Errorf("steps started for a parked epic = %d, want 0", n)
	}
	if got := epicOf(t, s).Parked; got != park {
		t.Errorf("epic Parked = %q, want %q", got, park)
	}

	if err := s.RetryEpic(ctx, runnerEpicID); err != nil {
		t.Fatalf("RetryEpic: %v", err)
	}
	waitFor(t, "a planner step to start after the retry", func() bool { return len(steps.started()) == 1 })
	waitFor(t, "step-1 recorded on turn 2", func() bool {
		turns := epicOf(t, s).Turns
		return len(turns) == 2 && turns[1].Step == "step-1"
	})
}

func TestRunnerStopAfterStepStoredResultKeepsIt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	steps := &waitHeldSteps{fakeSteps: newFakeSteps(), entered: make(chan struct{}, 1), release: make(chan struct{})}
	var reads atomic.Int64
	runRunner(t, runnerConfig(s, steps, agentReturning(runnerAgent, nil, &reads)))
	releaseWait := sync.OnceFunc(func() { close(steps.release) })
	t.Cleanup(releaseWait)

	select {
	case <-steps.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the runner to wait on the planner step")
	}
	// The step has stored its result, and the runner holds its Wait, so StopEpic runs before the runner settles it.
	steps.end("step-1", store.StepRow{End: "done", Result: validPlan})
	step, err := s.StopEpic(ctx, runnerEpicID)
	if err != nil {
		t.Fatalf("StopEpic: %v", err)
	}
	if step != "step-1" {
		t.Fatalf("StopEpic step = %q, want step-1", step)
	}
	releaseWait()

	waitFor(t, "turn 1 to succeed", func() bool { return epicOf(t, s).Turns[0].State == "succeeded" })
	epic := epicOf(t, s)
	if !epic.Paused {
		t.Errorf("epic Paused = false, want true: the stop paused the epic")
	}
	if want := "one task"; epic.Turns[0].Outline != want {
		t.Errorf("turn 1 outline = %q, want %q", epic.Turns[0].Outline, want)
	}
	if len(epic.Tasks) != 1 || epic.Tasks[0].Title != "Build it" {
		t.Errorf("tasks = %+v, want the one task of the stored plan", epic.Tasks)
	}
}
