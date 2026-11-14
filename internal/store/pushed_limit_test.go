package store

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPushedLimitRowsReopen(t *testing.T) {
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

	// A pane row has no session and no limit; a session row has no pane and no limit; a limit row has neither.
	pane := PushedRow{Pane: "%3", Time: 1_700_000_000}
	session := PushedRow{Session: "session-a", Time: 1_700_000_010}
	weekly := PushedRow{Limit: "weekly", Time: 1_700_000_020}
	fiveHour := PushedRow{Limit: "five-hour", Time: 1_700_000_030}

	for _, row := range []PushedRow{weekly, pane, fiveHour, session} {
		if err := s.PutPushed(ctx, row); err != nil {
			t.Fatalf("PutPushed %+v: %v", row, err)
		}
	}
	// Pane and session rows list first, ordered by pane then session; limit rows follow, ordered by limit key.
	wantRows("after puts", []PushedRow{session, pane, fiveHour, weekly})

	reopen()
	wantRows("after reopen", []PushedRow{session, pane, fiveHour, weekly})

	// Putting the limit row again replaces its time; it does not add a second row.
	fiveHour.Time = 1_700_000_500
	if err := s.PutPushed(ctx, fiveHour); err != nil {
		t.Fatalf("PutPushed %+v again: %v", fiveHour, err)
	}
	wantRows("after limit row replaced", []PushedRow{session, pane, fiveHour, weekly})

	reopen()
	wantRows("after limit row replaced and reopen", []PushedRow{session, pane, fiveHour, weekly})

	// Deleting one limit row removes only that row; a limit that is not stored is not an error.
	if err := s.DeletePushedLimit(ctx, fiveHour.Limit); err != nil {
		t.Fatalf("DeletePushedLimit five-hour: %v", err)
	}
	if err := s.DeletePushedLimit(ctx, "monthly"); err != nil {
		t.Fatalf("DeletePushedLimit of absent limit: got %v, want nil", err)
	}
	wantRows("after deleting limit row", []PushedRow{session, pane, weekly})

	reopen()
	wantRows("after deleting limit row and reopen", []PushedRow{session, pane, weekly})
}
