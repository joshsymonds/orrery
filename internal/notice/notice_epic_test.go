package notice_test

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/notice"
	"github.com/joshsymonds/orrery/internal/push"
	"github.com/joshsymonds/orrery/internal/store"
)

func TestNotifyEpicPublishesAndStores(t *testing.T) {
	const at = 1700000000
	ctx := context.Background()
	key := notice.Key{Epic: "epic-a"}
	s := openStore(ctx, t, filepath.Join(t.TempDir(), "orrery.db"))
	clk := &clock{at: time.Unix(at, 0)}
	sender := &fakeSender{}
	n := notice.New("host-a", sender, clk.now, slog.New(slog.DiscardHandler))

	n.Attach(ctx, s)
	n.Notify(ctx, key, build(push.Notification{Summary: "parked: reason"}), nil)
	n.Wait()

	wantCalls(t, sender.snapshot(), call{kind: "publish", note: push.Notification{
		Summary: "parked: reason", Host: "host-a", Epic: "epic-a", Time: at,
	}})
	wantRows(t, storedPushes(ctx, t, s), store.PushedRow{Epic: "epic-a", Time: at})
}

func TestSeenEpicClearsAndDeletesRow(t *testing.T) {
	const at = 1700000000
	ctx := context.Background()
	key := notice.Key{Epic: "epic-a"}
	s := openStore(ctx, t, filepath.Join(t.TempDir(), "orrery.db"))
	clk := &clock{at: time.Unix(at, 0)}
	sender := &fakeSender{}
	n := notice.New("host-a", sender, clk.now, slog.New(slog.DiscardHandler))

	n.Attach(ctx, s)
	n.Notify(ctx, key, build(push.Notification{Summary: "parked: reason"}), nil)
	n.Wait()

	n.Seen(ctx, key, at-1)
	n.Wait()
	wantCalls(t, sender.snapshot(), call{kind: "publish", note: push.Notification{
		Summary: "parked: reason", Host: "host-a", Epic: "epic-a", Time: at,
	}})
	wantRows(t, storedPushes(ctx, t, s), store.PushedRow{Epic: "epic-a", Time: at})

	n.Seen(ctx, key, at)
	n.Wait()
	wantCalls(t, sender.snapshot(),
		call{kind: "publish", note: push.Notification{
			Summary: "parked: reason", Host: "host-a", Epic: "epic-a", Time: at,
		}},
		call{kind: "clear", clear: push.Clear{Host: "host-a", Epic: "epic-a", Seen: at}},
	)
	wantRows(t, storedPushes(ctx, t, s))
}

func TestEpicPushSurvivesRestartAndIsCleared(t *testing.T) {
	const at = 1700000000
	ctx := context.Background()
	key := notice.Key{Epic: "epic-a"}
	path := filepath.Join(t.TempDir(), "orrery.db")
	clk := &clock{at: time.Unix(at, 0)}
	sender := &fakeSender{}

	before, err := store.Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	first := notice.New("host-a", sender, clk.now, slog.New(slog.DiscardHandler))
	first.Attach(ctx, before)
	first.Notify(ctx, key, build(push.Notification{Summary: "parked: reason"}), nil)
	first.Wait()
	if err := before.Close(); err != nil {
		t.Fatalf("Close before restart: %v", err)
	}

	after := openStore(ctx, t, path)
	restarted := notice.New("host-a", sender, clk.now, slog.New(slog.DiscardHandler))
	restarted.Attach(ctx, after)

	wantPublish := call{kind: "publish", note: push.Notification{
		Summary: "parked: reason", Host: "host-a", Epic: "epic-a", Time: at,
	}}
	restarted.Seen(ctx, key, at-1)
	restarted.Wait()
	wantCalls(t, sender.snapshot(), wantPublish)
	wantRows(t, storedPushes(ctx, t, after), store.PushedRow{Epic: "epic-a", Time: at})

	restarted.Seen(ctx, key, at)
	restarted.Wait()
	wantCalls(t, sender.snapshot(), wantPublish,
		call{kind: "clear", clear: push.Clear{Host: "host-a", Epic: "epic-a", Seen: at}},
	)
	wantRows(t, storedPushes(ctx, t, after))
}
