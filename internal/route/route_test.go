package route_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/deskstream"
	"github.com/joshsymonds/orrery/internal/notice"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/push"
	"github.com/joshsymonds/orrery/internal/route"
)

const (
	clockStart   = int64(1_700_000_000)
	otherPane    = "%synthetic-other"
	stateWaiting = "waiting"
	stateDone    = "done"
	quietReason  = "quiet hours"
	testTitle    = "synthetic title"
	testBody     = "synthetic body"
	testSummary  = "synthetic summary"
	waitLimit    = 5 * time.Second
)

// fake is the desk, the phone, the log, the clock, and the timers at once. Its timers run only when a test fires
// them, and firing one advances the clock by the wait it was set for.
type fake struct {
	mu         sync.Mutex
	now        int64
	timers     []*fakeTimer
	present    bool
	deskInput  int64
	localInput int64
	localOK    bool
	localErr   error
	subscribed bool
	subErr     error
	watching   bool
	watchErr   error
	logErr     error
	posts      []deskstream.Notice
	pushes     []pushCall
	seens      []seenCall
	lines      []route.Line
}

type fakeTimer struct {
	d       time.Duration
	f       func()
	stopped bool
	fired   bool
}

func (t *fakeTimer) active() bool {
	return !t.stopped && !t.fired
}

type pushCall struct {
	note push.Notification
	err  error
}

type seenCall struct {
	key  notice.Key
	seen int64
}

func newFake() *fake {
	return &fake{now: clockStart, subscribed: true}
}

// newRouter returns a Router wired to f, closed when the test ends.
func newRouter(t *testing.T, f *fake) *route.Router {
	t.Helper()
	r := route.New(route.Config{
		Host:       testHost,
		Now:        f.Now,
		After:      f.After,
		Desk:       f,
		Phone:      f,
		Subscribed: f.Subscribed,
		LocalInput: f.LocalInput,
		Log:        f,
	})
	t.Cleanup(r.Close)
	return r
}

func (f *fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return time.Unix(f.now, 0)
}

func (f *fake) After(d time.Duration, fn func()) func() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	timer := &fakeTimer{d: d, f: fn}
	f.timers = append(f.timers, timer)
	return func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		if !timer.active() {
			return false
		}
		timer.stopped = true
		return true
	}
}

func (f *fake) Desk() (int64, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.deskInput, f.present
}

func (f *fake) Post(n deskstream.Notice) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.posts = append(f.posts, n)
}

func (f *fake) Notify(ctx context.Context, _ notice.Key, build func(context.Context) (push.Notification, error)) {
	note, err := build(ctx)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pushes = append(f.pushes, pushCall{note: note, err: err})
}

func (f *fake) Seen(_ context.Context, key notice.Key, seen int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seens = append(f.seens, seenCall{key: key, seen: seen})
}

func (f *fake) Write(line route.Line) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lines = append(f.lines, line)
	return f.logErr
}

func (f *fake) Subscribed() (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.subscribed, f.subErr
}

func (f *fake) LocalInput(context.Context) (int64, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.localInput, f.localOK, f.localErr
}

func (f *fake) Watching(context.Context) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.watching, f.watchErr
}

func (f *fake) setWatching(watching bool, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.watching = watching
	f.watchErr = err
}

func (f *fake) setDesk(present bool, input int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.present = present
	f.deskInput = input
}

func (f *fake) setLocal(input int64, ok bool, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.localInput = input
	f.localOK = ok
	f.localErr = err
}

func (f *fake) setSubscribed(subscribed bool, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.subscribed = subscribed
	f.subErr = err
}

func (f *fake) setLogErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.logErr = err
}

// fire runs the one active timer set for d, advancing the clock by d as if that wait had ended.
func (f *fake) fire(t *testing.T, d time.Duration) {
	t.Helper()
	f.mu.Lock()
	var due []*fakeTimer
	for _, timer := range f.timers {
		if timer.active() && timer.d == d {
			due = append(due, timer)
		}
	}
	if len(due) != 1 {
		f.mu.Unlock()
		t.Fatalf("active timers waiting %v = %d, want 1", d, len(due))
	}
	due[0].fired = true
	f.now += int64(d / time.Second)
	fn := due[0].f
	f.mu.Unlock()
	fn()
}

func (f *fake) activeTimers() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	count := 0
	for _, timer := range f.timers {
		if timer.active() {
			count++
		}
	}
	return count
}

// check asserts every line, desk post, and phone push the fake recorded, in order.
func (f *fake) check(t *testing.T, lines []route.Line, posts []deskstream.Notice, pushes []push.Notification) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if !slices.Equal(f.lines, lines) {
		t.Errorf("lines = %+v, want %+v", f.lines, lines)
	}
	if !slices.Equal(f.posts, posts) {
		t.Errorf("desk posts = %+v, want %+v", f.posts, posts)
	}
	got := make([]push.Notification, 0, len(f.pushes))
	for _, p := range f.pushes {
		if p.err != nil {
			t.Errorf("push build failed: %v", p.err)
		}
		got = append(got, p.note)
	}
	if !slices.Equal(got, pushes) {
		t.Errorf("phone pushes = %+v, want %+v", got, pushes)
	}
}

func (f *fake) checkSeen(t *testing.T, want ...seenCall) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if !slices.Equal(f.seens, want) {
		t.Errorf("phone seen calls = %+v, want %+v", f.seens, want)
	}
}

func (f *fake) checkActiveTimers(t *testing.T, want int) {
	t.Helper()
	if got := f.activeTimers(); got != want {
		t.Errorf("active timers = %d, want %d", got, want)
	}
}

// notice builds a pane notice. It is watched when watched is set, and its desk and push builds succeed.
func (f *fake) notice(pane, state string, watched bool) route.Notice {
	n := route.Notice{
		Key:   notice.Key{Pane: pane},
		State: state,
		Desk: func(context.Context) (deskstream.Notice, error) {
			return deskPost(state), nil
		},
		Push: func(context.Context) (push.Notification, error) {
			return phonePush(pane, state), nil
		},
	}
	if watched {
		n.Watching = f.Watching
	}
	return n
}

func deskPost(state string) deskstream.Notice {
	return deskstream.Notice{State: state, Title: testTitle, Body: testBody}
}

func phonePush(pane, state string) push.Notification {
	return push.Notification{Pane: pane, Summary: testSummary, State: protocol.State(state)}
}

func lineOf(pane string, at int64, state, routed, reason string) route.Line {
	return route.Line{Time: at, Host: testHost, Pane: pane, State: state, Route: routed, Reason: reason}
}

func TestSilentNoticeWritesSilenceAndSendsNothing(t *testing.T) {
	f := newFake()
	r := newRouter(t, f)

	r.Notify(t.Context(), route.Notice{Key: notice.Key{Pane: testPane}, State: stateDone, Silent: quietReason})
	r.Wait()

	f.check(t, []route.Line{lineOf(testPane, clockStart, stateDone, route.None, quietReason)}, nil, nil)
	f.checkActiveTimers(t, 0)
}

func TestSilentSessionNoticeLineNamesTheSession(t *testing.T) {
	f := newFake()
	r := newRouter(t, f)

	r.Notify(t.Context(), route.Notice{Key: notice.Key{Session: testSession}, State: stateDone, Silent: quietReason})
	r.Wait()

	want := route.Line{
		Time:    clockStart,
		Host:    testHost,
		Session: testSession,
		State:   stateDone,
		Route:   route.None,
		Reason:  quietReason,
	}
	f.check(t, []route.Line{want}, nil, nil)
}

func TestSilentNoticeEndsPendingNoticeAsChanged(t *testing.T) {
	f := newFake()
	f.setWatching(true, nil)
	r := newRouter(t, f)

	r.Notify(t.Context(), f.notice(testPane, stateWaiting, true))
	r.Wait()
	r.Notify(t.Context(), route.Notice{Key: notice.Key{Pane: testPane}, State: stateDone, Silent: quietReason})
	r.Wait()

	f.check(t, []route.Line{
		lineOf(testPane, clockStart, stateWaiting, route.None, "watching"),
		lineOf(testPane, clockStart, stateWaiting, route.None, "changed"),
		lineOf(testPane, clockStart, stateDone, route.None, quietReason),
	}, nil, nil)
	f.checkActiveTimers(t, 0)
}

func TestNewNoticeReplacesPendingNoticeAsChanged(t *testing.T) {
	f := newFake()
	f.setWatching(true, nil)
	r := newRouter(t, f)

	r.Notify(t.Context(), f.notice(testPane, stateWaiting, true))
	r.Wait()
	f.setDesk(true, clockStart)
	r.Notify(t.Context(), f.notice(testPane, stateDone, false))
	r.Wait()

	f.check(t, []route.Line{
		lineOf(testPane, clockStart, stateWaiting, route.None, "watching"),
		lineOf(testPane, clockStart, stateWaiting, route.None, "changed"),
		lineOf(testPane, clockStart, stateDone, route.Desk, "at the desk"),
	}, []deskstream.Notice{deskPost(stateDone)}, nil)
	f.checkActiveTimers(t, 1)
}

func TestWatchingHoldsThenRoutesWhenItLapses(t *testing.T) {
	f := newFake()
	f.setWatching(true, nil)
	r := newRouter(t, f)

	r.Notify(t.Context(), f.notice(testPane, stateWaiting, true))
	r.Wait()
	f.check(t, []route.Line{lineOf(testPane, clockStart, stateWaiting, route.None, "watching")}, nil, nil)

	f.fire(t, route.WatchRecheck)
	r.Wait()
	f.check(t, []route.Line{lineOf(testPane, clockStart, stateWaiting, route.None, "watching")}, nil, nil)
	f.checkActiveTimers(t, 1)

	f.setWatching(false, nil)
	f.fire(t, route.WatchRecheck)
	r.Wait()
	f.check(t, []route.Line{
		lineOf(testPane, clockStart, stateWaiting, route.None, "watching"),
		lineOf(testPane, clockStart+10, stateWaiting, route.DeskPhone, "away"),
	}, []deskstream.Notice{deskPost(stateWaiting)}, []push.Notification{phonePush(testPane, stateWaiting)})
	f.checkActiveTimers(t, 0)
}

func TestWatchingEndedBySeen(t *testing.T) {
	f := newFake()
	f.setWatching(true, nil)
	r := newRouter(t, f)
	key := notice.Key{Pane: testPane}

	r.Notify(t.Context(), f.notice(testPane, stateWaiting, true))
	r.Wait()
	r.Seen(t.Context(), key, clockStart)
	r.Wait()

	f.check(t, []route.Line{
		lineOf(testPane, clockStart, stateWaiting, route.None, "watching"),
		lineOf(testPane, clockStart, stateWaiting, route.None, "seen"),
	}, nil, nil)
	f.checkSeen(t, seenCall{key: key, seen: clockStart})
	f.checkActiveTimers(t, 0)
}

func TestSeenBeforeNoticeDoesNotEndIt(t *testing.T) {
	f := newFake()
	f.setWatching(true, nil)
	r := newRouter(t, f)
	key := notice.Key{Pane: testPane}

	r.Notify(t.Context(), f.notice(testPane, stateWaiting, true))
	r.Wait()
	r.Seen(t.Context(), key, clockStart-1)
	r.Wait()

	f.check(t, []route.Line{lineOf(testPane, clockStart, stateWaiting, route.None, "watching")}, nil, nil)
	f.checkSeen(t, seenCall{key: key, seen: clockStart - 1})
	f.checkActiveTimers(t, 1)
}

func TestWatchingEndedByChanged(t *testing.T) {
	f := newFake()
	f.setWatching(true, nil)
	r := newRouter(t, f)

	r.Notify(t.Context(), f.notice(testPane, stateWaiting, true))
	r.Wait()
	r.Changed(notice.Key{Pane: testPane})
	r.Wait()

	f.check(t, []route.Line{
		lineOf(testPane, clockStart, stateWaiting, route.None, "watching"),
		lineOf(testPane, clockStart, stateWaiting, route.None, "changed"),
	}, nil, nil)
	f.checkActiveTimers(t, 0)
}

func TestChangedDuringWatchCheckWritesNoSecondLine(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	f := newFake()
	r := newRouter(t, f)

	n := f.notice(testPane, stateWaiting, false)
	n.Watching = func(context.Context) (bool, error) {
		close(started)
		<-release
		return true, nil
	}
	r.Notify(t.Context(), n)
	select {
	case <-started:
	case <-time.After(waitLimit):
		t.Fatal("watching check never started")
	}

	r.Changed(notice.Key{Pane: testPane})
	close(release)
	r.Wait()

	f.check(t, []route.Line{lineOf(testPane, clockStart, stateWaiting, route.None, "changed")}, nil, nil)
	f.checkActiveTimers(t, 0)
}

func TestAtDeskByDeskInputEscalatesAfterFiveMinutesUnseen(t *testing.T) {
	f := newFake()
	f.setDesk(true, clockStart)
	r := newRouter(t, f)

	r.Notify(t.Context(), f.notice(testPane, stateWaiting, false))
	r.Wait()
	f.check(t, []route.Line{lineOf(testPane, clockStart, stateWaiting, route.Desk, "at the desk")},
		[]deskstream.Notice{deskPost(stateWaiting)}, nil)

	f.fire(t, route.Escalation)
	r.Wait()
	f.check(t, []route.Line{
		lineOf(testPane, clockStart, stateWaiting, route.Desk, "at the desk"),
		lineOf(testPane, clockStart+300, stateWaiting, route.Phone, "unseen after 5 minutes"),
	}, []deskstream.Notice{deskPost(stateWaiting)}, []push.Notification{phonePush(testPane, stateWaiting)})
	f.checkActiveTimers(t, 0)
}

func TestAtDeskByDeskInputNewerThanLocalInput(t *testing.T) {
	f := newFake()
	f.setDesk(true, clockStart)
	f.setLocal(clockStart-3600, true, nil)
	r := newRouter(t, f)

	r.Notify(t.Context(), f.notice(testPane, stateWaiting, false))
	r.Wait()
	f.check(t, []route.Line{lineOf(testPane, clockStart, stateWaiting, route.Desk, "at the desk")},
		[]deskstream.Notice{deskPost(stateWaiting)}, nil)
	f.checkActiveTimers(t, 1)
}

func TestAtDeskByLocalInputEndedBySeen(t *testing.T) {
	f := newFake()
	f.setDesk(true, clockStart-3600)
	f.setLocal(clockStart, true, nil)
	r := newRouter(t, f)
	key := notice.Key{Pane: testPane}

	r.Notify(t.Context(), f.notice(testPane, stateWaiting, false))
	r.Wait()
	f.check(t, []route.Line{lineOf(testPane, clockStart, stateWaiting, route.Desk, "at the desk")},
		[]deskstream.Notice{deskPost(stateWaiting)}, nil)

	r.Seen(t.Context(), key, clockStart)
	r.Wait()
	f.check(t, []route.Line{
		lineOf(testPane, clockStart, stateWaiting, route.Desk, "at the desk"),
		lineOf(testPane, clockStart, stateWaiting, route.None, "seen"),
	}, []deskstream.Notice{deskPost(stateWaiting)}, nil)
	f.checkSeen(t, seenCall{key: key, seen: clockStart})
	f.checkActiveTimers(t, 0)
}

func TestDeskInputWindowAndPresenceDecideRoute(t *testing.T) {
	cases := []struct {
		name    string
		present bool
		input   int64
		routed  string
		reason  string
		posts   []deskstream.Notice
		pushes  []push.Notification
	}{
		{
			name:    "input at the window edge is at the desk",
			present: true,
			input:   clockStart - 120,
			routed:  route.Desk,
			reason:  "at the desk",
			posts:   []deskstream.Notice{deskPost(stateWaiting)},
		},
		{
			name:    "input older than the window is away",
			present: true,
			input:   clockStart - 121,
			routed:  route.DeskPhone,
			reason:  "away",
			posts:   []deskstream.Notice{deskPost(stateWaiting)},
			pushes:  []push.Notification{phonePush(testPane, stateWaiting)},
		},
		{
			name:    "desk not present is away",
			present: false,
			input:   clockStart,
			routed:  route.DeskPhone,
			reason:  "away",
			posts:   []deskstream.Notice{deskPost(stateWaiting)},
			pushes:  []push.Notification{phonePush(testPane, stateWaiting)},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake()
			f.setDesk(tc.present, tc.input)
			r := newRouter(t, f)

			r.Notify(t.Context(), f.notice(testPane, stateWaiting, false))
			r.Wait()

			f.check(
				t,
				[]route.Line{lineOf(testPane, clockStart, stateWaiting, tc.routed, tc.reason)},
				tc.posts,
				tc.pushes,
			)
		})
	}
}

func TestNoSubscriptionAtDeskEscalatesToNone(t *testing.T) {
	f := newFake()
	f.setDesk(true, clockStart)
	f.setSubscribed(false, nil)
	r := newRouter(t, f)

	r.Notify(t.Context(), f.notice(testPane, stateWaiting, false))
	r.Wait()
	f.fire(t, route.Escalation)
	r.Wait()

	f.check(t, []route.Line{
		lineOf(testPane, clockStart, stateWaiting, route.Desk, "at the desk"),
		lineOf(testPane, clockStart+300, stateWaiting, route.None, "unseen after 5 minutes; no push subscription"),
	}, []deskstream.Notice{deskPost(stateWaiting)}, nil)
	f.checkActiveTimers(t, 0)
}

func TestNoSubscriptionAwayRoutesDeskOnly(t *testing.T) {
	f := newFake()
	f.setSubscribed(false, nil)
	r := newRouter(t, f)

	r.Notify(t.Context(), f.notice(testPane, stateWaiting, false))
	r.Wait()

	f.check(t, []route.Line{
		lineOf(testPane, clockStart, stateWaiting, route.Desk, "away; no push subscription"),
	}, []deskstream.Notice{deskPost(stateWaiting)}, nil)
	f.checkActiveTimers(t, 0)
}

func TestFailedDeskBuildRoutesAwayWithoutDeskNotice(t *testing.T) {
	cases := []struct {
		name       string
		present    bool
		subscribed bool
		routed     string
		reason     string
		pushes     []push.Notification
	}{
		{
			name:       "at the desk routes away to the phone",
			present:    true,
			subscribed: true,
			routed:     route.Phone,
			reason:     "away; desk notice failed",
			pushes:     []push.Notification{phonePush(testPane, stateWaiting)},
		},
		{
			name:       "away without a subscription routes nowhere",
			present:    false,
			subscribed: false,
			routed:     route.None,
			reason:     "away; desk notice failed; no push subscription",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake()
			f.setDesk(tc.present, clockStart)
			f.setSubscribed(tc.subscribed, nil)
			r := newRouter(t, f)

			n := f.notice(testPane, stateWaiting, false)
			n.Desk = func(context.Context) (deskstream.Notice, error) {
				return deskstream.Notice{}, errors.New("synthetic desk build failure")
			}
			r.Notify(t.Context(), n)
			r.Wait()

			f.check(t, []route.Line{lineOf(testPane, clockStart, stateWaiting, tc.routed, tc.reason)}, nil, tc.pushes)
			f.checkActiveTimers(t, 0)
		})
	}
}

func TestErrorsReadAsDefaults(t *testing.T) {
	f := newFake()
	err := errors.New("synthetic read failure")
	f.setWatching(true, err)
	f.setDesk(true, clockStart-3600)
	f.setLocal(clockStart, true, err)
	f.setSubscribed(false, err)
	r := newRouter(t, f)

	r.Notify(t.Context(), f.notice(testPane, stateWaiting, true))
	r.Wait()

	f.check(t, []route.Line{lineOf(testPane, clockStart, stateWaiting, route.DeskPhone, "away")},
		[]deskstream.Notice{deskPost(stateWaiting)}, []push.Notification{phonePush(testPane, stateWaiting)})
	f.checkActiveTimers(t, 0)
}

func TestLogErrorStillRoutes(t *testing.T) {
	f := newFake()
	f.setLogErr(errors.New("synthetic log failure"))
	r := newRouter(t, f)

	r.Notify(t.Context(), f.notice(testPane, stateWaiting, false))
	r.Wait()

	f.check(t, []route.Line{lineOf(testPane, clockStart, stateWaiting, route.DeskPhone, "away")},
		[]deskstream.Notice{deskPost(stateWaiting)}, []push.Notification{phonePush(testPane, stateWaiting)})
}

func TestCloseWritesShutdownForEachPendingNoticeAndStopsTimers(t *testing.T) {
	f := newFake()
	f.setWatching(true, nil)
	r := newRouter(t, f)

	r.Notify(t.Context(), f.notice(testPane, stateWaiting, true))
	r.Wait()
	f.setDesk(true, clockStart)
	r.Notify(t.Context(), f.notice(otherPane, stateDone, false))
	r.Wait()

	r.Close()

	f.check(t, []route.Line{
		lineOf(testPane, clockStart, stateWaiting, route.None, "watching"),
		lineOf(otherPane, clockStart, stateDone, route.Desk, "at the desk"),
		lineOf(testPane, clockStart, stateWaiting, route.None, "shutdown"),
		lineOf(otherPane, clockStart, stateDone, route.None, "shutdown"),
	}, []deskstream.Notice{deskPost(stateDone)}, nil)
	f.checkActiveTimers(t, 0)
}
