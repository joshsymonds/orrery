package enginehost

import (
	"context"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/llmroutes"
	"github.com/joshsymonds/orrery/internal/store"
)

// accountTestID is the id the fixed NewID of the Accounts tests mints for the account they add.
const accountTestID = "acct-synthetic"

// accountTestContext and accountTestSeat are the route context and seat of the Accounts tests' registry.
const (
	accountTestContext = "ctx-work"
	accountTestSeat    = "seat-forward"
)

// accountTestNow returns the fixed time the Accounts tests read as now: 2026-11-21 14:00:00 UTC.
func accountTestNow() time.Time { return time.Date(2026, time.November, 21, 14, 0, 0, 0, time.UTC) }

// accountTestNewID is the fixed NewID of the Accounts tests: it mints accountTestID.
func accountTestNewID() string { return accountTestID }

// accountTestRegistry returns the route registry of the Accounts tests: a forward seat, and the default context, which
// sends through that seat with account. An account of "" is the caller's own login.
func accountTestRegistry(account string) llmroutes.Registry {
	return llmroutes.Registry{
		DefaultContext: accountTestContext,
		Seats: []llmroutes.Seat{
			{ID: accountTestSeat, AuthMode: llmroutes.AuthForward, Upstream: llmroutes.AnthropicUpstream},
		},
		Contexts: []llmroutes.Context{
			{ID: accountTestContext, DefaultSeat: accountTestSeat, Account: account},
		},
	}
}

// accountTestServe serves accounts on a sessions socket in a temporary directory, and returns the client that calls it.
// The server stops at cleanup.
func accountTestServe(t *testing.T, accounts *Accounts) enginev1connect.AccountsClient {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sessions.sock")
	ln, err := Listen(path)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- ServeWith(ctx, ln, ServeConfig{Accounts: accounts}) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("ServeWith after cleanup: %v", err)
		}
	})
	return enginev1connect.NewAccountsClient(unixHTTPClient(t, path), "http://accounts.invalid")
}

// accountTestAddAccount adds the synthetic account of the Accounts tests through client. The fixed NewID names it
// accountTestID.
func accountTestAddAccount(t *testing.T, client enginev1connect.AccountsClient) {
	t.Helper()
	_, err := client.AddAccount(t.Context(), connect.NewRequest(&v1.AddAccountRequest{
		Name: "Synthetic account", Provider: "anthropic", Token: "sk-synthetic-token",
	}))
	if err != nil {
		t.Fatalf("AddAccount: %v", err)
	}
}

func TestAccountServiceAddRenameRemove(t *testing.T) {
	stored, _ := openStore(t)
	// The id is not fixed here, so the default NewID mints it.
	client := accountTestServe(t, &Accounts{Store: stored, Now: accountTestNow})

	added, err := client.AddAccount(t.Context(), connect.NewRequest(&v1.AddAccountRequest{
		Name: "Synthetic account", Provider: "anthropic", Plan: "synthetic-plan", Token: "  sk-synthetic-token \n",
	}))
	if err != nil {
		t.Fatalf("AddAccount: %v", err)
	}
	account := added.Msg.GetAccount()
	if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(account.GetId()) {
		t.Fatalf("minted id = %q, want 32 lowercase hex digits", account.GetId())
	}
	// The fingerprint is of the trimmed token, so the surrounding whitespace is gone.
	want := &orreryv1.Account{
		Id: account.GetId(), Name: "Synthetic account", Provider: "anthropic", Plan: "synthetic-plan",
		Fingerprint: store.AccountFingerprint("sk-synthetic-token"), Created: accountTestNow().Unix(),
	}
	if !proto.Equal(account, want) {
		t.Fatalf("added account = %v, want %v", account, want)
	}

	renamed, err := client.RenameAccount(t.Context(), connect.NewRequest(&v1.RenameAccountRequest{
		Id: account.GetId(), Name: "Renamed synthetic",
	}))
	if err != nil {
		t.Fatalf("RenameAccount: %v", err)
	}
	want.Name = "Renamed synthetic"
	if !proto.Equal(renamed.Msg.GetAccount(), want) {
		t.Fatalf("renamed account = %v, want %v", renamed.Msg.GetAccount(), want)
	}
	_, err = client.RenameAccount(t.Context(), connect.NewRequest(&v1.RenameAccountRequest{
		Id: account.GetId(), Name: " ",
	}))
	wantConnectError(t, err, connect.CodeInvalidArgument, "name: empty")
	_, err = client.RenameAccount(t.Context(), connect.NewRequest(&v1.RenameAccountRequest{
		Id: "acct-unknown", Name: "Synthetic",
	}))
	wantConnectError(t, err, connect.CodeNotFound, "account: unknown id")

	if _, err = client.RemoveAccount(t.Context(), connect.NewRequest(&v1.RemoveAccountRequest{
		Id: account.GetId(),
	})); err != nil {
		t.Fatalf("RemoveAccount: %v", err)
	}
	listed, err := stored.Accounts(t.Context())
	if err != nil {
		t.Fatalf("Accounts: %v", err)
	}
	if len(listed) != 0 {
		t.Fatalf("stored accounts after remove = %d, want 0", len(listed))
	}
	_, err = client.RemoveAccount(t.Context(), connect.NewRequest(&v1.RemoveAccountRequest{Id: account.GetId()}))
	wantConnectError(t, err, connect.CodeNotFound, "account: unknown id")
}

func TestAccountServiceAddRefusals(t *testing.T) {
	stored, _ := openStore(t)
	client := accountTestServe(t, &Accounts{Store: stored, Now: accountTestNow, NewID: accountTestNewID})
	refusals := []struct {
		label, name, provider, token, message string
	}{
		{label: "empty name", name: "", provider: "anthropic", token: "sk-synthetic", message: "name: empty"},
		{label: "whitespace name", name: " \t", provider: "anthropic", token: "sk-synthetic", message: "name: empty"},
		{
			label: "other provider", name: "Synthetic account", provider: "openai", token: "sk-synthetic",
			message: "provider: want anthropic",
		},
		{
			label: "empty provider", name: "Synthetic account", provider: "", token: "sk-synthetic",
			message: "provider: want anthropic",
		},
		{
			label: "empty token", name: "Synthetic account", provider: "anthropic", token: "",
			message: "token: empty or holds whitespace",
		},
		{
			label: "whitespace token", name: "Synthetic account", provider: "anthropic", token: " \t ",
			message: "token: empty or holds whitespace",
		},
		{
			label: "interior whitespace", name: "Synthetic account", provider: "anthropic", token: "sk-synthetic part",
			message: "token: empty or holds whitespace",
		},
	}
	for _, refusal := range refusals {
		t.Run(refusal.label, func(t *testing.T) {
			_, err := client.AddAccount(t.Context(), connect.NewRequest(&v1.AddAccountRequest{
				Name: refusal.name, Provider: refusal.provider, Token: refusal.token,
			}))
			wantConnectError(t, err, connect.CodeInvalidArgument, refusal.message)
		})
	}
	listed, err := stored.Accounts(t.Context())
	if err != nil {
		t.Fatalf("Accounts: %v", err)
	}
	if len(listed) != 0 {
		t.Fatalf("stored accounts after refusals = %d, want 0", len(listed))
	}
}

func TestAccountServiceRemoveInUse(t *testing.T) {
	stored, _ := openStore(t)
	client := accountTestServe(t, &Accounts{Store: stored, Now: accountTestNow, NewID: accountTestNewID})
	accountTestAddAccount(t, client)
	_, err := client.ReplaceRoutes(t.Context(), connect.NewRequest(&v1.ReplaceRoutesRequest{
		Registry: llmroutes.ToProto(accountTestRegistry(accountTestID)),
	}))
	if err != nil {
		t.Fatalf("ReplaceRoutes: %v", err)
	}

	_, err = client.RemoveAccount(t.Context(), connect.NewRequest(&v1.RemoveAccountRequest{Id: accountTestID}))
	wantConnectError(t, err, connect.CodeFailedPrecondition, "account: in use by context "+accountTestContext)
	listed, err := stored.Accounts(t.Context())
	if err != nil {
		t.Fatalf("Accounts: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("stored accounts after refused remove = %d, want 1", len(listed))
	}

	// Once the context sends its own login, nothing holds the account, and its removal goes through.
	if _, err = client.SetContextAccount(t.Context(), connect.NewRequest(&v1.SetContextAccountRequest{
		Context: accountTestContext,
	})); err != nil {
		t.Fatalf("SetContextAccount: %v", err)
	}
	if _, err = client.RemoveAccount(t.Context(), connect.NewRequest(&v1.RemoveAccountRequest{
		Id: accountTestID,
	})); err != nil {
		t.Fatalf("RemoveAccount after clearing the context: %v", err)
	}
}

func TestAccountServiceReplaceAndSetContextAccount(t *testing.T) {
	stored, _ := openStore(t)
	client := accountTestServe(t, &Accounts{Store: stored, Now: accountTestNow, NewID: accountTestNewID})
	accountTestAddAccount(t, client)

	// ReplaceRoutes answers the registry as stored, which is the registry it was given.
	sent := llmroutes.ToProto(accountTestRegistry(""))
	replaced, err := client.ReplaceRoutes(t.Context(), connect.NewRequest(&v1.ReplaceRoutesRequest{Registry: sent}))
	if err != nil {
		t.Fatalf("ReplaceRoutes: %v", err)
	}
	if !proto.Equal(replaced.Msg.GetRegistry(), sent) {
		t.Fatalf("replaced registry = %v, want %v", replaced.Msg.GetRegistry(), sent)
	}

	// SetContextAccount sets the context's account and answers the registry as it stands.
	set, err := client.SetContextAccount(t.Context(), connect.NewRequest(&v1.SetContextAccountRequest{
		Context: accountTestContext, Account: accountTestID,
	}))
	if err != nil {
		t.Fatalf("SetContextAccount: %v", err)
	}
	if want := llmroutes.ToProto(accountTestRegistry(accountTestID)); !proto.Equal(set.Msg.GetRegistry(), want) {
		t.Fatalf("set registry = %v, want %v", set.Msg.GetRegistry(), want)
	}

	// A registry that names an unknown account is refused, and the stored registry stays as it was.
	_, err = client.ReplaceRoutes(t.Context(), connect.NewRequest(&v1.ReplaceRoutesRequest{
		Registry: llmroutes.ToProto(accountTestRegistry("acct-missing")),
	}))
	wantConnectError(t, err, connect.CodeInvalidArgument,
		`routes: context "ctx-work": account "acct-missing" is not one of the accounts`)
	kept, err := stored.Routes(t.Context())
	if err != nil {
		t.Fatalf("Routes: %v", err)
	}
	if want := llmroutes.ToProto(accountTestRegistry(accountTestID)); !proto.Equal(llmroutes.ToProto(kept), want) {
		t.Fatalf("stored registry after refusal = %v, want %v", llmroutes.ToProto(kept), want)
	}

	_, err = client.SetContextAccount(t.Context(), connect.NewRequest(&v1.SetContextAccountRequest{
		Context: "ctx-missing", Account: accountTestID,
	}))
	wantConnectError(t, err, connect.CodeNotFound, "context: unknown id")
	_, err = client.SetContextAccount(t.Context(), connect.NewRequest(&v1.SetContextAccountRequest{
		Context: accountTestContext, Account: "acct-missing",
	}))
	wantConnectError(t, err, connect.CodeNotFound, "account: unknown id")

	// An empty account makes the context send its own login again.
	cleared, err := client.SetContextAccount(t.Context(), connect.NewRequest(&v1.SetContextAccountRequest{
		Context: accountTestContext,
	}))
	if err != nil {
		t.Fatalf("SetContextAccount clearing: %v", err)
	}
	if want := llmroutes.ToProto(accountTestRegistry("")); !proto.Equal(cleared.Msg.GetRegistry(), want) {
		t.Fatalf("cleared registry = %v, want %v", cleared.Msg.GetRegistry(), want)
	}
}

func TestAccountServiceUnavailableWithoutStore(t *testing.T) {
	client := accountTestServe(t, nil)
	calls := []struct {
		name string
		call func() error
	}{
		{name: "AddAccount", call: func() error {
			_, err := client.AddAccount(t.Context(), connect.NewRequest(&v1.AddAccountRequest{}))
			return err
		}},
		{name: "RenameAccount", call: func() error {
			_, err := client.RenameAccount(t.Context(), connect.NewRequest(&v1.RenameAccountRequest{}))
			return err
		}},
		{name: "RemoveAccount", call: func() error {
			_, err := client.RemoveAccount(t.Context(), connect.NewRequest(&v1.RemoveAccountRequest{}))
			return err
		}},
		{name: "ReplaceRoutes", call: func() error {
			_, err := client.ReplaceRoutes(t.Context(), connect.NewRequest(&v1.ReplaceRoutesRequest{}))
			return err
		}},
		{name: "SetContextAccount", call: func() error {
			_, err := client.SetContextAccount(t.Context(), connect.NewRequest(&v1.SetContextAccountRequest{}))
			return err
		}},
	}
	for _, c := range calls {
		t.Run(c.name, func(t *testing.T) {
			wantConnectError(t, c.call(), connect.CodeUnavailable, "account store unavailable")
		})
	}
}
