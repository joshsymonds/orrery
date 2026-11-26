package hostrelay

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
)

// ForwardConfig configures Forward: the controller's relay address, the dialer that reaches it over the tailnet, and
// the logger.
type ForwardConfig struct {
	// Address is the controller's relay address, as host:port.
	Address string
	// Dial connects to Address over the tailnet, as net.Dialer.DialContext does.
	Dial func(ctx context.Context, network, address string) (net.Conn, error)
	// Secret is the fleet secret that Forward sets as HeaderSecret on each request it relays to Address.
	Secret string
	// Logger receives the forwarder's diagnostics. Nil discards them.
	Logger *slog.Logger
}

// flushImmediately is the ReverseProxy flush interval that flushes the response to the client after each write, so each
// message of a server stream reaches the client as it is sent.
const flushImmediately = -1

// Forward serves HTTP on ln, the listener of an agent's sessions.sock, and relays each request to the controller at
// cfg.Address, which cfg.Dial connects to. A request keeps its method, path, query, headers and body, and each response
// streams back to the client, flushed as each chunk arrives, so that the messages of a Connect server stream reach the
// client as they are sent. A request whose controller cannot be reached is answered 502. Forward shuts down once ctx
// ends, when its handlers see ctx end and return, and then returns nil.
func Forward(ctx context.Context, ln net.Listener, cfg ForwardConfig) error {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	transport := &http.Transport{DialContext: cfg.Dial}
	defer transport.CloseIdleConnections()
	target := &url.URL{Scheme: "http", Host: cfg.Address}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
		},
		Transport:     transport,
		FlushInterval: flushImmediately,
		ErrorLog:      slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}
	srv := &http.Server{
		Handler:           proxy,
		BaseContext:       func(net.Listener) context.Context { return ctx },
		ReadHeaderTimeout: headerTimeout,
	}
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ln) }()
	select {
	case err := <-served:
		// Serve returns before ctx ends only when the listener fails.
		return fmt.Errorf("hostrelay: forward: %w", err)
	case <-ctx.Done():
	}
	shutdownErr := srv.Shutdown(context.WithoutCancel(ctx))
	if err := <-served; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("hostrelay: forward: %w", err)
	}
	if shutdownErr != nil {
		return fmt.Errorf("hostrelay: forward shut down: %w", shutdownErr)
	}
	return nil
}
