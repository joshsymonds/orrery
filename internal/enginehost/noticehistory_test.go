package enginehost

import (
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/inapp"
	"github.com/joshsymonds/orrery/internal/notice"
	"github.com/joshsymonds/orrery/internal/route"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/storetest"
)

// secondsPerDay is a day in unix seconds, the unit the history's times are counted in.
const secondsPerDay = 24 * 60 * 60

// openHistoryStore seeds the store at path with the migrated image and opens it. The caller closes it.
func openHistoryStore(t *testing.T, path string) *store.Store {
	t.Helper()
	storetest.Seed(t, path)
	st, err := store.Open(t.Context(), path, storetest.NoSync())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	return st
}

// historyRouter returns a router whose clock is noticeNow and that is not yet attached to a store. It routes no notice,
// so its phone and log are never written to. Its store-call bound never fires, so each history read and write lands
// however long it takes. The caller closes it.
func historyRouter() *route.Router {
	return route.New(route.Config{
		Now:        noticeNow,
		Phone:      notice.New(sessionTestHost, &fakeSender{}, noticeNow, slog.New(slog.DiscardHandler)),
		Log:        &fakeRouteLog{},
		StoreAfter: func(time.Duration, func()) func() bool { return func() bool { return true } },
	})
}

// seedNoticeHistory records the notice history the tests list into st, before any router is attached. Its times are set
// from noticeNow: one row 15 days before it, outside the 14-day window, and four rows within the window under keys of
// each kind, one of which opens a block. The rows are recorded out of time order.
func seedNoticeHistory(t *testing.T, st *store.Store) {
	t.Helper()
	now := noticeNow().Unix()
	rows := []store.NoticeHistoryRow{
		{
			Key:   store.NoticeHistoryKey{Pane: "%1"},
			State: "done", Title: "synthetic old title", Body: "synthetic old body",
			Time: now - 15*secondsPerDay,
		},
		{
			Key:   store.NoticeHistoryKey{Epic: "synthetic-epic"},
			State: "waiting", Title: "synthetic epic title", Body: "synthetic epic body",
			Block: "release_step", BlockRef: "2", Time: now - secondsPerDay,
		},
		{
			Key:   store.NoticeHistoryKey{Pane: "%3"},
			State: "waiting", Title: "synthetic pane title", Body: "synthetic pane body",
			Block: "question", BlockRef: "Q3", Time: now - 3600,
		},
		{
			Key:   store.NoticeHistoryKey{Limit: "usage@1700100000"},
			State: "limited", Title: "synthetic limit title", Body: "synthetic limit body",
			Time: now - 13*secondsPerDay,
		},
		{
			Key:   store.NoticeHistoryKey{Session: "0123456789abcdef0123456789abcdef"},
			State: "done", Title: "synthetic session title", Body: "synthetic session body",
			Time: now - 7200,
		},
	}
	for _, row := range rows {
		if err := st.RecordNoticeHistory(t.Context(), row); err != nil {
			t.Fatalf("RecordNoticeHistory of %+v: %v", row.Key, err)
		}
	}
}

// wantHistoryWindow returns the notices List answers for the history seedNoticeHistory records: the four within the
// window, newest first, each with the read mark read reports for its key.
func wantHistoryWindow(read func(*v1.NoticeKey) bool) []*v1.ListedNotice {
	now := noticeNow().Unix()
	notices := []*v1.ListedNotice{
		{
			Key:   &v1.NoticeKey{Pane: "%3"},
			State: "waiting", Title: "synthetic pane title", Body: "synthetic pane body",
			Block: &v1.NoticeBlock{Kind: "question", Ref: "Q3"}, Time: now - 3600,
		},
		{
			Key:   &v1.NoticeKey{Session: "0123456789abcdef0123456789abcdef"},
			State: "done", Title: "synthetic session title", Body: "synthetic session body",
			Time: now - 7200,
		},
		{
			Key:   &v1.NoticeKey{Epic: "synthetic-epic"},
			State: "waiting", Title: "synthetic epic title", Body: "synthetic epic body",
			Block: &v1.NoticeBlock{Kind: "release_step", Ref: "2"}, Time: now - secondsPerDay,
		},
		{
			Key:   &v1.NoticeKey{Limit: "usage@1700100000"},
			State: "limited", Title: "synthetic limit title", Body: "synthetic limit body",
			Time: now - 13*secondsPerDay,
		},
	}
	for _, n := range notices {
		n.Read = read(n.Key)
	}
	return notices
}

// noRowRead is the read mark of a history with no row read.
func noRowRead(*v1.NoticeKey) bool { return false }

// allRowsRead is the read mark of a history with every row read.
func allRowsRead(*v1.NoticeKey) bool { return true }

// onlyRowsOf returns the read mark of a history whose rows under key alone are read.
func onlyRowsOf(key *v1.NoticeKey) func(*v1.NoticeKey) bool {
	return func(k *v1.NoticeKey) bool { return proto.Equal(k, key) }
}

// listNotices calls List on client and returns its response. It fails the test when the call fails.
func listNotices(t *testing.T, client enginev1connect.NoticesClient) *v1.NoticesListResponse {
	t.Helper()
	res, err := client.List(t.Context(), connect.NewRequest(&v1.NoticesListRequest{}))
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	return res.Msg
}

func TestNoticeHistoryListReturnsWindowNewestFirst(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	st := openHistoryStore(t, filepath.Join(t.TempDir(), "orrery.db"))
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	seedNoticeHistory(t, st)
	router := historyRouter()
	t.Cleanup(router.Close)
	router.Attach(ctx, st)
	client := startNoticesServer(t, NewPageNotices(inapp.NewHub(), router, router, noticeNow))

	got := listNotices(t, client)
	want := &v1.NoticesListResponse{Notices: wantHistoryWindow(noRowRead)}
	if !proto.Equal(got, want) {
		t.Fatalf("List = %v, want %v", got, want)
	}
}

func TestNoticeHistorySeenMarksKeyRead(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	st := openHistoryStore(t, filepath.Join(t.TempDir(), "orrery.db"))
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	seedNoticeHistory(t, st)
	router := historyRouter()
	t.Cleanup(router.Close)
	router.Attach(ctx, st)
	client := startNoticesServer(t, NewPageNotices(inapp.NewHub(), router, router, noticeNow))

	paneKey := &v1.NoticeKey{Pane: "%3"}
	if _, err := client.Seen(ctx, connect.NewRequest(&v1.NoticesSeenRequest{Key: paneKey})); err != nil {
		t.Fatalf("Seen: %v", err)
	}

	got := listNotices(t, client)
	want := &v1.NoticesListResponse{Notices: wantHistoryWindow(onlyRowsOf(paneKey))}
	if !proto.Equal(got, want) {
		t.Fatalf("List after Seen = %v, want %v", got, want)
	}
}

func TestNoticeHistoryReadAllMarksEveryRowRead(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	st := openHistoryStore(t, filepath.Join(t.TempDir(), "orrery.db"))
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	seedNoticeHistory(t, st)
	router := historyRouter()
	t.Cleanup(router.Close)
	router.Attach(ctx, st)
	client := startNoticesServer(t, NewPageNotices(inapp.NewHub(), router, router, noticeNow))

	if _, err := client.ReadAll(ctx, connect.NewRequest(&v1.NoticesReadAllRequest{})); err != nil {
		t.Fatalf("ReadAll: %v", err)
	}

	got := listNotices(t, client)
	want := &v1.NoticesListResponse{Notices: wantHistoryWindow(allRowsRead)}
	if !proto.Equal(got, want) {
		t.Fatalf("List after ReadAll = %v, want %v", got, want)
	}
}

func TestNoticeHistorySurvivesRouterRestart(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "orrery.db")
	first := openHistoryStore(t, path)
	seedNoticeHistory(t, first)
	router := historyRouter()
	router.Attach(ctx, first)
	client := startNoticesServer(t, NewPageNotices(inapp.NewHub(), router, router, noticeNow))
	paneKey := &v1.NoticeKey{Pane: "%3"}
	if _, err := client.Seen(ctx, connect.NewRequest(&v1.NoticesSeenRequest{Key: paneKey})); err != nil {
		t.Fatalf("Seen: %v", err)
	}
	router.Close()
	if err := first.Close(); err != nil {
		t.Fatalf("close store before the restart: %v", err)
	}

	second, err := store.Open(ctx, path, storetest.NoSync())
	if err != nil {
		t.Fatalf("reopen store after the restart: %v", err)
	}
	t.Cleanup(func() {
		if err := second.Close(); err != nil {
			t.Errorf("close reopened store: %v", err)
		}
	})
	restarted := historyRouter()
	t.Cleanup(restarted.Close)
	restarted.Attach(ctx, second)
	restartedClient := startNoticesServer(t, NewPageNotices(inapp.NewHub(), restarted, restarted, noticeNow))

	got := listNotices(t, restartedClient)
	want := &v1.NoticesListResponse{Notices: wantHistoryWindow(onlyRowsOf(paneKey))}
	if !proto.Equal(got, want) {
		t.Fatalf("List after the restart = %v, want %v", got, want)
	}
}

func TestNoticeHistoryReadAllSurvivesRouterRestart(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "orrery.db")
	first := openHistoryStore(t, path)
	seedNoticeHistory(t, first)
	router := historyRouter()
	router.Attach(ctx, first)
	client := startNoticesServer(t, NewPageNotices(inapp.NewHub(), router, router, noticeNow))
	if _, err := client.ReadAll(ctx, connect.NewRequest(&v1.NoticesReadAllRequest{})); err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	router.Close()
	if err := first.Close(); err != nil {
		t.Fatalf("close store before the restart: %v", err)
	}

	second, err := store.Open(ctx, path, storetest.NoSync())
	if err != nil {
		t.Fatalf("reopen store after the restart: %v", err)
	}
	t.Cleanup(func() {
		if err := second.Close(); err != nil {
			t.Errorf("close reopened store: %v", err)
		}
	})
	restarted := historyRouter()
	t.Cleanup(restarted.Close)
	restarted.Attach(ctx, second)
	restartedClient := startNoticesServer(t, NewPageNotices(inapp.NewHub(), restarted, restarted, noticeNow))

	got := listNotices(t, restartedClient)
	want := &v1.NoticesListResponse{Notices: wantHistoryWindow(allRowsRead)}
	if !proto.Equal(got, want) {
		t.Fatalf("List after ReadAll and the restart = %v, want %v", got, want)
	}
}

func TestNoticeHistoryUnavailableWithoutHistory(t *testing.T) {
	t.Parallel()
	unattached := historyRouter()
	t.Cleanup(unattached.Close)
	tests := []struct {
		name    string
		history NoticeHistory
	}{
		{name: "nil history", history: nil},
		{name: "router never attached", history: unattached},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			client := startNoticesServer(t, NewPageNotices(inapp.NewHub(), &fakeRouter{}, tc.history, noticeNow))

			ctx := t.Context()
			_, err := client.List(ctx, connect.NewRequest(&v1.NoticesListRequest{}))
			if code := connect.CodeOf(err); code != connect.CodeUnavailable {
				t.Fatalf("List code = %v (err %v), want unavailable", code, err)
			}
			_, err = client.ReadAll(ctx, connect.NewRequest(&v1.NoticesReadAllRequest{}))
			if code := connect.CodeOf(err); code != connect.CodeUnavailable {
				t.Fatalf("ReadAll code = %v (err %v), want unavailable", code, err)
			}
		})
	}
}
