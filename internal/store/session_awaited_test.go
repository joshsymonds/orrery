package store

import (
	"context"
	"testing"
	"time"
)

// TestSessionAwaitedMigrationTakesEachRowsBackgroundFlag covers 00073_session_awaited.sql: a row stored before the
// awaited column takes its background flag as its awaited flag, so a step re-adopted from it after the upgrade waits as
// it did before, and the Down drops the column.
func TestSessionAwaitedMigrationTakesEachRowsBackgroundFlag(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, provider := storeAtVersion(t, 72)
	if err := s.ApplyEngine(ctx, []EngineOp{
		EngineAdd{ID: "listed", Dir: "/d", State: "done"},
		EngineAdd{ID: "quiet", Dir: "/d", State: "done"},
	}, time.Unix(1700000000, 0)); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE sessions SET background = 1 WHERE id = 'listed'`); err != nil {
		t.Fatalf("set the background flag at version 72: %v", err)
	}

	if _, err := provider.UpByOne(ctx); err != nil {
		t.Fatalf("UpByOne: %v", err)
	}
	for id, want := range map[string]bool{"listed": true, "quiet": false} {
		var awaited bool
		if err := s.db.QueryRowContext(ctx, `SELECT awaited FROM sessions WHERE id = ?`, id).
			Scan(&awaited); err != nil {
			t.Fatalf("read awaited of %s: %v", id, err)
		}
		if awaited != want {
			t.Errorf("awaited of %s after the Up = %v, want its background flag %v", id, awaited, want)
		}
	}

	if _, err := provider.Down(ctx); err != nil {
		t.Fatalf("Down: %v", err)
	}
	var columns int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name = 'awaited'`).Scan(&columns); err != nil {
		t.Fatalf("read the sessions columns: %v", err)
	}
	if columns != 0 {
		t.Fatalf("sessions has %d awaited columns after the Down, want none", columns)
	}
}
