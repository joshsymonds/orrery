package daemon

import (
	"context"
	"time"

	"github.com/joshsymonds/orrery/internal/panetranscript"
	"github.com/joshsymonds/orrery/internal/state"
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

// applyPaneLocked runs one registry update through state.ApplyPane under the mutex, and calls every observer when the
// state changes. The update's context and session writes follow the update's own writes; an unbound update writes no
// session, since its own writes unset it. It returns the output, read and ok that syncApplied takes.
func (h *frameHandler) applyPaneLocked(
	ctx context.Context, client *tmuxctl.Client, server tmuxctl.Server, update PaneUpdate,
) (out string, read, ok bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	extra := []tmuxctl.OptionWrite{h.contextWrite(ctx, update.PaneID, update.Cwd, update.BaseURL)}
	if !update.Unbound {
		extra = append(extra, state.SessionValueWrites(update.SessionID, update.Transcript)...)
	}
	return h.applyPaneHeld(ctx, client, server, update, extra)
}

// applyPaneHeld runs one registry update through state.ApplyPane with extra after the update's own writes, and calls
// every observer when the state changes, as applyPaneLocked describes. The caller holds the mutex.
func (h *frameHandler) applyPaneHeld(
	ctx context.Context, client *tmuxctl.Client, server tmuxctl.Server, update PaneUpdate,
	extra []tmuxctl.OptionWrite,
) (out string, read, ok bool) {
	var then []string
	if h.cfg.Mirror != nil {
		then, read = h.cfg.Mirror.TryRead(update.PaneID)
	}
	pane := state.Pane{Unbound: update.Unbound, Status: update.Status, WaitingFor: update.WaitingFor, Turn: update.Turn}
	transition, changed, out, err := state.ApplyPane(ctx, client, update.PaneID, pane, time.Now(), extra, then)
	if err != nil {
		h.logger.ErrorContext(ctx, "apply pane update", "pane", update.PaneID, "error", err)
		return "", read, false
	}
	if !changed {
		return out, read, true
	}
	event := Transition{
		PaneID:      update.PaneID,
		Server:      server,
		From:        transition.From,
		To:          transition.To,
		Cwd:         update.Cwd,
		BaseURL:     update.BaseURL,
		Transcript:  update.Transcript,
		LastMessage: update.Turn.LastMessage,
		Failed:      update.Turn.Failed,
		APIError:    update.Turn.APIError,
	}
	for _, observe := range h.cfg.Observers {
		observe(event)
	}
	return out, read, true
}
