package store_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

func TestRoutesSnapshotGivesRoutesAndTokens(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openLLMStore(t, path)
	addRouteAccounts(t, s)
	// No context in fullRoutes names acct-gamma, so its token must not come back.
	if _, err := s.AddAccount(ctx, store.NewAccount{
		ID: "acct-gamma", Name: "Gamma", Provider: "example-provider", Token: "example-token-gamma", Created: 1700000300,
	}); err != nil {
		t.Fatalf("AddAccount acct-gamma: %v", err)
	}
	reg := fullRoutes()
	if err := s.ReplaceRoutes(ctx, reg); err != nil {
		t.Fatalf("ReplaceRoutes: %v", err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read store file: %v", err)
	}

	got, err := store.NewReader(path).RoutesSnapshot(ctx)
	if err != nil {
		t.Fatalf("RoutesSnapshot: %v", err)
	}
	if !reflect.DeepEqual(got.Routes, reg) {
		t.Errorf("RoutesSnapshot.Routes = %+v; want %+v", got.Routes, reg)
	}
	// The map holds exactly the accounts the contexts name, so it shows each named token and no unnamed one.
	want := map[string]string{"acct-alpha": "example-token-alpha", "acct-beta": "example-token-beta"}
	if !reflect.DeepEqual(got.Tokens, want) {
		t.Errorf("RoutesSnapshot.Tokens = %v; want %v", got.Tokens, want)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read store file after RoutesSnapshot: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Error("RoutesSnapshot changed the store file's bytes")
	}
}
