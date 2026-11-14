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
	testWhy      = "synthetic why"
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
	subscribed bool
	subErr     error
	watching   bool
	watchErr   error
	logErr     error
	posts      []deskstream.Notice
	pushes     []pushCall
	sents      []func()
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

func (f *fake) Notify(
	ctx context.Context,
	_ notice.Key,
	build func(context.Context) (push.Notification, error),
	sent func(),
) {
	note, err := build(ctx)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pushes = append(f.pushes, pushCall{note: note, err: err})
	if sent != nil {
		f.sents = append(f.sents, sent)
	}
}

// publish runs the sent callbacks Notify recorded, in order, as the phone does when each push's publish returns. It
// runs them outside f.mu, because a callback takes the Router's lock.
func (f *fake) publish() {
	f.mu.Lock()
	sents := f.sents
	f.sents = nil
	f.mu.Unlock()
	for _, sent := range sents {
		sent()
	}
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

func TestAtDeskByDeskInputEndedBySeen(t *testing.T) {
	f := newFake()
	f.setDesk(true, clockStart)
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

// canned is a Judge that answers with one verdict and one error, and counts its calls.
type canned struct {
	mu      sync.Mutex
	calls   int
	verdict route.Verdict
	err     error
}

func (c *canned) judge(context.Context) (route.Verdict, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	return c.verdict, c.err
}

func (c *canned) checkCalls(t *testing.T, want int) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.calls != want {
		t.Errorf("judge calls = %d, want %d", c.calls, want)
	}
}

// fireAsync runs fire for d on its own goroutine, for a timer whose callback blocks until the test releases it. The
// returned channel closes when the callback returns.
func fireAsync(t *testing.T, f *fake, d time.Duration) <-chan struct{} {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		f.fire(t, d)
	}()
	return done
}

// quietDonePost is the quiet desk notice an unneeded done posts.
func quietDonePost() deskstream.Notice {
	n := deskPost(stateDone)
	n.Quiet = true
	return n
}

// silentDonePush is the silent push an unneeded done sends for testPane.
func silentDonePush() push.Notification {
	n := phonePush(testPane, stateDone)
	n.Silent = true
	return n
}

func TestJudgeNoCallBeforeDelayThenNeededRoutesAsToday(t *testing.T) {
	f := newFake()
	c := &canned{verdict: route.Verdict{Needed: true}}
	r := newRouter(t, f)

	n := f.notice(testPane, stateDone, false)
	n.Judge = c.judge
	r.Notify(t.Context(), n)
	r.Wait()
	c.checkCalls(t, 0)
	f.check(t, nil, nil, nil)
	f.checkActiveTimers(t, 1)

	f.fire(t, route.JudgeDelay)
	r.Wait()
	c.checkCalls(t, 1)
	f.check(t, []route.Line{lineOf(testPane, clockStart+10, stateDone, route.DeskPhone, "away; needed")},
		[]deskstream.Notice{deskPost(stateDone)}, []push.Notification{phonePush(testPane, stateDone)})
	f.checkActiveTimers(t, 0)
}

func TestJudgeUnneededGoesQuietToDeskAndSilentToPhone(t *testing.T) {
	cases := []struct {
		name       string
		why        string
		subscribed bool
		deskFails  bool
		atDesk     bool
		routed     string
		reason     string
		posts      []deskstream.Notice
		pushes     []push.Notification
	}{
		{
			name:       "with why and a subscription",
			why:        testWhy,
			subscribed: true,
			routed:     route.DeskPhone,
			reason:     "unneeded: synthetic why",
			posts:      []deskstream.Notice{quietDonePost()},
			pushes:     []push.Notification{silentDonePush()},
		},
		{
			name:       "without why",
			subscribed: true,
			routed:     route.DeskPhone,
			reason:     "unneeded",
			posts:      []deskstream.Notice{quietDonePost()},
			pushes:     []push.Notification{silentDonePush()},
		},
		{
			name:   "without a subscription the push is dropped",
			why:    testWhy,
			routed: route.Desk,
			reason: "unneeded: synthetic why; no push subscription",
			posts:  []deskstream.Notice{quietDonePost()},
		},
		{
			name:       "at the desk it still goes to both at once",
			why:        testWhy,
			subscribed: true,
			atDesk:     true,
			routed:     route.DeskPhone,
			reason:     "unneeded: synthetic why",
			posts:      []deskstream.Notice{quietDonePost()},
			pushes:     []push.Notification{silentDonePush()},
		},
		{
			name:       "a failed desk build sends the push alone",
			why:        testWhy,
			subscribed: true,
			deskFails:  true,
			routed:     route.Phone,
			reason:     "unneeded: synthetic why; desk notice failed",
			pushes:     []push.Notification{silentDonePush()},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake()
			f.setSubscribed(tc.subscribed, nil)
			if tc.atDesk {
				f.setDesk(true, clockStart)
			}
			c := &canned{verdict: route.Verdict{Needed: false, Why: tc.why}}
			r := newRouter(t, f)

			n := f.notice(testPane, stateDone, false)
			n.Judge = c.judge
			if tc.deskFails {
				n.Desk = func(context.Context) (deskstream.Notice, error) {
					return deskstream.Notice{}, errors.New("synthetic desk build failure")
				}
			}
			r.Notify(t.Context(), n)
			r.Wait()
			f.fire(t, route.JudgeDelay)
			r.Wait()

			c.checkCalls(t, 1)
			f.check(t, []route.Line{lineOf(testPane, clockStart+10, stateDone, tc.routed, tc.reason)},
				tc.posts, tc.pushes)
			f.checkActiveTimers(t, 0)
		})
	}
}

func TestJudgeNeededRoutesAsTodayWithVerdictClause(t *testing.T) {
	cases := []struct {
		name      string
		why       string
		deskFails bool
		routed    string
		reason    string
		posts     []deskstream.Notice
		pushes    []push.Notification
	}{
		{
			name:   "away with why",
			why:    testWhy,
			routed: route.DeskPhone,
			reason: "away; needed: synthetic why",
			posts:  []deskstream.Notice{deskPost(stateDone)},
			pushes: []push.Notification{phonePush(testPane, stateDone)},
		},
		{
			name:   "away without why",
			routed: route.DeskPhone,
			reason: "away; needed",
			posts:  []deskstream.Notice{deskPost(stateDone)},
			pushes: []push.Notification{phonePush(testPane, stateDone)},
		},
		{
			name:      "failed desk build keeps the clause",
			why:       testWhy,
			deskFails: true,
			routed:    route.Phone,
			reason:    "away; needed: synthetic why; desk notice failed",
			pushes:    []push.Notification{phonePush(testPane, stateDone)},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake()
			c := &canned{verdict: route.Verdict{Needed: true, Why: tc.why}}
			r := newRouter(t, f)

			n := f.notice(testPane, stateDone, false)
			n.Judge = c.judge
			if tc.deskFails {
				n.Desk = func(context.Context) (deskstream.Notice, error) {
					return deskstream.Notice{}, errors.New("synthetic desk build failure")
				}
			}
			r.Notify(t.Context(), n)
			r.Wait()
			f.fire(t, route.JudgeDelay)
			r.Wait()

			c.checkCalls(t, 1)
			f.check(t, []route.Line{lineOf(testPane, clockStart+10, stateDone, tc.routed, tc.reason)},
				tc.posts, tc.pushes)
			f.checkActiveTimers(t, 0)
		})
	}
}

func TestJudgeNeededAtDeskEscalatesAsToday(t *testing.T) {
	f := newFake()
	f.setDesk(true, clockStart)
	c := &canned{verdict: route.Verdict{Needed: true, Why: testWhy}}
	r := newRouter(t, f)

	n := f.notice(testPane, stateDone, false)
	n.Judge = c.judge
	r.Notify(t.Context(), n)
	r.Wait()
	f.fire(t, route.JudgeDelay)
	r.Wait()
	f.check(
		t,
		[]route.Line{lineOf(testPane, clockStart+10, stateDone, route.Desk, "at the desk; needed: synthetic why")},
		[]deskstream.Notice{deskPost(stateDone)},
		nil,
	)

	f.fire(t, route.Escalation)
	r.Wait()
	f.check(t, []route.Line{
		lineOf(testPane, clockStart+10, stateDone, route.Desk, "at the desk; needed: synthetic why"),
		lineOf(testPane, clockStart+310, stateDone, route.Phone, "unseen after 5 minutes"),
	}, []deskstream.Notice{deskPost(stateDone)}, []push.Notification{phonePush(testPane, stateDone)})
	f.checkActiveTimers(t, 0)
}

func TestJudgeFailureRoutesAsNeededWithReason(t *testing.T) {
	f := newFake()
	c := &canned{err: errors.New("synthetic judge failure")}
	r := newRouter(t, f)

	n := f.notice(testPane, stateDone, false)
	n.Judge = c.judge
	r.Notify(t.Context(), n)
	r.Wait()
	f.fire(t, route.JudgeDelay)
	r.Wait()

	c.checkCalls(t, 1)
	f.check(
		t,
		[]route.Line{
			lineOf(testPane, clockStart+10, stateDone, route.DeskPhone, "away; judge failed: synthetic judge failure"),
		},
		[]deskstream.Notice{deskPost(stateDone)},
		[]push.Notification{phonePush(testPane, stateDone)},
	)
	f.checkActiveTimers(t, 0)
}

func TestJudgeLateRoutesAsNeededWithReasonWhateverJudgeReturned(t *testing.T) {
	started := make(chan struct{})
	f := newFake()
	r := newRouter(t, f)

	n := f.notice(testPane, stateDone, false)
	n.Judge = func(ctx context.Context) (route.Verdict, error) {
		close(started)
		select {
		case <-ctx.Done():
			return route.Verdict{Needed: false, Why: testWhy}, nil
		case <-time.After(waitLimit):
			return route.Verdict{}, errors.New("synthetic context never canceled")
		}
	}
	r.Notify(t.Context(), n)
	r.Wait()
	done := fireAsync(t, f, route.JudgeDelay)
	select {
	case <-started:
	case <-time.After(waitLimit):
		t.Fatal("judge never started")
	}
	f.fire(t, route.JudgeTimeout)
	<-done
	r.Wait()

	f.check(t, []route.Line{lineOf(testPane, clockStart+25, stateDone, route.DeskPhone, "away; judge late")},
		[]deskstream.Notice{deskPost(stateDone)}, []push.Notification{phonePush(testPane, stateDone)})
	f.checkActiveTimers(t, 0)
}

func TestJudgeTimeoutCancelsJudgeContext(t *testing.T) {
	started := make(chan struct{})
	canceled := make(chan error, 1)
	f := newFake()
	r := newRouter(t, f)

	n := f.notice(testPane, stateDone, false)
	n.Judge = func(ctx context.Context) (route.Verdict, error) {
		close(started)
		select {
		case <-ctx.Done():
			canceled <- ctx.Err()
			return route.Verdict{}, ctx.Err()
		case <-time.After(waitLimit):
			return route.Verdict{}, errors.New("synthetic context never canceled")
		}
	}
	r.Notify(t.Context(), n)
	r.Wait()
	done := fireAsync(t, f, route.JudgeDelay)
	select {
	case <-started:
	case <-time.After(waitLimit):
		t.Fatal("judge never started")
	}
	f.fire(t, route.JudgeTimeout)
	<-done
	r.Wait()

	select {
	case err := <-canceled:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("judge context error = %v, want %v", err, context.Canceled)
		}
	default:
		t.Error("judge context was not canceled")
	}
	f.check(t, []route.Line{lineOf(testPane, clockStart+25, stateDone, route.DeskPhone, "away; judge late")},
		[]deskstream.Notice{deskPost(stateDone)}, []push.Notification{phonePush(testPane, stateDone)})
}

func TestJudgeEndedWhileRunningWritesNothingMore(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	f := newFake()
	r := newRouter(t, f)

	n := f.notice(testPane, stateDone, false)
	n.Judge = func(context.Context) (route.Verdict, error) {
		close(started)
		<-release
		return route.Verdict{Needed: false, Why: testWhy}, nil
	}
	r.Notify(t.Context(), n)
	r.Wait()
	done := fireAsync(t, f, route.JudgeDelay)
	select {
	case <-started:
	case <-time.After(waitLimit):
		t.Fatal("judge never started")
	}
	r.Changed(notice.Key{Pane: testPane})
	close(release)
	<-done
	r.Wait()

	f.check(t, []route.Line{lineOf(testPane, clockStart+10, stateDone, route.None, "changed")}, nil, nil)
	f.checkActiveTimers(t, 0)
}

func TestJudgeWatchedAfterDelayHoldsWithoutCallThenJudgedWithoutSecondDelay(t *testing.T) {
	f := newFake()
	c := &canned{verdict: route.Verdict{Needed: true}}
	r := newRouter(t, f)

	n := f.notice(testPane, stateDone, true)
	n.Judge = c.judge
	r.Notify(t.Context(), n)
	r.Wait()
	f.checkActiveTimers(t, 1)

	f.setWatching(true, nil)
	f.fire(t, route.JudgeDelay)
	r.Wait()
	c.checkCalls(t, 0)
	f.check(t, []route.Line{lineOf(testPane, clockStart+10, stateDone, route.None, "watching")}, nil, nil)
	f.checkActiveTimers(t, 1)

	f.setWatching(false, nil)
	f.fire(t, route.WatchRecheck)
	r.Wait()
	c.checkCalls(t, 1)
	f.check(t, []route.Line{
		lineOf(testPane, clockStart+10, stateDone, route.None, "watching"),
		lineOf(testPane, clockStart+15, stateDone, route.DeskPhone, "away; needed"),
	}, []deskstream.Notice{deskPost(stateDone)}, []push.Notification{phonePush(testPane, stateDone)})
	f.checkActiveTimers(t, 0)
}

func TestJudgeWaitEndedAsToday(t *testing.T) {
	cases := []struct {
		name string
		end  func(ctx context.Context, r *route.Router, key notice.Key)
		want []route.Line
	}{
		{
			name: "changed",
			end: func(_ context.Context, r *route.Router, key notice.Key) {
				r.Changed(key)
			},
			want: []route.Line{lineOf(testPane, clockStart, stateDone, route.None, "changed")},
		},
		{
			name: "seen",
			end: func(ctx context.Context, r *route.Router, key notice.Key) {
				r.Seen(ctx, key, clockStart)
			},
			want: []route.Line{lineOf(testPane, clockStart, stateDone, route.None, "seen")},
		},
		{
			name: "superseded",
			end: func(ctx context.Context, r *route.Router, key notice.Key) {
				r.Notify(ctx, route.Notice{Key: key, State: stateDone, Silent: quietReason})
			},
			want: []route.Line{
				lineOf(testPane, clockStart, stateDone, route.None, "changed"),
				lineOf(testPane, clockStart, stateDone, route.None, quietReason),
			},
		},
		{
			name: "closed",
			end: func(_ context.Context, r *route.Router, _ notice.Key) {
				r.Close()
			},
			want: []route.Line{lineOf(testPane, clockStart, stateDone, route.None, "shutdown")},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake()
			c := &canned{verdict: route.Verdict{Needed: false}}
			r := newRouter(t, f)

			n := f.notice(testPane, stateDone, false)
			n.Judge = c.judge
			r.Notify(t.Context(), n)
			r.Wait()
			tc.end(t.Context(), r, notice.Key{Pane: testPane})
			r.Wait()

			c.checkCalls(t, 0)
			f.check(t, tc.want, nil, nil)
			f.checkActiveTimers(t, 0)
		})
	}
}
