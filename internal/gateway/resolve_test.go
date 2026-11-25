package gateway_test

import (
	"errors"
	"testing"

	"github.com/joshsymonds/orrery/internal/gateway"
	"github.com/joshsymonds/orrery/internal/llmroutes"
)

// testRegistry is a route registry with invented seat ids and hosts. Its main context is the default, and its side
// context maps the same model to a different seat, so each case shows which context a request reached.
func testRegistry() llmroutes.Registry {
	return llmroutes.Registry{
		DefaultContext: "main",
		Seats: []llmroutes.Seat{
			{ID: "alpha", AuthMode: llmroutes.AuthForward, Upstream: llmroutes.AnthropicUpstream},
			{
				ID: "bravo", AuthMode: llmroutes.AuthInject, Upstream: "https://bravo.example.test",
				Model: "example-model", APIKeyEnvFile: "EXAMPLE_API_KEY", Billing: "metered",
			},
			{
				ID: "charlie", AuthMode: llmroutes.AuthInject, Upstream: "https://charlie.example.test",
				Model: "example-model", APIKeyEnvFile: "EXAMPLE_API_KEY", Billing: "metered",
			},
			{
				ID: "delta", AuthMode: llmroutes.AuthSigV4, AWSProfile: "example-profile", AWSRegion: "us-east-1",
				ModelMapEnvFile: "/etc/example/models.env",
			},
			{
				ID: "echo", AuthMode: llmroutes.AuthInject, Upstream: "https://echo.example.test",
				Model: "example-model", APIKeyEnvFile: "EXAMPLE_API_KEY", Billing: "metered",
				StripBetas: []string{"example-beta"},
			},
		},
		Contexts: []llmroutes.Context{
			{
				ID: "main", DefaultSeat: "alpha", Models: map[string]string{"model-one": "bravo"},
				Subagents: &llmroutes.Subagents{
					DefaultSeat: "delta",
					AgentTypes:  map[string]string{"explorer": "echo"},
					Models:      map[string]string{"model-two": "charlie"},
				},
			},
			{ID: "side", DefaultSeat: "charlie", Models: map[string]string{"model-one": "echo"}},
		},
	}
}

// wantResolution is the context, seat, selector and subagent routing a case expects.
type wantResolution struct {
	context  string
	seat     string
	selector string
	subagent bool
}

// assertResolution fails the test unless got is the resolution want describes.
func assertResolution(t *testing.T, got gateway.Resolution, want wantResolution) {
	t.Helper()
	if got.Context.ID != want.context || got.SeatID != want.seat || got.Seat.ID != want.seat ||
		got.Selector != want.selector || got.Subagent != want.subagent {
		t.Fatalf("Resolve() = context %q seat %q selector %q subagent %v; want %+v",
			got.Context.ID, got.SeatID, got.Selector, got.Subagent, want)
	}
}

func TestGatewayRoutesResolveContextByPrefix(t *testing.T) {
	t.Parallel()
	name, rest := gateway.SplitContext("/ctx/side/v1/messages")
	if name != "side" || rest != "/v1/messages" {
		t.Fatalf("SplitContext() = %q, %q; want side, /v1/messages", name, rest)
	}
	res, err := gateway.Resolve(testRegistry(), name, "model-one", nil)
	if err != nil {
		t.Fatalf("Resolve() = %v", err)
	}
	assertResolution(t, res, wantResolution{context: "side", seat: "echo", selector: "model-one"})
}

func TestGatewayRoutesResolveSplitContextPaths(t *testing.T) {
	t.Parallel()
	tests := []struct {
		path string
		name string
		rest string
	}{
		{path: "/ctx/work", name: "work", rest: "/"},
		{path: "/ctx/work/", name: "work", rest: "/"},
		{path: "/ctx/", name: "", rest: "/ctx/"},
		{path: "/ctx", name: "", rest: "/ctx"},
		{path: "/ctxwork/v1/messages", name: "", rest: "/ctxwork/v1/messages"},
	}
	for _, tc := range tests {
		name, rest := gateway.SplitContext(tc.path)
		if name != tc.name || rest != tc.rest {
			t.Errorf("SplitContext(%q) = %q, %q; want %q, %q", tc.path, name, rest, tc.name, tc.rest)
		}
	}
}

func TestGatewayRoutesResolveDefaultContext(t *testing.T) {
	t.Parallel()
	name, rest := gateway.SplitContext("/v1/messages")
	if name != "" || rest != "/v1/messages" {
		t.Fatalf("SplitContext() = %q, %q; want no context and /v1/messages", name, rest)
	}
	res, err := gateway.Resolve(testRegistry(), name, "unbound-model", nil)
	if err != nil {
		t.Fatalf("Resolve() = %v", err)
	}
	assertResolution(t, res, wantResolution{context: "main", seat: "alpha"})
}

func TestGatewayRoutesResolveUnknownContext(t *testing.T) {
	t.Parallel()
	name, _ := gateway.SplitContext("/ctx/missing/v1/messages")
	_, err := gateway.Resolve(testRegistry(), name, "model-one", nil)
	if !errors.Is(err, gateway.ErrNoContext) {
		t.Fatalf("Resolve() = %v; want ErrNoContext", err)
	}
}

func TestGatewayRoutesResolveExactModel(t *testing.T) {
	t.Parallel()
	res, err := gateway.Resolve(testRegistry(), "main", "model-one", nil)
	if err != nil {
		t.Fatalf("Resolve() = %v", err)
	}
	assertResolution(t, res, wantResolution{context: "main", seat: "bravo", selector: "model-one"})
}

func TestGatewayRoutesResolveSubagentAgentType(t *testing.T) {
	t.Parallel()
	// model-two is bound to charlie among the subagent models, so reaching echo shows the agent type binding won.
	obs := &gateway.Observation{AgentID: "agent-one", AgentType: "explorer"}
	res, err := gateway.Resolve(testRegistry(), "main", "model-two", obs)
	if err != nil {
		t.Fatalf("Resolve() = %v", err)
	}
	assertResolution(t, res, wantResolution{context: "main", seat: "echo", selector: "explorer", subagent: true})
}

func TestGatewayRoutesResolveSubagentModel(t *testing.T) {
	t.Parallel()
	obs := &gateway.Observation{AgentID: "agent-one", AgentType: "general-purpose"}
	res, err := gateway.Resolve(testRegistry(), "main", "model-two", obs)
	if err != nil {
		t.Fatalf("Resolve() = %v", err)
	}
	assertResolution(t, res, wantResolution{context: "main", seat: "charlie", selector: "model-two", subagent: true})
}

func TestGatewayRoutesResolveSubagentDefaultSeat(t *testing.T) {
	t.Parallel()
	obs := &gateway.Observation{AgentID: "agent-one"}
	res, err := gateway.Resolve(testRegistry(), "main", "unbound-model", obs)
	if err != nil {
		t.Fatalf("Resolve() = %v", err)
	}
	assertResolution(t, res, wantResolution{context: "main", seat: "delta", subagent: true})
}

func TestGatewayRoutesResolveMarkedSkipsPublicBinding(t *testing.T) {
	t.Parallel()
	// model-one is bound to bravo publicly, but a marked subagent takes the subagent routing instead.
	obs := &gateway.Observation{AgentID: "agent-one"}
	res, err := gateway.Resolve(testRegistry(), "main", "model-one", obs)
	if err != nil {
		t.Fatalf("Resolve() = %v", err)
	}
	assertResolution(t, res, wantResolution{context: "main", seat: "delta", subagent: true})
}

func TestGatewayRoutesResolveMarkedWithoutSubagents(t *testing.T) {
	t.Parallel()
	obs := &gateway.Observation{AgentID: "agent-one"}
	res, err := gateway.Resolve(testRegistry(), "side", "model-one", obs)
	if err != nil {
		t.Fatalf("Resolve() = %v", err)
	}
	assertResolution(t, res, wantResolution{context: "side", seat: "echo", selector: "model-one"})
}

func TestGatewayRoutesResolveContextDefaultSeat(t *testing.T) {
	t.Parallel()
	res, err := gateway.Resolve(testRegistry(), "side", "unbound-model", nil)
	if err != nil {
		t.Fatalf("Resolve() = %v", err)
	}
	assertResolution(t, res, wantResolution{context: "side", seat: "charlie"})
}

func TestGatewayRoutesResolveUnknownSeat(t *testing.T) {
	t.Parallel()
	reg := testRegistry()
	reg.Contexts[0].DefaultSeat = "ghost"
	if _, err := gateway.Resolve(reg, "main", "unbound-model", nil); err == nil {
		t.Fatal("Resolve() succeeded for a context whose default seat is not defined")
	}
}

func TestGatewayRoutesResolveCopiesAreIsolated(t *testing.T) {
	t.Parallel()
	reg := testRegistry()
	res, err := gateway.Resolve(reg, "side", "model-one", nil)
	if err != nil {
		t.Fatalf("Resolve() = %v", err)
	}
	res.Seat.StripBetas[0] = "changed"
	res.Context.Models["model-one"] = "alpha"
	if got := reg.Seats[4].StripBetas[0]; got != "example-beta" {
		t.Fatalf("registry seat beta = %q after the resolution was changed; want example-beta", got)
	}
	if got := reg.Contexts[1].Models["model-one"]; got != "echo" {
		t.Fatalf("registry binding = %q after the resolution was changed; want echo", got)
	}
}
