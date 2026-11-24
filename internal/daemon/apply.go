package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/joshsymonds/orrery/internal/state"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// Applied is what one pane update changed on its pane, as Applier.Apply reports it.
type Applied struct {
	// Changed is whether the update changed the pane's state. Transition holds the change with the facts of the update,
	// and it is meaningful only when Changed is set.
	Changed    bool
	Transition Transition
	// ID is the pane's @orrery_id after the update, or "" when the pane has none, and Fields are its mirrored options.
	ID     string
	Fields store.Fields
}

// Applier applies registry pane updates to tmux exactly as Serve does: the same context and session writes, the same
// state.ApplyPane and the same mutex. It is for a host that serves its panes with no store, so it syncs nothing and
// calls no observer; its caller reads each change from what Apply returns.
type Applier struct {
	h   *frameHandler
	now func() time.Time
}

// NewApplier returns an Applier that searches each update's cwd for a context stamp under home, and stamps each state
// change with now(). A nil logger discards.
func NewApplier(home string, now func() time.Time, logger *slog.Logger) *Applier {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Applier{h: &frameHandler{cfg: Config{Home: home}, logger: logger}, now: now}
}

// Apply applies update to its pane on the tmux server that its $TMUX names. It reads the pane's mirrored options in the
// same tmux invocation as its writes, so the returned ID and Fields are the pane's as the apply left it. An update with
// no pane is an error, since tmux would then write to the current pane.
func (a *Applier) Apply(ctx context.Context, update PaneUpdate) (Applied, error) {
	if update.PaneID == "" {
		return Applied{}, errors.New("pane update has no pane")
	}
	server := ServerOf(update.Tmux)
	client, err := tmuxctl.New(server)
	if err != nil {
		return Applied{}, fmt.Errorf("resolve tmux server: %w", err)
	}
	a.h.mu.Lock()
	defer a.h.mu.Unlock()
	transition, changed, out, err := applyState(
		ctx, client, server, update, a.now(), a.h.registryWrites(ctx, update), mirrorReadCommand(update.PaneID),
	)
	if err != nil {
		return Applied{}, err
	}
	id, fields, err := parsePaneRead(out)
	if err != nil {
		return Applied{}, fmt.Errorf("read pane %s options: %w", update.PaneID, err)
	}
	return Applied{Changed: changed, Transition: transition, ID: id, Fields: fields}, nil
}

// ServerOf returns the tmux server that tmuxEnv names, a $TMUX value: the socket before its first comma, or the default
// server when tmuxEnv is empty. It is how Serve resolves an update's server when Config.Server is unset.
func ServerOf(tmuxEnv string) tmuxctl.Server {
	return resolveServer(tmuxctl.Server{}, tmuxEnv)
}

// applyState runs update through state.ApplyPane on its pane of client's server, with extra after the update's own
// writes, and reads the pane with then in the same tmux invocation. It returns the transition the update made, with the
// update's facts, whether the update changed the state, and the output of then.
func applyState(
	ctx context.Context, client *tmuxctl.Client, server tmuxctl.Server, update PaneUpdate, now time.Time,
	extra []tmuxctl.OptionWrite, then []string,
) (Transition, bool, string, error) {
	pane := state.Pane{Unbound: update.Unbound, Status: update.Status, WaitingFor: update.WaitingFor, Turn: update.Turn}
	change, changed, out, err := state.ApplyPane(ctx, client, update.PaneID, pane, now, extra, then)
	if err != nil {
		return Transition{}, false, "", fmt.Errorf("apply state: %w", err)
	}
	return Transition{
		PaneID:      update.PaneID,
		Server:      server,
		From:        change.From,
		To:          change.To,
		Cwd:         update.Cwd,
		BaseURL:     update.BaseURL,
		Transcript:  update.Transcript,
		LastMessage: update.Turn.LastMessage,
		Failed:      update.Turn.Failed,
		APIError:    update.Turn.APIError,
	}, changed, out, nil
}

// registryWrites returns the writes that follow an update's own writes: its context, and its session and transcript
// unless it unbinds the pane, since its own writes unset those. The caller holds the mutex.
func (h *frameHandler) registryWrites(ctx context.Context, update PaneUpdate) []tmuxctl.OptionWrite {
	extra := []tmuxctl.OptionWrite{h.contextWrite(ctx, update.PaneID, update.Cwd, update.BaseURL)}
	if !update.Unbound {
		extra = append(extra, state.SessionValueWrites(update.SessionID, update.Transcript)...)
	}
	return extra
}
