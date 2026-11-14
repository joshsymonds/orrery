package notice_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/notice"
	"github.com/joshsymonds/orrery/internal/push"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/storetest"
)

// openStore opens a store at path and closes it when the test ends.
func openStore(ctx context.Context, t *testing.T, path string) *store.Store {
	t.Helper()
	storetest.Seed(t, path)
	s, err := store.Open(ctx, path)
	if err != nil {
		t.Fatalf("Open(%s): %v", path, err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return s
}

// storedPushes returns the rows s holds. It reports from any goroutine, so it uses Errorf.
func storedPushes(ctx context.Context, t *testing.T, s *store.Store) []store.PushedRow {
	t.Helper()
	got, err := s.Pushed(ctx)
	if err != nil {
		t.Errorf("Pushed: %v", err)
	}
	return got
}

// spySender runs onPublish and onPublishClear before the matching call on inner, so a test reads the store at the
// moment each send happens.
type spySender struct {
	inner          *fakeSender
	onPublish      func()
	onPublishClear func()
}

func (s *spySender) Publish(ctx context.Context, note push.Notification) error {
	s.onPublish()
	return s.inner.Publish(ctx, note)
}

func (s *spySender) PublishClear(ctx context.Context, c push.Clear) error {
	s.onPublishClear()
	return s.inner.PublishClear(ctx, c)
}

func TestPushedRowFollowsNotices(t *testing.T) {
	const at = 1700000000
	ctx := context.Background()
	s := openStore(ctx, t, filepath.Join(t.TempDir(), "orrery.db"))
	clk := &clock{at: time.Unix(at, 0)}
	inner := &fakeSender{}
	var atPublish, atClear []store.PushedRow
	sender := &spySender{
		inner:          inner,
		onPublish:      func() { atPublish = storedPushes(ctx, t, s) },
		onPublishClear: func() { atClear = storedPushes(ctx, t, s) },
	}
	n := notice.New("host-a", sender, clk.now, slog.New(slog.DiscardHandler))
	key := notice.Key{Pane: "%3"}

	n.Attach(ctx, s)
	n.Notify(ctx, key, build(push.Notification{Summary: "done"}), nil)
	n.Seen(ctx, key, at)
	n.Wait()

	wantRow := []store.PushedRow{{Pane: "%3", Time: at}}
	if !reflect.DeepEqual(atPublish, wantRow) {
		t.Fatalf("rows at Publish = %+v, want %+v", atPublish, wantRow)
	}
	if !reflect.DeepEqual(atClear, wantRow) {
		t.Fatalf("rows at PublishClear = %+v, want %+v (the row is deleted after the clear is sent)", atClear, wantRow)
	}
	if got := storedPushes(ctx, t, s); len(got) != 0 {
		t.Fatalf("rows after the clear = %+v, want none", got)
	}
	wantCalls(t, inner.snapshot(),
		call{kind: "publish", note: push.Notification{Summary: "done", Host: "host-a", Pane: "%3", Time: at}},
		call{kind: "clear", clear: push.Clear{Host: "host-a", Pane: "%3", Seen: at}},
	)
}

func TestSeenAfterRestartClears(t *testing.T) {
	const at = 1700000000
	ctx := context.Background()
	for _, key := range []notice.Key{{Pane: "%3"}, {Session: "session-a"}} {
		t.Run(key.Pane+key.Session, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "orrery.db")
			clk := &clock{at: time.Unix(at, 0)}
			sender := &fakeSender{}

			before, err := store.Open(ctx, path)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			first := notice.New("host-a", sender, clk.now, slog.New(slog.DiscardHandler))
			first.Attach(ctx, before)
			first.Notify(ctx, key, build(push.Notification{Summary: "done"}), nil)
			first.Wait()
			if err := before.Close(); err != nil {
				t.Fatalf("Close before restart: %v", err)
			}

			after := openStore(ctx, t, path)
			restarted := notice.New("host-a", sender, clk.now, slog.New(slog.DiscardHandler))
			restarted.Attach(ctx, after)

			wantPublish := call{kind: "publish", note: push.Notification{
				Summary: "done", Host: "host-a", Pane: key.Pane, Session: key.Session, Time: at,
			}}
			wantClear := call{kind: "clear", clear: push.Clear{
				Host: "host-a", Pane: key.Pane, Session: key.Session, Seen: at,
			}}

			restarted.Seen(ctx, key, at-1)
			restarted.Wait()
			wantCalls(t, sender.snapshot(), wantPublish)
			if got := storedPushes(ctx, t, after); len(got) != 1 {
				t.Fatalf("rows after a seen before the push = %+v, want the push's row kept", got)
			}

			restarted.Seen(ctx, key, at)
			restarted.Wait()
			wantCalls(t, sender.snapshot(), wantPublish, wantClear)

			restarted.Seen(ctx, key, at+10)
			restarted.Wait()
			wantCalls(t, sender.snapshot(), wantPublish, wantClear)
		})
	}
}

// stubRows fails every call with a disk error. When block is set it holds every call until the call's context ends.
type stubRows struct{ block bool }

func (s stubRows) call(ctx context.Context) error {
	if s.block {
		<-ctx.Done()
		return ctx.Err()
	}
	return errors.New("disk I/O error")
}

func (s stubRows) Pushed(ctx context.Context) ([]store.PushedRow, error) {
	return nil, s.call(ctx)
}

func (s stubRows) PutPushed(ctx context.Context, _ store.PushedRow) error {
	return s.call(ctx)
}

func (s stubRows) DeletePushed(ctx context.Context, _, _ string) error {
	return s.call(ctx)
}

func (s stubRows) DeletePushedEpic(ctx context.Context, _ string) error {
	return s.call(ctx)
}

func (s stubRows) DeletePushedLimit(ctx context.Context, _ string) error {
	return s.call(ctx)
}

func TestPushedRowStoreFails(t *testing.T) {
	const at = 1700000000
	ctx := context.Background()
	key := notice.Key{Pane: "%3"}
	for _, tc := range []struct {
		name    string
		rows    stubRows
		wantErr string
	}{
		{name: "fails", rows: stubRows{}, wantErr: "disk I/O error"},
		{name: "blocks until the deadline", rows: stubRows{block: true}, wantErr: "context deadline exceeded"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clk := &clock{at: time.Unix(at, 0)}
			sender := &fakeSender{}
			var logs bytes.Buffer
			n := notice.New("host-a", sender, clk.now, slog.New(slog.NewTextHandler(&logs, nil)))

			start := time.Now()
			n.Attach(ctx, tc.rows)
			if d := time.Since(start); d > time.Second {
				t.Fatalf("Attach took %v with a failing store, want under a second", d)
			}

			start = time.Now()
			n.Notify(ctx, key, build(push.Notification{Summary: "done"}), nil)
			n.Wait()
			if d := time.Since(start); d > time.Second {
				t.Fatalf("push took %v with a failing store, want under a second", d)
			}

			start = time.Now()
			n.Seen(ctx, key, at)
			n.Wait()
			if d := time.Since(start); d > time.Second {
				t.Fatalf("clear took %v with a failing store, want under a second", d)
			}

			wantCalls(t, sender.snapshot(),
				call{kind: "publish", note: push.Notification{Summary: "done", Host: "host-a", Pane: "%3", Time: at}},
				call{kind: "clear", clear: push.Clear{Host: "host-a", Pane: "%3", Seen: at}},
			)
			out := logs.String()
			for _, msg := range []string{"read pushed notices", "put pushed notice", "delete pushed notice", tc.wantErr} {
				if !strings.Contains(out, msg) {
					t.Fatalf("log = %q, want it to contain %q", out, msg)
				}
			}
		})
	}
}
