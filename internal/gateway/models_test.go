package gateway_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/joshsymonds/orrery/internal/gateway"
	"github.com/joshsymonds/orrery/internal/llmroutes"
	"github.com/joshsymonds/orrery/internal/store"
)

// The model tests serve invented seats, selectors, tokens and hosts. The upstream is a fake Anthropic on the loopback
// interface, or, where a test expects none to be reached, a host that no dial reaches.
const (
	// modelsInventedUpstream is the upstream a test names when it starts no fake. The unreachable client it is paired
	// with never dials it, so the host is only a name.
	modelsInventedUpstream = "https://api.anthropic.example.test"

	// modelsTeamAccount and modelsTeamToken are the account the team context names and the token the registry holds
	// for it.
	modelsTeamAccount = "team-account"
	modelsTeamToken   = "example-team-token"

	// modelsEpoch is the created_at every entry the gateway describes itself carries.
	modelsEpoch = "1970-01-01T00:00:00Z"

	// modelsListPath is the path the model listing answers, and modelsModelPath the prefix of the path one model
	// answers, both beneath no context prefix.
	modelsListPath  = "/v1/models"
	modelsModelPath = "/v1/models/"

	// modelsStubPage is the first page of an upstream listing that has more to follow. Two of its entries are shadowed
	// by the personal context's bindings, and the last is not.
	modelsStubPage = `{"data":[{"type":"model","id":"claude-opus-5"},{"type":"model","id":"claude-haiku-5"}],` +
		`"has_more":true,"last_id":"claude-haiku-5"}`

	// modelsUpstreamOne is a one-entry upstream listing whose entry carries a created_at the gateway does not invent.
	modelsUpstreamOne = `{"data":[{"type":"model","id":"upstream-model","display_name":"upstream-model",` +
		`"created_at":"2026-01-01T00:00:00Z"}],"has_more":false,"first_id":"upstream-model","last_id":"upstream-model"}`

	// modelsMapBody is the model map a sigv4 seat reads. Its values are Bedrock ids, which must never be listed.
	modelsMapBody = `{"claude-sonnet-5":"arn:example-sonnet","claude-haiku-5":"arn:example-haiku","empty":""}`
)

// modelsRegistry is the route registry the model tests serve. Its contexts are personal, which forwards by default with
// the caller's own login and binds two selectors; inject, whose default seat injects a key and so never reaches
// Anthropic; aws, whose default seat signs with sigv4 and lists its model map's keys; and team, which forwards through
// the account modelsTeamAccount. defaultContext is the context a bare path resolves to.
func modelsRegistry(defaultContext string) llmroutes.Registry {
	return llmroutes.Registry{
		DefaultContext: defaultContext,
		Seats: []llmroutes.Seat{
			{ID: "anthropic", AuthMode: llmroutes.AuthForward, Upstream: llmroutes.AnthropicUpstream},
			{
				ID: "luna", AuthMode: llmroutes.AuthInject, Upstream: "https://openrouter.example.test/api",
				Model: "openai/gpt-6-luna", APIKeyEnvFile: "EXAMPLE_LUNA_KEY_FILE", MaxInputTokens: 262144,
			},
			{
				ID:       "pin",
				AuthMode: llmroutes.AuthForward,
				Upstream: llmroutes.AnthropicUpstream,
				Model:    "claude-sonnet-5",
			},
			{
				ID: "bedrock", AuthMode: llmroutes.AuthSigV4, AWSProfile: "example-profile",
				AWSRegion: "example-region-1", ModelMapEnvFile: "EXAMPLE_MODEL_MAP",
			},
			{
				ID: "sub", AuthMode: llmroutes.AuthInject, Upstream: "https://openrouter.example.test/api",
				Model: "openrouter/worker", APIKeyEnvFile: "EXAMPLE_LUNA_KEY_FILE",
			},
		},
		Contexts: []llmroutes.Context{
			{
				ID: "personal", DefaultSeat: "anthropic",
				Models: map[string]string{"luna-fast": "luna", "claude-opus-5": "pin"},
				Subagents: &llmroutes.Subagents{
					DefaultSeat: "sub",
					Models:      map[string]string{"requested-worker": "sub"},
				},
			},
			{ID: "inject", DefaultSeat: "luna", Models: map[string]string{"public-luna": "luna"}},
			{ID: "aws", DefaultSeat: "bedrock", Models: map[string]string{"public-bedrock": "bedrock"}},
			{
				ID: "team", DefaultSeat: "anthropic", Account: modelsTeamAccount,
				Models: map[string]string{"team-opus": "pin"},
			},
		},
	}
}

// modelsSetup is what a model test builds its gateway from. A zero field serves the default described beside it.
type modelsSetup struct {
	// defaultContext is the context a bare path resolves to. Empty means personal.
	defaultContext string

	// tokens are the account tokens the registry stores.
	tokens map[string]string

	// fake, when set, is the upstream: its URL becomes Config.AnthropicUpstream, and it is reached over the loopback
	// interface. When it is nil the upstream is never reached.
	fake *fakeAnthropic

	// client, when set, is the client the gateway calls upstream with. Empty means the loopback client when a fake is
	// set, and a client whose every dial fails otherwise.
	client *http.Client

	// callerKeyFile is the path Config.CallerKeyFile names. Empty means no caller key.
	callerKeyFile string

	// env is what Config.Getenv returns for each variable name.
	env map[string]string

	// logger receives the gateway's warnings. Nil means a logger that discards them.
	logger *slog.Logger

	// unloaded leaves the gateway without a route registry read, so every request is answered 503.
	unloaded bool
}

// newModelsGateway returns a gateway built from setup, with the route registry loaded unless setup says otherwise.
func newModelsGateway(t *testing.T, setup modelsSetup) *gateway.Gateway {
	t.Helper()

	upstream := modelsInventedUpstream
	client := setup.client
	if setup.fake != nil {
		upstream = setup.fake.url()
		if client == nil {
			client = &http.Client{Transport: http.DefaultTransport}
		}
	} else if client == nil {
		client = unreachableModelsClient()
	}

	defaultContext := setup.defaultContext
	if defaultContext == "" {
		defaultContext = "personal"
	}

	logger := setup.logger
	if logger == nil {
		logger = discardLogger()
	}

	gw, err := gateway.New(gateway.Config{
		Routes: fakeRoutes{snapshot: store.RoutesSnapshot{
			Routes: modelsRegistry(defaultContext),
			Tokens: setup.tokens,
		}},
		Ledger:            fakeRecorder{},
		Logger:            logger,
		Client:            client,
		AnthropicUpstream: upstream,
		CallerKeyFile:     setup.callerKeyFile,
		Getenv:            func(name string) string { return setup.env[name] },
	})
	if err != nil {
		t.Fatalf("New() = %v", err)
	}

	if setup.unloaded {
		return gw
	}

	if err := gw.Load(t.Context()); err != nil {
		t.Fatalf("Load() = %v", err)
	}

	return gw
}

// errModelsUnreachable is the failure unreachableModelsClient's dialer answers with. It is named here so that "the
// upstream is unreachable" is a fact of the test rather than of whatever the machine has listening.
var errModelsUnreachable = errors.New("upstream unreachable")

// unreachableModelsClient returns a client whose every dial fails at once, which produces "the upstream is unreachable"
// without a socket, a timeout, or a network.
func unreachableModelsClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(context.Context, string, string) (net.Conn, error) {
				return nil, errModelsUnreachable
			},
		},
	}
}

// modelsHostRecorder is a transport that records the host of every request it carries, then hands the request to the
// transport it wraps.
type modelsHostRecorder struct {
	inner http.RoundTripper
	mu    sync.Mutex
	hosts []string
}

// RoundTrip records the request's host and forwards the request.
func (r *modelsHostRecorder) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	r.hosts = append(r.hosts, req.URL.Host)
	r.mu.Unlock()

	return r.inner.RoundTrip(req)
}

// seenHosts returns the host of every request the recorder carried, in arrival order.
func (r *modelsHostRecorder) seenHosts() []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	return slices.Clone(r.hosts)
}

// modelsReply returns a fake Anthropic handler that answers every request with status and body, as JSON.
func modelsReply(t *testing.T, status int, body string) http.HandlerFunc {
	t.Helper()

	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)

		if _, err := io.WriteString(w, body); err != nil {
			t.Errorf("write the fake Anthropic's reply: %v", err)
		}
	}
}

// serveModelsRequest sends one GET for target through gw's handler, carrying header, and returns what it answered.
func serveModelsRequest(
	t *testing.T,
	gw *gateway.Gateway,
	target string,
	header http.Header,
) *httptest.ResponseRecorder {
	t.Helper()

	req := newProxyRequest(t, http.MethodGet, target, "")
	for name, values := range header {
		for _, value := range values {
			req.Header.Add(name, value)
		}
	}

	rec := httptest.NewRecorder()
	gw.Handler().ServeHTTP(rec, req)

	return rec
}

// writeModelsFile writes contents to a file in a temporary directory and returns the file's path.
func writeModelsFile(t *testing.T, contents string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "models.json")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write the model map: %v", err)
	}

	return path
}

// modelsListing is a decoded /v1/models envelope, with each entry left as the bytes it was written as, so that an
// upstream field is compared exactly.
type modelsListing struct {
	Data    []json.RawMessage
	HasMore bool
	FirstID *string
	LastID  *string
}

// decodeModelsListing decodes a /v1/models response body, member by member, so that each member is read as its type.
func decodeModelsListing(t *testing.T, body []byte) modelsListing {
	t.Helper()

	var members map[string]json.RawMessage
	if err := json.Unmarshal(body, &members); err != nil {
		t.Fatalf("decode the model listing %q: %v", body, err)
	}

	var list modelsListing
	for name, target := range map[string]any{
		"data":     &list.Data,
		"has_more": &list.HasMore,
		"first_id": &list.FirstID,
		"last_id":  &list.LastID,
	} {
		raw, ok := members[name]
		if !ok {
			continue
		}
		if err := json.Unmarshal(raw, target); err != nil {
			t.Fatalf("decode the model listing's %s from %q: %v", name, body, err)
		}
	}

	return list
}

// modelsIDs returns the id of every entry of a decoded listing, in order.
func modelsIDs(t *testing.T, list modelsListing) []string {
	t.Helper()

	found := make([]string, 0, len(list.Data))
	for _, raw := range list.Data {
		var entry struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(raw, &entry); err != nil {
			t.Fatalf("decode model entry %q: %v", raw, err)
		}

		found = append(found, entry.ID)
	}

	return found
}

// modelFields decodes one model entry into a plain map, so that a test asserts every field the entry carries.
func modelFields(t *testing.T, raw []byte) map[string]any {
	t.Helper()

	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("decode model entry %q: %v", raw, err)
	}

	return fields
}

// cursorOf renders a pagination cursor for a failure message, naming an absent one rather than printing a pointer.
func cursorOf(cursor *string) string {
	if cursor == nil {
		return "<absent>"
	}

	return *cursor
}

// requireModelIDs fails the test unless the listing carries exactly want, in order.
func requireModelIDs(t *testing.T, list modelsListing, want []string) {
	t.Helper()

	if got := modelsIDs(t, list); !slices.Equal(got, want) {
		t.Fatalf("ids = %v; want %v", got, want)
	}
}

// TestGatewayModelsListMergesBindingsAndUpstream pins the listing a forward context answers: its public bindings in
// selector order, then the upstream's entries no binding shadows, with the upstream's own has_more. Nothing private
// about the registry is published.
func TestGatewayModelsListMergesBindingsAndUpstream(t *testing.T) {
	t.Parallel()

	fake := newFakeAnthropic(t, modelsReply(t, http.StatusOK, modelsStubPage))
	gw := newModelsGateway(t, modelsSetup{fake: fake})

	rec := serveModelsRequest(t, gw, "/v1/models", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rec.Code)
	}

	list := decodeModelsListing(t, rec.Body.Bytes())
	requireModelIDs(t, list, []string{"claude-opus-5", "luna-fast", "claude-haiku-5"})

	if !list.HasMore || cursorOf(list.LastID) != "claude-haiku-5" {
		t.Fatalf("has_more = %t, last_id = %s; want the upstream's own true and claude-haiku-5",
			list.HasMore, cursorOf(list.LastID))
	}

	body := rec.Body.String()
	for _, private := range []string{"anthropic", "luna", "pin", "bedrock", "sub", "requested-worker", "openrouter/worker"} {
		if strings.Contains(body, `"id":"`+private+`"`) {
			t.Errorf("private id %q exposed in the listing", private)
		}
	}
}

// TestGatewayModelsListShadowsUpstreamOnLaterPages pins a later page. It repeats no binding, but a public selector still
// shadows an upstream id that collides with it, because the binding is what the gateway serves for that name.
func TestGatewayModelsListShadowsUpstreamOnLaterPages(t *testing.T) {
	t.Parallel()

	fake := newFakeAnthropic(t, modelsReply(t, http.StatusOK,
		`{"data":[{"type":"model","id":"next"},{"type":"model","id":"luna-fast"},`+
			`{"type":"model","id":"claude-opus-5"}],"has_more":false,"first_id":"next","last_id":"claude-opus-5"}`))
	gw := newModelsGateway(t, modelsSetup{fake: fake})

	rec := serveModelsRequest(t, gw, "/v1/models?after_id=claude-haiku-5", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rec.Code)
	}

	requireModelIDs(t, decodeModelsListing(t, rec.Body.Bytes()), []string{"next"})
}

// TestGatewayModelsReversePageKeepsBindingsOffAndCursorsIntact pins the backward page. A before_id request is as
// cursor-qualified as an after_id one, so the bindings do not reappear, shadowing still applies, and both of the
// upstream's cursors are kept whether or not more pages lie further back.
func TestGatewayModelsReversePageKeepsBindingsOffAndCursorsIntact(t *testing.T) {
	t.Parallel()

	const entries = `{"data":[{"type":"model","id":"luna-fast"},{"type":"model","id":"claude-sonnet-4"},` +
		`{"type":"model","id":"claude-haiku-4"},{"type":"model","id":"claude-opus-5"}],"has_more":`

	for _, tc := range []struct {
		name string
		body string
		more bool
	}{
		{name: "more pages lie further back", body: entries + `true,"first_id":"luna-fast","last_id":"claude-opus-5"}`, more: true},
		{name: "a terminal reverse page", body: entries + `false,"first_id":"luna-fast","last_id":"claude-opus-5"}`, more: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fake := newFakeAnthropic(t, modelsReply(t, http.StatusOK, tc.body))
			gw := newModelsGateway(t, modelsSetup{fake: fake})

			rec := serveModelsRequest(t, gw, "/v1/models?before_id=claude-sonnet-5", nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d; want 200", rec.Code)
			}

			list := decodeModelsListing(t, rec.Body.Bytes())
			requireModelIDs(t, list, []string{"claude-sonnet-4", "claude-haiku-4"})

			if list.HasMore != tc.more {
				t.Errorf("has_more = %t; want the upstream's own %t", list.HasMore, tc.more)
			}
			if got := cursorOf(list.FirstID); got != "luna-fast" {
				t.Errorf("first_id = %s; want the upstream's reverse cursor luna-fast", got)
			}
			if got := cursorOf(list.LastID); got != "claude-opus-5" {
				t.Errorf("last_id = %s; want the upstream's forward cursor claude-opus-5", got)
			}
			if strings.Contains(rec.Body.String(), `"created_at":"`+modelsEpoch+`"`) {
				t.Errorf("a binding entry rode a reverse page: %s", rec.Body)
			}
		})
	}
}

// TestGatewayModelsListEntryShape pins the exact fields of each entry in a mixed listing. A binding carries its seat's
// input ceiling when it sets one and omits it otherwise, and an upstream entry keeps every field it arrived with.
func TestGatewayModelsListEntryShape(t *testing.T) {
	t.Parallel()

	fake := newFakeAnthropic(t, modelsReply(t, http.StatusOK, modelsUpstreamOne))
	gw := newModelsGateway(t, modelsSetup{fake: fake})

	rec := serveModelsRequest(t, gw, "/v1/models", nil)
	list := decodeModelsListing(t, rec.Body.Bytes())
	if len(list.Data) != 3 {
		t.Fatalf("entries = %v; want the 2 bindings and the upstream model", modelsIDs(t, list))
	}

	for _, tc := range []struct {
		name  string
		index int
		want  map[string]any
	}{
		{
			name:  "a binding whose seat sets no ceiling",
			index: 0,
			want: map[string]any{
				"type": "model", "id": "claude-opus-5", "display_name": "claude-opus-5", "created_at": modelsEpoch,
			},
		},
		{
			name:  "a bound selector with its seat's stored input ceiling",
			index: 1,
			want: map[string]any{
				"type": "model", "id": "luna-fast", "display_name": "luna-fast", "created_at": modelsEpoch,
				"max_input_tokens": float64(262144),
			},
		},
		{
			name:  "an upstream entry, kept with every field it arrived with",
			index: 2,
			want: map[string]any{
				"type": "model", "id": "upstream-model", "display_name": "upstream-model",
				"created_at": "2026-01-01T00:00:00Z",
			},
		},
	} {
		if got := modelFields(t, list.Data[tc.index]); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: entry %d = %v; want exactly %v", tc.name, tc.index, got, tc.want)
		}
	}
}

// TestGatewayModelsListRelaysEntriesItCannotRead keeps an upstream entry the merge cannot read an id from. It has no id a
// binding could shadow, so it is listed as it arrived rather than dropped.
func TestGatewayModelsListRelaysEntriesItCannotRead(t *testing.T) {
	t.Parallel()

	fake := newFakeAnthropic(t, modelsReply(t, http.StatusOK,
		`{"data":["a bare string",{"no_id":true},{"type":"model","id":"real"}],"has_more":false}`))
	gw := newModelsGateway(t, modelsSetup{fake: fake})

	rec := serveModelsRequest(t, gw, "/v1/models", nil)
	list := decodeModelsListing(t, rec.Body.Bytes())
	if len(list.Data) != 5 {
		t.Fatalf("entries = %d; want the 2 bindings and all 3 upstream entries", len(list.Data))
	}
	if string(list.Data[2]) != `"a bare string"` || string(list.Data[3]) != `{"no_id":true}` {
		t.Fatalf("unreadable entries were not relayed verbatim: %s", rec.Body)
	}
}

// TestGatewayModelsListDegradesWithoutFailing pins the half that can fail. Every way the upstream can let the gateway
// down costs the upstream entries and nothing else: the caller still gets the context's own bindings with 200, the
// failure is logged, and the merged envelope's cursors are the merged listing's own ends.
func TestGatewayModelsListDegradesWithoutFailing(t *testing.T) {
	t.Parallel()

	oversized := `{"data":[{"type":"model","id":"` + strings.Repeat("a", 4<<20) + `"}]}`
	for _, tc := range []struct {
		name   string
		dead   bool
		status int
		body   string
	}{
		{name: "unreachable", dead: true},
		{name: "non 2xx", status: http.StatusUnauthorized, body: `{"error":"nope"}`},
		{name: "server error", status: http.StatusInternalServerError, body: `{}`},
		{name: "not a listing", status: http.StatusOK, body: `["not","a","listing"]`},
		{name: "not json at all", status: http.StatusOK, body: `<html>nope</html>`},
		{name: "oversized", status: http.StatusOK, body: oversized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var logs bytes.Buffer
			setup := modelsSetup{logger: slog.New(slog.NewTextHandler(&logs, nil))}
			if !tc.dead {
				setup.fake = newFakeAnthropic(t, modelsReply(t, tc.status, tc.body))
			}
			gw := newModelsGateway(t, setup)

			rec := serveModelsRequest(t, gw, "/v1/models", nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d; want 200 with the local bindings", rec.Code)
			}

			list := decodeModelsListing(t, rec.Body.Bytes())
			want := []string{"claude-opus-5", "luna-fast"}
			requireModelIDs(t, list, want)

			if list.HasMore {
				t.Error("has_more = true; want false with no upstream listing to page")
			}
			if got := cursorOf(list.FirstID); got != want[0] {
				t.Errorf("first_id = %s; want the merged first id %s", got, want[0])
			}
			if got := cursorOf(list.LastID); got != want[len(want)-1] {
				t.Errorf("last_id = %s; want the merged final id %s", got, want[len(want)-1])
			}
			if !strings.Contains(logs.String(), "upstream model list unavailable") {
				t.Errorf("the degraded listing was not logged: %q", logs.String())
			}
		})
	}
}

// TestGatewayModelsFetchCarriesOnlyTheCallersAllowedHeaders pins what crosses when the gateway asks Anthropic on the
// caller's behalf. The credentials that pay for the answer and the version flags that shape it travel, to the upstream
// the gateway was configured with and nowhere else. Anything else the caller sent stays home, and so does the caller
// key, which is the gateway's own.
func TestGatewayModelsFetchCarriesOnlyTheCallersAllowedHeaders(t *testing.T) {
	t.Parallel()

	fake := newFakeAnthropic(t, modelsReply(t, http.StatusOK, modelsUpstreamOne))
	gw := newModelsGateway(t, modelsSetup{fake: fake})

	header := http.Header{}
	header.Set("Authorization", callerAuthorization)
	header.Set("X-Api-Key", callerKeyHeader)
	header.Set("Anthropic-Version", "2023-06-01")
	header.Add("Anthropic-Beta", "one-2026-01-01")
	header.Add("Anthropic-Beta", "two-2026-01-01")
	header.Set("X-Something-Else", "not forwarded")
	header.Set("Cookie", "session=nope")
	header.Set(patchbayKeyHeader, exampleCallerKey)

	rec := serveModelsRequest(t, gw, "/v1/models?limit=3&after_id=model_x", header)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rec.Code)
	}

	got := onlyRequest(t, fake)
	if got.method != http.MethodGet || got.path != modelsListPath {
		t.Fatalf("upstream call = %s %s; want GET %s", got.method, got.path, modelsListPath)
	}
	if got.query != "limit=3&after_id=model_x" {
		t.Fatalf("upstream query = %q; want the caller's own", got.query)
	}

	for _, want := range []struct {
		name   string
		values []string
	}{
		{name: "Authorization", values: []string{callerAuthorization}},
		{name: "X-Api-Key", values: []string{callerKeyHeader}},
		{name: "Anthropic-Version", values: []string{"2023-06-01"}},
		{name: "Anthropic-Beta", values: []string{"one-2026-01-01", "two-2026-01-01"}},
	} {
		if values := got.header.Values(want.name); !slices.Equal(values, want.values) {
			t.Errorf("upstream %s = %v; want the caller's %v", want.name, values, want.values)
		}
	}

	for _, dropped := range []string{"X-Something-Else", "Cookie", patchbayKeyHeader} {
		if values := got.header.Values(dropped); len(values) != 0 {
			t.Errorf("upstream %s = %v; want it dropped", dropped, values)
		}
	}
}

// TestGatewayModelsSingleBoundSelectorCarriesMaxInputTokens pins GET /v1/models/{id} for a public binding. The binding
// is described from the registry: its seat's stored input ceiling is carried, and the upstream is never asked.
func TestGatewayModelsSingleBoundSelectorCarriesMaxInputTokens(t *testing.T) {
	t.Parallel()

	fake := newFakeAnthropic(t, modelsReply(t, http.StatusOK, modelsUpstreamOne))
	gw := newModelsGateway(t, modelsSetup{fake: fake})

	rec := serveModelsRequest(t, gw, modelsModelPath+"luna-fast", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200 (body %s)", rec.Code, rec.Body)
	}

	want := map[string]any{
		"type": "model", "id": "luna-fast", "display_name": "luna-fast", "created_at": modelsEpoch,
		"max_input_tokens": float64(262144),
	}
	if got := modelFields(t, rec.Body.Bytes()); !reflect.DeepEqual(got, want) {
		t.Fatalf("entry = %v; want exactly %v", got, want)
	}
	requireNoUpstream(t, fake)
}

// TestGatewayModelsSingleModelRelaysUpstreamAnswer pins the single-model relay for a forward context: the upstream's
// status, body and content type reach the caller untouched, because the upstream's answer about its own models is the
// honest one.
func TestGatewayModelsSingleModelRelaysUpstreamAnswer(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name        string
		status      int
		contentType string
		body        string
		wantType    string
	}{
		{
			name: "found", status: http.StatusOK, contentType: "application/json",
			body: `{"type":"model","id":"claude-haiku-5"}`, wantType: "application/json",
		},
		{
			name: "not found", status: http.StatusNotFound, contentType: "application/json",
			body: `{"type":"error"}`, wantType: "application/json",
		},
		{
			name: "refused credential", status: http.StatusUnauthorized, contentType: "application/json",
			body: `{"type":"error","error":{"type":"authentication_error"}}`, wantType: "application/json",
		},
		{
			name: "no content type defaults to json", status: http.StatusOK,
			body: `{"type":"model","id":"claude-haiku-5"}`, wantType: "application/json",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fake := newFakeAnthropic(t, func(w http.ResponseWriter, _ *http.Request) {
				if tc.contentType == "" {
					// Go sniffs a content type for a handler that sets none, so the header is suppressed explicitly to
					// produce an upstream that genuinely declares nothing.
					w.Header()["Content-Type"] = nil
				} else {
					w.Header().Set("Content-Type", tc.contentType)
				}
				w.WriteHeader(tc.status)

				if _, err := io.WriteString(w, tc.body); err != nil {
					t.Errorf("write the fake Anthropic's reply: %v", err)
				}
			})
			gw := newModelsGateway(t, modelsSetup{fake: fake})

			rec := serveModelsRequest(t, gw, modelsModelPath+"claude-haiku-5", nil)
			if rec.Code != tc.status {
				t.Fatalf("status = %d; want the upstream's %d", rec.Code, tc.status)
			}
			if rec.Body.String() != tc.body {
				t.Fatalf("body = %q; want the upstream's %q", rec.Body.String(), tc.body)
			}
			if got := rec.Header().Get("Content-Type"); got != tc.wantType {
				t.Fatalf("content type = %q; want %q", got, tc.wantType)
			}
		})
	}
}

// TestGatewayModelsSingleModelFailureAnswers502 pins the two failures the single-model relay answers for itself. The
// answer is Anthropic's error shape, marked as the gateway's own by its fixed message, and it carries no detail of the
// caller's credentials or the gateway's insides.
func TestGatewayModelsSingleModelFailureAnswers502(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		dead     bool
		body     string
		wantBody string
	}{
		{
			name:     "unreachable",
			dead:     true,
			wantBody: `{"type":"error","error":{"type":"api_error","message":"gateway: the upstream request failed"}}`,
		},
		{
			name: "oversized",
			body: `{"type":"model","id":"` + strings.Repeat("a", 4<<20) + `"}`,
			wantBody: `{"type":"error","error":{"type":"api_error",` +
				`"message":"gateway: the upstream response exceeds the size the gateway holds"}}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			setup := modelsSetup{}
			if !tc.dead {
				setup.fake = newFakeAnthropic(t, modelsReply(t, http.StatusOK, tc.body))
			}
			gw := newModelsGateway(t, setup)

			rec := serveModelsRequest(t, gw, modelsModelPath+"claude-haiku-5", nil)
			if rec.Code != http.StatusBadGateway {
				t.Fatalf("status = %d; want 502 (body %s)", rec.Code, rec.Body)
			}
			if got := rec.Header().Get("Content-Type"); got != "application/json" {
				t.Errorf("content type = %q; want application/json", got)
			}
			if got := rec.Body.String(); got != tc.wantBody {
				t.Errorf("body = %s\nwant exactly = %s", got, tc.wantBody)
			}
		})
	}
}

// TestGatewayModelsSynthesizedContextNeverAsksAnthropic pins a context whose default seat injects a key. Anthropic has
// nothing to say about a context that never reaches it, so the listing and the single model are answered from the
// registry alone: its bindings, and the one model its default seat rewrites to.
func TestGatewayModelsSynthesizedContextNeverAsksAnthropic(t *testing.T) {
	t.Parallel()

	fake := newFakeAnthropic(t, modelsReply(t, http.StatusOK, modelsUpstreamOne))
	gw := newModelsGateway(t, modelsSetup{fake: fake, defaultContext: "inject"})

	t.Run("listing", func(t *testing.T) {
		t.Parallel()

		rec := serveModelsRequest(t, gw, "/v1/models", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d; want 200", rec.Code)
		}

		list := decodeModelsListing(t, rec.Body.Bytes())
		requireModelIDs(t, list, []string{"public-luna", "openai/gpt-6-luna"})

		want := []map[string]any{
			{"type": "model", "id": "public-luna", "display_name": "public-luna", "created_at": modelsEpoch,
				"max_input_tokens": float64(262144)},
			{"type": "model", "id": "openai/gpt-6-luna", "display_name": "openai/gpt-6-luna", "created_at": modelsEpoch,
				"max_input_tokens": float64(262144)},
		}
		for i, entry := range want {
			if got := modelFields(t, list.Data[i]); !reflect.DeepEqual(got, entry) {
				t.Errorf("entry %d = %v; want exactly %v", i, got, entry)
			}
		}
		requireNoUpstream(t, fake)
	})

	t.Run("single model", func(t *testing.T) {
		t.Parallel()

		for _, tc := range []struct {
			id   string
			want int
		}{
			{id: "public-luna", want: http.StatusOK},
			{id: "openai%2Fgpt-6-luna", want: http.StatusOK},
			{id: "requested-worker", want: http.StatusNotFound},
			{id: "luna", want: http.StatusNotFound},
		} {
			rec := serveModelsRequest(t, gw, modelsModelPath+tc.id, nil)
			if rec.Code != tc.want {
				t.Errorf("%s status = %d; want %d (body %s)", tc.id, rec.Code, tc.want, rec.Body)
			}
		}
		requireNoUpstream(t, fake)
	})
}

// TestGatewayModelsSigV4ListingNamesMapKeysNotTargets pins the sigv4 listing. A sigv4 context lists the keys of its model
// map and never the Bedrock ids the map names, and a context that never reaches Anthropic asks it nothing.
func TestGatewayModelsSigV4ListingNamesMapKeysNotTargets(t *testing.T) {
	t.Parallel()

	fake := newFakeAnthropic(t, modelsReply(t, http.StatusOK, modelsUpstreamOne))
	gw := newModelsGateway(t, modelsSetup{
		fake:           fake,
		defaultContext: "aws",
		env:            map[string]string{"EXAMPLE_MODEL_MAP": writeModelsFile(t, modelsMapBody)},
	})

	rec := serveModelsRequest(t, gw, "/v1/models", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rec.Code)
	}

	requireModelIDs(t, decodeModelsListing(t, rec.Body.Bytes()),
		[]string{"public-bedrock", "claude-haiku-5", "claude-sonnet-5"})

	if strings.Contains(rec.Body.String(), "arn:") || strings.Contains(rec.Body.String(), "requested-worker") {
		t.Fatalf("private model data exposed: %s", rec.Body)
	}
	requireNoUpstream(t, fake)
}

// TestGatewayModelsSigV4ListingSurvivesUnreadableMap pins the sigv4 listing when its model map cannot be read: the map
// costs its entries and nothing else, and the public bindings still stand.
func TestGatewayModelsSigV4ListingSurvivesUnreadableMap(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer
	gw := newModelsGateway(t, modelsSetup{
		defaultContext: "aws",
		env:            map[string]string{"EXAMPLE_MODEL_MAP": filepath.Join(t.TempDir(), "absent.json")},
		logger:         slog.New(slog.NewTextHandler(&logs, nil)),
	})

	rec := serveModelsRequest(t, gw, "/v1/models", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rec.Code)
	}

	requireModelIDs(t, decodeModelsListing(t, rec.Body.Bytes()), []string{"public-bedrock"})

	if !strings.Contains(logs.String(), "model map unavailable") {
		t.Errorf("the unreadable model map was not logged: %q", logs.String())
	}
}

// TestGatewayModelsSigV4OneModelNamesMapKeysOnly pins the single-model half of a sigv4 context. A caller may name a public
// binding of the context or a key of its model map, and nothing else exists. A key mapping to nothing is absent, and
// the value a key maps to is never echoed.
func TestGatewayModelsSigV4OneModelNamesMapKeysOnly(t *testing.T) {
	t.Parallel()

	fake := newFakeAnthropic(t, modelsReply(t, http.StatusOK, modelsUpstreamOne))
	gw := newModelsGateway(t, modelsSetup{
		fake:           fake,
		defaultContext: "aws",
		env:            map[string]string{"EXAMPLE_MODEL_MAP": writeModelsFile(t, modelsMapBody)},
	})

	for _, tc := range []struct {
		name string
		id   string
		want int
	}{
		{name: "a public binding of the context", id: "public-bedrock", want: http.StatusOK},
		{name: "a key of the model map", id: "claude-sonnet-5", want: http.StatusOK},
		{name: "another key of the model map", id: "claude-haiku-5", want: http.StatusOK},
		{name: "a model the map does not claim", id: "claude-opus-5", want: http.StatusNotFound},
		{name: "a key the map maps to nothing", id: "empty", want: http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := serveModelsRequest(t, gw, modelsModelPath+tc.id, nil)
			requireNoUpstream(t, fake)
			if rec.Code != tc.want {
				t.Fatalf("status = %d; want %d (body %s)", rec.Code, tc.want, rec.Body)
			}
			if strings.Contains(rec.Body.String(), "arn:") {
				t.Errorf("answer carries a mapped Bedrock id: %s", rec.Body)
			}
			if tc.want != http.StatusOK {
				return
			}

			want := map[string]any{
				"type": "model", "id": tc.id, "display_name": tc.id, "created_at": modelsEpoch,
			}
			if got := modelFields(t, rec.Body.Bytes()); !reflect.DeepEqual(got, want) {
				t.Errorf("entry = %v; want exactly %v", got, want)
			}
		})
	}
}

// TestGatewayModelsAccountContextSendsOnlyAccountToken pins the upstream half of a context with an account. The request
// to Anthropic carries only the account's token, never the caller's credentials, and it goes to the one host the gateway
// was configured with. The listing and the single-model relay are both checked.
func TestGatewayModelsAccountContextSendsOnlyAccountToken(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		target string
		body   string
	}{
		{name: "listing", target: "/ctx/team/v1/models", body: modelsStubPage},
		{name: "single model", target: "/ctx/team/v1/models/claude-haiku-5", body: `{"type":"model","id":"claude-haiku-5"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fake := newFakeAnthropic(t, modelsReply(t, http.StatusOK, tc.body))
			host, err := url.Parse(fake.url())
			if err != nil {
				t.Fatalf("parse the fake Anthropic's URL: %v", err)
			}
			recorder := &modelsHostRecorder{inner: http.DefaultTransport}
			gw := newModelsGateway(t, modelsSetup{
				fake:          fake,
				client:        &http.Client{Transport: recorder},
				tokens:        map[string]string{modelsTeamAccount: modelsTeamToken},
				callerKeyFile: writeKeyFile(t, exampleCallerKey),
			})

			header := http.Header{}
			header.Set("Authorization", callerAuthorization)
			header.Set("X-Api-Key", callerKeyHeader)
			header.Set(patchbayKeyHeader, exampleCallerKey)

			rec := serveModelsRequest(t, gw, tc.target, header)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d; want 200 (body %s)", rec.Code, rec.Body)
			}

			got := onlyRequest(t, fake)
			authorization := got.header.Values("Authorization")
			if !slices.Equal(authorization, []string{"Bearer " + modelsTeamToken}) {
				t.Errorf("upstream Authorization = %v; want only the account's token", authorization)
			}
			if values := got.header.Values("X-Api-Key"); len(values) != 0 {
				t.Errorf("upstream X-Api-Key = %v; want the caller's key dropped", values)
			}
			for name, values := range got.header {
				for _, value := range values {
					for _, secret := range []string{callerAuthorization, callerKeyHeader, exampleCallerKey} {
						if strings.Contains(value, secret) {
							t.Errorf("upstream %s carries the caller's credential %q", name, secret)
						}
					}
				}
			}

			if hosts := recorder.seenHosts(); !slices.Equal(hosts, []string{host.Host}) {
				t.Errorf("the gateway contacted %v; want only the configured upstream %s", hosts, host.Host)
			}
		})
	}
}

// TestGatewayModelsAccountWithoutTokenSendsNothingUpstream pins a context whose account has no token. The listing
// degrades to the bindings, and the single-model relay is refused. Neither falls back to the caller's credentials, and
// neither reaches the upstream.
func TestGatewayModelsAccountWithoutTokenSendsNothingUpstream(t *testing.T) {
	t.Parallel()

	fake := newFakeAnthropic(t, modelsReply(t, http.StatusOK, modelsUpstreamOne))
	gw := newModelsGateway(t, modelsSetup{fake: fake, defaultContext: "team"})

	rec := serveModelsRequest(t, gw, "/v1/models", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("listing status = %d; want 200 with the local bindings", rec.Code)
	}
	requireModelIDs(t, decodeModelsListing(t, rec.Body.Bytes()), []string{"team-opus"})

	rec = serveModelsRequest(t, gw, modelsModelPath+"claude-haiku-5", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("single model status = %d; want 500 (body %s)", rec.Code, rec.Body)
	}
	if got := errorTypeOf(t, rec.Body.Bytes()); got != "api_error" {
		t.Fatalf("error type = %q; want api_error", got)
	}

	requireNoUpstream(t, fake)
}

// TestGatewayModelsNoRoutesIs503 pins the answer before the route registry has been read: nothing can be listed, so the
// request is refused with 503 and the upstream is not asked.
func TestGatewayModelsNoRoutesIs503(t *testing.T) {
	t.Parallel()

	fake := newFakeAnthropic(t, modelsReply(t, http.StatusOK, modelsUpstreamOne))
	gw := newModelsGateway(t, modelsSetup{fake: fake, unloaded: true})

	for _, target := range []string{"/v1/models", modelsModelPath + "luna-fast"} {
		rec := serveModelsRequest(t, gw, target, nil)
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s status = %d; want 503", target, rec.Code)
		}
		if got := errorTypeOf(t, rec.Body.Bytes()); got != "api_error" {
			t.Errorf("%s error type = %q; want api_error", target, got)
		}
	}

	requireNoUpstream(t, fake)
}

// TestGatewayModelsUnknownContextIs404 pins a model endpoint that names a context the registry does not define. It is
// refused as an unknown resource, before any credential is asked for.
func TestGatewayModelsUnknownContextIs404(t *testing.T) {
	t.Parallel()

	fake := newFakeAnthropic(t, modelsReply(t, http.StatusOK, modelsUpstreamOne))
	gw := newModelsGateway(t, modelsSetup{fake: fake})

	for _, target := range []string{"/ctx/nowhere/v1/models", "/ctx/nowhere/v1/models/luna-fast"} {
		rec := serveModelsRequest(t, gw, target, nil)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s status = %d; want 404", target, rec.Code)
		}
		if got := errorTypeOf(t, rec.Body.Bytes()); got != "not_found_error" {
			t.Errorf("%s error type = %q; want not_found_error", target, got)
		}
	}

	requireNoUpstream(t, fake)
}

// TestGatewayRoutesNamedModelsCallerKeyGate pins the caller-key gate on the model endpoints a request reaches through a
// named context. Naming a context in the path asks what that context serves, so such a request must present the caller
// key: a missing or wrong key is refused with 403. The bare path stays open, since it is what a client sends with no
// idea the gateway is there.
func TestGatewayRoutesNamedModelsCallerKeyGate(t *testing.T) {
	t.Parallel()

	fake := newFakeAnthropic(t, modelsReply(t, http.StatusOK, modelsStubPage))
	gw := newModelsGateway(t, modelsSetup{fake: fake, callerKeyFile: writeKeyFile(t, exampleCallerKey)})

	for _, target := range []string{"/ctx/personal/v1/models", "/ctx/personal/v1/models/luna-fast"} {
		for _, key := range []string{"", exampleWrongKey} {
			header := http.Header{}
			if key != "" {
				header.Set(patchbayKeyHeader, key)
			}

			rec := serveModelsRequest(t, gw, target, header)
			if rec.Code != http.StatusForbidden {
				t.Errorf("%s with key %q status = %d; want 403", target, key, rec.Code)
			}
			if got := errorTypeOf(t, rec.Body.Bytes()); got != "api_error" {
				t.Errorf("%s with key %q error type = %q; want api_error", target, key, got)
			}
		}

		header := http.Header{}
		header.Set(patchbayKeyHeader, exampleCallerKey)
		if rec := serveModelsRequest(t, gw, target, header); rec.Code != http.StatusOK {
			t.Errorf("%s with the right key status = %d; want 200", target, rec.Code)
		}
	}

	if rec := serveModelsRequest(t, gw, "/v1/models", nil); rec.Code != http.StatusOK {
		t.Errorf("the bare listing is gated: status = %d; want 200", rec.Code)
	}
}

// TestGatewayRoutesNamedModelsCallerKeyUnavailableIs500 separates the server's own misconfiguration from the caller's
// refusal. With no caller key loaded, a named context's model endpoints answer 500, whatever the caller presents, and
// the bare path still answers.
func TestGatewayRoutesNamedModelsCallerKeyUnavailableIs500(t *testing.T) {
	t.Parallel()

	fake := newFakeAnthropic(t, modelsReply(t, http.StatusOK, modelsStubPage))
	// A directory is configured as the key file: it is named, and reading it fails, so no caller key is loaded.
	gw := newModelsGateway(t, modelsSetup{fake: fake, callerKeyFile: t.TempDir()})

	for _, target := range []string{"/ctx/personal/v1/models", "/ctx/personal/v1/models/luna-fast"} {
		header := http.Header{}
		header.Set(patchbayKeyHeader, exampleCallerKey)

		rec := serveModelsRequest(t, gw, target, header)
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("%s status = %d; want 500", target, rec.Code)
		}
		if got := errorTypeOf(t, rec.Body.Bytes()); got != "api_error" {
			t.Errorf("%s error type = %q; want api_error", target, got)
		}
	}

	if rec := serveModelsRequest(t, gw, "/v1/models", nil); rec.Code != http.StatusOK {
		t.Errorf("the bare listing status = %d; want 200", rec.Code)
	}
}
