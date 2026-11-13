package notice_test

import (
	"context"
	"log/slog"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/notice"
	"github.com/joshsymonds/orrery/internal/push"
	"github.com/joshsymonds/orrery/internal/store"
)

// gatedRows is a store whose Pushed read reports on reading, then waits for release before it reads.
type gatedRows struct {
	*store.Store
	reading chan struct{}
	release chan struct{}
}

func (g gatedRows) Pushed(ctx context.Context) ([]store.PushedRow, error) {
	g.reading <- struct{}{}
	<-g.release
	return g.Store.Pushed(ctx)
}

// attachAt is the notice time of the push an earlier run left in the store.
const attachAt int64 = 1700000000

// storeWithPush returns the path of a new store holding key's row at attachAt, as an earlier run of Notices left it.
func storeWithPush(ctx context.Context, t *testing.T, key notice.Key) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s, err := store.Open(ctx, path)
	if err != nil {
		t.Fatalf("Open(%s): %v", path, err)
	}
	earlier := notice.New(
		"host-a",
		&fakeSender{},
		(&clock{at: time.Unix(attachAt, 0)}).now,
		slog.New(slog.DiscardHandler),
	)
	earlier.Attach(ctx, s)
	earlier.Notify(ctx, key, build(push.Notification{Summary: "done"}), nil)
	earlier.Wait()
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return path
}

// newNotices returns a Notices for the restarted daemon, publishing to sender and reading the time from clk.
func newNotices(sender *fakeSender, clk *clock) *notice.Notices {
	return notice.New("host-a", sender, clk.now, slog.New(slog.DiscardHandler))
}

// wantRows fails the test unless the store holds exactly want.
func wantRows(t *testing.T, got []store.PushedRow, want ...store.PushedRow) {
	t.Helper()
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %+v, want %+v", got, want)
	}
}

func TestAttachWindow(t *testing.T) {
	const at = attachAt
	ctx := context.Background()
	key := notice.Key{Pane: "%3"}
	notePublish := func(noticed int64) call {
		return call{kind: "publish", note: push.Notification{
			Summary: "done", Host: "host-a", Pane: "%3", Time: noticed,
		}}
	}
	clearAt := func(seen int64) call {
		return call{kind: "clear", clear: push.Clear{Host: "host-a", Pane: "%3", Seen: seen}}
	}

	t.Run("seen at the row time clears it once at Attach", func(t *testing.T) {
		after := openStore(ctx, t, storeWithPush(ctx, t, key))
		sender := &fakeSender{}
		n := newNotices(sender, &clock{at: time.Unix(at, 0)})

		n.Seen(ctx, key, at)
		n.Wait()
		wantCalls(t, sender.snapshot())

		n.Attach(ctx, after)
		n.Wait()
		wantCalls(t, sender.snapshot(), clearAt(at))
		wantRows(t, storedPushes(ctx, t, after))

		n.Seen(ctx, key, at)
		n.Wait()
		wantCalls(t, sender.snapshot(), clearAt(at))
	})

	t.Run("seen before the row time waits for a later seen", func(t *testing.T) {
		after := openStore(ctx, t, storeWithPush(ctx, t, key))
		sender := &fakeSender{}
		n := newNotices(sender, &clock{at: time.Unix(at, 0)})

		n.Seen(ctx, key, at-1)
		n.Wait()
		n.Attach(ctx, after)
		n.Wait()
		wantCalls(t, sender.snapshot())
		wantRows(t, storedPushes(ctx, t, after), store.PushedRow{Pane: "%3", Time: at})

		n.Seen(ctx, key, at-1)
		n.Wait()
		wantCalls(t, sender.snapshot())

		n.Seen(ctx, key, at)
		n.Wait()
		wantCalls(t, sender.snapshot(), clearAt(at))
		wantRows(t, storedPushes(ctx, t, after))
	})

	t.Run("seen after a push before Attach sends one clear in total", func(t *testing.T) {
		after := openStore(ctx, t, storeWithPush(ctx, t, key))
		sender := &fakeSender{}
		clk := &clock{at: time.Unix(at+5, 0)}
		n := newNotices(sender, clk)

		n.Notify(ctx, key, build(push.Notification{Summary: "done"}), nil)
		n.Seen(ctx, key, at+5)
		n.Wait()
		wantCalls(t, sender.snapshot(), notePublish(at+5), clearAt(at+5))

		n.Attach(ctx, after)
		n.Wait()
		wantCalls(t, sender.snapshot(), notePublish(at+5), clearAt(at+5))
		wantRows(t, storedPushes(ctx, t, after))
	})

	t.Run("push before Attach keeps only its new time", func(t *testing.T) {
		after := openStore(ctx, t, storeWithPush(ctx, t, key))
		sender := &fakeSender{}
		n := newNotices(sender, &clock{at: time.Unix(at+5, 0)})

		n.Notify(ctx, key, build(push.Notification{Summary: "done"}), nil)
		n.Wait()
		n.Attach(ctx, after)
		n.Wait()

		wantCalls(t, sender.snapshot(), notePublish(at+5))
		wantRows(t, storedPushes(ctx, t, after), store.PushedRow{Pane: "%3", Time: at + 5})
	})

	t.Run("push before Attach with no stored row is kept", func(t *testing.T) {
		after := openStore(ctx, t, filepath.Join(t.TempDir(), "orrery.db"))
		sender := &fakeSender{}
		n := newNotices(sender, &clock{at: time.Unix(at+5, 0)})
		sessionKey := notice.Key{Session: "s1"}

		n.Notify(ctx, sessionKey, build(push.Notification{Summary: "done"}), nil)
		n.Wait()
		n.Attach(ctx, after)
		n.Wait()

		wantCalls(t, sender.snapshot(), call{kind: "publish", note: push.Notification{
			Summary: "done", Host: "host-a", Session: "s1", Time: at + 5,
		}})
		wantRows(t, storedPushes(ctx, t, after), store.PushedRow{Session: "s1", Time: at + 5})
	})

	t.Run("seen during the read counts as before Attach", func(t *testing.T) {
		after := openStore(ctx, t, storeWithPush(ctx, t, key))
		sender := &fakeSender{}
		n := newNotices(sender, &clock{at: time.Unix(at, 0)})
		gated := gatedRows{Store: after, reading: make(chan struct{}, 1), release: make(chan struct{})}
		attached := make(chan struct{})
		go func() {
			n.Attach(ctx, gated)
			close(attached)
		}()

		<-gated.reading
		n.Seen(ctx, key, at)
		close(gated.release)
		<-attached
		n.Wait()

		wantCalls(t, sender.snapshot(), clearAt(at))
		wantRows(t, storedPushes(ctx, t, after))
	})

	t.Run("push during the read counts as before Attach", func(t *testing.T) {
		after := openStore(ctx, t, storeWithPush(ctx, t, key))
		sender := &fakeSender{}
		clk := &clock{at: time.Unix(at, 0)}
		n := newNotices(sender, clk)
		gated := gatedRows{Store: after, reading: make(chan struct{}, 1), release: make(chan struct{})}
		attached := make(chan struct{})
		go func() {
			n.Attach(ctx, gated)
			close(attached)
		}()

		<-gated.reading
		clk.at = time.Unix(at+5, 0)
		n.Notify(ctx, key, build(push.Notification{Summary: "done"}), nil)
		close(gated.release)
		<-attached
		n.Wait()

		wantCalls(t, sender.snapshot(), notePublish(at+5))
		wantRows(t, storedPushes(ctx, t, after), store.PushedRow{Pane: "%3", Time: at + 5})
	})

	t.Run("a lower seen after a covering one still clears at Attach", func(t *testing.T) {
		after := openStore(ctx, t, storeWithPush(ctx, t, key))
		sender := &fakeSender{}
		n := newNotices(sender, &clock{at: time.Unix(at, 0)})

		n.Seen(ctx, key, at+5)
		n.Seen(ctx, key, at-1)
		n.Attach(ctx, after)
		n.Wait()

		wantCalls(t, sender.snapshot(), clearAt(at+5))
		wantRows(t, storedPushes(ctx, t, after))
	})

	t.Run("Attach with no store after a seen sends nothing", func(t *testing.T) {
		storeWithPush(ctx, t, key)
		sender := &fakeSender{}
		n := newNotices(sender, &clock{at: time.Unix(at, 0)})

		n.Seen(ctx, key, at)
		n.Wait()
		n.Attach(ctx, nil)
		n.Wait()
		n.Seen(ctx, key, at)
		n.Wait()

		wantCalls(t, sender.snapshot())
	})
}
