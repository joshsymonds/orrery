package notice_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/daemon"
	"github.com/joshsymonds/orrery/internal/notice"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/push"
)

// ioTimeout bounds each wait on a goroutine or socket exchange in these tests.
const ioTimeout = 5 * time.Second

// clearHold is how long TestClearNeverPrecedesNoticeWhenBuildBlocks holds the build after Seen returns.
const clearHold = 100 * time.Millisecond

// call is one Sender call, recorded in call order.
type call struct {
	kind  string
	note  push.Notification
	clear push.Clear
}

// fakeSender records every call in order. Publish signals published after each publish when it is set.
type fakeSender struct {
	mu        sync.Mutex
	calls     []call
	err       error
	published chan struct{}
}

func (f *fakeSender) Publish(_ context.Context, note push.Notification) error {
	f.mu.Lock()
	f.calls = append(f.calls, call{kind: "publish", note: note})
	f.mu.Unlock()
	if f.published != nil {
		f.published <- struct{}{}
	}
	return f.err
}

func (f *fakeSender) PublishClear(_ context.Context, c push.Clear) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call{kind: "clear", clear: c})
	return f.err
}

func (f *fakeSender) snapshot() []call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]call(nil), f.calls...)
}

// clock is a fixed clock whose time a test sets.
type clock struct{ at time.Time }

func (c *clock) now() time.Time { return c.at }

func build(note push.Notification) func(context.Context) (push.Notification, error) {
	return func(context.Context) (push.Notification, error) { return note, nil }
}

func wantCalls(t *testing.T, got []call, want ...call) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("calls = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("call %d = %+v, want %+v (all calls %+v)", i, got[i], want[i], got)
		}
	}
}

func TestNotifyThenSeenAtNoticeTimeClears(t *testing.T) {
	clk := &clock{at: time.Unix(1700000000, 0)}
	sender := &fakeSender{}
	n := notice.New("host-a", sender, clk.now, slog.New(slog.DiscardHandler))
	key := notice.Key{Pane: "%3"}
	at := clk.at.Unix()

	n.Notify(context.Background(), key, build(push.Notification{Summary: "done"}))
	n.Seen(context.Background(), key, at)
	n.Wait()

	wantCalls(t, sender.snapshot(),
		call{kind: "publish", note: push.Notification{Summary: "done", Host: "host-a", Pane: "%3", Time: at}},
		call{kind: "clear", clear: push.Clear{Host: "host-a", Pane: "%3", Seen: at}},
	)
}

func TestSeenBeforeNoticeTimeWaitsForLaterSeen(t *testing.T) {
	clk := &clock{at: time.Unix(1700000000, 0)}
	sender := &fakeSender{}
	n := notice.New("host-a", sender, clk.now, slog.New(slog.DiscardHandler))
	key := notice.Key{Pane: "%3"}
	at := clk.at.Unix()

	n.Notify(context.Background(), key, build(push.Notification{}))
	n.Seen(context.Background(), key, at-1)
	n.Wait()
	wantCalls(t, sender.snapshot(),
		call{kind: "publish", note: push.Notification{Host: "host-a", Pane: "%3", Time: at}},
	)

	n.Seen(context.Background(), key, at)
	n.Wait()
	wantCalls(t, sender.snapshot(),
		call{kind: "publish", note: push.Notification{Host: "host-a", Pane: "%3", Time: at}},
		call{kind: "clear", clear: push.Clear{Host: "host-a", Pane: "%3", Seen: at}},
	)
}

func TestSecondSeenSendsNothing(t *testing.T) {
	clk := &clock{at: time.Unix(1700000000, 0)}
	sender := &fakeSender{}
	n := notice.New("host-a", sender, clk.now, slog.New(slog.DiscardHandler))
	key := notice.Key{Pane: "%3"}
	at := clk.at.Unix()

	n.Notify(context.Background(), key, build(push.Notification{}))
	n.Seen(context.Background(), key, at)
	n.Seen(context.Background(), key, at+10)
	n.Wait()

	wantCalls(t, sender.snapshot(),
		call{kind: "publish", note: push.Notification{Host: "host-a", Pane: "%3", Time: at}},
		call{kind: "clear", clear: push.Clear{Host: "host-a", Pane: "%3", Seen: at}},
	)
}

func TestClearNeverPrecedesNoticeWhenBuildBlocks(t *testing.T) {
	clk := &clock{at: time.Unix(1700000000, 0)}
	sender := &fakeSender{}
	n := notice.New("host-a", sender, clk.now, slog.New(slog.DiscardHandler))
	key := notice.Key{Session: "s1"}
	at := clk.at.Unix()
	release := make(chan struct{})

	n.Notify(context.Background(), key, func(context.Context) (push.Notification, error) {
		<-release
		return push.Notification{Summary: "waiting"}, nil
	})
	seenReturned := make(chan struct{})
	go func() {
		n.Seen(context.Background(), key, at)
		close(seenReturned)
	}()
	select {
	case <-seenReturned:
	case <-time.After(ioTimeout):
		t.Fatal("Seen blocked on a notice whose build is still running")
	}
	// Hold the build long enough for an unordered clear to reach the sender first, so broken ordering fails here
	// every time rather than only when the scheduler happens to run the clear early.
	time.Sleep(clearHold)
	close(release)
	n.Wait()

	wantCalls(t, sender.snapshot(),
		call{kind: "publish", note: push.Notification{Summary: "waiting", Host: "host-a", Session: "s1", Time: at}},
		call{kind: "clear", clear: push.Clear{Host: "host-a", Session: "s1", Seen: at}},
	)
}

func TestSessionKeysClearBySession(t *testing.T) {
	clk := &clock{at: time.Unix(1700000000, 0)}
	sender := &fakeSender{}
	n := notice.New("host-a", sender, clk.now, slog.New(slog.DiscardHandler))
	at := clk.at.Unix()

	n.Notify(context.Background(), notice.Key{Session: "s1"}, build(push.Notification{}))
	n.Seen(context.Background(), notice.Key{Session: "s1"}, at)
	n.Wait()

	wantCalls(t, sender.snapshot(),
		call{kind: "publish", note: push.Notification{Host: "host-a", Session: "s1", Time: at}},
		call{kind: "clear", clear: push.Clear{Host: "host-a", Session: "s1", Seen: at}},
	)
}

func TestKeysDoNotWaitOnEachOther(t *testing.T) {
	clk := &clock{at: time.Unix(1700000000, 0)}
	sender := &fakeSender{published: make(chan struct{}, 2)}
	n := notice.New("host-a", sender, clk.now, slog.New(slog.DiscardHandler))
	release := make(chan struct{})

	n.Notify(context.Background(), notice.Key{Pane: "%1"}, func(context.Context) (push.Notification, error) {
		<-release
		return push.Notification{}, nil
	})
	n.Notify(context.Background(), notice.Key{Pane: "%2"}, build(push.Notification{}))

	select {
	case <-sender.published:
	case <-time.After(ioTimeout):
		t.Fatal("pane %2 was held behind pane %1's blocked build")
	}
	close(release)
	n.Wait()
	if got := sender.snapshot(); len(got) != 2 || got[0].note.Pane != "%2" || got[1].note.Pane != "%1" {
		t.Fatalf("calls = %+v, want %%2 then %%1", got)
	}
}

func TestSendsIgnoreContextCancellation(t *testing.T) {
	clk := &clock{at: time.Unix(1700000000, 0)}
	sender := &fakeSender{}
	n := notice.New("host-a", sender, clk.now, slog.New(slog.DiscardHandler))
	key := notice.Key{Pane: "%3"}
	at := clk.at.Unix()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	n.Notify(ctx, key, func(buildCtx context.Context) (push.Notification, error) {
		if err := buildCtx.Err(); err != nil {
			return push.Notification{}, err
		}
		return push.Notification{Summary: "done"}, nil
	})
	n.Seen(ctx, key, at)
	n.Wait()

	wantCalls(t, sender.snapshot(),
		call{kind: "publish", note: push.Notification{Summary: "done", Host: "host-a", Pane: "%3", Time: at}},
		call{kind: "clear", clear: push.Clear{Host: "host-a", Pane: "%3", Seen: at}},
	)
}

func TestSendErrorsAreLoggedWithRedactedEndpoint(t *testing.T) {
	clk := &clock{at: time.Unix(1700000000, 0)}
	const endpoint = "https://push.example.test/subscription/secret"
	sender := &fakeSender{err: &url.Error{Op: "Post", URL: endpoint, Err: errors.New("connection refused")}}
	var logs bytes.Buffer
	n := notice.New("host-a", sender, clk.now, slog.New(slog.NewTextHandler(&logs, nil)))

	n.Notify(context.Background(), notice.Key{Pane: "%3"}, build(push.Notification{}))
	n.Wait()

	out := logs.String()
	if !strings.Contains(out, "connection refused") {
		t.Fatalf("log = %q, want the send error", out)
	}
	if strings.Contains(out, endpoint) || strings.Contains(out, "secret") {
		t.Fatalf("log = %q, leaks the endpoint", out)
	}
}

func TestDaemonSeenReportClearsRecordedPaneNotice(t *testing.T) {
	const seenAt = 1700000000
	clk := &clock{at: time.Unix(seenAt, 0)}
	sender := &fakeSender{}
	n := notice.New("host-a", sender, clk.now, slog.New(slog.DiscardHandler))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	socketPath := filepath.Join(t.TempDir(), "orreryd.sock")
	ln, err := daemon.Listen(socketPath)
	if err != nil {
		t.Fatalf("Listen(%s): %v", socketPath, err)
	}
	serveDone := make(chan error, 1)
	reports := make(chan daemon.SeenReport, 1)
	go func() {
		serveDone <- daemon.Serve(ctx, ln, daemon.Config{
			Seen: func(report daemon.SeenReport) {
				n.Seen(ctx, notice.Key{Pane: report.PaneID}, report.Seen)
				reports <- report
			},
		})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-serveDone:
			if err != nil {
				t.Errorf("Serve: %v", err)
			}
		case <-time.After(ioTimeout):
			t.Errorf("Serve did not return after cancel")
		}
	})

	n.Notify(ctx, notice.Key{Pane: "%7"}, build(push.Notification{Summary: "waiting"}))
	sendSeenFrame(t, socketPath, protocol.HookFrame{TmuxPane: "%7", Seen: seenAt})
	select {
	case report := <-reports:
		if report != (daemon.SeenReport{PaneID: "%7", Seen: seenAt}) {
			t.Fatalf("seen report = %+v, want pane %%7 at %d", report, seenAt)
		}
	case <-time.After(ioTimeout):
		t.Fatal("daemon did not pass the seen report to Config.Seen")
	}
	n.Wait()

	wantCalls(t, sender.snapshot(),
		call{kind: "publish", note: push.Notification{Summary: "waiting", Host: "host-a", Pane: "%7", Time: seenAt}},
		call{kind: "clear", clear: push.Clear{Host: "host-a", Pane: "%7", Seen: seenAt}},
	)
}

// sendSeenFrame writes frame to the daemon socket and returns once the daemon closes the connection. The daemon
// closes it after the seen report is handed to Config.Seen, so this return is the synchronization point.
func sendSeenFrame(t *testing.T, socketPath string, frame protocol.HookFrame) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), ioTimeout)
	defer cancel()
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "unix", socketPath)
	if err != nil {
		t.Fatalf("dial %s: %v", socketPath, err)
	}
	defer func() {
		if err := conn.Close(); err != nil {
			t.Errorf("close connection: %v", err)
		}
	}()
	var buf bytes.Buffer
	if err := protocol.WriteFrame(&buf, frame); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	if _, err := conn.Write(buf.Bytes()); err != nil {
		t.Fatalf("write seen frame: %v", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(ioTimeout)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	_, err = conn.Read(make([]byte, 1))
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		t.Fatalf("daemon did not close the connection: %v", err)
	}
}
