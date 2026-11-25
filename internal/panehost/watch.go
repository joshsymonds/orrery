package panehost

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sync"

	"github.com/joshsymonds/orrery/internal/daemon"
	"github.com/joshsymonds/orrery/internal/node"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// watcher is the queue of one Watch call. The host pushes each event to it in order, and the call sends the queued
// events. A push never blocks, so a slow send holds up no apply.
type watcher struct {
	mu    sync.Mutex
	queue []node.PaneEvent
	// wake holds a token once the queue may hold events, so the call wakes to take them.
	wake chan struct{}
}

// newWatcher returns a watcher with an empty queue.
func newWatcher() *watcher {
	return &watcher{wake: make(chan struct{}, 1)}
}

// push queues event, and wakes the call that sends it.
func (w *watcher) push(event node.PaneEvent) {
	w.mu.Lock()
	w.queue = append(w.queue, event)
	w.mu.Unlock()
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// take returns the queued events in order, and empties the queue.
func (w *watcher) take() []node.PaneEvent {
	w.mu.Lock()
	defer w.mu.Unlock()
	events := w.queue
	w.queue = nil
	return events
}

// Watch sends a full listing of the host's panes first, then each event the host publishes, until ctx ends or send
// fails. It returns the error that ended the watch: ctx's, or the one send returned.
func (h *Host) Watch(ctx context.Context, send func(node.PaneEvent) error) error {
	queue := newWatcher()
	h.join(ctx, queue)
	defer h.leave(queue)
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("panehost: watch: %w", ctx.Err())
		case <-queue.wake:
		}
		for _, event := range queue.take() {
			if err := send(event); err != nil {
				return fmt.Errorf("panehost: send pane event: %w", err)
			}
		}
	}
}

// join adds w to the watchers, with a full listing queued first. The listing is taken under mu, so it falls between
// the events before and after it.
func (h *Host) join(ctx context.Context, w *watcher) {
	h.mu.Lock()
	defer h.mu.Unlock()
	w.push(h.listingEvent(ctx))
	h.watchers[w] = struct{}{}
}

// leave removes w from the watchers, so the host queues nothing more for it.
func (h *Host) leave(w *watcher) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.watchers, w)
}

// broadcastListing queues a fresh full listing for every watcher, as each tick does.
func (h *Host) broadcastListing(ctx context.Context) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.publish(h.listingEvent(ctx))
}

// publish queues event for every watcher. The caller holds mu.
func (h *Host) publish(event node.PaneEvent) {
	for w := range h.watchers {
		w.push(event)
	}
}

// listingEvent returns the full listing event of the host's panes. The caller holds mu.
func (h *Host) listingEvent(ctx context.Context) node.PaneEvent {
	return node.PaneEvent{Full: true, Listing: h.listing(ctx)}
}

// listing returns a snapshot of each pane of each server the host lists, the servers in column order. The caller holds
// mu.
func (h *Host) listing(ctx context.Context) []node.PaneSnapshot {
	panes := make([]node.PaneSnapshot, 0, len(h.servers))
	for _, column := range slices.Sorted(maps.Keys(h.servers)) {
		panes = append(panes, h.listServer(ctx, h.servers[column])...)
	}
	return panes
}

// listServer returns a snapshot of each pane of server, minting the @orrery_id of each pane that has none. A server
// that cannot be listed is logged, and contributes no panes.
func (h *Host) listServer(ctx context.Context, server tmuxctl.Server) []node.PaneSnapshot {
	rows, err := daemon.ListPanes(ctx, server)
	if err != nil {
		h.logger.DebugContext(ctx, "list panes", "server", server.Column(), "error", err)
		return nil
	}
	panes := make([]node.PaneSnapshot, 0, len(rows))
	for _, row := range rows {
		panes = append(panes, node.PaneSnapshot{
			Server: server.Column(),
			Pane:   row.Pane,
			ID:     h.ensureID(ctx, server, row.Pane, row.ID),
			Fields: row.Fields,
		})
	}
	return panes
}
