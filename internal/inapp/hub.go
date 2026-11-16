// Package inapp holds the notices open web pages show in-app. Show and End change the set of notices showing, and each
// watcher receives the set as it stood when it watched, then every change after it, in order.
package inapp

import (
	"slices"
	"sync"

	"github.com/joshsymonds/orrery/internal/deskstream"
	"github.com/joshsymonds/orrery/internal/notice"
)

// queueLen is how many changes a watcher may hold unread. A watcher that falls this far behind is closed and dropped.
const queueLen = 64

// Notice is one notice showing in-app.
type Notice struct {
	Key                notice.Key
	State, Title, Body string
	Block, BlockRef    string
}

// Change is one change to the notices showing: a notice now showing, or the end of one. An ended change carries only
// the key of the notice that ended.
type Change struct {
	Notice Notice
	Ended  bool
}

// Hub holds the notices showing in-app and sends their changes to watchers. It is safe for concurrent use.
type Hub struct {
	mu       sync.Mutex
	notices  map[notice.Key]Notice
	order    []notice.Key
	watchers map[chan Change]struct{}
	closed   bool
}

// NewHub returns a hub with no notices showing and no watchers.
func NewHub() *Hub {
	return &Hub{notices: map[notice.Key]Notice{}, watchers: map[chan Change]struct{}{}}
}

// Show records n as the notice showing for key, replacing any notice already there, and sends a shown change to every
// watcher. A replaced notice keeps its place in the order.
func (h *Hub) Show(key notice.Key, n deskstream.Notice) {
	h.mu.Lock()
	defer h.mu.Unlock()
	shown := Notice{Key: key, State: n.State, Title: n.Title, Body: n.Body, Block: n.Block, BlockRef: n.BlockRef}
	if _, ok := h.notices[key]; !ok {
		h.order = append(h.order, key)
	}
	h.notices[key] = shown
	h.sendLocked(Change{Notice: shown})
}

// End removes the notice showing for key and sends an ended change to every watcher. It does nothing for a key with no
// notice showing.
func (h *Hub) End(key notice.Key) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.notices[key]; !ok {
		return
	}
	delete(h.notices, key)
	h.order = slices.DeleteFunc(h.order, func(k notice.Key) bool { return k == key })
	h.sendLocked(Change{Notice: Notice{Key: key}, Ended: true})
}

// Watch returns the notices showing now, in the order first shown, and a channel that carries every change after that
// snapshot, in order. stop, or Close, closes the channel. A watcher that falls queueLen changes behind is closed and
// dropped. Watch after Close returns no notices and a closed channel.
func (h *Hub) Watch() (shown []Notice, changes <-chan Change, stop func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	ch := make(chan Change, queueLen)
	if h.closed {
		close(ch)
		return nil, ch, func() {}
	}
	shown = make([]Notice, 0, len(h.order))
	for _, key := range h.order {
		shown = append(shown, h.notices[key])
	}
	h.watchers[ch] = struct{}{}
	return shown, ch, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.dropLocked(ch)
	}
}

// Close ends every watch and makes later Watch calls return closed channels. It is safe to call more than once.
func (h *Hub) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = true
	for ch := range h.watchers {
		h.dropLocked(ch)
	}
}

// sendLocked queues c on every watcher. A watcher whose queue is full is closed and dropped rather than waited for.
// Callers hold h.mu.
func (h *Hub) sendLocked(c Change) {
	for ch := range h.watchers {
		select {
		case ch <- c:
		default:
			h.dropLocked(ch)
		}
	}
}

// dropLocked removes the watcher on ch and closes ch. It does nothing when ch is no longer a watcher, so a second stop
// is harmless. Callers hold h.mu.
func (h *Hub) dropLocked(ch chan Change) {
	if _, ok := h.watchers[ch]; !ok {
		return
	}
	delete(h.watchers, ch)
	close(ch)
}
