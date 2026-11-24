package server_test

import (
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
)

// TestRoutesRoundTripEveryLiveField replaces an invented route registry through serve, one that uses every field the
// live routes use: forward, inject and sigv4 seats, and contexts with models, a default seat, subagent routing and an
// account. The Replace answer and a following Get both carry the registry unchanged.
func TestRoutesRoundTripEveryLiveField(t *testing.T) {
	t.Parallel()
	h := newAccountsHarness(t)
	added, err := h.Accounts.Add(t.Context(), connect.NewRequest(&orreryv1.AccountsAddRequest{
		Name: "Synthetic route account", Provider: "anthropic", Plan: "synthetic-plan", Token: "sk-ant-oat01-routes",
	}))
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	want := &orreryv1.RouteRegistry{
		DefaultContext: "alpha",
		Seats: []*orreryv1.RouteSeat{
			{
				Id:                     "fwd-seat",
				AuthMode:               "forward",
				Upstream:               "https://api.anthropic.com",
				Model:                  "example-forward-model",
				CacheTtl:               "1h",
				MaxInputTokens:         200000,
				StrictStructuredOutput: true,
			},
			{
				Id:                "inject-http",
				AuthMode:          "inject",
				Upstream:          "http://gateway.example.test",
				Model:             "example-model",
				ApiKeyEnvFile:     "EXAMPLE_HTTP_API_KEY",
				Billing:           "metered",
				Speed:             "fast",
				Insecure:          true,
				StripCacheControl: true,
				MaxInputTokens:    100000,
			},
			{
				Id:                "inject-https",
				AuthMode:          "inject",
				Upstream:          "https://inject.example.test",
				Model:             "example-model-two",
				ApiKeyEnvFile:     "EXAMPLE_HTTPS_API_KEY",
				Billing:           "subscription",
				Speed:             "fast",
				StripCacheControl: true,
				MaxInputTokens:    150000,
			},
			{
				Id:              "sigv4-seat",
				AuthMode:        "sigv4",
				Model:           "example-sigv4-model",
				AwsProfile:      "example-profile",
				AwsRegion:       "us-east-1",
				ModelMapEnvFile: "example-model-map.env",
				StripBetas:      []string{"example-beta-one", "example-beta-two"},
				MaxInputTokens:  50000,
			},
		},
		Contexts: []*orreryv1.RouteContext{
			{
				Id:          "alpha",
				DefaultSeat: "fwd-seat",
				Models: map[string]string{
					"claude-example":      "fwd-seat",
					"example-model":       "inject-http",
					"example-sigv4-model": "sigv4-seat",
				},
				Subagents: &orreryv1.RouteSubagents{
					DefaultSeat: "sigv4-seat",
					AgentTypes: map[string]string{
						"Explore":       "fwd-seat",
						"example-agent": "inject-https",
					},
					Models: map[string]string{
						"example-model-two": "inject-https",
					},
				},
			},
			{
				Id:          "beta",
				DefaultSeat: "inject-https",
				Subagents:   &orreryv1.RouteSubagents{DefaultSeat: "inject-https"},
			},
			{
				Id:          "gamma",
				DefaultSeat: "fwd-seat",
				Models:      map[string]string{"claude-example": "fwd-seat"},
				Account:     added.Msg.GetAccount().GetId(),
			},
		},
	}

	replaced, err := h.Routes.Replace(t.Context(), connect.NewRequest(&orreryv1.RoutesReplaceRequest{Registry: want}))
	if err != nil {
		t.Fatalf("Replace: %v", err)
	}
	t.Logf("Replace answered %v", replaced.Msg.GetRegistry())
	if !proto.Equal(replaced.Msg.GetRegistry(), want) {
		t.Fatalf("Replace answered %v, want %v", replaced.Msg.GetRegistry(), want)
	}

	got, err := h.Routes.Get(t.Context(), connect.NewRequest(&orreryv1.RoutesGetRequest{}))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	t.Logf("Get answered %v", got.Msg.GetRegistry())
	if !proto.Equal(got.Msg.GetRegistry(), want) {
		t.Fatalf("Get answered %v, want %v", got.Msg.GetRegistry(), want)
	}
}
