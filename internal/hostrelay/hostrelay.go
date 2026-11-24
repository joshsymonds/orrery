// Package hostrelay is the controller's relay listener that admits hosts.toml agents by WhoIs, and the forwarder an
// agent's sessions.sock relays through.
package hostrelay

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"tailscale.com/tailcfg"

	"github.com/joshsymonds/orrery/internal/hosts"
	"github.com/joshsymonds/orrery/internal/node"
	"github.com/joshsymonds/orrery/internal/protocol"
)

// whoIsTimeout bounds each WhoIs that the relay asks of tailscaled, so that a hung tailscaled holds only the connection
// it was asked about.
const whoIsTimeout = 10 * time.Second

// headerTimeout bounds how long a connection that the relay admitted may take to send its request headers, as the other
// servers of this repository bound theirs.
const headerTimeout = 5 * time.Second

// hostKey is the context key under which an admitted connection's context carries the orrery name of its host.
type hostKey struct{}

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
type Relay struct {
	listener net.Listener
	login    string
	agents   []hosts.Host
	whoIs    node.WhoIs
	handler  http.Handler
	logger   *slog.Logger
}

// Listen returns the Relay that cfg describes, listening on cfg.Address. It returns nil and no error when cfg.Hosts is
// empty, since a controller with no hosts has no agent to admit, and then it opens no socket. Otherwise it fails when
// cfg.WhoIs or cfg.Handler is nil, and when WhoIs of the relay's own address names no node, no user profile, a tagged
// node or no login. A listener that it has opened is closed before it fails.
func Listen(ctx context.Context, cfg Config) (*Relay, error) {
	if len(cfg.Hosts) == 0 {
		return nil, nil
	}
	switch {
	case cfg.WhoIs == nil:
		return nil, errors.New("hostrelay: no WhoIs to admit connections by")
	case cfg.Handler == nil:
		return nil, errors.New("hostrelay: no handler for the calls it admits")
	}
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", cfg.Address)
	if err != nil {
		return nil, fmt.Errorf("hostrelay: listen on %s: %w", cfg.Address, err)
	}
	login, err := ownLogin(ctx, cfg.WhoIs, listener.Addr().String())
	if err != nil {
		return nil, errors.Join(err, listener.Close())
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Relay{
		listener: listener,
		login:    login,
		agents:   cfg.Hosts,
		whoIs:    cfg.WhoIs,
		handler:  cfg.Handler,
		logger:   logger,
	}, nil
}

// ownLogin asks WhoIs about addr, the relay's own address, and returns the login that its node is signed in as. It fails
// when the answer names no node, no user profile, a tagged node or no login, as node.Agent does.
func ownLogin(ctx context.Context, whoIs node.WhoIs, addr string) (string, error) {
	wctx, cancel := context.WithTimeout(ctx, whoIsTimeout)
	defer cancel()
	resp, err := whoIs.WhoIs(wctx, addr)
	if err != nil {
		return "", fmt.Errorf("hostrelay: WhoIs of the relay's own address %s: %w", addr, err)
	}
	switch {
	case resp == nil || resp.Node == nil:
		return "", fmt.Errorf("hostrelay: the relay's own address %s names no node", addr)
	case resp.UserProfile == nil:
		return "", fmt.Errorf("hostrelay: the relay's own address %s names no user profile", addr)
	case resp.Node.IsTagged():
		return "", fmt.Errorf("hostrelay: the relay's own address %s is a tagged node, not a device of its owner", addr)
	case resp.UserProfile.LoginName == "":
		return "", fmt.Errorf("hostrelay: the relay's own address %s names no login", addr)
	}
	return resp.UserProfile.LoginName, nil
}

// Addr returns the address that the relay listens on.
func (r *Relay) Addr() net.Addr {
	return r.listener.Addr()
}

// Serve serves HTTP on the relay's listener until ctx ends, then shuts down and returns nil. A connection reaches the
// handler only after admit has named its peer an agent of hosts.toml, and each request on it carries the host's name in
// protocol.HeaderHost. Shutdown waits for the handlers, which see ctx end and can return.
func (r *Relay) Serve(ctx context.Context) error {
	admitted := newAdmitListener(r.listener, func(conn net.Conn) (string, error) {
		return r.admit(ctx, conn)
	}, r.logger)
	srv := &http.Server{
		Handler:           hostHeader(r.handler),
		BaseContext:       func(net.Listener) context.Context { return ctx },
		ConnContext:       connContext,
		ReadHeaderTimeout: headerTimeout,
	}
	served := make(chan error, 1)
	go func() { served <- srv.Serve(admitted) }()
	select {
	case err := <-served:
		// Serve returns before ctx ends only when the listener fails.
		return fmt.Errorf("hostrelay: serve: %w", err)
	case <-ctx.Done():
	}
	shutdownErr := srv.Shutdown(context.WithoutCancel(ctx))
	if err := <-served; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("hostrelay: serve: %w", err)
	}
	if shutdownErr != nil {
		return fmt.Errorf("hostrelay: shut down: %w", shutdownErr)
	}
	return nil
}

// admit asks WhoIs, within whoIsTimeout, about conn's remote address. It returns the orrery name of the host that
// conn's peer is, when the peer is an untagged node of the relay's login that matches an agent of hosts.toml. Otherwise
// it returns the reason the peer is refused.
func (r *Relay) admit(ctx context.Context, conn net.Conn) (string, error) {
	remote := conn.RemoteAddr().String()
	wctx, cancel := context.WithTimeout(ctx, whoIsTimeout)
	defer cancel()
	resp, err := r.whoIs.WhoIs(wctx, remote)
	if err != nil {
		return "", fmt.Errorf("hostrelay: WhoIs of %s: %w", remote, err)
	}
	switch {
	case resp == nil || resp.Node == nil:
		return "", fmt.Errorf("hostrelay: peer %s names no node", remote)
	case resp.Node.IsTagged():
		return "", fmt.Errorf("hostrelay: peer %s is a tagged node, not a device of its owner", remote)
	case resp.UserProfile == nil:
		return "", fmt.Errorf("hostrelay: peer %s names no user profile", remote)
	case resp.UserProfile.LoginName != r.login:
		return "", fmt.Errorf("hostrelay: peer %s is signed in as %s, not %s",
			remote, resp.UserProfile.LoginName, r.login)
	}
	for _, agent := range r.agents {
		if matches(resp.Node, agent.Address) {
			return agent.Name, nil
		}
	}
	return "", fmt.Errorf("hostrelay: peer %s is %s, which no hosts.toml agent names", remote, resp.Node.Name)
}

// matches reports whether peer is the node of the host at address, which is host:port. The node matches when its Name,
// with the trailing dot trimmed, or its ComputedName equals the host part of address, case-insensitively; or, when the
// host part is an IP literal, when one of the node's Addresses holds that IP.
func matches(peer *tailcfg.Node, address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil || host == "" {
		return false
	}
	if strings.EqualFold(strings.TrimSuffix(peer.Name, "."), host) || strings.EqualFold(peer.ComputedName, host) {
		return true
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	for _, prefix := range peer.Addresses {
		if prefix.Addr().Unmap() == ip.Unmap() {
			return true
		}
	}
	return false
}

// connContext records on an admitted connection's context the host that the connection was admitted as.
func connContext(ctx context.Context, conn net.Conn) context.Context {
	peer, ok := conn.(*peerConn)
	if !ok {
		return ctx
	}
	return context.WithValue(ctx, hostKey{}, peer.host)
}

// hostHeader sets protocol.HeaderHost of each request to the host that its connection was admitted as, which replaces
// any value the request sent, and then calls next. A request whose connection carries no admitted host is refused.
func hostHeader(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		host, ok := req.Context().Value(hostKey{}).(string)
		if !ok {
			http.Error(w, "hostrelay: the connection was not admitted", http.StatusForbidden)
			return
		}
		req.Header.Set(protocol.HeaderHost, host)
		next.ServeHTTP(w, req)
	})
}

// peerConn is an admitted connection, and the orrery name of the host it was admitted as.
type peerConn struct {
	net.Conn
	host string
}

// admitListener is the listener that Serve gives http.Server. Its Accept yields only the connections that admit has
// admitted, as *peerConn. Each accepted connection is admitted in a goroutine of its own, so that a slow WhoIs holds
// only its connection, and a refused connection is closed before http.Server reads any byte of it.
type admitListener struct {
	inner    net.Listener
	admit    func(net.Conn) (string, error)
	logger   *slog.Logger
	admitted chan net.Conn
	failed   chan error
	closed   chan struct{}
	once     sync.Once
}

// newAdmitListener returns an admitListener over inner, which admit admits each connection by, and starts taking
// connections from inner.
func newAdmitListener(inner net.Listener, admit func(net.Conn) (string, error), logger *slog.Logger) *admitListener {
	listener := &admitListener{
		inner:    inner,
		admit:    admit,
		logger:   logger,
		admitted: make(chan net.Conn),
		failed:   make(chan error),
		closed:   make(chan struct{}),
	}
	go listener.acceptLoop()
	return listener
}

// acceptLoop takes each connection from the inner listener, and admits it in a goroutine of its own. An error of the
// inner listener is handed to Accept, and the loop ends once the listener is closed.
func (l *admitListener) acceptLoop() {
	for {
		conn, err := l.inner.Accept()
		if err != nil {
			select {
			case l.failed <- err:
			case <-l.closed:
				return
			}
			continue
		}
		go l.admitConn(conn)
	}
}

// admitConn admits conn, and hands it to Accept. It closes conn instead when the peer is refused or the listener is
// closed first.
func (l *admitListener) admitConn(conn net.Conn) {
	host, err := l.admit(conn)
	if err != nil {
		l.logger.Warn("hostrelay: refused a connection", "remote", conn.RemoteAddr().String(), "reason", err)
		l.closeConn(conn)
		return
	}
	select {
	case l.admitted <- &peerConn{Conn: conn, host: host}:
	case <-l.closed:
		l.closeConn(conn)
	}
}

// closeConn closes conn, and logs a failure to close it.
func (l *admitListener) closeConn(conn net.Conn) {
	if err := conn.Close(); err != nil {
		l.logger.Warn("hostrelay: close a connection", "remote", conn.RemoteAddr().String(), "err", err)
	}
}

// Accept returns the next admitted connection. It fails with the inner listener's error, or with net.ErrClosed once the
// listener is closed.
func (l *admitListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.admitted:
		return conn, nil
	case err := <-l.failed:
		return nil, err
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

// Close closes the inner listener, and makes Accept fail with net.ErrClosed.
func (l *admitListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	if err := l.inner.Close(); err != nil {
		return fmt.Errorf("hostrelay: close the listener: %w", err)
	}
	return nil
}

// Addr returns the address of the inner listener.
func (l *admitListener) Addr() net.Addr {
	return l.inner.Addr()
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
