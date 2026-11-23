package storetest_test

import (
	"context"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/storetest"
)

// TestBusyErrorIsTheStoresBusyRefusal checks that BusyError returns an error store.Busy reports, wrapped as callers wrap
// it or not.
func TestBusyErrorIsTheStoresBusyRefusal(t *testing.T) {
	t.Parallel()
	busy := storetest.BusyError(t)
	if !store.Busy(busy) || !store.Busy(fmt.Errorf("wrapped: %w", busy)) {
		t.Fatalf("store.Busy(%v) = false, want true", busy)
	}
}

// TestSeedFilledFillsOnceAndSeedsEveryPath seeds two paths under one key. The fill runs once, and each path opens as
// a store that holds what it wrote.
func TestSeedFilledFillsOnceAndSeedsEveryPath(t *testing.T) {
	t.Parallel()
	var fills atomic.Int32
	fill := func(ctx context.Context, s *store.Store) error {
		fills.Add(1)
		return s.SyncServer(ctx, "", "-L a", []store.Pane{{ID: "orrery-1", Pane: "%1"}}, time.Unix(1_700_000_000, 0))
	}
	for _, name := range []string{"first", "second"} {
		path := filepath.Join(t.TempDir(), name, "orrery.db")
		storetest.SeedFilled(t, path, "storetest one pane", fill)
		snap, err := store.NewReader(path).Snapshot(t.Context())
		if err != nil {
			t.Fatalf("Snapshot of the %s seeded store: %v", name, err)
		}
		if len(snap.Sessions) != 1 || snap.Sessions[0].ID != "orrery-1" {
			t.Fatalf("%s seeded store holds %+v, want the one pane the fill wrote", name, snap.Sessions)
		}
	}
	if got := fills.Load(); got != 1 {
		t.Fatalf("fill ran %d times for one key, want once", got)
	}
}
