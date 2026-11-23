package daemon

import (
	"context"
	"time"

	"github.com/joshsymonds/orrery/internal/panetranscript"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// PaneUpdate is one update of a pane from the session registry and its transcript: the registry entry that binds the
// pane, or the unbinding of the pane.
type PaneUpdate struct {
	// PaneID is the tmux pane, and Tmux is the $TMUX of the process that runs in it, which names its server.
	PaneID string
	Tmux   string
	// Unbound is set when the registry no longer binds the pane, and the update ends its session.
	Unbound bool
	// SessionID, Cwd, BaseURL and Transcript are the session's id, its working directory, its API base URL and its
	// transcript path.
	SessionID  string
	Cwd        string
	BaseURL    string
	Transcript string
	// Status and WaitingFor are the registry entry's status and waitingFor.
	Status     string
	WaitingFor string
	// Turn is the turn that the transcript shows.
	Turn panetranscript.Turn
}

// applyUpdates applies each update received from updates, in order, on the calling goroutine, until ctx is done or
// updates is closed.
func (h *frameHandler) applyUpdates(ctx context.Context, updates <-chan PaneUpdate) {
	for {
		select {
		case <-ctx.Done():
			return
		case update, ok := <-updates:
			if !ok {
				return
			}
			h.applyUpdate(context.WithoutCancel(ctx), update)
		}
	}
}

// applyUpdate runs one registry update through applyPaneLocked, then syncs its pane to the store. An update with no
// pane is dropped, since tmux would then write to the current pane.
func (h *frameHandler) applyUpdate(ctx context.Context, update PaneUpdate) {
	if update.PaneID == "" {
		h.logger.WarnContext(ctx, "drop pane update", "reason", "empty pane")
		return
	}
	server := resolveServer(h.cfg.Server, update.Tmux)
	h.noteServer(server)
	client, err := tmuxctl.New(server)
	if err != nil {
		h.logger.ErrorContext(ctx, "resolve tmux server", "error", err)
		return
	}
	out, read, ok := h.applyPaneLocked(ctx, client, server, update)
	h.syncApplied(ctx, server, update.PaneID, out, read, ok)
}

// applyPaneLocked runs one registry update through applyPaneHeld under the mutex, with the update's context and session
// writes after its own writes. It returns the output, read and ok that syncApplied takes.
func (h *frameHandler) applyPaneLocked(
	ctx context.Context, client *tmuxctl.Client, server tmuxctl.Server, update PaneUpdate,
) (out string, read, ok bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.applyPaneHeld(ctx, client, server, update, h.registryWrites(ctx, update))
}

// applyPaneHeld runs one registry update through applyState with extra after the update's own writes, and calls every
// observer when the state changes. The mirror, when it is set, reads the pane in the same tmux invocation, and read
// reports whether that read holds the mirror's lock. The caller holds the mutex.
func (h *frameHandler) applyPaneHeld(
	ctx context.Context, client *tmuxctl.Client, server tmuxctl.Server, update PaneUpdate,
	extra []tmuxctl.OptionWrite,
) (out string, read, ok bool) {
	var then []string
	if h.cfg.Mirror != nil {
		then, read = h.cfg.Mirror.TryRead(update.PaneID)
	}
	transition, changed, out, err := applyState(ctx, client, server, update, time.Now(), extra, then)
	if err != nil {
		h.logger.ErrorContext(ctx, "apply pane update", "pane", update.PaneID, "error", err)
		return "", read, false
	}
	if !changed {
		return out, read, true
	}
	for _, observe := range h.cfg.Observers {
		observe(transition)
	}
	return out, read, true
}
