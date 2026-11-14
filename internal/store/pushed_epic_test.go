package store

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPushedEpicRows(t *testing.T) {
	t.Parallel()
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

	pane := PushedRow{Pane: "%3", Time: 1_700_000_000}
	session := PushedRow{Session: "session-a", Time: 1_700_000_010}
	// The epic row is put before epic-a, so the listing must order epics by name, not by put order.
	epicB := PushedRow{Epic: "epic-b", Time: 1_700_000_020}
	for _, row := range []PushedRow{pane, session, epicB} {
		if err := s.PutPushed(ctx, row); err != nil {
			t.Fatalf("PutPushed %+v: %v", row, err)
		}
	}
	// Pane and session rows list first, ordered by pane then session; epic rows follow.
	wantRows("after puts", []PushedRow{session, pane, epicB})

	// Putting the epic row again replaces its time; it does not add a second row.
	epicB.Time = 1_700_000_500
	if err := s.PutPushed(ctx, epicB); err != nil {
		t.Fatalf("PutPushed %+v again: %v", epicB, err)
	}
	wantRows("after replace", []PushedRow{session, pane, epicB})

	epicA := PushedRow{Epic: "epic-a", Time: 1_700_000_030}
	if err := s.PutPushed(ctx, epicA); err != nil {
		t.Fatalf("PutPushed %+v: %v", epicA, err)
	}
	wantRows("after second epic", []PushedRow{session, pane, epicA, epicB})

	reopen()
	wantRows("after reopen", []PushedRow{session, pane, epicA, epicB})

	// Deleting epic-a removes only that row; an absent epic is not an error.
	if err := s.DeletePushedEpic(ctx, epicA.Epic); err != nil {
		t.Fatalf("DeletePushedEpic epic-a: %v", err)
	}
	if err := s.DeletePushedEpic(ctx, "epic-z"); err != nil {
		t.Fatalf("DeletePushedEpic of absent epic: got %v, want nil", err)
	}
	wantRows("after deleting epic-a", []PushedRow{session, pane, epicB})

	reopen()
	wantRows("after deleting epic-a and reopen", []PushedRow{session, pane, epicB})
}

func TestPushedEpicAndPaneKeysAreDistinct(t *testing.T) {
	t.Parallel()
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

	// A pane and an epic whose ids have the same text are two rows, not one.
	pane := PushedRow{Pane: "orrery-1", Time: 1_700_000_000}
	epic := PushedRow{Epic: "orrery-1", Time: 1_700_000_010}
	for _, row := range []PushedRow{pane, epic} {
		if err := s.PutPushed(ctx, row); err != nil {
			t.Fatalf("PutPushed %+v: %v", row, err)
		}
	}
	wantRows("after puts", []PushedRow{pane, epic})

	// Deleting the pane row leaves the epic row, and deleting the epic row leaves nothing.
	if err := s.DeletePushed(ctx, pane.Pane, pane.Session); err != nil {
		t.Fatalf("DeletePushed pane: %v", err)
	}
	wantRows("after deleting pane row", []PushedRow{epic})
	if err := s.DeletePushedEpic(ctx, epic.Epic); err != nil {
		t.Fatalf("DeletePushedEpic: %v", err)
	}
	wantRows("after deleting epic row", []PushedRow{})
}

func TestNoticeEpicRoundTrip(t *testing.T) {
	t.Parallel()
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

	// A notice naming an epic, and a notice naming a pane: the pane notice keeps Epic empty.
	epic := NoticeRow{
		ID: 1, Epic: "epic-a", State: "waiting", Time: 1_700_000_000, Phase: "deciding",
		SourceKind: "session", Source: []byte(`{"event":"stop"}`),
	}
	pane := NoticeRow{
		ID: 2, Pane: "%3", Session: "session-a", State: "waiting", Time: 1_700_000_010, Phase: "deciding",
	}
	for _, row := range []NoticeRow{pane, epic} {
		if err := s.PutNotice(ctx, row); err != nil {
			t.Fatalf("PutNotice %d: %v", row.ID, err)
		}
	}
	wantRows("after puts", []NoticeRow{epic, pane})

	reopen()
	wantRows("after reopen", []NoticeRow{epic, pane})
}
