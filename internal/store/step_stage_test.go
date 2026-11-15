package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// checkStage fails the test unless row reads back the stage it was set to: the stage name and the time it started.
func checkStage(t *testing.T, reader string, row StepRow, stage string, from time.Time) {
	t.Helper()
	if row.LoopStage != stage || !row.LoopStageFrom.Equal(from) {
		t.Fatalf("%s stage: got %q from %v, want %q from %v", reader, row.LoopStage, row.LoopStageFrom, stage, from)
	}
}

func TestStepLoopStoresEachStageAndItsStart(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openSyncStore(t)
	if err := s.ApplyEngine(ctx, []EngineOp{stepAddOf("s1")}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine add: %v", err)
	}

	stages := []string{
		StepStageTurn, StepStageChecks, StepStageGate, StepStageJudge, StepStageHeld, StepStageLimited,
	}
	for i, stage := range stages {
		from := engineNowAt().Add(time.Duration(i+1) * time.Minute)
		if err := s.ApplyEngine(ctx, []EngineOp{
			StepLoop{ID: "s1", Stage: stage, StageFrom: from},
		}, engineNowAt()); err != nil {
			t.Fatalf("ApplyEngine stage %q: %v", stage, err)
		}
		got, err := s.Step(ctx, "s1")
		if err != nil {
			t.Fatalf("Step after stage %q: %v", stage, err)
		}
		checkStage(t, "Step", got, stage, from)
	}
}

func TestStepLoopWithZeroStageStartReadsZeroTime(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openSyncStore(t)
	if err := s.ApplyEngine(ctx, []EngineOp{stepAddOf("s1")}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine add: %v", err)
	}
	if err := s.ApplyEngine(ctx, []EngineOp{
		StepLoop{ID: "s1", Stage: StepStageGate},
	}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine stage: %v", err)
	}

	got, err := s.Step(ctx, "s1")
	if err != nil {
		t.Fatalf("Step: %v", err)
	}
	checkStage(t, "Step", got, StepStageGate, time.Time{})
	if !got.LoopStageFrom.IsZero() {
		t.Fatalf("zero stage start: got %v, want the zero time", got.LoopStageFrom)
	}
}

func TestStepStageSurvivesReopeningTheStore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	seedMigrated(t, path)
	from := engineNowAt().Add(time.Minute)

	s, err := Open(ctx, path, noSync())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.ApplyEngine(ctx, []EngineOp{
		stepAddOf("s1"),
		StepLoop{ID: "s1", Stage: StepStageJudge, StageFrom: from},
	}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := Open(ctx, path, noSync())
	if err != nil {
		t.Fatalf("Open after close: %v", err)
	}
	t.Cleanup(func() {
		if err := reopened.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	got, err := reopened.Step(ctx, "s1")
	if err != nil {
		t.Fatalf("Step after reopen: %v", err)
	}
	checkStage(t, "Step after reopen", got, StepStageJudge, from)
}
