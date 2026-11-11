// Package autoseen marks a pane seen when its tmux client is looking at it as the pane finishes.
package autoseen

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/joshsymonds/orrery/internal/daemon"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

const (
	// observerTimeout bounds the tmux calls made for one transition. Daemon observers must return quickly.
	observerTimeout = 2 * time.Second
	// activeWindow is how long before now a client's last input still counts as looking at its window.
	activeWindow = 30 * time.Second
	// clientFields is the number of fields in one line of list-clients output.
	clientFields = 2
)

// New returns a daemon observer that marks a pane seen when it enters done while a client of its tmux
// server shows the pane's window and had input within activeWindow. The seen time is now().Unix(). Other
// transitions change nothing. Tmux failures are logged to logger, which may be nil. After it marks a pane
// seen, it calls report with the pane and the seen time it wrote.
func New(now func() time.Time, logger *slog.Logger, report func(pane string, seen int64)) daemon.Observer {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return func(transition daemon.Transition) {
		if transition.To != protocol.StateDone {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), observerTimeout)
		defer cancel()
		if err := markSeen(ctx, transition, now, report); err != nil {
			logger.ErrorContext(ctx, "mark pane seen", "pane", transition.PaneID, "error", err)
		}
	}
}

// Viewing reports whether a client of pane's tmux server shows the pane's window and had input within
// activeWindow before now.
func Viewing(ctx context.Context, client *tmuxctl.Client, pane string, now time.Time) (bool, error) {
	window, err := client.Run(ctx, "display", "-p", "-t", pane, "#{window_id}")
	if err != nil {
		return false, fmt.Errorf("pane window: %w", err)
	}
	clients, err := client.Run(ctx, "list-clients", "-F", "#{client_activity} #{window_id}")
	if err != nil {
		return false, fmt.Errorf("list clients: %w", err)
	}
	return showsRecentInput(clients, strings.TrimSpace(window), now)
}

// markSeen sets the seen option on the transition's pane when a recently active client shows its window,
// then calls report with the pane and the seen time it wrote.
func markSeen(
	ctx context.Context,
	transition daemon.Transition,
	now func() time.Time,
	report func(pane string, seen int64),
) error {
	client, err := tmuxctl.New(transition.Server)
	if err != nil {
		return fmt.Errorf("resolve tmux server: %w", err)
	}
	current := now()
	viewing, err := Viewing(ctx, client, transition.PaneID, current)
	if err != nil || !viewing {
		return err
	}
	seen := current.Unix()
	err = client.SetPaneOption(ctx, transition.PaneID, protocol.OptionSeen, strconv.FormatInt(seen, 10))
	if err != nil {
		return fmt.Errorf("set seen: %w", err)
	}
	report(transition.PaneID, seen)
	return nil
}

// showsRecentInput reports whether a client whose current window is windowID had input within activeWindow
// before now. Each line of clients is "<client_activity> <window_id>".
func showsRecentInput(clients, windowID string, now time.Time) (bool, error) {
	for line := range strings.SplitSeq(clients, "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != clientFields {
			return false, fmt.Errorf("list clients: got %d fields in %q, want %d", len(fields), line, clientFields)
		}
		activity, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			return false, fmt.Errorf("client activity %q: %w", fields[0], err)
		}
		if fields[1] == windowID && now.Sub(time.Unix(activity, 0)) <= activeWindow {
			return true, nil
		}
	}
	return false, nil
}
