package route_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/joshsymonds/orrery/internal/push"
	"github.com/joshsymonds/orrery/internal/route"
)

// deskSourceData is the source data of the desk row TestRestartResendsSending stores beside the sending row.
const deskSourceData = "synthetic desk source"

func TestRestartResendsSending(t *testing.T) {
	t.Run("loud", func(t *testing.T) {
		ctx := t.Context()
		path := filepath.Join(t.TempDir(), "orrery.db")
		s := openStore(t, path)
		// The row of a push handed off before the stop, not yet published.
		if err := s.PutNotice(ctx, rowFor(1, testPane, stateWaiting, clockStart, rowSending, 0)); err != nil {
			t.Fatalf("put row: %v", err)
		}
		reopened := reopenStore(t, s, path)

		f := fakeAt(clockStart + 30)
		f.setSubscribed(false, nil)
		rebuilds := map[string]route.Rebuild{testSourceKind: rebuildAs(f.notice(testPane, stateWaiting, false))}
		r := restoreAt(t, f, rebuilds, reopened)
		r.Wait()

		f.check(t, []route.Line{lineOf(testPane, clockStart+30, stateWaiting, route.Phone, "resent after restart")},
			nil, []push.Notification{phonePush(testPane, stateWaiting)})
		f.checkActiveTimers(t, 0)
		checkRows(t, reopened, rowFor(1, testPane, stateWaiting, clockStart, rowSending, 0))

		f.publish()
		checkRows(t, reopened)
	})

	t.Run("silent", func(t *testing.T) {
		ctx := t.Context()
		path := filepath.Join(t.TempDir(), "orrery.db")
		s := openStore(t, path)
		// The row of a silent push handed off before the stop, not yet published.
		if err := s.PutNotice(ctx, withSilent(rowFor(1, testPane, stateDone, clockStart, rowSending, 0))); err != nil {
			t.Fatalf("put row: %v", err)
		}
		reopened := reopenStore(t, s, path)

		f := fakeAt(clockStart + 30)
		f.setSubscribed(false, nil)
		rebuilds := map[string]route.Rebuild{testSourceKind: rebuildAs(f.notice(testPane, stateDone, false))}
		r := restoreAt(t, f, rebuilds, reopened)
		r.Wait()

		f.check(t, []route.Line{lineOf(testPane, clockStart+30, stateDone, route.Phone, "resent after restart")},
			nil, []push.Notification{silentDonePush()})
		f.checkActiveTimers(t, 0)
		checkRows(t, reopened, withSilent(rowFor(1, testPane, stateDone, clockStart, rowSending, 0)))

		f.publish()
		checkRows(t, reopened)
	})

	t.Run("a desk row beside it is not posted again", func(t *testing.T) {
		ctx := t.Context()
		path := filepath.Join(t.TempDir(), "orrery.db")
		s := openStore(t, path)
		if err := s.PutNotice(ctx, rowFor(1, testPane, stateWaiting, clockStart, rowSending, 0)); err != nil {
			t.Fatalf("put sending row: %v", err)
		}
		desk := rowFor(2, otherPane, stateDone, clockStart, rowDesk, (clockStart+escalationSeconds)*1000)
		desk.Source = []byte(deskSourceData)
		if err := s.PutNotice(ctx, desk); err != nil {
			t.Fatalf("put desk row: %v", err)
		}
		reopened := reopenStore(t, s, path)

		f := fakeAt(clockStart + 30)
		rebuild := func(_ context.Context, data []byte, _ int64) (route.Notice, error) {
			if string(data) == deskSourceData {
				return f.notice(otherPane, stateDone, false), nil
			}
			return f.notice(testPane, stateWaiting, false), nil
		}
		r := restoreAt(t, f, map[string]route.Rebuild{testSourceKind: rebuild}, reopened)
		r.Wait()

		f.check(t, []route.Line{lineOf(testPane, clockStart+30, stateWaiting, route.Phone, "resent after restart")},
			nil, []push.Notification{phonePush(testPane, stateWaiting)})
		// The desk row waits for its escalation, and that is the only timer.
		f.checkActiveTimers(t, 1)
		checkRows(t, reopened, rowFor(1, testPane, stateWaiting, clockStart, rowSending, 0), desk)

		f.publish()
		checkRows(t, reopened, desk)
	})

	t.Run("a row whose key saw a Notify before Attach ends changed with no push", func(t *testing.T) {
		ctx := t.Context()
		path := filepath.Join(t.TempDir(), "orrery.db")
		s := openStore(t, path)
		if err := s.PutNotice(ctx, rowFor(1, testPane, stateWaiting, clockStart, rowSending, 0)); err != nil {
			t.Fatalf("put row: %v", err)
		}
		reopened := reopenStore(t, s, path)

		f := fakeAt(clockStart + 30)
		rebuilds := map[string]route.Rebuild{testSourceKind: rebuildAs(f.notice(testPane, stateWaiting, false))}
		r := newRestoreRouter(f, rebuilds, nil)
		t.Cleanup(r.Close)
		// A notice silenced before Attach still counts as a Notify for its key, and it writes only its silence.
		silenced := f.notice(testPane, stateWaiting, false)
		silenced.Silent = "synthetic silence"
		r.Notify(ctx, silenced)
		r.Attach(ctx, reopened)
		r.Wait()

		f.check(t, []route.Line{
			lineOf(testPane, clockStart+30, stateWaiting, route.None, "synthetic silence"),
			lineOf(testPane, clockStart+30, stateWaiting, route.None, "changed"),
		}, nil, nil)
		f.checkActiveTimers(t, 0)
		checkRows(t, reopened)
	})
}
