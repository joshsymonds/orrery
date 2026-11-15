package route_test

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/deskstream"
	"github.com/joshsymonds/orrery/internal/notice"
	"github.com/joshsymonds/orrery/internal/push"
	"github.com/joshsymonds/orrery/internal/route"
	"github.com/joshsymonds/orrery/internal/store"
)

var errDeskBuild = errors.New("synthetic desk build failure")

// deskRowStored records a desk notice in testPane at clockStart over a new store, then restarts: it returns the store
// reopened, holding the row that notice left.
func deskRowStored(t *testing.T) *store.Store {
	t.Helper()
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openStore(t, path)
	first := newFake()
	first.setDesk(clockStart)
	r1 := newRestoreRouter(first, nil, nil)
	r1.Attach(ctx, s)
	r1.Notify(ctx, sourced(first.notice(testPane, stateWaiting, false)))
	r1.Wait()
	first.check(t, []route.Line{lineOf(testPane, clockStart, stateWaiting, route.Desk, "at the desk")},
		[]deskstream.Notice{deskPost(stateWaiting)}, nil)
	return reopenStore(t, s, path)
}

// newPagedRestoreRouter returns a Router wired to f and pages that restores stored notices with rebuilds. It is closed
// when the test ends, so the caller attaches the store after reopening it.
func newPagedRestoreRouter(t *testing.T, f *fake, pages *pageSink, rebuilds map[string]route.Rebuild) *route.Router {
	t.Helper()
	r := route.New(route.Config{
		Host:       testHost,
		Now:        f.Now,
		After:      f.After,
		Desk:       f,
		Phone:      f,
		Subscribed: f.Subscribed,
		Log:        f,
		Pages:      pages,
		Rebuild:    rebuilds,
	})
	t.Cleanup(r.Close)
	return r
}

func TestRestoredDeskNoticeShowsOnPages(t *testing.T) {
	t.Parallel()
	reopened := deskRowStored(t)
	f := fakeAt(clockStart + 60)
	pages := &pageSink{}
	r := newPagedRestoreRouter(t, f, pages, map[string]route.Rebuild{
		testSourceKind: rebuildAs(f.notice(testPane, stateWaiting, false)),
	})
	r.Attach(t.Context(), reopened)
	r.Wait()

	f.check(t, nil, nil, nil)
	f.checkActiveTimers(t, 1)
	pages.check(t, showOf(notice.Key{Pane: testPane}, deskPost(stateWaiting)))
}

func TestRestoredDeskNoticeEndsOnceWhenItEnds(t *testing.T) {
	t.Parallel()
	key := notice.Key{Pane: testPane}
	cases := []struct {
		name string
		// end ends the restored notice, which is shown for testPane.
		end        func(t *testing.T, f *fake, r *route.Router)
		wantLines  []route.Line
		wantPushes []push.Notification
	}{
		{
			name: "seen",
			end: func(t *testing.T, _ *fake, r *route.Router) {
				t.Helper()
				r.Seen(t.Context(), key, clockStart)
			},
			wantLines: []route.Line{lineOf(testPane, clockStart+60, stateWaiting, route.None, "seen")},
		},
		{
			name: "escalated to the phone",
			end: func(t *testing.T, f *fake, _ *route.Router) {
				t.Helper()
				f.fire(t, seconds(escalationSeconds-60))
			},
			wantLines: []route.Line{
				lineOf(testPane, clockStart+escalationSeconds, stateWaiting, route.Phone, "unseen after 5 minutes"),
			},
			wantPushes: []push.Notification{phonePush(testPane, stateWaiting)},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			reopened := deskRowStored(t)
			f := fakeAt(clockStart + 60)
			pages := &pageSink{}
			r := newPagedRestoreRouter(t, f, pages, map[string]route.Rebuild{
				testSourceKind: rebuildAs(f.notice(testPane, stateWaiting, false)),
			})
			r.Attach(t.Context(), reopened)
			r.Wait()
			pages.check(t, showOf(key, deskPost(stateWaiting)))

			tc.end(t, f, r)
			r.Wait()
			f.check(t, tc.wantLines, nil, tc.wantPushes)
			pages.check(t, showOf(key, deskPost(stateWaiting)), endOf(key))
		})
	}
}

func TestRestoredHeldAndDelayedNoticesAreNotShown(t *testing.T) {
	t.Parallel()
	t.Run("held", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		path := filepath.Join(t.TempDir(), "orrery.db")
		s := openStore(t, path)
		first := newFake()
		first.setWatching(true, nil)
		r1 := newRestoreRouter(first, nil, nil)
		r1.Attach(ctx, s)
		r1.Notify(ctx, sourced(first.notice(testPane, stateWaiting, true)))
		r1.Wait()
		reopened := reopenStore(t, s, path)

		f := newFake()
		f.setWatching(true, nil)
		pages := &pageSink{}
		r := newPagedRestoreRouter(t, f, pages, map[string]route.Rebuild{
			testSourceKind: rebuildAs(f.notice(testPane, stateWaiting, true)),
		})
		r.Attach(ctx, reopened)
		r.Wait()

		f.checkActiveTimers(t, 1)
		pages.check(t)
	})
	t.Run("delayed", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		path := filepath.Join(t.TempDir(), "orrery.db")
		s := openStore(t, path)
		first := newFake()
		c := &canned{verdict: route.Verdict{Needed: true}}
		r1 := newRestoreRouter(first, nil, nil)
		r1.Attach(ctx, s)
		n := first.notice(testPane, stateWaiting, false)
		n.Judge = c.judge
		r1.Notify(ctx, sourced(n))
		r1.Wait()
		reopened := reopenStore(t, s, path)

		f := fakeAt(clockStart + 4)
		c2 := &canned{verdict: route.Verdict{Needed: true}}
		rebuilt := f.notice(testPane, stateWaiting, false)
		rebuilt.Judge = c2.judge
		pages := &pageSink{}
		r := newPagedRestoreRouter(t, f, pages, map[string]route.Rebuild{testSourceKind: rebuildAs(rebuilt)})
		r.Attach(ctx, reopened)
		r.Wait()

		c2.checkCalls(t, 0)
		f.checkActiveTimers(t, 1)
		pages.check(t)
	})
}

func TestRestoredDeskNoticeWithFailedBuildShowsNothing(t *testing.T) {
	t.Parallel()
	reopened := deskRowStored(t)
	f := fakeAt(clockStart + 60)
	pages := &pageSink{}
	n := f.notice(testPane, stateWaiting, false)
	n.Desk = func(context.Context) (deskstream.Notice, error) { return deskstream.Notice{}, errDeskBuild }
	r := newPagedRestoreRouter(t, f, pages, map[string]route.Rebuild{testSourceKind: rebuildAs(n)})
	r.Attach(t.Context(), reopened)
	r.Wait()

	pages.check(t)
	f.checkActiveTimers(t, 1)

	f.fire(t, seconds(escalationSeconds-60))
	r.Wait()
	f.check(t, []route.Line{
		lineOf(testPane, clockStart+escalationSeconds, stateWaiting, route.Phone, "unseen after 5 minutes"),
	}, nil, []push.Notification{phonePush(testPane, stateWaiting)})
	pages.check(t)
}

func TestRestoredDeskBuildRunsOffTheLock(t *testing.T) {
	t.Parallel()
	reopened := deskRowStored(t)
	f := fakeAt(clockStart + 60)
	pages := &pageSink{}
	building := make(chan struct{})
	release := make(chan struct{})
	releaseOnce := sync.OnceFunc(func() { close(release) })
	n := f.notice(testPane, stateWaiting, false)
	n.Desk = func(context.Context) (deskstream.Notice, error) {
		close(building)
		<-release
		return deskPost(stateWaiting), nil
	}
	r := newPagedRestoreRouter(t, f, pages, map[string]route.Rebuild{testSourceKind: rebuildAs(n)})
	// Registered after the router's Close, so it runs first and frees a build that a failing run left blocked.
	t.Cleanup(releaseOnce)

	attached := make(chan struct{})
	go func() {
		defer close(attached)
		r.Attach(t.Context(), reopened)
	}()
	select {
	case <-attached:
	case <-time.After(waitLimit):
		t.Fatal("Attach waited for the desk build")
	}
	select {
	case <-building:
	case <-time.After(waitLimit):
		t.Fatal("the desk build did not start")
	}
	releaseOnce()
	r.Wait()

	f.check(t, nil, nil, nil)
	pages.check(t, showOf(notice.Key{Pane: testPane}, deskPost(stateWaiting)))
}
