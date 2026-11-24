package gateway_test

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/joshsymonds/orrery/internal/gateway"
)

// The caller's credentials and the account token are invented. accountGrant stands for the token the registry stores
// for example-account.
const (
	callerAuthorization = "Bearer example-caller-login"
	callerKeyHeader     = "example-caller-key"
	accountGrant        = "example-account-grant"
	forwardBody         = `{"model":"model-two","max_tokens":16,"messages":[]}`
	upstreamReply       = `{"id":"example-message"}`
)

// admissionBound is how many requests the gateway serves at once, which is requestAdmissionCapacity.
const admissionBound = 34

// heldPath is the path the admission test's upstream holds open until the test releases it.
const heldPath = "/v1/messages"

// probePath is the path the admission test's probes name. The upstream answers it at once.
const probePath = "/v1/models"

func TestGatewayRoutesForwardPassesPathQueryBodyAndCredential(t *testing.T) {
	t.Parallel()

	fake := newFakeAnthropic(t, replyWith(t, http.StatusCreated))
	gw := newGateway(t, routesWithTokens(nil), fake.url(), discardLogger())

	req := newProxyRequest(t, http.MethodPost, "/v1/messages?beta=true", forwardBody)
	req.Header.Set("Authorization", callerAuthorization)
	req.Header.Set("X-Api-Key", callerKeyHeader)
	rec := httptest.NewRecorder()
	gw.ServeHTTP(rec, req)

	got := onlyRequest(t, fake)
	if got.method != http.MethodPost || got.path != "/v1/messages" || got.query != "beta=true" {
		t.Errorf("upstream got %s %s?%s; want POST /v1/messages?beta=true", got.method, got.path, got.query)
	}
	if string(got.body) != forwardBody {
		t.Errorf("upstream got body %q; want the caller's %q", got.body, forwardBody)
	}
	if v := got.header.Get("Authorization"); v != callerAuthorization {
		t.Errorf("upstream Authorization = %q; want the caller's %q", v, callerAuthorization)
	}
	if v := got.header.Get("X-Api-Key"); v != callerKeyHeader {
		t.Errorf("upstream X-Api-Key = %q; want the caller's %q", v, callerKeyHeader)
	}
	if rec.Code != http.StatusCreated || rec.Body.String() != upstreamReply {
		t.Errorf("caller got %d %q; want the upstream's 201 and body", rec.Code, rec.Body.String())
	}
}

func TestGatewayRoutesForwardNamedContextStripsPrefix(t *testing.T) {
	t.Parallel()

	fake := newFakeAnthropic(t, replyWith(t, http.StatusOK))
	routes := routesWithTokens(map[string]string{"example-account": accountGrant})
	gw := newGateway(t, routes, fake.url(), discardLogger())

	req := newProxyRequest(t, http.MethodPost, "/ctx/work/v1/messages?beta=true", forwardBody)
	gw.ServeHTTP(httptest.NewRecorder(), req)

	got := onlyRequest(t, fake)
	if got.path != "/v1/messages" || got.query != "beta=true" {
		t.Errorf("upstream got path %q query %q; want /v1/messages and beta=true", got.path, got.query)
	}
}

func TestGatewayRoutesForwardDropsHopByHopHeaders(t *testing.T) {
	t.Parallel()

	fake := newFakeAnthropic(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Connection", "X-Example-Upstream-Hop")
		w.Header().Set("X-Example-Upstream-Hop", "1")
		w.Header().Set("Keep-Alive", "timeout=5")
		w.Header().Set("X-Example-Kept", "yes")
		w.WriteHeader(http.StatusOK)
	})
	gw := newGateway(t, routesWithTokens(nil), fake.url(), discardLogger())

	req := newProxyRequest(t, http.MethodPost, "/v1/messages", forwardBody)
	req.Header.Set("Connection", "X-Example-Caller-Hop")
	req.Header.Set("X-Example-Caller-Hop", "1")
	req.Header.Set("Keep-Alive", "timeout=5")
	req.Header.Set("Anthropic-Version", "2023-06-01")
	rec := httptest.NewRecorder()
	gw.ServeHTTP(rec, req)

	got := onlyRequest(t, fake)
	if v := got.header.Get("X-Example-Caller-Hop"); v != "" {
		t.Errorf("upstream got the caller's hop-by-hop header %q; want it dropped", v)
	}
	if v := got.header.Get("Keep-Alive"); v != "" {
		t.Errorf("upstream got the caller's Keep-Alive %q; want it dropped", v)
	}
	if v := got.header.Get("Anthropic-Version"); v != "2023-06-01" {
		t.Errorf("upstream Anthropic-Version = %q; want the caller's end-to-end header kept", v)
	}
	for _, name := range []string{"Connection", "Keep-Alive", "X-Example-Upstream-Hop"} {
		if v := rec.Header().Get(name); v != "" {
			t.Errorf("caller got the upstream's hop-by-hop header %s = %q; want it dropped", name, v)
		}
	}
	if v := rec.Header().Get("X-Example-Kept"); v != "yes" {
		t.Errorf("caller X-Example-Kept = %q; want the upstream's end-to-end header relayed", v)
	}
}

func TestGatewayRoutesForwardUnknownContextIs404(t *testing.T) {
	t.Parallel()

	fake := newFakeAnthropic(t, replyWith(t, http.StatusOK))
	gw := newGateway(t, routesWithTokens(nil), fake.url(), discardLogger())

	rec := httptest.NewRecorder()
	gw.ServeHTTP(rec, newProxyRequest(t, http.MethodPost, "/ctx/missing/v1/messages", forwardBody))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want 404 for a context the registry does not define", rec.Code)
	}
	if got := errorTypeOf(t, rec.Body.Bytes()); got != "not_found_error" {
		t.Errorf("error type = %q; want not_found_error", got)
	}
	requireNoUpstream(t, fake)
}

func TestGatewayRoutesForwardLoadFailureAnswers503(t *testing.T) {
	t.Parallel()

	fake := newFakeAnthropic(t, replyWith(t, http.StatusOK))
	routes := fakeRoutes{err: errors.New("example registry unreadable")}
	gw := newUnloadedGateway(t, routes, fake.url(), discardLogger())

	if err := gw.Load(t.Context()); err == nil {
		t.Fatal("Load() = nil; want the failed read's error")
	}

	rec := httptest.NewRecorder()
	gw.ServeHTTP(rec, newProxyRequest(t, http.MethodPost, "/v1/messages", forwardBody))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d; want 503 until a registry read has succeeded", rec.Code)
	}
	if got := errorTypeOf(t, rec.Body.Bytes()); got != "api_error" {
		t.Errorf("error type = %q; want api_error", got)
	}
	requireNoUpstream(t, fake)
}

func TestGatewayRoutesForwardRefusesSeatWithoutForward(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		body    string
		headers map[string]string
	}{
		{name: "model binding", body: `{"model":"model-one"}`},
		{
			name: "subagent agent type",
			body: forwardBody,
			headers: map[string]string{
				"X-Claude-Code-Agent-Id":   "example-agent",
				"X-Claude-Code-Agent-Type": "explorer",
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fake := newFakeAnthropic(t, replyWith(t, http.StatusOK))
			gw := newGateway(t, routesWithTokens(nil), fake.url(), discardLogger())

			req := newProxyRequest(t, http.MethodPost, "/v1/messages", tc.body)
			for name, value := range tc.headers {
				req.Header.Set(name, value)
			}
			rec := httptest.NewRecorder()
			gw.ServeHTTP(rec, req)

			if rec.Code != http.StatusNotImplemented {
				t.Fatalf("status = %d; want 501 for a seat that does not forward", rec.Code)
			}
			requireNoUpstream(t, fake)
		})
	}
}

func TestGatewayRoutesAdmissionRefusesWhenBoundIsFull(t *testing.T) {
	t.Parallel()

	arrivals := make(chan struct{}, admissionBound+1)
	release := make(chan struct{})
	var holds sync.WaitGroup
	fake := newFakeAnthropic(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == heldPath {
			arrivals <- struct{}{}
			<-release
		}
		w.WriteHeader(http.StatusOK)
	})
	t.Cleanup(func() {
		close(release)
		holds.Wait()
	})
	gw := newGateway(t, routesWithTokens(nil), fake.url(), discardLogger())

	// Each round probes for room and, when there is room, holds one request open until the upstream has it. The probe
	// that finds the bound full is the request the bound must refuse.
	admitted := 0
	for range admissionBound + 1 {
		probe := httptest.NewRecorder()
		gw.ServeHTTP(probe, newProxyRequest(t, http.MethodGet, probePath, ""))
		if probe.Code == http.StatusServiceUnavailable {
			checkAdmissionRefusal(t, probe, fake, admitted)
			return
		}
		if probe.Code != http.StatusOK {
			t.Fatalf("probe answered %d; want 200 while there is room, or 503 once it is full", probe.Code)
		}
		holdOne(t, gw, &holds, arrivals)
		admitted++
	}

	t.Fatalf("the bound admitted %d requests and refused none", admitted)
}

// holdOne sends a request the upstream holds open, and waits until the upstream has it, so the permit the request took
// is known to be held. It fails the test if the request finishes without reaching the upstream.
func holdOne(t *testing.T, gw *gateway.Gateway, holds *sync.WaitGroup, arrivals <-chan struct{}) {
	t.Helper()

	req := newProxyRequest(t, http.MethodPost, heldPath, "{}")
	finished := make(chan struct{})
	holds.Add(1)
	go func() {
		defer holds.Done()
		defer close(finished)
		gw.ServeHTTP(httptest.NewRecorder(), req)
	}()

	select {
	case <-arrivals:
	case <-finished:
		t.Fatal("a request the bound admitted finished without reaching the upstream")
	}
}

// checkAdmissionRefusal fails the test unless probe is the refusal of a request past the bound: an Anthropic-shaped
// 503, after exactly the admitted probes reached the upstream, and not the refused one.
func checkAdmissionRefusal(t *testing.T, probe *httptest.ResponseRecorder, fake *fakeAnthropic, admitted int) {
	t.Helper()

	if admitted != admissionBound {
		t.Fatalf("refused after %d admitted requests; want the bound of %d", admitted, admissionBound)
	}
	if ct := probe.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("refusal Content-Type = %q; want application/json", ct)
	}
	if got := errorTypeOf(t, probe.Body.Bytes()); got != "api_error" {
		t.Errorf("refusal error type = %q; want api_error", got)
	}

	reached := 0
	for _, seen := range fake.requests() {
		if seen.path == probePath {
			reached++
		}
	}
	if reached != admitted {
		t.Errorf("%d probes reached the upstream; want %d, the admitted ones only", reached, admitted)
	}
}

func TestGatewayAccountForwardSendsContextAccountToken(t *testing.T) {
	t.Parallel()

	fake := newFakeAnthropic(t, replyWith(t, http.StatusOK))
	routes := routesWithTokens(map[string]string{"example-account": accountGrant})
	gw := newGateway(t, routes, fake.url(), discardLogger())

	req := newProxyRequest(t, http.MethodPost, "/ctx/work/v1/messages", forwardBody)
	req.Header.Set("Authorization", callerAuthorization)
	req.Header.Set("X-Api-Key", callerKeyHeader)
	gw.ServeHTTP(httptest.NewRecorder(), req)

	got := onlyRequest(t, fake)
	authorization := got.header.Values("Authorization")
	if len(authorization) != 1 || authorization[0] != "Bearer "+accountGrant {
		t.Errorf("upstream Authorization = %q; want only the account's bearer token", authorization)
	}
	if keys := got.header.Values("X-Api-Key"); len(keys) != 0 {
		t.Errorf("upstream got the caller's X-Api-Key %q; want it removed", keys)
	}
}

func TestGatewayAccountForwardUnsetAccountPassesCallerCredential(t *testing.T) {
	t.Parallel()

	fake := newFakeAnthropic(t, replyWith(t, http.StatusOK))
	routes := routesWithTokens(map[string]string{"example-account": accountGrant})
	gw := newGateway(t, routes, fake.url(), discardLogger())

	req := newProxyRequest(t, http.MethodPost, "/v1/messages", forwardBody)
	req.Header.Set("Authorization", callerAuthorization)
	req.Header.Set("X-Api-Key", callerKeyHeader)
	gw.ServeHTTP(httptest.NewRecorder(), req)

	got := onlyRequest(t, fake)
	authorization := got.header.Values("Authorization")
	if len(authorization) != 1 || authorization[0] != callerAuthorization {
		t.Errorf("upstream Authorization = %q; want the caller's %q, as the context names no account",
			authorization, callerAuthorization)
	}
	if v := got.header.Get("X-Api-Key"); v != callerKeyHeader {
		t.Errorf("upstream X-Api-Key = %q; want the caller's %q, as the context names no account", v, callerKeyHeader)
	}
}

func TestGatewayAccountForwardMissingTokenRefused(t *testing.T) {
	t.Parallel()

	fake := newFakeAnthropic(t, replyWith(t, http.StatusOK))
	gw := newGateway(t, routesWithTokens(nil), fake.url(), discardLogger())

	req := newProxyRequest(t, http.MethodPost, "/ctx/work/v1/messages", forwardBody)
	req.Header.Set("Authorization", callerAuthorization)
	rec := httptest.NewRecorder()
	gw.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d; want 500 for an account with no token", rec.Code)
	}
	requireNoUpstream(t, fake)
}

func TestGatewayAccountForwardKeepsTokenOutOfErrorsAndLogs(t *testing.T) {
	t.Parallel()

	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close()

	logs := &bytes.Buffer{}
	routes := routesWithTokens(map[string]string{"example-account": accountGrant})
	gw := newGateway(t, routes, deadURL, slog.New(slog.NewTextHandler(logs, nil)))

	rec := httptest.NewRecorder()
	gw.ServeHTTP(rec, newProxyRequest(t, http.MethodPost, "/ctx/work/v1/messages", forwardBody))

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d; want 502 for an upstream that is down", rec.Code)
	}
	if strings.Contains(rec.Body.String(), accountGrant) {
		t.Errorf("caller's response body carries the account token: %q", rec.Body.String())
	}
	if logs.Len() == 0 {
		t.Fatal("the failed upstream request was not logged")
	}
	if strings.Contains(logs.String(), accountGrant) {
		t.Errorf("gateway log carries the account token: %q", logs.String())
	}
}
