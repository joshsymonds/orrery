package gateway_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/gateway"
	"github.com/joshsymonds/orrery/internal/llmroutes"
	"github.com/joshsymonds/orrery/internal/store"
)

// rewriteSeatID is the id of the seat the rewrite tests send every request through.
const rewriteSeatID = "example-rewrite"

// structuredOutputHead opens a StructuredOutput tool object; the input_schema member follows it.
const structuredOutputHead = `{"name":"StructuredOutput","description":"Reports the answer",`

// qualifyingSchema is a StructuredOutput input_schema that passes the strict allowlist. Its properties are out of
// alphabetical order, it nests an object with a number enum, and it carries an array of strings with items.
const qualifyingSchema = `{"type":"object","properties":{` +
	`"zeta":{"type":"string","description":"last"},` +
	`"alpha":{"type":"object","properties":{"inner":{"type":"number","enum":[1.5,2,-3]}},` +
	`"required":["inner"],"additionalProperties":false},` +
	`"tags":{"type":"array","items":{"type":"string"}}},` +
	`"required":["zeta","alpha","tags"],"additionalProperties":false,"title":"Answer"}`

// controlTool is a StructuredOutput tool whose schema qualifies. A test that holds it beside the tool under test can
// see the strict mark land on the control, so the test fails when the rewrite does not happen at all.
const controlTool = structuredOutputHead + `"input_schema":` + qualifyingSchema + `}`

// largeIntegerTool is a tool that is not StructuredOutput, so it must come through untouched. Its const is an integer
// beyond float64 precision.
const largeIntegerTool = `{"name":"Counter","input_schema":{"type":"object","properties":` +
	`{"limit":{"type":"integer","const":12345678901234567890}},"required":["limit"],` +
	`"additionalProperties":false}}`

// rewriteGateway returns a Gateway whose default context sends every request through seat, a forward seat, to fake.
// The seat's id and auth mode are set here, so a test names only the fields it rewrites.
func rewriteGateway(t *testing.T, fake *fakeAnthropic, seat llmroutes.Seat) *gateway.Gateway {
	t.Helper()

	seat.ID = rewriteSeatID
	seat.AuthMode = llmroutes.AuthForward
	routes := fakeRoutes{snapshot: store.RoutesSnapshot{Routes: llmroutes.Registry{
		DefaultContext: "main",
		Seats:          []llmroutes.Seat{seat},
		Contexts:       []llmroutes.Context{{ID: "main", DefaultSeat: rewriteSeatID}},
	}}}

	return newGateway(t, routes, fake.url(), discardLogger())
}

// postMessages sends body to gw as the caller, with the values of header, and returns what the caller was answered.
func postMessages(t *testing.T, gw *gateway.Gateway, body string, header http.Header) *httptest.ResponseRecorder {
	t.Helper()

	req := newProxyRequest(t, http.MethodPost, "/v1/messages", body)
	for name, values := range header {
		for _, value := range values {
			req.Header.Add(name, value)
		}
	}

	rec := httptest.NewRecorder()
	gw.ServeHTTP(rec, req)

	return rec
}

// forwardedOnce sends body to gw and returns the one request the fake Anthropic received, failing the test unless the
// caller was answered 200.
func forwardedOnce(
	t *testing.T,
	fake *fakeAnthropic,
	gw *gateway.Gateway,
	body string,
	header http.Header,
) seenRequest {
	t.Helper()

	rec := postMessages(t, gw, body, header)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200 (body %q)", rec.Code, rec.Body.String())
	}

	return onlyRequest(t, fake)
}

// decodeBody decodes a JSON object into the generic values Go's json package makes of it, failing the test when it is
// not JSON.
func decodeBody(t *testing.T, body []byte) map[string]any {
	t.Helper()

	var document map[string]any
	if err := json.Unmarshal(body, &document); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}

	return document
}

// compactJSON returns value with insignificant whitespace removed, which is the form the forwarded body carries its
// tools in.
func compactJSON(t *testing.T, value string) []byte {
	t.Helper()

	var buf bytes.Buffer
	if err := json.Compact(&buf, []byte(value)); err != nil {
		t.Fatalf("compact %s: %v", value, err)
	}

	return buf.Bytes()
}

// withStrictMember is the compacted element with ,"strict":true written before its closing brace, the splice a
// qualifying element without a strict member receives.
func withStrictMember(t *testing.T, element string) string {
	t.Helper()

	compact := compactJSON(t, element)

	return string(compact[:len(compact)-1]) + `,"strict":true}`
}

// optionalPropertiesSchema returns an object schema with n string properties, none of them required.
func optionalPropertiesSchema(n int) string {
	properties := make([]string, n)
	for i := range properties {
		properties[i] = fmt.Sprintf(`"p%d":{"type":"string"}`, i)
	}

	return `{"type":"object","properties":{` + strings.Join(properties, ",") +
		`},"additionalProperties":false}`
}

// arrayTypedSchema returns an object schema with n required properties, each of which carries a type array.
func arrayTypedSchema(n int) string {
	properties := make([]string, n)
	names := make([]string, n)
	for i := range properties {
		properties[i] = fmt.Sprintf(`"p%d":{"type":["string","null"]}`, i)
		names[i] = fmt.Sprintf(`"p%d"`, i)
	}

	return `{"type":"object","properties":{` + strings.Join(properties, ",") +
		`},"required":[` + strings.Join(names, ",") + `],"additionalProperties":false}`
}

// propertyWith returns an object schema whose single required property x is a string carrying extra, a keyword or value
// the allowlist has to refuse.
func propertyWith(extra string) string {
	return `{"type":"object","properties":{"x":{"type":"string",` + extra + `}},` +
		`"required":["x"],"additionalProperties":false}`
}

// strictNamed returns a tool object named StructuredOutput whose input_schema is schema.
func strictNamed(schema string) string {
	return `{"name":"StructuredOutput","input_schema":` + schema + `}`
}

// forwardedTools sends a request carrying tools through seat, and returns the tools member of the body the fake
// Anthropic received.
func forwardedTools(t *testing.T, seat llmroutes.Seat, tools string) []byte {
	t.Helper()

	fake := newFakeAnthropic(t, replyWith(t, http.StatusOK))
	gw := rewriteGateway(t, fake, seat)
	body := fmt.Sprintf(`{"model":"claude-opus-5","max_tokens":16,"tools":%s,`+
		`"messages":[{"role":"user","content":"hi"}]}`, tools)

	call := forwardedOnce(t, fake, gw, body, nil)

	var document map[string]json.RawMessage
	if err := json.Unmarshal(call.body, &document); err != nil {
		t.Fatalf("decode forwarded body: %v", err)
	}

	return document["tools"]
}

func TestGatewayRoutesRewriteSeatModel(t *testing.T) {
	t.Parallel()

	fake := newFakeAnthropic(t, replyWith(t, http.StatusOK))
	gw := rewriteGateway(t, fake, llmroutes.Seat{Model: "vendor/model-1"})

	call := forwardedOnce(t, fake, gw,
		`{"model":"claude-opus-5","max_tokens":16,"top_p":0.30000000000000004,"messages":[]}`, nil)

	want := decodeBody(t, []byte(
		`{"model":"vendor/model-1","max_tokens":16,"top_p":0.30000000000000004,"messages":[]}`))
	if got := decodeBody(t, call.body); !reflect.DeepEqual(got, want) {
		t.Errorf("forwarded body = %s\nwant %v", call.body, want)
	}

	// A value the rewrite does not touch keeps the exact decimal expansion the caller wrote.
	if !bytes.Contains(call.body, []byte("0.30000000000000004")) {
		t.Errorf("forwarded body lost the exact float expansion: %s", call.body)
	}
}

func TestGatewayRoutesRewriteStripBetas(t *testing.T) {
	t.Parallel()

	const stripped = "advisor-tool-2026-03-01"

	tests := []struct {
		name string
		beta []string
		want []string
	}{
		{
			name: "named flag removed from a comma list",
			beta: []string{stripped + ", prompt-caching-2026-01-05"},
			want: []string{"prompt-caching-2026-01-05"},
		},
		{
			name: "every named flag removed drops the header",
			beta: []string{stripped},
			want: nil,
		},
		{
			name: "named flag removed across repeated header lines",
			beta: []string{stripped, "prompt-caching-2026-01-05,context-1m"},
			want: []string{"prompt-caching-2026-01-05,context-1m"},
		},
		{
			name: "header naming no flag forwarded as it arrived",
			beta: []string{"prompt-caching-2026-01-05, context-1m"},
			want: []string{"prompt-caching-2026-01-05, context-1m"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fake := newFakeAnthropic(t, replyWith(t, http.StatusOK))
			gw := rewriteGateway(t, fake, llmroutes.Seat{StripBetas: []string{stripped}})

			call := forwardedOnce(t, fake, gw, forwardBody, http.Header{"Anthropic-Beta": tc.beta})

			if got := call.header.Values("Anthropic-Beta"); !slices.Equal(got, tc.want) {
				t.Errorf("upstream Anthropic-Beta = %q; want %q", got, tc.want)
			}
		})
	}
}

func TestGatewayRoutesRewriteSpeed(t *testing.T) {
	t.Parallel()

	fake := newFakeAnthropic(t, replyWith(t, http.StatusOK))
	gw := rewriteGateway(t, fake, llmroutes.Seat{Speed: "fast"})

	call := forwardedOnce(t, fake, gw,
		`{"model":"claude-opus-5","max_tokens":16,"speed":"standard","messages":[{"role":"user","content":"hi"}]}`, nil)

	want := decodeBody(t, []byte(
		`{"model":"claude-opus-5","max_tokens":16,"speed":"fast","messages":[{"role":"user","content":"hi"}]}`))
	if got := decodeBody(t, call.body); !reflect.DeepEqual(got, want) {
		t.Errorf("forwarded body = %s\nwant %v", call.body, want)
	}
}

func TestGatewayRoutesRewriteCacheTTL(t *testing.T) {
	t.Parallel()

	fake := newFakeAnthropic(t, replyWith(t, http.StatusOK))
	gw := rewriteGateway(t, fake, llmroutes.Seat{CacheTTL: "1h"})

	// Every marker, at any depth, takes the seat's lifetime. A marker that already asked for five minutes is replaced,
	// and a tool that only mentions the field in prose is left as it arrived. A marker that is not an object is the
	// caller's to answer for, so it stays null.
	call := forwardedOnce(t, fake, gw,
		`{"model":"claude-opus-5","max_tokens":16,`+
			`"cache_control":{"type":"ephemeral"},`+
			`"system":[{"type":"text","text":"sys","cache_control":{"type":"ephemeral","ttl":"5m"}}],`+
			`"tools":[{"name":"t","description":"says cache_control in prose","input_schema":{"type":"object"}}],`+
			`"metadata":{"cache_control":null},`+
			`"messages":[{"role":"user","content":[{"type":"text","text":"hi",`+
			`"cache_control":{"type":"ephemeral"}}]}]}`, nil)

	want := decodeBody(t, []byte(`{"model":"claude-opus-5","max_tokens":16,`+
		`"cache_control":{"type":"ephemeral","ttl":"1h"},`+
		`"system":[{"type":"text","text":"sys","cache_control":{"type":"ephemeral","ttl":"1h"}}],`+
		`"tools":[{"name":"t","description":"says cache_control in prose","input_schema":{"type":"object"}}],`+
		`"metadata":{"cache_control":null},`+
		`"messages":[{"role":"user","content":[{"type":"text","text":"hi",`+
		`"cache_control":{"type":"ephemeral","ttl":"1h"}}]}]}`))
	if got := decodeBody(t, call.body); !reflect.DeepEqual(got, want) {
		t.Errorf("forwarded body = %s\nwant %v", call.body, want)
	}
}

func TestGatewayRoutesRewriteStripCacheControl(t *testing.T) {
	t.Parallel()

	fake := newFakeAnthropic(t, replyWith(t, http.StatusOK))
	gw := rewriteGateway(t, fake, llmroutes.Seat{StripCacheControl: true})

	call := forwardedOnce(t, fake, gw,
		`{"model":"claude-opus-5","max_tokens":16,"cache_control":{"type":"ephemeral"},`+
			`"system":[{"type":"text","text":"sys","cache_control":{"type":"ephemeral"}}],`+
			`"messages":[{"role":"user","content":[{"type":"text","text":"hi",`+
			`"cache_control":{"type":"ephemeral"}}]}]}`, nil)

	want := decodeBody(t, []byte(`{"model":"claude-opus-5","max_tokens":16,`+
		`"system":[{"type":"text","text":"sys"}],`+
		`"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`))
	if got := decodeBody(t, call.body); !reflect.DeepEqual(got, want) {
		t.Errorf("forwarded body = %s\nwant %v", call.body, want)
	}
}

func TestGatewayRoutesRewriteStrictStructuredOutput(t *testing.T) {
	t.Parallel()

	strict := llmroutes.Seat{Model: "vendor/model-1", StrictStructuredOutput: true}

	tests := []struct {
		name string
		tool string
		want string
	}{
		{
			name: "member added last",
			tool: structuredOutputHead + `"input_schema":` + qualifyingSchema + `}`,
			want: structuredOutputHead + `"input_schema":` + qualifyingSchema + `,"strict":true}`,
		},
		{
			name: "existing member set in place",
			tool: `{"name":"StructuredOutput","strict":false,"input_schema":` + qualifyingSchema + `}`,
			want: `{"name":"StructuredOutput","strict":true,"input_schema":` + qualifyingSchema + `}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := forwardedTools(t, strict, "["+largeIntegerTool+","+tc.tool+"]")
			want := "[" + largeIntegerTool + "," + tc.want + "]"

			if !bytes.Equal(got, []byte(want)) {
				t.Errorf("forwarded tools = %s\nwant %s", got, want)
			}
		})
	}

	t.Run("strict-only seat marks the tool", func(t *testing.T) {
		t.Parallel()

		got := forwardedTools(t, llmroutes.Seat{StrictStructuredOutput: true}, "["+controlTool+"]")
		want := "[" + withStrictMember(t, controlTool) + "]"

		if !bytes.Equal(got, []byte(want)) {
			t.Errorf("forwarded tools = %s\nwant %s", got, want)
		}
	})

	t.Run("strict off leaves the tool alone", func(t *testing.T) {
		t.Parallel()

		seat := llmroutes.Seat{Model: "vendor/model-1"}
		got := forwardedTools(t, seat, "["+controlTool+"]")
		want := compactJSON(t, "["+controlTool+"]")

		if !bytes.Equal(got, want) {
			t.Errorf("forwarded tools = %s\nwant %s", got, want)
		}
	})
}

// TestGatewayRoutesRewriteStrictStructuredOutputLeavesOtherTools holds the allowlist. Each candidate is named
// StructuredOutput, yet none may gain strict. Each request also carries controlTool, which must gain it, so the test
// cannot pass by the rewrite never running.
func TestGatewayRoutesRewriteStrictStructuredOutputLeavesOtherTools(t *testing.T) {
	t.Parallel()

	strict := llmroutes.Seat{Model: "vendor/model-1", StrictStructuredOutput: true}

	tests := []struct {
		name    string
		element string
	}{
		{
			name: "$schema",
			element: strictNamed(`{"$schema":"https://json-schema.org/draft/2020-12/schema",` +
				`"type":"object","properties":{"x":{"type":"string"}},` +
				`"required":["x"],"additionalProperties":false}`),
		},
		{name: "$ref", element: strictNamed(propertyWith(`"$ref":"#/$defs/x"`))},
		{name: "default", element: strictNamed(propertyWith(`"default":"a"`))},
		{name: "examples", element: strictNamed(propertyWith(`"examples":["a"]`))},
		{name: "anyOf", element: strictNamed(propertyWith(`"anyOf":[{"type":"string"}]`))},
		{name: "format", element: strictNamed(propertyWith(`"format":"date"`))},
		{name: "pattern", element: strictNamed(propertyWith(`"pattern":"^a"`))},
		{name: "minimum", element: strictNamed(propertyWith(`"minimum":0`))},
		{name: "maxLength", element: strictNamed(propertyWith(`"maxLength":5`))},
		{
			name: "nested object without additionalProperties false",
			element: strictNamed(`{"type":"object","properties":{"inner":{"type":"object",` +
				`"properties":{"x":{"type":"string"}},"required":["x"]}},` +
				`"required":["inner"],"additionalProperties":false}`),
		},
		{name: "25 optional properties", element: strictNamed(optionalPropertiesSchema(25))},
		{name: "17 array-typed nodes", element: strictNamed(arrayTypedSchema(17))},
		{name: "enum with an object member", element: strictNamed(propertyWith(`"enum":["a",{"k":1}]`))},
		{
			name:    "input_schema absent",
			element: `{"name":"StructuredOutput","description":"Reports the answer"}`,
		},
		{name: "input_schema a string", element: `{"name":"StructuredOutput","input_schema":"object"}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := forwardedTools(t, strict, "["+controlTool+","+tc.element+"]")
			want := "[" + withStrictMember(t, controlTool) + "," + string(compactJSON(t, tc.element)) + "]"

			if !bytes.Equal(got, []byte(want)) {
				t.Errorf("forwarded tools = %s\nwant %s", got, want)
			}
		})
	}
}

func TestGatewayRoutesRewriteStrictStructuredOutputAdmitsBudgets(t *testing.T) {
	t.Parallel()

	strict := llmroutes.Seat{Model: "vendor/model-1", StrictStructuredOutput: true}

	tests := []struct {
		name   string
		schema string
	}{
		{name: "24 optional properties", schema: optionalPropertiesSchema(24)},
		{name: "16 array-typed nodes", schema: arrayTypedSchema(16)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			element := strictNamed(tc.schema)
			got := forwardedTools(t, strict, "["+element+"]")
			want := "[" + withStrictMember(t, element) + "]"

			if !bytes.Equal(got, []byte(want)) {
				t.Errorf("forwarded tools = %s\nwant %s", got, want)
			}
		})
	}
}

func TestGatewayRoutesRewriteRefusesBodyThatIsNotAnObject(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
	}{
		{name: "JSON null", body: "null"},
		{name: "not JSON", body: "not json at all"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fake := newFakeAnthropic(t, replyWith(t, http.StatusOK))
			gw := rewriteGateway(t, fake, llmroutes.Seat{Model: "vendor/model-1"})

			rec := postMessages(t, gw, tc.body, nil)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d; want 400 for a body the seat cannot rewrite", rec.Code)
			}
			if got := errorTypeOf(t, rec.Body.Bytes()); got != "invalid_request_error" {
				t.Errorf("error type = %q; want invalid_request_error", got)
			}
			requireNoUpstream(t, fake)
		})
	}
}
