package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

// TestReadsRacingRemovalAnswerUnavailable removes the store file while readers open and read it. Every read that
// fails must match ErrUnavailable, because a lost store is reported the same way whether the file is gone before
// the read or disappears during it.
func TestReadsRacingRemovalAnswerUnavailable(t *testing.T) {
	t.Parallel()
	// The 300 rounds run as four groups of 75, each over its own store file and reader, so the rounds of one group
	// race removal while the other groups' rounds run beside them.
	const groups = 4
	const roundsPerGroup = 75
	for group := range groups {
		t.Run(strconv.Itoa(group), func(t *testing.T) {
			t.Parallel()
			readsRacingRemoval(t, group*roundsPerGroup, roundsPerGroup)
		})
	}
}

// readsRacingRemoval runs rounds numbered first to first+rounds-1. Each round seeds a store file, writes one pane,
// then removes the file while a reader reads it readsPerRound times; every read that fails must match
// ErrUnavailable.
func readsRacingRemoval(t *testing.T, first, rounds int) {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	now := time.Unix(1_700_000_000, 0)
	const readsPerRound = 20

	r := NewReader(path)
	for round := first; round < first+rounds; round++ {
		// Each round's store starts as a copy of the migrated image, so the round costs its reads, not a migration.
		seedMigrated(t, path)
		s, err := Open(ctx, path, noSync())
		if err != nil {
			t.Fatalf("round %d Open: %v", round, err)
		}
		pane := Pane{ID: "orrery-" + strconv.Itoa(round), Pane: "%1"}
		if err = s.SyncPane(ctx, "-L a", pane, now); err != nil {
			t.Fatalf("round %d SyncPane: %v", round, err)
		}

		start := make(chan struct{})
		var wg sync.WaitGroup
		var readErrs []error
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := range readsPerRound {
				var readErr error
				if i%2 == 0 {
					_, readErr = r.Snapshot(ctx)
				} else {
					_, readErr = r.Events(ctx, 0, 10)
				}
				if readErr != nil && !errors.Is(readErr, ErrUnavailable) {
					readErrs = append(readErrs, readErr)
				}
			}
		}()

		close(start)
		if err = os.Remove(path); err != nil {
			t.Errorf("round %d remove store: %v", round, err)
		}
		wg.Wait()
		if err = s.Close(); err != nil {
			t.Errorf("round %d Close writer: %v", round, err)
		}
		for _, readErr := range readErrs {
			t.Errorf("round %d read racing removal: %v, want ErrUnavailable", round, readErr)
		}
		if t.Failed() {
			return
		}
	}
}
