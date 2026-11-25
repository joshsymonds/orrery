package store_test

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

func TestRoutesSnapshotAccountTokensHoldsUnnamedAccount(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openLLMStore(t, path)
	addRouteAccounts(t, s)
	// No context in fullRoutes names acct-gamma, so only AccountTokens can hold its token.
	if _, err := s.AddAccount(ctx, store.NewAccount{
		ID: "acct-gamma", Name: "Gamma", Provider: "example-provider", Token: "example-token-gamma",
		Created: 1700000300,
	}); err != nil {
		t.Fatalf("AddAccount acct-gamma: %v", err)
	}
	if err := s.ReplaceRoutes(ctx, fullRoutes()); err != nil {
		t.Fatalf("ReplaceRoutes: %v", err)
	}

	got, err := store.NewReader(path).RoutesSnapshot(ctx)
	if err != nil {
		t.Fatalf("RoutesSnapshot: %v", err)
	}
	want := map[string]string{
		"acct-alpha": "example-token-alpha",
		"acct-beta":  "example-token-beta",
		"acct-gamma": "example-token-gamma",
	}
	if !reflect.DeepEqual(got.AccountTokens, want) {
		t.Errorf("RoutesSnapshot.AccountTokens = %v; want every stored account's token, %v", got.AccountTokens, want)
	}
}
