// Package server holds the loopback listener and request guard for the orrery HTTP surface.
package server

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/http"
)

// HeaderLogin names the header Tailscale serve sets to the signed-in login of a request.
const HeaderLogin = "Tailscale-User-Login"

// HeaderClient names the header a write must carry to show it came from an orrery client.
const HeaderClient = "X-Orrery-Client"

// Config holds the guard settings for New.
type Config struct {
	// Logins lists the Tailscale logins admitted by the guard, matched exactly.
	Logins []string
}

// New returns a handler that admits only the configured logins, requires a JSON content type and
// the HeaderClient header on every POST and DELETE, and never emits CORS headers. Requests that pass
// the guard reach an empty mux, so every path currently answers 404.
func New(cfg Config) (http.Handler, error) {
	if len(cfg.Logins) == 0 {
		return nil, errors.New("server: no allowed logins configured")
	}
	allowed := make(map[string]struct{}, len(cfg.Logins))
	for _, login := range cfg.Logins {
		if login == "" {
			return nil, errors.New("server: empty allowed login")
		}
		allowed[login] = struct{}{}
	}

	mux := http.NewServeMux()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := allowed[r.Header.Get(HeaderLogin)]; !ok {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if isWrite(r.Method) && !isJSONWrite(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		mux.ServeHTTP(w, r)
	}), nil
}

// isWrite reports whether method is one the guard checks for a JSON content type and client header.
func isWrite(method string) bool {
	return method == http.MethodPost || method == http.MethodDelete
}

// isJSONWrite reports whether r carries an application/json body and the HeaderClient header.
func isJSONWrite(r *http.Request) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return false
	}
	return r.Header.Get(HeaderClient) != ""
}

// Listen binds a tcp listener on addr only when addr's host is a loopback IP literal. Any other
// host, including the empty host, a wildcard address, a hostname, or a non-loopback address, is
// refused before anything is bound.
func Listen(ctx context.Context, addr string) (net.Listener, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("server: listen address %q: %w", addr, err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return nil, fmt.Errorf("server: listen address %q is not a loopback IP literal", addr)
	}
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("server: listen %q: %w", addr, err)
	}
	return ln, nil
}
