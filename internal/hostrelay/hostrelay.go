// Package hostrelay is the controller's relay listener that admits hosts.toml agents by WhoIs, and the forwarder an
// agent's sessions.sock relays through.
package hostrelay

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"

	"github.com/joshsymonds/orrery/internal/hosts"
	"github.com/joshsymonds/orrery/internal/node"
)

// SessionHosts names the host that runs each session. SessionHost returns the orrery name of the host that runs session
// id, and false when no host runs it.
type SessionHosts interface {
	SessionHost(ctx context.Context, id string) (string, bool, error)
}

// Config configures a Relay: the address it listens on, the hosts.toml agents it admits, the WhoIs that names each
// connection's peer, the sessions it routes, the handler that serves the session-service calls it admits, and its
// logger.
type Config struct {
	// Address is the address the relay listens on, as host:port.
	Address string
	// Hosts are the agents that the relay admits, as hosts.toml gives them.
	Hosts []hosts.Host
	// WhoIs names each connection's peer from its remote address.
	WhoIs node.WhoIs
	// Sessions names the host that runs each session.
	Sessions SessionHosts
	// Handler serves the session-service calls that the relay admits.
	Handler http.Handler
	// Logger receives the relay's diagnostics. Nil discards them.
	Logger *slog.Logger
}

// Relay is the controller's relay listener. Listen returns it, and Serve runs it.
type Relay struct{}

// Listen returns the Relay that cfg describes, listening on cfg.Address. It returns nil and no error when cfg.Hosts is
// empty, since a controller with no hosts has no agent to admit.
func Listen(_ context.Context, _ Config) (*Relay, error) {
	return nil, errors.New("hostrelay: Listen is not built yet")
}

// Addr returns the address that the relay listens on.
func (*Relay) Addr() net.Addr {
	return nil
}

// Serve admits the relay's connections until ctx ends, and returns once it has stopped serving.
func (*Relay) Serve(_ context.Context) error {
	return errors.New("hostrelay: Relay.Serve is not built yet")
}

// ForwardConfig configures Forward: the controller's relay address, the dialer that reaches it over the tailnet, and
// the logger.
type ForwardConfig struct {
	// Address is the controller's relay address, as host:port.
	Address string
	// Dial connects to Address over the tailnet, as net.Dialer.DialContext does.
	Dial func(ctx context.Context, network, address string) (net.Conn, error)
	// Logger receives the forwarder's diagnostics. Nil discards them.
	Logger *slog.Logger
}

// Forward accepts connections on ln, the listener of an agent's sessions.sock, and relays each one to the controller at
// cfg.Address, until ctx ends.
func Forward(_ context.Context, _ net.Listener, _ ForwardConfig) error {
	return errors.New("hostrelay: Forward is not built yet")
}
