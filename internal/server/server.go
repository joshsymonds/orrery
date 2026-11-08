// Package server holds the loopback listener and request guard for the orrery HTTP surface.
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/http"
	"strings"

	"github.com/joshsymonds/orrery/internal/cli"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// HeaderLogin names the header Tailscale serve sets to the signed-in login of a request.
const HeaderLogin = "Tailscale-User-Login"

// HeaderClient names the header a write must carry to show it came from an orrery client.
const HeaderClient = "X-Orrery-Client"

// questionHashKey is the key GET /v1/panes adds to each pane, holding protocol.QuestionHash of its question.
const questionHashKey = "question_hash"

// jsonContentType is the Content-Type of every JSON response.
const jsonContentType = "application/json"

// errMalformedPaneID reports a pane ID that is not % followed by digits.
var errMalformedPaneID = errors.New("malformed pane id")

// errPaneNotFound reports a well-formed pane ID that tmux does not list.
var errPaneNotFound = errors.New("pane not found")

// screenResponse is the body of GET /v1/panes/{id}/screen.
type screenResponse struct {
	Pane   string `json:"pane"`
	Screen string `json:"screen"`
}

// Config holds the guard settings for New.
type Config struct {
	// Logins lists the Tailscale logins admitted by the guard, matched exactly.
	Logins []string
	// Tmux runs the tmux commands behind the read routes.
	Tmux *tmuxctl.Client
}

// New returns a handler that admits only the configured logins, requires a JSON content type and
// the HeaderClient header on every POST and DELETE, and never emits CORS headers. Requests that pass
// the guard reach the read routes, and every other path answers 404.
func New(cfg Config) (http.Handler, error) {
	if len(cfg.Logins) == 0 {
		return nil, errors.New("server: no allowed logins configured")
	}
	if cfg.Tmux == nil {
		return nil, errors.New("server: no tmux client configured")
	}
	allowed := make(map[string]struct{}, len(cfg.Logins))
	for _, login := range cfg.Logins {
		if login == "" {
			return nil, errors.New("server: empty allowed login")
		}
		allowed[login] = struct{}{}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/panes", servePanes(cfg.Tmux))
	mux.HandleFunc("GET /v1/panes/{id}/screen", serveScreen(cfg.Tmux))
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

// servePanes answers GET /v1/panes with the array cli.List writes for tmux, each pane carrying the
// question_hash of its question. With no running server the array is empty.
func servePanes(tmux *tmuxctl.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var list bytes.Buffer
		if err := cli.List(r.Context(), tmux, []string{"--json"}, &list); err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		var panes []map[string]json.RawMessage
		if err := json.Unmarshal(list.Bytes(), &panes); err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		for _, pane := range panes {
			var question string
			if err := json.Unmarshal(pane["question"], &question); err != nil {
				http.Error(w, "internal server error", http.StatusInternalServerError)
				return
			}
			hash, err := json.Marshal(protocol.QuestionHash(question))
			if err != nil {
				http.Error(w, "internal server error", http.StatusInternalServerError)
				return
			}
			pane[questionHashKey] = hash
		}
		writeJSON(w, panes)
	}
}

// serveScreen answers GET /v1/panes/{id}/screen with the text one pane currently shows.
func serveScreen(tmux *tmuxctl.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if err := checkPane(r.Context(), tmux, id); err != nil {
			switch {
			case errors.Is(err, errMalformedPaneID):
				http.Error(w, "malformed pane id", http.StatusBadRequest)
			case errors.Is(err, errPaneNotFound):
				http.Error(w, "pane not found", http.StatusNotFound)
			default:
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}
			return
		}
		screen, err := tmux.Run(r.Context(), "capture-pane", "-p", "-J", "-t", id)
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, screenResponse{Pane: id, Screen: screen})
	}
}

// checkPane returns errMalformedPaneID unless id is % followed by digits, and errPaneNotFound unless
// tmux lists a pane with id. Any other failure is the wrapped tmux error.
func checkPane(ctx context.Context, tmux *tmuxctl.Client, id string) error {
	if !validPaneID(id) {
		return errMalformedPaneID
	}
	panes, err := tmux.ListPanes(ctx)
	if err != nil {
		return fmt.Errorf("server: list panes: %w", err)
	}
	for _, pane := range panes {
		if pane.ID == id {
			return nil
		}
	}
	return errPaneNotFound
}

// validPaneID reports whether id is % followed by one or more decimal digits.
func validPaneID(id string) bool {
	digits, found := strings.CutPrefix(id, "%")
	if !found || digits == "" {
		return false
	}
	for _, c := range digits {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// writeJSON writes v as an application/json body with status 200, or a 500 when v cannot be encoded.
func writeJSON(w http.ResponseWriter, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", jsonContentType)
	_, _ = w.Write(body)
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
