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

// openArchiveStore opens a migrated store file at path that holds no rows, and closes it when the test ends.
func openArchiveStore(t *testing.T, path string) *store.Store {
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

// assertMarks fails unless the reader at path lists exactly want, in order.
func assertMarks(t *testing.T, path string, want []store.ArchiveMark) {
	t.Helper()
	got, err := store.NewReader(path).ArchivedRows(context.Background())
	if err != nil {
		t.Fatalf("ArchivedRows: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("ArchivedRows = %+v, want %+v", got, want)
	}
	for i := range want {
		g, w := got[i], want[i]
		sameTimes := g.ArchivedAt.Equal(w.ArchivedAt) && g.UnarchivedAt.Equal(w.UnarchivedAt)
		if g.Kind != w.Kind || g.ID != w.ID || !sameTimes {
			t.Errorf("ArchivedRows[%d] = %+v, want %+v", i, g, w)
		}
	}
}

func TestArchiveRowsListsArchivedRows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openArchiveStore(t, path)
	at := time.Unix(1700000000, 0)

	for _, row := range []struct{ kind, id string }{
		{"epic", "orrery-0668c87/epic-archived"},
		{"session", "orrery-1"},
		{"headless", "orrery-2"},
	} {
		if err := s.ArchiveRow(ctx, row.kind, row.id, at); err != nil {
			t.Fatalf("ArchiveRow %s %s: %v", row.kind, row.id, err)
		}
	}
	assertMarks(t, path, []store.ArchiveMark{
		{Kind: "epic", ID: "orrery-0668c87/epic-archived", ArchivedAt: at},
		{Kind: "headless", ID: "orrery-2", ArchivedAt: at},
		{Kind: "session", ID: "orrery-1", ArchivedAt: at},
	})
}

func TestArchiveRowsUnarchiveClearsMarkAndRecordsTime(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openArchiveStore(t, path)
	const id = "orrery-0668c87/epic-unarchived"
	archived := time.Unix(1700000000, 0)
	unarchived := time.Unix(1700000500, 0)

	if err := s.ArchiveRow(ctx, "epic", id, archived); err != nil {
		t.Fatalf("ArchiveRow: %v", err)
	}
	if err := s.UnarchiveRow(ctx, "epic", id, unarchived); err != nil {
		t.Fatalf("UnarchiveRow: %v", err)
	}
	assertMarks(t, path, []store.ArchiveMark{{Kind: "epic", ID: id, UnarchivedAt: unarchived}})

	rearchived := time.Unix(1700001000, 0)
	if err := s.ArchiveRow(ctx, "epic", id, rearchived); err != nil {
		t.Fatalf("ArchiveRow again: %v", err)
	}
	assertMarks(t, path, []store.ArchiveMark{{Kind: "epic", ID: id, ArchivedAt: rearchived}})
}

func TestArchiveRowsMarkSurvivesReopen(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	storetest.Seed(t, path)
	at := time.Unix(1700000000, 0)

	s, err := store.Open(ctx, path, store.NoSync())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.ArchiveRow(ctx, "session", "orrery-1", at); err != nil {
		t.Fatalf("ArchiveRow: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := store.Open(ctx, path, store.NoSync())
	if err != nil {
		t.Fatalf("Open after close: %v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatalf("Close after reopen: %v", err)
	}
	assertMarks(t, path, []store.ArchiveMark{{Kind: "session", ID: "orrery-1", ArchivedAt: at}})
}

func TestArchiveRowsArchivesEpicWithOnlyARecord(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openArchiveStore(t, path)
	const id = "orrery-0668c87/record-only"
	at := time.Unix(1700000000, 0)

	if _, err := s.Epic(ctx, id); !errors.Is(err, store.ErrNoEpic) {
		t.Fatalf("Epic of a record-only id = %v, want ErrNoEpic", err)
	}
	if err := s.ArchiveRow(ctx, "epic", id, at); err != nil {
		t.Fatalf("ArchiveRow of a record-only epic: %v", err)
	}
	assertMarks(t, path, []store.ArchiveMark{{Kind: "epic", ID: id, ArchivedAt: at}})
}

func TestArchiveRowsRefusesUnknownKindAndEmptyID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openArchiveStore(t, path)
	at := time.Unix(1700000000, 0)

	for _, tc := range []struct {
		kind, id string
		want     error
	}{
		{"record", "orrery-1", store.ErrBadRowKind},
		{"pane", "%1", store.ErrBadRowKind},
		{"", "orrery-1", store.ErrBadRowKind},
		{"epic", "", store.ErrEmptyRowID},
		{"session", "", store.ErrEmptyRowID},
	} {
		if err := s.ArchiveRow(ctx, tc.kind, tc.id, at); !errors.Is(err, tc.want) {
			t.Errorf("ArchiveRow(%q, %q) = %v, want %v", tc.kind, tc.id, err, tc.want)
		}
		if err := s.UnarchiveRow(ctx, tc.kind, tc.id, at); !errors.Is(err, tc.want) {
			t.Errorf("UnarchiveRow(%q, %q) = %v, want %v", tc.kind, tc.id, err, tc.want)
		}
	}
	assertMarks(t, path, nil)
}
