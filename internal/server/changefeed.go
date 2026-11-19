package server

import (
	"context"
	"sync"
	"time"

	"connectrpc.com/connect"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	enginev1connect "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
)

// feedRetry is how long the hub waits before it reopens a Changes call that ended or failed while subscribers remain.
const feedRetry = time.Second

// changeHub holds the one Changes.Watch call that wakes the Events.Watch streams of a handler. It opens the call when
// its first subscriber arrives and cancels it when the last one leaves. Each message the call delivers, the opened
// message included, wakes every subscriber. A nil changes wakes none.
type changeHub struct {
	changes  enginev1connect.ChangesClient
	newTimer func(time.Duration) (<-chan time.Time, func() bool)

	mu     sync.Mutex
	subs   map[chan struct{}]struct{}
	cancel context.CancelFunc // cancels the open call, or nil when no subscriber holds one
	last   <-chan struct{}    // closed once the goroutine of the most recent call has exited
}

// newChangeHub returns a hub over changes that arms its retry timer through newTimer, or through time.NewTimer when
// newTimer is nil.
func newChangeHub(
	changes enginev1connect.ChangesClient,
	newTimer func(time.Duration) (<-chan time.Time, func() bool),
) *changeHub {
	if newTimer == nil {
		newTimer = stdTimer
	}
	started := make(chan struct{})
	close(started)
	return &changeHub{
		changes: changes, newTimer: newTimer, subs: make(map[chan struct{}]struct{}), last: started,
	}
}

// stdTimer is time.NewTimer as a NewTimer: the channel the timer fires on, and its Stop.
func stdTimer(d time.Duration) (<-chan time.Time, func() bool) {
	timer := time.NewTimer(d)
	return timer.C, timer.Stop
}

// subscribe returns the wake channel of a new subscriber and the cancel that ends its subscription. The wake channel
// holds one token once a message has arrived since the subscriber last took it, so no wake is lost by a subscriber that
// is busy. The first subscriber opens the call on a context that keeps ctx's values but not its cancellation, so the
// call outlives the subscriber's request and ends when the last subscriber leaves. A nil changes returns a channel that
// is never signaled.
func (h *changeHub) subscribe(ctx context.Context) (<-chan struct{}, func()) {
	wake := make(chan struct{}, 1)
	if h.changes == nil {
		return wake, func() {}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.subs[wake] = struct{}{}
	if h.cancel == nil {
		callCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
		done := make(chan struct{})
		h.cancel = cancel
		go h.run(callCtx, h.last, done)
		h.last = done
	}
	return wake, func() { h.unsubscribe(wake) }
}

// unsubscribe removes the subscriber whose wake channel is wake, and cancels the open call when none remains.
func (h *changeHub) unsubscribe(wake chan struct{}) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.subs, wake)
	if len(h.subs) == 0 && h.cancel != nil {
		h.cancel()
		h.cancel = nil
	}
}

// signal wakes every subscriber without blocking, so a subscriber whose token is already pending takes no second one.
func (h *changeHub) signal() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for wake := range h.subs {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
}

// run keeps a call open until ctx ends. It first waits for prev, the goroutine of the call before it, to exit, so no
// two calls are open at once. A call that ends or fails is reopened after feedRetry.
func (h *changeHub) run(ctx context.Context, prev <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	<-prev
	for ctx.Err() == nil {
		h.watchCall(ctx)
		if ctx.Err() != nil {
			return
		}
		h.pause(ctx)
	}
}

// watchCall opens one Changes.Watch call and signals every subscriber on each message it delivers, until the call
// ends. Neither the call's error nor how it ended is returned: run retries a call that fails exactly as one that ends,
// and a subscriber reads from its own cursor on the wake the reopened call's opened message brings, so nothing appended
// in between is lost. Canceling the call's own context on return releases its connection either way.
func (h *changeHub) watchCall(ctx context.Context) {
	call, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := h.changes.Watch(call, connect.NewRequest(&enginev1.ChangesWatchRequest{}))
	if err != nil {
		return
	}
	for stream.Receive() {
		h.signal()
	}
}

// pause waits feedRetry on a timer from newTimer, or until ctx ends, and stops the timer before it returns.
func (h *changeHub) pause(ctx context.Context) {
	fired, stop := h.newTimer(feedRetry)
	defer stop()
	select {
	case <-ctx.Done():
	case <-fired:
	}
}
