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

// settledPlan is validPlan with no decisions and no questions. A completion that is not refused would then insert its
// task, where the decision and question keys of validPlan would reject the whole write before the task.
const settledPlan = `{"status":"done","decisions":[],` +
	`"tasks":[{"title":"Build it","deliverable":"the runner","files":["internal/planner/runner.go"],` +
	`"requirements":["R1"],"gate":"check","evidence":{"capture":["scripts/check"],"limit":60,"artifacts":[],` +
	`"supplied":[],"conclusion":"the runner runs","judge":"a judge"}}],"outline":"one task","questions":[],` +
	`"outcome":"work"}`

// settledRows is what a settled turn leaves in the store: the runner epic as stored, and the triggers of each of its
// turns, turn 0 included.
type settledRows struct {
	epic     store.EpicRow
	triggers [][]store.EpicTrigger
}

// settledRowsOf reads the rows settledRows holds for the runner epic.
func settledRowsOf(t *testing.T, s *store.Store) settledRows {
	t.Helper()
	epic := epicOf(t, s)
	rows := settledRows{epic: epic}
	for seq := range int64(len(epic.Turns)) + 1 {
		triggers, err := s.TurnTriggers(context.Background(), runnerEpicID, seq)
		if err != nil {
			t.Fatalf("TurnTriggers of turn %d: %v", seq, err)
		}
		rows.triggers = append(rows.triggers, triggers)
	}
	return rows
}

// restartedRunner returns a Runner over s whose steps are steps, as a restart of orreryd over the same store leaves
// one.
func restartedRunner(s *store.Store, steps *fakeSteps) *Runner {
	var reads atomic.Int64
	return New(recoverConfig(s, steps, agentReturning(runnerAgent, nil, &reads)))
}

// TestSettledTurnCompletedAgainChangesNoRow covers R3: a turn a runner completed, settled again by a runner rebuilt
// from the store, is refused by the store and changes no row.
func TestSettledTurnCompletedAgainChangesNoRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	claimTurnLeftRunning(t, s, "old-step-1", settledPlan, "done")
	steps := newFakeSteps()
	restartedRunner(s, steps).recoverTurns(ctx)
	if got := epicOf(t, s).Turns[0].State; got != "succeeded" {
		t.Fatalf("turn 1 state = %q, want succeeded", got)
	}
	settled := settledRowsOf(t, s)

	rebuilt := restartedRunner(s, steps)
	rebuilt.recoverTurns(ctx)
	err := rebuilt.settle(ctx, runnerEpicID, 1, "old-step-1", store.StepRow{End: "done", Result: settledPlan})
	if !errors.Is(err, store.ErrTurnNotRunning) {
		t.Errorf("settle of a completed turn = %v, want ErrTurnNotRunning", err)
	}
	if got := settledRowsOf(t, s); !reflect.DeepEqual(got, settled) {
		t.Errorf("rows after the repeat = %+v, want %+v", got, settled)
	}
	if n := len(steps.started()); n != 0 {
		t.Errorf("steps started = %d, want 0", n)
	}
}

// TestSettledTurnFailedAgainChangesNoRow covers R3: a failed turn settled again after the epic was parked by its later
// failures is refused by the store, so the park keeps the reason it was given.
func TestSettledTurnFailedAgainChangesNoRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	for _, reason := range []string{"checks_failed: first", "checks_failed: second", "checks_failed: third"} {
		turn, err := s.ClaimTurn(ctx, runnerEpicID, claimedAt())
		if err != nil {
			t.Fatalf("ClaimTurn: %v", err)
		}
		if err = s.FailTurn(ctx, runnerEpicID, turn.Seq, reason, claimedAt()); err != nil {
			t.Fatalf("FailTurn: %v", err)
		}
	}
	if epic := epicOf(t, s); len(epic.Turns) != 3 || epic.Parked == "" {
		t.Fatalf("turns = %d, parked = %q, want three failed turns and the epic parked", len(epic.Turns), epic.Parked)
	}
	settled := settledRowsOf(t, s)

	steps := newFakeSteps()
	rebuilt := restartedRunner(s, steps)
	rebuilt.recoverTurns(ctx)
	err := rebuilt.settle(ctx, runnerEpicID, 2, "old-step-2", store.StepRow{End: "checks_failed", Reason: "second"})
	if !errors.Is(err, store.ErrTurnNotRunning) {
		t.Errorf("settle of a failed turn = %v, want ErrTurnNotRunning", err)
	}
	if got := settledRowsOf(t, s); !reflect.DeepEqual(got, settled) {
		t.Errorf("rows after the repeat = %+v, want %+v", got, settled)
	}
}

// TestSettledTurnFreedAgainChangesNoRow covers R3: a turn freed by a stop, settled again after the owner resumed the
// epic, is refused by the store and does not pause the epic again.
func TestSettledTurnFreedAgainChangesNoRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	claimTurnLeftRunning(t, s, "old-step-1", "", "stopped")
	steps := newFakeSteps()
	restartedRunner(s, steps).recoverTurns(ctx)
	if !epicOf(t, s).Paused {
		t.Fatal("epic Paused after the stopped turn = false, want true")
	}
	if err := s.ResumeEpic(ctx, runnerEpicID, store.Maker{By: store.MakerOwner}, time.Unix(1700000000, 0)); err != nil {
		t.Fatalf("ResumeEpic: %v", err)
	}
	settled := settledRowsOf(t, s)

	rebuilt := restartedRunner(s, steps)
	rebuilt.recoverTurns(ctx)
	err := rebuilt.settle(ctx, runnerEpicID, 1, "old-step-1", store.StepRow{End: "stopped"})
	if !errors.Is(err, store.ErrTurnNotRunning) {
		t.Errorf("settle of a freed turn = %v, want ErrTurnNotRunning", err)
	}
	if got := settledRowsOf(t, s); !reflect.DeepEqual(got, settled) {
		t.Errorf("rows after the repeat = %+v, want %+v", got, settled)
	}
	if epicOf(t, s).Paused {
		t.Error("epic Paused after the repeat = true, want the owner's resume kept")
	}
}
