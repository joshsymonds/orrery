// Package server holds the loopback listener and request guard for the orrery HTTP surface.
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/joshsymonds/orrery/internal/cli"
	"github.com/joshsymonds/orrery/internal/conversation"
	"github.com/joshsymonds/orrery/internal/hoststatus"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/push"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// HeaderLogin names the header Tailscale serve sets to the signed-in login of a request.
const HeaderLogin = "Tailscale-User-Login"

// HeaderClient names the header a write must carry to show it came from an orrery client.
const HeaderClient = "X-Orrery-Client"

// questionHashKey is the key GET /v1/panes adds to each pane, holding protocol.QuestionHash of its question.
const questionHashKey = "question_hash"

// Query keys of GET /v1/panes/{id}/screen, and the largest history value the route accepts, in lines.
const (
	queryANSI        = "ansi"
	queryHistory     = "history"
	screenHistoryMax = 2000
)

// queryLimit is the query key of GET /v1/panes/{id}/conversation, the number of newest items it answers with.
// conversationLimitDefault is the value when limit is absent, and conversationLimitMax the largest value it accepts.
const (
	queryLimit               = "limit"
	conversationLimitDefault = 30
	conversationLimitMax     = 100
)

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

// hostResponse is the body of a 200 answer from GET /v1/host.
type hostResponse struct {
	Host  string            `json:"host"`
	Stats []hoststatus.Stat `json:"stats"`
}

// Config holds the guard settings for New.
type Config struct {
	// Logins lists the Tailscale logins admitted by the guard, matched exactly.
	Logins []string
	// Tmux runs the tmux commands behind the read routes.
	Tmux *tmuxctl.Client
	// Push holds the push subscriptions the registration routes write.
	Push *push.Store
	// HostStatus runs the host status command behind GET /v1/host, or is nil when serve has no command.
	HostStatus *hoststatus.Runner
	// Sessions reads the store behind the Sessions and Events Connect APIs, or is nil when there is no store
	// reader, which answers unavailable.
	Sessions *store.Reader
	// EventPoll is the interval at which Events.Watch polls the store for new events, or zero for the default.
	EventPoll time.Duration
	// EventSendTimeout bounds each Events.Watch send, or is zero for the default. A send that does not complete in
	// time ends its stream, and no other.
	EventSendTimeout time.Duration
	// Shutdown ends every open Events.Watch stream when it closes, or is nil to leave streams open.
	Shutdown <-chan struct{}
}

// New returns a handler that admits only the configured logins, and never emits CORS headers. A request to
// a Connect path must carry the HeaderClient header, and a POST there one of the Connect content types. Every
// other POST and DELETE must carry a JSON content type and the HeaderClient header. Requests that pass the
// guard reach the read, write and Connect routes, and every other path answers 404.
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
	if cfg.EventPoll < 0 {
		return nil, fmt.Errorf("server: negative event poll interval %v", cfg.EventPoll)
	}
	poll := cfg.EventPoll
	if poll == 0 {
		poll = defaultEventPoll
	}
	sendTimeout := cfg.EventSendTimeout
	if sendTimeout == 0 {
		sendTimeout = defaultEventSendTimeout
	}

	goals := conversation.NewGoals()
	queues := conversation.NewQueues()
	textLocks := &paneLocks{locks: make(map[string]*sync.Mutex)}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/panes", servePanes(cfg.Tmux))
	mux.HandleFunc("POST /v1/panes", serveNewWindow(cfg.Tmux))
	mux.HandleFunc("GET /v1/panes/{id}/screen", serveScreen(cfg.Tmux))
	mux.HandleFunc("GET /v1/panes/{id}/conversation", serveConversation(cfg.Tmux, goals, queues))
	mux.HandleFunc("GET /v1/host", serveHost(cfg.HostStatus))
	mux.HandleFunc("POST /v1/panes/{id}/text", servePaneText(cfg.Tmux, textLocks))
	mux.HandleFunc("POST /v1/panes/{id}/keys", servePaneKeys(cfg.Tmux))
	mux.HandleFunc("POST /v1/panes/{id}/seen", servePaneSeen(cfg.Tmux))
	mux.HandleFunc("POST /v1/push/register", servePushRegister(cfg.Push))
	mux.HandleFunc("DELETE /v1/push/register", serveUnregister(cfg.Push))
	sessionsPath, sessions := newSessionsHandler(cfg.Sessions)
	mux.Handle(sessionsPath, sessions)
	eventsPath, events := newEventsHandler(cfg.Sessions, poll, sendTimeout, cfg.Shutdown)
	mux.Handle(eventsPath, events)
	connectPaths := []string{sessionsPath, eventsPath}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := allowed[r.Header.Get(HeaderLogin)]; !ok {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if !guardAdmits(r, connectPaths) {
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

// serveScreen answers GET /v1/panes/{id}/screen with the text one pane currently shows. ansi=1 keeps the
// color escape sequences, and history=N adds N scrollback lines above the visible screen.
func serveScreen(tmux *tmuxctl.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		flags, ok := screenFlags(r.URL.Query())
		if !ok {
			http.Error(w, "bad request query", http.StatusBadRequest)
			return
		}
		id := r.PathValue("id")
		pane, err := checkPane(r.Context(), tmux, id)
		if err != nil {
			paneError(w, err)
			return
		}
		screen, err := tmux.Run(r.Context(), append(flags, "-t", pane.ID)...)
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", textContentType)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(screen))
	}
}

// serveHost answers GET /v1/host with the host's name and the stats of the host status command, which takes no
// request input. It answers 404 when runner is nil, the serve flag being unset, and 502 when the command fails or
// prints invalid stats.
func serveHost(runner *hoststatus.Runner) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runner == nil {
			http.NotFound(w, r)
			return
		}
		stats, err := runner.Stats(r.Context())
		if err != nil {
			http.Error(w, "host status unavailable", http.StatusBadGateway)
			return
		}
		host, err := os.Hostname()
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, hostResponse{Host: host, Stats: stats})
	}
}

// screenFlags returns the capture-pane arguments for a screen query, and false when ansi or history is not a
// valid value. An absent ansi or ansi=0 is plain text and ansi=1 keeps escape sequences. An absent history adds
// no scrollback, and history=N with N from 0 to screenHistoryMax adds N lines of it.
func screenFlags(query url.Values) ([]string, bool) {
	flags := []string{"capture-pane", "-p", "-J"}
	if values, present := query[queryANSI]; present {
		switch singleValue(values) {
		case "0":
		case "1":
			flags = append(flags, "-e")
		default:
			return nil, false
		}
	}
	if values, present := query[queryHistory]; present {
		lines, ok := boundedDecimal(values, 0, screenHistoryMax)
		if !ok {
			return nil, false
		}
		flags = append(flags, "-S", "-"+strconv.Itoa(lines))
	}
	return flags, true
}

// serveConversation answers GET /v1/panes/{id}/conversation with the conversation of the pane's Claude transcript:
// its opening prompt, its newest limit items, its active goal and its waiting messages. The goal comes from goals and
// the waiting messages from queues. Each remembers how far each transcript has been read across requests. When the
// pane is done, the waiting messages are idle and the answer lists only the canceled ones. The transcript is the path
// in the pane's @orrery_transcript option, never a request value. It answers 404 unless the pane has a live Claude
// session and that file exists.
func serveConversation(tmux *tmuxctl.Client, goals *conversation.Goals, queues *conversation.Queues) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit, ok := conversationLimit(r.URL.Query())
		if !ok {
			http.Error(w, "bad request query", http.StatusBadRequest)
			return
		}
		pane, err := checkPane(r.Context(), tmux, r.PathValue("id"))
		if err != nil {
			paneError(w, err)
			return
		}
		if pane.State == "" {
			http.Error(w, "no live session", http.StatusNotFound)
			return
		}
		path, ok, err := tmux.PaneOption(r.Context(), pane.ID, protocol.OptionTranscript)
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		if !ok || path == "" {
			http.Error(w, "no transcript", http.StatusNotFound)
			return
		}
		conv, err := conversation.Read(path, limit)
		if err == nil {
			conv.Goal, err = goals.Goal(path)
		}
		if err == nil {
			conv.Waiting, err = queues.Waiting(path, pane.State == protocol.StateDone)
		}
		switch {
		case errors.Is(err, fs.ErrNotExist):
			http.Error(w, "transcript not found", http.StatusNotFound)
			return
		case err != nil:
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, conv)
	}
}

// conversationLimit returns the limit query value, or conversationLimitDefault when limit is absent. It returns
// false unless limit holds exactly one decimal integer from 1 to conversationLimitMax.
func conversationLimit(query url.Values) (int, bool) {
	values, present := query[queryLimit]
	if !present {
		return conversationLimitDefault, true
	}
	return boundedDecimal(values, 1, conversationLimitMax)
}

// singleValue returns the one value in values, or "" when there are none or several.
func singleValue(values []string) string {
	if len(values) != 1 {
		return ""
	}
	return values[0]
}

// boundedDecimal returns the integer in values, and false unless values holds exactly one string of decimal
// digits from low to high inclusive.
func boundedDecimal(values []string, low, high int) (int, bool) {
	value := singleValue(values)
	if value == "" || strings.Trim(value, "0123456789") != "" {
		return 0, false
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < low || n > high {
		return 0, false
	}
	return n, true
}

// paneLocks holds one mutex per pane ID, so that text sends to one pane run one after the other.
type paneLocks struct {
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

// lock returns the mutex for pane id, creating it on first use.
func (p *paneLocks) lock(id string) *sync.Mutex {
	p.mu.Lock()
	defer p.mu.Unlock()
	mu, ok := p.locks[id]
	if !ok {
		mu = &sync.Mutex{}
		p.locks[id] = mu
	}
	return mu
}

// servePaneText answers POST /v1/panes/{id}/text by sending the body's text as `orrery send --text` does: typed
// into a Claude pane showing its input box, else pasted, then Enter.
// The send holds the pane's lock, so two texts to one pane never interleave.
func servePaneText(tmux *tmuxctl.Client, locks *paneLocks) http.HandlerFunc {
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
		mu := locks.lock(id)
		mu.Lock()
		err := cli.Send(r.Context(), tmux, []string{flagPane, id, "--text", text}, io.Discard)
		mu.Unlock()
		if err != nil {
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

// guardAdmits reports whether the guard passes r past the login check. A request to a path under one of
// connectPaths needs the client header, and a POST there one of the Connect content types. Any other POST or
// DELETE needs a JSON content type and the client header. Every other request passes.
func guardAdmits(r *http.Request, connectPaths []string) bool {
	for _, connectPath := range connectPaths {
		if strings.HasPrefix(r.URL.Path, connectPath) {
			return r.Header.Get(HeaderClient) != "" && (r.Method != http.MethodPost || isConnectContentType(r))
		}
	}
	return !isWrite(r.Method) || isJSONWrite(r)
}

// isConnectContentType reports whether r carries one of the four content types a Connect POST may send.
func isConnectContentType(r *http.Request) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		return false
	}
	switch mediaType {
	case "application/proto", jsonContentType, "application/connect+proto", "application/connect+json":
		return true
	default:
		return false
	}
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
