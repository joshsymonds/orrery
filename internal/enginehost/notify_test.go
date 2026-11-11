package enginehost

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/push"
)

const (
	notifierSession      = "0123456789abcdef0123456789abcdef"
	notifierShortSession = "01234567"
	notifierDir          = "/x/proj"
	notifierHost         = "ultraviolet"
	notifierProject      = "proj"
	// notifierGuard bounds each wait for a post. It is a hang guard, never a timing assumption.
	notifierGuard = 30 * time.Second
	// notifierHoldWindow is how long Wait is observed blocked while a post is still held open.
	notifierHoldWindow = 20 * time.Millisecond
)

// ntfyPost is one PublishSession call the fake ntfy publisher received.
type ntfyPost struct {
	project, session, state, body string
}

// fakeNtfy records the session posts it receives. A non-nil gate holds each post until it is closed.
type fakeNtfy struct {
	gate     <-chan struct{}
	err      error
	posts    []ntfyPost
	finished int
	canceled bool
}

func (f *fakeNtfy) PublishSession(ctx context.Context, project, session, state, body string) error {
	if f.gate != nil {
		<-f.gate
	}
	f.posts = append(f.posts, ntfyPost{project: project, session: session, state: state, body: body})
	f.canceled = f.canceled || ctx.Err() != nil
	f.finished++
	return f.err
}

// fakePush records the notifications it receives. A non-nil gate holds each post until it is closed.
type fakePush struct {
	gate     <-chan struct{}
	err      error
	notes    []push.Notification
	canceled bool
}

func (f *fakePush) Publish(ctx context.Context, note push.Notification) error {
	if f.gate != nil {
		<-f.gate
	}
	f.notes = append(f.notes, note)
	f.canceled = f.canceled || ctx.Err() != nil
	return f.err
}

// notifyAndWait posts notice through n and waits for every post to finish.
func notifyAndWait(n *Notifier, notice Notice) {
	n.Notify(context.Background(), notice)
	n.Wait()
}

func TestNotifyNtfyPayload(t *testing.T) {
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
			ntfy := &fakeNtfy{}
			n := NewNotifier(NotifierConfig{Ntfy: ntfy})
			notifyAndWait(n, Notice{Session: notifierSession, Dir: notifierDir, State: tc.state, Body: tc.body})
			want := ntfyPost{project: notifierProject, session: notifierShortSession, state: tc.state, body: tc.want}
			if len(ntfy.posts) != 1 || ntfy.posts[0] != want {
				t.Fatalf("ntfy posts = %+v, want exactly [%+v]", ntfy.posts, want)
			}
		})
	}
}

func TestNotifyPushPayload(t *testing.T) {
	cases := []struct {
		name, state, body string
		want              push.Notification
	}{
		{
			name: "waiting carries the body as the question", state: "waiting", body: "which file?",
			want: push.Notification{Host: notifierHost, Project: notifierProject, Session: notifierSession,
				State: protocol.StateWaiting, Question: "which file?"},
		},
		{
			name: "waiting with an empty body carries no question", state: "waiting", body: "",
			want: push.Notification{Host: notifierHost, Project: notifierProject, Session: notifierSession,
				State: protocol.StateWaiting},
		},
		{
			name: "done carries the body as the summary", state: "done", body: "all tests pass",
			want: push.Notification{Host: notifierHost, Project: notifierProject, Session: notifierSession,
				State: protocol.StateDone, Summary: "all tests pass"},
		},
		{
			name: "failed carries the body as the summary", state: "failed", body: "build broke",
			want: push.Notification{Host: notifierHost, Project: notifierProject, Session: notifierSession,
				State: protocol.State("failed"), Summary: "build broke"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pushes := &fakePush{}
			n := NewNotifier(NotifierConfig{Host: notifierHost, Push: pushes})
			notifyAndWait(n, Notice{Session: notifierSession, Dir: notifierDir, State: tc.state, Body: tc.body})
			if len(pushes.notes) != 1 || pushes.notes[0] != tc.want {
				t.Fatalf("push notes = %+v, want exactly [%+v]", pushes.notes, tc.want)
			}
		})
	}
}

func TestNotifySkipsWorkingAndStopped(t *testing.T) {
	for _, state := range []string{"working", "stopped"} {
		t.Run(state, func(t *testing.T) {
			ntfy := &fakeNtfy{}
			pushes := &fakePush{}
			n := NewNotifier(NotifierConfig{Ntfy: ntfy, Push: pushes})
			notifyAndWait(n, Notice{Session: notifierSession, Dir: notifierDir, State: state, Body: "text"})
			if len(ntfy.posts) != 0 || len(pushes.notes) != 0 {
				t.Fatalf("state %q posted ntfy=%+v push=%+v, want nothing", state, ntfy.posts, pushes.notes)
			}
		})
	}
}

func TestNotifyNilChannelIsSkipped(t *testing.T) {
	t.Run("push only", func(t *testing.T) {
		pushes := &fakePush{}
		n := NewNotifier(NotifierConfig{Host: notifierHost, Push: pushes})
		notifyAndWait(n, Notice{Session: notifierSession, Dir: notifierDir, State: "done", Body: "ok"})
		if len(pushes.notes) != 1 {
			t.Fatalf("push notes = %d, want 1", len(pushes.notes))
		}
	})
	t.Run("ntfy only", func(t *testing.T) {
		ntfy := &fakeNtfy{}
		n := NewNotifier(NotifierConfig{Ntfy: ntfy})
		notifyAndWait(n, Notice{Session: notifierSession, Dir: notifierDir, State: "done", Body: "ok"})
		if len(ntfy.posts) != 1 {
			t.Fatalf("ntfy posts = %d, want 1", len(ntfy.posts))
		}
	})
}

func TestNotifyReturnsAtOnceAndWaitWaitsForPosts(t *testing.T) {
	gate := make(chan struct{})
	ntfy := &fakeNtfy{gate: gate}
	n := NewNotifier(NotifierConfig{Ntfy: ntfy})

	notified := make(chan struct{})
	go func() {
		n.Notify(context.Background(), Notice{Session: notifierSession, Dir: notifierDir, State: "done", Body: "ok"})
		close(notified)
	}()
	select {
	case <-notified:
	case <-time.After(notifierGuard):
		t.Fatal("Notify blocked on the post instead of returning at once")
	}

	waited := make(chan struct{})
	go func() {
		n.Wait()
		close(waited)
	}()
	select {
	case <-waited:
		t.Fatal("Wait returned while a post was still held open")
	case <-time.After(notifierHoldWindow):
	}

	close(gate)
	select {
	case <-waited:
	case <-time.After(notifierGuard):
		t.Fatal("Wait did not return after the post finished")
	}
	if ntfy.finished != 1 {
		t.Fatalf("ntfy finished posts = %d, want 1 once Wait returns", ntfy.finished)
	}
}

func TestNotifyIgnoresCancellationOfCallerContext(t *testing.T) {
	ntfy := &fakeNtfy{}
	pushes := &fakePush{}
	n := NewNotifier(NotifierConfig{Ntfy: ntfy, Push: pushes})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	n.Notify(ctx, Notice{Session: notifierSession, Dir: notifierDir, State: "done", Body: "ok"})
	n.Wait()

	if len(ntfy.posts) != 1 || len(pushes.notes) != 1 {
		t.Fatalf("posts after canceled Notify: ntfy=%d push=%d, want 1 each", len(ntfy.posts), len(pushes.notes))
	}
	if ntfy.canceled {
		t.Fatal("ntfy post saw the caller's cancellation")
	}
	if pushes.canceled {
		t.Fatal("push post saw the caller's cancellation")
	}
}

func TestNotifyLogsPushFailureWithoutEndpoint(t *testing.T) {
	const endpoint = "https://push.example.test/send/secret-endpoint-token"
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	pushes := &fakePush{err: &url.Error{Op: "Post", URL: endpoint, Err: errors.New("connection refused")}}
	ntfy := &fakeNtfy{err: errors.New("ntfy unreachable")}
	n := NewNotifier(NotifierConfig{Host: notifierHost, Ntfy: ntfy, Push: pushes, Logger: logger})

	notifyAndWait(n, Notice{Session: notifierSession, Dir: notifierDir, State: "waiting", Body: "which file?"})

	if len(pushes.notes) != 1 || len(ntfy.posts) != 1 {
		t.Fatalf("a failing channel stopped the other: ntfy=%d push=%d, want 1 each",
			len(ntfy.posts), len(pushes.notes))
	}
	out := logs.String()
	if strings.Contains(out, "secret-endpoint-token") {
		t.Fatalf("log carries the push endpoint: %s", out)
	}
	if !strings.Contains(out, "connection refused") {
		t.Fatalf("push failure was not logged: %s", out)
	}
	if !strings.Contains(out, "ntfy unreachable") {
		t.Fatalf("ntfy failure was not logged: %s", out)
	}
}

func TestNotifyNilLoggerDiscardsFailures(t *testing.T) {
	pushes := &fakePush{err: errors.New("push down")}
	n := NewNotifier(NotifierConfig{Push: pushes})
	notifyAndWait(n, Notice{Session: notifierSession, Dir: notifierDir, State: "done", Body: "ok"})
	if len(pushes.notes) != 1 {
		t.Fatalf("push notes = %d, want 1", len(pushes.notes))
	}
}
