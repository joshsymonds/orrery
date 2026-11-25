package gateway_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/gateway"
	"github.com/joshsymonds/orrery/internal/ledger"
	"github.com/joshsymonds/orrery/internal/llmroutes"
	"github.com/joshsymonds/orrery/internal/store"
)

// The identities and times a ledger test sends and records are invented. The rate card names ledgerModel, so its list
// cost is known; ledgerUnpricedModel is named by no card. ledgerStarted and ledgerCompleted are the synthetic times a
// request starts and completes at. ledgerPinHeader is the header a loopback caller pins an account with.
const (
	ledgerPinHeader      = "X-Orrery-Account"
	ledgerContextAccount = "example-account"
	ledgerPinnedAccount  = "example-pinned-account"
	ledgerPinnedGrant    = "example-pinned-grant"
	ledgerCallerAccount  = "example-caller-account"
	ledgerSession        = "example-session"
	ledgerAgentID        = "example-agent"
	ledgerAgentType      = "explorer"
	ledgerRequested      = "model-two"
	ledgerUnboundModel   = "model-one"
	ledgerInjectModel    = "model-three"
	ledgerModel          = "claude-sonnet-5-5"
	ledgerUnpricedModel  = "example-unpriced-model"
	ledgerStarted        = int64(1_800_000_000)
	ledgerCompleted      = int64(1_800_000_042)
	ledgerWriteFailure   = "example ledger unavailable"
	ledgerLongAnswer     = 17 << 20
)

// ledgerUsageAnswer is a Messages answer from ledgerModel that reports the usage the cost tests check: 1000 input
// tokens, 500 output tokens, 2000 cache reads, and 140 cache writes, 100 at the 5-minute price and 40 at the 1-hour one.
// It reports a cost of 0.25 US dollars.
const ledgerUsageAnswer = `{"id":"example-message","model":"claude-sonnet-5-5","usage":{` +
	`"input_tokens":1000,"output_tokens":500,"cache_read_input_tokens":2000,"cache_creation_input_tokens":140,` +
	`"cache_creation":{"ephemeral_5m_input_tokens":100,"ephemeral_1h_input_tokens":40},"cost":0.25}}`

// ledgerStreamAnswer is a streamed Messages answer. Its usage starts at message_start with the input counts and
// finishes at message_delta with the output count, so the usage is complete only once the stream has ended.
const ledgerStreamAnswer = "event: message_start\n" +
	`data: {"type":"message_start","message":{"model":"claude-sonnet-5-5","usage":{"input_tokens":1000,"output_tokens":1,` +
	`"cache_read_input_tokens":2000,"cache_creation_input_tokens":140,"cache_creation":{"ephemeral_5m_input_tokens":100,` +
	`"ephemeral_1h_input_tokens":40}}}}` + "\n\n" +
	"event: message_delta\n" +
	`data: {"type":"message_delta","usage":{"output_tokens":500}}` + "\n\n" +
	"event: message_stop\n" +
	`data: {"type":"message_stop"}` + "\n\n"

// ledgerClaims returns the unified claim headers an answer reports: a five-hour and a seven-day claim.
func ledgerClaims() map[string]string {
	return map[string]string{
		"anthropic-ratelimit-unified-5h-utilization": "0.42",
		"anthropic-ratelimit-unified-5h-reset":       "1800003600",
		"anthropic-ratelimit-unified-5h-status":      "allowed",
		"anthropic-ratelimit-unified-7d-utilization": "0.8",
		"anthropic-ratelimit-unified-7d-reset":       "1800600000",
		"anthropic-ratelimit-unified-7d-status":      "allowed_warning",
	}
}

// ledgerClaimTexts returns the claims ledgerClaims reports, rendered by claimText, in claim name order.
func ledgerClaimTexts() []string {
	return []string{
		"5h utilization=0.42 reset=1800003600 status=allowed",
		"7d utilization=0.8 reset=1800600000 status=allowed_warning",
	}
}

// ledgerRegistry is the route registry the ledger tests serve. Its main context is the default: it forwards through
// alpha, binds ledgerRequested to charlie, another forward seat, sends the subagent type explorer to charlie too, and
// binds ledgerInjectModel to delta, an inject seat. Its work context forwards through alpha under ledgerContextAccount.
func ledgerRegistry() llmroutes.Registry {
	return llmroutes.Registry{
		DefaultContext: "main",
		Seats: []llmroutes.Seat{
			{ID: "alpha", AuthMode: llmroutes.AuthForward, Upstream: llmroutes.AnthropicUpstream},
			{ID: "charlie", AuthMode: llmroutes.AuthForward, Upstream: llmroutes.AnthropicUpstream},
			{
				ID:       "delta",
				AuthMode: llmroutes.AuthInject,
				Upstream: "https://delta.example.test",
				Model:    "example-model",
			},
		},
		Contexts: []llmroutes.Context{
			{
				ID: "main", DefaultSeat: "alpha",
				Models: map[string]string{ledgerRequested: "charlie", ledgerInjectModel: "delta"},
				Subagents: &llmroutes.Subagents{
					DefaultSeat: "alpha",
					AgentTypes:  map[string]string{ledgerAgentType: "charlie"},
				},
			},
			{ID: "work", DefaultSeat: "alpha", Account: ledgerContextAccount},
		},
	}
}

// ledgerRoutes returns a RoutesReader that reads ledgerRegistry and the given account tokens. Every account the test
// stores is both a context's account and a pinnable one, as the store reads them.
func ledgerRoutes(tokens map[string]string) fakeRoutes {
	return fakeRoutes{snapshot: store.RoutesSnapshot{Routes: ledgerRegistry(), Tokens: tokens, AccountTokens: tokens}}
}

// ledgerClock is the Clock of a ledger test. Each Now answers the next of its times, and the last repeats, so a test
// fixes the instant of the Start and of the Finish.
type ledgerClock struct {
	times []time.Time
	calls int
}

// newLedgerClock returns a ledgerClock that answers ledgerStarted, then ledgerCompleted.
func newLedgerClock() *ledgerClock {
	return &ledgerClock{times: []time.Time{time.Unix(ledgerStarted, 0), time.Unix(ledgerCompleted, 0)}}
}

// Now answers the next of the clock's times.
func (c *ledgerClock) Now() time.Time {
	now := c.times[min(c.calls, len(c.times)-1)]
	c.calls++

	return now
}

// After is never called by a ledger test. It returns a channel that never receives.
func (*ledgerClock) After(time.Duration) <-chan time.Time {
	return make(chan time.Time)
}

// recordedFinish is one Finish a recordingLedger was given: the id it names and what it finishes with.
type recordedFinish struct {
	id     int64
	finish ledger.Finish
}

// recordingLedger is a Recorder that keeps every Start and every Finish it is given. Each write answers with startErr
// or finishErr, so a test can make the ledger's writes fail. onFinish, when set, runs as each Finish is given.
type recordingLedger struct {
	mu        sync.Mutex
	starts    []ledger.Start
	finishes  []recordedFinish
	startErr  error
	finishErr error
	onFinish  func()
}

// Start keeps start, and answers with the id of its place in the ledger, or with startErr.
func (l *recordingLedger) Start(_ context.Context, start ledger.Start) (int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.starts = append(l.starts, start)
	if l.startErr != nil {
		return 0, l.startErr
	}

	return int64(len(l.starts)), nil
}

// Finish keeps the finish of id, runs onFinish, and answers with finishErr.
func (l *recordingLedger) Finish(_ context.Context, id int64, finish ledger.Finish) error {
	l.mu.Lock()
	l.finishes = append(l.finishes, recordedFinish{id: id, finish: finish})
	onFinish := l.onFinish
	l.mu.Unlock()

	if onFinish != nil {
		onFinish()
	}

	return l.finishErr
}

// startsSeen returns the Starts the ledger has been given, in order.
func (l *recordingLedger) startsSeen() []ledger.Start {
	l.mu.Lock()
	defer l.mu.Unlock()

	return slices.Clone(l.starts)
}

// finishesSeen returns the Finishes the ledger has been given, in order.
func (l *recordingLedger) finishesSeen() []recordedFinish {
	l.mu.Lock()
	defer l.mu.Unlock()

	return slices.Clone(l.finishes)
}

// newLedgerGateway returns a loaded Gateway that forwards to upstream, reads ledgerRegistry through routes, records to
// recorder on clock and logs to logger.
func newLedgerGateway(
	t *testing.T,
	routes gateway.RoutesReader,
	upstream string,
	recorder gateway.Recorder,
	clock gateway.Clock,
	logger *slog.Logger,
) *gateway.Gateway {
	t.Helper()

	gw, err := gateway.New(gateway.Config{
		Routes:            routes,
		Ledger:            recorder,
		Clock:             clock,
		AnthropicUpstream: upstream,
		Logger:            logger,
	})
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	if err := gw.Load(t.Context()); err != nil {
		t.Fatalf("Load() = %v", err)
	}

	return gw
}

// ledgerBody returns a Messages request that names model. A non-empty userID is sent as metadata.user_id.
func ledgerBody(t *testing.T, model, userID string) string {
	t.Helper()

	body := map[string]any{"model": model, "max_tokens": 16, "messages": []any{}}
	if userID != "" {
		body["metadata"] = map[string]string{"user_id": userID}
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("encode the request body: %v", err)
	}

	return string(encoded)
}

// claudeUserID returns the metadata.user_id Claude Code sends for an invented device, account and session.
func claudeUserID(t *testing.T, account, session string) string {
	t.Helper()

	encoded, err := json.Marshal(map[string]string{
		"device_id":    "example-device",
		"account_uuid": account,
		"session_id":   session,
	})
	if err != nil {
		t.Fatalf("encode the user id: %v", err)
	}

	return string(encoded)
}

// ledgerAnswer returns a fake Anthropic handler that answers with status, the headers and body given. Its
// Content-Type is application/json unless header sets another.
func ledgerAnswer(t *testing.T, status int, header map[string]string, body string) http.HandlerFunc {
	t.Helper()

	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		for name, value := range header {
			w.Header().Set(name, value)
		}
		w.WriteHeader(status)
		if _, err := io.WriteString(w, body); err != nil {
			t.Errorf("write the fake Anthropic's answer: %v", err)
		}
	}
}

// claimText renders a claim, or a reading, as its name, utilization, reset and status, so that a test compares what
// it says without comparing pointers.
func claimText(name string, utilization *float64, reset *int64, status string) string {
	utilizationText, resetText := "none", "none"
	if utilization != nil {
		utilizationText = strconv.FormatFloat(*utilization, 'g', -1, 64)
	}
	if reset != nil {
		resetText = strconv.FormatInt(*reset, 10)
	}

	return name + " utilization=" + utilizationText + " reset=" + resetText + " status=" + status
}

// checkCost fails the test unless got is a known cost of want nano-USD.
func checkCost(t *testing.T, name string, got *int64, want int64) {
	t.Helper()

	if got == nil {
		t.Errorf("%s is unknown; want %d nano-USD", name, want)

		return
	}
	if *got != want {
		t.Errorf("%s = %d; want %d nano-USD", name, *got, want)
	}
}

// requireOneStartAndFinish returns the one Start and the one Finish the ledger was given, failing the test for any other
// count.
func requireOneStartAndFinish(t *testing.T, recorder *recordingLedger) (ledger.Start, recordedFinish) {
	t.Helper()

	starts := recorder.startsSeen()
	if len(starts) != 1 {
		t.Fatalf("the ledger was given %d Starts; want 1", len(starts))
	}
	finishes := recorder.finishesSeen()
	if len(finishes) != 1 {
		t.Fatalf("the ledger was given %d Finishes; want 1", len(finishes))
	}

	return starts[0], finishes[0]
}

func TestGatewayLedgerStartAndFinishOncePerRequest(t *testing.T) {
	t.Parallel()

	recorder := &recordingLedger{}
	var startsAtDispatch atomic.Int64
	answer := ledgerAnswer(t, http.StatusOK, nil, upstreamReply)
	fake := newFakeAnthropic(t, func(w http.ResponseWriter, r *http.Request) {
		startsAtDispatch.Store(int64(len(recorder.startsSeen())))
		answer(w, r)
	})
	gw := newLedgerGateway(t, ledgerRoutes(nil), fake.url(), recorder, newLedgerClock(), discardLogger())

	gw.ServeHTTP(
		httptest.NewRecorder(),
		newProxyRequest(t, http.MethodPost, "/v1/messages", ledgerBody(t, ledgerRequested, "")),
	)

	onlyRequest(t, fake)
	if got := startsAtDispatch.Load(); got != 1 {
		t.Errorf("the upstream was reached after %d Starts; want the Start written before dispatch", got)
	}
	start, finished := requireOneStartAndFinish(t, recorder)
	if start.StartedAt != ledgerStarted {
		t.Errorf("Start.StartedAt = %d; want the synthetic start %d", start.StartedAt, ledgerStarted)
	}
	if finished.id != 1 {
		t.Errorf("Finish named request %d; want the id the Start returned, 1", finished.id)
	}
	if finished.finish.CompletedAt != ledgerCompleted {
		t.Errorf(
			"Finish.CompletedAt = %d; want the synthetic completion %d",
			finished.finish.CompletedAt,
			ledgerCompleted,
		)
	}
}

func TestGatewayLedgerAccountFromContext(t *testing.T) {
	t.Parallel()

	recorder := &recordingLedger{}
	fake := newFakeAnthropic(t, ledgerAnswer(t, http.StatusOK, nil, upstreamReply))
	routes := ledgerRoutes(map[string]string{ledgerContextAccount: accountGrant})
	gw := newLedgerGateway(t, routes, fake.url(), recorder, newLedgerClock(), discardLogger())

	gw.ServeHTTP(httptest.NewRecorder(),
		newProxyRequest(t, http.MethodPost, "/ctx/work/v1/messages", ledgerBody(t, ledgerUnboundModel, "")))

	start, _ := requireOneStartAndFinish(t, recorder)
	if start.Account != ledgerContextAccount {
		t.Errorf("Start.Account = %q; want the context's account %q", start.Account, ledgerContextAccount)
	}
}

func TestGatewayLedgerAccountFromPinHeader(t *testing.T) {
	t.Parallel()

	recorder := &recordingLedger{}
	fake := newFakeAnthropic(t, ledgerAnswer(t, http.StatusOK, nil, upstreamReply))
	routes := ledgerRoutes(map[string]string{
		ledgerContextAccount: accountGrant,
		ledgerPinnedAccount:  ledgerPinnedGrant,
	})
	gw := newLedgerGateway(t, routes, fake.url(), recorder, newLedgerClock(), discardLogger())

	req := newProxyRequest(t, http.MethodPost, "/ctx/work/v1/messages",
		ledgerBody(t, ledgerUnboundModel, claudeUserID(t, ledgerCallerAccount, ledgerSession)))
	req.Header.Set(ledgerPinHeader, ledgerPinnedAccount)
	req.RemoteAddr = "127.0.0.1:40000"
	gw.ServeHTTP(httptest.NewRecorder(), req)

	start, _ := requireOneStartAndFinish(t, recorder)
	if start.Account != ledgerPinnedAccount {
		t.Errorf(
			"Start.Account = %q; want the pinned account %q, which beats the context's",
			start.Account,
			ledgerPinnedAccount,
		)
	}
}

func TestGatewayLedgerAccountFromCallerAccountUUID(t *testing.T) {
	t.Parallel()

	recorder := &recordingLedger{}
	fake := newFakeAnthropic(t, ledgerAnswer(t, http.StatusOK, nil, upstreamReply))
	gw := newLedgerGateway(t, ledgerRoutes(nil), fake.url(), recorder, newLedgerClock(), discardLogger())

	gw.ServeHTTP(httptest.NewRecorder(), newProxyRequest(t, http.MethodPost, "/v1/messages",
		ledgerBody(t, ledgerUnboundModel, claudeUserID(t, ledgerCallerAccount, ledgerSession))))

	start, _ := requireOneStartAndFinish(t, recorder)
	if start.Account != "" {
		t.Errorf("Start.Account = %q; want empty, as the context names no account", start.Account)
	}
	if start.CallerAccount != ledgerCallerAccount {
		t.Errorf(
			"Start.CallerAccount = %q; want the metadata.user_id account %q",
			start.CallerAccount,
			ledgerCallerAccount,
		)
	}
}

func TestGatewayLedgerSeatContextModelsAndAgentIDs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		path          string
		model         string
		headers       map[string]string
		wantSeat      string
		wantContext   string
		wantAgentID   string
		wantAgentType string
	}{
		{
			name:        "model binding",
			path:        "/v1/messages",
			model:       ledgerRequested,
			wantSeat:    "charlie",
			wantContext: "main",
		},
		{
			name:  "marked subagent",
			path:  "/v1/messages",
			model: ledgerUnboundModel,
			headers: map[string]string{
				"X-Claude-Code-Agent-Id":   ledgerAgentID,
				"X-Claude-Code-Agent-Type": ledgerAgentType,
			},
			wantSeat:      "charlie",
			wantContext:   "main",
			wantAgentID:   ledgerAgentID,
			wantAgentType: ledgerAgentType,
		},
		{
			name:        "default seat of a named context",
			path:        "/ctx/work/v1/messages",
			model:       ledgerUnboundModel,
			wantSeat:    "alpha",
			wantContext: "work",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			recorder := &recordingLedger{}
			fake := newFakeAnthropic(t, ledgerAnswer(t, http.StatusOK, nil, upstreamReply))
			gw := newLedgerGateway(t, ledgerRoutes(nil), fake.url(), recorder, newLedgerClock(), discardLogger())

			req := newProxyRequest(t, http.MethodPost, tc.path,
				ledgerBody(t, tc.model, claudeUserID(t, ledgerCallerAccount, ledgerSession)))
			for name, value := range tc.headers {
				req.Header.Set(name, value)
			}
			gw.ServeHTTP(httptest.NewRecorder(), req)

			start, _ := requireOneStartAndFinish(t, recorder)
			if start.Seat != tc.wantSeat {
				t.Errorf("Start.Seat = %q; want %q", start.Seat, tc.wantSeat)
			}
			if start.Context != tc.wantContext {
				t.Errorf("Start.Context = %q; want %q", start.Context, tc.wantContext)
			}
			if start.RequestedModel != tc.model {
				t.Errorf("Start.RequestedModel = %q; want the body's model %q", start.RequestedModel, tc.model)
			}
			if start.ClaudeSession != ledgerSession {
				t.Errorf(
					"Start.ClaudeSession = %q; want the metadata.user_id session %q",
					start.ClaudeSession,
					ledgerSession,
				)
			}
			if start.AgentID != tc.wantAgentID || start.AgentType != tc.wantAgentType {
				t.Errorf("Start agent = %q %q; want %q %q",
					start.AgentID, start.AgentType, tc.wantAgentID, tc.wantAgentType)
			}
		})
	}
}

func TestGatewayLedgerTokensByKindAndListCost(t *testing.T) {
	t.Parallel()

	recorder := &recordingLedger{}
	fake := newFakeAnthropic(t, ledgerAnswer(t, http.StatusOK, nil, ledgerUsageAnswer))
	gw := newLedgerGateway(t, ledgerRoutes(nil), fake.url(), recorder, newLedgerClock(), discardLogger())

	gw.ServeHTTP(
		httptest.NewRecorder(),
		newProxyRequest(t, http.MethodPost, "/v1/messages", ledgerBody(t, ledgerRequested, "")),
	)

	_, finished := requireOneStartAndFinish(t, recorder)
	finish := finished.finish
	if finish.ResolvedModel != ledgerModel {
		t.Errorf("Finish.ResolvedModel = %q; want the answer's model %q", finish.ResolvedModel, ledgerModel)
	}
	if finish.InputTokens != 1000 || finish.OutputTokens != 500 || finish.CacheReadTokens != 2000 {
		t.Errorf("tokens input=%d output=%d cache read=%d; want 1000, 500 and 2000",
			finish.InputTokens, finish.OutputTokens, finish.CacheReadTokens)
	}
	if finish.CacheWriteTokens != 140 {
		t.Errorf(
			"Finish.CacheWriteTokens = %d; want the 5-minute and 1-hour writes together, 140",
			finish.CacheWriteTokens,
		)
	}
	checkCost(t, "Finish.BilledCostNanoUSD", finish.BilledCostNanoUSD, 250_000_000)
	checkCost(t, "Finish.ListCostNanoUSD", finish.ListCostNanoUSD, 7_610_000)
	checkCost(t, "Finish.ListInputCostNanoUSD", finish.ListInputCostNanoUSD, 2_000_000)
	checkCost(t, "Finish.ListOutputCostNanoUSD", finish.ListOutputCostNanoUSD, 5_000_000)
	checkCost(t, "Finish.ListCacheReadCostNanoUSD", finish.ListCacheReadCostNanoUSD, 200_000)
	checkCost(t, "Finish.ListCacheWriteCostNanoUSD", finish.ListCacheWriteCostNanoUSD, 410_000)
}

func TestGatewayLedgerUnpricedModelHasNoListCost(t *testing.T) {
	t.Parallel()

	recorder := &recordingLedger{}
	answer := `{"model":"` + ledgerUnpricedModel + `","usage":{"input_tokens":1000,"output_tokens":500}}`
	fake := newFakeAnthropic(t, ledgerAnswer(t, http.StatusOK, nil, answer))
	gw := newLedgerGateway(t, ledgerRoutes(nil), fake.url(), recorder, newLedgerClock(), discardLogger())

	gw.ServeHTTP(
		httptest.NewRecorder(),
		newProxyRequest(t, http.MethodPost, "/v1/messages", ledgerBody(t, ledgerRequested, "")),
	)

	_, finished := requireOneStartAndFinish(t, recorder)
	finish := finished.finish
	if finish.InputTokens != 1000 || finish.OutputTokens != 500 {
		t.Errorf("tokens input=%d output=%d; want 1000 and 500", finish.InputTokens, finish.OutputTokens)
	}
	if finish.ListCostNanoUSD != nil || finish.ListInputCostNanoUSD != nil || finish.ListOutputCostNanoUSD != nil ||
		finish.ListCacheReadCostNanoUSD != nil || finish.ListCacheWriteCostNanoUSD != nil {
		t.Errorf("list costs for a model the card does not name = %+v; want every one unknown", finish)
	}
	checkCost(t, "Finish.BilledCostNanoUSD", finish.BilledCostNanoUSD, 0)
}

func TestGatewayLedgerClaimsPassedToFinish(t *testing.T) {
	t.Parallel()

	recorder := &recordingLedger{}
	fake := newFakeAnthropic(t, ledgerAnswer(t, http.StatusOK, ledgerClaims(), upstreamReply))
	gw := newLedgerGateway(t, ledgerRoutes(nil), fake.url(), recorder, newLedgerClock(), discardLogger())

	gw.ServeHTTP(
		httptest.NewRecorder(),
		newProxyRequest(t, http.MethodPost, "/v1/messages", ledgerBody(t, ledgerRequested, "")),
	)

	_, finished := requireOneStartAndFinish(t, recorder)
	got := make([]string, 0, len(finished.finish.Claims))
	for _, claim := range finished.finish.Claims {
		got = append(got, claimText(claim.Name, claim.Utilization, claim.Reset, claim.Status))
	}
	if !slices.Equal(got, ledgerClaimTexts()) {
		t.Errorf("Finish.Claims = %q; want %q", got, ledgerClaimTexts())
	}
}

func TestGatewayLedgerStreamFinishesAtStreamEnd(t *testing.T) {
	t.Parallel()

	recorder := &recordingLedger{}
	rec := httptest.NewRecorder()
	var bodyAtFinish string
	recorder.onFinish = func() { bodyAtFinish = rec.Body.String() }
	stream := ledgerAnswer(t, http.StatusOK, map[string]string{"Content-Type": "text/event-stream"}, ledgerStreamAnswer)
	fake := newFakeAnthropic(t, stream)
	gw := newLedgerGateway(t, ledgerRoutes(nil), fake.url(), recorder, newLedgerClock(), discardLogger())

	gw.ServeHTTP(rec, newProxyRequest(t, http.MethodPost, "/v1/messages", ledgerBody(t, ledgerRequested, "")))

	_, finished := requireOneStartAndFinish(t, recorder)
	if bodyAtFinish != ledgerStreamAnswer {
		t.Errorf("the Finish came when the caller had %q; want the whole stream, %q", bodyAtFinish, ledgerStreamAnswer)
	}
	if finished.finish.OutputTokens != 500 || finished.finish.InputTokens != 1000 {
		t.Errorf("tokens input=%d output=%d; want the stream's final 1000 and 500",
			finished.finish.InputTokens, finished.finish.OutputTokens)
	}
	if finished.finish.ResolvedModel != ledgerModel {
		t.Errorf("Finish.ResolvedModel = %q; want the stream's model %q", finished.finish.ResolvedModel, ledgerModel)
	}
}

func TestGatewayLedgerFailedUpstreamAnswerIsFinished(t *testing.T) {
	t.Parallel()

	recorder := &recordingLedger{}
	answer := `{"type":"error","error":{"type":"api_error","message":"example upstream failure"}}`
	fake := newFakeAnthropic(t, ledgerAnswer(t, http.StatusInternalServerError, nil, answer))
	gw := newLedgerGateway(t, ledgerRoutes(nil), fake.url(), recorder, newLedgerClock(), discardLogger())

	rec := httptest.NewRecorder()
	gw.ServeHTTP(rec, newProxyRequest(t, http.MethodPost, "/v1/messages", ledgerBody(t, ledgerRequested, "")))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("caller got status %d; want the upstream's 500", rec.Code)
	}
	_, finished := requireOneStartAndFinish(t, recorder)
	if finished.finish.InputTokens != 0 || finished.finish.OutputTokens != 0 {
		t.Errorf("tokens input=%d output=%d; want none, as the answer reported no usage",
			finished.finish.InputTokens, finished.finish.OutputTokens)
	}
	checkCost(t, "Finish.BilledCostNanoUSD", finished.finish.BilledCostNanoUSD, 0)
}

func TestGatewayLedgerUnreachableUpstreamIsFinished(t *testing.T) {
	t.Parallel()

	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close()

	recorder := &recordingLedger{}
	gw := newLedgerGateway(t, ledgerRoutes(nil), deadURL, recorder, newLedgerClock(), discardLogger())

	rec := httptest.NewRecorder()
	gw.ServeHTTP(rec, newProxyRequest(t, http.MethodPost, "/v1/messages", ledgerBody(t, ledgerRequested, "")))

	if rec.Code != http.StatusBadGateway {
		t.Errorf("caller got status %d; want the gateway's 502 for an upstream that is down", rec.Code)
	}
	_, finished := requireOneStartAndFinish(t, recorder)
	if finished.finish.ResolvedModel != "" {
		t.Errorf("Finish.ResolvedModel = %q; want none, as no answer came", finished.finish.ResolvedModel)
	}
}

func TestGatewayLedgerRefusedBeforeResolutionWritesNoRow(t *testing.T) {
	t.Parallel()

	recorder := &recordingLedger{}
	fake := newFakeAnthropic(t, ledgerAnswer(t, http.StatusOK, nil, upstreamReply))
	gw := newLedgerGateway(t, ledgerRoutes(nil), fake.url(), recorder, newLedgerClock(), discardLogger())

	rec := httptest.NewRecorder()
	gw.ServeHTTP(rec, newProxyRequest(t, http.MethodPost, "/ctx/missing/v1/messages",
		ledgerBody(t, ledgerUnboundModel, "")))

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d; want 404 for a context the registry does not define", rec.Code)
	}
	if starts := recorder.startsSeen(); len(starts) != 0 {
		t.Errorf("the ledger was given %d Starts; want none for a request refused before resolution", len(starts))
	}
	if finishes := recorder.finishesSeen(); len(finishes) != 0 {
		t.Errorf("the ledger was given %d Finishes; want none for a request refused before resolution", len(finishes))
	}
	requireNoUpstream(t, fake)
}

func TestGatewayLedgerRefusedAfterResolutionIsFinished(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		path        string
		model       string
		headers     map[string]string
		wantStatus  int
		wantAccount string
		wantSeat    string
		wantContext string
	}{
		{
			// httptest's default peer is not a loopback address, so the pin is refused, and the request is recorded under
			// its context's account, since the pin did not apply.
			name:        "account pin from off the machine",
			path:        "/ctx/work/v1/messages",
			model:       ledgerUnboundModel,
			headers:     map[string]string{ledgerPinHeader: ledgerPinnedAccount},
			wantStatus:  http.StatusForbidden,
			wantAccount: ledgerContextAccount,
			wantSeat:    "alpha",
			wantContext: "work",
		},
		{
			name:        "inject seat without the caller key",
			path:        "/v1/messages",
			model:       ledgerInjectModel,
			wantStatus:  http.StatusUnauthorized,
			wantSeat:    "delta",
			wantContext: "main",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			recorder := &recordingLedger{}
			fake := newFakeAnthropic(t, ledgerAnswer(t, http.StatusOK, nil, upstreamReply))
			routes := ledgerRoutes(map[string]string{ledgerPinnedAccount: ledgerPinnedGrant})
			gw := newLedgerGateway(t, routes, fake.url(), recorder, newLedgerClock(), discardLogger())

			req := newProxyRequest(t, http.MethodPost, tc.path, ledgerBody(t, tc.model, ""))
			for name, value := range tc.headers {
				req.Header.Set(name, value)
			}
			rec := httptest.NewRecorder()
			gw.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d; want %d", rec.Code, tc.wantStatus)
			}
			start, finished := requireOneStartAndFinish(t, recorder)
			if start.Account != tc.wantAccount {
				t.Errorf("Start.Account = %q; want %q", start.Account, tc.wantAccount)
			}
			if start.Seat != tc.wantSeat || start.Context != tc.wantContext {
				t.Errorf("Start seat %q context %q; want %q %q", start.Seat, start.Context, tc.wantSeat, tc.wantContext)
			}
			if finished.finish.InputTokens != 0 || finished.finish.OutputTokens != 0 {
				t.Errorf("tokens input=%d output=%d; want none, as the gateway answered without an upstream",
					finished.finish.InputTokens, finished.finish.OutputTokens)
			}
			requireNoUpstream(t, fake)
		})
	}
}

func TestGatewayLedgerFailedWritesAreLoggedAndAnswerServed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		startErr     error
		finishErr    error
		wantFinishes int
	}{
		{name: "start fails", startErr: errors.New(ledgerWriteFailure), wantFinishes: 0},
		{name: "finish fails", finishErr: errors.New(ledgerWriteFailure), wantFinishes: 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			recorder := &recordingLedger{startErr: tc.startErr, finishErr: tc.finishErr}
			fake := newFakeAnthropic(t, ledgerAnswer(t, http.StatusCreated, nil, upstreamReply))
			logs := &bytes.Buffer{}
			gw := newLedgerGateway(t, ledgerRoutes(nil), fake.url(), recorder, newLedgerClock(),
				slog.New(slog.NewTextHandler(logs, nil)))

			rec := httptest.NewRecorder()
			gw.ServeHTTP(rec, newProxyRequest(t, http.MethodPost, "/v1/messages", ledgerBody(t, ledgerRequested, "")))

			if rec.Code != http.StatusCreated || rec.Body.String() != upstreamReply {
				t.Errorf("caller got %d %q; want the upstream's 201 and body despite the failed write",
					rec.Code, rec.Body.String())
			}
			onlyRequest(t, fake)
			if finishes := recorder.finishesSeen(); len(finishes) != tc.wantFinishes {
				t.Errorf("the ledger was given %d Finishes; want %d", len(finishes), tc.wantFinishes)
			}
			for _, want := range []string{ledgerWriteFailure, "seat=charlie", "context=main"} {
				if !strings.Contains(logs.String(), want) {
					t.Errorf("the failed write was logged as %q; want it to carry %q", logs.String(), want)
				}
			}
		})
	}
}

func TestGatewayLedgerRecordsClaimsAsLimitReadings(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "ledger.db")
	l, err := ledger.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("ledger.Open() = %v", err)
	}
	t.Cleanup(func() {
		if err := l.Close(); err != nil {
			t.Errorf("close the ledger: %v", err)
		}
	})
	fake := newFakeAnthropic(t, ledgerAnswer(t, http.StatusOK, ledgerClaims(), upstreamReply))
	routes := ledgerRoutes(map[string]string{ledgerContextAccount: accountGrant})
	gw := newLedgerGateway(t, routes, fake.url(), l, newLedgerClock(), discardLogger())

	gw.ServeHTTP(httptest.NewRecorder(),
		newProxyRequest(t, http.MethodPost, "/ctx/work/v1/messages", ledgerBody(t, ledgerRequested, "")))

	readings, err := ledger.NewReader(path).Readings(t.Context())
	if err != nil {
		t.Fatalf("Readings() = %v", err)
	}
	got := make([]string, 0, len(readings))
	for _, reading := range readings {
		if reading.Account != ledgerContextAccount || reading.Observed != ledgerCompleted || reading.Dropped {
			t.Errorf("reading %+v; want the context's account, observed at completion, and not dropped", reading)
		}
		got = append(got, claimText(reading.Claim, reading.Utilization, reading.Reset, reading.Status))
	}
	if !slices.Equal(got, ledgerClaimTexts()) {
		t.Errorf("limit readings = %q; want %q", got, ledgerClaimTexts())
	}
}

func TestGatewayLedgerLongAnswerIsServedWhole(t *testing.T) {
	t.Parallel()

	long := `{"id":"example-message","padding":"` + strings.Repeat("x", ledgerLongAnswer) + `"}`
	recorder := &recordingLedger{}
	fake := newFakeAnthropic(t, ledgerAnswer(t, http.StatusOK, nil, long))
	gw := newLedgerGateway(t, ledgerRoutes(nil), fake.url(), recorder, newLedgerClock(), discardLogger())

	rec := httptest.NewRecorder()
	gw.ServeHTTP(rec, newProxyRequest(t, http.MethodPost, "/v1/messages", ledgerBody(t, ledgerRequested, "")))

	if rec.Body.String() != long {
		t.Errorf("caller got %d bytes; want the whole %d-byte answer", rec.Body.Len(), len(long))
	}
	requireOneStartAndFinish(t, recorder)
}
