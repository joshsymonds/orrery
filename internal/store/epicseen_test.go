package store_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/storetest"
)

// seenEpic is the released epic the seen tests mark, and seenBuilding an accepted epic they leave unmarked.
const (
	seenEpic     = "orrery-7d8e9f0/epic-seen"
	seenBuilding = "orrery-7d8e9f0/epic-building"
)

// openSeenStore opens a migrated store file that holds seenEpic, released, and seenBuilding, accepted, both created at
// the unix second 1700000000.
func openSeenStore(t *testing.T) *store.Store {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	storetest.Seed(t, path)
	s, err := store.Open(ctx, path, store.NoSync())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	created := time.Unix(1700000000, 0)
	for _, row := range []store.EpicRow{
		{ID: seenEpic, Phase: "released"},
		{ID: seenBuilding, Phase: "accepted"},
	} {
		row.Repository, row.Title, row.Intent = "/work/repo", "Seen epic", "intent"
		row.Care, row.Approach, row.ReleaseThen, row.Checks = "serious", "approach", "then", "checks"
		row.Created, row.Changed = created, created
		if err := s.CreateEpic(ctx, row); err != nil {
			t.Fatalf("CreateEpic %s: %v", row.ID, err)
		}
	}
	return s
}

// seenOf returns the Seen of every epic Epics lists, keyed by epic id.
func seenOf(t *testing.T, s *store.Store) map[string]time.Time {
	t.Helper()
	epics, err := s.Epics(context.Background())
	if err != nil {
		t.Fatalf("Epics: %v", err)
	}
	seen := map[string]time.Time{}
	for _, row := range epics {
		seen[row.ID] = row.Seen
	}
	return seen
}

func TestSeeEpicReadsBackOnEpicAndEpics(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openSeenStore(t)

	got, err := s.Epic(ctx, seenEpic)
	if err != nil {
		t.Fatalf("Epic before SeeEpic: %v", err)
	}
	if !got.Seen.IsZero() {
		t.Errorf("Seen of an unmarked epic = %v, want zero", got.Seen)
	}

	mark := time.Unix(1700001000, 0)
	if err := s.SeeEpic(ctx, seenEpic, mark); err != nil {
		t.Fatalf("SeeEpic: %v", err)
	}
	got, err = s.Epic(ctx, seenEpic)
	if err != nil {
		t.Fatalf("Epic after SeeEpic: %v", err)
	}
	if !got.Seen.Equal(mark) {
		t.Errorf("Epic Seen = %v, want %v", got.Seen, mark)
	}
	seen := seenOf(t, s)
	if !seen[seenEpic].Equal(mark) {
		t.Errorf("Epics Seen of %s = %v, want %v", seenEpic, seen[seenEpic], mark)
	}
	if !seen[seenBuilding].IsZero() {
		t.Errorf("Epics Seen of unmarked %s = %v, want zero", seenBuilding, seen[seenBuilding])
	}
}

func TestSeeEpicSecondMarkReplacesTheFirst(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openSeenStore(t)

	first := time.Unix(1700001000, 0)
	second := time.Unix(1700002000, 0)
	if err := s.SeeEpic(ctx, seenEpic, first); err != nil {
		t.Fatalf("SeeEpic first: %v", err)
	}
	if err := s.SeeEpic(ctx, seenEpic, second); err != nil {
		t.Fatalf("SeeEpic second: %v", err)
	}
	got, err := s.Epic(ctx, seenEpic)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	if !got.Seen.Equal(second) {
		t.Errorf("Epic Seen after two marks = %v, want the second %v", got.Seen, second)
	}
	if seen := seenOf(t, s); !seen[seenEpic].Equal(second) {
		t.Errorf("Epics Seen after two marks = %v, want the second %v", seen[seenEpic], second)
	}
}

func TestSeeEpicUnknownEpicIsErrNoEpic(t *testing.T) {
	t.Parallel()
	s := openSeenStore(t)

	err := s.SeeEpic(context.Background(), "orrery-7d8e9f0/epic-missing", time.Unix(1700001000, 0))
	if !errors.Is(err, store.ErrNoEpic) {
		t.Fatalf("SeeEpic of an unknown epic: err = %v, want ErrNoEpic", err)
	}
}
