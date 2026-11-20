package enginehost

import (
	"context"
	"errors"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/joshsymonds/orrery/internal/notice"
	"github.com/joshsymonds/orrery/internal/route"
	"github.com/joshsymonds/orrery/internal/store"
)

// wakeRouter is a fakeRouter that also sends each notice and seen report on its channel, after it records it, so a test
// waits for the check that routed it. The channels are buffered, so a route never waits for the test to read them.
type wakeRouter struct {
	fakeRouter
	notified chan route.Notice
	seen     chan seenReport
}

func newWakeRouter() *wakeRouter {
	return &wakeRouter{notified: make(chan route.Notice, 8), seen: make(chan seenReport, 8)}
}

// Notify records n, then sends it on notified.
func (w *wakeRouter) Notify(ctx context.Context, n route.Notice) {
	w.fakeRouter.Notify(ctx, n)
	w.notified <- n
}

// Seen records the report, then sends it on seen.
func (w *wakeRouter) Seen(ctx context.Context, key notice.Key, seen int64) {
	w.fakeRouter.Seen(ctx, key, seen)
	w.seen <- seenReport{key: key, seen: seen}
}

// wakeRun is a LimitNotices running on its own goroutine until the test stops it. Its clock is a fireClock the test
// moves, and the retry timers it arms are fake: each is sent on timers, for the test to fire.
type wakeRun struct {
	limits *LimitNotices
	clock  *fireClock
	timers *retryClock
	steps  *scriptedLimits
	router *wakeRouter
	cancel context.CancelFunc
	done   chan struct{}
}

// startWakeRun runs a LimitNotices over told and reads on its own goroutine, and stops it at cleanup. Its first read is
// the one at start, and each later read is the read of one wake's check, in order.
func startWakeRun(t *testing.T, told *fakeTold, reads ...limitRead) *wakeRun {
	t.Helper()
	run := &wakeRun{
		clock:  &fireClock{at: limitNow()},
		timers: &retryClock{armed: make(chan armedRetry, 8)},
		steps:  &scriptedLimits{reads: reads, reading: make(chan struct{}, 8)},
		router: newWakeRouter(),
	}
	run.limits = NewLimitNotices(LimitNoticesConfig{
		Steps:    run.steps,
		Epics:    &fakeEpics{titles: limitTitles()},
		Told:     told,
		Route:    run.router,
		Now:      run.clock.read,
		NewTimer: run.timers.newTimer,
	})
	var ctx context.Context
	ctx, run.cancel = context.WithCancel(context.Background())
	run.done = make(chan struct{})
	go func() {
		defer close(run.done)
		run.limits.Run(ctx)
	}()
	t.Cleanup(run.stop)
	return run
}

// stop ends the run and waits for it to return. Each check it was woken for has finished by then.
func (r *wakeRun) stop() {
	r.cancel()
	<-r.done
}

// awaitRead waits until a check has begun to read the steps.
func (r *wakeRun) awaitRead() {
	<-r.steps.reading
}

// wake asks for a check, and waits for it to read the steps. The test wakes only after the previous check has read,
// so no wake is pending when this one is sent.
func (r *wakeRun) wake() {
	r.limits.Wake()
	r.awaitRead()
}

// TestLimitNoticeWakeClockAdvancedHoursWithoutWakeReadsNoSteps checks that with the clock advanced hours and no wake, the
// limited steps are read no further times than the check at start. The run is in a synctest bubble with the default
// timer, so a period or a retry timer in Run fires during the three hours' sleep. Only a wake or an armed retry starts a
// check, and a clean check arms no retry.
func TestLimitNoticeWakeClockAdvancedHoursWithoutWakeReadsNoSteps(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		steps := &scriptedLimits{reads: []limitRead{{}}, reading: make(chan struct{}, 8)}
		limits := NewLimitNotices(LimitNoticesConfig{
			Steps: steps,
			Epics: &fakeEpics{titles: limitTitles()},
			Told:  &fakeTold{},
			Route: &fakeRouter{},
			Now:   limitNow,
		})
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		defer func() {
			cancel()
			<-done
		}()
		go func() {
			defer close(done)
			limits.Run(ctx)
		}()

		synctest.Wait()
		if n := len(steps.reading); n != 1 {
			t.Fatalf("limited steps read %d times at start, want 1", n)
		}
		time.Sleep(3 * time.Hour)
		synctest.Wait()
		if n := len(steps.reading); n != 1 {
			t.Fatalf("limited steps read %d times after the clock advanced hours with no wake, want 1", n)
		}
	})
}

// TestLimitNoticeWakeWhenAStepHoldIsStoredTellsOneNotice checks that a wake following a step's stored hold tells one
// notice for the held step, and marks it told.
func TestLimitNoticeWakeWhenAStepHoldIsStoredTellsOneNotice(t *testing.T) {
	t.Parallel()
	told := &fakeTold{}
	run := startWakeRun(t, told,
		limitRead{},
		limitRead{rows: []store.StepRow{usageRow("a")}},
	)
	run.awaitRead()
	run.wake()
	got := <-run.router.notified
	run.stop()

	if len(run.router.notices) != 1 {
		t.Fatalf("got %d notices, want 1: %+v", len(run.router.notices), run.router.notices)
	}
	checkLimitNotice(t, got, resetKey(), "Held by the usage limit, reset 2026-11-14T17:00:00Z: Alpha")
	checkTells(t, told, tellCall{limit: resetKey().Limit, names: []string{"Alpha"}})
}

// TestLimitNoticeWakeWhenAStepResumesEndsItsNotice checks that the notice a stored hold told ends when a wake follows the
// step's resume: the limit is seen at the check's time, and its mark is forgotten.
func TestLimitNoticeWakeWhenAStepResumesEndsItsNotice(t *testing.T) {
	t.Parallel()
	told := &fakeTold{}
	run := startWakeRun(t, told,
		limitRead{},
		limitRead{rows: []store.StepRow{usageRow("a")}},
		limitRead{},
	)
	run.awaitRead()
	run.wake()
	<-run.router.notified
	run.wake()
	got := <-run.router.seen
	run.stop()

	want := seenReport{key: resetKey(), seen: limitNow().Unix()}
	if got != want {
		t.Fatalf("seen report = %+v, want %+v", got, want)
	}
	if !slices.Equal(told.forgets, []string{resetKey().Limit}) {
		t.Fatalf("forgot %v, want the resumed limit %q once", told.forgets, resetKey().Limit)
	}
}

// TestLimitNoticeWakeFailedCheckArmsOneRetryThatRunsACheck checks that a wake whose check fails to read the steps arms
// one retry, limitRetry long, and that the retry, when it fires, runs one check, which tells the held step. A clean
// check after the retry arms none.
func TestLimitNoticeWakeFailedCheckArmsOneRetryThatRunsACheck(t *testing.T) {
	t.Parallel()
	run := startWakeRun(t, &fakeTold{},
		limitRead{},
		limitRead{err: errors.New("store is closed")},
		limitRead{rows: []store.StepRow{usageRow("a")}},
	)
	run.awaitRead()
	run.wake()
	retry := <-run.timers.armed
	if retry.d != limitRetry {
		t.Fatalf("retry armed for %v, want limitRetry %v", retry.d, limitRetry)
	}

	retry.fire <- time.Time{}
	got := <-run.router.notified
	run.stop()

	checkLimitNotice(t, got, resetKey(), "Held by the usage limit, reset 2026-11-14T17:00:00Z: Alpha")
	if n := len(run.timers.armed); n != 0 {
		t.Fatalf("%d retry timers armed after the retry's check read cleanly, want none", n)
	}
}

// TestLimitNoticeWakeRefusedMarkArmsRetryThatWritesIt checks that a wake whose check leaves a mark write the store
// refused arms one retry, and that the retry writes the mark again, so the mark is written twice and the notice is told
// once.
func TestLimitNoticeWakeRefusedMarkArmsRetryThatWritesIt(t *testing.T) {
	t.Parallel()
	told := &fakeTold{failWrites: 1}
	run := startWakeRun(t, told,
		limitRead{},
		limitRead{rows: []store.StepRow{usageRow("a")}},
	)
	run.awaitRead()
	run.wake()
	<-run.router.notified
	retry := <-run.timers.armed
	if retry.d != limitRetry {
		t.Fatalf("retry armed for %v, want limitRetry %v", retry.d, limitRetry)
	}

	retry.fire <- time.Time{}
	run.awaitRead()
	run.stop()

	if len(run.router.notices) != 1 {
		t.Fatalf("got %d notices, want 1: %+v", len(run.router.notices), run.router.notices)
	}
	mark := tellCall{limit: resetKey().Limit, names: []string{"Alpha"}}
	checkTells(t, told, mark, mark)
}
