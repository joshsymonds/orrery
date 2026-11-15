package store

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPushedAlertRowsReopen(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")

	seedMigrated(t, path)
	s, err := Open(ctx, path, noSync())
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
		reopened, openErr := Open(ctx, path, noSync())
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

	// Each row sets exactly one of Pane, Session, Epic, Limit and Alert.
	session := PushedRow{Session: "session-a", Time: 1_700_000_010}
	pane := PushedRow{Pane: "%3", Time: 1_700_000_000}
	epic := PushedRow{Epic: "epic-a", Time: 1_700_000_015}
	weekly := PushedRow{Limit: "weekly", Time: 1_700_000_020}
	diskFull := PushedRow{Alert: "disk-full", Time: 1_700_000_040}
	apiLatency := PushedRow{Alert: "api-latency", Time: 1_700_000_050}

	for _, row := range []PushedRow{diskFull, weekly, pane, epic, apiLatency, session} {
		if err := s.PutPushed(ctx, row); err != nil {
			t.Fatalf("PutPushed %+v: %v", row, err)
		}
	}
	// Pane and session rows list first, then epic rows, then limit rows; alert rows follow last, ordered by alert.
	wantRows("after puts", []PushedRow{session, pane, epic, weekly, apiLatency, diskFull})

	reopen()
	wantRows("after reopen", []PushedRow{session, pane, epic, weekly, apiLatency, diskFull})

	// Putting the alert row again replaces its time; it does not add a second row.
	diskFull.Time = 1_700_000_500
	if err := s.PutPushed(ctx, diskFull); err != nil {
		t.Fatalf("PutPushed %+v again: %v", diskFull, err)
	}
	wantRows("after alert row replaced", []PushedRow{session, pane, epic, weekly, apiLatency, diskFull})

	reopen()
	wantRows("after alert row replaced and reopen", []PushedRow{session, pane, epic, weekly, apiLatency, diskFull})

	// Deleting one alert row removes only that row; an alert that is not stored is not an error.
	if err := s.DeletePushedAlert(ctx, diskFull.Alert); err != nil {
		t.Fatalf("DeletePushedAlert disk-full: %v", err)
	}
	if err := s.DeletePushedAlert(ctx, "memory-pressure"); err != nil {
		t.Fatalf("DeletePushedAlert of absent alert: got %v, want nil", err)
	}
	wantRows("after deleting alert row", []PushedRow{session, pane, epic, weekly, apiLatency})

	reopen()
	wantRows("after deleting alert row and reopen", []PushedRow{session, pane, epic, weekly, apiLatency})
}
