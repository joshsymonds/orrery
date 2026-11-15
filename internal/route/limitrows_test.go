package route_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/joshsymonds/orrery/internal/deskstream"
	"github.com/joshsymonds/orrery/internal/notice"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/push"
	"github.com/joshsymonds/orrery/internal/route"
	"github.com/joshsymonds/orrery/internal/store"
)

// testLimit names the usage limit the limit tests' notices are about.
const testLimit = "synthetic-limit@1700000000"

// limitNotice is a sourced notice about testLimit. Its desk and push builds succeed.
func limitNotice() route.Notice {
	return sourced(route.Notice{
		Key:   notice.Key{Limit: testLimit},
		State: stateWaiting,
		Desk: func(context.Context) (deskstream.Notice, error) {
			return deskPost(stateWaiting), nil
		},
		Push: func(context.Context) (push.Notification, error) {
			return limitPush(), nil
		},
	})
}

// limitPush is the phone push of limitNotice.
func limitPush() push.Notification {
	return push.Notification{Limit: testLimit, Summary: testSummary, State: protocol.State(stateWaiting)}
}

// limitDeskRow is the row limitNotice keeps while it waits at the desk, recorded at clockStart.
func limitDeskRow() store.NoticeRow {
	row := rowFor(1, "", stateWaiting, clockStart, rowDesk, (clockStart+escalationSeconds)*1000)
	row.Limit = testLimit
	return row
}

// parkLimitAtDesk routes limitNotice to the desk, closes its router with the notice still pending, and returns the
// store reopened after that restart.
func parkLimitAtDesk(t *testing.T) *store.Store {
	t.Helper()
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openStore(t, path)
	f := newFake()
	f.setDesk(clockStart)
	r := newRestartRouter(t, f, nil)
	r.Attach(ctx, s)

	r.Notify(ctx, limitNotice())
	r.Wait()
	f.check(t, []route.Line{lineOf("", clockStart, stateWaiting, route.Desk, "at the desk")},
		[]deskstream.Notice{deskPost(stateWaiting)}, nil)
	checkRows(t, s, limitDeskRow())

	r.Close()
	return reopenStore(t, s, path)
}

func TestLimitRowRestoredUnderLimitKey(t *testing.T) {
	t.Run("a limit notice pending at a restart resumes under its limit key", func(t *testing.T) {
		reopened := parkLimitAtDesk(t)

		f := fakeAt(clockStart)
		rebuilds := 0
		rebuild := func(context.Context, []byte, int64) (route.Notice, error) {
			rebuilds++
			return limitNotice(), nil
		}
		r := restoreAt(t, f, map[string]route.Rebuild{testSourceKind: rebuild}, reopened)
		r.Wait()

		if rebuilds != 1 {
			t.Errorf("rebuilds = %d, want 1", rebuilds)
		}
		f.check(t, nil, nil, nil)
		f.checkActiveTimers(t, 1)
		checkRows(t, reopened, limitDeskRow())

		f.fire(t, route.Escalation)
		r.Wait()
		f.check(t, []route.Line{
			lineOf("", clockStart+escalationSeconds, stateWaiting, route.Phone, "unseen after 5 minutes"),
		}, nil, []push.Notification{limitPush()})
		f.publish()
		checkRows(t, reopened)
	})

	t.Run("a restored limit notice is ended by a change under its limit key", func(t *testing.T) {
		reopened := parkLimitAtDesk(t)

		f := fakeAt(clockStart)
		r := restoreAt(t, f, map[string]route.Rebuild{testSourceKind: rebuildAs(limitNotice())}, reopened)
		r.Wait()

		r.Changed(notice.Key{Limit: testLimit})
		f.check(t, []route.Line{lineOf("", clockStart, stateWaiting, route.None, "changed")}, nil, nil)
		f.checkActiveTimers(t, 0)
		checkRows(t, reopened)
	})
}
