package enginehost

import (
	"strings"
	"sync"
	"time"
)

// The waits of a model call that failed before it is made again, by its failures in a row: callRetryWait1 after the
// first, callRetryWait2 after the second, callRetryWait3 after the third, and callRetryWaitLater after each one after that.
const (
	callRetryWait1     = 5 * time.Second
	callRetryWait2     = 30 * time.Second
	callRetryWait3     = time.Minute
	callRetryWaitLater = 5 * time.Minute
)

// callRetryWait returns how long a model call waits before it is made again after its failures-th failure in a row, at
// least 1.
func callRetryWait(failures int) time.Duration {
	waits := [...]time.Duration{callRetryWait1, callRetryWait2, callRetryWait3, callRetryWaitLater}
	return waits[min(failures, len(waits))-1]
}

// callRetries holds back the model calls that failed, by key, until their wait ends. A failure arms the key's deadline
// for callRetryWait of its failures in a row, and the deadline calls the wake the failure named, so the loop reads the key
// again and calls for it once more. A key is ready when it has no failure held, or its deadline has fired. A success, or
// a read that finds the key needs no call, forgets it. The failures are kept in memory: a restart calls for each key at
// once.
type callRetries struct {
	afterFunc func(time.Duration, func()) func() bool

	mu       sync.Mutex
	failures map[string]int
	// waiting holds the stop of each key's armed deadline, by key. A deadline removes its own entry when it fires.
	waiting map[string]func() bool
}

// newCallRetries returns a callRetries whose deadlines come from afterFunc, which arms a function to run once after a
// duration and returns its stop, as time.AfterFunc does. A nil afterFunc wraps time.AfterFunc.
func newCallRetries(afterFunc func(time.Duration, func()) func() bool) *callRetries {
	if afterFunc == nil {
		afterFunc = func(d time.Duration, f func()) func() bool { return time.AfterFunc(d, f).Stop }
	}
	return &callRetries{afterFunc: afterFunc, failures: make(map[string]int), waiting: make(map[string]func() bool)}
}

// ready reports whether a call for key may be made now: no failure of key is waiting out its deadline.
func (r *callRetries) ready(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, waiting := r.waiting[key]
	return !waiting
}

// failed records a failed call for key and arms its deadline, which calls wake once the wait for key's failures in a row
// has passed. A key whose deadline is armed already keeps it.
func (r *callRetries) failed(key string, wake func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, waiting := r.waiting[key]; waiting {
		return
	}
	r.failures[key]++
	r.waiting[key] = r.afterFunc(callRetryWait(r.failures[key]), func() {
		r.mu.Lock()
		delete(r.waiting, key)
		r.mu.Unlock()
		wake()
	})
}

// forget drops key's failures and stops its deadline, once a call for it succeeded or it needs no call.
func (r *callRetries) forget(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if stop, waiting := r.waiting[key]; waiting {
		stop()
		delete(r.waiting, key)
	}
	delete(r.failures, key)
}

// forgetPrefix forgets each key that starts with prefix, as forget does.
func (r *callRetries) forgetPrefix(prefix string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for key, stop := range r.waiting {
		if strings.HasPrefix(key, prefix) {
			stop()
			delete(r.waiting, key)
		}
	}
	for key := range r.failures {
		if strings.HasPrefix(key, prefix) {
			delete(r.failures, key)
		}
	}
}

// stop stops every armed deadline, so none wakes a loop that has returned.
func (r *callRetries) stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, stop := range r.waiting {
		stop()
	}
	clear(r.waiting)
}
