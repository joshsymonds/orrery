package gateway_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/joshsymonds/orrery/internal/gateway"
	"github.com/joshsymonds/orrery/internal/ledger"
	"github.com/joshsymonds/orrery/internal/llmroutes"
	"github.com/joshsymonds/orrery/internal/store"
)

// proxyRegistry is the route registry the forward tests serve, with invented seat ids and hosts. Its main context is
// the default: it forwards through alpha with the caller's own credential, binds model-one to bravo, which injects a
// key, and sends a subagent of type explorer to bravo too. Its work context forwards through alpha and sends the token
// of the account example-account.
func proxyRegistry() llmroutes.Registry {
	return llmroutes.Registry{
		DefaultContext: "main",
		Seats: []llmroutes.Seat{
			{ID: "alpha", AuthMode: llmroutes.AuthForward, Upstream: llmroutes.AnthropicUpstream},
			{ID: "bravo", AuthMode: llmroutes.AuthInject, Upstream: "https://bravo.example.test", Model: "example-model"},
		},
		Contexts: []llmroutes.Context{
			{
				ID: "main", DefaultSeat: "alpha", Models: map[string]string{"model-one": "bravo"},
				Subagents: &llmroutes.Subagents{
					DefaultSeat: "alpha",
					AgentTypes:  map[string]string{"explorer": "bravo"},
				},
			},
			{ID: "work", DefaultSeat: "alpha", Account: "example-account"},
		},
	}
}

// fakeRoutes is a RoutesReader that answers with snapshot, or with err when err is set.
type fakeRoutes struct {
	snapshot store.RoutesSnapshot
	err      error
}

// RoutesSnapshot answers with the fake's snapshot and error.
func (f fakeRoutes) RoutesSnapshot(context.Context) (store.RoutesSnapshot, error) {
	return f.snapshot, f.err
}

// routesWithTokens returns a RoutesReader that reads proxyRegistry and the given account tokens.
func routesWithTokens(tokens map[string]string) fakeRoutes {
	return fakeRoutes{snapshot: store.RoutesSnapshot{Routes: proxyRegistry(), Tokens: tokens}}
}

// fakeRecorder is a Recorder that keeps no records, since ledger recording is not part of the forward path these tests
// cover.
type fakeRecorder struct{}

// Start returns a zero id and no error.
func (fakeRecorder) Start(context.Context, ledger.Start) (int64, error) {
	return 0, nil
}

// Finish records nothing and returns no error.
func (fakeRecorder) Finish(context.Context, int64, ledger.Finish) error {
	return nil
}

// seenRequest is what a fake Anthropic received of one request.
type seenRequest struct {
	method string
	path   string
	query  string
	header http.Header
	body   []byte
}

// fakeAnthropic is an httptest server standing in for Anthropic's API. It records every request it receives, then
// answers it with the respond function it was built with.
type fakeAnthropic struct {
	server *httptest.Server
	mu     sync.Mutex
	seen   []seenRequest
}

// newFakeAnthropic starts a fake Anthropic that answers each request with respond, and closes it when the test ends.
func newFakeAnthropic(t *testing.T, respond http.HandlerFunc) *fakeAnthropic {
	t.Helper()

	fake := &fakeAnthropic{}
	fake.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read the request body the gateway sent: %v", err)
		}

		fake.mu.Lock()
		fake.seen = append(fake.seen, seenRequest{
			method: r.Method,
			path:   r.URL.Path,
			query:  r.URL.RawQuery,
			header: r.Header.Clone(),
			body:   body,
		})
		fake.mu.Unlock()

		respond(w, r)
	}))
	t.Cleanup(fake.server.Close)

	return fake
}

// url returns the base URL of the fake Anthropic.
func (f *fakeAnthropic) url() string {
	return f.server.URL
}

// requests returns what the fake Anthropic has received, in arrival order.
func (f *fakeAnthropic) requests() []seenRequest {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.seen)
}

// replyWith returns a fake Anthropic handler that answers with status and body.
func replyWith(t *testing.T, status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if _, err := io.WriteString(w, body); err != nil {
			t.Errorf("write the fake Anthropic's reply: %v", err)
		}
	}
}

// newUnloadedGateway returns a Gateway that forwards to upstream and reads routes, which has not yet been loaded.
func newUnloadedGateway(
	t *testing.T,
	routes gateway.RoutesReader,
	upstream string,
	logger *slog.Logger,
) *gateway.Gateway {
	t.Helper()

	gw, err := gateway.New(gateway.Config{
		Routes:            routes,
		Ledger:            fakeRecorder{},
		AnthropicUpstream: upstream,
		Logger:            logger,
	})
	if err != nil {
		t.Fatalf("New() = %v", err)
	}

	return gw
}

// newGateway returns a Gateway that forwards to upstream and has loaded routes.
func newGateway(t *testing.T, routes gateway.RoutesReader, upstream string, logger *slog.Logger) *gateway.Gateway {
	t.Helper()

	gw := newUnloadedGateway(t, routes, upstream, logger)
	if err := gw.Load(t.Context()); err != nil {
		t.Fatalf("Load() = %v", err)
	}

	return gw
}

// newProxyRequest returns a request to target with body, bound to the test's context.
func newProxyRequest(t *testing.T, method, target, body string) *http.Request {
	t.Helper()

	return httptest.NewRequestWithContext(t.Context(), method, target, strings.NewReader(body))
}

// discardLogger returns a logger whose output is thrown away, so that test output carries only the test's own lines.
func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

// onlyRequest returns the one request the fake Anthropic received, failing the test when it received any other number.
func onlyRequest(t *testing.T, fake *fakeAnthropic) seenRequest {
	t.Helper()

	seen := fake.requests()
	if len(seen) != 1 {
		t.Fatalf("the fake Anthropic received %d requests; want 1", len(seen))
	}

	return seen[0]
}

// requireNoUpstream fails the test when the fake Anthropic received any request.
func requireNoUpstream(t *testing.T, fake *fakeAnthropic) {
	t.Helper()

	if seen := fake.requests(); len(seen) != 0 {
		t.Fatalf("the gateway contacted the upstream %d times; want none", len(seen))
	}
}

// errorTypeOf returns the error type of an Anthropic-shaped error body, failing the test when the body is not one.
func errorTypeOf(t *testing.T, body []byte) string {
	t.Helper()

	var envelope struct {
		Type  string `json:"type"`
		Error struct {
			Type string `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("error body %q is not JSON: %v", body, err)
	}
	if envelope.Type != "error" {
		t.Fatalf("error body %q has type %q; want error", body, envelope.Type)
	}

	return envelope.Error.Type
}
