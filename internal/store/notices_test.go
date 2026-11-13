package store

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPendingRowsReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")

	s, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	reopen := func() {
		t.Helper()
		if err := s.Close(); err != nil {
			t.Fatalf("Close before reopen: %v", err)
		}
		reopened, openErr := Open(ctx, path)
		if openErr != nil {
			t.Fatalf("Open after close: %v", openErr)
		}
		s = reopened
	}
	put := func(row NoticeRow) {
		t.Helper()
		if err := s.PutNotice(ctx, row); err != nil {
			t.Fatalf("PutNotice %d: %v", row.ID, err)
		}
	}
	wantRows := func(stage string, want []NoticeRow) {
		t.Helper()
		got, err := s.Notices(ctx)
		if err != nil {
			t.Fatalf("Notices %s: %v", stage, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("Notices %s: got %+v, want %+v", stage, got, want)
		}
	}
	wantFloor := func(stage string, want int64) {
		t.Helper()
		got, err := s.NoticeIDFloor(ctx)
		if err != nil {
			t.Fatalf("NoticeIDFloor %s: %v", stage, err)
		}
		if got != want {
			t.Fatalf("NoticeIDFloor %s: got %d, want %d", stage, got, want)
		}
	}

	first := NoticeRow{
		ID: 1, Pane: "%3", Session: "session-a", State: "waiting", Time: 1_700_000_000,
		Phase: "delayed", Due: 1_700_000_090_000, DelayServed: true, WatchLogged: false,
		SourceKind: "session", Source: []byte(`{"event":"stop"}`),
	}
	second := NoticeRow{
		ID: 2, Pane: "%4", Session: "session-b", State: "waiting", Time: 1_700_000_010,
		Phase: "held", Due: 0, DelayServed: false, WatchLogged: true,
		SourceKind: "session", Source: []byte(`{"event":"idle"}`),
	}
	// A notice with no source: SourceKind is empty and Source is nil.
	bare := NoticeRow{
		ID: 3, Pane: "%5", Session: "session-c", State: "waiting", Time: 1_700_000_020,
		Phase: "deciding",
	}

	wantRows("on an empty store", []NoticeRow{})
	wantFloor("on an empty store", 0)

	// Put out of id order: Notices must still list ascending by id.
	put(second)
	put(first)
	put(bare)
	wantRows("after puts", []NoticeRow{first, second, bare})
	wantFloor("after puts", 3)

	// A second put of id 2 replaces every column of the row, including the source.
	replaced := NoticeRow{
		ID: 2, Pane: "%9", Session: "session-z", State: "desk", Time: 1_700_000_500,
		Phase: "desk", Due: 1_700_000_900_000, DelayServed: true, WatchLogged: true,
	}
	put(replaced)
	wantRows("after replace", []NoticeRow{first, replaced, bare})
	wantFloor("after replace", 3)

	reopen()
	wantRows("after reopen", []NoticeRow{first, replaced, bare})
	wantFloor("after reopen", 3)

	// Deleting id 2 removes only that row; an absent id is not an error.
	if err := s.DeleteNotice(ctx, 2); err != nil {
		t.Fatalf("DeleteNotice 2: %v", err)
	}
	if err := s.DeleteNotice(ctx, 99); err != nil {
		t.Fatalf("DeleteNotice of absent id 99: got %v, want nil", err)
	}
	wantRows("after deleting id 2", []NoticeRow{first, bare})

	// Deleting every row empties the list, but the floor stays at the highest id put.
	if err := s.DeleteNotice(ctx, 1); err != nil {
		t.Fatalf("DeleteNotice 1: %v", err)
	}
	if err := s.DeleteNotice(ctx, 3); err != nil {
		t.Fatalf("DeleteNotice 3: %v", err)
	}
	wantRows("after deleting every row", []NoticeRow{})
	wantFloor("after deleting every row", 3)

	reopen()
	wantRows("empty after reopen", []NoticeRow{})
	wantFloor("after deleting every row and reopen", 3)

	// A higher id raises the floor; a lower id put afterwards does not lower it.
	put(NoticeRow{ID: 5, Pane: "%6", Session: "session-d", State: "waiting", Time: 1_700_000_600, Phase: "deciding"})
	wantFloor("after put of id 5", 5)
	put(NoticeRow{ID: 4, Pane: "%7", Session: "session-e", State: "waiting", Time: 1_700_000_700, Phase: "deciding"})
	wantFloor("after put of lower id 4", 5)
}
