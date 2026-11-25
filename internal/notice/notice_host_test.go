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

// TestNoticeKeyHostSeparates checks that two hosts' panes with the same id keep two notices: a Seen of one clears and
// deletes only its own row, and each push carries its key's host.
func TestNoticeKeyHostSeparates(t *testing.T) {
	const at = 1700000000
	ctx := context.Background()
	s := openStore(ctx, t, filepath.Join(t.TempDir(), "orrery.db"))
	clk := &clock{at: time.Unix(at, 0)}
	sender := &fakeSender{}
	n := newTestNotices(sender, clk.now, slog.New(slog.DiscardHandler))
	hostA := notice.Key{Pane: "%3", Host: "host-a"}
	hostB := notice.Key{Pane: "%3", Host: "host-b"}

	n.Attach(ctx, s)
	// Each send is waited for before the next, so the calls come in a known order.
	n.Notify(ctx, hostA, build(push.Notification{Summary: "done"}), nil)
	n.Wait()
	n.Notify(ctx, hostB, build(push.Notification{Summary: "waiting"}), nil)
	n.Wait()
	wantRows(t, storedPushes(ctx, t, s),
		store.PushedRow{Host: "host-a", Pane: "%3", Time: at},
		store.PushedRow{Host: "host-b", Pane: "%3", Time: at},
	)

	n.Seen(ctx, hostA, at)
	n.Wait()
	wantRows(t, storedPushes(ctx, t, s), store.PushedRow{Host: "host-b", Pane: "%3", Time: at})
	wantCalls(t, sender.snapshot(),
		call{kind: "publish", note: push.Notification{Summary: "done", Host: "host-a", Pane: "%3", Time: at}},
		call{kind: "publish", note: push.Notification{Summary: "waiting", Host: "host-b", Pane: "%3", Time: at}},
		call{kind: "clear", clear: push.Clear{Host: "host-a", Pane: "%3", Seen: at}},
	)
}
