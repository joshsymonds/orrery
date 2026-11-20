package server

import (
	"context"
	"slices"
	"sync"
	"time"

	"connectrpc.com/connect"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	enginev1connect "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
)

// feedRetry is how long the hub waits before it reopens a Changes call that ended or failed while subscribers remain.
const feedRetry = time.Second

// changeHub holds the one Changes.Watch call that wakes the streams of a handler: the Events.Watch streams, the
// Panes.WatchConversation streams and the Sessions.WatchConversation streams that follow a session. It opens the call
// when its first subscriber arrives and cancels it when the last one leaves. A message wakes each subscriber it
// concerns, and the opened message concerns every subscriber. A nil changes wakes none.
type changeHub struct {
	changes  enginev1connect.ChangesClient
	newTimer func(time.Duration) (<-chan time.Time, func() bool)

	mu     sync.Mutex
	subs   map[*hubSubscriber]struct{}
	cancel context.CancelFunc // cancels the open call, or nil when no subscriber holds one
	last   <-chan struct{}    // closed once the goroutine of the most recent call has exited
}

// hubSubscriber is one subscriber of a changeHub. session is the session it follows, or empty while it follows every
// message. The hub's mu guards session.
type hubSubscriber struct {
	wake    chan struct{}
	session string
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
		changes: changes, newTimer: newTimer, subs: make(map[*hubSubscriber]struct{}), last: started,
	}
}

// stdTimer is time.NewTimer as a NewTimer: the channel the timer fires on, and its Stop.
func stdTimer(d time.Duration) (<-chan time.Time, func() bool) {
	timer := time.NewTimer(d)
	return timer.C, timer.Stop
}

// subscribe returns the wake channel of a new subscriber that wakes on every message, and the cancel that ends its
// subscription. The wake channel holds one token once a message has arrived since the subscriber last took it, so no
// wake is lost by a subscriber that is busy. The first subscriber opens the call on a context that keeps ctx's values
// but not its cancellation, so the call outlives the subscriber's request and ends when the last subscriber leaves. A
// nil changes returns a channel that is never signaled.
func (h *changeHub) subscribe(ctx context.Context) (<-chan struct{}, func()) {
	sub, cancel := h.open(ctx)
	return sub.wake, cancel
}

// subscribeFiltered is subscribe for a subscriber that follows one session. The subscriber wakes on every message until
// its follow names a session, and then only on the messages whose sessions include that session. follow may name a
// session again. The opened message wakes the subscriber either way. A nil changes returns a channel that is never
// signaled.
func (h *changeHub) subscribeFiltered(ctx context.Context) (<-chan struct{}, func(session string), func()) {
	sub, cancel := h.open(ctx)
	return sub.wake, func(session string) { h.follow(sub, session) }, cancel
}

// open registers a new subscriber, and opens the call when no subscriber holds one, as subscribe documents. A nil
// changes registers nothing.
func (h *changeHub) open(ctx context.Context) (*hubSubscriber, func()) {
	sub := &hubSubscriber{wake: make(chan struct{}, 1)}
	if h.changes == nil {
		return sub, func() {}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.subs[sub] = struct{}{}
	if h.cancel == nil {
		callCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
		done := make(chan struct{})
		h.cancel = cancel
		go h.run(callCtx, h.last, done)
		h.last = done
	}
	return sub, func() { h.unsubscribe(sub) }
}

// follow names the session that sub follows. A message then wakes sub only when its sessions include session, and an
// empty session makes sub follow every message again.
func (h *changeHub) follow(sub *hubSubscriber, session string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	sub.session = session
}

// unsubscribe removes sub, and cancels the open call when no subscriber remains.
func (h *changeHub) unsubscribe(sub *hubSubscriber) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.subs, sub)
	if len(h.subs) == 0 && h.cancel != nil {
		h.cancel()
		h.cancel = nil
	}
}

// signal wakes each subscriber that msg concerns, without blocking, so a subscriber whose token is already pending
// takes no second one.
func (h *changeHub) signal(msg *enginev1.ChangesWatchResponse) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for sub := range h.subs {
		if !sub.concerns(msg) {
			continue
		}
		select {
		case sub.wake <- struct{}{}:
		default:
		}
	}
}

// concerns reports whether msg wakes sub. The opened message wakes every subscriber. Any other message wakes a
// subscriber that follows no session, or one whose sessions include the session sub follows. The caller holds the
// hub's mu.
func (sub *hubSubscriber) concerns(msg *enginev1.ChangesWatchResponse) bool {
	return msg.GetOpened() || sub.session == "" || slices.Contains(msg.GetSessions(), sub.session)
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

// watchCall opens one Changes.Watch call and signals the subscribers each message it delivers concerns, until the call
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
		h.signal(stream.Msg())
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
