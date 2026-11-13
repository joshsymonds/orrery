package route_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/deskstream"
	"github.com/joshsymonds/orrery/internal/notice"
	"github.com/joshsymonds/orrery/internal/push"
	"github.com/joshsymonds/orrery/internal/route"
	"github.com/joshsymonds/orrery/internal/store"
)

const (
	testSourceKind = "synthetic-kind"
	testSourceData = "synthetic source"
	// rowLimit bounds how long routing takes while the store blocks every write.
	rowLimit = time.Second
	// The phase timers in whole seconds, as the rows' due times count them.
	watchSeconds      = int64(route.WatchRecheck / time.Second)
	judgeSeconds      = int64(route.JudgeDelay / time.Second)
	escalationSeconds = int64(route.Escalation / time.Second)
	// The phases a row records.
	rowDeciding = "deciding"
	rowHeld     = "held"
	rowDelayed  = "delayed"
	rowDesk     = "desk"
)

var errStore = errors.New("synthetic store failure")

// faults say which store calls fail, or block until their context ends.
type faults struct {
	floor    bool   // NoticeIDFloor fails
	putPhase string // PutNotice fails for a row in this phase
	puts     bool   // every PutNotice fails
	deletes  bool   // every DeleteNotice fails
	block    bool   // every write blocks until its context ends
}

// flakyRows is a store with faults injected. Calls that are not faulted reach the real store.
type flakyRows struct {
	*store.Store
	mu sync.Mutex
	f  faults
}

func newFlakyRows(t *testing.T) *flakyRows {
	t.Helper()
	return &flakyRows{Store: openStore(t, filepath.Join(t.TempDir(), "orrery.db"))}
}

func (s *flakyRows) set(f faults) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.f = f
}

func (s *flakyRows) current() faults {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.f
}

func (s *flakyRows) PutNotice(ctx context.Context, row store.NoticeRow) error {
	f := s.current()
	if f.block {
		<-ctx.Done()
		return fmt.Errorf("blocked put: %w", ctx.Err())
	}
	if f.puts || row.Phase == f.putPhase {
		return errStore
	}
	return s.Store.PutNotice(ctx, row)
}

func (s *flakyRows) DeleteNotice(ctx context.Context, id int64) error {
	f := s.current()
	if f.block {
		<-ctx.Done()
		return fmt.Errorf("blocked delete: %w", ctx.Err())
	}
	if f.deletes {
		return errStore
	}
	return s.Store.DeleteNotice(ctx, id)
}

func (s *flakyRows) NoticeIDFloor(ctx context.Context) (int64, error) {
	if s.current().floor {
		return 0, errStore
	}
	return s.Store.NoticeIDFloor(ctx)
}

// logBuffer collects the log output of the Router's goroutines.
type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *logBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *logBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// openStore opens the store at path and closes it when the test ends.
func openStore(t *testing.T, path string) *store.Store {
	t.Helper()
	s, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	return s
}

// newRestartRouter returns a Router wired to f that logs to logger, or discards when logger is nil. It is closed
// when the test ends.
func newRestartRouter(t *testing.T, f *fake, logger *slog.Logger) *route.Router {
	t.Helper()
	r := route.New(route.Config{
		Host:       testHost,
		Now:        f.Now,
		After:      f.After,
		Desk:       f,
		Phone:      f,
		Subscribed: f.Subscribed,
		LocalInput: f.LocalInput,
		Log:        f,
		Logger:     logger,
	})
	t.Cleanup(r.Close)
	return r
}

// sourced returns n with the source the restart tests give every notice.
func sourced(n route.Notice) route.Notice {
	n.Source = route.Source{Kind: testSourceKind, Data: []byte(testSourceData)}
	return n
}

// rowFor is the row a sourced notice in pane and state keeps when recorded at at, in phase, due at due (unix ms).
func rowFor(id int64, pane, state string, at int64, phase string, due int64) store.NoticeRow {
	return store.NoticeRow{
		ID:         id,
		Pane:       pane,
		State:      state,
		Time:       at,
		Phase:      phase,
		Due:        due,
		SourceKind: testSourceKind,
		Source:     []byte(testSourceData),
	}
}

// withWatchLogged is row after its notice's watching line was written.
func withWatchLogged(row store.NoticeRow) store.NoticeRow {
	row.WatchLogged = true
	return row
}

// withDelayServed is row after its notice's judge delay was served and armed.
func withDelayServed(row store.NoticeRow) store.NoticeRow {
	row.DelayServed = true
	return withWatchLogged(row)
}

// checkRows asserts the rows rows holds, in id order.
func checkRows(t *testing.T, rows route.Rows, want ...store.NoticeRow) {
	t.Helper()
	got, err := rows.Notices(t.Context())
	if err != nil {
		t.Fatalf("list notices: %v", err)
	}
	if !slices.EqualFunc(got, want, func(a, b store.NoticeRow) bool { return reflect.DeepEqual(a, b) }) {
		t.Errorf("rows = %+v, want %+v", got, want)
	}
}

// within runs run and fails the test when it takes longer than limit.
func within(t *testing.T, limit time.Duration, run func()) {
	t.Helper()
	start := time.Now()
	run()
	if elapsed := time.Since(start); elapsed > limit {
		t.Errorf("routing took %v with the store blocked, want within %v", elapsed, limit)
	}
}

func TestCloseKeepsRows(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openStore(t, path)
	f := newFake()
	f.setDesk(true, clockStart)
	f.setWatching(true, nil)
	r := newRestartRouter(t, f, nil)
	r.Attach(ctx, s)

	r.Notify(ctx, sourced(f.notice(testPane, stateWaiting, false)))
	r.Wait()
	r.Notify(ctx, f.notice(otherPane, stateDone, true))
	r.Wait()
	f.check(t, []route.Line{
		lineOf(testPane, clockStart, stateWaiting, route.Desk, "at the desk"),
		lineOf(otherPane, clockStart, stateDone, route.None, "watching"),
	}, []deskstream.Notice{deskPost(stateWaiting)}, nil)

	r.Close()

	f.check(t, []route.Line{
		lineOf(testPane, clockStart, stateWaiting, route.Desk, "at the desk"),
		lineOf(otherPane, clockStart, stateDone, route.None, "watching"),
		lineOf(otherPane, clockStart, stateDone, route.None, "shutdown"),
	}, []deskstream.Notice{deskPost(stateWaiting)}, nil)
	f.checkActiveTimers(t, 0)

	if err := s.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	reopened := openStore(t, path)
	checkRows(t, reopened, rowFor(1, testPane, stateWaiting, clockStart, rowDesk,
		(clockStart+escalationSeconds)*1000))
}

func TestPendingRowPhases(t *testing.T) {
	t.Run("rows follow the notice through each phase to its end", func(t *testing.T) {
		ctx := t.Context()
		s := newFlakyRows(t)
		f := newFake()
		r := newRestartRouter(t, f, nil)
		r.Attach(ctx, s)

		// The gate holds the first watch check until the test has read the row written at Notify.
		gate := make(chan struct{})
		open := sync.OnceFunc(func() { close(gate) })
		t.Cleanup(open)
		c := &canned{verdict: route.Verdict{Needed: true}}
		n := f.notice(testPane, stateWaiting, false)
		n.Watching = func(ctx context.Context) (bool, error) {
			<-gate
			return f.Watching(ctx)
		}
		n.Judge = c.judge
		f.setWatching(true, nil)
		r.Notify(ctx, sourced(n))
		checkRows(t, s, rowFor(1, testPane, stateWaiting, clockStart, rowDeciding, clockStart*1000))

		open()
		r.Wait()
		checkRows(t, s, withWatchLogged(rowFor(1, testPane, stateWaiting, clockStart, rowHeld,
			(clockStart+watchSeconds)*1000)))

		f.setWatching(false, nil)
		f.fire(t, route.WatchRecheck)
		r.Wait()
		checkRows(t, s, withDelayServed(rowFor(1, testPane, stateWaiting, clockStart, rowDelayed,
			(clockStart+watchSeconds+judgeSeconds)*1000)))

		f.setDesk(true, clockStart)
		f.fire(t, route.JudgeDelay)
		r.Wait()
		checkRows(t, s, withDelayServed(rowFor(1, testPane, stateWaiting, clockStart, rowDesk,
			(clockStart+watchSeconds+judgeSeconds+escalationSeconds)*1000)))

		f.fire(t, route.Escalation)
		r.Wait()
		checkRows(t, s)

		later := clockStart + watchSeconds + judgeSeconds + escalationSeconds
		f.setWatching(true, nil)
		r.Notify(ctx, sourced(f.notice(otherPane, stateDone, true)))
		r.Wait()
		checkRows(t, s, withWatchLogged(rowFor(2, otherPane, stateDone, later, rowHeld,
			(later+watchSeconds)*1000)))
		r.Seen(ctx, notice.Key{Pane: otherPane}, later)
		checkRows(t, s)

		r.Notify(ctx, sourced(f.notice(testPane, stateDone, true)))
		r.Wait()
		checkRows(t, s, withWatchLogged(rowFor(3, testPane, stateDone, later, rowHeld,
			(later+watchSeconds)*1000)))
		r.Changed(notice.Key{Pane: testPane})
		checkRows(t, s)
	})

	t.Run("a notice pending before Attach gets a row above the floor", func(t *testing.T) {
		ctx := t.Context()
		s := newFlakyRows(t)
		// Putting and deleting row 7 leaves the id floor at 7.
		if err := s.PutNotice(ctx, rowFor(7, "%synthetic-old", stateDone, clockStart, rowDesk, 0)); err != nil {
			t.Fatalf("put floor row: %v", err)
		}
		if err := s.DeleteNotice(ctx, 7); err != nil {
			t.Fatalf("delete floor row: %v", err)
		}
		f := newFake()
		f.setWatching(true, nil)
		r := newRestartRouter(t, f, nil)
		r.Notify(ctx, sourced(f.notice(testPane, stateWaiting, true)))
		r.Wait()
		checkRows(t, s)

		r.Attach(ctx, s)
		checkRows(t, s, withWatchLogged(rowFor(8, testPane, stateWaiting, clockStart, rowHeld,
			(clockStart+watchSeconds)*1000)))
	})
}

func TestRowWriteFails(t *testing.T) {
	deskNotice := func(f *fake) route.Notice {
		return sourced(f.notice(testPane, stateWaiting, false))
	}
	// atDeskEscalated is what routing writes when a desk notice goes unseen and the phone is pushed.
	atDeskEscalated := func(t *testing.T, f *fake) {
		t.Helper()
		f.check(t, []route.Line{
			lineOf(testPane, clockStart, stateWaiting, route.Desk, "at the desk"),
			lineOf(testPane, clockStart+escalationSeconds, stateWaiting, route.Phone, "unseen after 5 minutes"),
		}, []deskstream.Notice{deskPost(stateWaiting)}, []push.Notification{phonePush(testPane, stateWaiting)})
	}

	t.Run("floor read fails and routing goes on as with no store", func(t *testing.T) {
		ctx := t.Context()
		s := newFlakyRows(t)
		s.set(faults{floor: true})
		logs := &logBuffer{}
		f := newFake()
		f.setDesk(true, clockStart)
		r := newRestartRouter(t, f, slog.New(slog.NewTextHandler(logs, nil)))
		r.Attach(ctx, s)

		r.Notify(ctx, deskNotice(f))
		r.Wait()
		f.fire(t, route.Escalation)
		r.Wait()

		atDeskEscalated(t, f)
		checkRows(t, s)
		if !strings.Contains(logs.String(), errStore.Error()) {
			t.Errorf("logs = %q, want the floor read error", logs.String())
		}
	})

	t.Run("failed deletes are retried by a later write", func(t *testing.T) {
		ctx := t.Context()
		s := newFlakyRows(t)
		logs := &logBuffer{}
		f := newFake()
		f.setDesk(true, clockStart)
		r := newRestartRouter(t, f, slog.New(slog.NewTextHandler(logs, nil)))
		r.Attach(ctx, s)

		r.Notify(ctx, deskNotice(f))
		r.Wait()
		s.set(faults{deletes: true})
		f.fire(t, route.Escalation)
		r.Wait()
		atDeskEscalated(t, f)
		checkRows(t, s, rowFor(1, testPane, stateWaiting, clockStart, rowDesk,
			(clockStart+escalationSeconds)*1000))

		s.set(faults{})
		f.setWatching(true, nil)
		later := clockStart + escalationSeconds
		r.Notify(ctx, sourced(f.notice(otherPane, stateDone, true)))
		r.Wait()
		checkRows(t, s, withWatchLogged(rowFor(2, otherPane, stateDone, later, rowHeld,
			(later+watchSeconds)*1000)))
		if !strings.Contains(logs.String(), "delete notice row") {
			t.Errorf("logs = %q, want the failed delete", logs.String())
		}
	})

	t.Run("failed puts are retried by a later write with the current row", func(t *testing.T) {
		ctx := t.Context()
		s := newFlakyRows(t)
		logs := &logBuffer{}
		f := newFake()
		f.setWatching(true, nil)
		r := newRestartRouter(t, f, slog.New(slog.NewTextHandler(logs, nil)))
		r.Attach(ctx, s)

		s.set(faults{putPhase: rowHeld})
		r.Notify(ctx, sourced(f.notice(testPane, stateWaiting, true)))
		r.Wait()
		checkRows(t, s, rowFor(1, testPane, stateWaiting, clockStart, rowDeciding, clockStart*1000))

		s.set(faults{})
		r.Notify(ctx, sourced(f.notice(otherPane, stateDone, true)))
		r.Wait()
		checkRows(t, s,
			withWatchLogged(rowFor(1, testPane, stateWaiting, clockStart, rowHeld, (clockStart+watchSeconds)*1000)),
			withWatchLogged(rowFor(2, otherPane, stateDone, clockStart, rowHeld, (clockStart+watchSeconds)*1000)))
		if !strings.Contains(logs.String(), testPane) {
			t.Errorf("logs = %q, want the failed put's key", logs.String())
		}
	})

	t.Run("Close retries a failed put of a kept row", func(t *testing.T) {
		ctx := t.Context()
		s := newFlakyRows(t)
		f := newFake()
		f.setDesk(true, clockStart)
		r := newRestartRouter(t, f, nil)
		r.Attach(ctx, s)

		s.set(faults{putPhase: rowDesk})
		r.Notify(ctx, deskNotice(f))
		r.Wait()
		checkRows(t, s, rowFor(1, testPane, stateWaiting, clockStart, rowDeciding, clockStart*1000))

		s.set(faults{})
		r.Close()
		f.check(t, []route.Line{lineOf(testPane, clockStart, stateWaiting, route.Desk, "at the desk")},
			[]deskstream.Notice{deskPost(stateWaiting)}, nil)
		checkRows(t, s, rowFor(1, testPane, stateWaiting, clockStart, rowDesk,
			(clockStart+escalationSeconds)*1000))
	})

	t.Run("Close ends a notice whose row could not be stored", func(t *testing.T) {
		ctx := t.Context()
		s := newFlakyRows(t)
		f := newFake()
		f.setDesk(true, clockStart)
		r := newRestartRouter(t, f, nil)
		r.Attach(ctx, s)

		s.set(faults{putPhase: rowDesk})
		r.Notify(ctx, deskNotice(f))
		r.Wait()
		checkRows(t, s, rowFor(1, testPane, stateWaiting, clockStart, rowDeciding, clockStart*1000))

		r.Close()
		f.check(t, []route.Line{
			lineOf(testPane, clockStart, stateWaiting, route.Desk, "at the desk"),
			lineOf(testPane, clockStart, stateWaiting, route.None, "shutdown"),
		}, []deskstream.Notice{deskPost(stateWaiting)}, nil)
		f.checkActiveTimers(t, 0)
		checkRows(t, s)
	})

	t.Run("Close retries a failed delete", func(t *testing.T) {
		ctx := t.Context()
		s := newFlakyRows(t)
		f := newFake()
		f.setDesk(true, clockStart)
		r := newRestartRouter(t, f, nil)
		r.Attach(ctx, s)

		r.Notify(ctx, deskNotice(f))
		r.Wait()
		s.set(faults{deletes: true})
		f.fire(t, route.Escalation)
		r.Wait()
		checkRows(t, s, rowFor(1, testPane, stateWaiting, clockStart, rowDesk,
			(clockStart+escalationSeconds)*1000))

		s.set(faults{})
		r.Close()
		checkRows(t, s)
	})

	t.Run("blocked writes route as with no store within a second", func(t *testing.T) {
		ctx := t.Context()
		s := newFlakyRows(t)
		logs := &logBuffer{}
		f := newFake()
		r := newRestartRouter(t, f, slog.New(slog.NewTextHandler(logs, nil)))
		r.Attach(ctx, s)

		s.set(faults{block: true})
		within(t, rowLimit, func() {
			r.Notify(ctx, deskNotice(f))
			r.Wait()
		})

		f.check(t, []route.Line{lineOf(testPane, clockStart, stateWaiting, route.DeskPhone, "away")},
			[]deskstream.Notice{deskPost(stateWaiting)}, []push.Notification{phonePush(testPane, stateWaiting)})
		if !strings.Contains(logs.String(), "deadline exceeded") {
			t.Errorf("logs = %q, want the deadline error", logs.String())
		}
	})

	t.Run("a backlog of failed deletes keeps blocked writes within a second", func(t *testing.T) {
		ctx := t.Context()
		s := newFlakyRows(t)
		f := newFake()
		f.setWatching(true, nil)
		r := newRestartRouter(t, f, nil)
		r.Attach(ctx, s)

		// Five notices are stored, then each ends while every store call blocks, so each row's delete fails and waits.
		const backlog = 5
		backlogPane := func(i int) string { return fmt.Sprintf("%%synthetic-backlog-%d", i) }
		for i := range backlog {
			r.Notify(ctx, sourced(f.notice(backlogPane(i), stateDone, true)))
			r.Wait()
		}
		s.set(faults{block: true})
		for i := range backlog {
			r.Changed(notice.Key{Pane: backlogPane(i)})
		}

		// Each blocked write costs its own calls and at most one retry, not one call per failed delete.
		within(t, rowLimit, func() {
			r.Notify(ctx, sourced(f.notice(testPane, stateWaiting, true)))
			r.Wait()
		})
		within(t, rowLimit, r.Close)

		// Once the store recovers, one later write deletes the backlog and stores the notice that is pending.
		s.set(faults{})
		r.Notify(ctx, sourced(f.notice(otherPane, stateDone, true)))
		r.Wait()
		want := withWatchLogged(rowFor(7, otherPane, stateDone, clockStart, rowHeld, (clockStart+watchSeconds)*1000))
		checkRows(t, s, want)
		r.Close()
		checkRows(t, s, want)
	})
}
