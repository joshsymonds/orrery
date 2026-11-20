package route_test

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/joshsymonds/orrery/internal/deskstream"
	"github.com/joshsymonds/orrery/internal/notice"
	"github.com/joshsymonds/orrery/internal/push"
	"github.com/joshsymonds/orrery/internal/route"
	"github.com/joshsymonds/orrery/internal/store"
)

const (
	historyBlock    = "synthetic block"
	historyBlockRef = "synthetic block ref"
	altTitle        = "synthetic alternate title"
	altBody         = "synthetic alternate body"
)

// markCountingRows is the store a Router is attached to, which counts the MarkNoticeHistoryRead calls the Router makes.
type markCountingRows struct {
	*store.Store
	mu    sync.Mutex
	marks int
}

func (s *markCountingRows) MarkNoticeHistoryRead(
	ctx context.Context, key store.NoticeHistoryKey, at int64,
) (bool, error) {
	s.mu.Lock()
	s.marks++
	s.mu.Unlock()
	return s.Store.MarkNoticeHistoryRead(ctx, key, at)
}

// checkMarks asserts how many MarkNoticeHistoryRead calls s has counted.
func checkMarks(t *testing.T, s *markCountingRows, want int) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.marks != want {
		t.Errorf("mark notice history read calls = %d, want %d", s.marks, want)
	}
}

// historyNotice is a notice in pane whose desk notice is d and whose push builds.
func historyNotice(pane string, d deskstream.Notice) route.Notice {
	return route.Notice{
		Key:   notice.Key{Pane: pane},
		State: d.State,
		Desk:  func(context.Context) (deskstream.Notice, error) { return d, nil },
		Push:  func(context.Context) (push.Notification, error) { return phonePush(pane, d.State), nil },
	}
}

// historyRow is the row of the notice in pane whose desk notice is d, delivered at at and unread.
func historyRow(pane string, d deskstream.Notice, at int64) store.NoticeHistoryRow {
	return store.NoticeHistoryRow{
		Key:      store.NoticeHistoryKey{Pane: pane},
		State:    d.State,
		Title:    d.Title,
		Body:     d.Body,
		Block:    d.Block,
		BlockRef: d.BlockRef,
		Time:     at,
	}
}

// markedRead is row after it was read.
func markedRead(row store.NoticeHistoryRow) store.NoticeHistoryRow {
	row.Read = true
	return row
}

// withoutIDs returns rows with their ids cleared, so they compare with the rows a test wants.
func withoutIDs(rows []store.NoticeHistoryRow) []store.NoticeHistoryRow {
	out := slices.Clone(rows)
	for i := range out {
		out[i].ID = 0
	}
	return out
}

// checkHistory asserts the rows s holds, newest first, ids aside.
func checkHistory(t *testing.T, s *store.Store, want ...store.NoticeHistoryRow) {
	t.Helper()
	got, err := s.NoticeHistory(t.Context(), 0)
	if err != nil {
		t.Fatalf("list notice history: %v", err)
	}
	if !slices.Equal(withoutIDs(got), want) {
		t.Errorf("notice history = %+v, want %+v", withoutIDs(got), want)
	}
}

func TestNoticeHistoryRecordsDeliveredNotice(t *testing.T) {
	d := deskstream.Notice{
		State: stateWaiting, Title: testTitle, Body: testBody, Block: historyBlock, BlockRef: historyBlockRef,
	}

	t.Run("at the desk", func(t *testing.T) {
		ctx := t.Context()
		s := openStore(t, filepath.Join(t.TempDir(), "orrery.db"))
		f := newFake()
		f.setDesk(clockStart)
		r := newRestartRouter(t, f, nil)
		r.Attach(ctx, s)

		r.Notify(ctx, historyNotice(testPane, d))
		r.Wait()

		checkHistory(t, s, historyRow(testPane, d, clockStart))
	})

	t.Run("sent away", func(t *testing.T) {
		ctx := t.Context()
		s := openStore(t, filepath.Join(t.TempDir(), "orrery.db"))
		f := newFake()
		r := newRestartRouter(t, f, nil)
		r.Attach(ctx, s)

		r.Notify(ctx, historyNotice(testPane, d))
		r.Wait()

		checkHistory(t, s, historyRow(testPane, d, clockStart))
	})
}

func TestNoticeHistoryRecordsNoRowForUnwrittenNotices(t *testing.T) {
	t.Run("silent", func(t *testing.T) {
		ctx := t.Context()
		s := openStore(t, filepath.Join(t.TempDir(), "orrery.db"))
		f := newFake()
		f.setDesk(clockStart)
		r := newRestartRouter(t, f, nil)
		r.Attach(ctx, s)

		r.Notify(ctx, route.Notice{Key: notice.Key{Pane: testPane}, State: stateDone, Silent: quietReason})
		r.Wait()

		checkHistory(t, s)
	})

	t.Run("held while watched, then ended", func(t *testing.T) {
		ctx := t.Context()
		s := openStore(t, filepath.Join(t.TempDir(), "orrery.db"))
		f := newFake()
		f.setWatching(true, nil)
		r := newRestartRouter(t, f, nil)
		r.Attach(ctx, s)

		r.Notify(ctx, f.notice(testPane, stateWaiting, true))
		r.Wait()
		r.Changed(notice.Key{Pane: testPane})
		r.Wait()

		checkHistory(t, s)
	})

	t.Run("desk build fails", func(t *testing.T) {
		ctx := t.Context()
		s := openStore(t, filepath.Join(t.TempDir(), "orrery.db"))
		f := newFake()
		f.setDesk(clockStart)
		r := newRestartRouter(t, f, nil)
		r.Attach(ctx, s)

		n := f.notice(testPane, stateDone, false)
		n.Desk = func(context.Context) (deskstream.Notice, error) { return deskstream.Notice{}, errDeskBuild }
		r.Notify(ctx, n)
		r.Wait()

		checkHistory(t, s)
	})
}

func TestNoticeHistoryReplacesUnreadRowAndAddsAfterRead(t *testing.T) {
	ctx := t.Context()
	s := openStore(t, filepath.Join(t.TempDir(), "orrery.db"))
	f := newFake()
	r := newRestartRouter(t, f, nil)
	r.Attach(ctx, s)
	key := notice.Key{Pane: testPane}
	first := deskstream.Notice{State: stateWaiting, Title: testTitle, Body: testBody}
	second := deskstream.Notice{
		State: stateDone, Title: altTitle, Body: altBody, Block: historyBlock, BlockRef: historyBlockRef,
	}

	r.Notify(ctx, historyNotice(testPane, first))
	r.Wait()
	checkHistory(t, s, historyRow(testPane, first, clockStart))

	f.now = clockStart + 60
	r.Notify(ctx, historyNotice(testPane, second))
	r.Wait()
	checkHistory(t, s, historyRow(testPane, second, clockStart+60))

	r.Seen(ctx, key, clockStart+60)
	f.now = clockStart + 120
	r.Notify(ctx, historyNotice(testPane, first))
	r.Wait()
	checkHistory(t, s,
		historyRow(testPane, first, clockStart+120),
		markedRead(historyRow(testPane, second, clockStart+60)))
}

func TestNoticeHistorySeenMarksRowsAtOrBeforeRead(t *testing.T) {
	ctx := t.Context()
	s := openStore(t, filepath.Join(t.TempDir(), "orrery.db"))
	f := newFake()
	r := newRestartRouter(t, f, nil)
	r.Attach(ctx, s)
	key := notice.Key{Pane: testPane}
	d := deskstream.Notice{State: stateWaiting, Title: testTitle, Body: testBody}

	r.Notify(ctx, historyNotice(testPane, d))
	r.Wait()
	r.Seen(ctx, key, clockStart+5)
	checkHistory(t, s, markedRead(historyRow(testPane, d, clockStart)))

	f.now = clockStart + 60
	r.Notify(ctx, historyNotice(testPane, d))
	r.Wait()
	r.Seen(ctx, key, clockStart+30)
	checkHistory(t, s,
		historyRow(testPane, d, clockStart+60),
		markedRead(historyRow(testPane, d, clockStart)))

	r.Seen(ctx, key, clockStart+60)
	checkHistory(t, s,
		markedRead(historyRow(testPane, d, clockStart+60)),
		markedRead(historyRow(testPane, d, clockStart)))
}

func TestNoticeHistorySeenWithoutUnreadRowMakesNoMarkCall(t *testing.T) {
	ctx := t.Context()
	s := &markCountingRows{Store: openStore(t, filepath.Join(t.TempDir(), "orrery.db"))}
	f := newFake()
	r := newRestartRouter(t, f, nil)
	r.Attach(ctx, s)
	d := deskstream.Notice{State: stateWaiting, Title: testTitle, Body: testBody}

	r.Notify(ctx, historyNotice(testPane, d))
	r.Wait()
	r.Seen(ctx, notice.Key{Pane: testPane}, clockStart)
	checkMarks(t, s, 1)

	r.Seen(ctx, notice.Key{Pane: testPane}, clockStart+10)
	r.Seen(ctx, notice.Key{Pane: otherPane}, clockStart+10)
	checkMarks(t, s, 1)
}

func TestNoticeHistoryChangedLeavesRowUnread(t *testing.T) {
	ctx := t.Context()
	s := openStore(t, filepath.Join(t.TempDir(), "orrery.db"))
	f := newFake()
	f.setDesk(clockStart)
	r := newRestartRouter(t, f, nil)
	r.Attach(ctx, s)
	d := deskstream.Notice{State: stateWaiting, Title: testTitle, Body: testBody}

	r.Notify(ctx, historyNotice(testPane, d))
	r.Wait()
	checkHistory(t, s, historyRow(testPane, d, clockStart))

	r.Changed(notice.Key{Pane: testPane})
	r.Wait()
	checkHistory(t, s, historyRow(testPane, d, clockStart))
}

func TestNoticeHistoryReadAllLeavesNoRowUnread(t *testing.T) {
	ctx := t.Context()
	s := &markCountingRows{Store: openStore(t, filepath.Join(t.TempDir(), "orrery.db"))}
	f := newFake()
	r := newRestartRouter(t, f, nil)
	r.Attach(ctx, s)
	first := deskstream.Notice{State: stateWaiting, Title: testTitle, Body: testBody}
	second := deskstream.Notice{State: stateDone, Title: altTitle, Body: altBody}

	r.Notify(ctx, historyNotice(testPane, first))
	r.Wait()
	f.now = clockStart + 1
	r.Notify(ctx, historyNotice(otherPane, second))
	r.Wait()
	checkHistory(t, s.Store, historyRow(otherPane, second, clockStart+1), historyRow(testPane, first, clockStart))

	if err := r.ReadAll(ctx); err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	checkHistory(t, s.Store,
		markedRead(historyRow(otherPane, second, clockStart+1)),
		markedRead(historyRow(testPane, first, clockStart)))

	r.Seen(ctx, notice.Key{Pane: testPane}, clockStart+100)
	checkMarks(t, s, 0)
}

func TestNoticeHistoryHistoryReturnsRowsSinceNewestFirst(t *testing.T) {
	ctx := t.Context()
	s := openStore(t, filepath.Join(t.TempDir(), "orrery.db"))
	f := newFake()
	r := newRestartRouter(t, f, nil)
	r.Attach(ctx, s)
	d := deskstream.Notice{State: stateWaiting, Title: testTitle, Body: testBody}

	r.Notify(ctx, historyNotice(testPane, d))
	r.Wait()
	f.now = clockStart + 60
	r.Notify(ctx, historyNotice(otherPane, d))
	r.Wait()
	f.now = clockStart + 120
	r.Notify(ctx, historyNotice(thirdPane, d))
	r.Wait()

	got, err := r.History(ctx, clockStart+60)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	want := []store.NoticeHistoryRow{
		historyRow(thirdPane, d, clockStart+120),
		historyRow(otherPane, d, clockStart+60),
	}
	if !slices.Equal(withoutIDs(got), want) {
		t.Errorf("History = %+v, want %+v", withoutIDs(got), want)
	}
}

func TestNoticeHistoryNoStoreBeforeAttach(t *testing.T) {
	ctx := t.Context()
	f := newFake()
	r := newRestartRouter(t, f, nil)

	if err := r.ReadAll(ctx); !errors.Is(err, route.ErrNoHistory) {
		t.Errorf("ReadAll before Attach = %v, want %v", err, route.ErrNoHistory)
	}
	if _, err := r.History(ctx, 0); !errors.Is(err, route.ErrNoHistory) {
		t.Errorf("History before Attach = %v, want %v", err, route.ErrNoHistory)
	}
}

func TestNoticeHistorySeenOnNewRouterMarksRowLeftUnread(t *testing.T) {
	ctx := t.Context()
	s := openStore(t, filepath.Join(t.TempDir(), "orrery.db"))
	d := deskstream.Notice{State: stateWaiting, Title: testTitle, Body: testBody}

	first := newFake()
	r1 := newRestartRouter(t, first, nil)
	r1.Attach(ctx, s)
	r1.Notify(ctx, historyNotice(testPane, d))
	r1.Wait()
	first.now = clockStart + 1
	r1.Notify(ctx, historyNotice(otherPane, d))
	r1.Wait()
	r1.Seen(ctx, notice.Key{Pane: testPane}, clockStart)
	checkHistory(t, s, historyRow(otherPane, d, clockStart+1), markedRead(historyRow(testPane, d, clockStart)))
	r1.Close()

	second := fakeAt(clockStart + 2)
	r2 := newRestartRouter(t, second, nil)
	r2.Attach(ctx, s)
	r2.Seen(ctx, notice.Key{Pane: otherPane}, clockStart+1)
	checkHistory(t, s,
		markedRead(historyRow(otherPane, d, clockStart+1)),
		markedRead(historyRow(testPane, d, clockStart)))
}
