// Package hostfollow follows a remote host's panes into the store under that host. Follow watches the host's agent:
// each pane event is stored under the host, each full listing syncs the servers it names and removes the rows of the
// servers the host no longer names, and a watch that cannot open or breaks is opened again after Retry.
package hostfollow

import (
	"context"
	"log/slog"
	"maps"
	"slices"
	"time"

	"github.com/joshsymonds/orrery/internal/holder"
	"github.com/joshsymonds/orrery/internal/node"
	"github.com/joshsymonds/orrery/internal/store"
)

// retryWait is how long Follow waits before it opens a new watch after one that failed or broke. Production waits the
// bound of its client, which is node.DefaultBound.
const retryWait = node.DefaultBound

// PaneWatcher opens a host's pane watch. *node.Client satisfies it.
type PaneWatcher interface {
	WatchPanes(parent context.Context) (*node.PaneWatch, error)
}

// Config configures Follow: the host whose panes it follows, the client that watches its agent, the store that
// receives the panes, the clock that stamps each write, the clock that waits before a new watch, the callback of each
// transition, and the logger.
type Config struct {
	// Host is the orrery name of the host. Each pane is stored under it.
	Host string
	// Client opens the host's pane watch.
	Client PaneWatcher
	// Store receives the panes.
	Store *store.Store
	// Now stamps each write.
	Now func() time.Time
	// Retry is the clock that waits before a new watch is opened.
	Retry holder.Clock
	// Transitions, when non-nil, is called with the host and each event that carries a Transition, after its pane is
	// stored.
	Transitions func(host string, event node.PaneEvent)
	// Logger receives what Follow does beyond its writes. Nil discards it.
	Logger *slog.Logger
}

// Follow watches the host's agent until ctx ends. Each watch delivers a full listing first and then each change, and
// each event is stored as it arrives. While a watch cannot open or has broken, no tidy runs, the host's stored rows
// stay, and Follow opens a new watch once Retry has waited retryWait.
func Follow(ctx context.Context, cfg Config) {
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	for {
		watchOnce(ctx, cfg)
		select {
		case <-ctx.Done():
			return
		case <-cfg.Retry.After(retryWait):
		}
	}
}

// watchOnce opens one pane watch of the host's agent, and stores each of its events until the watch breaks or ctx ends.
func watchOnce(ctx context.Context, cfg Config) {
	watch, err := cfg.Client.WatchPanes(ctx)
	if err != nil {
		if ctx.Err() == nil {
			cfg.Logger.WarnContext(ctx, "hostfollow: open the pane watch", "host", cfg.Host, "error", err)
		}
		return
	}
	defer closeWatch(ctx, cfg, watch)
	for {
		event, nextErr := watch.Next()
		if nextErr != nil {
			if ctx.Err() == nil {
				cfg.Logger.WarnContext(ctx, "hostfollow: pane watch broke", "host", cfg.Host, "error", nextErr)
			}
			return
		}
		record(ctx, cfg, event)
	}
}

// closeWatch closes watch. A failed close is logged, since the watch is over either way.
func closeWatch(ctx context.Context, cfg Config, watch *node.PaneWatch) {
	if err := watch.Close(); err != nil {
		cfg.Logger.DebugContext(ctx, "hostfollow: close the pane watch", "host", cfg.Host, "error", err)
	}
}

// record stores one event. A full listing syncs the servers it names, and a pane event stores its pane. Then the
// event's transition, when it has one, goes to cfg.Transitions.
func record(ctx context.Context, cfg Config, event node.PaneEvent) {
	switch {
	case event.Full:
		syncListing(ctx, cfg, event.Listing)
	case event.Pane != nil:
		syncPane(ctx, cfg, *event.Pane)
		if event.Transition != nil && cfg.Transitions != nil {
			cfg.Transitions(cfg.Host, event)
		}
	}
}

// syncPane stores one pane's snapshot under the host. A snapshot with no @orrery_id is not stored, since its row would
// have no key.
func syncPane(ctx context.Context, cfg Config, snapshot node.PaneSnapshot) {
	if snapshot.ID == "" {
		cfg.Logger.WarnContext(ctx, "hostfollow: pane has no id", "host", cfg.Host, "server", snapshot.Server,
			"pane", snapshot.Pane)
		return
	}
	if err := cfg.Store.SyncPane(ctx, snapshot.Server, paneOf(cfg.Host, snapshot), cfg.Now()); err != nil {
		cfg.Logger.ErrorContext(ctx, "hostfollow: store pane", "host", cfg.Host, "pane", snapshot.Pane, "error", err)
	}
}

// syncListing stores the panes of a full listing under the host. Each server the listing names is synced with its
// panes, and then each server stored under the host that the listing does not name is synced empty, so the rows of the
// panes that are gone are removed. Rows of other hosts, and of other servers, are left alone.
func syncListing(ctx context.Context, cfg Config, listing []node.PaneSnapshot) {
	now := cfg.Now()
	named := make(map[string]bool)
	panes := make(map[string][]store.Pane)
	for _, snapshot := range listing {
		named[snapshot.Server] = true
		if snapshot.ID == "" {
			cfg.Logger.WarnContext(ctx, "hostfollow: pane has no id", "host", cfg.Host, "server", snapshot.Server,
				"pane", snapshot.Pane)
			continue
		}
		panes[snapshot.Server] = append(panes[snapshot.Server], paneOf(cfg.Host, snapshot))
	}
	for _, server := range slices.Sorted(maps.Keys(named)) {
		syncServer(ctx, cfg, server, panes[server], now)
	}
	stored, err := cfg.Store.HostServers(ctx, cfg.Host)
	if err != nil {
		cfg.Logger.ErrorContext(ctx, "hostfollow: list stored servers", "host", cfg.Host, "error", err)
		return
	}
	for _, server := range stored {
		if !named[server] {
			syncServer(ctx, cfg, server, nil, now)
		}
	}
}

// syncServer syncs the rows of the host on server to panes, and logs a failed sync.
func syncServer(ctx context.Context, cfg Config, server string, panes []store.Pane, now time.Time) {
	if err := cfg.Store.SyncServer(ctx, cfg.Host, server, panes, now); err != nil {
		cfg.Logger.ErrorContext(ctx, "hostfollow: sync server", "host", cfg.Host, "server", server, "error", err)
	}
}

// paneOf returns the store's Pane of snapshot under host.
func paneOf(host string, snapshot node.PaneSnapshot) store.Pane {
	return store.Pane{Host: host, ID: snapshot.ID, Pane: snapshot.Pane, Fields: snapshot.Fields}
}
