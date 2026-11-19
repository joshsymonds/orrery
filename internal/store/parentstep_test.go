package store

import (
	"context"
	"reflect"
	"testing"
	"time"
)

// startStepAt starts step id under session at at, with no epic.
func startStepAt(t *testing.T, s *Store, id, session string, at time.Time) {
	t.Helper()
	add := StepAdd{ID: id, Agent: "implementer", Prompt: "do it", Caller: EpicCaller{Session: session}}
	if err := s.ApplyEngine(context.Background(), []EngineOp{add}, at); err != nil {
		t.Fatalf("ApplyEngine add step %s: %v", id, err)
	}
}

// TestParentStepStoreRunningCallerSteps lists the open steps of one caller in start order, and leaves out the ended
// step and the step of another caller. step-a starts after step-b, so the order is by start and not by id.
func TestParentStepStoreRunningCallerSteps(t *testing.T) {
	t.Parallel()
	s, _ := openPendingStore(t)
	addPendingEngines(t, s, "engine-1", "engine-2")
	at := pendingAt()
	startStepAt(t, s, "step-a", "engine-1", at.Add(20*time.Second))
	startStepAt(t, s, "step-b", "engine-1", at.Add(10*time.Second))
	startStepAt(t, s, "step-c", "engine-1", at.Add(5*time.Second))
	endPendingStep(t, s, "step-c", "C", at.Add(15*time.Second))
	startStepAt(t, s, "step-d", "engine-2", at.Add(10*time.Second))

	got, err := s.RunningCallerSteps(context.Background(), "engine-1")
	if err != nil {
		t.Fatalf("RunningCallerSteps: %v", err)
	}
	ids := make([]string, 0, len(got))
	for _, step := range got {
		ids = append(ids, step.ID)
	}
	if want := []string{"step-b", "step-a"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("RunningCallerSteps(engine-1) = %v, want %v", ids, want)
	}
}
