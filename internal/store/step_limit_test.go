package store

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// checkLimit fails the test unless row reads back the limit it was set to: limited, kind and reset.
func checkLimit(t *testing.T, reader string, row StepRow, limited bool, kind string, resets time.Time) {
	t.Helper()
	if row.LoopLimited != limited || row.LoopLimitKind != kind || !row.LoopLimitResets.Equal(resets) {
		t.Fatalf("%s limit: got limited %t kind %q resets %v, want limited %t kind %q resets %v",
			reader, row.LoopLimited, row.LoopLimitKind, row.LoopLimitResets, limited, kind, resets)
	}
}

func TestStepLimitReadsBackThroughEveryReader(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openSyncStore(t)
	if err := s.ApplyEngine(ctx, []EngineOp{stepAddOf("s1")}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine add: %v", err)
	}
	resets := time.Unix(1_700_003_600, 0)
	if err := s.ApplyEngine(ctx, []EngineOp{
		StepLoop{ID: "s1", Limited: true, LimitKind: "usage", LimitResets: resets},
	}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine limit: %v", err)
	}

	got, err := s.Step(ctx, "s1")
	if err != nil {
		t.Fatalf("Step: %v", err)
	}
	checkLimit(t, "Step", got, true, "usage", resets)

	open, err := s.OpenSteps(ctx)
	if err != nil {
		t.Fatalf("OpenSteps: %v", err)
	}
	if len(open) != 1 {
		t.Fatalf("OpenSteps: got %d rows, want 1", len(open))
	}
	checkLimit(t, "OpenSteps", open[0], true, "usage", resets)

	limited, err := s.LimitedSteps(ctx)
	if err != nil {
		t.Fatalf("LimitedSteps: %v", err)
	}
	if len(limited) != 1 {
		t.Fatalf("LimitedSteps: got %d rows, want 1", len(limited))
	}
	checkLimit(t, "LimitedSteps", limited[0], true, "usage", resets)
}

func TestStepLimitWithUnknownResetStoresZeroAndReadsZeroTime(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openSyncStore(t)
	if err := s.ApplyEngine(ctx, []EngineOp{stepAddOf("s1")}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine add: %v", err)
	}
	if err := s.ApplyEngine(ctx, []EngineOp{
		StepLoop{ID: "s1", Limited: true, LimitKind: "rate"},
	}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine limit: %v", err)
	}

	var raw int64
	if err := s.db.QueryRowContext(ctx,
		`SELECT loop_limit_resets FROM steps WHERE id = 's1'`,
	).Scan(&raw); err != nil {
		t.Fatalf("read loop_limit_resets: %v", err)
	}
	if raw != 0 {
		t.Fatalf("loop_limit_resets: got %d, want 0 for an unknown reset", raw)
	}

	got, err := s.Step(ctx, "s1")
	if err != nil {
		t.Fatalf("Step: %v", err)
	}
	checkLimit(t, "Step", got, true, "rate", time.Time{})
	if !got.LoopLimitResets.IsZero() {
		t.Fatalf("unknown reset: got %v, want the zero time", got.LoopLimitResets)
	}
}

func TestStepLoopWithoutLimitClearsTheLimit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openSyncStore(t)
	if err := s.ApplyEngine(ctx, []EngineOp{stepAddOf("s1")}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine add: %v", err)
	}
	if err := s.ApplyEngine(ctx, []EngineOp{
		StepLoop{ID: "s1", Limited: true, LimitKind: "usage", LimitResets: time.Unix(1_700_003_600, 0)},
	}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine limit: %v", err)
	}

	// StepLoop sets every loop column, so a later write with no limit stores unlimited, "" and zero.
	if err := s.ApplyEngine(ctx, []EngineOp{StepLoop{ID: "s1"}}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine unlimited: %v", err)
	}
	got, err := s.Step(ctx, "s1")
	if err != nil {
		t.Fatalf("Step: %v", err)
	}
	checkLimit(t, "Step", got, false, "", time.Time{})
	if !got.LoopLimitResets.IsZero() {
		t.Fatalf("cleared reset: got %v, want the zero time", got.LoopLimitResets)
	}

	limited, err := s.LimitedSteps(ctx)
	if err != nil {
		t.Fatalf("LimitedSteps: %v", err)
	}
	if len(limited) != 0 {
		t.Fatalf("LimitedSteps after clear: got %v, want none", stepIDsOf(limited))
	}
}

func TestLimitedStepsListsOpenLimitedRowsOldestFirst(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openSyncStore(t)
	t0 := engineNowAt()
	t1 := t0.Add(time.Minute)
	// b and c start together at t0 and a starts later, so started order differs from id order.
	if err := s.ApplyEngine(ctx, []EngineOp{
		stepAddOf("b"), stepAddOf("c"), stepAddOf("d"), stepAddOf("e"),
	}, t0); err != nil {
		t.Fatalf("ApplyEngine add t0: %v", err)
	}
	if err := s.ApplyEngine(ctx, []EngineOp{stepAddOf("a")}, t1); err != nil {
		t.Fatalf("ApplyEngine add t1: %v", err)
	}
	if err := s.ApplyEngine(ctx, []EngineOp{
		StepLoop{ID: "a", Limited: true, LimitKind: "usage"},
		StepLoop{ID: "b", Limited: true, LimitKind: "usage"},
		StepLoop{ID: "c", Limited: true, LimitKind: "usage"},
		StepLoop{ID: "e", Limited: true, LimitKind: "usage"},
		StepEnd{ID: "e", End: "done", Status: "failed", Reason: "gate red"},
	}, t1); err != nil {
		t.Fatalf("ApplyEngine limit: %v", err)
	}

	limited, err := s.LimitedSteps(ctx)
	if err != nil {
		t.Fatalf("LimitedSteps: %v", err)
	}
	if got, want := stepIDsOf(limited), []string{"b", "c", "a"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("LimitedSteps: got %v, want %v (open limited rows, started then id; d unlimited and e ended left out)",
			got, want)
	}
}

func TestStepRowWrittenBeforeLimitColumnsReadsUnlimited(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openSyncStore(t)
	// This insert names no limit column, the way a row written before migration 00017 was written.
	if _, err := s.db.ExecContext(ctx, `INSERT INTO steps (id, agent, prompt, output_schema, workspace, revision,
		worktree, done_value, read_only, owned, gate, gate_limit, started, checks, result, status, end_value, reason,
		ended) VALUES ('old', 'implementer', 'do it', '{}', '/ws/old', 'abc123', '/wt/old', 'DONE', 1, '[]', '[]', 0,
		0, '[]', '', '', '', '', 0)`); err != nil {
		t.Fatalf("insert row without limit columns: %v", err)
	}

	got, err := s.Step(ctx, "old")
	if err != nil {
		t.Fatalf("Step: %v", err)
	}
	checkLimit(t, "Step", got, false, "", time.Time{})

	limited, err := s.LimitedSteps(ctx)
	if err != nil {
		t.Fatalf("LimitedSteps: %v", err)
	}
	if len(limited) != 0 {
		t.Fatalf("LimitedSteps: got %v, want none for a pre-migration row", stepIDsOf(limited))
	}
}

func TestStepLimitSurvivesReopeningTheStore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	seedMigrated(t, path)
	resets := time.Unix(1_700_003_600, 0)

	s, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.ApplyEngine(ctx, []EngineOp{
		stepAddOf("s1"),
		StepLoop{ID: "s1", Limited: true, LimitKind: "usage", LimitResets: resets},
	}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := Open(ctx, path)
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
	checkLimit(t, "Step after reopen", got, true, "usage", resets)

	limited, err := reopened.LimitedSteps(ctx)
	if err != nil {
		t.Fatalf("LimitedSteps after reopen: %v", err)
	}
	if len(limited) != 1 {
		t.Fatalf("LimitedSteps after reopen: got %d rows, want 1", len(limited))
	}
	checkLimit(t, "LimitedSteps after reopen", limited[0], true, "usage", resets)
}
