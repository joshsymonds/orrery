package enginehost

import (
	"context"
	"fmt"
)

// PaneWaker is an agent that types text into a pane of its host's tmux server when the pane is idle, and reports
// whether it did. *node.Client satisfies it.
type PaneWaker interface {
	WakePane(ctx context.Context, server, pane, text string) (bool, error)
}

// HostPaneWake returns the pane waker of a caller on host. A host of "" or controller is this machine, and local types
// into its pane. A host in agents is woken through that agent. Any other host is refused with an error that names it,
// and no pane is typed.
func HostPaneWake(
	controller string,
	local func(ctx context.Context, server, pane, text string) (bool, error),
	agents map[string]PaneWaker,
) func(ctx context.Context, host, server, pane, text string) (bool, error) {
	return func(ctx context.Context, host, server, pane, text string) (bool, error) {
		if host == "" || host == controller {
			return local(ctx, server, pane, text)
		}
		agent, ok := agents[host]
		if !ok {
			return false, fmt.Errorf(
				"wake pane %s of server %q: host %q is neither the controller nor an agent", pane, server, host,
			)
		}
		sent, err := agent.WakePane(ctx, server, pane, text)
		if err != nil {
			return sent, fmt.Errorf("wake pane %s of server %q on host %q: %w", pane, server, host, err)
		}
		return sent, nil
	}
}
