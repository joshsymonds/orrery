package store

import (
	"context"
	"reflect"
	"testing"
)

// TestNoticeHistoryMigration checks the migration that adds notice_history. Its Down drops the table and leaves the
// pending notices and the pushed rows as they were, and its Up restores an empty table with those rows unchanged. The
// seeded rows are written under the migration, between the Up and the Down, as a store that has run them would hold.
func TestNoticeHistoryMigration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, provider := storeAtVersion(t, 76)
	pane := NoticeHistoryKey{Pane: "%3", Session: "session-a"}
	for _, row := range []NoticeRow{
		{ID: 1, Pane: "%3", Session: "session-a", State: "waiting", Time: 1_700_000_000, Phase: "delayed",
			Due: 1_700_000_090_000, DelayServed: true, SourceKind: "session", Source: []byte(`{"event":"stop"}`)},
		{ID: 2, Limit: "weekly", State: "waiting", Time: 1_700_000_010, Phase: "held"},
	} {
		if err := s.PutNotice(ctx, row); err != nil {
			t.Fatalf("PutNotice %d: %v", row.ID, err)
		}
	}
	for _, row := range []PushedRow{
		{Pane: "%3", Session: "session-a", Time: 1_700_000_000},
		{Alert: "usage-warning", Time: 1_700_000_020},
	} {
		if err := s.PutPushed(ctx, row); err != nil {
			t.Fatalf("PutPushed %+v: %v", row, err)
		}
	}
	if err := s.RecordNoticeHistory(ctx, NoticeHistoryRow{
		Key: pane, State: "waiting", Title: "Claude is waiting", Body: "the session has been idle",
		Block: "step-7", BlockRef: "orrery-0123456/epic-one", Time: 1_700_000_000,
	}); err != nil {
		t.Fatalf("RecordNoticeHistory: %v", err)
	}
	notices, err := s.Notices(ctx)
	if err != nil {
		t.Fatalf("Notices before Down: %v", err)
	}
	pushed, err := s.Pushed(ctx)
	if err != nil {
		t.Fatalf("Pushed before Down: %v", err)
	}

	if _, err := provider.Down(ctx); err != nil {
		t.Fatalf("Down of 00076: %v", err)
	}
	if tableNames(t, s)["notice_history"] {
		t.Errorf("notice_history after Down: present, want absent")
	}
	if got, err := s.Notices(ctx); err != nil || !reflect.DeepEqual(got, notices) {
		t.Errorf("pending notices after Down = %+v (err %v), want %+v", got, err, notices)
	}
	if got, err := s.Pushed(ctx); err != nil || !reflect.DeepEqual(got, pushed) {
		t.Errorf("pushed rows after Down = %+v (err %v), want %+v", got, err, pushed)
	}

	if _, err := provider.UpByOne(ctx); err != nil {
		t.Fatalf("Up of 00076: %v", err)
	}
	if !tableNames(t, s)["notice_history"] {
		t.Fatalf("notice_history after Up: absent, want present")
	}
	if got := rowsOf(t, s.db.DB, "notice_history"); len(got) != 0 {
		t.Errorf("notice_history after Up = %v, want none", got)
	}
	if got, err := s.Notices(ctx); err != nil || !reflect.DeepEqual(got, notices) {
		t.Errorf("pending notices after Up = %+v (err %v), want %+v", got, err, notices)
	}
	if got, err := s.Pushed(ctx); err != nil || !reflect.DeepEqual(got, pushed) {
		t.Errorf("pushed rows after Up = %+v (err %v), want %+v", got, err, pushed)
	}
}
