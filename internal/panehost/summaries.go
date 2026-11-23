package panehost

import (
	"context"
	"fmt"

	"github.com/joshsymonds/orrery/internal/autoseen"
	"github.com/joshsymonds/orrery/internal/daemon"
	"github.com/joshsymonds/orrery/internal/node"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/summary"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// observe runs the observers of one state change of transition, on the tmux server the pane runs on. A session's end
// forgets the pane on its summarizer, a working or done transition requests the pane's summary, and a done transition
// runs the auto-seen check. The caller holds mu.
func (h *Host) observe(ctx context.Context, server tmuxctl.Server, transition daemon.Transition) {
	if transition.To == "" {
		h.forget(context.WithoutCancel(ctx), server, transition.PaneID)
	}
	if transition.To == protocol.StateWorking || transition.To == protocol.StateDone {
		h.summarize(ctx, server, transition)
	}
	if transition.To == protocol.StateDone {
		h.markSeen(context.WithoutCancel(ctx), server, transition)
	}
}

// summarize requests the summary of transition's pane from the summarizer of its server. A working request is a
// periodic refresh. A done request is the Stop run, and carries the turn's last message. The request settles on the
// summarizer's own goroutine, so summarize does not wait for it. The caller holds mu.
func (h *Host) summarize(ctx context.Context, server tmuxctl.Server, transition daemon.Transition) {
	summarizer, err := h.summarizerFor(server)
	if err != nil {
		h.logger.ErrorContext(ctx, "create summarizer", "pane", transition.PaneID, "error", err)
		return
	}
	req := summary.Request{
		PaneID:         transition.PaneID,
		BaseURL:        transition.BaseURL,
		Dir:            transition.Cwd,
		TranscriptPath: transition.Transcript,
	}
	if transition.To == protocol.StateDone {
		req.Stop = true
		req.LastAssistantMessage = transition.LastMessage
	}
	summarizer.Request(ctx, req)
}

// forget ends pane's session on the summarizer of its server, which unsets the pane's summary and label, and then
// publishes the pane's snapshot, so the controller stores them unset. The caller holds mu.
func (h *Host) forget(ctx context.Context, server tmuxctl.Server, pane string) {
	summarizer, err := h.summarizerFor(server)
	if err != nil {
		h.logger.ErrorContext(ctx, "create summarizer", "pane", pane, "error", err)
		return
	}
	summarizer.Forget(pane)
	h.publishPane(ctx, server, pane)
}

// markSeen runs the auto-seen check of transition, a done, on the tmux server its pane runs on, through autoseen's
// observer. The observer's report runs under mu, from the same call, so it publishes the pane's snapshot as the seen
// write leaves it, and the controller stores the seen time too. The caller holds mu.
func (h *Host) markSeen(ctx context.Context, server tmuxctl.Server, transition daemon.Transition) {
	observe := autoseen.New(h.cfg.Now, h.logger, func(pane string, _ int64) {
		h.publishPane(ctx, server, pane)
	}, h.cfg.Viewer)
	observe(transition)
}

// summarizerFor returns the summarizer of the tmux server, created on first use. Its runs write each pane's options on
// the server, and its OnWrite publishes the pane's snapshot once they are written. The caller holds mu.
func (h *Host) summarizerFor(server tmuxctl.Server) (*summary.Summarizer, error) {
	if s, ok := h.summarizers[server.Column()]; ok {
		return s, nil
	}
	client, err := tmuxctl.New(server)
	if err != nil {
		return nil, fmt.Errorf("tmux server: %w", err)
	}
	summarizer, err := summary.New(summary.Config{
		Claude:  h.cfg.Claude,
		Models:  h.cfg.Models,
		Home:    h.cfg.Home,
		Tmux:    client,
		Now:     h.cfg.Now,
		Logger:  h.logger,
		OnWrite: func(ctx context.Context, pane string) { h.paneWritten(ctx, server, pane) },
	})
	if err != nil {
		return nil, fmt.Errorf("summarizer: %w", err)
	}
	h.summarizers[server.Column()] = summarizer
	return summarizer, nil
}

// paneWritten publishes the snapshot of pane on server after a summary run has written the pane's options. The run's
// goroutine calls it, so it takes mu.
func (h *Host) paneWritten(ctx context.Context, server tmuxctl.Server, pane string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.publishPane(ctx, server, pane)
}

// publishPane queues the snapshot of pane on server, as the server holds it now, for every watcher. A pane that is not
// listed queues nothing. The caller holds mu, so the snapshot falls in order with the events around it.
func (h *Host) publishPane(ctx context.Context, server tmuxctl.Server, pane string) {
	for _, snapshot := range h.listServer(ctx, server) {
		if snapshot.Pane == pane {
			h.publish(node.PaneEvent{Pane: &snapshot})
			return
		}
	}
}

// waitSummaries returns once every summary run has finished, so no run writes after Run returns. It takes no lock: a
// run's write takes mu to publish, and Run calls waitSummaries after its last apply, outside any hold of mu.
func (h *Host) waitSummaries() {
	for _, summarizer := range h.summarizers {
		summarizer.Wait()
	}
}
