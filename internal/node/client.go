package node

import (
	"context"
	"log/slog"
	"net"
	"time"

	"github.com/joshsymonds/orrery/internal/engine/session"
	"github.com/joshsymonds/orrery/internal/holder"
	"github.com/joshsymonds/orrery/internal/holderhost"
)

// DefaultBound is how long a Client waits for an agent's answer to one op, when its ClientConfig sets no Bound.
const DefaultBound = 15 * time.Second

// ClientConfig configures a Client: the remote host it reaches, the dialer that reaches its agent over the tailnet, how
// long an op waits for the agent, the clock its waits run on, and its logger.
type ClientConfig struct {
	// Name is the host's name, as hosts.toml gives it.
	Name string
	// Address is the host's agent, as host:port, as hosts.toml gives it.
	Address string
	// Dial connects to the agent's address over the tailnet, as net.Dialer.DialContext does.
	Dial func(ctx context.Context, network, address string) (net.Conn, error)
	// Bound is how long an op waits for the agent's answer. Zero takes DefaultBound.
	Bound time.Duration
	// Clock times the waits of the ops. Nil takes holder.SystemClock{}.
	Clock holder.Clock
	// Logger receives the client's diagnostics. Nil discards them.
	Logger *slog.Logger
}

// Client is the holderhost.Host of one remote machine: the holders on the machine whose agent it dials. A Client never
// relaunches a session on another host.
type Client struct {
	cfg ClientConfig
}

// NewClient returns the Client that cfg describes. A zero Bound becomes DefaultBound, a nil Clock becomes
// holder.SystemClock{}, and a nil Logger discards the diagnostics.
func NewClient(cfg ClientConfig) *Client {
	if cfg.Bound == 0 {
		cfg.Bound = DefaultBound
	}
	if cfg.Clock == nil {
		cfg.Clock = holder.SystemClock{}
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	return &Client{cfg: cfg}
}

var _ holderhost.Host = (*Client)(nil)

// Launch starts the session on the remote machine. It is not implemented yet and returns a not-implemented error.
func (*Client) Launch(_ context.Context, _ holderhost.Spec) (*session.Session, error) {
	return nil, notImplemented("Client.Launch")
}

// Reattach takes over the holder of session id on the remote machine. It is not implemented yet and returns a
// not-implemented error.
func (*Client) Reattach(_ context.Context, _ string, _ int64, _ int) (*holder.Process, []string, error) {
	return nil, nil, notImplemented("Client.Reattach")
}

// Stop ends the holder of session id on the remote machine. It is not implemented yet and returns a not-implemented
// error.
func (*Client) Stop(_ context.Context, _ string) error {
	return notImplemented("Client.Stop")
}

// Kill ends the holder of session id on the remote machine without asking it. It is not implemented yet and returns a
// not-implemented error.
func (*Client) Kill(_ context.Context, _ string) error {
	return notImplemented("Client.Kill")
}

// Clear empties the holder directory of session id on the remote machine. It is not implemented yet and returns a
// not-implemented error.
func (*Client) Clear(_ context.Context, _ string) error {
	return notImplemented("Client.Clear")
}

// StopOrphan stops the child that the gone holder of session id recorded on the remote machine. It is not implemented
// yet and returns a not-implemented error.
func (*Client) StopOrphan(_ context.Context, _ string) error {
	return notImplemented("Client.StopOrphan")
}

// OpenGone returns the child of session id on the remote machine as its gone holder left it. It is not implemented yet
// and returns a not-implemented error.
func (*Client) OpenGone(_ context.Context, _ string, _ int64) (holderhost.Gone, error) {
	return nil, notImplemented("Client.OpenGone")
}

// Sessions returns the ids that have a holder directory on the remote machine. It is not implemented yet and returns a
// not-implemented error.
func (*Client) Sessions(_ context.Context) ([]string, error) {
	return nil, notImplemented("Client.Sessions")
}

// Reach reports that the remote machine is not reachable and returns a nil channel. It is a stub until Reach is
// implemented.
func (*Client) Reach() (bool, <-chan struct{}) {
	return false, nil
}
