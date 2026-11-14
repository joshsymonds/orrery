package notice_test

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/notice"
	"github.com/joshsymonds/orrery/internal/push"
	"github.com/joshsymonds/orrery/internal/store"
)

// limitName is the key of a usage limit notice: the limit's name and its reset time.
const limitName = "seven_day@1900000000"

// deleteRecorder is a store that records the key each delete names, in call order.
type deleteRecorder struct {
	*store.Store
	deleted []string
}

func (r *deleteRecorder) DeletePushed(ctx context.Context, pane, session string) error {
	r.deleted = append(r.deleted, "pane "+pane+" session "+session)
	return r.Store.DeletePushed(ctx, pane, session)
}

func (r *deleteRecorder) DeletePushedLimit(ctx context.Context, limit string) error {
	r.deleted = append(r.deleted, "limit "+limit)
	return r.Store.DeletePushedLimit(ctx, limit)
}

func TestLimitNoticePublishesAndStores(t *testing.T) {
	const at = attachAt
	ctx := context.Background()
	s := openStore(ctx, t, filepath.Join(t.TempDir(), "orrery.db"))
	sender := &fakeSender{}
	n := newNotices(sender, &clock{at: time.Unix(at, 0)})
	n.Attach(ctx, s)

	n.Notify(ctx, notice.Key{Limit: limitName}, build(push.Notification{Summary: "done"}), nil)
	n.Wait()

	wantCalls(t, sender.snapshot(), call{kind: "publish", note: push.Notification{
		Summary: "done", Host: "host-a", Limit: limitName, Time: at,
	}})
	wantRows(t, storedPushes(ctx, t, s), store.PushedRow{Limit: limitName, Time: at})
}

func TestLimitSeenClearsOnlyLimitRow(t *testing.T) {
	const at = attachAt
	ctx := context.Background()
	s := openStore(ctx, t, filepath.Join(t.TempDir(), "orrery.db"))
	rows := &deleteRecorder{Store: s}
	sender := &fakeSender{}
	n := newNotices(sender, &clock{at: time.Unix(at, 0)})
	n.Attach(ctx, rows)
	limit := notice.Key{Limit: limitName}

	n.Notify(ctx, notice.Key{Pane: "%3"}, build(push.Notification{Summary: "done"}), nil)
	n.Wait()
	n.Notify(ctx, limit, build(push.Notification{Summary: "limit"}), nil)
	n.Wait()
	wantRows(t, storedPushes(ctx, t, s),
		store.PushedRow{Pane: "%3", Time: at},
		store.PushedRow{Limit: limitName, Time: at},
	)

	n.Seen(ctx, limit, at)
	n.Wait()

	wantCalls(t, sender.snapshot(),
		call{kind: "publish", note: push.Notification{Summary: "done", Host: "host-a", Pane: "%3", Time: at}},
		call{kind: "publish", note: push.Notification{Summary: "limit", Host: "host-a", Limit: limitName, Time: at}},
		call{kind: "clear", clear: push.Clear{Host: "host-a", Limit: limitName, Seen: at}},
	)
	wantRows(t, storedPushes(ctx, t, s), store.PushedRow{Pane: "%3", Time: at})
	if want := []string{"limit " + limitName}; !reflect.DeepEqual(rows.deleted, want) {
		t.Fatalf("deletes = %q, want %q", rows.deleted, want)
	}
}

func TestLimitSeenAfterRestartClears(t *testing.T) {
	const at = attachAt
	ctx := context.Background()
	key := notice.Key{Limit: limitName}
	wantClear := call{kind: "clear", clear: push.Clear{Host: "host-a", Limit: limitName, Seen: at}}

	t.Run("seen after Attach", func(t *testing.T) {
		after := openStore(ctx, t, storeWithPush(ctx, t, key))
		sender := &fakeSender{}
		n := newNotices(sender, &clock{at: time.Unix(at, 0)})
		n.Attach(ctx, after)
		n.Wait()
		wantRows(t, storedPushes(ctx, t, after), store.PushedRow{Limit: limitName, Time: at})

		n.Seen(ctx, key, at)
		n.Wait()

		wantCalls(t, sender.snapshot(), wantClear)
		wantRows(t, storedPushes(ctx, t, after))
	})

	t.Run("seen before Attach", func(t *testing.T) {
		after := openStore(ctx, t, storeWithPush(ctx, t, key))
		sender := &fakeSender{}
		n := newNotices(sender, &clock{at: time.Unix(at, 0)})
		n.Seen(ctx, key, at)
		n.Wait()
		wantCalls(t, sender.snapshot())

		n.Attach(ctx, after)
		n.Wait()

		wantCalls(t, sender.snapshot(), wantClear)
		wantRows(t, storedPushes(ctx, t, after))
	})
}
