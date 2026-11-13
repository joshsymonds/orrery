package enginehost

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/deskstream"
	"github.com/joshsymonds/orrery/internal/notice"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/push"
	"github.com/joshsymonds/orrery/internal/route"
	"github.com/joshsymonds/orrery/internal/store"
)

const (
	notifierSession   = "0123456789abcdef0123456789abcdef"
	notifierDir       = "/x/proj"
	notifierProject   = "proj"
	notifierDeskTitle = "proj · session 01234567"
)

// seenReport is one Seen call the fake Router received.
type seenReport struct {
	key  notice.Key
	seen int64
}

// fakeRouter records the notices, changes and seen reports it receives. canceled reports whether any Notify call saw
// its context canceled.
type fakeRouter struct {
	notices  []route.Notice
	changes  []notice.Key
	seens    []seenReport
	canceled bool
}

func (f *fakeRouter) Notify(ctx context.Context, n route.Notice) {
	f.notices = append(f.notices, n)
	f.canceled = f.canceled || ctx.Err() != nil
}

func (f *fakeRouter) Changed(key notice.Key) {
	f.changes = append(f.changes, key)
}

func (f *fakeRouter) Seen(_ context.Context, key notice.Key, seen int64) {
	f.seens = append(f.seens, seenReport{key: key, seen: seen})
}

func TestNotifyChangedHandsRouterSessionKey(t *testing.T) {
	router := &fakeRouter{}
	n := NewNotifier(NotifierConfig{Route: router})

	n.Changed(context.Background(), notifierSession)

	want := notice.Key{Session: notifierSession}
	if len(router.changes) != 1 || router.changes[0] != want {
		t.Fatalf("changes = %+v, want exactly [%+v]", router.changes, want)
	}
}

func TestNotifyHandsSessionNoticeToRouter(t *testing.T) {
	cases := []struct {
		name, state, body string
		wantBody          string
		wantPush          push.Notification
	}{
		{
			name:     "waiting with question",
			state:    "waiting",
			body:     "which file?",
			wantBody: "which file?",
			wantPush: push.Notification{
				Project:  notifierProject,
				State:    protocol.StateWaiting,
				Question: "which file?",
			},
		},
		{
			name:     "waiting without question",
			state:    "waiting",
			body:     "",
			wantBody: "waiting for input",
			wantPush: push.Notification{Project: notifierProject, State: protocol.StateWaiting},
		},
		{
			name:     "done with summary",
			state:    "done",
			body:     "all tests pass",
			wantBody: "all tests pass",
			wantPush: push.Notification{
				Project: notifierProject,
				State:   protocol.StateDone,
				Summary: "all tests pass",
			},
		},
		{
			name: "done without summary", state: "done", body: "", wantBody: "done",
			wantPush: push.Notification{Project: notifierProject, State: protocol.StateDone},
		},
		{
			name:     "failed with reason",
			state:    "failed",
			body:     "build broke",
			wantBody: "build broke",
			wantPush: push.Notification{
				Project: notifierProject,
				State:   protocol.State("failed"),
				Summary: "build broke",
			},
		},
		{
			name: "failed without body", state: "failed", body: "", wantBody: "failed",
			wantPush: push.Notification{Project: notifierProject, State: protocol.State("failed")},
		},
		{
			name:     "interrupted without body",
			state:    "interrupted",
			body:     "",
			wantBody: "interrupted",
			wantPush: push.Notification{
				Project: notifierProject,
				State:   protocol.State("interrupted"),
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			router := &fakeRouter{}
			n := NewNotifier(NotifierConfig{Route: router})
			event := Notice{
				Session: notifierSession,
				Dir:     notifierDir,
				State:   tc.state,
				Body:    tc.body,
			}
			n.Notify(context.Background(), event)

			if len(router.notices) != 1 {
				t.Fatalf("Notify calls = %d, want 1", len(router.notices))
			}
			got := router.notices[0]
			if got.Key != (notice.Key{Session: notifierSession}) || got.State != tc.state {
				t.Fatalf(
					"notice key and state = %+v and %q, want session key and %q",
					got.Key,
					got.State,
					tc.state,
				)
			}
			if got.Silent != "" || got.Watching != nil {
				t.Fatalf(
					"notice Silent = %q, Watching set = %t, want neither",
					got.Silent,
					got.Watching != nil,
				)
			}
			wantDesk := deskstream.Notice{
				State: tc.state,
				Title: notifierDeskTitle,
				Body:  tc.wantBody,
			}
			desk, err := got.Desk(context.Background())
			if err != nil || desk != wantDesk {
				t.Fatalf("desk notice = %+v, %v, want %+v", desk, err, wantDesk)
			}
			note, err := got.Push(context.Background())
			if err != nil || note != tc.wantPush {
				t.Fatalf("push notification = %+v, %v, want %+v", note, err, tc.wantPush)
			}
		})
	}
}

func TestNotifyHandsRouterNothingForWorkingAndStopped(t *testing.T) {
	for _, state := range []string{"working", "stopped"} {
		t.Run(state, func(t *testing.T) {
			router := &fakeRouter{}
			n := NewNotifier(NotifierConfig{Route: router})
			event := Notice{Session: notifierSession, Dir: notifierDir, State: state, Body: "text"}
			n.Notify(context.Background(), event)
			if len(router.notices) != 0 {
				t.Fatalf("state %q handed the router %+v, want nothing", state, router.notices)
			}
		})
	}
}

func TestNotifyNilRouteIsSkipped(t *testing.T) {
	n := NewNotifier(NotifierConfig{})
	n.Notify(
		t.Context(),
		Notice{Session: notifierSession, Dir: notifierDir, State: "done", Body: "ok"},
	)
	n.Seen(t.Context(), notifierSession, time.Unix(1, 0))
	n.Changed(t.Context(), notifierSession)
}

func TestNotifySeenForwardsSessionAndUnixTime(t *testing.T) {
	router := &fakeRouter{}
	n := NewNotifier(NotifierConfig{Route: router})

	seen := time.Unix(1_700_000_123, 500_000_000)
	n.Seen(context.Background(), notifierSession, seen)

	want := seenReport{key: notice.Key{Session: notifierSession}, seen: 1_700_000_123}
	if len(router.seens) != 1 || router.seens[0] != want {
		t.Fatalf("seen reports = %+v, want exactly [%+v]", router.seens, want)
	}
}

func TestNotifyIgnoresCancellationOfCallerContext(t *testing.T) {
	router := &fakeRouter{}
	n := NewNotifier(NotifierConfig{Route: router})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	n.Notify(ctx, Notice{Session: notifierSession, Dir: notifierDir, State: "done", Body: "ok"})

	if len(router.notices) != 1 {
		t.Fatalf("Notify calls after canceled Notify = %d, want 1", len(router.notices))
	}
	if router.canceled {
		t.Fatal("router saw the caller's cancellation")
	}
}

// sessionDesk is a desk the owner is at. It records the notices posted to it.
type sessionDesk struct {
	mu    sync.Mutex
	posts []deskstream.Notice
}

func (d *sessionDesk) Post(n deskstream.Notice) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.posts = append(d.posts, n)
}

func (d *sessionDesk) Desk() (int64, bool) { return fixedClock().Unix(), true }

// got returns a copy of the notices posted so far.
func (d *sessionDesk) got() []deskstream.Notice {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]deskstream.Notice(nil), d.posts...)
}

// sessionPhone records the pushes the phone is sent, each built as the phone builds it. buildErr joins the errors
// those builds returned.
type sessionPhone struct {
	mu       sync.Mutex
	pushes   []push.Notification
	buildErr error
}

func (p *sessionPhone) Notify(
	ctx context.Context,
	_ notice.Key,
	build func(context.Context) (push.Notification, error),
	_ func(),
) {
	note, err := build(ctx)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pushes = append(p.pushes, note)
	p.buildErr = errors.Join(p.buildErr, err)
}

func (p *sessionPhone) Seen(context.Context, notice.Key, int64) {}

// got returns a copy of the pushes sent so far and the errors their builds returned.
func (p *sessionPhone) got() ([]push.Notification, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]push.Notification(nil), p.pushes...), p.buildErr
}

// restartRig is a Router attached to rows, with the desk and phone it sends to and the timers it arms.
type restartRig struct {
	router *route.Router
	desk   *sessionDesk
	phone  *sessionPhone
	after  *fakeAfter
}

// newRestartRig returns a Router attached to rows that restores session notices with RestoreNotice. The owner is at
// the desk, the phone is subscribed, and the Router's clock is fixedClock.
func newRestartRig(t *testing.T, rows route.Rows) restartRig {
	t.Helper()
	rig := restartRig{desk: &sessionDesk{}, phone: &sessionPhone{}, after: &fakeAfter{}}
	rig.router = route.New(route.Config{
		Now:        fixedClock,
		After:      rig.after.After,
		Desk:       rig.desk,
		Phone:      rig.phone,
		Subscribed: func() (bool, error) { return true, nil },
		Log:        &fakeRouteLog{},
		Rebuild:    map[string]route.Rebuild{SessionSource: RestoreNotice},
	})
	rig.router.Attach(t.Context(), rows)
	return rig
}

// openRestartStore opens the store at path. It closes when the test ends, and closing it again is harmless.
func openRestartStore(t *testing.T, path string) *store.Store {
	t.Helper()
	st, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("store.Open(%q): %v", path, err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestSessionResumes(t *testing.T) {
	waiting := Notice{Session: notifierSession, Dir: notifierDir, State: "waiting", Body: "which file?"}

	t.Run("waiting notice escalates once after a restart", func(t *testing.T) {
		straight := newRestartRig(t, openRestartStore(t, filepath.Join(t.TempDir(), "orrery.db")))
		NewNotifier(NotifierConfig{Route: straight.router}).Notify(t.Context(), waiting)
		straight.router.Wait()
		if n := straight.after.armed(); n != 1 {
			t.Fatalf("timers armed without a restart = %d, want the escalation", n)
		}
		straight.after.fire(0)
		straight.router.Wait()
		want, buildErr := straight.phone.got()
		if buildErr != nil || len(want) != 1 {
			t.Fatalf("pushes without a restart = %+v, %v, want exactly one", want, buildErr)
		}

		path := filepath.Join(t.TempDir(), "orrery.db")
		first := openRestartStore(t, path)
		rig := newRestartRig(t, first)
		NewNotifier(NotifierConfig{Route: rig.router}).Notify(t.Context(), waiting)
		rig.router.Wait()
		if got := rig.desk.got(); len(got) != 1 {
			t.Fatalf("desk posts before the restart = %+v, want exactly one", got)
		}

		// The restart drops the router without Close and closes the store before it is opened again.
		if err := first.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		restarted := newRestartRig(t, openRestartStore(t, path))
		restarted.router.Wait()
		if got := restarted.desk.got(); len(got) != 0 {
			t.Fatalf("desk posts after the restart = %+v, want none", got)
		}
		if n := restarted.after.armed(); n != 1 {
			t.Fatalf("timers armed after the restart = %d, want the escalation", n)
		}
		restarted.after.fire(0)
		restarted.router.Wait()
		got, buildErr := restarted.phone.got()
		if buildErr != nil || !slices.Equal(got, want) {
			t.Fatalf("pushes after the restart = %+v, %v, want %+v", got, buildErr, want)
		}
	})

	t.Run("done notice still deciding posts its desk text after a restart", func(t *testing.T) {
		done := Notice{Session: notifierSession, Dir: notifierDir, State: "done", Body: "all tests pass"}
		capture := &fakeRouter{}
		NewNotifier(NotifierConfig{Route: capture}).Notify(t.Context(), done)
		if len(capture.notices) != 1 {
			t.Fatalf("Notify calls = %d, want 1", len(capture.notices))
		}
		src := capture.notices[0].Source

		// The row is the one Notify writes for a notice that has not decided yet. The process then stops before the
		// decision runs, so the row is still deciding at the restart.
		path := filepath.Join(t.TempDir(), "orrery.db")
		first := openRestartStore(t, path)
		if err := first.PutNotice(t.Context(), store.NoticeRow{
			ID:         1,
			Session:    notifierSession,
			State:      "done",
			Time:       fixedClock().Unix(),
			Phase:      "deciding",
			Due:        fixedClock().UnixMilli(),
			SourceKind: src.Kind,
			Source:     src.Data,
		}); err != nil {
			t.Fatalf("PutNotice: %v", err)
		}
		if err := first.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}

		restarted := newRestartRig(t, openRestartStore(t, path))
		restarted.router.Wait()
		want := deskstream.Notice{State: "done", Title: notifierDeskTitle, Body: "all tests pass"}
		if got := restarted.desk.got(); len(got) != 1 || got[0] != want {
			t.Fatalf("desk posts after the restart = %+v, want exactly [%+v]", got, want)
		}
	})
}

func TestRestoreNoticeRebuildsTheNoticeNotifySent(t *testing.T) {
	event := Notice{Session: notifierSession, Dir: notifierDir, State: "waiting", Body: "which file?"}
	capture := &fakeRouter{}
	NewNotifier(NotifierConfig{Route: capture}).Notify(t.Context(), event)
	if len(capture.notices) != 1 {
		t.Fatalf("Notify calls = %d, want 1", len(capture.notices))
	}
	sent := capture.notices[0]
	if sent.Source.Kind != SessionSource {
		t.Fatalf("source kind = %q, want %q", sent.Source.Kind, SessionSource)
	}

	restored, err := RestoreNotice(t.Context(), sent.Source.Data)
	if err != nil {
		t.Fatalf("RestoreNotice: %v", err)
	}
	if restored.Key != sent.Key || restored.State != sent.State {
		t.Fatalf(
			"restored key and state = %+v and %q, want %+v and %q",
			restored.Key,
			restored.State,
			sent.Key,
			sent.State,
		)
	}
	wantDesk, err := sent.Desk(t.Context())
	if err != nil {
		t.Fatalf("sent desk notice: %v", err)
	}
	if desk, err := restored.Desk(t.Context()); err != nil || desk != wantDesk {
		t.Fatalf("restored desk notice = %+v, %v, want %+v", desk, err, wantDesk)
	}
	wantPush, err := sent.Push(t.Context())
	if err != nil {
		t.Fatalf("sent push notification: %v", err)
	}
	if note, err := restored.Push(t.Context()); err != nil || note != wantPush {
		t.Fatalf("restored push notification = %+v, %v, want %+v", note, err, wantPush)
	}
}

func TestRestoreNoticeRejectsUndecodableData(t *testing.T) {
	for _, data := range []string{"{not json", "[]"} {
		_, err := RestoreNotice(t.Context(), []byte(data))
		if err == nil {
			t.Fatalf("RestoreNotice(%q) returned no error", data)
		}
		if !strings.Contains(err.Error(), SessionSource) {
			t.Fatalf("RestoreNotice(%q) error = %q, want it to name the %q source", data, err, SessionSource)
		}
	}
}
