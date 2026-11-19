package store

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// applyStepOps applies ops in one write at at, failing the test when the write fails.
func applyStepOps(ctx context.Context, t *testing.T, s *Store, at time.Time, ops ...EngineOp) {
	t.Helper()
	if err := s.ApplyEngine(ctx, ops, at); err != nil {
		t.Fatalf("ApplyEngine at %d: %v", at.Unix(), err)
	}
}

func TestStepEndFactsStoreReturnLines(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openPendingStore(t)
	const id = "step-lines"
	applyStepOps(ctx, t, s, pendingAt(), StepAdd{ID: id, Agent: "implementer", Prompt: "do it"})

	// returnWith gives step id a return carrying lines, and reads back the step's line counts.
	returnWith := func(lines *StepLines) StepLines {
		t.Helper()
		applyStepOps(ctx, t, s, pendingAt(), StepReturn{ID: id, Result: `{"summary":"built"}`, Lines: lines})
		row, err := s.Step(ctx, id)
		if err != nil {
			t.Fatalf("Step %s: %v", id, err)
		}
		return row.Lines
	}

	if got := returnWith(nil); got != (StepLines{}) {
		t.Errorf("Lines after a return with no counts = %+v, want zeros", got)
	}
	counted := StepLines{Counted: true, Files: 2, Added: 10, Removed: 3}
	if got := returnWith(&counted); got != counted {
		t.Errorf("Lines after a counted return = %+v, want %+v", got, counted)
	}
	later := StepLines{Counted: true, Files: 4, Added: 25, Removed: 9}
	if got := returnWith(&later); got != later {
		t.Errorf("Lines after a later counted return = %+v, want %+v", got, later)
	}
	if got := returnWith(nil); got != later {
		t.Errorf("Lines after a return with no counts = %+v, want the earlier %+v", got, later)
	}
	uncounted := StepLines{Counted: false, Files: 5, Added: 6, Removed: 7}
	if got := returnWith(&uncounted); got != (StepLines{}) {
		t.Errorf("Lines after an uncounted return = %+v, want zeros", got)
	}
}

func TestStepEndFactsStorePendingEvent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, path := openPendingStore(t)
	addPendingEngines(t, s, "engine-1")
	start := pendingAt()

	// step-counted loops three rounds, returns with a counted change and ends 90 seconds after it started. step-bare
	// never returns and ends 45 seconds after it started.
	startPendingStep(t, s, "step-counted", "", "engine-1")
	applyStepOps(ctx, t, s, start, StepLoop{ID: "step-counted", Round: 3, Stage: StepStageTurn, StageFrom: start})
	applyStepOps(ctx, t, s, start.Add(90*time.Second),
		StepReturn{ID: "step-counted", Result: `{"summary":"built"}`,
			Lines: &StepLines{Counted: true, Files: 3, Added: 40, Removed: 7}},
		StepEnd{ID: "step-counted", End: "done", Status: "passed", Reason: "gate green"},
	)
	startPendingStep(t, s, "step-bare", "", "engine-1")
	applyStepOps(ctx, t, s, start.Add(45*time.Second),
		StepEnd{ID: "step-bare", End: "failed", Status: "failed", Reason: "gave up"},
	)

	events := readPending(t, path, "engine-1")
	want := map[string]StepFacts{
		"step-counted": {Rounds: 3, Seconds: 90, Lines: StepLines{Counted: true, Files: 3, Added: 40, Removed: 7}},
		"step-bare":    {Rounds: 0, Seconds: 45, Lines: StepLines{}},
	}
	got := make(map[string]StepFacts, len(events))
	for _, event := range events {
		got[event.Step] = event.Facts
	}
	if len(events) != len(want) || !reflect.DeepEqual(got, want) {
		t.Errorf("pending step ends carry facts %+v, want %+v", got, want)
	}
}

func TestStepEndFactsStoreReaderFacts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, path := openPendingStore(t)
	start := pendingAt()

	// step-ended loops two rounds, returns with a counted change and ends 120 seconds after it started. step-running
	// loops four rounds and has not ended. step-returned ends 30 seconds after it started and never returns.
	applyStepOps(ctx, t, s, start,
		StepAdd{ID: "step-ended", Agent: "implementer", Prompt: "do it"},
		StepAdd{ID: "step-running", Agent: "implementer", Prompt: "do it"},
		StepAdd{ID: "step-returned", Agent: "implementer", Prompt: "do it"},
		StepLoop{ID: "step-ended", Round: 2, Stage: StepStageTurn, StageFrom: start},
		StepLoop{ID: "step-running", Round: 4, Stage: StepStageTurn, StageFrom: start},
	)
	applyStepOps(ctx, t, s, start.Add(120*time.Second),
		StepReturn{ID: "step-ended", Result: `{"summary":"built"}`,
			Lines: &StepLines{Counted: true, Files: 5, Added: 50, Removed: 12}},
		StepEnd{ID: "step-ended", End: "done", Status: "passed", Reason: "gate green"},
	)
	applyStepOps(ctx, t, s, start.Add(30*time.Second),
		StepEnd{ID: "step-returned", End: "failed", Status: "failed", Reason: "gave up"},
	)

	got, err := NewReader(path).StepFacts(ctx, []string{"step-ended", "step-running", "step-returned", "step-unknown"})
	if err != nil {
		t.Fatalf("StepFacts: %v", err)
	}
	want := map[string]StepFacts{
		"step-ended":    {Rounds: 2, Seconds: 120, Lines: StepLines{Counted: true, Files: 5, Added: 50, Removed: 12}},
		"step-running":  {Rounds: 4, Seconds: 0, Lines: StepLines{}},
		"step-returned": {Rounds: 0, Seconds: 30, Lines: StepLines{}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("StepFacts = %+v, want %+v", got, want)
	}

	empty, err := NewReader(filepath.Join(t.TempDir(), "missing.db")).StepFacts(ctx, nil)
	if err != nil {
		t.Fatalf("StepFacts of no ids: %v", err)
	}
	if !reflect.DeepEqual(empty, map[string]StepFacts{}) {
		t.Errorf("StepFacts of no ids = %+v, want an empty map", empty)
	}

	_, err = NewReader(filepath.Join(t.TempDir(), "missing.db")).StepFacts(ctx, []string{"step-ended"})
	if !errors.Is(err, ErrUnavailable) {
		t.Errorf("StepFacts of a missing store: got %v, want ErrUnavailable", err)
	}
}
