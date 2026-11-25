package gateway_test

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash/crc32"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/gateway"
	"github.com/joshsymonds/orrery/internal/gateway/awssig"
	"github.com/joshsymonds/orrery/internal/llmroutes"
	"github.com/joshsymonds/orrery/internal/store"
)

// The Bedrock tests serve invented seats, credentials and models. bedrockProfile is the AWS profile the sigv4 seat
// signs with, and bedrockRegion the region it signs in. bedrockModel is the Bedrock model the seat sends to.
const (
	bedrockProfile    = "example-profile"
	bedrockRegion     = "example-region-1"
	bedrockModel      = "example.model-v1:0"
	bedrockAccessKey  = "AKIAEXAMPLEACCESSKEY"
	bedrockSecretKey  = "example-secret-access-key"
	bedrockSession    = "example-session-token"
	bedrockCallerBody = `{"model":"caller-model","max_tokens":16,"messages":[]}`
	bedrockStreamBody = `{"model":"caller-model","stream":true,"max_tokens":16,"messages":[]}`
	bedrockStreamType = "application/vnd.amazon.eventstream"
	bedrockWantDate   = "20261124"
	bedrockWantStamp  = "20261124T150405Z"
	bedrockStreamPath = "/invoke-with-response-stream"
	bedrockInvokePath = "/invoke"
)

// bedrockClock is the synthetic clock the Bedrock tests give the gateway. It reads one fixed instant, so the signing
// date and time are known in advance, and it never waits.
type bedrockClock struct{}

// Now returns the fixed instant of 2026-11-24 15:04:05 UTC.
func (bedrockClock) Now() time.Time {
	return time.Date(2026, time.November, 24, 15, 4, 5, 0, time.UTC)
}

// After returns a channel that never fires, since the Bedrock tests never run the gateway's re-read loop.
func (bedrockClock) After(time.Duration) <-chan time.Time {
	return make(chan time.Time)
}

// bedrockCredentials exports the invented key pair and session token of bedrockProfile.
func bedrockCredentials(context.Context, string) (awssig.Credentials, error) {
	return awssig.Credentials{
		AccessKeyID:     bedrockAccessKey,
		SecretAccessKey: bedrockSecretKey,
		SessionToken:    bedrockSession,
	}, nil
}

// bedrockNoCredentials is a credential source whose export fails.
func bedrockNoCredentials(context.Context, string) (awssig.Credentials, error) {
	return awssig.Credentials{}, errors.New("example credential export failure")
}

// bedrockRegistry is the route registry the Bedrock tests serve. Its default context sends every request through the
// sigv4 seat bedrock, which sends to bedrockModel in bedrockRegion, signing as bedrockProfile.
func bedrockRegistry() llmroutes.Registry {
	return llmroutes.Registry{
		DefaultContext: "main",
		Seats: []llmroutes.Seat{{
			ID: "bedrock", AuthMode: llmroutes.AuthSigV4, Model: bedrockModel,
			AWSProfile: bedrockProfile, AWSRegion: bedrockRegion,
			StripBetas: []string{"example-stripped-beta"},
		}},
		Contexts: []llmroutes.Context{{ID: "main", DefaultSeat: "bedrock"}},
	}
}

// newBedrockGateway returns a loaded Gateway serving bedrockRegistry. Its Bedrock endpoint is endpoint with the region
// appended, so a request shows which region the gateway sent it to, and it signs with creds at bedrockNow. Its log is
// written to logger.
func newBedrockGateway(
	t *testing.T,
	endpoint string,
	creds func(context.Context, string) (awssig.Credentials, error),
	logger *slog.Logger,
) *gateway.Gateway {
	t.Helper()

	gw, err := gateway.New(gateway.Config{
		Routes:            fakeRoutes{snapshot: store.RoutesSnapshot{Routes: bedrockRegistry()}},
		Ledger:            fakeRecorder{},
		Clock:             bedrockClock{},
		AnthropicUpstream: endpoint,
		Logger:            logger,
		CallerKeyFile:     writeKeyFile(t, exampleCallerKey),
		AWSCredentials:    creds,
		BedrockEndpoint:   func(region string) string { return endpoint + "/" + region },
	})
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	if err := gw.Load(t.Context()); err != nil {
		t.Fatalf("Load() = %v", err)
	}

	return gw
}

// postAsCaller sends one POST of body to path through gw. It presents the caller key and the caller's own credentials,
// then adds the extra headers, and returns what the gateway answered.
func postAsCaller(t *testing.T, gw *gateway.Gateway, path, body string, extra http.Header) *httptest.ResponseRecorder {
	t.Helper()

	req := newProxyRequest(t, http.MethodPost, path, body)
	req.Header.Set(patchbayKeyHeader, exampleCallerKey)
	req.Header.Set("Authorization", callerAuthorization)
	req.Header.Set("X-Api-Key", callerKeyHeader)
	for name, values := range extra {
		for _, value := range values {
			req.Header.Add(name, value)
		}
	}
	rec := httptest.NewRecorder()
	gw.ServeHTTP(rec, req)

	return rec
}

// hangUpURL returns the URL of a server that closes every connection as it arrives, so a request to it fails upstream
// at once. The server is never closed early, so its port is never freed for another test to take.
func hangUpURL(t *testing.T) string {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			t.Errorf("the hang-up server's response cannot be hijacked")
			return
		}
		conn, _, err := hijacker.Hijack()
		if err != nil {
			t.Errorf("hijack the hang-up server's connection: %v", err)
			return
		}
		if err := conn.Close(); err != nil {
			t.Errorf("close the hang-up server's connection: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	return server.URL
}

// streamReply returns a fake Bedrock handler that answers with frames as a streamed invocation is answered: as an
// event-stream, one frame after another.
func streamReply(t *testing.T, frames ...[]byte) http.HandlerFunc {
	t.Helper()

	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", bedrockStreamType)
		w.WriteHeader(http.StatusOK)
		for _, frame := range frames {
			if _, err := w.Write(frame); err != nil {
				t.Errorf("write the fake Bedrock's frame: %v", err)
			}
		}
	}
}

// stringHeaderType is the event-stream value type of a string header, the only type Bedrock's own headers use.
const stringHeaderType = 7

// encodeFrame frames one event-stream message the way Bedrock does: a prelude of the total and headers lengths with
// its CRC, the string headers in name order, the payload, and a CRC over the whole message. The 16 bytes of overhead
// are the prelude, its CRC and the message CRC. The decoder is tested against this framing rather than against itself.
func encodeFrame(headers map[string]string, payload []byte) []byte {
	block := make([]byte, 0, 32*len(headers))
	for _, name := range slices.Sorted(maps.Keys(headers)) {
		value := headers[name]
		block = append(block, byte(len(name)))
		block = append(block, name...)
		block = append(block, stringHeaderType)
		block = binary.BigEndian.AppendUint16(block, uint16(len(value)))
		block = append(block, value...)
	}

	total := 16 + len(block) + len(payload)
	frame := binary.BigEndian.AppendUint32(nil, uint32(total))
	frame = binary.BigEndian.AppendUint32(frame, uint32(len(block)))
	frame = binary.BigEndian.AppendUint32(frame, crc32.ChecksumIEEE(frame))
	frame = append(frame, block...)
	frame = append(frame, payload...)

	return binary.BigEndian.AppendUint32(frame, crc32.ChecksumIEEE(frame))
}

// chunkFrame returns one streamed chunk frame: the event JSON, base64 encoded inside the one-field envelope Bedrock
// wraps each event in.
func chunkFrame(event string) []byte {
	envelope := `{"bytes":"` + base64.StdEncoding.EncodeToString([]byte(event)) + `"}`

	return encodeFrame(map[string]string{
		":event-type":   "chunk",
		":message-type": "event",
	}, []byte(envelope))
}

// exceptionFrame returns one frame Bedrock sends when a streamed invocation fails with the named exception.
func exceptionFrame(name string) []byte {
	return encodeFrame(map[string]string{
		":exception-type": name,
		":message-type":   "exception",
	}, []byte(`{"message":"example"}`))
}

// bedrockPath is the path of a model invocation of bedrockModel in bedrockRegion, as the fake Bedrock sees it.
func bedrockPath(suffix string) string {
	return "/" + bedrockRegion + "/model/" + bedrockModel + suffix
}

func TestGatewayRoutesSigV4BedrockSignsRequest(t *testing.T) {
	t.Parallel()

	fake := newFakeAnthropic(t, replyWith(t, http.StatusOK))
	gw := newBedrockGateway(t, fake.url(), bedrockCredentials, discardLogger())

	rec := postAsCaller(t, gw, "/v1/messages", bedrockCallerBody, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200 for a sigv4 seat that answers", rec.Code)
	}

	got := onlyRequest(t, fake)
	if got.method != http.MethodPost || got.path != bedrockPath(bedrockInvokePath) {
		t.Errorf("upstream got %s %s; want POST %s", got.method, got.path, bedrockPath(bedrockInvokePath))
	}

	wantScope := "AWS4-HMAC-SHA256 Credential=" + bedrockAccessKey + "/" + bedrockWantDate + "/" + bedrockRegion +
		"/bedrock/aws4_request, SignedHeaders="
	auth := got.header.Get("Authorization")
	if !strings.HasPrefix(auth, wantScope) || !strings.Contains(auth, ", Signature=") {
		t.Errorf("upstream Authorization = %q; want a SigV4 signature over the seat's scope %q", auth, wantScope)
	}
	if v := got.header.Get("X-Amz-Date"); v != bedrockWantStamp {
		t.Errorf("upstream X-Amz-Date = %q; want the synthetic clock's %q", v, bedrockWantStamp)
	}
	if v := got.header.Get("X-Amz-Security-Token"); v != bedrockSession {
		t.Errorf("upstream X-Amz-Security-Token = %q; want the profile's session token", v)
	}

	for name, values := range got.header {
		for _, value := range values {
			if strings.Contains(value, bedrockSecretKey) || strings.Contains(value, callerAuthorization) {
				t.Errorf("upstream header %s carries a credential the caller or the profile must keep: %q", name, value)
			}
		}
	}
	if v := got.header.Get("X-Api-Key"); v != "" {
		t.Errorf("upstream X-Api-Key = %q; want the caller's credential removed", v)
	}
	if v := got.header.Get(patchbayKeyHeader); v != "" {
		t.Errorf("upstream got %s = %q; want the caller key removed", patchbayKeyHeader, v)
	}
}

func TestGatewayRoutesSigV4BedrockTranslatesBody(t *testing.T) {
	t.Parallel()

	fake := newFakeAnthropic(t, replyWith(t, http.StatusOK))
	gw := newBedrockGateway(t, fake.url(), bedrockCredentials, discardLogger())

	body := `{"model":"caller-model","stream":false,"max_tokens":16,` +
		`"messages":[{"role":"user","content":"example"}],` +
		`"anthropic_beta":["example-body-beta","example-stripped-beta"],"example_future_field":{"kept":true}}`
	header := http.Header{}
	header.Add("Anthropic-Beta", "example-header-beta, example-body-beta")
	header.Add("Anthropic-Beta", "example-stripped-beta,example-second-header-beta")

	rec := postAsCaller(t, gw, "/v1/messages", body, header)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200 for a sigv4 seat that answers", rec.Code)
	}

	got := onlyRequest(t, fake)
	const wantBody = `{"anthropic_version":"bedrock-2023-05-31",` +
		`"anthropic_beta":["example-body-beta","example-header-beta","example-second-header-beta"],` +
		`"max_tokens":16,"messages":[{"role":"user","content":"example"}],"example_future_field":{"kept":true}}`
	var sent, want map[string]any
	if err := json.Unmarshal(got.body, &sent); err != nil {
		t.Fatalf("upstream body %q is not JSON: %v", got.body, err)
	}
	if err := json.Unmarshal([]byte(wantBody), &want); err != nil {
		t.Fatalf("expected body is not JSON: %v", err)
	}
	if !reflect.DeepEqual(sent, want) {
		t.Errorf("upstream got body %s; want %s", got.body, wantBody)
	}
	if values := got.header.Values("Anthropic-Beta"); len(values) != 0 {
		t.Errorf("upstream got the Anthropic-Beta header %q; want the flags carried in the body only", values)
	}
}

func TestGatewayRoutesSigV4BedrockStreamTranslatedToSSE(t *testing.T) {
	t.Parallel()

	events := []string{
		`{"type":"message_start","message":{"id":"example-message"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"example"}}`,
		`{"type":"message_stop"}`,
	}
	fake := newFakeAnthropic(t, streamReply(t, chunkFrame(events[0]), chunkFrame(events[1]), chunkFrame(events[2])))
	gw := newBedrockGateway(t, fake.url(), bedrockCredentials, discardLogger())

	rec := postAsCaller(t, gw, "/v1/messages", bedrockStreamBody, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200 for a streamed answer", rec.Code)
	}

	got := onlyRequest(t, fake)
	if got.path != bedrockPath(bedrockStreamPath) {
		t.Errorf("upstream path = %q; want the streamed invocation %q", got.path, bedrockPath(bedrockStreamPath))
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("caller Content-Type = %q; want text/event-stream", ct)
	}
	want := "event: message_start\ndata: " + events[0] + "\n\n" +
		"event: content_block_delta\ndata: " + events[1] + "\n\n" +
		"event: message_stop\ndata: " + events[2] + "\n\n"
	if rec.Body.String() != want {
		t.Errorf("caller got body %q; want the Anthropic SSE %q", rec.Body.String(), want)
	}
}

func TestGatewayRoutesSigV4BedrockStreamRefusesMalformedEvents(t *testing.T) {
	t.Parallel()

	const (
		errorTypeEvent = "event: error\ndata: "
		endOfEvent     = "\n\n"
		apiError       = `{"type":"error","error":{"type":"api_error","message":`
	)
	tests := []struct {
		name  string
		frame []byte
		want  string
	}{
		{
			name:  "exception",
			frame: exceptionFrame("throttlingException"),
			want: errorTypeEvent + apiError +
				`"gateway: the Bedrock stream failed with throttlingException"}}` + endOfEvent,
		},
		{
			name:  "event name with a line break",
			frame: chunkFrame(`{"type":"message_start\r\nevent: forged"}`),
			want: errorTypeEvent + apiError +
				`"gateway: Bedrock named an event the gateway will not render"}}` + endOfEvent,
		},
		{
			name:  "event that is not JSON",
			frame: chunkFrame(`not json`),
			want: errorTypeEvent + apiError +
				`"gateway: Bedrock sent an event the gateway could not decode"}}` + endOfEvent,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// A valid event follows the bad one, so the check is that the stream ends at the bad event.
			fake := newFakeAnthropic(t, streamReply(t, tc.frame, chunkFrame(`{"type":"message_stop"}`)))
			gw := newBedrockGateway(t, fake.url(), bedrockCredentials, discardLogger())

			rec := postAsCaller(t, gw, "/v1/messages", bedrockStreamBody, nil)

			if rec.Body.String() != tc.want {
				t.Errorf("caller got body %q; want only the gateway's error event %q", rec.Body.String(), tc.want)
			}
		})
	}
}

func TestGatewayRoutesSigV4BedrockNonStreamRelayed(t *testing.T) {
	t.Parallel()

	fake := newFakeAnthropic(t, replyWith(t, http.StatusOK))
	gw := newBedrockGateway(t, fake.url(), bedrockCredentials, discardLogger())

	rec := postAsCaller(t, gw, "/v1/messages", bedrockCallerBody, nil)

	got := onlyRequest(t, fake)
	if got.path != bedrockPath(bedrockInvokePath) {
		t.Errorf("upstream path = %q; want the single invocation %q", got.path, bedrockPath(bedrockInvokePath))
	}
	if rec.Code != http.StatusOK || rec.Body.String() != upstreamReply {
		t.Errorf("caller got %d %q; want the Bedrock answer relayed as it arrived", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("caller Content-Type = %q; want application/json", ct)
	}
}

func TestGatewayRoutesSigV4BedrockRefusesRequests(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		path    string
		body    string
		status  int
		errType string
	}{
		{
			name: "count_tokens", path: "/v1/messages/count_tokens", body: bedrockCallerBody,
			status: http.StatusNotImplemented, errType: "api_error",
		},
		{
			name: "other path", path: "/v1/models", body: bedrockCallerBody,
			status: http.StatusNotFound, errType: "not_found_error",
		},
		{
			name: "body that is not an object", path: "/v1/messages", body: `[1,2]`,
			status: http.StatusBadRequest, errType: "invalid_request_error",
		},
		{
			name: "beta list that is not strings", path: "/v1/messages",
			body:   `{"model":"caller-model","anthropic_beta":5,"messages":[]}`,
			status: http.StatusBadRequest, errType: "invalid_request_error",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fake := newFakeAnthropic(t, replyWith(t, http.StatusOK))
			gw := newBedrockGateway(t, fake.url(), bedrockCredentials, discardLogger())

			rec := postAsCaller(t, gw, tc.path, tc.body, nil)

			if rec.Code != tc.status {
				t.Fatalf("status = %d; want %d", rec.Code, tc.status)
			}
			if got := errorTypeOf(t, rec.Body.Bytes()); got != tc.errType {
				t.Errorf("error type = %q; want %s", got, tc.errType)
			}
			requireNoUpstream(t, fake)
		})
	}
}

func TestGatewayRoutesSigV4BedrockRefusesWithoutCredentials(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		creds func(context.Context, string) (awssig.Credentials, error)
	}{
		{name: "no credential source", creds: nil},
		{name: "credential export fails", creds: bedrockNoCredentials},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fake := newFakeAnthropic(t, replyWith(t, http.StatusOK))
			gw := newBedrockGateway(t, fake.url(), tc.creds, discardLogger())

			rec := postAsCaller(t, gw, "/v1/messages", bedrockCallerBody, nil)

			if rec.Code != http.StatusBadGateway {
				t.Fatalf("status = %d; want 502 when the seat has no credentials to sign with", rec.Code)
			}
			if got := errorTypeOf(t, rec.Body.Bytes()); got != "api_error" {
				t.Errorf("error type = %q; want api_error", got)
			}
			requireNoUpstream(t, fake)
		})
	}
}
