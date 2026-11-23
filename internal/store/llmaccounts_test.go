package store_test

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/llmroutes"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/storetest"
)

// openLLMStore opens a migrated store file at path that holds no rows, and closes it when the test ends.
func openLLMStore(t *testing.T, path string) *store.Store {
	t.Helper()
	storetest.Seed(t, path)
	s, err := store.Open(context.Background(), path, store.NoSync())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return s
}

// routeAccounts returns the accounts the route tests store. Their ids and tokens are invented.
func routeAccounts() []store.NewAccount {
	return []store.NewAccount{
		{
			ID: "acct-alpha", Name: "Alpha", Provider: "example-provider", Token: "example-token-alpha",
			Created: 1700000100,
		},
		{
			ID: "acct-beta", Name: "Beta", Provider: "example-provider", Token: "example-token-beta",
			Created: 1700000200,
		},
	}
}

// addRouteAccounts stores routeAccounts and returns the stored accounts, in the order they were added.
func addRouteAccounts(t *testing.T, s *store.Store) []store.Account {
	t.Helper()
	accounts := routeAccounts()
	stored := make([]store.Account, 0, len(accounts))
	for _, account := range accounts {
		got, err := s.AddAccount(context.Background(), account)
		if err != nil {
			t.Fatalf("AddAccount %s: %v", account.ID, err)
		}
		stored = append(stored, got)
	}
	return stored
}

// assertAccountList fails unless the store and the reader at path both list exactly want, in order.
func assertAccountList(t *testing.T, s *store.Store, path string, want []store.Account) {
	t.Helper()
	got, err := s.Accounts(context.Background())
	if err != nil {
		t.Fatalf("Accounts: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Accounts = %+v; want %+v", got, want)
	}
	read, err := store.NewReader(path).Accounts(context.Background())
	if err != nil {
		t.Fatalf("Reader.Accounts: %v", err)
	}
	if !reflect.DeepEqual(read, want) {
		t.Errorf("Reader.Accounts = %+v; want %+v", read, want)
	}
}

// assertRoutes fails unless the store and the reader at path both read back exactly want.
func assertRoutes(t *testing.T, s *store.Store, path string, want llmroutes.Registry) {
	t.Helper()
	got, err := s.Routes(context.Background())
	if err != nil {
		t.Fatalf("Routes: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Routes = %+v; want %+v", got, want)
	}
	read, err := store.NewReader(path).Routes(context.Background())
	if err != nil {
		t.Fatalf("Reader.Routes: %v", err)
	}
	if !reflect.DeepEqual(read, want) {
		t.Errorf("Reader.Routes = %+v; want %+v", read, want)
	}
}

// fullRoutes is a registry that sets every field of a seat. Its contexts are one with a model binding and every
// subagent scope, one with no routing of its own, and one whose subagent routing binds no selector. Its seats and
// contexts are in id order, the order a read gives them back in.
func fullRoutes() llmroutes.Registry {
	return llmroutes.Registry{
		DefaultContext: "main",
		Seats: []llmroutes.Seat{
			{
				ID: "fwd", AuthMode: llmroutes.AuthForward, Upstream: llmroutes.AnthropicUpstream, Speed: "fast",
				CacheTTL: "1h", StrictStructuredOutput: true, MaxInputTokens: 8192,
			},
			{
				ID: "inj", AuthMode: llmroutes.AuthInject, Upstream: "http://gateway.example.test:8080",
				Model: "example-model", APIKeyEnvFile: "EXAMPLE_API_KEY", Billing: "metered", Speed: "fast",
				Insecure: true, StripCacheControl: true, StrictStructuredOutput: true, MaxInputTokens: 4096,
			},
			{
				ID: "sig", AuthMode: llmroutes.AuthSigV4, Model: "example-sigv4-model",
				AWSProfile: "example-profile", AWSRegion: "us-east-1", ModelMapEnvFile: "/etc/example/models.env",
				StripCacheControl: true, StrictStructuredOutput: true, MaxInputTokens: 2048,
				StripBetas: []string{"example-beta-a", "example-beta-b"},
			},
		},
		Contexts: []llmroutes.Context{
			{
				ID: "main", DefaultSeat: "fwd", Account: "acct-alpha",
				Models: map[string]string{"example-model": "inj", "example-sigv4": "sig", "example-fwd": "fwd"},
				Subagents: &llmroutes.Subagents{
					DefaultSeat: "fwd",
					AgentTypes:  map[string]string{"explore": "inj", "plan": "fwd"},
					Models:      map[string]string{"example-haiku": "inj"},
				},
			},
			{ID: "quiet", DefaultSeat: "fwd"},
			{
				ID: "side", DefaultSeat: "inj", Account: "acct-beta",
				Models:    map[string]string{"example-model": "inj"},
				Subagents: &llmroutes.Subagents{DefaultSeat: "inj"},
			},
		},
	}
}

// smallRoutes is a registry of one seat and one context, with no model binding and no subagent routing.
func smallRoutes() llmroutes.Registry {
	seat := llmroutes.Seat{ID: "fwd", AuthMode: llmroutes.AuthForward, Upstream: llmroutes.AnthropicUpstream}
	return llmroutes.Registry{
		DefaultContext: "main",
		Seats:          []llmroutes.Seat{seat},
		Contexts:       []llmroutes.Context{{ID: "main", DefaultSeat: "fwd"}},
	}
}

func TestStoreLLMAccountsAddListRenameRemove(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openLLMStore(t, path)

	alpha := store.Account{
		ID: "acct-alpha", Name: "Alpha", Provider: "example-provider", Plan: "example-plan",
		Fingerprint: "db4df5215dfc", Created: 1700000100,
	}
	gamma := store.Account{
		ID: "acct-gamma", Name: "Gamma", Provider: "example-provider", Fingerprint: "ee43f4dccfd8",
		Created: 1700000100,
	}
	beta := store.Account{
		ID: "acct-beta", Name: "Beta", Provider: "other-provider", Fingerprint: "2bf08413c9c2", Created: 1700000200,
	}
	// beta is added first, so the listing shows that it orders by creation time, not by insertion.
	for _, tc := range []struct {
		account store.NewAccount
		want    store.Account
	}{
		{
			account: store.NewAccount{
				ID: "acct-beta", Name: "Beta", Provider: "other-provider", Token: "example-token-beta",
				Created: 1700000200,
			},
			want: beta,
		},
		{
			account: store.NewAccount{
				ID: "acct-alpha", Name: "Alpha", Provider: "example-provider", Plan: "example-plan",
				Token: "example-token-alpha", Created: 1700000100,
			},
			want: alpha,
		},
		{
			account: store.NewAccount{
				ID: "acct-gamma", Name: "Gamma", Provider: "example-provider", Token: "example-token-gamma",
				Created: 1700000100,
			},
			want: gamma,
		},
	} {
		got, err := s.AddAccount(ctx, tc.account)
		if err != nil {
			t.Fatalf("AddAccount %s: %v", tc.account.ID, err)
		}
		if got != tc.want {
			t.Errorf("AddAccount %s = %+v; want %+v", tc.account.ID, got, tc.want)
		}
	}
	assertAccountList(t, s, path, []store.Account{alpha, gamma, beta})

	renamed := alpha
	renamed.Name = "Alpha renamed"
	got, err := s.RenameAccount(ctx, "acct-alpha", "Alpha renamed")
	if err != nil {
		t.Fatalf("RenameAccount acct-alpha: %v", err)
	}
	if got != renamed {
		t.Errorf("RenameAccount = %+v; want %+v", got, renamed)
	}
	if _, err = s.RenameAccount(ctx, "acct-missing", "Missing"); !errors.Is(err, store.ErrNoAccount) {
		t.Errorf("RenameAccount acct-missing = %v; want ErrNoAccount", err)
	}
	assertAccountList(t, s, path, []store.Account{renamed, gamma, beta})

	if err = s.RemoveAccount(ctx, "acct-beta"); err != nil {
		t.Fatalf("RemoveAccount acct-beta: %v", err)
	}
	if err = s.RemoveAccount(ctx, "acct-beta"); !errors.Is(err, store.ErrNoAccount) {
		t.Errorf("RemoveAccount acct-beta again = %v; want ErrNoAccount", err)
	}
	assertAccountList(t, s, path, []store.Account{renamed, gamma})
}

func TestStoreLLMAccountsRemoveInUseRefused(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openLLMStore(t, path)
	accounts := addRouteAccounts(t, s)
	if err := s.ReplaceRoutes(ctx, fullRoutes()); err != nil {
		t.Fatalf("ReplaceRoutes: %v", err)
	}

	// main names acct-alpha and side names acct-beta, so each removal is refused and names its context.
	for _, tc := range []struct{ id, route string }{{"acct-alpha", "main"}, {"acct-beta", "side"}} {
		err := s.RemoveAccount(ctx, tc.id)
		if !errors.Is(err, store.ErrAccountInUse) || !strings.Contains(err.Error(), tc.route) {
			t.Errorf("RemoveAccount %s = %v; want ErrAccountInUse naming %s", tc.id, err, tc.route)
		}
	}
	assertAccountList(t, s, path, accounts)

	if err := s.SetContextAccount(ctx, "main", ""); err != nil {
		t.Fatalf("SetContextAccount main: %v", err)
	}
	if err := s.RemoveAccount(ctx, "acct-alpha"); err != nil {
		t.Fatalf("RemoveAccount acct-alpha once unused: %v", err)
	}
	assertAccountList(t, s, path, accounts[1:])
}

func TestStoreLLMRoutesReplaceReadBack(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openLLMStore(t, path)
	addRouteAccounts(t, s)
	assertRoutes(t, s, path, llmroutes.Registry{})

	full := fullRoutes()
	if err := s.ReplaceRoutes(ctx, full); err != nil {
		t.Fatalf("ReplaceRoutes full: %v", err)
	}
	assertRoutes(t, s, path, full)

	// A second replacement holds only what it names, so no row of the first registry survives it.
	small := smallRoutes()
	if err := s.ReplaceRoutes(ctx, small); err != nil {
		t.Fatalf("ReplaceRoutes small: %v", err)
	}
	assertRoutes(t, s, path, small)
}

func TestStoreLLMRoutesRefusedLeavesStore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openLLMStore(t, path)
	addRouteAccounts(t, s)
	full := fullRoutes()
	if err := s.ReplaceRoutes(ctx, full); err != nil {
		t.Fatalf("ReplaceRoutes full: %v", err)
	}

	unknownSeat := fullRoutes()
	unknownSeat.Contexts[2].DefaultSeat = "missing"
	unknownAccount := fullRoutes()
	unknownAccount.Contexts[0].Account = "acct-missing"
	for _, tc := range []struct {
		name, want string
		reg        llmroutes.Registry
	}{
		{name: "unknown default seat", want: "does not name a seat", reg: unknownSeat},
		{name: "unknown account", want: "is not one of the accounts", reg: unknownAccount},
	} {
		err := s.ReplaceRoutes(ctx, tc.reg)
		if !errors.Is(err, store.ErrRoutesRefused) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("ReplaceRoutes %s = %v; want ErrRoutesRefused naming %q", tc.name, err, tc.want)
		}
		assertRoutes(t, s, path, full)
	}
}

func TestStoreLLMRoutesSetContextAccount(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openLLMStore(t, path)
	addRouteAccounts(t, s)
	full := fullRoutes()
	if err := s.ReplaceRoutes(ctx, full); err != nil {
		t.Fatalf("ReplaceRoutes: %v", err)
	}

	// quiet names no account, so setting its account changes that column and nothing else.
	if err := s.SetContextAccount(ctx, "quiet", "acct-beta"); err != nil {
		t.Fatalf("SetContextAccount quiet: %v", err)
	}
	want := fullRoutes()
	want.Contexts[1].Account = "acct-beta"
	assertRoutes(t, s, path, want)

	if err := s.SetContextAccount(ctx, "quiet", ""); err != nil {
		t.Fatalf("SetContextAccount quiet cleared: %v", err)
	}
	assertRoutes(t, s, path, full)

	if err := s.SetContextAccount(ctx, "missing", "acct-alpha"); !errors.Is(err, store.ErrNoRouteContext) {
		t.Errorf("SetContextAccount missing = %v; want ErrNoRouteContext", err)
	}
	if err := s.SetContextAccount(ctx, "missing", ""); !errors.Is(err, store.ErrNoRouteContext) {
		t.Errorf("SetContextAccount missing cleared = %v; want ErrNoRouteContext", err)
	}
	if err := s.SetContextAccount(ctx, "quiet", "acct-missing"); !errors.Is(err, store.ErrNoAccount) {
		t.Errorf("SetContextAccount unknown account = %v; want ErrNoAccount", err)
	}
	assertRoutes(t, s, path, full)
}
