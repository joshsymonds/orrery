package main

import (
	"context"
	"log/slog"
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
	viewHost    = "synthetic-view-host"
	viewStart   = int64(1_700_000_000)
	viewSession = "synthetic-view-session"
	viewEpic    = "synthetic-view-epic"
	viewPane    = "%synthetic-view"
	viewOther   = "synthetic-view-other"
)

// viewPages is the PageSink the viewing tests wire into the router. It records each Show and End, in order.
type viewPages struct {
	mu    sync.Mutex
	shown []notice.Key
	ended []notice.Key
}

// Show records a notice shown on a page for key.
func (p *viewPages) Show(key notice.Key, _ deskstream.Notice) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.shown = append(p.shown, key)
}

// End records a page's notice for key ending.
func (p *viewPages) End(key notice.Key) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ended = append(p.ended, key)
}

// check asserts the notices shown on pages, in order, and that no page notice ended.
func (p *viewPages) check(t *testing.T, shown []notice.Key) {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if !slices.Equal(p.shown, shown) {
		t.Errorf("page shows = %+v, want %+v", p.shown, shown)
	}
	if len(p.ended) != 0 {
		t.Errorf("page ends = %+v, want none", p.ended)
	}
}

// newViewRouter returns the router serve builds: the config from routeConfig, which serve calls too, with sinks as
// its desk, phone, log and clock, and pages as its page sink. It is closed when the test ends.
func newViewRouter(t *testing.T, sinks *paneSinks, pages *viewPages, viewing *viewTracker) *route.Router {
	t.Helper()
	hub := newRelay(routeConfig(serveRouteInputs{
		Host:       viewHost,
		Now:        sinks.Now,
		After:      sinks.After,
		Desk:       sinks,
		Phone:      sinks,
		Pages:      pages,
		Subscribed: sinks.Subscribed,
		Log:        sinks,
		Logger:     slog.New(slog.DiscardHandler),
	}, viewing), &relay{})
	t.Cleanup(hub.router.Close)
	return hub.router
}

// viewNotice builds a waiting notice for key, whose desk and push builds succeed. It has no watching check.
func viewNotice(key notice.Key) route.Notice {
	return route.Notice{
		Key:   key,
		State: "waiting",
		Desk: func(context.Context) (deskstream.Notice, error) {
			return viewDeskPost(), nil
		},
		Push: func(context.Context) (push.Notification, error) {
			return push.Notification{
				Pane:    key.Pane,
				Session: key.Session,
				Epic:    key.Epic,
				Summary: "synthetic summary",
				State:   protocol.State("waiting"),
			}, nil
		},
	}
}

// viewDeskPost is the desk notice viewNotice builds.
func viewDeskPost() deskstream.Notice {
	return deskstream.Notice{State: "waiting", Title: "synthetic title", Body: "synthetic body"}
}

// viewLine is the log line for a waiting notice at key, written at at.
func viewLine(key notice.Key, at int64, routed, reason string) route.Line {
	return route.Line{
		Time:    at,
		Host:    viewHost,
		Pane:    key.Pane,
		Session: key.Session,
		Epic:    key.Epic,
		State:   "waiting",
		Route:   routed,
		Reason:  reason,
	}
}

// TestViewingHoldsSessionAndEpicNoticesOnEverySurface wires the viewing tracker into the router orreryd builds, on one
// synthetic clock. While the reports name the notice's key and are under 30 s old, the notice shows on no surface: no
// desk post, no page show and no push. Once the reports stop, the notice routes at the first recheck that finds its
// newest report 30 s old.
func TestViewingHoldsSessionAndEpicNoticesOnEverySurface(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		key  notice.Key
		view protocol.ViewKey
	}{
		{"session", notice.Key{Session: viewSession}, protocol.ViewKey{Session: viewSession}},
		{"epic", notice.Key{Epic: viewEpic}, protocol.ViewKey{Epic: viewEpic}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sinks := newPaneSinks(viewStart, true)
			pages := &viewPages{}
			tracker := newViewTracker(sinks.Now)
			r := newViewRouter(t, sinks, pages, tracker)

			tracker.note(sinks.Now().Unix(), tc.view)
			r.Notify(t.Context(), viewNotice(tc.key))
			r.Wait()
			sinks.check(t, []route.Line{viewLine(tc.key, viewStart, route.None, "watching")}, nil, nil)
			pages.check(t, nil)

			// The reports continue for 25 s, one before each recheck, so the notice stays held at each recheck.
			for range 5 {
				tracker.note(sinks.Now().Unix(), tc.view)
				sinks.fire(t, route.WatchRecheck)
				r.Wait()
			}
			sinks.check(t, []route.Line{viewLine(tc.key, viewStart, route.None, "watching")}, nil, nil)
			pages.check(t, nil)

			// The reports stop. The last one is at +20 s, so the notice is held through +45 s and routes at +50 s.
			for range 4 {
				sinks.fire(t, route.WatchRecheck)
				r.Wait()
			}
			sinks.check(t, []route.Line{viewLine(tc.key, viewStart, route.None, "watching")}, nil, nil)
			pages.check(t, nil)

			sinks.fire(t, route.WatchRecheck)
			r.Wait()
			sinks.check(t,
				[]route.Line{
					viewLine(tc.key, viewStart, route.None, "watching"),
					viewLine(tc.key, viewStart+50, route.Desk, "at the desk"),
				},
				[]deskstream.Notice{viewDeskPost()},
				nil,
			)
			pages.check(t, []notice.Key{tc.key})
		})
	}
}

// TestViewingReportForAnotherKeyDoesNotHold checks that a viewing report naming another key, or no key, leaves a
// notice unheld: it routes at once, as it does with no viewing report.
func TestViewingReportForAnotherKeyDoesNotHold(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		key  notice.Key
		view protocol.ViewKey
	}{
		{"session notice, report names another session", notice.Key{Session: viewSession},
			protocol.ViewKey{Session: viewOther}},
		{"epic notice, report names a pane", notice.Key{Epic: viewEpic}, protocol.ViewKey{Pane: viewPane}},
		{"session notice, report names no key", notice.Key{Session: viewSession}, protocol.ViewKey{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sinks := newPaneSinks(viewStart, true)
			pages := &viewPages{}
			tracker := newViewTracker(sinks.Now)
			r := newViewRouter(t, sinks, pages, tracker)

			tracker.note(sinks.Now().Unix(), tc.view)
			r.Notify(t.Context(), viewNotice(tc.key))
			r.Wait()
			sinks.check(t, []route.Line{viewLine(tc.key, viewStart, route.Desk, "at the desk")},
				[]deskstream.Notice{viewDeskPost()}, nil)
			pages.check(t, []notice.Key{tc.key})
			if got := sinks.activeTimers(route.WatchRecheck); got != 0 {
				t.Errorf("active recheck timers = %d, want none", got)
			}
		})
	}
}

// TestViewingReportNamingAnotherKeyRoutesAtNextRecheck holds a session notice on its viewing report, then has the page
// name an epic instead. The notice routes at the next recheck, as it does once the page stops viewing it.
func TestViewingReportNamingAnotherKeyRoutesAtNextRecheck(t *testing.T) {
	t.Parallel()
	sinks := newPaneSinks(viewStart, true)
	pages := &viewPages{}
	tracker := newViewTracker(sinks.Now)
	r := newViewRouter(t, sinks, pages, tracker)
	key := notice.Key{Session: viewSession}

	tracker.note(sinks.Now().Unix(), protocol.ViewKey{Session: viewSession})
	r.Notify(t.Context(), viewNotice(key))
	r.Wait()
	sinks.check(t, []route.Line{viewLine(key, viewStart, route.None, "watching")}, nil, nil)

	tracker.note(sinks.Now().Unix(), protocol.ViewKey{Epic: viewEpic})
	sinks.fire(t, route.WatchRecheck)
	r.Wait()
	sinks.check(t,
		[]route.Line{
			viewLine(key, viewStart, route.None, "watching"),
			viewLine(key, viewStart+5, route.Desk, "at the desk"),
		},
		[]deskstream.Notice{viewDeskPost()},
		nil,
	)
	pages.check(t, []notice.Key{key})
}
