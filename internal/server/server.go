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
	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
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

// questionHashKey is the key listPanes adds to each pane, holding protocol.QuestionHash of its question.
const questionHashKey = "question_hash"

// newestReplyKey is the key listPanes adds to each pane, holding the unix seconds of the newest reply or
// question in its transcript, or 0 when it has none.
const newestReplyKey = "newest_reply"

// Query keys screenFlags reads, and the largest history value it accepts, in lines.
const (
	queryANSI        = "ansi"
	queryHistory     = "history"
	screenHistoryMax = 2000
)

// queryLimit is the query key conversationLimit reads, the number of newest items a conversation answers with.
// conversationLimitDefault is the value when limit is absent, and conversationLimitMax the largest value it accepts.
const (
	queryLimit               = "limit"
	conversationLimitDefault = 30
	conversationLimitMax     = 100
)

// jsonContentType is the media type of a JSON request body, which a Connect call may send.
const jsonContentType = "application/json"

// badRequestMessage is the message of a 400 answer to a malformed body or an invalid value in one.
const badRequestMessage = "bad request body"

// badQueryMessage is the message of a 400 answer to a malformed query, such as a conversation limit out of range.
const badQueryMessage = "bad request query"

// flagPane is the cli.Send and cli.Seen flag that names the target pane.
const flagPane = "--pane"

// errMalformedPaneID reports a pane ID that is not % followed by digits.
var errMalformedPaneID = errors.New("malformed pane id")

// errPaneNotFound reports a well-formed pane ID that tmux does not list.
var errPaneNotFound = errors.New("pane not found")

// hostResponse is the host's name and the stats hostAnswer returns.
type hostResponse struct {
	Host  string            `json:"host"`
	Stats []hoststatus.Stat `json:"stats"`
}

// StepClient is the part of orreryd's Steps service serve uses. It has no StartStep, so serve cannot start a step.
type StepClient interface {
	StepStatus(
		context.Context,
		*connect.Request[enginev1.StepStatusRequest],
	) (*connect.Response[enginev1.StepStatusResponse], error)
	ListSteps(
		context.Context,
		*connect.Request[enginev1.ListStepsRequest],
	) (*connect.Response[enginev1.ListStepsResponse], error)
	HoldStep(
		context.Context,
		*connect.Request[enginev1.HoldStepRequest],
	) (*connect.Response[enginev1.HoldStepResponse], error)
	NudgeStep(
		context.Context,
		*connect.Request[enginev1.NudgeStepRequest],
	) (*connect.Response[enginev1.NudgeStepResponse], error)
	StopStep(
		context.Context,
		*connect.Request[enginev1.StopStepRequest],
	) (*connect.Response[enginev1.StopStepResponse], error)
}

// Config holds the guard settings for New.
type Config struct {
	// Logins lists the Tailscale logins admitted by the guard, matched exactly.
	Logins []string
	// Tmux runs the tmux commands behind the phone services.
	Tmux *tmuxctl.Client
	// Push holds the push subscriptions Push.Register and Push.Unregister write.
	Push *push.Store
	// HostStatus runs the host status command behind Host.Status, or is nil when serve has no command.
	HostStatus *hoststatus.Runner
	// Sessions reads the store behind the Sessions and Events Connect APIs, or is nil when there is no store
	// reader, which answers unavailable.
	Sessions *store.Reader
	// EventPoll is the interval at which Events.Watch polls the store for new events, or zero for the default.
	EventPoll time.Duration
	// EventSendTimeout bounds each Events.Watch, Panes.WatchConversation and Sessions.WatchConversation send, or is
	// zero for the default. A send that does not complete in time ends its stream, and no other.
	EventSendTimeout time.Duration
	// ConversationPoll is the interval at which Panes.WatchConversation and Sessions.WatchConversation poll their
	// pane or session and transcript, or zero for the default.
	ConversationPoll time.Duration
	// ConversationKeepalive is the time without a delta after which Panes.WatchConversation and
	// Sessions.WatchConversation send a keepalive, or zero for the default.
	ConversationKeepalive time.Duration
	// Shutdown ends every open Events.Watch, Panes.WatchConversation and Sessions.WatchConversation stream when it
	// closes, or is nil to leave streams open.
	Shutdown <-chan struct{}
	// Engine forwards the Control API to orreryd's engine socket, or is nil, which answers unavailable.
	Engine enginev1connect.EngineClient
	// ControlTimeout bounds each forward to Engine, or is zero for the default.
	ControlTimeout time.Duration
	// Steps is orreryd's Steps client, which Control asks before it forwards a session command, or is nil, which
	// forwards every session command as before.
	Steps StepClient
	// Home is the home directory a Control start must name a directory under, after symlinks resolve. An empty,
	// relative or root Home, or one that does not exist, refuses every start.
	Home string
	// Picker resolves the open configuration each Picker.Ask uses, or is nil, which answers not found.
	Picker func() (open.Config, error)
	// Now returns the current time, which dates the expiry of each picker token, or is nil for time.Now.
	Now func() time.Time
	// ReportSeen receives the pane and the seen time it holds after each Panes.MarkSeen succeeds, or is nil to
	// report nothing.
	ReportSeen cli.SeenReporter
}

// New returns a handler that admits only the configured logins, and never emits CORS headers. A request to
// a Connect path must carry the HeaderClient header, and a POST there one of the Connect content types. Requests
// that pass the guard reach the Connect services, and every other path answers 404.
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
	conversationPoll := cfg.ConversationPoll
	if conversationPoll == 0 {
		conversationPoll = defaultConversationPoll
	}
	conversationKeepalive := cfg.ConversationKeepalive
	if conversationKeepalive == 0 {
		conversationKeepalive = defaultConversationKeepalive
	}

	goals := conversation.NewGoals()
	newest := conversation.NewNewest()
	queues := conversation.NewQueues()
	textLocks := &paneLocks{locks: make(map[string]*sync.Mutex)}
	seenLocks := &paneLocks{locks: make(map[string]*sync.Mutex)}
	mux := http.NewServeMux()
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	tokens := &pickerTokens{entries: make(map[string]pickerEntry)}
	sessionsPath, sessions := newSessionsHandler(
		cfg.Sessions, conversationPoll, conversationKeepalive, sendTimeout, cfg.Shutdown,
	)
	mux.Handle(sessionsPath, sessions)
	eventsPath, events := newEventsHandler(cfg.Sessions, poll, sendTimeout, cfg.Shutdown)
	mux.Handle(eventsPath, events)
	controlPath, control := newControlHandler(cfg.Engine, cfg.ControlTimeout, cfg.Home, cfg.Steps)
	mux.Handle(controlPath, control)
	panesPath, panesService := newPanesHandler(
		cfg.Tmux, newest, goals, queues, textLocks, seenLocks, cfg.ReportSeen,
		conversationPoll, conversationKeepalive, sendTimeout, cfg.Shutdown,
	)
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
	if cfg.ConversationPoll < 0 {
		return nil, fmt.Errorf("server: negative conversation poll interval %v", cfg.ConversationPoll)
	}
	if cfg.ConversationKeepalive < 0 {
		return nil, fmt.Errorf("server: negative conversation keepalive interval %v", cfg.ConversationKeepalive)
	}
	if cfg.ControlTimeout < 0 {
		return nil, fmt.Errorf("server: negative control timeout %v", cfg.ControlTimeout)
	}
	return allowed, nil
}

// listPanes returns the array cli.List writes for tmux, each pane carrying the
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

// hostAnswer returns the host's name and the stats runner gives. A nil runner is a
// routeError with 404, the message http.NotFound writes. A failed command or invalid stats is 502, and a hostname
// failure is internal.
func hostAnswer(ctx context.Context, runner *hoststatus.Runner) (hostResponse, error) {
	if runner == nil {
		return hostResponse{}, routeError{status: http.StatusNotFound, message: "404 page not found"}
	}
	stats, err := runner.Stats(ctx)
	if err != nil {
		return hostResponse{}, routeError{status: http.StatusBadGateway, message: "host status unavailable"}
	}
	host, err := os.Hostname()
	if err != nil {
		return hostResponse{}, internalError()
	}
	return hostResponse{Host: host, Stats: stats}, nil
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

// sendKeys sends keys to pane id. A non-nil expectQuestion makes the send conditional
// on the pane's question: when that question does not match, nothing is sent and the answer is 409. An empty key list
// is 400. A tmux failure is internalError. The caller has checked that the pane exists.
func sendKeys(ctx context.Context, tmux *tmuxctl.Client, id string, keys []string, expectQuestion *string) error {
	if len(keys) == 0 {
		return routeError{status: http.StatusBadRequest, message: badRequestMessage}
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

// markPaneSeen sets pane id's seen time: now when readUpTo is nil, else the time it
// points to, which must be 0 or more. After the mark is written, report receives the pane and the stored time; a nil
// report, or a pane that still holds no seen time, reports nothing. The pane's lock covers the write and the report,
// so two seen posts to one pane never store an earlier time last, and their reports arrive in the order the times
// were stored. A negative time is 400, and a failed write or report is internalError, with the mark kept. The caller
// has checked that the pane exists.
func markPaneSeen(
	ctx context.Context,
	tmux *tmuxctl.Client,
	locks *paneLocks,
	report cli.SeenReporter,
	id string,
	readUpTo *int64,
) error {
	if readUpTo != nil && *readUpTo < 0 {
		return routeError{status: http.StatusBadRequest, message: badRequestMessage}
	}
	mu := locks.lock(id)
	mu.Lock()
	seen, err := markSeen(ctx, tmux, id, readUpTo)
	if err == nil && report != nil && seen > 0 {
		err = report(id, seen)
	}
	mu.Unlock()
	if err != nil {
		return internalError()
	}
	return nil
}

// markSeen sets pane id's seen time and returns the time the pane holds afterwards. Without a readUpTo it is now, as
// `orrery seen` sets it. With one, it is the later of the stored time and that one, so the stored time never moves
// back. An unset stored time counts as 0. The caller holds the pane's lock.
func markSeen(ctx context.Context, tmux *tmuxctl.Client, id string, readUpTo *int64) (int64, error) {
	if readUpTo == nil {
		// cli.Seen hands back the time it wrote; the lock keeps the pane at that time until the caller reports it.
		var written int64
		capture := func(_ string, seen int64) error {
			written = seen
			return nil
		}
		if err := cli.Seen(ctx, tmux, capture, []string{flagPane, id}, io.Discard); err != nil {
			return 0, fmt.Errorf("server: seen: %w", err)
		}
		return written, nil
	}
	value, set, err := tmux.PaneOption(ctx, id, protocol.OptionSeen)
	if err != nil {
		return 0, fmt.Errorf("server: read seen time: %w", err)
	}
	var stored int64
	if set {
		if stored, err = strconv.ParseInt(value, 10, 64); err != nil {
			return 0, fmt.Errorf("server: seen time %q: %w", value, err)
		}
	}
	if *readUpTo <= stored {
		return stored, nil
	}
	if err = tmux.SetPaneOption(ctx, id, protocol.OptionSeen, strconv.FormatInt(*readUpTo, 10)); err != nil {
		return 0, fmt.Errorf("server: set seen time: %w", err)
	}
	return *readUpTo, nil
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

// registerSubscription stores sub for Push.Register. It returns a routeError of 400 for
// an invalid subscription, 422 for an endpoint the store refuses, and internal for any other store error.
func registerSubscription(store *push.Store, sub push.Subscription) error {
	err := store.Add(sub)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, push.ErrInvalidSubscription):
		return routeError{status: http.StatusBadRequest, message: badRequestMessage}
	case errors.Is(err, push.ErrEndpointNotAllowed):
		return routeError{status: http.StatusUnprocessableEntity, message: "endpoint not allowed"}
	default:
		return internalError()
	}
}

// unregisterSubscription removes the subscription stored for endpoint for Push.Unregister. It returns a routeError of
// 404 when none is stored and internal for any other store error.
func unregisterSubscription(store *push.Store, endpoint string) error {
	err := store.Remove(endpoint)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, push.ErrNotRegistered):
		return routeError{status: http.StatusNotFound, message: "endpoint not registered"}
	default:
		return internalError()
	}
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

// routeError is a failure of a shared core: the HTTP status and plain-text message the core reports, and the
// message its Connect error carries too.
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

// guardAdmits reports whether the guard passes r past the login check. A request to a path under one of
// connectPaths needs the client header, and a POST there one of the Connect content types. Every other request
// passes, and the mux answers it 404.
func guardAdmits(r *http.Request, connectPaths []string) bool {
	for _, connectPath := range connectPaths {
		if strings.HasPrefix(r.URL.Path, connectPath) {
			return r.Header.Get(HeaderClient) != "" && (r.Method != http.MethodPost || isConnectContentType(r))
		}
	}
	return true
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
