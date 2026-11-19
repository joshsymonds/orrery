package store

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
)

// recordNoticeHistory records row and fails the test when the write fails.
func recordNoticeHistory(t *testing.T, s *Store, row NoticeHistoryRow) {
	t.Helper()
	if err := s.RecordNoticeHistory(context.Background(), row); err != nil {
		t.Fatalf("RecordNoticeHistory %+v: %v", row.Key, err)
	}
}

// markNoticeHistoryRead marks key's rows read at or before at, and returns whether key stays unread.
func markNoticeHistoryRead(t *testing.T, s *Store, key NoticeHistoryKey, at int64) bool {
	t.Helper()
	unread, err := s.MarkNoticeHistoryRead(context.Background(), key, at)
	if err != nil {
		t.Fatalf("MarkNoticeHistoryRead %+v at %d: %v", key, at, err)
	}
	return unread
}

// noticeHistoryRows returns every row of s, newest first.
func noticeHistoryRows(t *testing.T, s *Store) []NoticeHistoryRow {
	t.Helper()
	rows, err := s.NoticeHistory(context.Background(), 0)
	if err != nil {
		t.Fatalf("NoticeHistory: %v", err)
	}
	return rows
}

func TestNoticeHistoryRecordInsertsARow(t *testing.T) {
	t.Parallel()
	s := openSyncStore(t)
	row := NoticeHistoryRow{
		Key:   NoticeHistoryKey{Pane: "%3", Session: "session-a"},
		State: "waiting", Title: "Claude is waiting", Body: "the session has been idle",
		Block: "step-7", BlockRef: "orrery-0123456/epic-one", Time: 1_700_000_100,
	}
	recordNoticeHistory(t, s, row)
	want := row
	want.ID = 1
	if got := noticeHistoryRows(t, s); !reflect.DeepEqual(got, []NoticeHistoryRow{want}) {
		t.Fatalf("NoticeHistory after one Record = %+v, want %+v", got, []NoticeHistoryRow{want})
	}
}

func TestNoticeHistoryRecordReplacesTheNewestUnreadRow(t *testing.T) {
	t.Parallel()
	s := openSyncStore(t)
	key := NoticeHistoryKey{Pane: "%3", Session: "session-a"}
	recordNoticeHistory(t, s, NoticeHistoryRow{
		Key: key, State: "waiting", Title: "first", Body: "first body", Block: "step-1", BlockRef: "ref-1",
		Time: 1_700_000_100,
	})
	replacement := NoticeHistoryRow{
		Key: key, State: "done", Title: "second", Body: "second body", Block: "step-2", BlockRef: "ref-2",
		Time: 1_700_000_200,
	}
	recordNoticeHistory(t, s, replacement)
	// The second Record replaces the first row, which kept its id, so one row holds the second notice's text and time.
	want := replacement
	want.ID = 1
	if got := noticeHistoryRows(t, s); !reflect.DeepEqual(got, []NoticeHistoryRow{want}) {
		t.Fatalf("NoticeHistory after two Records of one unread key = %+v, want %+v", got, []NoticeHistoryRow{want})
	}
}

func TestNoticeHistoryRecordAddsARowAfterTheNewestIsRead(t *testing.T) {
	t.Parallel()
	s := openSyncStore(t)
	key := NoticeHistoryKey{Pane: "%3", Session: "session-a"}
	first := NoticeHistoryRow{
		Key: key, State: "waiting", Title: "first", Body: "first body", Block: "step-1", BlockRef: "ref-1",
		Time: 1_700_000_100,
	}
	recordNoticeHistory(t, s, first)
	if unread := markNoticeHistoryRead(t, s, key, first.Time); unread {
		t.Fatalf("key unread after its only row was read = true, want false")
	}
	second := NoticeHistoryRow{
		Key: key, State: "done", Title: "second", Body: "second body", Block: "step-2", BlockRef: "ref-2",
		Time: 1_700_000_200,
	}
	recordNoticeHistory(t, s, second)
	firstRead := first
	firstRead.ID = 1
	firstRead.Read = true
	wantSecond := second
	wantSecond.ID = 2
	want := []NoticeHistoryRow{wantSecond, firstRead}
	if got := noticeHistoryRows(t, s); !reflect.DeepEqual(got, want) {
		t.Fatalf("NoticeHistory after a Record past a read row = %+v, want %+v", got, want)
	}
}

func TestNoticeHistoryMarkReadMarksOnlyRowsAtOrBeforeTheTime(t *testing.T) {
	t.Parallel()
	s := openSyncStore(t)
	pane := NoticeHistoryKey{Pane: "%3", Session: "session-a"}
	limit := NoticeHistoryKey{Limit: "weekly"}
	paneAt100 := NoticeHistoryRow{Key: pane, State: "waiting", Title: "pane at 100", Body: "b", Time: 100}
	recordNoticeHistory(t, s, paneAt100)
	// The row's time is the boundary: a read at exactly that time marks the row read, and nothing is left unread.
	if unread := markNoticeHistoryRead(t, s, pane, 100); unread {
		t.Fatalf("pane unread after reading at its row's time = true, want false")
	}
	paneAt200 := NoticeHistoryRow{Key: pane, State: "waiting", Title: "pane at 200", Body: "b", Time: 200}
	recordNoticeHistory(t, s, paneAt200)
	limitAt120 := NoticeHistoryRow{Key: limit, State: "waiting", Title: "limit at 120", Body: "b", Time: 120}
	recordNoticeHistory(t, s, limitAt120)

	// The pane's row at 200 is later than the read time 150, so it stays unread, and the report says so. The limit's
	// row is at 120, before 150, but it is another key's row, so it stays unread too.
	if unread := markNoticeHistoryRead(t, s, pane, 150); !unread {
		t.Fatalf("pane unread after reading at 150 = false, want true: its row at 200 is later")
	}
	paneAt200Want := paneAt200
	paneAt200Want.ID = 2
	limitAt120Want := limitAt120
	limitAt120Want.ID = 3
	paneAt100Want := paneAt100
	paneAt100Want.ID = 1
	paneAt100Want.Read = true
	want := []NoticeHistoryRow{paneAt200Want, limitAt120Want, paneAt100Want}
	if got := noticeHistoryRows(t, s); !reflect.DeepEqual(got, want) {
		t.Fatalf("NoticeHistory after reading the pane at 150 = %+v, want %+v", got, want)
	}

	if unread := markNoticeHistoryRead(t, s, pane, 200); unread {
		t.Fatalf("pane unread after reading at 200 = true, want false")
	}
	paneAt200Want.Read = true
	want = []NoticeHistoryRow{paneAt200Want, limitAt120Want, paneAt100Want}
	if got := noticeHistoryRows(t, s); !reflect.DeepEqual(got, want) {
		t.Fatalf("NoticeHistory after reading the pane at 200 = %+v, want %+v", got, want)
	}
}

func TestNoticeHistoryReadAllLeavesNoRowUnread(t *testing.T) {
	t.Parallel()
	s := openSyncStore(t)
	pane := NoticeHistoryKey{Pane: "%3", Session: "session-a"}
	limit := NoticeHistoryKey{Limit: "weekly"}
	alert := NoticeHistoryKey{Alert: "usage-warning"}
	recordNoticeHistory(t, s, NoticeHistoryRow{Key: pane, State: "waiting", Title: "a", Body: "a", Time: 100})
	markNoticeHistoryRead(t, s, pane, 100)
	recordNoticeHistory(t, s, NoticeHistoryRow{Key: pane, State: "waiting", Title: "b", Body: "b", Time: 200})
	recordNoticeHistory(t, s, NoticeHistoryRow{Key: limit, State: "waiting", Title: "c", Body: "c", Time: 120})
	recordNoticeHistory(t, s, NoticeHistoryRow{Key: alert, State: "waiting", Title: "d", Body: "d", Time: 130})

	if err := s.ReadAllNoticeHistory(context.Background()); err != nil {
		t.Fatalf("ReadAllNoticeHistory: %v", err)
	}
	rows := noticeHistoryRows(t, s)
	if len(rows) != 4 {
		t.Fatalf("NoticeHistory after ReadAll = %d rows, want 4", len(rows))
	}
	for _, row := range rows {
		if !row.Read {
			t.Errorf("row %d (%+v) unread after ReadAll, want read", row.ID, row.Key)
		}
	}
}

func TestNoticeHistorySinceWindowAndNewestFirst(t *testing.T) {
	t.Parallel()
	s := openSyncStore(t)
	paneOld := NoticeHistoryKey{Pane: "%3", Session: "session-a"}
	paneNew := NoticeHistoryKey{Pane: "%9", Session: "session-b"}
	epic := NoticeHistoryKey{Epic: "orrery-0123456/epic-one"}
	limit := NoticeHistoryKey{Limit: "weekly"}
	alert := NoticeHistoryKey{Alert: "usage-warning"}
	// Recorded in this order, so the ids are 1 to 5 in the order of the rows below.
	rowA := NoticeHistoryRow{Key: paneOld, State: "waiting", Title: "a", Body: "a", Time: 100}
	rowD := NoticeHistoryRow{Key: epic, State: "waiting", Title: "d", Body: "d", Time: 50}
	rowC := NoticeHistoryRow{Key: alert, State: "waiting", Title: "c", Body: "c", Time: 200}
	rowB := NoticeHistoryRow{Key: limit, State: "waiting", Title: "b", Body: "b", Time: 300}
	rowE := NoticeHistoryRow{Key: paneNew, State: "waiting", Title: "e", Body: "e", Time: 300}
	for _, row := range []NoticeHistoryRow{rowA, rowD, rowC, rowB, rowE} {
		recordNoticeHistory(t, s, row)
	}

	withID := func(row NoticeHistoryRow, id int64) NoticeHistoryRow {
		row.ID = id
		return row
	}
	// The row at 50 is before since and left out. The row at 100 is at since and kept. The two rows at 300 are newest
	// first, and the later-recorded one, which has the higher id, comes first.
	got, err := s.NoticeHistory(context.Background(), 100)
	if err != nil {
		t.Fatalf("NoticeHistory since 100: %v", err)
	}
	want := []NoticeHistoryRow{withID(rowE, 5), withID(rowB, 4), withID(rowC, 3), withID(rowA, 1)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NoticeHistory since 100 = %+v, want %+v", got, want)
	}
}

func TestNoticeHistoryUnreadKeysListEachKeyOnce(t *testing.T) {
	t.Parallel()
	s := openSyncStore(t)
	pane := NoticeHistoryKey{Pane: "%3", Session: "session-a"}
	epic := NoticeHistoryKey{Epic: "orrery-0123456/epic-one"}
	limit := NoticeHistoryKey{Limit: "weekly"}
	alert := NoticeHistoryKey{Alert: "usage-warning"}
	// The pane and the epic each record twice while unread, so each keeps one row.
	recordNoticeHistory(t, s, NoticeHistoryRow{Key: pane, State: "waiting", Title: "a", Body: "a", Time: 100})
	recordNoticeHistory(t, s, NoticeHistoryRow{Key: pane, State: "waiting", Title: "a2", Body: "a2", Time: 150})
	recordNoticeHistory(t, s, NoticeHistoryRow{Key: epic, State: "waiting", Title: "e", Body: "e", Time: 140})
	recordNoticeHistory(t, s, NoticeHistoryRow{Key: epic, State: "waiting", Title: "e2", Body: "e2", Time: 160})
	// The limit's only row is read, so the limit is not listed.
	recordNoticeHistory(t, s, NoticeHistoryRow{Key: limit, State: "waiting", Title: "l", Body: "l", Time: 120})
	markNoticeHistoryRead(t, s, limit, 120)
	recordNoticeHistory(t, s, NoticeHistoryRow{Key: alert, State: "waiting", Title: "al", Body: "al", Time: 130})

	got, err := s.UnreadNoticeHistoryKeys(context.Background())
	if err != nil {
		t.Fatalf("UnreadNoticeHistoryKeys: %v", err)
	}
	// Keys are listed in the order of their pane, session, epic, limit and alert.
	want := []NoticeHistoryKey{alert, epic, pane}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("UnreadNoticeHistoryKeys = %+v, want %+v", got, want)
	}
}

func TestNoticeHistoryReopenKeepsReadMarks(t *testing.T) {
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
	pane := NoticeHistoryKey{Pane: "%3", Session: "session-a"}
	limit := NoticeHistoryKey{Limit: "weekly"}
	paneRow := NoticeHistoryRow{Key: pane, State: "waiting", Title: "pane", Body: "pane body", Time: 100}
	limitRow := NoticeHistoryRow{Key: limit, State: "waiting", Title: "limit", Body: "limit body", Time: 120}
	recordNoticeHistory(t, s, paneRow)
	recordNoticeHistory(t, s, limitRow)
	markNoticeHistoryRead(t, s, pane, 100)

	if err := s.Close(); err != nil {
		t.Fatalf("Close before reopen: %v", err)
	}
	reopened, err := Open(ctx, path, noSync())
	if err != nil {
		t.Fatalf("Open after close: %v", err)
	}
	s = reopened

	paneWant := paneRow
	paneWant.ID = 1
	paneWant.Read = true
	limitWant := limitRow
	limitWant.ID = 2
	if got := noticeHistoryRows(t, s); !reflect.DeepEqual(got, []NoticeHistoryRow{limitWant, paneWant}) {
		t.Fatalf("NoticeHistory after reopen = %+v, want %+v", got, []NoticeHistoryRow{limitWant, paneWant})
	}
	got, err := s.UnreadNoticeHistoryKeys(ctx)
	if err != nil {
		t.Fatalf("UnreadNoticeHistoryKeys after reopen: %v", err)
	}
	if want := []NoticeHistoryKey{limit}; !reflect.DeepEqual(got, want) {
		t.Fatalf("UnreadNoticeHistoryKeys after reopen = %+v, want %+v", got, want)
	}
}
