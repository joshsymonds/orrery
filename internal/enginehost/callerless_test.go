package enginehost

import (
	"encoding/json"
	"testing"
)

// TestCallerlessStartInProcess starts a step in process over the real Steps the way orreryd's planner starts its turns:
// it names an id, an epic and a return check, is read-only, and carries no caller. The zero caller is an in-process
// start, so the step starts, and its stored row names no caller.
func TestCallerlessStartInProcess(t *testing.T) {
	t.Parallel()
	f := newStepStartFixture(t)
	start := StepStart{
		ID:          "f4b187b037542e3e7e3e723a78064575",
		Agent:       "planner-agent",
		Prompt:      "plan the epic",
		Schema:      stepLoopSchema,
		Workspace:   stepGitTestRepo(t),
		ReadOnly:    true,
		Epic:        "orrery-1a2b3c4/planner-runner",
		ReturnCheck: func(json.RawMessage) []string { return nil },
	}
	id, err := f.steps.Start(t.Context(), start)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if id != start.ID {
		t.Fatalf("Start id = %q, want %q", id, start.ID)
	}
	if n := f.sessions.startCalls(); n != 1 {
		t.Fatalf("step sessions started = %d, want 1", n)
	}
	row, err := f.store.Step(t.Context(), id)
	if err != nil {
		t.Fatalf("Step: %v", err)
	}
	if row.Caller != "" {
		t.Fatalf("stored caller = %q, want none for an in-process start", row.Caller)
	}
}
