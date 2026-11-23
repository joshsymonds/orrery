package node

import (
	"context"
	"errors"
	"log/slog"
	"net"

	"github.com/joshsymonds/orrery/internal/holderhost"
)

// AgentConfig configures an Agent: the controller it admits, the WhoIs that names each peer, the holders of this machine
// that it serves, and its logger.
type AgentConfig struct {
	// Controller is the controller's node name on the tailnet. Authorize admits only that peer.
	Controller string
	// WhoIs names each connection's peer from its remote address. An agent has one, since it serves no peer it cannot name.
	WhoIs WhoIs
	// Holders is this machine's holders, which the ops act on.
	Holders holderhost.Host
	// Logger receives the agent's diagnostics. Nil discards them.
	Logger *slog.Logger
}

// Agent serves node.v1 for this machine's holders, to the one controller that its config admits.
type Agent struct {
	cfg AgentConfig
}

// NewAgent returns the Agent that cfg describes. It fails when cfg names no controller, or has no WhoIs or no Holders,
// because an agent that cannot name its peer would serve any peer that connects.
func NewAgent(cfg AgentConfig) (*Agent, error) {
	switch {
	case cfg.Controller == "":
		return nil, errors.New("node: agent names no controller")
	case cfg.WhoIs == nil:
		return nil, errors.New("node: agent has no WhoIs")
	case cfg.Holders == nil:
		return nil, errors.New("node: agent has no holders")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	return &Agent{cfg: cfg}, nil
}

// Serve accepts connections on listener until ctx ends, and serves the op each connection's Request names. Listener must
// be on the tailnet. A connection is served only after WhoIs names its peer and Authorize admits that peer. A connection
// that fails either check is closed before any op runs.
//
// Serve is not implemented yet and always returns an error.
func (*Agent) Serve(_ context.Context, _ net.Listener) error {
	return notImplemented("Agent.Serve")
}
