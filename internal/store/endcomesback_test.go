package store

import (
	"context"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"
)

// TestEndComesBackPendingListsEndUntilMarked: the end of a step its caller started is owed to that caller until a mark
// of the event under the caller's row id, and is listed no more after it.
func TestEndComesBackPendingListsEndUntilMarked(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, path := openPendingStore(t)
	addPendingEngines(t, s, "engine-1")
	startPendingStep(t, s, "step-1", "", "engine-1")
	endPendingStep(t, s, "step-1", "A", pendingAt().Add(time.Minute))
	const event = "step:step-1"

	if got := pendingIDs(readPending(t, path, "engine-1")); !reflect.DeepEqual(got, []string{event}) {
		t.Fatalf("PendingCallerEvents before the mark = %v, want [%s]", got, event)
	}
	if err := s.MarkCallerEvent(ctx, event, "engine-1", pendingAt()); err != nil {
		t.Fatalf("MarkCallerEvent %s: %v", event, err)
	}
	if got := readPending(t, path, "engine-1"); len(got) != 0 {
		t.Fatalf("PendingCallerEvents after the mark = %v, want none", pendingIDs(got))
	}
}

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
