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

	"connectrpc.com/connect"

	"github.com/joshsymonds/orrery/internal/cli"
	"github.com/joshsymonds/orrery/internal/conversation"
	enginev1connect "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/hoststatus"
	"github.com/joshsymonds/orrery/internal/open"
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

// newestReplyKey is the key GET /v1/panes adds to each pane, holding the unix seconds of the newest reply or
// question in its transcript, or 0 when it has none.
const newestReplyKey = "newest_reply"

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
	fieldReadUpTo       = "read_up_to"
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
	// Engine forwards the Control API to orreryd's engine socket, or is nil, which answers unavailable.
	Engine enginev1connect.EngineClient
	// ControlTimeout bounds each forward to Engine, or is zero for the default.
	ControlTimeout time.Duration
	// Home is the home directory a Control start must name a directory under, after symlinks resolve. An empty,
	// relative or root Home, or one that does not exist, refuses every start.
	Home string
	// Picker resolves the open configuration each POST /v1/picker/ask uses, or is nil, which answers 404.
	Picker func() (open.Config, error)
	// Now returns the current time, which dates the expiry of each picker token, or is nil for time.Now.
	Now func() time.Time
}

// New returns a handler that admits only the configured logins, and never emits CORS headers. A request to
// a Connect path must carry the HeaderClient header, and a POST there one of the Connect content types. Every
// other POST and DELETE must carry a JSON content type and the HeaderClient header. Requests that pass the
// guard reach the read, write and Connect routes, and every other path answers 404.
func New(cfg Config) (http.Handler, error) {
	allowed, err := checkConfig(cfg)
	if err != nil {
		return nil, err
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
	newest := conversation.NewNewest()
	queues := conversation.NewQueues()
	textLocks := &paneLocks{locks: make(map[string]*sync.Mutex)}
	seenLocks := &paneLocks{locks: make(map[string]*sync.Mutex)}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/panes", servePanes(cfg.Tmux, newest))
	mux.HandleFunc("POST /v1/panes", serveNewWindow(cfg.Tmux))
	mux.HandleFunc("GET /v1/panes/{id}/screen", serveScreen(cfg.Tmux))
	mux.HandleFunc("GET /v1/panes/{id}/conversation", serveConversation(cfg.Tmux, goals, queues))
	mux.HandleFunc("GET /v1/host", serveHost(cfg.HostStatus))
	mux.HandleFunc("POST /v1/panes/{id}/text", servePaneText(cfg.Tmux, textLocks))
	mux.HandleFunc("POST /v1/panes/{id}/keys", servePaneKeys(cfg.Tmux))
	mux.HandleFunc("POST /v1/panes/{id}/seen", servePaneSeen(cfg.Tmux, seenLocks))
	mux.HandleFunc("POST /v1/push/register", servePushRegister(cfg.Push))
	mux.HandleFunc("DELETE /v1/push/register", serveUnregister(cfg.Push))
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	tokens := &pickerTokens{entries: make(map[string]pickerEntry)}
	mux.HandleFunc("POST /v1/picker/ask", servePickerAsk(cfg.Picker, tokens, cfg.Tmux, now))
	mux.HandleFunc("POST /v1/picker/run", servePickerRun(tokens, cfg.Tmux, now))
	sessionsPath, sessions := newSessionsHandler(cfg.Sessions)
	mux.Handle(sessionsPath, sessions)
	eventsPath, events := newEventsHandler(cfg.Sessions, poll, sendTimeout, cfg.Shutdown)
	mux.Handle(eventsPath, events)
	controlPath, control := newControlHandler(cfg.Engine, cfg.ControlTimeout, cfg.Home)
	mux.Handle(controlPath, control)
	panesPath, panesService := newPanesHandler(cfg.Tmux, newest, goals, queues, textLocks, seenLocks)
	mux.Handle(panesPath, panesService)
	hostPath, hostService := newHostHandler(cfg.HostStatus)
	mux.Handle(hostPath, hostService)
	pushPath, pushService := newPushHandler(cfg.Push)
	mux.Handle(pushPath, pushService)
	pickerPath, pickerService := newPickerHandler(cfg.Picker, tokens, cfg.Tmux, now)
	mux.Handle(pickerPath, pickerService)
	connectPaths := []string{sessionsPath, eventsPath, controlPath, panesPath, hostPath, pushPath, pickerPath}
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

// checkConfig refuses a Config New cannot serve, and returns its allowed logins as a set.
func checkConfig(cfg Config) (map[string]struct{}, error) {
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
	if cfg.ControlTimeout < 0 {
		return nil, fmt.Errorf("server: negative control timeout %v", cfg.ControlTimeout)
	}
	return allowed, nil
}

// servePanes answers GET /v1/panes with the panes listPanes gives, as JSON.
func servePanes(tmux *tmuxctl.Client, newest *conversation.Newest) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		panes, err := listPanes(r.Context(), tmux, newest)
		if err != nil {
			writeRouteError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, panes)
	}
}

// listPanes returns the array GET /v1/panes answers: the array cli.List writes for tmux, each pane carrying the
// question_hash of its question and the newest_reply of its transcript, as paneNewestReply gives it. newest
// keeps each transcript's newest time across requests. With no running server the array is empty. Any failure is
// a routeError with status 500.
func listPanes(
	ctx context.Context,
	tmux *tmuxctl.Client,
	newest *conversation.Newest,
) ([]map[string]json.RawMessage, error) {
	var list bytes.Buffer
	if err := cli.List(ctx, tmux, []string{"--json"}, &list); err != nil {
		return nil, internalError()
	}
	var panes []map[string]json.RawMessage
	if err := json.Unmarshal(list.Bytes(), &panes); err != nil {
		return nil, internalError()
	}
	for _, pane := range panes {
		if err := addPaneFields(ctx, tmux, newest, pane); err != nil {
			return nil, internalError()
		}
	}
	return panes, nil
}

// addPaneFields sets the questionHashKey and newestReplyKey fields of pane, which holds one pane of cli.List.
func addPaneFields(
	ctx context.Context,
	tmux *tmuxctl.Client,
	newest *conversation.Newest,
	pane map[string]json.RawMessage,
) error {
	var question, id, state string
	if err := json.Unmarshal(pane["question"], &question); err != nil {
		return fmt.Errorf("server: decode pane question: %w", err)
	}
	if err := json.Unmarshal(pane["id"], &id); err != nil {
		return fmt.Errorf("server: decode pane id: %w", err)
	}
	if err := json.Unmarshal(pane["state"], &state); err != nil {
		return fmt.Errorf("server: decode pane state: %w", err)
	}
	hash, err := json.Marshal(protocol.QuestionHash(question))
	if err != nil {
		return fmt.Errorf("server: encode question hash: %w", err)
	}
	at, err := paneNewestReply(ctx, tmux, newest, id, state)
	if err != nil {
		return err
	}
	newestJSON, err := json.Marshal(at)
	if err != nil {
		return fmt.Errorf("server: encode newest reply: %w", err)
	}
	pane[questionHashKey] = hash
	pane[newestReplyKey] = newestJSON
	return nil
}

// paneNewestReply returns the unix seconds of the newest reply or question in the transcript that the pane
// id names in its @orrery_transcript option. It returns 0 when the pane has no state, no transcript option, or
// a transcript file that does not exist. Any other read error is returned.
func paneNewestReply(
	ctx context.Context,
	tmux *tmuxctl.Client,
	newest *conversation.Newest,
	id, state string,
) (int64, error) {
	if state == "" {
		return 0, nil
	}
	path, ok, err := tmux.PaneOption(ctx, id, protocol.OptionTranscript)
	if err != nil {
		return 0, fmt.Errorf("server: pane %s transcript option: %w", id, err)
	}
	if !ok || path == "" {
		return 0, nil
	}
	at, err := newest.Time(path)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("server: newest reply of pane %s: %w", id, err)
	}
	return at, nil
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

// serveConversation answers GET /v1/panes/{id}/conversation with the conversation conversationAnswer builds for the
// pane. The limit query is checked first, and a failure answers its routeError as the route's plain-text body.
func serveConversation(tmux *tmuxctl.Client, goals *conversation.Goals, queues *conversation.Queues) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit, ok := conversationLimit(r.URL.Query())
		if !ok {
			http.Error(w, "bad request query", http.StatusBadRequest)
			return
		}
		conv, err := conversationAnswer(r.Context(), tmux, goals, queues, r.PathValue("id"), limit)
		if err != nil {
			writeRouteError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, conv)
	}
}

// conversationAnswer returns the conversation of the pane with the ID id: its opening prompt, its newest limit items,
// its active goal and its waiting messages. The goal comes from goals and the waiting messages from queues. Each
// remembers how far each transcript has been read across requests. When the pane is done, the waiting messages are
// idle and the answer lists only the canceled ones. The transcript is the path in the pane's @orrery_transcript
// option, never a request value. It fails with a routeError: 404 unless the pane has a live Claude session and that
// file exists, 400 or 404 for a malformed or absent pane, and 500 otherwise.
func conversationAnswer(
	ctx context.Context,
	tmux *tmuxctl.Client,
	goals *conversation.Goals,
	queues *conversation.Queues,
	id string,
	limit int,
) (conversation.Conversation, error) {
	pane, err := checkPane(ctx, tmux, id)
	if err != nil {
		return conversation.Conversation{}, paneRouteError(err)
	}
	if pane.State == "" {
		return conversation.Conversation{}, routeError{status: http.StatusNotFound, message: "no live session"}
	}
	path, ok, err := tmux.PaneOption(ctx, pane.ID, protocol.OptionTranscript)
	if err != nil {
		return conversation.Conversation{}, internalError()
	}
	if !ok || path == "" {
		return conversation.Conversation{}, routeError{status: http.StatusNotFound, message: "no transcript"}
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
		return conversation.Conversation{}, routeError{status: http.StatusNotFound, message: "transcript not found"}
	case err != nil:
		return conversation.Conversation{}, internalError()
	}
	return conv, nil
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

// servePaneText answers POST /v1/panes/{id}/text: the pane is checked before the body, then the body's text is sent
// by sendText.
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
		if err := sendText(r.Context(), tmux, locks, id, text); err != nil {
			writeRouteError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// sendText sends text to pane id as `orrery send --text` does: typed into a Claude pane showing its input box, else
// pasted, then Enter. The send holds the pane's lock, so two texts to one pane never interleave. A tmux failure is
// internalError. The caller has checked that the pane exists.
func sendText(ctx context.Context, tmux *tmuxctl.Client, locks *paneLocks, id, text string) error {
	mu := locks.lock(id)
	mu.Lock()
	err := cli.Send(ctx, tmux, []string{flagPane, id, "--text", text}, io.Discard)
	mu.Unlock()
	if err != nil {
		return internalError()
	}
	return nil
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
		var expect *string
		if raw, guarded := fields[fieldExpectQuestion]; guarded {
			hash, isString := jsonString(raw)
			if !isString {
				http.Error(w, "bad request body", http.StatusBadRequest)
				return
			}
			expect = &hash
		}
		if err := sendKeys(r.Context(), tmux, id, keys, expect); err != nil {
			writeRouteError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// sendKeys sends keys to pane id as POST /v1/panes/{id}/keys does. A non-nil expectQuestion makes the send conditional
// on the pane's question: when that question does not match, nothing is sent and the answer is 409. An empty key list
// is 400. A tmux failure is internalError. The caller has checked that the pane exists.
func sendKeys(ctx context.Context, tmux *tmuxctl.Client, id string, keys []string, expectQuestion *string) error {
	if len(keys) == 0 {
		return routeError{status: http.StatusBadRequest, message: "bad request body"}
	}
	args := []string{flagPane, id}
	if expectQuestion != nil {
		args = append(args, "--expect-question", *expectQuestion)
	}
	// The -- ends flag parsing, so a key spelled like a flag is typed into the pane as a key.
	args = append(args, "--keys", "--")
	args = append(args, keys...)
	switch err := cli.Send(ctx, tmux, args, io.Discard); {
	case errors.Is(err, cli.ErrQuestionRefused):
		return routeError{status: http.StatusConflict, message: "question refused"}
	case err != nil:
		return internalError()
	}
	return nil
}

// servePaneSeen answers POST /v1/panes/{id}/seen by setting the pane's seen time. The body is optional: with a
// read_up_to field the time is that one, and otherwise it is now. See markSeen for how the stored time moves.
// The pane's lock covers the write, so two seen posts to one pane never store an earlier time last.
func servePaneSeen(tmux *tmuxctl.Client, locks *paneLocks) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if _, err := checkPane(r.Context(), tmux, id); err != nil {
			paneError(w, err)
			return
		}
		fields, ok := seenFields(r)
		if !ok {
			http.Error(w, "bad request body", http.StatusBadRequest)
			return
		}
		raw, reported := fields[fieldReadUpTo]
		var readUpTo int64
		if reported {
			if readUpTo, ok = unixSeconds(raw); !ok {
				http.Error(w, "bad request body", http.StatusBadRequest)
				return
			}
		}
		mu := locks.lock(id)
		mu.Lock()
		err := markSeen(r.Context(), tmux, id, readUpTo, reported)
		mu.Unlock()
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// markSeen sets pane id's seen time. Without a reported time it is now, as `orrery seen` sets it. With one, it is
// the later of the stored time and the reported one, so the stored time never moves back. An unset stored time
// counts as 0. The caller holds the pane's lock.
func markSeen(ctx context.Context, tmux *tmuxctl.Client, id string, reported int64, hasReported bool) error {
	if !hasReported {
		if err := cli.Seen(ctx, tmux, []string{flagPane, id}, io.Discard); err != nil {
			return fmt.Errorf("server: seen: %w", err)
		}
		return nil
	}
	value, set, err := tmux.PaneOption(ctx, id, protocol.OptionSeen)
	if err != nil {
		return fmt.Errorf("server: read seen time: %w", err)
	}
	var stored int64
	if set {
		if stored, err = strconv.ParseInt(value, 10, 64); err != nil {
			return fmt.Errorf("server: seen time %q: %w", value, err)
		}
	}
	if reported <= stored {
		return nil
	}
	if err = tmux.SetPaneOption(ctx, id, protocol.OptionSeen, strconv.FormatInt(reported, 10)); err != nil {
		return fmt.Errorf("server: set seen time: %w", err)
	}
	return nil
}

// paneError answers a failed checkPane: 400 for a malformed pane ID, 404 for an absent pane, else 500.
func paneError(w http.ResponseWriter, err error) {
	writeRouteError(w, paneRouteError(err))
}

// paneRouteError returns the routeError of a failed checkPane: 400 for a malformed pane ID, 404 for an absent
// pane, else 500.
func paneRouteError(err error) routeError {
	switch {
	case errors.Is(err, errMalformedPaneID):
		return routeError{status: http.StatusBadRequest, message: "malformed pane id"}
	case errors.Is(err, errPaneNotFound):
		return routeError{status: http.StatusNotFound, message: "pane not found"}
	default:
		return internalError()
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
	return parseBody(body)
}

// seenFields returns the fields of a seen request's body. An empty body has no fields. It reports false when the
// body cannot be read or is neither empty nor a JSON object.
func seenFields(r *http.Request) (map[string]json.RawMessage, bool) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, false
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return map[string]json.RawMessage{}, true
	}
	return parseBody(body)
}

// parseBody returns the fields of body, which must be one JSON object. It reports false when body is not one,
// including null.
func parseBody(body []byte) (map[string]json.RawMessage, bool) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil || fields == nil {
		return nil, false
	}
	return fields, true
}

// unixSeconds returns the non-negative integer raw holds. It reports false when raw is null, not an integer, or
// negative.
func unixSeconds(raw json.RawMessage) (int64, bool) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return 0, false
	}
	var n int64
	if err := json.Unmarshal(raw, &n); err != nil || n < 0 {
		return 0, false
	}
	return n, true
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

// routeError is a failure of a route's shared core: the HTTP status and plain-text message its REST answer
// carries, and the message its Connect twin carries too.
type routeError struct {
	status  int
	message string
}

// Error returns the message of the failure.
func (e routeError) Error() string {
	return e.message
}

// internalError returns the routeError of a failure no caller can act on: 500 with the internal server error message.
func internalError() routeError {
	return routeError{status: http.StatusInternalServerError, message: "internal server error"}
}

// writeRouteError answers the REST route with err's status and message, as http.Error writes them. An error that
// is not a routeError is answered as an internal failure.
func writeRouteError(w http.ResponseWriter, err error) {
	var route routeError
	if !errors.As(err, &route) {
		route = internalError()
	}
	http.Error(w, route.message, route.status)
}

// connectError returns the Connect error of err, carrying err's message. The code follows the routeError's status
// by the Phone API table of docs/contract.md: 400 and 422 are invalid_argument, 404 is not_found, 409 is aborted,
// 410 is failed_precondition, 500 is internal, and 502 is unavailable. The status alone decides the code, so every
// 409 is aborted. An error that is not a routeError is internal.
func connectError(err error) *connect.Error {
	var route routeError
	if !errors.As(err, &route) {
		route = internalError()
	}
	var code connect.Code
	switch route.status {
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		code = connect.CodeInvalidArgument
	case http.StatusNotFound:
		code = connect.CodeNotFound
	case http.StatusConflict:
		code = connect.CodeAborted
	case http.StatusGone:
		code = connect.CodeFailedPrecondition
	case http.StatusBadGateway:
		code = connect.CodeUnavailable
	default:
		code = connect.CodeInternal
	}
	return connect.NewError(code, errors.New(route.message))
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
