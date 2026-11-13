package store

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPushedRowsReopen(t *testing.T) {
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
	wantRows := func(stage string, want []PushedRow) {
		t.Helper()
		got, err := s.Pushed(ctx)
		if err != nil {
			t.Fatalf("Pushed %s: %v", stage, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("Pushed %s: got %+v, want %+v", stage, got, want)
		}
	}

	// A pane row has no session; a session row has no pane.
	pane := PushedRow{Pane: "%3", Session: "", Time: 1_700_000_000}
	session := PushedRow{Pane: "", Session: "session-a", Time: 1_700_000_010}

	for _, row := range []PushedRow{pane, session} {
		if err := s.PutPushed(ctx, row); err != nil {
			t.Fatalf("PutPushed %+v: %v", row, err)
		}
	}
	// Putting the pane row again replaces its time; it does not add a second row.
	pane.Time = 1_700_000_500
	if err := s.PutPushed(ctx, pane); err != nil {
		t.Fatalf("PutPushed %+v again: %v", pane, err)
	}
	// Rows list ordered by pane, then session: the empty pane sorts first.
	wantRows("after puts", []PushedRow{session, pane})

	reopen()
	wantRows("after reopen", []PushedRow{session, pane})

	// Deleting the pane row removes only that row; an absent key is not an error.
	if err := s.DeletePushed(ctx, pane.Pane, pane.Session); err != nil {
		t.Fatalf("DeletePushed pane: %v", err)
	}
	if err := s.DeletePushed(ctx, "%9", ""); err != nil {
		t.Fatalf("DeletePushed of absent key: got %v, want nil", err)
	}
	wantRows("after deleting pane row", []PushedRow{session})

	reopen()
	wantRows("after deleting pane row and reopen", []PushedRow{session})
}
