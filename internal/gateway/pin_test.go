package gateway_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/joshsymonds/orrery/internal/gateway"
	"github.com/joshsymonds/orrery/internal/llmroutes"
	"github.com/joshsymonds/orrery/internal/store"
)

// The pin tests' account is one no context names, so only the snapshot's AccountTokens holds its token. Its id and its
// token are invented. The peers are invented addresses: loopbackPeer is on the loopback interface, and remotePeer is
// in a documentation range that no local process holds.
const (
	pinnedAccount = "example-pinned-account"
	pinnedGrant   = "example-pinned-grant"
	loopbackPeer  = "127.0.0.1:41000"
	remotePeer    = "198.51.100.7:41000"
)

// pinRegistry is the registry the pin tests serve. Its main context forwards through alpha, with the caller's own
// credential, and binds model-inject to the inject seat bravo and model-sigv4 to the sigv4 seat charlie. Its work
// context forwards through alpha and sends the token of the account example-account. The inject seat sends to upstream,
// so that a request reaching it would reach the fake Anthropic.
func pinRegistry(upstream string) llmroutes.Registry {
	return llmroutes.Registry{
		DefaultContext: "main",
		Seats: []llmroutes.Seat{
			{ID: "alpha", AuthMode: llmroutes.AuthForward, Upstream: llmroutes.AnthropicUpstream},
			{
				ID: "bravo", AuthMode: llmroutes.AuthInject, Upstream: upstream, Model: "example-model",
				APIKeyEnvFile: "EXAMPLE_KEY_FILE",
			},
			{
				ID: "charlie", AuthMode: llmroutes.AuthSigV4, Model: "example-sigv4-model",
				AWSProfile: "example-profile", AWSRegion: "us-east-1",
			},
		},
		Contexts: []llmroutes.Context{
			{
				ID: "main", DefaultSeat: "alpha",
				Models: map[string]string{"model-inject": "bravo", "model-sigv4": "charlie"},
			},
			{ID: "work", DefaultSeat: "alpha", Account: "example-account"},
		},
	}
}

// newPinGateway returns a Gateway that serves pinRegistry with its routes loaded. Its forward seats and its inject seat
// send to upstream, and so does its Bedrock endpoint, so every upstream the gateway could reach is the fake Anthropic.
func newPinGateway(t *testing.T, upstream string) *gateway.Gateway {
	t.Helper()

	routes := fakeRoutes{snapshot: store.RoutesSnapshot{
		Routes:        pinRegistry(upstream),
		Tokens:        map[string]string{"example-account": accountGrant},
		AccountTokens: map[string]string{"example-account": accountGrant, pinnedAccount: pinnedGrant},
	}}
	gw, err := gateway.New(gateway.Config{
		Routes:            routes,
		Ledger:            fakeRecorder{},
		AnthropicUpstream: upstream,
		BedrockEndpoint:   func(string) string { return upstream },
		Logger:            discardLogger(),
	})
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	if err := gw.Load(t.Context()); err != nil {
		t.Fatalf("Load() = %v", err)
	}

	return gw
}

// pinRequest returns a POST to target with body, which pins account with the X-Orrery-Account header and is sent from
// the peer at remoteAddr.
func pinRequest(t *testing.T, target, body, account, remoteAddr string) *http.Request {
	t.Helper()

	req := newProxyRequest(t, http.MethodPost, target, body)
	req.Header.Set("X-Orrery-Account", account)
	req.RemoteAddr = remoteAddr

	return req
}

func TestGatewayProbeAccountLoopbackPinSendsAccountToken(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		target string
	}{
		{name: "context names no account", target: "/v1/messages"},
		{name: "context names another account", target: "/ctx/work/v1/messages"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fake := newFakeAnthropic(t, replyWith(t, http.StatusOK))
			gw := newPinGateway(t, fake.url())

			req := pinRequest(t, tc.target, forwardBody, pinnedAccount, loopbackPeer)
			req.Header.Set("Authorization", callerAuthorization)
			req.Header.Set("X-Api-Key", callerKeyHeader)
			rec := httptest.NewRecorder()
			gw.ServeHTTP(rec, req)

			got := onlyRequest(t, fake)
			authorization := got.header.Values("Authorization")
			if len(authorization) != 1 || authorization[0] != "Bearer "+pinnedGrant {
				t.Errorf("upstream Authorization = %q; want only the pinned account's bearer token", authorization)
			}
			if keys := got.header.Values("X-Api-Key"); len(keys) != 0 {
				t.Errorf("upstream got the caller's X-Api-Key %q; want it removed", keys)
			}
			if pins := got.header.Values("X-Orrery-Account"); len(pins) != 0 {
				t.Errorf("upstream got the account pin %q; want it removed before forwarding", pins)
			}
			if rec.Code != http.StatusOK || rec.Body.String() != upstreamReply {
				t.Errorf("caller got %d %q; want the upstream's 200 and body", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestGatewayProbeAccountNonLoopbackPeerRefused(t *testing.T) {
	t.Parallel()

	fake := newFakeAnthropic(t, replyWith(t, http.StatusOK))
	gw := newPinGateway(t, fake.url())

	rec := httptest.NewRecorder()
	gw.ServeHTTP(rec, pinRequest(t, "/v1/messages", forwardBody, pinnedAccount, remotePeer))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d; want 403 for a pin from a non-loopback peer", rec.Code)
	}
	if got := errorTypeOf(t, rec.Body.Bytes()); got != "permission_error" {
		t.Errorf("error type = %q; want permission_error", got)
	}
	requireNoUpstream(t, fake)
}

func TestGatewayProbeAccountUnknownAccountRefused(t *testing.T) {
	t.Parallel()

	fake := newFakeAnthropic(t, replyWith(t, http.StatusOK))
	gw := newPinGateway(t, fake.url())

	rec := httptest.NewRecorder()
	gw.ServeHTTP(rec, pinRequest(t, "/v1/messages", forwardBody, "example-unknown-account", loopbackPeer))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400 for an account the store does not hold", rec.Code)
	}
	if got := errorTypeOf(t, rec.Body.Bytes()); got != "invalid_request_error" {
		t.Errorf("error type = %q; want invalid_request_error", got)
	}
	requireNoUpstream(t, fake)
}

func TestGatewayProbeAccountInjectSeatRefused(t *testing.T) {
	t.Parallel()

	requirePinSeatRefused(t, `{"model":"model-inject"}`)
}

func TestGatewayProbeAccountSigV4SeatRefused(t *testing.T) {
	t.Parallel()

	requirePinSeatRefused(t, `{"model":"model-sigv4"}`)
}

// requirePinSeatRefused sends a loopback request with an account pin that resolves to a seat other than forward, and
// fails the test unless the request is refused with a 400 before the fake Anthropic is contacted.
func requirePinSeatRefused(t *testing.T, body string) {
	t.Helper()

	fake := newFakeAnthropic(t, replyWith(t, http.StatusOK))
	gw := newPinGateway(t, fake.url())

	rec := httptest.NewRecorder()
	gw.ServeHTTP(rec, pinRequest(t, "/v1/messages", body, pinnedAccount, loopbackPeer))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400 for a pin on a seat that is not forward", rec.Code)
	}
	if got := errorTypeOf(t, rec.Body.Bytes()); got != "invalid_request_error" {
		t.Errorf("error type = %q; want invalid_request_error", got)
	}
	requireNoUpstream(t, fake)
}
