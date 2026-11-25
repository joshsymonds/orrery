package gateway_test

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/gateway"
	"github.com/joshsymonds/orrery/internal/llmroutes"
	"github.com/joshsymonds/orrery/internal/store"
)

// The caller key, the wrong key and the household key are invented. patchbayKeyHeader is the header a caller presents
// the caller key in. exampleBravoKeyEnv is the variable the inject seat's key file is named by.
const (
	patchbayKeyHeader  = "X-Patchbay-Key"
	exampleCallerKey   = "example-caller-key-value"
	exampleWrongKey    = "example-wrong-key-value"
	exampleInjectedKey = "example-injected-api-key"
	exampleBravoKeyEnv = "EXAMPLE_BRAVO_KEY_FILE"
	injectBody         = `{"model":"model-one","max_tokens":16,"messages":[]}`
	sigv4Body          = `{"model":"model-two","max_tokens":16,"messages":[]}`
	plainBody          = `{"model":"example-plain-model","max_tokens":16,"messages":[]}`
)

// callerKeyRegistry is the route registry the caller key tests serve. The default context binds model-one to bravo, an
// inject seat whose upstream is the fake upstream and whose key file EXAMPLE_BRAVO_KEY_FILE names, and model-two to
// delta, a sigv4 seat. Every other model goes through the forward seat alpha.
func callerKeyRegistry(upstream string) llmroutes.Registry {
	return llmroutes.Registry{
		DefaultContext: "main",
		Seats: []llmroutes.Seat{
			{ID: "alpha", AuthMode: llmroutes.AuthForward, Upstream: llmroutes.AnthropicUpstream},
			{
				ID: "bravo", AuthMode: llmroutes.AuthInject, Upstream: upstream,
				Model: "example-model", APIKeyEnvFile: exampleBravoKeyEnv,
			},
			{ID: "delta", AuthMode: llmroutes.AuthSigV4, AWSProfile: "example-profile", AWSRegion: "us-east-1"},
		},
		Contexts: []llmroutes.Context{
			{
				ID: "main", DefaultSeat: "alpha",
				Models: map[string]string{"model-one": "bravo", "model-two": "delta"},
			},
		},
	}
}

// writeKeyFile writes contents to a file in a temporary directory and returns the file's path.
func writeKeyFile(t *testing.T, contents string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write the key file: %v", err)
	}

	return path
}

// seatEnv returns the environment the caller key tests' seats read. EXAMPLE_BRAVO_KEY_FILE names a file holding
// exampleInjectedKey, with the trailing newline a key file often has.
func seatEnv(t *testing.T) map[string]string {
	t.Helper()

	return map[string]string{exampleBravoKeyEnv: writeKeyFile(t, exampleInjectedKey+"\n")}
}

// newCallerKeyGateway returns a loaded Gateway that forwards to upstream, reads the caller key from callerKeyFile, and
// looks up the environment variables its seats name in env.
func newCallerKeyGateway(t *testing.T, upstream, callerKeyFile string, env map[string]string) *gateway.Gateway {
	t.Helper()

	gw, err := gateway.New(gateway.Config{
		Routes:            fakeRoutes{snapshot: store.RoutesSnapshot{Routes: callerKeyRegistry(upstream)}},
		Ledger:            fakeRecorder{},
		AnthropicUpstream: upstream,
		Logger:            discardLogger(),
		CallerKeyFile:     callerKeyFile,
		Getenv:            func(name string) string { return env[name] },
	})
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	if err := gw.Load(t.Context()); err != nil {
		t.Fatalf("Load() = %v", err)
	}

	return gw
}

// postWithHeaders sends one POST to /v1/messages through gw, with body and the given headers, and returns what it
// answered.
func postWithHeaders(
	t *testing.T,
	gw *gateway.Gateway,
	body string,
	headers map[string]string,
) *httptest.ResponseRecorder {
	t.Helper()

	req := newProxyRequest(t, http.MethodPost, "/v1/messages", body)
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	rec := httptest.NewRecorder()
	gw.ServeHTTP(rec, req)

	return rec
}

// requireCallerKeyRefusal fails the test unless rec refuses a request that presented no valid caller key: a 401 in
// Anthropic's error shape, with no upstream call.
func requireCallerKeyRefusal(t *testing.T, rec *httptest.ResponseRecorder, fake *fakeAnthropic) {
	t.Helper()

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d; want 401 for a request without a valid caller key", rec.Code)
	}
	if got := errorTypeOf(t, rec.Body.Bytes()); got != "authentication_error" {
		t.Errorf("error type = %q; want authentication_error", got)
	}
	requireNoUpstream(t, fake)
}

func TestGatewayCallerKeyInjectRefusedWithoutHeader(t *testing.T) {
	t.Parallel()

	fake := newFakeAnthropic(t, replyWith(t, http.StatusOK))
	gw := newCallerKeyGateway(t, fake.url(), writeKeyFile(t, exampleCallerKey), seatEnv(t))

	rec := postWithHeaders(t, gw, injectBody, nil)

	requireCallerKeyRefusal(t, rec, fake)
}

func TestGatewayCallerKeyInjectRefusedWithWrongHeader(t *testing.T) {
	t.Parallel()

	fake := newFakeAnthropic(t, replyWith(t, http.StatusOK))
	gw := newCallerKeyGateway(t, fake.url(), writeKeyFile(t, exampleCallerKey), seatEnv(t))

	rec := postWithHeaders(t, gw, injectBody, map[string]string{patchbayKeyHeader: exampleWrongKey})

	requireCallerKeyRefusal(t, rec, fake)
}

func TestGatewayCallerKeySigV4RefusedWithoutHeader(t *testing.T) {
	t.Parallel()

	fake := newFakeAnthropic(t, replyWith(t, http.StatusOK))
	gw := newCallerKeyGateway(t, fake.url(), writeKeyFile(t, exampleCallerKey), seatEnv(t))

	rec := postWithHeaders(t, gw, sigv4Body, nil)

	requireCallerKeyRefusal(t, rec, fake)
}

func TestGatewayCallerKeyNoKeyRefusesInject(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		callerKeyFile func(t *testing.T) string
	}{
		{name: "empty path", callerKeyFile: func(*testing.T) string { return "" }},
		{
			name: "unreadable file",
			callerKeyFile: func(t *testing.T) string {
				t.Helper()

				return filepath.Join(t.TempDir(), "missing")
			},
		},
		{
			name: "empty file",
			callerKeyFile: func(t *testing.T) string {
				t.Helper()

				return writeKeyFile(t, " \n")
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fake := newFakeAnthropic(t, replyWith(t, http.StatusOK))
			gw := newCallerKeyGateway(t, fake.url(), tc.callerKeyFile(t), seatEnv(t))

			// An empty header would match an empty expected key, so presenting one must still be refused.
			rec := postWithHeaders(t, gw, injectBody, map[string]string{patchbayKeyHeader: ""})

			requireCallerKeyRefusal(t, rec, fake)
		})
	}
}

func TestGatewayCallerKeyStrippedFromUpstream(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
	}{
		{name: "forward", body: plainBody},
		{name: "inject", body: injectBody},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fake := newFakeAnthropic(t, replyWith(t, http.StatusOK))
			gw := newCallerKeyGateway(t, fake.url(), writeKeyFile(t, exampleCallerKey), seatEnv(t))

			rec := postWithHeaders(t, gw, tc.body, map[string]string{patchbayKeyHeader: exampleCallerKey})
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d; want 200 for a request that presents the caller key", rec.Code)
			}

			got := onlyRequest(t, fake)
			if v := got.header.Get(patchbayKeyHeader); v != "" {
				t.Errorf("upstream got %s = %q; want the caller key removed", patchbayKeyHeader, v)
			}
			for name, values := range got.header {
				for _, value := range values {
					if strings.Contains(value, exampleCallerKey) {
						t.Errorf("upstream got the caller key in %s; want it absent from every header", name)
					}
				}
			}
		})
	}
}

func TestGatewayCallerKeyInjectDropsCallerCredentials(t *testing.T) {
	t.Parallel()

	fake := newFakeAnthropic(t, replyWith(t, http.StatusOK))
	gw := newCallerKeyGateway(t, fake.url(), writeKeyFile(t, exampleCallerKey), seatEnv(t))

	rec := postWithHeaders(t, gw, injectBody, map[string]string{
		patchbayKeyHeader: exampleCallerKey,
		"Authorization":   callerAuthorization,
		"X-Api-Key":       callerKeyHeader,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200 for an inject request that presents the caller key", rec.Code)
	}

	got := onlyRequest(t, fake)
	if v := got.header.Get("Authorization"); v == callerAuthorization {
		t.Errorf("upstream got the caller's Authorization; want it removed before the inject seat's call")
	}
	if v := got.header.Get("X-Api-Key"); v != "" {
		t.Errorf("upstream X-Api-Key = %q; want the caller's credential removed", v)
	}
}

func TestGatewayRoutesInjectSendsKeyFromFile(t *testing.T) {
	t.Parallel()

	fake := newFakeAnthropic(t, replyWith(t, http.StatusOK))
	gw := newCallerKeyGateway(t, fake.url(), writeKeyFile(t, exampleCallerKey), seatEnv(t))

	rec := postWithHeaders(t, gw, injectBody, map[string]string{patchbayKeyHeader: exampleCallerKey})

	got := onlyRequest(t, fake)
	if v := got.header.Get("Authorization"); v != "Bearer "+exampleInjectedKey {
		t.Errorf("upstream Authorization = %q; want the bearer key the key file holds", v)
	}
	if string(got.body) != injectBody {
		t.Errorf("upstream got body %q; want the caller's %q", got.body, injectBody)
	}
	if rec.Code != http.StatusOK || rec.Body.String() != upstreamReply {
		t.Errorf("caller got %d %q; want the upstream's 200 and body", rec.Code, rec.Body.String())
	}
}

func TestGatewayRoutesInjectMissingKeyFileRefused(t *testing.T) {
	t.Parallel()

	fake := newFakeAnthropic(t, replyWith(t, http.StatusOK))
	env := map[string]string{exampleBravoKeyEnv: filepath.Join(t.TempDir(), "missing")}
	gw := newCallerKeyGateway(t, fake.url(), writeKeyFile(t, exampleCallerKey), env)

	rec := postWithHeaders(t, gw, injectBody, map[string]string{patchbayKeyHeader: exampleCallerKey})

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d; want 500 when the seat's key file is missing", rec.Code)
	}
	if got := errorTypeOf(t, rec.Body.Bytes()); got != "api_error" {
		t.Errorf("error type = %q; want api_error", got)
	}
	requireNoUpstream(t, fake)
}

// newAccountInjectGateway returns a loaded Gateway serving callerKeyRegistry with its default context naming
// example-account, whose token accountGrant the route registry stores, so that an inject request is served beside a
// stored account token. Its log is written to logs.
func newAccountInjectGateway(t *testing.T, upstream string, logs *bytes.Buffer) *gateway.Gateway {
	t.Helper()

	registry := callerKeyRegistry(upstream)
	registry.Contexts[0].Account = "example-account"
	env := seatEnv(t)

	gw, err := gateway.New(gateway.Config{
		Routes: fakeRoutes{snapshot: store.RoutesSnapshot{
			Routes: registry,
			Tokens: map[string]string{"example-account": accountGrant},
		}},
		Ledger:            fakeRecorder{},
		AnthropicUpstream: upstream,
		Logger:            slog.New(slog.NewTextHandler(logs, nil)),
		CallerKeyFile:     writeKeyFile(t, exampleCallerKey),
		Getenv:            func(name string) string { return env[name] },
	})
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	if err := gw.Load(t.Context()); err != nil {
		t.Fatalf("Load() = %v", err)
	}

	return gw
}

func TestGatewayAccountInjectKeepsTokenOutOfRequest(t *testing.T) {
	t.Parallel()

	fake := newFakeAnthropic(t, replyWith(t, http.StatusOK))
	logs := &bytes.Buffer{}
	gw := newAccountInjectGateway(t, fake.url(), logs)

	rec := postWithHeaders(t, gw, injectBody, map[string]string{patchbayKeyHeader: exampleCallerKey})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200 for an inject request that presents the caller key", rec.Code)
	}

	got := onlyRequest(t, fake)
	if v := got.header.Values("Authorization"); len(v) != 1 || v[0] != "Bearer "+exampleInjectedKey {
		t.Errorf("upstream Authorization = %q; want only the seat's injected key", v)
	}
	for name, values := range got.header {
		for _, value := range values {
			if strings.Contains(value, accountGrant) {
				t.Errorf("upstream header %s carries the account token: %q", name, value)
			}
		}
	}
	if strings.Contains(string(got.body), accountGrant) {
		t.Errorf("upstream body carries the account token: %q", got.body)
	}
	if strings.Contains(logs.String(), accountGrant) {
		t.Errorf("gateway log carries the account token: %q", logs.String())
	}
}

func TestGatewayAccountInjectKeepsTokenOutOfErrorsAndLogs(t *testing.T) {
	t.Parallel()

	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close()

	logs := &bytes.Buffer{}
	gw := newAccountInjectGateway(t, deadURL, logs)

	rec := postWithHeaders(t, gw, injectBody, map[string]string{patchbayKeyHeader: exampleCallerKey})

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d; want 502 for an inject upstream that is down", rec.Code)
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
