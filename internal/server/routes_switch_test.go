package server_test

import (
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/llmroutes"
)

// routesSwitchTarget is the context the RoutesSwitch test sets to the second account. Before the switch it names no
// account, so the context sends its caller's own login.
const routesSwitchTarget = "ctx-review"

// routesSwitchRegistry returns the route registry of the RoutesSwitch test: three contexts in id order, the first of
// which names firstAccount, over three seats of different auth modes. Its contexts bind models and subagents to seats.
func routesSwitchRegistry(firstAccount string) llmroutes.Registry {
	return llmroutes.Registry{
		DefaultContext: "ctx-work",
		Seats: []llmroutes.Seat{
			{
				ID: "seat-forward", AuthMode: llmroutes.AuthForward, Upstream: llmroutes.AnthropicUpstream,
				Speed: "fast", CacheTTL: "1h",
			},
			{
				ID: "seat-inject", AuthMode: llmroutes.AuthInject, Upstream: "https://inject.example.test",
				Model: "synthetic-inject-model", APIKeyEnvFile: "SYNTHETIC_INJECT_KEY", Billing: "metered",
			},
			{
				ID: "seat-sigv4", AuthMode: llmroutes.AuthSigV4, AWSProfile: "synthetic-profile",
				AWSRegion: "us-test-1", ModelMapEnvFile: "SYNTHETIC_MODEL_MAP", StripBetas: []string{"synthetic-beta"},
			},
		},
		Contexts: []llmroutes.Context{
			{
				ID: "ctx-quiet", DefaultSeat: "seat-inject",
				Models: map[string]string{"synthetic-model-gamma": "seat-inject"},
			},
			{
				ID: routesSwitchTarget, DefaultSeat: "seat-forward",
				Models: map[string]string{"synthetic-model-delta": "seat-sigv4"},
				Subagents: &llmroutes.Subagents{
					DefaultSeat: "seat-forward",
					AgentTypes:  map[string]string{"synthetic-review": "seat-forward"},
					Models:      map[string]string{"synthetic-sub-model": "seat-sigv4"},
				},
			},
			{
				ID: "ctx-work", DefaultSeat: "seat-forward", Account: firstAccount,
				Models: map[string]string{
					"synthetic-model-alpha": "seat-forward",
					"synthetic-model-beta":  "seat-inject",
				},
				Subagents: &llmroutes.Subagents{
					DefaultSeat: "seat-forward",
					AgentTypes:  map[string]string{"synthetic-agent": "seat-inject"},
					Models:      map[string]string{"synthetic-sub-model": "seat-sigv4"},
				},
			},
		},
	}
}

// routesSwitchAddAccount adds an invented anthropic account through h's Accounts client and returns it.
func routesSwitchAddAccount(t *testing.T, h accountsHarness, name, token string) *orreryv1.Account {
	t.Helper()
	added, err := h.Accounts.Add(t.Context(), connect.NewRequest(&orreryv1.AccountsAddRequest{
		Name: name, Provider: "anthropic", Plan: "synthetic-plan", Token: token,
	}))
	if err != nil {
		t.Fatalf("Add %s: %v", name, err)
	}
	return added.Msg.GetAccount()
}

// routesSwitchGet reads the route registry through h's Routes client and fails t unless it answers.
func routesSwitchGet(t *testing.T, h accountsHarness) *orreryv1.RouteRegistry {
	t.Helper()
	got, err := h.Routes.Get(t.Context(), connect.NewRequest(&orreryv1.RoutesGetRequest{}))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	return got.Msg.GetRegistry()
}

// routesSwitchAssertOnlyTarget fails t unless got is before with only the account of routesSwitchTarget set to
// account. Every other context, every seat and default_context must be proto.Equal to before, and the target must
// keep its bindings.
func routesSwitchAssertOnlyTarget(t *testing.T, label string, before, got *orreryv1.RouteRegistry, account string) {
	t.Helper()
	if got.GetDefaultContext() != before.GetDefaultContext() {
		t.Fatalf("%s default_context = %q, want %q", label, got.GetDefaultContext(), before.GetDefaultContext())
	}
	if len(got.GetSeats()) != len(before.GetSeats()) {
		t.Fatalf("%s holds %d seats, want %d", label, len(got.GetSeats()), len(before.GetSeats()))
	}
	for i, seat := range before.GetSeats() {
		if !proto.Equal(got.GetSeats()[i], seat) {
			t.Fatalf("%s seat %s = %v, want %v", label, seat.GetId(), got.GetSeats()[i], seat)
		}
	}
	if len(got.GetContexts()) != len(before.GetContexts()) {
		t.Fatalf("%s holds %d contexts, want %d", label, len(got.GetContexts()), len(before.GetContexts()))
	}
	for i, route := range before.GetContexts() {
		gotRoute := got.GetContexts()[i]
		if route.GetId() != routesSwitchTarget {
			if !proto.Equal(gotRoute, route) {
				t.Fatalf("%s context %s = %v, want %v", label, route.GetId(), gotRoute, route)
			}
			continue
		}
		if gotRoute.GetAccount() != account {
			t.Fatalf("%s context %s account = %q, want %q", label, route.GetId(), gotRoute.GetAccount(), account)
		}
		want, ok := proto.Clone(route).(*orreryv1.RouteContext)
		if !ok {
			t.Fatalf("clone of context %s is not a RouteContext", route.GetId())
		}
		want.Account = account
		if !proto.Equal(gotRoute, want) {
			t.Fatalf("%s context %s = %v, want only its account changed to %v", label, route.GetId(), gotRoute, want)
		}
	}
}

func TestRoutesSwitchChangesOneContext(t *testing.T) {
	t.Parallel()
	h := newAccountsHarness(t)
	first := routesSwitchAddAccount(t, h, "Synthetic first", "sk-ant-oat01-first")
	second := routesSwitchAddAccount(t, h, "Synthetic second", "sk-ant-oat01-second")

	sent := llmroutes.ToProto(routesSwitchRegistry(first.GetId()))
	if _, err := h.Routes.Replace(t.Context(), connect.NewRequest(&orreryv1.RoutesReplaceRequest{
		Registry: sent,
	})); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	before := routesSwitchGet(t, h)
	if !proto.Equal(before, sent) {
		t.Fatalf("stored registry = %v, want the replaced %v", before, sent)
	}

	set, err := h.Routes.SetContextAccount(t.Context(), connect.NewRequest(&orreryv1.RoutesSetContextAccountRequest{
		Context: routesSwitchTarget, Account: second.GetId(),
	}))
	if err != nil {
		t.Fatalf("SetContextAccount: %v", err)
	}
	routesSwitchAssertOnlyTarget(t, "SetContextAccount answer", before, set.Msg.GetRegistry(), second.GetId())
	routesSwitchAssertOnlyTarget(t, "Get after the switch", before, routesSwitchGet(t, h), second.GetId())
}
