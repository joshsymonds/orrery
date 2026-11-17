package cli

import (
	"context"
	"fmt"

	"github.com/joshsymonds/orrery/internal/rpcretry"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// WakePane types text into pane on the tmux server that server names, then presses Enter, when the pane is done
// and its screen shows the input box. Otherwise it sends no key and returns false. server is the store's server
// column: "-L <name>", "-S <path>", or "" for the default server. A malformed column is an error that names pane,
// and nothing is sent. Its signature is enginehost's WakePane, so orreryd passes it straight in.
func WakePane(ctx context.Context, server, pane, text string) (bool, error) {
	return wakePaneWithClock(ctx, rpcretry.SystemClock{}, server, pane, text)
}

// wakePaneWithClock is WakePane with the clock that times the typed send's pauses.
func wakePaneWithClock(ctx context.Context, clock rpcretry.Clock, server, pane, text string) (bool, error) {
	srv, err := tmuxctl.ServerOfColumn(server)
	if err != nil {
		return false, fmt.Errorf("wake pane %s: %w", pane, err)
	}
	client, err := tmuxctl.New(srv)
	if err != nil {
		return false, fmt.Errorf("wake pane %s: %w", pane, err)
	}
	return sendIdleWithClock(ctx, clock, client, pane, text)
}
