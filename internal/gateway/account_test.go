package gateway_test

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/gateway"
	"github.com/joshsymonds/orrery/internal/llmroutes"
	"github.com/joshsymonds/orrery/internal/store"
)

// accountRegistry is the route registry the stored-token tests serve. Its main context names example-account, whose
// token the registry stores, so every request it serves is served beside that token. The context sends every model
// through bravo, an inject seat whose upstream is injectUpstream, except model-two, which goes to charlie, a sigv4
// seat.
func accountRegistry(injectUpstream string) llmroutes.Registry {
	return llmroutes.Registry{
		DefaultContext: "main",
		Seats: []llmroutes.Seat{
			{
				ID: "bravo", AuthMode: llmroutes.AuthInject, Upstream: injectUpstream,
				Model: "example-model", APIKeyEnvFile: exampleBravoKeyEnv,
			},
			{
				ID: "charlie", AuthMode: llmroutes.AuthSigV4,
				AWSProfile: bedrockProfile, AWSRegion: bedrockRegion, ModelMapEnvFile: bedrockMapEnv,
			},
		},
		Contexts: []llmroutes.Context{{
			ID: "main", DefaultSeat: "bravo", Account: "example-account",
			Models: map[string]string{"model-two": "charlie"},
		}},
	}
}

// newAccountGateway returns a loaded Gateway serving accountRegistry. Its inject seat sends to injectUpstream, and its
// sigv4 seat's Bedrock endpoint is bedrockEndpoint. The registry stores accountGrant as the token of example-account.
// The gateway logs everything, debug lines included, to logs.
func newAccountGateway(t *testing.T, injectUpstream, bedrockEndpoint string, logs *bytes.Buffer) *gateway.Gateway {
	t.Helper()

	env := seatEnv(t)
	env[bedrockMapEnv] = writeModelsFile(t, `{"model-two":"`+bedrockModel+`"}`)
	gw, err := gateway.New(gateway.Config{
		Routes: fakeRoutes{snapshot: store.RoutesSnapshot{
			Routes: accountRegistry(injectUpstream),
			Tokens: map[string]string{"example-account": accountGrant},
		}},
		Ledger:            fakeRecorder{},
		Clock:             bedrockClock{},
		AnthropicUpstream: injectUpstream,
		Logger:            slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		CallerKeyFile:     writeKeyFile(t, exampleCallerKey),
		Getenv:            func(name string) string { return env[name] },
		AWSCredentials:    bedrockCredentials,
		BedrockEndpoint:   func(region string) string { return bedrockEndpoint + "/" + region },
	})
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	if err := gw.Load(t.Context()); err != nil {
		t.Fatalf("Load() = %v", err)
	}

	return gw
}

// requireNoStoredToken fails the test when the stored account token appears in the name or value of any header of a
// request the fake upstream received, or in its body.
func requireNoStoredToken(t *testing.T, got seenRequest) {
	t.Helper()

	for name, values := range got.header {
		if strings.Contains(name, accountGrant) {
			t.Errorf("upstream header name %q carries the account token", name)
		}
		for _, value := range values {
			if strings.Contains(value, accountGrant) {
				t.Errorf("upstream header %s carries the account token: %q", name, value)
			}
		}
	}
	if strings.Contains(string(got.body), accountGrant) {
		t.Errorf("upstream body carries the account token: %q", got.body)
	}
}

// requireNoStoredTokenInAnswer fails the test when the stored account token appears in any header or body the caller
// received, or in the gateway's log. The log must have recorded something, so that its absence of the token means
// something.
func requireNoStoredTokenInAnswer(t *testing.T, rec *httptest.ResponseRecorder, logs *bytes.Buffer) {
	t.Helper()

	for name, values := range rec.Header() {
		for _, value := range values {
			if strings.Contains(value, accountGrant) {
				t.Errorf("caller's response header %s carries the account token: %q", name, value)
			}
		}
	}
	if strings.Contains(rec.Body.String(), accountGrant) {
		t.Errorf("caller's response body carries the account token: %q", rec.Body.String())
	}
	if logs.Len() == 0 {
		t.Fatal("the gateway logged nothing, so the log cannot be shown to be free of the account token")
	}
	if strings.Contains(logs.String(), accountGrant) {
		t.Errorf("gateway log carries the account token: %q", logs.String())
	}
}

func TestGatewayAccountStoredTokenOffInjectRequest(t *testing.T) {
	t.Parallel()

	fake := newFakeAnthropic(t, replyWith(t, http.StatusOK))
	logs := &bytes.Buffer{}
	gw := newAccountGateway(t, fake.url(), fake.url(), logs)

	rec := postAsCaller(t, gw, "/v1/messages", injectBody, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200 for an inject request that presents the caller key", rec.Code)
	}

	got := onlyRequest(t, fake)
	if v := got.header.Values("Authorization"); len(v) != 1 || v[0] != "Bearer "+exampleInjectedKey {
		t.Errorf("upstream Authorization = %q; want only the seat's injected key", v)
	}
	requireNoStoredToken(t, got)
	if rec.Body.String() != upstreamReply {
		t.Errorf("caller got body %q; want the upstream's answer", rec.Body.String())
	}
	if strings.Contains(logs.String(), accountGrant) {
		t.Errorf("gateway log carries the account token: %q", logs.String())
	}
}

func TestGatewayAccountStoredTokenOffInjectError(t *testing.T) {
	t.Parallel()

	logs := &bytes.Buffer{}
	gw := newAccountGateway(t, hangUpURL(t), hangUpURL(t), logs)

	rec := postAsCaller(t, gw, "/v1/messages", injectBody, nil)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d; want 502 for an inject upstream that is down", rec.Code)
	}
	requireNoStoredTokenInAnswer(t, rec, logs)
}

func TestGatewayAccountStoredTokenOffSigV4Request(t *testing.T) {
	t.Parallel()

	fake := newFakeAnthropic(t, replyWith(t, http.StatusOK))
	logs := &bytes.Buffer{}
	gw := newAccountGateway(t, fake.url(), fake.url(), logs)

	rec := postAsCaller(t, gw, "/v1/messages", sigv4Body, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200 for a sigv4 request that presents the caller key", rec.Code)
	}

	got := onlyRequest(t, fake)
	if !strings.HasPrefix(got.header.Get("Authorization"), "AWS4-HMAC-SHA256 ") {
		t.Errorf("upstream Authorization = %q; want the request signed for Bedrock", got.header.Get("Authorization"))
	}
	requireNoStoredToken(t, got)
	if rec.Body.String() != upstreamReply {
		t.Errorf("caller got body %q; want the upstream's answer", rec.Body.String())
	}
	if strings.Contains(logs.String(), accountGrant) {
		t.Errorf("gateway log carries the account token: %q", logs.String())
	}
}

func TestGatewayAccountStoredTokenOffSigV4Error(t *testing.T) {
	t.Parallel()

	logs := &bytes.Buffer{}
	gw := newAccountGateway(t, hangUpURL(t), hangUpURL(t), logs)

	rec := postAsCaller(t, gw, "/v1/messages", sigv4Body, nil)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d; want 502 for a Bedrock endpoint that is down", rec.Code)
	}
	requireNoStoredTokenInAnswer(t, rec, logs)
}
