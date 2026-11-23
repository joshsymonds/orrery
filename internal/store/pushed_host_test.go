package store

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
)

// TestPushedHostRows checks that two hosts' pushes for %3 keep two rows that delete apart. The migration that adds the
// host keeps a row stored before it, with an empty host.
func TestPushedHostRows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, provider := storeAtVersion(t, 103)
	if _, err := s.db.DB.ExecContext(ctx,
		`INSERT INTO pushed_notices (pane, session, time) VALUES ('%3', '', 1700000000)`); err != nil {
		t.Fatalf("seed a pushed row before the host column: %v", err)
	}
	if _, err := provider.UpByOne(ctx); err != nil {
		t.Fatalf("Up of 00104: %v", err)
	}
	wantRows := func(stage string, want ...PushedRow) {
		t.Helper()
		got, err := s.Pushed(ctx)
		if err != nil {
			t.Fatalf("Pushed %s: %v", stage, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("Pushed %s: got %+v, want %+v", stage, got, want)
		}
	}

	old := PushedRow{Host: "", Pane: "%3", Time: 1_700_000_000}
	hostA := PushedRow{Host: "host-a", Pane: "%3", Time: 1_700_000_100}
	hostB := PushedRow{Host: "host-b", Pane: "%3", Time: 1_700_000_200}
	wantRows("after the migration", old)

	for _, row := range []PushedRow{hostA, hostB} {
		if err := s.PutPushed(ctx, row); err != nil {
			t.Fatalf("PutPushed %+v: %v", row, err)
		}
	}
	wantRows("after the puts", old, hostA, hostB)

	if err := s.DeletePushed(ctx, hostA.Host, hostA.Pane, hostA.Session); err != nil {
		t.Fatalf("DeletePushed host-a: %v", err)
	}
	wantRows("after deleting host-a's row", old, hostB)

	if err := s.DeletePushed(ctx, hostB.Host, hostB.Pane, hostB.Session); err != nil {
		t.Fatalf("DeletePushed host-b: %v", err)
	}
	wantRows("after deleting host-b's row", old)
}

// TestNameUnhostedPaneRowsNamesPaneRows checks that the controller's name goes on each pane row that reads no host: a
// pending notice, a notice history row and a pushed row. A session row keeps an empty host. A pushed row whose pane and
// session the named row already holds is dropped, so the named row keeps its time.
func TestNameUnhostedPaneRowsNamesPaneRows(t *testing.T) {
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

	const at = 1_700_000_000
	if err := s.PutNotice(ctx, NoticeRow{
		ID: 1, Pane: "%4", State: "waiting", Time: at, Phase: "deciding", SourceKind: "pane", Source: []byte(`{}`),
	}); err != nil {
		t.Fatalf("PutNotice: %v", err)
	}
	if err := s.RecordNoticeHistory(ctx, NoticeHistoryRow{
		Key: NoticeHistoryKey{Pane: "%4"}, State: "waiting", Title: "t", Body: "b", Time: at,
	}); err != nil {
		t.Fatalf("RecordNoticeHistory: %v", err)
	}
	for _, row := range []PushedRow{
		{Session: "session-a", Time: at},
		{Pane: "%3", Time: at},
		{Host: "orrery-a", Pane: "%3", Time: at + 10},
		{Pane: "%4", Time: at},
	} {
		if err := s.PutPushed(ctx, row); err != nil {
			t.Fatalf("PutPushed %+v: %v", row, err)
		}
	}

	if err := s.NameUnhostedPaneRows(ctx, ""); err == nil {
		t.Fatal("NameUnhostedPaneRows with an empty name: got nil, want an error")
	}
	if err := s.NameUnhostedPaneRows(ctx, "orrery-a"); err != nil {
		t.Fatalf("NameUnhostedPaneRows: %v", err)
	}

	pushed, err := s.Pushed(ctx)
	if err != nil {
		t.Fatalf("Pushed: %v", err)
	}
	wantPushed := []PushedRow{
		{Session: "session-a", Time: at},
		{Host: "orrery-a", Pane: "%3", Time: at + 10},
		{Host: "orrery-a", Pane: "%4", Time: at},
	}
	if !reflect.DeepEqual(pushed, wantPushed) {
		t.Fatalf("Pushed after naming = %+v, want %+v", pushed, wantPushed)
	}
	history, err := s.NoticeHistory(ctx, 0)
	if err != nil {
		t.Fatalf("NoticeHistory: %v", err)
	}
	if len(history) != 1 || history[0].Key.Host != "orrery-a" {
		t.Fatalf("NoticeHistory after naming = %+v, want one row named orrery-a", history)
	}
	notices, err := s.Notices(ctx)
	if err != nil {
		t.Fatalf("Notices: %v", err)
	}
	if len(notices) != 1 || notices[0].Host != "orrery-a" {
		t.Fatalf("Notices after naming = %+v, want one row named orrery-a", notices)
	}
}
