package route_test

import (
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/joshsymonds/orrery/internal/deskstream"
	"github.com/joshsymonds/orrery/internal/notice"
	"github.com/joshsymonds/orrery/internal/push"
	"github.com/joshsymonds/orrery/internal/route"
)

// pageCall is one call a Router makes on its PageSink: a Show of note for key, or, when end is set, an End of key.
type pageCall struct {
	end  bool
	key  notice.Key
	note deskstream.Notice
}

func showOf(key notice.Key, note deskstream.Notice) pageCall {
	return pageCall{key: key, note: note}
}

func endOf(key notice.Key) pageCall {
	return pageCall{end: true, key: key}
}

// pageSink records the calls a Router makes on its PageSink, in order.
type pageSink struct {
	mu    sync.Mutex
	calls []pageCall
}

func (p *pageSink) Show(key notice.Key, note deskstream.Notice) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, showOf(key, note))
}

func (p *pageSink) End(key notice.Key) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, endOf(key))
}

// check asserts the calls p has recorded, in order.
func (p *pageSink) check(t *testing.T, want ...pageCall) {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if !slices.Equal(p.calls, want) {
		t.Errorf("page calls = %+v, want %+v", p.calls, want)
	}
}

// newPagedRouter returns a Router wired to f and to pages, closed when the test ends.
func newPagedRouter(t *testing.T, f *fake, pages *pageSink) *route.Router {
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
	})
	t.Cleanup(r.Close)
	return r
}

func TestAtDeskNoticeShowsItsDeskNoticeOnce(t *testing.T) {
	t.Parallel()
	f := newFake()
	f.setDesk(clockStart)
	pages := &pageSink{}
	r := newPagedRouter(t, f, pages)
	key := notice.Key{Pane: testPane}

	r.Notify(t.Context(), f.notice(testPane, stateWaiting, false))
	r.Wait()

	f.check(t, []route.Line{lineOf(testPane, clockStart, stateWaiting, route.Desk, "at the desk")},
		[]deskstream.Notice{deskPost(stateWaiting)}, nil)
	pages.check(t, showOf(key, deskPost(stateWaiting)))
}

func TestAtDeskNoticeEndsOnceWhenItEnds(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		// end makes the notice shown for testPane end.
		end func(t *testing.T, f *fake, r *route.Router)
	}{
		{
			name: "seen",
			end: func(t *testing.T, _ *fake, r *route.Router) {
				t.Helper()
				r.Seen(t.Context(), notice.Key{Pane: testPane}, clockStart)
			},
		},
		{
			name: "escalated to the phone",
			end: func(t *testing.T, f *fake, _ *route.Router) {
				t.Helper()
				f.fire(t, route.Escalation)
			},
		},
		{
			name: "changed",
			end: func(_ *testing.T, _ *fake, r *route.Router) {
				r.Changed(notice.Key{Pane: testPane})
			},
		},
		{
			name: "replaced by a newer notice under its key",
			end: func(t *testing.T, f *fake, r *route.Router) {
				t.Helper()
				f.setWatching(true, nil)
				r.Notify(t.Context(), f.notice(testPane, stateDone, true))
			},
		},
		{
			name: "shutdown",
			end: func(_ *testing.T, _ *fake, r *route.Router) {
				r.Close()
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFake()
			f.setDesk(clockStart)
			pages := &pageSink{}
			r := newPagedRouter(t, f, pages)
			key := notice.Key{Pane: testPane}

			r.Notify(t.Context(), f.notice(testPane, stateWaiting, false))
			r.Wait()
			pages.check(t, showOf(key, deskPost(stateWaiting)))

			tc.end(t, f, r)
			r.Wait()
			pages.check(t, showOf(key, deskPost(stateWaiting)), endOf(key))
		})
	}
}

func TestShutdownKeepingStoredRowEndsPageNotice(t *testing.T) {
	t.Parallel()
	f := newFake()
	f.setDesk(clockStart)
	pages := &pageSink{}
	r := newPagedRouter(t, f, pages)
	key := notice.Key{Pane: testPane}
	r.Attach(t.Context(), openStore(t, filepath.Join(t.TempDir(), "orrery.db")))

	r.Notify(t.Context(), sourced(f.notice(testPane, stateWaiting, false)))
	r.Wait()
	r.Close()

	f.check(t, []route.Line{lineOf(testPane, clockStart, stateWaiting, route.Desk, "at the desk")},
		[]deskstream.Notice{deskPost(stateWaiting)}, nil)
	pages.check(t, showOf(key, deskPost(stateWaiting)), endOf(key))
}

func TestAwayNoticeIsNeverShown(t *testing.T) {
	t.Parallel()
	f := newFake()
	pages := &pageSink{}
	r := newPagedRouter(t, f, pages)

	r.Notify(t.Context(), f.notice(testPane, stateWaiting, false))
	r.Wait()

	f.check(t, []route.Line{lineOf(testPane, clockStart, stateWaiting, route.DeskPhone, "away")},
		[]deskstream.Notice{deskPost(stateWaiting)}, []push.Notification{phonePush(testPane, stateWaiting)})
	pages.check(t)
}

func TestUnneededNoticeIsNeverShown(t *testing.T) {
	t.Parallel()
	f := newFake()
	f.setDesk(clockStart)
	pages := &pageSink{}
	r := newPagedRouter(t, f, pages)
	c := &canned{verdict: route.Verdict{Needed: false}}

	n := f.notice(testPane, stateDone, false)
	n.Judge = c.judge
	r.Notify(t.Context(), n)
	r.Wait()
	f.fire(t, route.JudgeDelay)
	r.Wait()

	c.checkCalls(t, 1)
	f.check(t, []route.Line{lineOf(testPane, clockStart+10, stateDone, route.DeskPhone, "unneeded")},
		[]deskstream.Notice{quietDonePost()}, []push.Notification{silentDonePush()})
	pages.check(t)
}

func TestRouterWithoutPagesStillRoutes(t *testing.T) {
	t.Parallel()
	f := newFake()
	f.setDesk(clockStart)
	r := newRouter(t, f)

	r.Notify(t.Context(), f.notice(testPane, stateWaiting, false))
	r.Wait()
	f.check(t, []route.Line{lineOf(testPane, clockStart, stateWaiting, route.Desk, "at the desk")},
		[]deskstream.Notice{deskPost(stateWaiting)}, nil)

	r.Seen(t.Context(), notice.Key{Pane: testPane}, clockStart)
	r.Wait()
	f.check(t, []route.Line{
		lineOf(testPane, clockStart, stateWaiting, route.Desk, "at the desk"),
		lineOf(testPane, clockStart, stateWaiting, route.None, "seen"),
	}, []deskstream.Notice{deskPost(stateWaiting)}, nil)
	f.checkActiveTimers(t, 0)
}
