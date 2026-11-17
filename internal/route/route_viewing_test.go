package route_test

import (
	"context"
	"slices"
	"sync"
	"testing"

	"github.com/joshsymonds/orrery/internal/deskstream"
	"github.com/joshsymonds/orrery/internal/notice"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/push"
	"github.com/joshsymonds/orrery/internal/route"
)

const (
	viewedPane    = "%synthetic-viewed"
	viewedSession = "synthetic-viewed-session"
	viewedEpic    = "synthetic-viewed-epic"
)

// viewCheck is a Config.Viewing that answers answer for every key and records each key it is asked about.
type viewCheck struct {
	mu     sync.Mutex
	answer bool
	asked  []notice.Key
}

// viewed is the check the Router calls. It records key and answers the check's answer.
func (c *viewCheck) viewed(key notice.Key) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.asked = append(c.asked, key)
	return c.answer
}

// set changes what the check answers from now on.
func (c *viewCheck) set(answer bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.answer = answer
}

// checkAsked asserts the keys the check was asked about, in order.
func (c *viewCheck) checkAsked(t *testing.T, want ...notice.Key) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if !slices.Equal(c.asked, want) {
		t.Errorf("viewing check asked about %+v, want %+v", c.asked, want)
	}
}

// newViewedRouter returns a Router wired to f, to pages and to check as its Config.Viewing, closed when the test ends.
func newViewedRouter(t *testing.T, f *fake, pages *pageSink, check *viewCheck) *route.Router {
	t.Helper()
	r := route.New(route.Config{
		Host:       testHost,
		Now:        f.Now,
		After:      f.After,
		Desk:       f,
		Phone:      f,
		Pages:      pages,
		Viewing:    check.viewed,
		Subscribed: f.Subscribed,
		Log:        f,
		StoreAfter: storeBoundNever,
	})
	t.Cleanup(r.Close)
	return r
}

// keyedNotice builds an unwatched notice for key, whose desk and push builds succeed.
func keyedNotice(key notice.Key, state string) route.Notice {
	return route.Notice{
		Key:   key,
		State: state,
		Desk: func(context.Context) (deskstream.Notice, error) {
			return deskPost(state), nil
		},
		Push: func(context.Context) (push.Notification, error) {
			return keyedPush(key, state), nil
		},
	}
}

// keyedPush is the phone push for a notice at key in state.
func keyedPush(key notice.Key, state string) push.Notification {
	return push.Notification{
		Pane:    key.Pane,
		Session: key.Session,
		Epic:    key.Epic,
		Summary: testSummary,
		State:   protocol.State(state),
	}
}

// keyedLine is the log line for a notice at key, written at at.
func keyedLine(key notice.Key, at int64, routed, reason string) route.Line {
	return route.Line{
		Time:    at,
		Host:    testHost,
		Pane:    key.Pane,
		Session: key.Session,
		Epic:    key.Epic,
		State:   stateWaiting,
		Route:   routed,
		Reason:  reason,
	}
}

// viewedKeyCases are the keys the viewing tests hold: a pane, a session and an epic.
func viewedKeyCases() []struct {
	name string
	key  notice.Key
} {
	return []struct {
		name string
		key  notice.Key
	}{
		{"pane", notice.Key{Pane: viewedPane}},
		{"session", notice.Key{Session: viewedSession}},
		{"epic", notice.Key{Epic: viewedEpic}},
	}
}

func TestViewingNoticeIsHeldOnEverySurfaceWhileCheckAnswersTrue(t *testing.T) {
	t.Parallel()
	for _, tc := range viewedKeyCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFake()
			pages := &pageSink{}
			check := &viewCheck{answer: true}
			r := newViewedRouter(t, f, pages, check)

			r.Notify(t.Context(), keyedNotice(tc.key, stateWaiting))
			r.Wait()
			f.check(t, []route.Line{keyedLine(tc.key, clockStart, route.None, "watching")}, nil, nil)
			pages.check(t)
			check.checkAsked(t, tc.key)
			f.checkActiveTimers(t, 1)

			f.fire(t, route.WatchRecheck)
			r.Wait()
			f.check(t, []route.Line{keyedLine(tc.key, clockStart, route.None, "watching")}, nil, nil)
			pages.check(t)
			check.checkAsked(t, tc.key, tc.key)
			f.checkActiveTimers(t, 1)
		})
	}
}

func TestViewingNoticeRoutesAtNextRecheckOnceCheckAnswersFalse(t *testing.T) {
	t.Parallel()
	for _, tc := range viewedKeyCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFake()
			pages := &pageSink{}
			check := &viewCheck{answer: true}
			r := newViewedRouter(t, f, pages, check)

			r.Notify(t.Context(), keyedNotice(tc.key, stateWaiting))
			r.Wait()
			check.set(false)
			f.fire(t, route.WatchRecheck)
			r.Wait()

			// The desk is away, so the notice goes to the desk post and the phone, and no page shows it.
			f.check(t,
				[]route.Line{
					keyedLine(tc.key, clockStart, route.None, "watching"),
					keyedLine(tc.key, clockStart+5, route.DeskPhone, "away"),
				},
				[]deskstream.Notice{deskPost(stateWaiting)},
				[]push.Notification{keyedPush(tc.key, stateWaiting)},
			)
			pages.check(t)
			check.checkAsked(t, tc.key, tc.key)
			f.checkActiveTimers(t, 0)
		})
	}
}

func TestViewingNoticeRoutesAtOnceWhenCheckAnswersFalse(t *testing.T) {
	t.Parallel()
	f := newFake()
	pages := &pageSink{}
	check := &viewCheck{answer: false}
	r := newViewedRouter(t, f, pages, check)

	r.Notify(t.Context(), keyedNotice(notice.Key{Session: viewedSession}, stateWaiting))
	r.Wait()
	f.check(t,
		[]route.Line{keyedLine(notice.Key{Session: viewedSession}, clockStart, route.DeskPhone, "away")},
		[]deskstream.Notice{deskPost(stateWaiting)},
		[]push.Notification{keyedPush(notice.Key{Session: viewedSession}, stateWaiting)},
	)
	pages.check(t)
	check.checkAsked(t, notice.Key{Session: viewedSession})
	f.checkActiveTimers(t, 0)
}
