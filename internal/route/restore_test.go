package route_test

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/deskstream"
	"github.com/joshsymonds/orrery/internal/push"
	"github.com/joshsymonds/orrery/internal/route"
	"github.com/joshsymonds/orrery/internal/store"
)

const (
	otherSourceKind = "synthetic-other-kind"
	thirdSourceKind = "synthetic-third-kind"
	thirdPane       = "%synthetic-third"
	badPhase        = "bogus"
)

var errRebuild = errors.New("synthetic rebuild failure")

// unlistable is a store whose notices cannot be listed.
type unlistable struct{ *store.Store }

func (unlistable) Notices(context.Context) ([]store.NoticeRow, error) {
	return nil, errStore
}

// seconds is n seconds as a duration.
func seconds(n int64) time.Duration {
	return time.Duration(n * int64(time.Second))
}

// fakeAt returns a fake whose clock starts at at, unix seconds.
func fakeAt(at int64) *fake {
	f := newFake()
	f.now = at
	return f
}

// newRestoreRouter returns a Router wired to f that restores stored notices with rebuilds and logs to logger, or
// discards when logger is nil. The caller closes it.
func newRestoreRouter(f *fake, rebuilds map[string]route.Rebuild, logger *slog.Logger) *route.Router {
	return route.New(route.Config{
		Host:       testHost,
		Now:        f.Now,
		After:      f.After,
		Desk:       f,
		Phone:      f,
		Subscribed: f.Subscribed,
		LocalInput: f.LocalInput,
		Log:        f,
		Logger:     logger,
		Rebuild:    rebuilds,
	})
}

// restoreAt returns a Router wired to f that attaches s and restores its notices. It is closed when the test ends,
// so call it after the store is reopened: the cleanups then run in the right order.
func restoreAt(t *testing.T, f *fake, rebuilds map[string]route.Rebuild, s *store.Store) *route.Router {
	t.Helper()
	r := newRestoreRouter(f, rebuilds, nil)
	t.Cleanup(r.Close)
	r.Attach(t.Context(), s)
	return r
}

// rebuildAs returns a Rebuild that rebuilds every stored row as n.
func rebuildAs(n route.Notice) route.Rebuild {
	return func(context.Context, []byte) (route.Notice, error) {
		return n, nil
	}
}

// reopenStore closes s and opens the store at path again, as a restart does.
func reopenStore(t *testing.T, s *store.Store, path string) *store.Store {
	t.Helper()
	if err := s.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	return openStore(t, path)
}

// countTimers counts the timers of f that are still waiting for d.
func countTimers(f *fake, d time.Duration) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	count := 0
	for _, timer := range f.timers {
		if timer.active() && timer.d == d {
			count++
		}
	}
	return count
}

func TestRestartResumesEachPhase(t *testing.T) {
	t.Run("deciding is decided at once", func(t *testing.T) {
		ctx := t.Context()
		path := filepath.Join(t.TempDir(), "orrery.db")
		s := openStore(t, path)
		// The row a Notify wrote before the restart, with no decision after it.
		row := rowFor(1, testPane, stateWaiting, clockStart, rowDeciding, clockStart*1000)
		if err := s.PutNotice(ctx, row); err != nil {
			t.Fatalf("put row: %v", err)
		}
		reopened := reopenStore(t, s, path)

		f := newFake()
		rebuilds := map[string]route.Rebuild{testSourceKind: rebuildAs(f.notice(testPane, stateWaiting, false))}
		r := restoreAt(t, f, rebuilds, reopened)
		r.Wait()

		f.check(t, []route.Line{lineOf(testPane, clockStart, stateWaiting, route.DeskPhone, "away")},
			[]deskstream.Notice{deskPost(stateWaiting)}, []push.Notification{phonePush(testPane, stateWaiting)})
		f.checkActiveTimers(t, 0)
		checkRows(t, reopened)
	})

	t.Run("held writes no second watching line and holds again", func(t *testing.T) {
		ctx := t.Context()
		path := filepath.Join(t.TempDir(), "orrery.db")
		s := openStore(t, path)
		first := newFake()
		first.setWatching(true, nil)
		r1 := newRestoreRouter(first, nil, nil)
		r1.Attach(ctx, s)
		r1.Notify(ctx, sourced(first.notice(testPane, stateWaiting, true)))
		r1.Wait()
		first.check(t, []route.Line{lineOf(testPane, clockStart, stateWaiting, route.None, "watching")}, nil, nil)
		reopened := reopenStore(t, s, path)

		f := newFake()
		f.setWatching(true, nil)
		rebuilds := map[string]route.Rebuild{testSourceKind: rebuildAs(f.notice(testPane, stateWaiting, true))}
		r := restoreAt(t, f, rebuilds, reopened)
		r.Wait()

		f.check(t, nil, nil, nil)
		f.checkActiveTimers(t, 1)
		checkRows(t, reopened, withWatchLogged(rowFor(1, testPane, stateWaiting, clockStart, rowHeld,
			(clockStart+watchSeconds)*1000)))

		f.setWatching(false, nil)
		f.fire(t, route.WatchRecheck)
		r.Wait()
		f.check(t, []route.Line{lineOf(testPane, clockStart+watchSeconds, stateWaiting, route.DeskPhone, "away")},
			[]deskstream.Notice{deskPost(stateWaiting)}, []push.Notification{phonePush(testPane, stateWaiting)})
		checkRows(t, reopened)
	})

	t.Run("held after its delay is decided at once", func(t *testing.T) {
		ctx := t.Context()
		path := filepath.Join(t.TempDir(), "orrery.db")
		s := openStore(t, path)
		first := newFake()
		first.setWatching(true, nil)
		r1 := newRestoreRouter(first, nil, nil)
		r1.Attach(ctx, s)
		r1.Notify(ctx, sourced(first.notice(testPane, stateWaiting, true)))
		r1.Wait()
		first.fire(t, route.WatchRecheck)
		r1.Wait()
		first.check(t, []route.Line{lineOf(testPane, clockStart, stateWaiting, route.None, "watching")}, nil, nil)
		checkRows(t, s, withWatchLogged(rowFor(1, testPane, stateWaiting, clockStart, rowHeld,
			(clockStart+2*watchSeconds)*1000)))
		reopened := reopenStore(t, s, path)

		// The watch has lapsed by the restart, so the held notice routes at once.
		f := fakeAt(clockStart + 2*watchSeconds)
		rebuilds := map[string]route.Rebuild{testSourceKind: rebuildAs(f.notice(testPane, stateWaiting, true))}
		r := restoreAt(t, f, rebuilds, reopened)
		r.Wait()

		f.check(t, []route.Line{lineOf(testPane, clockStart+2*watchSeconds, stateWaiting, route.DeskPhone, "away")},
			[]deskstream.Notice{deskPost(stateWaiting)}, []push.Notification{phonePush(testPane, stateWaiting)})
		f.checkActiveTimers(t, 0)
		checkRows(t, reopened)
	})

	t.Run("held after its judge delay is judged at once with no second delay", func(t *testing.T) {
		ctx := t.Context()
		path := filepath.Join(t.TempDir(), "orrery.db")
		s := openStore(t, path)
		first := newFake()
		c := &canned{verdict: route.Verdict{Needed: true}}
		r1 := newRestoreRouter(first, nil, nil)
		r1.Attach(ctx, s)
		n := first.notice(testPane, stateWaiting, true)
		n.Judge = c.judge
		r1.Notify(ctx, sourced(n))
		r1.Wait()
		// The judge delay is served, then the pane is watched, so the notice is held with its delay already served.
		first.setWatching(true, nil)
		first.fire(t, route.JudgeDelay)
		r1.Wait()
		c.checkCalls(t, 0)
		first.check(t, []route.Line{lineOf(testPane, clockStart+judgeSeconds, stateWaiting, route.None, "watching")},
			nil, nil)
		checkRows(t, s, withDelayServed(rowFor(1, testPane, stateWaiting, clockStart, rowHeld,
			(clockStart+judgeSeconds+watchSeconds)*1000)))
		reopened := reopenStore(t, s, path)

		restart := clockStart + judgeSeconds + watchSeconds
		f := fakeAt(restart)
		c2 := &canned{verdict: route.Verdict{Needed: true}}
		rebuilt := f.notice(testPane, stateWaiting, true)
		rebuilt.Judge = c2.judge
		r := restoreAt(t, f, map[string]route.Rebuild{testSourceKind: rebuildAs(rebuilt)}, reopened)
		r.Wait()

		c2.checkCalls(t, 1)
		if got := countTimers(f, route.JudgeDelay); got != 0 {
			t.Errorf("judge delay timers after restart = %d, want 0", got)
		}
		f.check(t, []route.Line{lineOf(testPane, restart, stateWaiting, route.DeskPhone, "away; needed")},
			[]deskstream.Notice{deskPost(stateWaiting)}, []push.Notification{phonePush(testPane, stateWaiting)})
		f.checkActiveTimers(t, 0)
		checkRows(t, reopened)
	})

	t.Run("delayed waits out the rest of its due and never serves the judge delay again", func(t *testing.T) {
		ctx := t.Context()
		path := filepath.Join(t.TempDir(), "orrery.db")
		s := openStore(t, path)
		first := newFake()
		c := &canned{verdict: route.Verdict{Needed: true}}
		r1 := newRestoreRouter(first, nil, nil)
		r1.Attach(ctx, s)
		n := first.notice(testPane, stateWaiting, true)
		n.Judge = c.judge
		r1.Notify(ctx, sourced(n))
		r1.Wait()
		c.checkCalls(t, 0)
		first.check(t, nil, nil, nil)
		wantRow := rowFor(1, testPane, stateWaiting, clockStart, rowDelayed, (clockStart+judgeSeconds)*1000)
		wantRow.DelayServed = true
		checkRows(t, s, wantRow)
		reopened := reopenStore(t, s, path)

		f := fakeAt(clockStart + 4)
		c2 := &canned{verdict: route.Verdict{Needed: true}}
		rebuilt := f.notice(testPane, stateWaiting, true)
		rebuilt.Judge = c2.judge
		r := restoreAt(t, f, map[string]route.Rebuild{testSourceKind: rebuildAs(rebuilt)}, reopened)
		r.Wait()

		c2.checkCalls(t, 0)
		if got := countTimers(f, route.JudgeDelay); got != 0 {
			t.Errorf("judge delay timers after restart = %d, want 0", got)
		}
		f.fire(t, seconds(judgeSeconds-4))
		r.Wait()
		c2.checkCalls(t, 1)
		f.check(
			t,
			[]route.Line{lineOf(testPane, clockStart+judgeSeconds, stateWaiting, route.DeskPhone, "away; needed")},
			[]deskstream.Notice{deskPost(stateWaiting)},
			[]push.Notification{phonePush(testPane, stateWaiting)},
		)
		f.checkActiveTimers(t, 0)
		checkRows(t, reopened)
	})

	t.Run("delayed past its due is judged at once without a second judge delay", func(t *testing.T) {
		ctx := t.Context()
		path := filepath.Join(t.TempDir(), "orrery.db")
		s := openStore(t, path)
		first := newFake()
		c := &canned{verdict: route.Verdict{Needed: true}}
		r1 := newRestoreRouter(first, nil, nil)
		r1.Attach(ctx, s)
		n := first.notice(testPane, stateWaiting, true)
		n.Judge = c.judge
		r1.Notify(ctx, sourced(n))
		r1.Wait()
		reopened := reopenStore(t, s, path)

		f := fakeAt(clockStart + 60)
		c2 := &canned{verdict: route.Verdict{Needed: true}}
		rebuilt := f.notice(testPane, stateWaiting, true)
		rebuilt.Judge = c2.judge
		r := restoreAt(t, f, map[string]route.Rebuild{testSourceKind: rebuildAs(rebuilt)}, reopened)
		r.Wait()

		c2.checkCalls(t, 1)
		if got := countTimers(f, route.JudgeDelay); got != 0 {
			t.Errorf("judge delay timers after restart = %d, want 0", got)
		}
		f.check(t, []route.Line{lineOf(testPane, clockStart+60, stateWaiting, route.DeskPhone, "away; needed")},
			[]deskstream.Notice{deskPost(stateWaiting)}, []push.Notification{phonePush(testPane, stateWaiting)})
		f.checkActiveTimers(t, 0)
		checkRows(t, reopened)
	})

	t.Run("desk is not posted again and escalates when its due passes", func(t *testing.T) {
		ctx := t.Context()
		path := filepath.Join(t.TempDir(), "orrery.db")
		s := openStore(t, path)
		first := newFake()
		first.setDesk(true, clockStart)
		r1 := newRestoreRouter(first, nil, nil)
		r1.Attach(ctx, s)
		r1.Notify(ctx, sourced(first.notice(testPane, stateWaiting, false)))
		r1.Wait()
		first.check(t, []route.Line{lineOf(testPane, clockStart, stateWaiting, route.Desk, "at the desk")},
			[]deskstream.Notice{deskPost(stateWaiting)}, nil)
		checkRows(t, s, rowFor(1, testPane, stateWaiting, clockStart, rowDesk, (clockStart+escalationSeconds)*1000))
		reopened := reopenStore(t, s, path)

		f := fakeAt(clockStart + 60)
		rebuilds := map[string]route.Rebuild{testSourceKind: rebuildAs(f.notice(testPane, stateWaiting, false))}
		r := restoreAt(t, f, rebuilds, reopened)
		r.Wait()
		f.check(t, nil, nil, nil)

		f.fire(t, seconds(escalationSeconds-60))
		r.Wait()
		f.check(t, []route.Line{lineOf(testPane, clockStart+escalationSeconds, stateWaiting, route.Phone,
			"unseen after 5 minutes")}, nil, []push.Notification{phonePush(testPane, stateWaiting)})
		f.checkActiveTimers(t, 0)
		checkRows(t, reopened)
	})

	t.Run("a desk past its due escalates at once", func(t *testing.T) {
		ctx := t.Context()
		path := filepath.Join(t.TempDir(), "orrery.db")
		s := openStore(t, path)
		first := newFake()
		first.setDesk(true, clockStart)
		r1 := newRestoreRouter(first, nil, nil)
		r1.Attach(ctx, s)
		r1.Notify(ctx, sourced(first.notice(testPane, stateWaiting, false)))
		r1.Wait()
		reopened := reopenStore(t, s, path)

		late := escalationSeconds + 100
		f := fakeAt(clockStart + late)
		rebuilds := map[string]route.Rebuild{testSourceKind: rebuildAs(f.notice(testPane, stateWaiting, false))}
		r := restoreAt(t, f, rebuilds, reopened)
		r.Wait()

		f.check(t, []route.Line{lineOf(testPane, clockStart+late, stateWaiting, route.Phone, "unseen after 5 minutes")},
			nil, []push.Notification{phonePush(testPane, stateWaiting)})
		f.checkActiveTimers(t, 0)
		checkRows(t, reopened)
	})

	t.Run("a notice made before Attach is restored after a restart", func(t *testing.T) {
		ctx := t.Context()
		path := filepath.Join(t.TempDir(), "orrery.db")
		s := openStore(t, path)
		first := newFake()
		first.setDesk(true, clockStart)
		r1 := newRestoreRouter(first, nil, nil)
		r1.Notify(ctx, sourced(first.notice(testPane, stateWaiting, false)))
		r1.Wait()
		r1.Attach(ctx, s)
		r1.Wait()
		first.check(t, []route.Line{lineOf(testPane, clockStart, stateWaiting, route.Desk, "at the desk")},
			[]deskstream.Notice{deskPost(stateWaiting)}, nil)
		checkRows(t, s, rowFor(1, testPane, stateWaiting, clockStart, rowDesk, (clockStart+escalationSeconds)*1000))
		reopened := reopenStore(t, s, path)

		f := fakeAt(clockStart + 30)
		rebuilds := map[string]route.Rebuild{testSourceKind: rebuildAs(f.notice(testPane, stateWaiting, false))}
		r := restoreAt(t, f, rebuilds, reopened)
		r.Wait()
		f.check(t, nil, nil, nil)

		f.fire(t, seconds(escalationSeconds-30))
		r.Wait()
		f.check(t, []route.Line{lineOf(testPane, clockStart+escalationSeconds, stateWaiting, route.Phone,
			"unseen after 5 minutes")}, nil, []push.Notification{phonePush(testPane, stateWaiting)})
		f.checkActiveTimers(t, 0)
		checkRows(t, reopened)
	})
}

func TestNewerBeatsRestored(t *testing.T) {
	t.Run("a newer notice recorded before Attach ends the restored one as changed", func(t *testing.T) {
		ctx := t.Context()
		path := filepath.Join(t.TempDir(), "orrery.db")
		s := openStore(t, path)
		first := newFake()
		first.setDesk(true, clockStart)
		r1 := newRestoreRouter(first, nil, nil)
		r1.Attach(ctx, s)
		r1.Notify(ctx, sourced(first.notice(testPane, stateWaiting, false)))
		r1.Wait()
		reopened := reopenStore(t, s, path)

		f := fakeAt(clockStart + 30)
		f.setWatching(true, nil)
		rebuilds := map[string]route.Rebuild{testSourceKind: rebuildAs(f.notice(testPane, stateWaiting, false))}
		r := newRestoreRouter(f, rebuilds, nil)
		t.Cleanup(r.Close)
		r.Notify(ctx, sourced(f.notice(testPane, stateDone, true)))
		r.Wait()
		r.Attach(ctx, reopened)
		r.Wait()

		f.check(t, []route.Line{
			lineOf(testPane, clockStart+30, stateDone, route.None, "watching"),
			lineOf(testPane, clockStart+30, stateWaiting, route.None, "changed"),
		}, nil, nil)
		checkRows(t, reopened, withWatchLogged(rowFor(2, testPane, stateDone, clockStart+30, rowHeld,
			(clockStart+30+watchSeconds)*1000)))
	})

	t.Run("a newer notice recorded after Attach ends the restored one as changed", func(t *testing.T) {
		ctx := t.Context()
		path := filepath.Join(t.TempDir(), "orrery.db")
		s := openStore(t, path)
		first := newFake()
		first.setDesk(true, clockStart)
		r1 := newRestoreRouter(first, nil, nil)
		r1.Attach(ctx, s)
		r1.Notify(ctx, sourced(first.notice(testPane, stateWaiting, false)))
		r1.Wait()
		reopened := reopenStore(t, s, path)

		f := fakeAt(clockStart + 30)
		f.setWatching(true, nil)
		rebuilds := map[string]route.Rebuild{testSourceKind: rebuildAs(f.notice(testPane, stateWaiting, false))}
		r := restoreAt(t, f, rebuilds, reopened)
		r.Wait()
		f.check(t, nil, nil, nil)

		r.Notify(ctx, sourced(f.notice(testPane, stateDone, true)))
		r.Wait()

		f.check(t, []route.Line{
			lineOf(testPane, clockStart+30, stateWaiting, route.None, "changed"),
			lineOf(testPane, clockStart+30, stateDone, route.None, "watching"),
		}, nil, nil)
		if got := countTimers(f, seconds(escalationSeconds-30)); got != 0 {
			t.Errorf("escalation timers of the restored notice = %d, want 0", got)
		}
		checkRows(t, reopened, withWatchLogged(rowFor(2, testPane, stateDone, clockStart+30, rowHeld,
			(clockStart+30+watchSeconds)*1000)))
	})

	t.Run("of two rows for one key the lower id ends as changed and the higher resumes", func(t *testing.T) {
		ctx := t.Context()
		path := filepath.Join(t.TempDir(), "orrery.db")
		s := openStore(t, path)
		if err := s.PutNotice(ctx, rowFor(1, testPane, stateWaiting, clockStart, rowDesk,
			(clockStart+escalationSeconds)*1000)); err != nil {
			t.Fatalf("put lower row: %v", err)
		}
		if err := s.PutNotice(ctx, rowFor(2, testPane, stateDone, clockStart, rowDesk,
			(clockStart+escalationSeconds)*1000)); err != nil {
			t.Fatalf("put higher row: %v", err)
		}
		reopened := reopenStore(t, s, path)

		f := fakeAt(clockStart + 30)
		r := restoreAt(t, f, map[string]route.Rebuild{testSourceKind: rebuildAs(f.notice(testPane, stateDone, false))},
			reopened)
		r.Wait()

		f.check(t, []route.Line{lineOf(testPane, clockStart+30, stateWaiting, route.None, "changed")}, nil, nil)
		checkRows(t, reopened, rowFor(2, testPane, stateDone, clockStart, rowDesk, (clockStart+escalationSeconds)*1000))

		f.fire(t, seconds(escalationSeconds-30))
		r.Wait()
		f.check(t, []route.Line{
			lineOf(testPane, clockStart+30, stateWaiting, route.None, "changed"),
			lineOf(testPane, clockStart+escalationSeconds, stateDone, route.Phone, "unseen after 5 minutes"),
		}, nil, []push.Notification{phonePush(testPane, stateDone)})
		checkRows(t, reopened)
	})
}

func TestNotRestoredLine(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openStore(t, path)
	unknownKind := rowFor(1, testPane, stateWaiting, clockStart, rowDesk, (clockStart+escalationSeconds)*1000)
	unknownKind.SourceKind = otherSourceKind
	failing := rowFor(2, otherPane, stateWaiting, clockStart, rowDesk, (clockStart+escalationSeconds)*1000)
	unphased := rowFor(3, thirdPane, stateWaiting, clockStart, badPhase, (clockStart+escalationSeconds)*1000)
	unphased.SourceKind = thirdSourceKind
	for _, row := range []store.NoticeRow{unknownKind, failing, unphased} {
		if err := s.PutNotice(ctx, row); err != nil {
			t.Fatalf("put row %d: %v", row.ID, err)
		}
	}
	reopened := reopenStore(t, s, path)

	f := fakeAt(clockStart)
	r := restoreAt(t, f, map[string]route.Rebuild{
		testSourceKind:  func(context.Context, []byte) (route.Notice, error) { return route.Notice{}, errRebuild },
		thirdSourceKind: rebuildAs(f.notice(thirdPane, stateWaiting, false)),
	}, reopened)
	r.Wait()

	f.check(t, []route.Line{
		lineOf(
			testPane,
			clockStart,
			stateWaiting,
			route.None,
			`not restored: unknown source kind "synthetic-other-kind"`,
		),
		lineOf(otherPane, clockStart, stateWaiting, route.None, "not restored: synthetic rebuild failure"),
		lineOf(thirdPane, clockStart, stateWaiting, route.None, `not restored: unknown phase "bogus"`),
	}, nil, nil)
	f.checkActiveTimers(t, 0)
	checkRows(t, reopened)
}

func TestFailedNoticesReadRestoresNothing(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openStore(t, path)
	row := rowFor(1, testPane, stateWaiting, clockStart, rowDesk, (clockStart+escalationSeconds)*1000)
	if err := s.PutNotice(ctx, row); err != nil {
		t.Fatalf("put row: %v", err)
	}
	reopened := reopenStore(t, s, path)

	f := fakeAt(clockStart + 30)
	rebuilds := map[string]route.Rebuild{testSourceKind: rebuildAs(f.notice(testPane, stateWaiting, false))}
	logs := &logBuffer{}
	r := newRestoreRouter(f, rebuilds, slog.New(slog.NewTextHandler(logs, nil)))
	t.Cleanup(r.Close)
	r.Attach(ctx, unlistable{reopened})
	r.Wait()

	f.check(t, nil, nil, nil)
	f.checkActiveTimers(t, 0)
	checkRows(t, reopened, rowFor(1, testPane, stateWaiting, clockStart, rowDesk, (clockStart+escalationSeconds)*1000))
	if got := logs.String(); !strings.Contains(got, errStore.Error()) {
		t.Errorf("logs = %q, want the notices read error", got)
	}
}
