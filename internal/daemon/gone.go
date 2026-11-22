package daemon

import (
	"context"
	"strings"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// goneSeparator splits the fields of goneFormat. tmux prints it raw, and it cannot appear in a single-line option value
// or a command name.
const goneSeparator = "\x1f"

// goneFields is the number of fields in goneFormat.
const goneFields = 4

// goneFormat reads what the gone check needs of a pane: its id, its @orrery_state, its foreground command and its
// @orrery_command.
const goneFormat = "#{pane_id}" + goneSeparator + "#{" + protocol.OptionState + "}" + goneSeparator +
	"#{pane_current_command}" + goneSeparator + "#{" + protocol.OptionCommand + "}"

// gonePane is one pane as goneFormat reads it.
type gonePane struct {
	id, state, current, recorded string
}

// gone reports whether the pane holds a session whose Claude Code no longer runs in its foreground.
func (p gonePane) gone() bool {
	return p.state != "" && protocol.ClaudeGone(p.current, p.recorded)
}

// parseGonePane decodes one line that goneFormat printed. ok is false for a line with another number of fields.
func parseGonePane(line string) (gonePane, bool) {
	fields := strings.Split(line, goneSeparator)
	if len(fields) != goneFields {
		return gonePane{}, false
	}
	return gonePane{id: fields[0], state: fields[1], current: fields[2], recorded: fields[3]}, true
}

// endGoneSessions ends the session of each pane of server that holds an @orrery_state while Claude Code no longer runs
// in its foreground (see protocol.ClaudeGone). A Claude that an OOM kill or any SIGKILL ends leaves its session
// registry entry as it last wrote it, so the registry never unbinds its pane and the pane would read working, or
// waiting or done, for good. One list-panes reads every pane. A list that fails ends nothing: the mirror's tidy of the
// same server logs an unreachable server.
func (h *frameHandler) endGoneSessions(ctx context.Context, server tmuxctl.Server) {
	client, err := tmuxctl.New(server)
	if err != nil {
		h.logger.ErrorContext(ctx, "end gone sessions", "server", server.Column(), "error", err)
		return
	}
	out, err := client.Run(ctx, "list-panes", "-a", "-F", goneFormat)
	if err != nil {
		return
	}
	for line := range strings.SplitSeq(out, "\n") {
		pane, ok := parseGonePane(line)
		if ok && pane.gone() {
			h.endGone(ctx, client, server, pane.id)
		}
	}
}

// endGone ends the session of pane as the registry's unbinding of it would have, through the update path: under the
// mutex, the pane is read again, so a Claude that started in it since the list keeps its session, and an update that
// comes after this end applies after it. The end unsets the pane's session options as an unbound update does, the
// observers see the session end, and the mirror then stores the pane. The session registry still lists a Claude that
// was killed, with the status it last wrote, and no change to its entry follows its death, so the registry never
// unbinds such a pane itself. The end writes no context option, which a real update's working directory decides.
func (h *frameHandler) endGone(ctx context.Context, client *tmuxctl.Client, server tmuxctl.Server, pane string) {
	out, read, ok, ended := h.endGoneLocked(ctx, client, server, pane)
	if ended {
		h.syncApplied(ctx, server, pane, out, read, ok)
	}
}

// endGoneLocked reads pane again and, when its Claude is still gone, applies the unbinding of the pane under the mutex.
// ended reports whether the unbinding was applied, and out, read and ok are its result, as applyPaneHeld gives them.
func (h *frameHandler) endGoneLocked(
	ctx context.Context, client *tmuxctl.Client, server tmuxctl.Server, pane string,
) (out string, read, ok, ended bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	now, err := client.Run(ctx, "display-message", "-p", "-t", pane, goneFormat)
	if err != nil {
		return "", false, false, false
	}
	current, parsed := parseGonePane(strings.TrimSuffix(now, "\n"))
	if !parsed || !current.gone() {
		return "", false, false, false
	}
	h.logger.InfoContext(ctx, "end gone claude session", "server", server.Column(), "pane", pane,
		"state", current.state, "command", current.current, "recorded", current.recorded)
	out, read, ok = h.applyPaneHeld(ctx, client, server, PaneUpdate{PaneID: pane, Unbound: true}, nil)
	return out, read, ok, true
}
