package store

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// applyDirsOps applies ops at the unix second offset seconds from engineNowAt.
func applyDirsOps(t *testing.T, s *Store, offset int64, ops ...EngineOp) {
	t.Helper()
	at := time.Unix(engineNowAt().Unix()+offset, 0)
	if err := s.ApplyEngine(context.Background(), ops, at); err != nil {
		t.Fatalf("ApplyEngine at offset %d: %v", offset, err)
	}
}

// stepWorkspaceOf is a step whose workspace is workspace; its worktree is the usual /wt/<id>.
func stepWorkspaceOf(id, workspace string) StepAdd {
	op := stepAddOf(id)
	op.Workspace = workspace
	return op
}

func assertDirs(t *testing.T, got, want []string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("dirs: got %q, want %q", got, want)
	}
}

func TestDirsListsEngineDirsAndStepWorkspacesNewestFirst(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openWriterAt(t, path)
	applyDirsOps(t, s, -300, EngineAdd{ID: "e-old", Dir: "/engine-old", State: "idle"})
	applyDirsOps(t, s, -200, stepWorkspaceOf("s1", "/ws/s1"))
	applyDirsOps(t, s, -100, EngineAdd{ID: "e-mid", Dir: "/engine-mid", State: "idle"})
	applyDirsOps(t, s, 0, stepWorkspaceOf("s2", "/ws/s2"))

	got, err := NewReader(path).Dirs(context.Background())
	if err != nil {
		t.Fatalf("Dirs: %v", err)
	}
	assertDirs(t, got, []string{"/ws/s2", "/engine-mid", "/ws/s1", "/engine-old"})
}

func TestDirsListsEachDirOnceAtItsNewestPosition(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openWriterAt(t, path)
	applyDirsOps(t, s, -500, EngineAdd{ID: "e-x", Dir: "/x", State: "idle"})
	applyDirsOps(t, s, -400, EngineAdd{ID: "e-y", Dir: "/y", State: "idle"})
	applyDirsOps(t, s, -350, stepWorkspaceOf("s1", "/y"))
	applyDirsOps(t, s, -100, stepWorkspaceOf("s2", "/x"))

	got, err := NewReader(path).Dirs(context.Background())
	if err != nil {
		t.Fatalf("Dirs: %v", err)
	}
	assertDirs(t, got, []string{"/x", "/y"})
}

func TestDirsBreaksTimeTiesByDirAscending(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openWriterAt(t, path)
	applyDirsOps(t, s, 0,
		EngineAdd{ID: "e-b", Dir: "/b", State: "idle"},
		stepWorkspaceOf("s-a", "/a"),
		EngineAdd{ID: "e-c", Dir: "/c", State: "idle"},
	)

	got, err := NewReader(path).Dirs(context.Background())
	if err != nil {
		t.Fatalf("Dirs: %v", err)
	}
	assertDirs(t, got, []string{"/a", "/b", "/c"})
}

func TestDirsOmitsWorktreesPaneSessionsAndEmptyDirs(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openWriterAt(t, path)
	applyDirsOps(t, s, -100,
		EngineAdd{ID: "e-kept", Dir: "/kept", State: "idle"},
		EngineAdd{ID: "e-nodir", Dir: "", State: "idle"},
		EngineAdd{ID: "step-sess", Dir: "/wt/s1", State: "idle", Kind: KindStep},
	)
	applyDirsOps(t, s, -50,
		stepWorkspaceOf("s1", "/ws/s1"),
		stepWorkspaceOf("s2", ""),
	)
	if err := s.SyncServer(ctx, "-L a", []Pane{
		{ID: "orrery-1", Pane: "%1", Fields: Fields{State: "idle"}},
	}, time.Unix(engineNowAt().Unix()-100, 0)); err != nil {
		t.Fatalf("SyncServer: %v", err)
	}
	// Pane sessions never get a dir from the writer; set one so the kind filter is what excludes this row.
	if _, err := s.db.ExecContext(ctx, `UPDATE sessions SET dir = '/pane' WHERE id = 'orrery-1'`); err != nil {
		t.Fatalf("set pane dir: %v", err)
	}

	got, err := NewReader(path).Dirs(ctx)
	if err != nil {
		t.Fatalf("Dirs: %v", err)
	}
	assertDirs(t, got, []string{"/ws/s1", "/kept"})
}

func TestDirsReturnsUnavailableWhenStoreIsMissing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "absent")
	path := filepath.Join(dir, "orrery.db")

	_, err := NewReader(path).Dirs(context.Background())
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Dirs of missing store: got %v, want ErrUnavailable", err)
	}
	if _, statErr := os.Stat(dir); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("missing store's directory was created or unreadable: %v", statErr)
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("missing store file was created or unreadable: %v", statErr)
	}
}

func TestDirsLeavesDatabaseBytesUnchanged(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openWriterAt(t, path)
	applyDirsOps(t, s, -100, EngineAdd{ID: "e1", Dir: "/a", State: "idle"})
	applyDirsOps(t, s, -50, stepWorkspaceOf("s1", "/ws/s1"))
	// Checkpoint so every committed page is in the main file the comparison reads.
	if _, err := s.db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read store before: %v", err)
	}

	r := NewReader(path)
	for range 2 {
		got, err := r.Dirs(ctx)
		if err != nil {
			t.Fatalf("Dirs: %v", err)
		}
		assertDirs(t, got, []string{"/ws/s1", "/a"})
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read store after: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("store bytes changed by Dirs: %d bytes before, %d after", len(before), len(after))
	}
}
