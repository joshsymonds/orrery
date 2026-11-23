package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/joshsymonds/orrery/internal/holderhost"
)

// whoIsTimeout bounds each WhoIs that an agent asks of tailscaled, so that a hung tailscaled holds only the connection
// it was asked about.
const whoIsTimeout = 10 * time.Second

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

// Serve accepts connections on listener until ctx ends, then closes listener and returns once every connection has
// finished. Listener must be on the tailnet.
//
// Serve first asks WhoIs about listener's own address, and returns an error without accepting anything when that answer
// names no node, no user profile or no login, or names a tagged node. The login it reads is the owner's login, which
// Authorize admits.
//
// Each accepted connection is served on its own goroutine, and only after WhoIs names its peer and Authorize admits
// that peer. A connection that fails either check is closed before any frame is read, and the reason is logged. An
// admitted peer's one Request is answered by answer.
func (a *Agent) Serve(ctx context.Context, listener net.Listener) error {
	login, err := a.ownLogin(ctx, listener)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	var conns sync.WaitGroup
	defer func() {
		cancel()
		conns.Wait()
	}()
	stop := context.AfterFunc(ctx, func() {
		if closeErr := listener.Close(); closeErr != nil {
			a.cfg.Logger.WarnContext(ctx, "node: close the listener", "err", closeErr)
		}
	})
	defer stop()
	for {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("node: accept: %w", acceptErr)
		}
		conns.Add(1)
		go func() {
			defer conns.Done()
			a.serveConn(ctx, conn, login)
		}()
	}
}

// ownLogin asks WhoIs about listener's own address, and returns the login that its node is signed in as. It fails when
// the answer names no node, no user profile, no login, or a tagged node.
func (a *Agent) ownLogin(ctx context.Context, listener net.Listener) (string, error) {
	addr := listener.Addr().String()
	wctx, cancel := context.WithTimeout(ctx, whoIsTimeout)
	defer cancel()
	resp, err := a.cfg.WhoIs.WhoIs(wctx, addr)
	if err != nil {
		return "", fmt.Errorf("node: WhoIs of the agent's own address %s: %w", addr, err)
	}
	switch {
	case resp == nil || resp.Node == nil:
		return "", fmt.Errorf("node: the agent's own address %s names no node", addr)
	case resp.UserProfile == nil:
		return "", fmt.Errorf("node: the agent's own address %s names no user profile", addr)
	case resp.Node.IsTagged():
		return "", fmt.Errorf("node: the agent's own address %s is a tagged node, not a device of its owner", addr)
	case resp.UserProfile.LoginName == "":
		return "", fmt.Errorf("node: the agent's own address %s names no login", addr)
	}
	return resp.UserProfile.LoginName, nil
}

// serveConn admits conn's peer, and once it is admitted, answers the one Request that the peer sends.
func (a *Agent) serveConn(ctx context.Context, conn net.Conn, login string) {
	stop := context.AfterFunc(ctx, func() { a.closeConn(ctx, conn) })
	defer stop()
	defer a.closeConn(ctx, conn)
	if err := a.admit(ctx, conn, login); err != nil {
		a.cfg.Logger.WarnContext(ctx, "node: refused a connection", "remote", conn.RemoteAddr().String(), "reason", err)
		return
	}
	var req Request
	if err := json.NewDecoder(conn).Decode(&req); err != nil {
		a.cfg.Logger.WarnContext(ctx, "node: read a request", "remote", conn.RemoteAddr().String(), "err", err)
		return
	}
	if err := json.NewEncoder(conn).Encode(a.answer(ctx, req)); err != nil {
		a.cfg.Logger.WarnContext(ctx, "node: write a response", "remote", conn.RemoteAddr().String(), "err", err)
	}
}

// admit asks WhoIs, within whoIsTimeout, about conn's remote address. It returns the reason the peer is refused, or nil
// when Authorize admits the peer.
func (a *Agent) admit(ctx context.Context, conn net.Conn, login string) error {
	remote := conn.RemoteAddr().String()
	wctx, cancel := context.WithTimeout(ctx, whoIsTimeout)
	defer cancel()
	resp, err := a.cfg.WhoIs.WhoIs(wctx, remote)
	if err != nil {
		return fmt.Errorf("node: WhoIs of %s: %w", remote, err)
	}
	return Authorize(resp, a.cfg.Controller, login)
}

// answer returns the Response to req, which an admitted peer sent. The agent serves the sessions op from its holders.
// It answers a Request that names another version, and any other op, with a failure that says so.
func (a *Agent) answer(ctx context.Context, req Request) Response {
	switch {
	case req.Version != Version:
		return Response{Code: CodeFailed, Message: fmt.Sprintf("node: agent serves %s, not %s", Version, req.Version)}
	case req.Op != OpSessions:
		return Response{Code: CodeFailed, Message: fmt.Sprintf("node: agent does not serve op %s yet", req.Op)}
	}
	ids, err := a.cfg.Holders.Sessions(ctx)
	if err != nil {
		return Response{Code: CodeFailed, Message: err.Error()}
	}
	return Response{Sessions: ids}
}

// closeConn closes conn. A conn that is already closed is not an error.
func (a *Agent) closeConn(ctx context.Context, conn net.Conn) {
	if err := conn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		a.cfg.Logger.WarnContext(ctx, "node: close a connection", "remote", conn.RemoteAddr().String(), "err", err)
	}
}
