// Package panehost serves one machine's panes to the controllers that watch it. A Host runs the machine's session
// registry watch, applies each registry update to its own tmux as the daemon does, and publishes each applied update as
// a node.PaneEvent. Each watcher first gets a full listing of the panes the host lists, then every event, and a fresh
// full listing on each tick.
package panehost

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/joshsymonds/orrery/internal/daemon"
	"github.com/joshsymonds/orrery/internal/node"
	"github.com/joshsymonds/orrery/internal/paneregistry"
	"github.com/joshsymonds/orrery/internal/panewatch"
	serverapi "github.com/joshsymonds/orrery/internal/server"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// listingInterval is how often Run sends a full listing to every watcher when Config.Ticks is nil.
const listingInterval = 10 * time.Second

// Config configures a Host.
type Config struct {
	// Name is the orrery name of the host. Handler names it as the host of each pane it serves.
	Name string
	// Dir is the session registry directory, ~/.claude/sessions.
	Dir string
	// Home is the home directory. Its .claude/projects holds the transcripts, and context stamps are searched under it.
	Home string
	// Proc tells whether a pid is alive, and reads the environment of a process.
	Proc paneregistry.Proc
	// DefaultServer is the machine's default tmux server. Its panes are listed even when no update names it.
	DefaultServer tmuxctl.Server
	// Ticks, when non-nil, is where Run takes the ticks that send each watcher a fresh full listing, in place of a ticker
	// every 10 s. It is a test seam: production leaves it nil.
	Ticks <-chan time.Time
	// Load reads the host's load. Production takes hostload.Local.
	Load func() (node.Load, error)
	// Now is the clock that stamps each state change. Nil takes time.Now.
	Now func() time.Time
	// Logger receives what the host does beyond its events. Nil discards it.
	Logger *slog.Logger
}

// Host serves one machine's panes. It implements node.HostPanes. Run runs it, and Watch hands its events to a caller.
type Host struct {
	cfg     Config
	logger  *slog.Logger
	applier *daemon.Applier
	// tmux is the client of DefaultServer, which Handler serves.
	tmux *tmuxctl.Client
	// mu serializes each apply, each listing and each watcher's join, so that a watcher's first listing falls between
	// two events. It guards servers and watchers too.
	mu sync.Mutex
	// servers holds each tmux server that a listing covers, keyed by its column: DefaultServer, and each server an
	// update named.
	servers map[string]tmuxctl.Server
	// watchers holds each watcher that Watch has joined and not yet left.
	watchers map[*watcher]struct{}
}

var _ node.HostPanes = (*Host)(nil)

// New returns a Host for cfg. It fails when cfg.DefaultServer names both a Name and a Socket.
func New(cfg Config) (*Host, error) {
	tmux, err := tmuxctl.New(cfg.DefaultServer)
	if err != nil {
		return nil, fmt.Errorf("panehost: default server: %w", err)
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Host{
		cfg:      cfg,
		logger:   logger,
		applier:  daemon.NewApplier(cfg.Home, cfg.Now, logger),
		tmux:     tmux,
		servers:  map[string]tmuxctl.Server{cfg.DefaultServer.Column(): cfg.DefaultServer},
		watchers: map[*watcher]struct{}{},
	}, nil
}

// Run runs the host until ctx ends. It runs the registry's pane watch, applies each update to tmux and publishes its
// event, and sends a fresh full listing to every watcher on each tick. It returns nil once ctx ends, and otherwise the
// error that ended the registry watch. Run is called once.
func (h *Host) Run(ctx context.Context) error {
	watchCtx, stopWatch := context.WithCancel(ctx)
	defer stopWatch()
	updates := make(chan daemon.PaneUpdate)
	registry := make(chan error, 1)
	go func() { registry <- panewatch.Run(watchCtx, h.watchConfig(updates)) }()
	ticks := h.cfg.Ticks
	if ticks == nil {
		ticker := time.NewTicker(listingInterval)
		defer ticker.Stop()
		ticks = ticker.C
	}
	for {
		select {
		case <-ctx.Done():
			stopWatch()
			return endOf(ctx, <-registry)
		case err := <-registry:
			return err
		case update := <-updates:
			h.applyAndPublish(ctx, update)
		case <-ticks:
			h.broadcastListing(ctx)
		}
	}
}

// Handler serves orrery.v1.Panes over the panes of DefaultServer, with Host set to Name.
func (h *Host) Handler() http.Handler {
	_, handler := serverapi.HostPanesHandler(serverapi.HostPanesConfig{
		Tmux: h.tmux,
		Host: h.cfg.Name,
		Now:  h.cfg.Now,
	})
	return handler
}

// Load returns the host's load, as Config.Load reads it.
func (h *Host) Load(_ context.Context) (node.Load, error) {
	return h.cfg.Load()
}

// endOf returns err, the error that the registry watch ended with, unless ctx's end caused it.
func endOf(ctx context.Context, err error) error {
	if errors.Is(err, ctx.Err()) {
		return nil
	}
	return err
}

// watchConfig returns the config of the registry watch, which sends its updates on updates.
func (h *Host) watchConfig(updates chan<- daemon.PaneUpdate) panewatch.Config {
	return panewatch.Config{
		Dir:     h.cfg.Dir,
		Home:    h.cfg.Home,
		Proc:    h.cfg.Proc,
		Updates: updates,
		Unreadable: func(path string, err error) {
			h.logger.Warn("unreadable session file", "path", path, "error", err)
		},
		Logger: h.logger,
	}
}

// applyAndPublish applies update to tmux, and queues its event for every watcher: the pane's snapshot, and its
// transition when the update changed the state. It holds mu throughout, so the event falls in order with the listings.
func (h *Host) applyAndPublish(ctx context.Context, update daemon.PaneUpdate) {
	h.mu.Lock()
	defer h.mu.Unlock()
	server := daemon.ServerOf(update.Tmux)
	h.servers[server.Column()] = server
	applied, err := h.applier.Apply(ctx, update)
	if err != nil {
		h.logger.ErrorContext(ctx, "apply pane update", "pane", update.PaneID, "error", err)
		return
	}
	pane := node.PaneSnapshot{
		Server: server.Column(),
		Pane:   update.PaneID,
		ID:     h.ensureID(ctx, server, update.PaneID, applied.ID),
		Fields: applied.Fields,
	}
	event := node.PaneEvent{Pane: &pane}
	if applied.Changed {
		event.Transition = transitionOf(applied.Transition)
	}
	h.publish(event)
}

// ensureID returns id, the @orrery_id of pane on server, or else a minted id set on the pane. A minted id that cannot
// be set is logged and reported as "", since a snapshot carries an id only when tmux holds it.
func (h *Host) ensureID(ctx context.Context, server tmuxctl.Server, pane, id string) string {
	if id != "" {
		return id
	}
	minted, err := daemon.MintID()
	if err == nil {
		err = daemon.SetPaneID(ctx, server, pane, minted)
	}
	if err != nil {
		h.logger.ErrorContext(ctx, "mint pane id", "server", server.Column(), "pane", pane, "error", err)
		return ""
	}
	return minted
}

// transitionOf returns the wire form of change, the state change that an applied update made.
func transitionOf(change daemon.Transition) *node.PaneTransition {
	return &node.PaneTransition{
		From:        change.From,
		To:          change.To,
		Cwd:         change.Cwd,
		BaseURL:     change.BaseURL,
		Transcript:  change.Transcript,
		LastMessage: change.LastMessage,
		Failed:      change.Failed,
		APIError:    change.APIError,
	}
}
