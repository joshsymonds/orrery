package store

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
)

// TestEndComesBackStepsTableHasNoCallerRead migrates a store from version 62, whose steps table has caller_read, and
// checks that Open drops the column.
func TestEndComesBackStepsTableHasNoCallerRead(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "orrery.db")
	fixture := openStoreAtVersion(t, path, 62)
	var before int
	if err := fixture.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info('steps') WHERE name = 'caller_read'`).Scan(&before); err != nil {
		t.Fatalf("read steps columns of the version 62 store: %v", err)
	}
	if err := fixture.Close(); err != nil {
		t.Fatalf("close fixture: %v", err)
	}
	if before != 1 {
		t.Fatalf("steps of the version 62 store has %d caller_read columns, want 1", before)
	}

	s, err := Open(context.Background(), path, noSync())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	if cols := columnsOf(t, s, "steps"); slices.Contains(cols, "caller_read") {
		t.Fatalf("steps columns after Open = %v, want no caller_read", cols)
	}
}
