// Package waittest waits in tests for work another goroutine or process does. It is imported only by tests.
package waittest

import (
	"sync"
	"testing"
	"time"
)

// poll is how often Until checks its condition.
const poll = 5 * time.Millisecond

// Until polls ok until it holds. It has no deadline of its own: the work runs at the machine's pace, and the test
// binary's timeout ends a wait that never ends. It fails tb at once, naming what never happened, when ended closes
// while ok still does not hold: ended closing means the producer whose work ok waits for has ended, so ok can no
// longer come true, and waiting on would turn the failure into a hang. A nil ended never closes.
func Until(tb testing.TB, ended <-chan struct{}, what string, ok func() bool) {
	tb.Helper()
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for !ok() {
		select {
		case <-ended:
			if ok() {
				return
			}
			tb.Fatalf("%s never happened: what would do it ended first", what)
		case <-ticker.C:
		}
	}
}

// UntilIdle polls ok until it holds, as Until does, and fails tb at once, naming what never happened, when finished
// reports that the work ok waits for has all finished while ok still does not hold. It suits producers whose end is a
// state to check rather than a channel that closes. A nil finished never reports so.
func UntilIdle(tb testing.TB, finished func() bool, what string, ok func() bool) {
	tb.Helper()
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for !ok() {
		if finished != nil && finished() {
			if ok() {
				return
			}
			tb.Fatalf("%s never happened: the work that would do it finished first", what)
		}
		<-ticker.C
	}
}

// Closed returns a channel that closes once done delivers a value, and the channel the value is passed on to. It lets
// a wait select on the end of a call whose result arrives on done without taking that result from the test.
func Closed[T any](done <-chan T) (ended <-chan struct{}, result <-chan T) {
	endedCh := make(chan struct{})
	resultCh := make(chan T, 1)
	go func() {
		v := <-done
		close(endedCh)
		resultCh <- v
	}()
	return endedCh, resultCh
}

// Any returns a channel that closes once any of chans closes: a wait whose work several producers can each end
// selects on it. A nil channel never closes.
func Any(chans ...<-chan struct{}) <-chan struct{} {
	anyEnded := make(chan struct{})
	var once sync.Once
	for _, ch := range chans {
		if ch == nil {
			continue
		}
		go func() {
			<-ch
			once.Do(func() { close(anyEnded) })
		}()
	}
	return anyEnded
}

// All returns a channel that closes once every one of chans has closed: a wait for work any of several producers can
// still do selects on it. With no channels it never closes.
func All(chans ...<-chan struct{}) <-chan struct{} {
	allEnded := make(chan struct{})
	if len(chans) == 0 {
		return allEnded
	}
	go func() {
		for _, ch := range chans {
			<-ch
		}
		close(allEnded)
	}()
	return allEnded
}
