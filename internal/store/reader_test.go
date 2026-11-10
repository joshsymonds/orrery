package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// openWriterAt opens the daemon's writer at path and closes it when the test ends.
func openWriterAt(t *testing.T, path string) *Store {
	t.Helper()
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close writer: %v", err)
		}
	})
	return s
}

func snapshotOf(t *testing.T, r *Reader) Snapshot {
	t.Helper()
	snap, err := r.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	return snap
}

func assertSnapshot(t *testing.T, got Snapshot, wantSessions []Session, wantSeq int64) {
	t.Helper()
	if len(got.Sessions) != len(wantSessions) {
		t.Fatalf("sessions: got %+v, want %+v", got.Sessions, wantSessions)
	}
	for i := range wantSessions {
		if got.Sessions[i] != wantSessions[i] {
			t.Fatalf("session %d: got %+v, want %+v", i, got.Sessions[i], wantSessions[i])
		}
	}
	if got.Seq != wantSeq {
		t.Fatalf("seq: got %d, want %d", got.Seq, wantSeq)
	}
}

func TestSnapshotReturnsWrittenRowsAndNewestSeq(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openWriterAt(t, path)
	now := time.Unix(1_700_000_000, 0)

	// Two rows share the pane pair ("-L a", "%1"), as they can for ten seconds after a tmux restart.
	if err := s.SyncServer(ctx, "-L a", []Pane{
		{ID: "orrery-1", Pane: "%1", Fields: Fields{State: "working", Label: "one"}},
		{ID: "orrery-2", Pane: "%1", Fields: Fields{State: "idle", Transcript: "t2"}},
	}, now); err != nil {
		t.Fatalf("SyncServer: %v", err)
	}
	if err := s.SyncPane(
		ctx,
		"-L b",
		Pane{ID: "orrery-3", Pane: "%7", Fields: Fields{Question: "q3"}},
		now,
	); err != nil {
		t.Fatalf("SyncPane: %v", err)
	}
	events := eventsOf(t, s)

	snap := snapshotOf(t, NewReader(path))
	assertSnapshot(t, snap, []Session{
		{ID: "orrery-1", Server: "-L a", Pane: "%1", Fields: Fields{State: "working", Label: "one"}},
		{ID: "orrery-2", Server: "-L a", Pane: "%1", Fields: Fields{State: "idle", Transcript: "t2"}},
		{ID: "orrery-3", Server: "-L b", Pane: "%7", Fields: Fields{Question: "q3"}},
	}, events[len(events)-1].Seq)
}

func TestSnapshotSeqIsZeroWithNoEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.db")
	openWriterAt(t, path)

	assertSnapshot(t, snapshotOf(t, NewReader(path)), nil, 0)
}

func TestSnapshotIsConsistentWhileWriterWrites(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openWriterAt(t, path)
	r := NewReader(path)
	now := time.Unix(1_700_000_000, 0)
	const panes = 300

	writerErr := make(chan error, 1)
	go func() {
		for i := range panes {
			// A new empty pane appends exactly one session_added event, so the sessions and events stay in step.
			pane := Pane{ID: "orrery-" + strconv.Itoa(i), Pane: "%" + strconv.Itoa(i)}
			if err := s.SyncPane(ctx, "-L a", pane, now); err != nil {
				writerErr <- err
				return
			}
		}
		writerErr <- nil
	}()

	var lastSeq int64
	reads := 0
	for done := false; !done; {
		select {
		case err := <-writerErr:
			if err != nil {
				t.Fatalf("writer: %v", err)
			}
			done = true
		default:
		}
		snap := snapshotOf(t, r)
		if int64(len(snap.Sessions)) != snap.Seq {
			t.Fatalf("snapshot not consistent: %d sessions at seq %d", len(snap.Sessions), snap.Seq)
		}
		if snap.Seq < lastSeq {
			t.Fatalf("seq went backwards: %d after %d", snap.Seq, lastSeq)
		}
		lastSeq = snap.Seq
		reads++
	}
	if reads == 0 {
		t.Fatal("no snapshots taken")
	}
	if final := snapshotOf(t, r); final.Seq != panes || len(final.Sessions) != panes {
		t.Fatalf("final snapshot: %d sessions at seq %d, want %d", len(final.Sessions), final.Seq, panes)
	}
}

func TestSnapshotReturnsUnavailableWhenStoreIsMissing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "absent")
	path := filepath.Join(dir, "orrery.db")

	_, err := NewReader(path).Snapshot(context.Background())
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Snapshot of missing store: got %v, want ErrUnavailable", err)
	}
	if _, statErr := os.Stat(dir); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("missing store's directory was created or unreadable: %v", statErr)
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("missing store file was created or unreadable: %v", statErr)
	}
}

func TestSnapshotReturnsUnavailableWhileStoreIsWriteLocked(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err = s.SyncPane(ctx, "-L a", Pane{ID: "orrery-1", Pane: "%1", Fields: Fields{State: "working"}},
		time.Unix(1, 0)); err != nil {
		t.Fatalf("SyncPane: %v", err)
	}
	events := eventsOf(t, s)
	seq := events[len(events)-1].Seq
	// The writer closes first: its idle connection would keep the file shared and block exclusive locking.
	if err = s.Close(); err != nil {
		t.Fatalf("Close writer: %v", err)
	}

	// An exclusive-locking connection holds the file against other connections, so readers cannot read it.
	holder, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open lock holder: %v", err)
	}
	t.Cleanup(func() {
		if err := holder.Close(); err != nil {
			t.Errorf("close lock holder: %v", err)
		}
	})
	conn, err := holder.Conn(ctx)
	if err != nil {
		t.Fatalf("lock holder connection: %v", err)
	}
	if _, err = conn.ExecContext(ctx, "PRAGMA locking_mode=EXCLUSIVE"); err != nil {
		t.Fatalf("set exclusive locking: %v", err)
	}
	if _, err = conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatalf("take write lock: %v", err)
	}
	if _, err = conn.ExecContext(
		ctx,
		"INSERT INTO events (session_id, kind, at, payload) VALUES ('x', 'k', 0, '{}')",
	); err != nil {
		t.Fatalf("write while holding lock: %v", err)
	}

	r := NewReader(path)
	start := time.Now()
	_, err = r.Snapshot(ctx)
	elapsed := time.Since(start)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Snapshot while busy: got %v, want ErrUnavailable", err)
	}
	if elapsed >= time.Second {
		t.Fatalf("Snapshot while busy took %v, want under 1s", elapsed)
	}

	if _, err = conn.ExecContext(ctx, "ROLLBACK"); err != nil {
		t.Fatalf("release write lock: %v", err)
	}
	if err = conn.Close(); err != nil {
		t.Fatalf("close lock holder connection: %v", err)
	}
	// The exclusive connection keeps the file locked until its database handle closes, not when it returns to
	// the pool.
	if err = holder.Close(); err != nil {
		t.Fatalf("close lock holder: %v", err)
	}
	assertSnapshot(t, snapshotOf(t, r), []Session{
		{ID: "orrery-1", Server: "-L a", Pane: "%1", Fields: Fields{State: "working"}},
	}, seq)
}

func TestSnapshotReopensReplacedStore(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	now := time.Unix(1_700_000_000, 0)

	old, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open old: %v", err)
	}
	if err = old.SyncPane(ctx, "-L a", Pane{ID: "orrery-old", Pane: "%1"}, now); err != nil {
		t.Fatalf("SyncPane old: %v", err)
	}
	// The daemon stops before the reader first reads, so no WAL frames of the old file outlive it.
	if err = old.Close(); err != nil {
		t.Fatalf("Close old: %v", err)
	}
	r := NewReader(path)
	assertSnapshot(t, snapshotOf(t, r), []Session{{ID: "orrery-old", Server: "-L a", Pane: "%1"}}, 1)

	// A second store is built elsewhere, closed so its WAL is checkpointed, then renamed over the first.
	replacementPath := filepath.Join(t.TempDir(), "replacement.db")
	replacement, err := Open(ctx, replacementPath)
	if err != nil {
		t.Fatalf("Open replacement: %v", err)
	}
	if err = replacement.SyncServer(ctx, "-L b", []Pane{{ID: "orrery-new", Pane: "%9"}}, now); err != nil {
		t.Fatalf("SyncServer replacement: %v", err)
	}
	newest := eventsOf(t, replacement)
	if err = replacement.Close(); err != nil {
		t.Fatalf("Close replacement: %v", err)
	}
	if err = os.Rename(replacementPath, path); err != nil {
		t.Fatalf("rename replacement over store: %v", err)
	}

	assertSnapshot(t, snapshotOf(t, r), []Session{{ID: "orrery-new", Server: "-L b", Pane: "%9"}},
		newest[len(newest)-1].Seq)
}

// TestSnapshotReplacedWhileReaderHasReadAndWriterOpen covers the order a daemon restart produces: the reader reads
// while the writer is still open, the writer closes, and another store is renamed over the path. The writer must
// be the last connection when it closes, so its WAL is checkpointed and removed; a WAL left behind would be replayed
// onto the new file.
func TestSnapshotReplacedWhileReaderHasReadAndWriterOpen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	now := time.Unix(1_700_000_000, 0)

	old, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open old: %v", err)
	}
	if err = old.SyncPane(ctx, "-L a", Pane{ID: "orrery-old", Pane: "%1"}, now); err != nil {
		t.Fatalf("SyncPane old: %v", err)
	}
	r := NewReader(path)
	assertSnapshot(t, snapshotOf(t, r), []Session{{ID: "orrery-old", Server: "-L a", Pane: "%1"}}, 1)

	replacementPath := filepath.Join(t.TempDir(), "replacement.db")
	replacement, err := Open(ctx, replacementPath)
	if err != nil {
		t.Fatalf("Open replacement: %v", err)
	}
	if err = replacement.SyncServer(ctx, "-L b", []Pane{{ID: "orrery-new", Pane: "%9"}}, now); err != nil {
		t.Fatalf("SyncServer replacement: %v", err)
	}
	newest := eventsOf(t, replacement)
	if err = replacement.Close(); err != nil {
		t.Fatalf("Close replacement: %v", err)
	}

	if err = old.Close(); err != nil {
		t.Fatalf("Close old: %v", err)
	}
	if err = os.Rename(replacementPath, path); err != nil {
		t.Fatalf("rename replacement over store: %v", err)
	}

	assertSnapshot(t, snapshotOf(t, r), []Session{{ID: "orrery-new", Server: "-L b", Pane: "%9"}},
		newest[len(newest)-1].Seq)
}

// TestReaderOpenRejectsWrites opens the store the way Snapshot does and asserts that a write through that
// connection fails and leaves the events unchanged. It fails when the read-only mode is removed from the DSN.
func TestReaderOpenRejectsWrites(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openWriterAt(t, path)
	if err := s.SyncPane(ctx, "-L a", Pane{ID: "orrery-1", Pane: "%1"}, time.Unix(1, 0)); err != nil {
		t.Fatalf("SyncPane: %v", err)
	}
	eventsBefore := eventsOf(t, s)

	db, err := sql.Open("sqlite", readOnlyDSN(path))
	if err != nil {
		t.Fatalf("open reader database: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close reader database: %v", err)
		}
	})
	if _, err = db.ExecContext(
		ctx,
		"INSERT INTO events (session_id, kind, at, payload) VALUES ('x', 'k', 0, '{}')",
	); err == nil {
		t.Fatal("write through the reader's open succeeded, want a read-only error")
	}
	assertEvents(t, s, eventsBefore)
}

func TestSnapshotReopensStoreCreatedAfterMissingRead(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	r := NewReader(path)
	if _, err := r.Snapshot(ctx); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Snapshot before store exists: got %v, want ErrUnavailable", err)
	}

	s := openWriterAt(t, path)
	if err := s.SyncPane(ctx, "-L a", Pane{ID: "orrery-1", Pane: "%1", Fields: Fields{Tool: "bash"}},
		time.Unix(1, 0)); err != nil {
		t.Fatalf("SyncPane: %v", err)
	}
	events := eventsOf(t, s)
	assertSnapshot(t, snapshotOf(t, r), []Session{
		{ID: "orrery-1", Server: "-L a", Pane: "%1", Fields: Fields{Tool: "bash"}},
	}, events[len(events)-1].Seq)
}

func TestSnapshotLeavesRowsAndEventsUnchanged(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openWriterAt(t, path)
	now := time.Unix(1_700_000_000, 0)
	if err := s.SyncServer(ctx, "-L a", []Pane{
		{ID: "orrery-1", Pane: "%1", Fields: Fields{State: "working"}},
		{ID: "orrery-2", Pane: "%2", Fields: Fields{State: "idle"}},
	}, now); err != nil {
		t.Fatalf("SyncServer: %v", err)
	}
	rowsBefore := sessionsOf(t, s)
	eventsBefore := eventsOf(t, s)

	r := NewReader(path)
	snapshotOf(t, r)
	snapshotOf(t, r)

	assertSessions(t, s, rowsBefore)
	assertEvents(t, s, eventsBefore)
}
