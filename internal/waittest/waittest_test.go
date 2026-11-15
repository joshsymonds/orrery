package waittest

import (
	"strings"
	"sync/atomic"
	"testing"
)

// recorder is a testing.TB that records a Fatalf instead of ending the test.
type recorder struct {
	testing.TB
	failed atomic.Pointer[string]
}

func (r *recorder) Helper() {}

func (r *recorder) Fatalf(format string, args ...any) {
	msg := format
	if len(args) > 0 {
		msg = strings.TrimSpace(format)
	}
	r.failed.Store(&msg)
	panic(r)
}

// run calls Until on r and returns whether it failed.
func (r *recorder) run(ended <-chan struct{}, ok func() bool) (failed bool) {
	defer func() {
		if got := recover(); got != nil {
			if got != r {
				panic(got)
			}
			failed = true
		}
	}()
	Until(r, ended, "the mark", ok)
	return false
}

func TestUntilReturnsOnceOkHolds(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	r := &recorder{TB: t}
	if r.run(make(chan struct{}), func() bool { return calls.Add(1) >= 3 }) {
		t.Fatal("Until failed though ok came true")
	}
}

func TestUntilFailsOnceTheProducerEnds(t *testing.T) {
	t.Parallel()
	ended := make(chan struct{})
	close(ended)
	r := &recorder{TB: t}
	if !r.run(ended, func() bool { return false }) {
		t.Fatal("Until returned though ok never held and the producer had ended")
	}
}

func TestUntilPassesWhenOkHoldsAsTheProducerEnds(t *testing.T) {
	t.Parallel()
	ended := make(chan struct{})
	var done atomic.Bool
	r := &recorder{TB: t}
	go func() {
		done.Store(true)
		close(ended)
	}()
	if r.run(ended, done.Load) {
		t.Fatal("Until failed though ok held once the producer ended")
	}
}

func TestClosedPassesTheResultOn(t *testing.T) {
	t.Parallel()
	done := make(chan int, 1)
	ended, result := Closed(done)
	done <- 7
	<-ended
	if got := <-result; got != 7 {
		t.Fatalf("result = %d, want 7", got)
	}
}

// closedChan returns a channel that is already closed.
func closedChan() <-chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}

// isClosed reports whether ch is closed, without blocking.
func isClosed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func TestAnyClosesWithTheFirstAndIgnoresNil(t *testing.T) {
	t.Parallel()
	open := make(chan struct{})
	if isClosed(Any(nil, open)) {
		t.Fatal("Any closed though none of its channels had")
	}
	<-Any(nil, open, closedChan())
}

func TestAllClosesOnlyOnceEveryChannelHas(t *testing.T) {
	t.Parallel()
	last := make(chan struct{})
	all := All(closedChan(), last)
	if isClosed(all) {
		t.Fatal("All closed while one of its channels was open")
	}
	close(last)
	<-all
	if isClosed(All()) {
		t.Fatal("All of no channels closed")
	}
}

// runIdle calls UntilIdle on r and returns whether it failed.
func (r *recorder) runIdle(finished func() bool, ok func() bool) (failed bool) {
	defer func() {
		if got := recover(); got != nil {
			if got != r {
				panic(got)
			}
			failed = true
		}
	}()
	UntilIdle(r, finished, "the mark", ok)
	return false
}

func TestUntilIdleFailsOnceTheWorkHasFinished(t *testing.T) {
	t.Parallel()
	r := &recorder{TB: t}
	if !r.runIdle(func() bool { return true }, func() bool { return false }) {
		t.Fatal("UntilIdle returned though ok never held and the work had finished")
	}
}

func TestUntilIdleReturnsOnceOkHolds(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	r := &recorder{TB: t}
	if r.runIdle(func() bool { return false }, func() bool { return calls.Add(1) >= 3 }) {
		t.Fatal("UntilIdle failed though ok came true")
	}
}

func TestUntilIdlePassesWhenOkHoldsAsTheWorkFinishes(t *testing.T) {
	t.Parallel()
	var done atomic.Bool
	r := &recorder{TB: t}
	finished := func() bool {
		done.Store(true)
		return true
	}
	if r.runIdle(finished, done.Load) {
		t.Fatal("UntilIdle failed though ok held once the work finished")
	}
}
