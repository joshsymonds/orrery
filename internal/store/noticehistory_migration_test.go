package store

import (
	"context"
	"reflect"
	"testing"
)

// TestNoticeHistoryMigration checks the migration that adds notice_history. Its Down drops the table and leaves the
// pending notices and the pushed rows as they were, and its Up restores an empty table with those rows unchanged. The
// seeded rows are written with SQL at version 76, before 00104_notice_host.sql adds the host column the store's own
// writes and reads of the pending and pushed tables name, so each table is compared as rowsOf reads it.
func TestNoticeHistoryMigration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, provider := storeAtVersion(t, 76)
	for _, seed := range []struct {
		stmt string
		args []any
	}{
		{`INSERT INTO pending_notices
			(id, pane, session, state, time, phase, due, delay_served, watch_logged, source_kind, source)
			VALUES (1, '%3', 'session-a', 'waiting', 1700000000, 'delayed', 1700000090000, 1, 0, 'session', ?)`,
			[]any{[]byte(`{"event":"stop"}`)}},
		{`INSERT INTO pending_notices
			(id, pane, session, state, time, phase, due, delay_served, watch_logged, source_kind, limit_key)
			VALUES (2, '', '', 'waiting', 1700000010, 'held', 0, 0, 0, '', 'weekly')`, nil},
		{`INSERT INTO pushed_notices (pane, session, time) VALUES ('%3', 'session-a', 1700000000)`, nil},
		{`INSERT INTO pushed_alert_notices (alert, time) VALUES ('usage-warning', 1700000020)`, nil},
		{`INSERT INTO notice_history
			(pane, session, state, title, body, block, block_ref, time)
			VALUES ('%3', 'session-a', 'waiting', 'Claude is waiting', 'the session has been idle', 'step-7',
				'orrery-0123456/epic-one', 1700000000)`, nil},
	} {
		if _, err := s.db.DB.ExecContext(ctx, seed.stmt, seed.args...); err != nil {
			t.Fatalf("seed %s: %v", seed.stmt, err)
		}
	}
	// noticeRows reads the pending notices and the pushed rows, each table as rowsOf reads it.
	noticeRows := func() map[string][][]any {
		return map[string][][]any{
			"pending_notices":      rowsOf(t, s.db.DB, "pending_notices"),
			"pushed_notices":       rowsOf(t, s.db.DB, "pushed_notices"),
			"pushed_alert_notices": rowsOf(t, s.db.DB, "pushed_alert_notices"),
		}
	}
	before := noticeRows()

	if _, err := provider.Down(ctx); err != nil {
		t.Fatalf("Down of 00076: %v", err)
	}
	if tableNames(t, s)["notice_history"] {
		t.Errorf("notice_history after Down: present, want absent")
	}
	if got := noticeRows(); !reflect.DeepEqual(got, before) {
		t.Errorf("notice rows after Down = %v, want %v", got, before)
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
	if got := noticeRows(); !reflect.DeepEqual(got, before) {
		t.Errorf("notice rows after Up = %v, want %v", got, before)
	}
}
