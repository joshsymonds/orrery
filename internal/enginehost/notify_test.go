package enginehost

import (
	"context"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/deskstream"
	"github.com/joshsymonds/orrery/internal/notice"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/push"
)

const (
	notifierSession   = "0123456789abcdef0123456789abcdef"
	notifierDir       = "/x/proj"
	notifierProject   = "proj"
	notifierDeskTitle = "proj · session 01234567"
)

// fakeDesk records the notices it is posted.
type fakeDesk struct {
	posts []deskstream.Notice
}

func (f *fakeDesk) Post(n deskstream.Notice) {
	f.posts = append(f.posts, n)
}

// pushNotify is one Notify call the fake notice record received, with the notification its build returned.
type pushNotify struct {
	key  notice.Key
	note push.Notification
	err  error
}

// seenReport is one Seen call the fake notice record received.
type seenReport struct {
	key  notice.Key
	seen int64
}

// fakePushNotices records the notices and seen reports it receives. Notify runs build as the notice record does.
type fakePushNotices struct {
	notifies []pushNotify
	seens    []seenReport
	canceled bool
}

func (f *fakePushNotices) Notify(ctx context.Context, key notice.Key,
	build func(context.Context) (push.Notification, error)) {
	note, err := build(ctx)
	f.notifies = append(f.notifies, pushNotify{key: key, note: note, err: err})
	f.canceled = f.canceled || ctx.Err() != nil
}

func (f *fakePushNotices) Seen(_ context.Context, key notice.Key, seen int64) {
	f.seens = append(f.seens, seenReport{key: key, seen: seen})
}

func TestNotifyDeskPayload(t *testing.T) {
	cases := []struct {
		name, state, body, want string
	}{
		{"waiting with question", "waiting", "which file?", "which file?"},
		{"waiting without question", "waiting", "", "waiting for input"},
		{"done with summary", "done", "all tests pass", "all tests pass"},
		{"done without summary", "done", "", "done"},
		{"failed without body", "failed", "", "failed"},
		{"interrupted without body", "interrupted", "", "interrupted"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			desk := &fakeDesk{}
			n := NewNotifier(NotifierConfig{Desk: desk})
			event := Notice{Session: notifierSession, Dir: notifierDir, State: tc.state, Body: tc.body}
			n.Notify(context.Background(), event)
			want := deskstream.Notice{State: tc.state, Title: notifierDeskTitle, Body: tc.want}
			if len(desk.posts) != 1 || desk.posts[0] != want {
				t.Fatalf("desk posts = %+v, want exactly [%+v]", desk.posts, want)
			}
		})
	}
}

func TestNotifyHandsBuiltPushToNoticeRecord(t *testing.T) {
	cases := []struct {
		name, state, body string
		want              push.Notification
	}{
		{
			name: "waiting carries the body as the question", state: "waiting", body: "which file?",
			want: push.Notification{Project: notifierProject, State: protocol.StateWaiting, Question: "which file?"},
		},
		{
			name: "waiting with an empty body carries no question", state: "waiting", body: "",
			want: push.Notification{Project: notifierProject, State: protocol.StateWaiting},
		},
		{
			name: "done carries the body as the summary", state: "done", body: "all tests pass",
			want: push.Notification{Project: notifierProject, State: protocol.StateDone, Summary: "all tests pass"},
		},
		{
			name: "failed carries the body as the summary", state: "failed", body: "build broke",
			want: push.Notification{Project: notifierProject, State: protocol.State("failed"), Summary: "build broke"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pushes := &fakePushNotices{}
			n := NewNotifier(NotifierConfig{Push: pushes})
			event := Notice{Session: notifierSession, Dir: notifierDir, State: tc.state, Body: tc.body}
			n.Notify(context.Background(), event)
			// The hand-off is synchronous: the record already holds the call.
			want := pushNotify{key: notice.Key{Session: notifierSession}, note: tc.want}
			if len(pushes.notifies) != 1 || pushes.notifies[0] != want {
				t.Fatalf("notice record calls = %+v, want exactly [%+v]", pushes.notifies, want)
			}
		})
	}
}

func TestNotifySkipsWorkingAndStopped(t *testing.T) {
	for _, state := range []string{"working", "stopped"} {
		t.Run(state, func(t *testing.T) {
			pushes := &fakePushNotices{}
			desk := &fakeDesk{}
			n := NewNotifier(NotifierConfig{Push: pushes, Desk: desk})
			event := Notice{Session: notifierSession, Dir: notifierDir, State: state, Body: "text"}
			n.Notify(context.Background(), event)
			if len(pushes.notifies) != 0 || len(desk.posts) != 0 {
				t.Fatalf("state %q posted push=%+v desk=%+v, want nothing", state, pushes.notifies, desk.posts)
			}
		})
	}
}

func TestNotifyNilChannelIsSkipped(t *testing.T) {
	t.Run("push only", func(t *testing.T) {
		pushes := &fakePushNotices{}
		n := NewNotifier(NotifierConfig{Push: pushes})
		n.Notify(context.Background(), Notice{Session: notifierSession, Dir: notifierDir, State: "done", Body: "ok"})
		if len(pushes.notifies) != 1 {
			t.Fatalf("notice record calls = %d, want 1", len(pushes.notifies))
		}
	})
	t.Run("desk only", func(t *testing.T) {
		desk := &fakeDesk{}
		n := NewNotifier(NotifierConfig{Desk: desk})
		n.Notify(context.Background(), Notice{Session: notifierSession, Dir: notifierDir, State: "done", Body: "ok"})
		if len(desk.posts) != 1 {
			t.Fatalf("desk posts = %d, want 1", len(desk.posts))
		}
		// With no push channel, a seen report has nowhere to go and must not panic.
		n.Seen(context.Background(), notifierSession, time.Unix(1, 0))
	})
}

func TestNotifySeenForwardsSessionAndUnixTime(t *testing.T) {
	pushes := &fakePushNotices{}
	n := NewNotifier(NotifierConfig{Push: pushes})

	seen := time.Unix(1_700_000_123, 500_000_000)
	n.Seen(context.Background(), notifierSession, seen)

	want := seenReport{key: notice.Key{Session: notifierSession}, seen: 1_700_000_123}
	if len(pushes.seens) != 1 || pushes.seens[0] != want {
		t.Fatalf("seen reports = %+v, want exactly [%+v]", pushes.seens, want)
	}
}

func TestNotifyIgnoresCancellationOfCallerContext(t *testing.T) {
	pushes := &fakePushNotices{}
	n := NewNotifier(NotifierConfig{Push: pushes})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	n.Notify(ctx, Notice{Session: notifierSession, Dir: notifierDir, State: "done", Body: "ok"})

	if len(pushes.notifies) != 1 {
		t.Fatalf("notice record calls after canceled Notify = %d, want 1", len(pushes.notifies))
	}
	if pushes.canceled {
		t.Fatal("push hand-off saw the caller's cancellation")
	}
}
