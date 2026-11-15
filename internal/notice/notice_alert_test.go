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

// testAlert names the alert the alert tests' notices are about.
const testAlert = "synthetic-alert"

// DeletePushedAlert records the alert it deletes, in call order, as DeletePushedLimit does for a limit.
func (r *deleteRecorder) DeletePushedAlert(ctx context.Context, alert string) error {
	r.deleted = append(r.deleted, "alert "+alert)
	return r.Store.DeletePushedAlert(ctx, alert)
}

func TestAlertSeenClearsOnlyAlertRow(t *testing.T) {
	const at = attachAt
	ctx := context.Background()
	s := openStore(ctx, t, filepath.Join(t.TempDir(), "orrery.db"))
	rows := &deleteRecorder{Store: s}
	sender := &fakeSender{}
	n := newNotices(sender, &clock{at: time.Unix(at, 0)})
	n.Attach(ctx, rows)
	alert := notice.Key{Alert: testAlert}

	n.Notify(ctx, notice.Key{Pane: "%3"}, build(push.Notification{Summary: "done"}), nil)
	n.Wait()
	n.Notify(ctx, alert, build(push.Notification{Summary: "firing"}), nil)
	n.Wait()
	wantRows(t, storedPushes(ctx, t, s),
		store.PushedRow{Pane: "%3", Time: at},
		store.PushedRow{Alert: testAlert, Time: at},
	)

	n.Seen(ctx, alert, at)
	n.Wait()

	wantCalls(t, sender.snapshot(),
		call{kind: "publish", note: push.Notification{Summary: "done", Host: "host-a", Pane: "%3", Time: at}},
		call{kind: "publish", note: push.Notification{Summary: "firing", Host: "host-a", Alert: testAlert, Time: at}},
		call{kind: "clear", clear: push.Clear{Host: "host-a", Alert: testAlert, Seen: at}},
	)
	wantRows(t, storedPushes(ctx, t, s), store.PushedRow{Pane: "%3", Time: at})
	if want := []string{"alert " + testAlert}; !reflect.DeepEqual(rows.deleted, want) {
		t.Fatalf("deletes = %q, want %q", rows.deleted, want)
	}
}

func TestAlertSeenAfterRestartClears(t *testing.T) {
	const at = attachAt
	ctx := context.Background()
	key := notice.Key{Alert: testAlert}
	after := openStore(ctx, t, storeWithPush(ctx, t, key))
	sender := &fakeSender{}
	n := newNotices(sender, &clock{at: time.Unix(at, 0)})

	n.Attach(ctx, after)
	n.Wait()
	wantRows(t, storedPushes(ctx, t, after), store.PushedRow{Alert: testAlert, Time: at})

	n.Seen(ctx, key, at)
	n.Wait()

	wantCalls(t, sender.snapshot(), call{kind: "clear", clear: push.Clear{Host: "host-a", Alert: testAlert, Seen: at}})
	wantRows(t, storedPushes(ctx, t, after))
}
