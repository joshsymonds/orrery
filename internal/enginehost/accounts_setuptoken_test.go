package enginehost

import (
	"strings"
	"testing"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// setupTokenAPIKeyMessage and setupTokenNotMessage are the messages AddAccount refuses an API key and any other
// non-setup-token with.
const (
	setupTokenAPIKeyMessage = "That is an API key, not a setup-token. A setup-token starts sk-ant-oat."
	setupTokenNotMessage    = "That is not a setup-token. A setup-token starts sk-ant-oat."
)

// TestAccountsSetupToken checks that AddAccount stores only a setup-token, one that starts sk-ant-oat. An API key and
// any other token are refused, each with its own message, and a refusal stores nothing and names no part of the token.
// The checks before the token's, the name, the provider and the whitespace, still refuse first, even for an API key.
func TestAccountsSetupToken(t *testing.T) {
	stored, _ := openStore(t)
	client := accountTestServe(t, &Accounts{Store: stored, Now: accountTestNow, NewID: accountTestNewID})
	const apiKey = "sk-ant-api03-synthetic-key"

	refusals := []struct {
		label, name, provider, token, message string
	}{
		{
			label: "API key", name: "Synthetic account", provider: "anthropic", token: apiKey,
			message: setupTokenAPIKeyMessage,
		},
		{
			label: "unknown prefix", name: "Synthetic account", provider: "anthropic", token: "sk-ant-xyz-synthetic",
			message: setupTokenNotMessage,
		},
		{
			label: "bare string", name: "Synthetic account", provider: "anthropic", token: "synthetic-bare-token",
			message: setupTokenNotMessage,
		},
		{
			label: "empty name with an API key", name: "", provider: "anthropic", token: apiKey,
			message: "name: empty",
		},
		{
			label: "wrong provider with an API key", name: "Synthetic account", provider: "openai", token: apiKey,
			message: "provider: want anthropic",
		},
		{
			label: "interior whitespace in an API key", name: "Synthetic account", provider: "anthropic",
			token: "sk-ant-api03-synthetic key", message: "token: empty or holds whitespace",
		},
	}
	for _, refusal := range refusals {
		t.Run(refusal.label, func(t *testing.T) {
			_, err := client.AddAccount(t.Context(), connect.NewRequest(&v1.AddAccountRequest{
				Name: refusal.name, Provider: refusal.provider, Token: refusal.token,
			}))
			wantConnectError(t, err, connect.CodeInvalidArgument, refusal.message)
			if strings.Contains(err.Error(), refusal.token) {
				t.Fatalf("refusal carries the token: %v", err)
			}
		})
	}

	listed, err := stored.Accounts(t.Context())
	if err != nil {
		t.Fatalf("Accounts: %v", err)
	}
	if len(listed) != 0 {
		t.Fatalf("stored accounts after refusals = %d, want 0", len(listed))
	}

	t.Run("setup-token stored", func(t *testing.T) {
		const setupToken = "sk-ant-oat01-synthetic-token"
		added, err := client.AddAccount(t.Context(), connect.NewRequest(&v1.AddAccountRequest{
			Name: "Synthetic account", Provider: "anthropic", Token: setupToken,
		}))
		if err != nil {
			t.Fatalf("AddAccount: %v", err)
		}
		if got, want := added.Msg.GetAccount().GetFingerprint(), store.AccountFingerprint(setupToken); got != want {
			t.Fatalf("added fingerprint = %q, want %q", got, want)
		}
		listed, err := stored.Accounts(t.Context())
		if err != nil {
			t.Fatalf("Accounts: %v", err)
		}
		if len(listed) != 1 || listed[0].Fingerprint != store.AccountFingerprint(setupToken) {
			t.Fatalf("stored accounts = %v, want the one setup-token account", listed)
		}
	})
}
