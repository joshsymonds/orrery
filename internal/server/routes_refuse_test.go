package server_test

import (
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
)

// addSyntheticAccount adds an invented account through h's Accounts client and returns it as serve answered it.
func addSyntheticAccount(t *testing.T, h accountsHarness, name, token string) *orreryv1.Account {
	t.Helper()
	added, err := h.Accounts.Add(t.Context(), connect.NewRequest(&orreryv1.AccountsAddRequest{
		Name: name, Provider: "anthropic", Plan: "synthetic-plan", Token: token,
	}))
	if err != nil {
		t.Fatalf("Add %s: %v", name, err)
	}
	return added.Msg.GetAccount()
}

// validRouteRegistry returns a registry the gateway admits, in which the context main names the account accountID and
// the context side names none. Its seats and contexts are in id order, as the store reads them back.
func validRouteRegistry(accountID string) *orreryv1.RouteRegistry {
	return &orreryv1.RouteRegistry{
		DefaultContext: "main",
		Seats: []*orreryv1.RouteSeat{
			{Id: "direct", AuthMode: "forward", Upstream: "https://api.anthropic.com"},
			{
				Id:            "gateway",
				AuthMode:      "inject",
				Upstream:      "https://gateway.example.test",
				ApiKeyEnvFile: "EXAMPLE_GATEWAY_KEY",
				Billing:       "metered",
				Model:         "example-model",
			},
		},
		Contexts: []*orreryv1.RouteContext{
			{
				Id:          "main",
				DefaultSeat: "direct",
				Account:     accountID,
				Models:      map[string]string{"example-main": "direct"},
			},
			{
				Id:          "side",
				DefaultSeat: "gateway",
				Models:      map[string]string{"example-side": "gateway"},
			},
		},
	}
}

// refusedWith fails t unless err is a refusal whose connect code is want.
func refusedWith(t *testing.T, err error, want connect.Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("answered no error, want a %v refusal", want)
	}
	if got := connect.CodeOf(err); got != want {
		t.Fatalf("refused with %v, want %v: %v", got, want, err)
	}
}

// routesOf returns the route registry h's Routes client answers with, and fails t unless it answers.
func routesOf(t *testing.T, h accountsHarness) *orreryv1.RoutesGetResponse {
	t.Helper()
	got, err := h.Routes.Get(t.Context(), connect.NewRequest(&orreryv1.RoutesGetRequest{}))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	return got.Msg
}

// assertStoreUnchanged fails t unless the route registry and the accounts still equal the ones it was given.
func assertStoreUnchanged(
	t *testing.T,
	h accountsHarness,
	wantRoutes *orreryv1.RoutesGetResponse,
	wantAccounts *orreryv1.AccountsListResponse,
) {
	t.Helper()
	if gotRoutes := routesOf(t, h); !proto.Equal(gotRoutes, wantRoutes) {
		t.Fatalf("Get after a refusal answered %v, want %v", gotRoutes, wantRoutes)
	}
	if gotAccounts := listAccountsOf(t, h); !proto.Equal(gotAccounts, wantAccounts) {
		t.Fatalf("List after a refusal answered %v, want %v", gotAccounts, wantAccounts)
	}
}

func TestRoutesRefuseLeavesStoreUnchanged(t *testing.T) {
	t.Parallel()
	h := newAccountsHarness(t)
	alpha := addSyntheticAccount(t, h, "Synthetic alpha", "sk-ant-oat01-alpha")
	addSyntheticAccount(t, h, "Synthetic beta", "sk-ant-oat01-beta")

	valid := validRouteRegistry(alpha.GetId())
	if _, err := h.Routes.Replace(t.Context(), connect.NewRequest(&orreryv1.RoutesReplaceRequest{
		Registry: valid,
	})); err != nil {
		t.Fatalf("Replace valid registry: %v", err)
	}
	wantRoutes := routesOf(t, h)
	if !proto.Equal(wantRoutes.GetRegistry(), valid) {
		t.Fatalf("Get after the valid Replace answered %v, want %v", wantRoutes.GetRegistry(), valid)
	}
	wantAccounts := listAccountsOf(t, h)
	if got := len(wantAccounts.GetAccounts()); got != 2 {
		t.Fatalf("List answered %d accounts, want the 2 added", got)
	}

	refusals := []struct {
		name  string
		build func() *orreryv1.RouteRegistry
	}{
		{
			name: "binding to a missing seat",
			build: func() *orreryv1.RouteRegistry {
				reg := validRouteRegistry(alpha.GetId())
				reg.Contexts[0].Models["example-main"] = "missing-seat"
				return reg
			},
		},
		{
			name: "bad id",
			build: func() *orreryv1.RouteRegistry {
				reg := validRouteRegistry(alpha.GetId())
				reg.Contexts[1].Id = "Bad_ID"
				return reg
			},
		},
		{
			name: "seat field its auth mode forbids",
			build: func() *orreryv1.RouteRegistry {
				reg := validRouteRegistry(alpha.GetId())
				reg.Seats[0].AwsProfile = "example-profile"
				return reg
			},
		},
		{
			name: "context naming an unknown account",
			build: func() *orreryv1.RouteRegistry {
				reg := validRouteRegistry(alpha.GetId())
				reg.Contexts[0].Account = "no-such-account"
				return reg
			},
		},
		{
			name: "forward seat upstream not the Anthropic upstream",
			build: func() *orreryv1.RouteRegistry {
				reg := validRouteRegistry(alpha.GetId())
				reg.Seats[0].Upstream = "https://gateway.example.test"
				return reg
			},
		},
	}
	for _, tc := range refusals {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := h.Routes.Replace(t.Context(), connect.NewRequest(&orreryv1.RoutesReplaceRequest{
				Registry: tc.build(),
			}))
			refusedWith(t, err, connect.CodeInvalidArgument)
			assertStoreUnchanged(t, h, wantRoutes, wantAccounts)
		})
	}

	t.Run("remove account a context uses", func(t *testing.T) {
		t.Parallel()
		_, err := h.Accounts.Remove(t.Context(), connect.NewRequest(&orreryv1.AccountsRemoveRequest{
			Id: alpha.GetId(),
		}))
		refusedWith(t, err, connect.CodeFailedPrecondition)
		assertStoreUnchanged(t, h, wantRoutes, wantAccounts)
	})
}
