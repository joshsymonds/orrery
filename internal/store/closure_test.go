package store_test

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

func TestInboxClosureVerdictStoredReadsBack(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openArchiveStore(t, path)
	want := store.ClosureVerdict{Session: "orrery-41", Changed: 1700000100, Closed: true, Why: "the owner closed the book"}

	if err := s.SetClosureVerdict(ctx, want, time.Unix(1700000200, 0)); err != nil {
		t.Fatalf("SetClosureVerdict: %v", err)
	}
	got, err := store.NewReader(path).ClosureVerdicts(ctx)
	if err != nil {
		t.Fatalf("ClosureVerdicts: %v", err)
	}
	if !reflect.DeepEqual(got, []store.ClosureVerdict{want}) {
		t.Fatalf("ClosureVerdicts = %+v, want [%+v]", got, want)
	}
}

func TestInboxClosureVerdictSecondReplacesFirst(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openArchiveStore(t, path)
	first := store.ClosureVerdict{Session: "orrery-42", Changed: 1700000100, Closed: false, Why: "still open"}
	second := store.ClosureVerdict{Session: "orrery-42", Changed: 1700000400, Closed: true, Why: "closed since"}

	if err := s.SetClosureVerdict(ctx, first, time.Unix(1700000200, 0)); err != nil {
		t.Fatalf("SetClosureVerdict first: %v", err)
	}
	if err := s.SetClosureVerdict(ctx, second, time.Unix(1700000500, 0)); err != nil {
		t.Fatalf("SetClosureVerdict second: %v", err)
	}
	got, err := store.NewReader(path).ClosureVerdicts(ctx)
	if err != nil {
		t.Fatalf("ClosureVerdicts: %v", err)
	}
	if !reflect.DeepEqual(got, []store.ClosureVerdict{second}) {
		t.Fatalf("ClosureVerdicts = %+v, want only the second verdict [%+v]", got, second)
	}
}

func TestInboxClosureVerdictEmptySessionIsRefused(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openArchiveStore(t, path)

	err := s.SetClosureVerdict(ctx, store.ClosureVerdict{Session: "", Why: "no session"}, time.Unix(1700000200, 0))
	if !errors.Is(err, store.ErrEmptyRowID) {
		t.Fatalf("SetClosureVerdict with an empty session = %v, want ErrEmptyRowID", err)
	}
	got, err := store.NewReader(path).ClosureVerdicts(ctx)
	if err != nil {
		t.Fatalf("ClosureVerdicts: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("ClosureVerdicts = %+v after a refused verdict, want none", got)
	}
}

func TestInboxClosureVerdictClosureSessionReadsEngineSession(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openArchiveStore(t, path)
	idle := "idle"

	if err := s.ApplyEngine(ctx, []store.EngineOp{
		store.EngineAdd{ID: "engine-43", Dir: "/work/inbox", State: "working"},
	}, time.Unix(1700000000, 0)); err != nil {
		t.Fatalf("ApplyEngine add: %v", err)
	}
	if err := s.ApplyEngine(ctx, []store.EngineOp{
		store.EngineSet{ID: "engine-43", Changes: store.EngineChanges{State: &idle}},
	}, time.Unix(1700000300, 0)); err != nil {
		t.Fatalf("ApplyEngine set: %v", err)
	}

	got, found, err := s.ClosureSession(ctx, "engine-43")
	if err != nil {
		t.Fatalf("ClosureSession engine-43: %v", err)
	}
	if !found || got != (store.ClosureSession{State: "idle", Changed: 1700000300}) {
		t.Fatalf("ClosureSession engine-43 = %+v, found %t, want idle changed at 1700000300", got, found)
	}

	got, found, err = s.ClosureSession(ctx, "missing-44")
	if err != nil {
		t.Fatalf("ClosureSession missing-44: %v", err)
	}
	if found || got != (store.ClosureSession{}) {
		t.Fatalf("ClosureSession missing-44 = %+v, found %t, want no session", got, found)
	}
}
