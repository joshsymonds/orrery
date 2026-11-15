package route_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/deskstream"
	"github.com/joshsymonds/orrery/internal/route"
	"github.com/joshsymonds/orrery/internal/store"
)

// boundDuration is the bound every store call is armed with.
const boundDuration = 200 * time.Millisecond

// deskRows is a store whose first desk-phase put waits in hold, and then writes through with its context detached, so
// the row lands once hold returns. Later desk-phase puts are not held.
type deskRows struct {
	*flakyRows
	t       *testing.T
	hold    func(context.Context)
	desks   atomic.Int32
	entered chan struct{}
}

func (s *deskRows) PutNotice(ctx context.Context, row store.NoticeRow) error {
	if row.Phase != rowDesk || s.desks.Add(1) > 1 {
		return s.flakyRows.PutNotice(ctx, row)
	}
	if _, ok := ctx.Deadline(); ok {
		s.t.Errorf("the desk put's context has a wall-clock deadline, want only the injected bound")
	}
	close(s.entered)
	s.hold(ctx)
	return s.flakyRows.PutNotice(context.WithoutCancel(ctx), row)
}

// bounds is the StoreAfter a test gives its Router. Each bound it arms stays armed until its stop is called. fire
// fires the bound that is armed, so the store call it bounds reads as late.
type bounds struct {
	t     *testing.T
	mu    sync.Mutex
	armed []*bound
}

type bound struct {
	fn      func()
	stopped bool
	fired   bool
}

// after arms a bound of d, as Config.StoreAfter does. Every bound must be boundDuration.
func (b *bounds) after(d time.Duration, fn func()) func() bool {
	if d != boundDuration {
		b.t.Errorf("store call bound = %v, want %v", d, boundDuration)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	bd := &bound{fn: fn}
	b.armed = append(b.armed, bd)
	return func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		wasStopped := bd.stopped
		bd.stopped = true
		return !bd.fired && !wasStopped
	}
}

// fire fires the armed bound, and fails the test when none is armed.
func (b *bounds) fire() {
	b.t.Helper()
	if !b.fireArmed() {
		b.t.Fatal("no store call bound is armed")
	}
}

// fireArmed fires the newest bound that is armed and reports whether one was.
func (b *bounds) fireArmed() bool {
	b.mu.Lock()
	var armed *bound
	for _, bd := range b.armed {
		if !bd.stopped {
			armed = bd
		}
	}
	if armed == nil {
		b.mu.Unlock()
		return false
	}
	armed.fired = true
	b.mu.Unlock()
	armed.fn()
	return true
}

// boundedRouter returns a Router wired to f whose store-call bounds b arms. It is closed when the test ends.
func boundedRouter(t *testing.T, f *fake, b *bounds) *route.Router {
	t.Helper()
	r := route.New(route.Config{
		Host: testHost, Now: f.Now, After: f.After, Desk: f, Phone: f, Subscribed: f.Subscribed, Log: f,
		StoreAfter: b.after,
	})
	t.Cleanup(r.Close)
	return r
}

// TestRouterLatePutIsRetried: a desk put whose bound fires while it waits reads as late, so the Router writes its row
// again, which Close does.
func TestRouterLatePutIsRetried(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := &deskRows{
		flakyRows: newFlakyRows(t), t: t, entered: make(chan struct{}),
		hold: func(callCtx context.Context) { <-callCtx.Done() },
	}
	f := newFake()
	f.setDesk(clockStart)
	b := &bounds{t: t}
	r := boundedRouter(t, f, b)
	r.Attach(ctx, s)

	r.Notify(ctx, sourced(f.notice(testPane, stateWaiting, false)))
	<-s.entered
	b.fire()
	r.Wait()
	r.Close()
	if got := s.desks.Load(); got != 2 {
		t.Errorf("desk row puts = %d, want 2: the late put and its retry", got)
	}
	f.check(t, []route.Line{lineOf(testPane, clockStart, stateWaiting, route.Desk, "at the desk")},
		[]deskstream.Notice{deskPost(stateWaiting)}, nil)
	checkRows(t, s, rowFor(1, testPane, stateWaiting, clockStart, rowDesk, (clockStart+escalationSeconds)*1000))
	f.checkActiveTimers(t, 0)
}

// TestRouterPutUnderANeverFiringBoundIsNotLate: a desk put that is slow, under a bound that never fires, is not late.
// It is written once and never retried.
func TestRouterPutUnderANeverFiringBoundIsNotLate(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	release := make(chan struct{})
	s := &deskRows{
		flakyRows: newFlakyRows(t), t: t, entered: make(chan struct{}),
		hold: func(context.Context) { <-release },
	}
	f := newFake()
	f.setDesk(clockStart)
	r := boundedRouter(t, f, &bounds{t: t})
	r.Attach(ctx, s)

	r.Notify(ctx, sourced(f.notice(testPane, stateWaiting, false)))
	<-s.entered
	close(release)
	r.Wait()
	r.Close()
	if got := s.desks.Load(); got != 1 {
		t.Errorf("desk row puts = %d, want 1: a put no bound fired on is not retried", got)
	}
	f.check(t, []route.Line{lineOf(testPane, clockStart, stateWaiting, route.Desk, "at the desk")},
		[]deskstream.Notice{deskPost(stateWaiting)}, nil)
	checkRows(t, s, rowFor(1, testPane, stateWaiting, clockStart, rowDesk, (clockStart+escalationSeconds)*1000))
	f.checkActiveTimers(t, 0)
}
