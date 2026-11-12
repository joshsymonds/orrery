package enginehost

import (
	"context"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/deskstream"
	"github.com/joshsymonds/orrery/internal/notice"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/push"
	"github.com/joshsymonds/orrery/internal/route"
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
