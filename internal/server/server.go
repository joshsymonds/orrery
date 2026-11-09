// Package server holds the loopback listener and request guard for the orrery HTTP surface.
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"strings"

	"github.com/joshsymonds/orrery/internal/cli"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/push"
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

// textContentType is the Content-Type of the plain-text screen response.
const textContentType = "text/plain; charset=utf-8"

// Keys of the JSON object a write route reads its body from.
const (
	fieldText           = "text"
	fieldKeys           = "keys"
	fieldExpectQuestion = "expect_question"
	fieldPane           = "pane"
	fieldKind           = "kind"
	fieldEndpoint       = "endpoint"
	fieldP256dh         = "p256dh"
	fieldAuth           = "auth"
)

// Kinds of window POST /v1/panes opens: kindClaude runs claude in it, kindShell runs the default shell.
const (
	kindClaude = "claude"
	kindShell  = "shell"
)

// claudeCommand is the command a kindClaude window runs.
const claudeCommand = "claude"

// flagPane is the cli.Send and cli.Seen flag that names the target pane.
const flagPane = "--pane"

// errMalformedPaneID reports a pane ID that is not % followed by digits.
var errMalformedPaneID = errors.New("malformed pane id")

// errPaneNotFound reports a well-formed pane ID that tmux does not list.
var errPaneNotFound = errors.New("pane not found")

// newPaneResponse is the body of a 201 answer from POST /v1/panes.
type newPaneResponse struct {
	Pane string `json:"pane"`
}

// Config holds the guard settings for New.
type Config struct {
	// Logins lists the Tailscale logins admitted by the guard, matched exactly.
	Logins []string
	// Tmux runs the tmux commands behind the read routes.
	Tmux *tmuxctl.Client
	// Push holds the push subscriptions the registration routes write.
	Push *push.Store
}

// New returns a handler that admits only the configured logins, requires a JSON content type and
// the HeaderClient header on every POST and DELETE, and never emits CORS headers. Requests that pass
// the guard reach the read and write routes, and every other path answers 404.
func New(cfg Config) (http.Handler, error) {
	if len(cfg.Logins) == 0 {
		return nil, errors.New("server: no allowed logins configured")
	}
	if cfg.Tmux == nil {
		return nil, errors.New("server: no tmux client configured")
	}
	if cfg.Push == nil {
		return nil, errors.New("server: no push store configured")
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
	mux.HandleFunc("POST /v1/panes", serveNewWindow(cfg.Tmux))
	mux.HandleFunc("GET /v1/panes/{id}/screen", serveScreen(cfg.Tmux))
	mux.HandleFunc("POST /v1/panes/{id}/text", servePaneText(cfg.Tmux))
	mux.HandleFunc("POST /v1/panes/{id}/keys", servePaneKeys(cfg.Tmux))
	mux.HandleFunc("POST /v1/panes/{id}/seen", servePaneSeen(cfg.Tmux))
	mux.HandleFunc("POST /v1/push/register", servePushRegister(cfg.Push))
	mux.HandleFunc("DELETE /v1/push/register", serveUnregister(cfg.Push))
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
		writeJSON(w, http.StatusOK, panes)
	}
}

// serveNewWindow answers POST /v1/panes {pane, kind} by opening a window in the pane's session, in the
// pane's directory, running claude for kind "claude" or the default shell for kind "shell". It answers 201
// with the new pane's ID.
func serveNewWindow(tmux *tmuxctl.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		fields, ok := decodeBody(r)
		if !ok {
			http.Error(w, "bad request body", http.StatusBadRequest)
			return
		}
		id, ok := jsonString(fields[fieldPane])
		if !ok {
			http.Error(w, "bad request body", http.StatusBadRequest)
			return
		}
		kind, ok := jsonString(fields[fieldKind])
		if !ok || (kind != kindClaude && kind != kindShell) {
			http.Error(w, "bad request body", http.StatusBadRequest)
			return
		}
		source, err := checkPane(r.Context(), tmux, id)
		if err != nil {
			paneError(w, err)
			return
		}
		// The = makes the session name match exactly, so a name that is a prefix of another does not match it.
		// tmux expands formats in -c, so each # is doubled to keep the directory literal.
		dir := strings.ReplaceAll(source.Cwd, "#", "##")
		args := []string{
			"new-window", "-d", "-P", "-F", "#{pane_id}", "-t", "=" + source.Session + ":", "-c", dir,
		}
		if kind == kindClaude {
			args = append(args, claudeCommand)
		}
		out, err := tmux.Run(r.Context(), args...)
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusCreated, newPaneResponse{Pane: strings.TrimSpace(out)})
	}
}

// serveScreen answers GET /v1/panes/{id}/screen with the text one pane currently shows.
func serveScreen(tmux *tmuxctl.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		pane, err := checkPane(r.Context(), tmux, id)
		if err != nil {
			paneError(w, err)
			return
		}
		screen, err := tmux.Run(r.Context(), "capture-pane", "-p", "-J", "-t", pane.ID)
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", textContentType)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(screen))
	}
}

// servePaneText answers POST /v1/panes/{id}/text by pasting the body's text into the pane and pressing Enter.
func servePaneText(tmux *tmuxctl.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if _, err := checkPane(r.Context(), tmux, id); err != nil {
			paneError(w, err)
			return
		}
		fields, ok := decodeBody(r)
		if !ok {
			http.Error(w, "bad request body", http.StatusBadRequest)
			return
		}
		text, ok := jsonString(fields[fieldText])
		if !ok {
			http.Error(w, "bad request body", http.StatusBadRequest)
			return
		}
		if err := cli.Send(r.Context(), tmux, []string{flagPane, id, "--text", text}, io.Discard); err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// servePaneKeys answers POST /v1/panes/{id}/keys by sending the body's keys to the pane. An expect_question
// field makes the send conditional on the pane's question: 409 when that question does not match.
func servePaneKeys(tmux *tmuxctl.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if _, err := checkPane(r.Context(), tmux, id); err != nil {
			paneError(w, err)
			return
		}
		fields, ok := decodeBody(r)
		if !ok {
			http.Error(w, "bad request body", http.StatusBadRequest)
			return
		}
		keys, ok := jsonStrings(fields[fieldKeys])
		if !ok {
			http.Error(w, "bad request body", http.StatusBadRequest)
			return
		}
		args := []string{flagPane, id}
		if raw, guarded := fields[fieldExpectQuestion]; guarded {
			hash, isString := jsonString(raw)
			if !isString {
				http.Error(w, "bad request body", http.StatusBadRequest)
				return
			}
			args = append(args, "--expect-question", hash)
		}
		// The -- ends flag parsing, so a key spelled like a flag is typed into the pane as a key.
		args = append(args, "--keys", "--")
		args = append(args, keys...)
		switch err := cli.Send(r.Context(), tmux, args, io.Discard); {
		case errors.Is(err, cli.ErrQuestionRefused):
			http.Error(w, "question refused", http.StatusConflict)
			return
		case err != nil:
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// servePaneSeen answers POST /v1/panes/{id}/seen by setting the pane's seen time to now.
func servePaneSeen(tmux *tmuxctl.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if _, err := checkPane(r.Context(), tmux, id); err != nil {
			paneError(w, err)
			return
		}
		if err := cli.Seen(r.Context(), tmux, []string{flagPane, id}, io.Discard); err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// paneError answers a failed checkPane: 400 for a malformed pane ID, 404 for an absent pane, else 500.
func paneError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errMalformedPaneID):
		http.Error(w, "malformed pane id", http.StatusBadRequest)
	case errors.Is(err, errPaneNotFound):
		http.Error(w, "pane not found", http.StatusNotFound)
	default:
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}
}

// servePushRegister answers POST /v1/push/register {endpoint, p256dh, auth} by storing the subscription. It
// answers 204 when stored, 400 for a malformed body or an invalid subscription, 422 for an endpoint the
// store refuses, and 500 for any other store error. No response body echoes the endpoint.
func servePushRegister(store *push.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		fields, ok := decodeBody(r)
		if !ok {
			http.Error(w, "bad request body", http.StatusBadRequest)
			return
		}
		endpoint, ok := jsonString(fields[fieldEndpoint])
		if !ok {
			http.Error(w, "bad request body", http.StatusBadRequest)
			return
		}
		p256dh, ok := jsonString(fields[fieldP256dh])
		if !ok {
			http.Error(w, "bad request body", http.StatusBadRequest)
			return
		}
		auth, ok := jsonString(fields[fieldAuth])
		if !ok {
			http.Error(w, "bad request body", http.StatusBadRequest)
			return
		}
		err := store.Add(push.Subscription{Endpoint: endpoint, P256dh: p256dh, Auth: auth})
		switch {
		case err == nil:
			w.WriteHeader(http.StatusNoContent)
		case errors.Is(err, push.ErrInvalidSubscription):
			http.Error(w, "bad request body", http.StatusBadRequest)
		case errors.Is(err, push.ErrEndpointNotAllowed):
			http.Error(w, "endpoint not allowed", http.StatusUnprocessableEntity)
		default:
			http.Error(w, "internal server error", http.StatusInternalServerError)
		}
	}
}

// serveUnregister answers DELETE /v1/push/register {endpoint} by removing the subscription. It answers 204
// when removed, 400 for a malformed body or a missing endpoint, 404 when no subscription is stored for the
// endpoint, and 500 for any other store error. No response body echoes the endpoint.
func serveUnregister(store *push.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		fields, ok := decodeBody(r)
		if !ok {
			http.Error(w, "bad request body", http.StatusBadRequest)
			return
		}
		endpoint, ok := jsonString(fields[fieldEndpoint])
		if !ok {
			http.Error(w, "bad request body", http.StatusBadRequest)
			return
		}
		err := store.Remove(endpoint)
		switch {
		case err == nil:
			w.WriteHeader(http.StatusNoContent)
		case errors.Is(err, push.ErrNotRegistered):
			http.Error(w, "endpoint not registered", http.StatusNotFound)
		default:
			http.Error(w, "internal server error", http.StatusInternalServerError)
		}
	}
}

// decodeBody reads the request body as one JSON object and returns its fields. It reports false when
// the body cannot be read or is not a JSON object, including null.
func decodeBody(r *http.Request) (map[string]json.RawMessage, bool) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, false
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil || fields == nil {
		return nil, false
	}
	return fields, true
}

// jsonString returns the string raw holds. It reports false when raw is absent, null, or not a string.
func jsonString(raw json.RawMessage) (string, bool) {
	if !bytes.HasPrefix(bytes.TrimSpace(raw), []byte(`"`)) {
		return "", false
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", false
	}
	return s, true
}

// jsonStrings returns the non-empty array of strings raw holds. It reports false when raw is absent,
// null, empty, not an array, or holds a value that is not a string.
func jsonStrings(raw json.RawMessage) ([]string, bool) {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil || len(items) == 0 {
		return nil, false
	}
	strs := make([]string, 0, len(items))
	for _, item := range items {
		s, ok := jsonString(item)
		if !ok {
			return nil, false
		}
		strs = append(strs, s)
	}
	return strs, true
}

// checkPane returns the pane tmux lists with id. It returns errMalformedPaneID unless id is % followed by
// digits, and errPaneNotFound unless tmux lists a pane with id. Any other failure is the wrapped tmux error.
func checkPane(ctx context.Context, tmux *tmuxctl.Client, id string) (protocol.Pane, error) {
	if !validPaneID(id) {
		return protocol.Pane{}, errMalformedPaneID
	}
	panes, err := tmux.ListPanes(ctx)
	if err != nil {
		return protocol.Pane{}, fmt.Errorf("server: list panes: %w", err)
	}
	for _, pane := range panes {
		if pane.ID == id {
			return pane, nil
		}
	}
	return protocol.Pane{}, errPaneNotFound
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

// writeJSON writes v as an application/json body with the given status, or a 500 when v cannot be encoded.
func writeJSON(w http.ResponseWriter, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", jsonContentType)
	w.WriteHeader(status)
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
