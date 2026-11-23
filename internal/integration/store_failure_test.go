package integration_test

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
	"github.com/joshsymonds/orrery/internal/storetest"
)

// storeUnavailableLog is the message orreryd logs when it serves hooks and notifications without its store.
const storeUnavailableLog = "store unavailable"

// storeOpenLog is the log line orreryd writes once its store is open and migrated, before its first tidy.
const storeOpenLog = `msg="store open"`

// mirrorDroppedLog is the message orreryd logs when a store write is dropped.
const mirrorDroppedLog = "mirror sync dropped"

// mirrorBusyLog is the reason orreryd logs when it defers a mirror sync or tidy whose store write failed busy.
const mirrorBusyLog = `reason="store busy"`

// corruptStoreBytes is not a SQLite database. A store file holding it must stay byte-identical.
const corruptStoreBytes = "this is not a sqlite database, it is plain text kept for the test\n"

// assertHooksStillReach requests a permission and fails unless the pane reads waiting and its waiting notice arrives. It
// then finishes the turn and fails unless the pane reads done.
func (f *storeFixture) assertHooksStillReach(t *testing.T, daemon *orreryProcess) {
	t.Helper()
	f.session.requestPermission(t)
	waitForOption(t, daemon, f.client, f.pane, protocol.OptionState, string(protocol.StateWaiting))
	waiting := noticeOf(t, f.notices.receive(t))
	question := readOption(t, f.client, f.pane, protocol.OptionQuestion)
	want := notice{state: string(protocol.StateWaiting), title: exampleTitle, body: question}
	if question == "" || waiting != want {
		t.Fatalf("waiting notice %+v, want state waiting, title %q, body %q", waiting, exampleTitle, question)
	}

	f.session.finishTurn(t, exampleLastMessage)
	waitForOption(t, daemon, f.client, f.pane, protocol.OptionState, string(protocol.StateDone))
}

// lockedDrop returns the first line of stderr that logs a mirror sync dropped because the store was locked, or "" when
// none does. A sync whose tmux read the shutdown ends is dropped for another reason, which this does not count: the
// next start's tidy repairs that pane, and the locked store is what the write-locked tests are about.
func lockedDrop(stderr string) string {
	for line := range strings.SplitSeq(stderr, "\n") {
		if strings.Contains(line, mirrorDroppedLog) && strings.Contains(line, "SQLITE_BUSY") {
			return line
		}
	}
	return ""
}

// prepareStore creates a migrated store at path and closes it.
func prepareStore(t *testing.T, path string) {
	t.Helper()
	storetest.Seed(t, path)
	s, err := store.Open(t.Context(), path, storetest.NoSync())
	if err != nil {
		t.Fatalf("prepare store: %v", err)
	}
	if err = s.Close(); err != nil {
		t.Fatalf("close prepared store: %v", err)
	}
}

// holdWriteLock takes the write lock of the store at path on a second connection and returns the function that
// releases it.
func holdWriteLock(t *testing.T, path string) func() {
	t.Helper()
	db := openStoreForInspection(t, path)
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatalf("open lock connection: %v", err)
	}
	// orreryd's start-time tidy may be writing; wait for it instead of failing on SQLITE_BUSY.
	if _, err = conn.ExecContext(t.Context(), "PRAGMA busy_timeout = 5000"); err != nil {
		conn.Close()
		t.Fatalf("set lock connection busy timeout: %v", err)
	}
	if _, err = conn.ExecContext(t.Context(), "BEGIN IMMEDIATE"); err != nil {
		conn.Close()
		t.Fatalf("take write lock: %v", err)
	}
	return func() {
		if _, rollbackErr := conn.ExecContext(t.Context(), "ROLLBACK"); rollbackErr != nil {
			t.Errorf("release write lock: %v", rollbackErr)
		}
		if closeErr := conn.Close(); closeErr != nil {
			t.Errorf("close lock connection: %v", closeErr)
		}
	}
}

func TestOrrerydServesHooksWithMissingStore(t *testing.T) {
	t.Parallel()
	f := newStoreFixture(t)
	daemon := f.startOrreryd(t)

	f.assertHooksStillReach(t, daemon)

	wantExit(t, daemon.stop(t), 0)
	if got := fileMode(t, f.dbPath); got != 0o600 {
		t.Fatalf("created store mode = %o, want 600", got)
	}
}

func TestOrrerydServesHooksWithCorruptStore(t *testing.T) {
	t.Parallel()
	f := newStoreFixture(t)
	if err := os.MkdirAll(filepath.Dir(f.dbPath), 0o700); err != nil {
		t.Fatalf("make store directory: %v", err)
	}
	if err := os.WriteFile(f.dbPath, []byte(corruptStoreBytes), 0o600); err != nil {
		t.Fatalf("write corrupt store: %v", err)
	}
	daemon := f.startOrreryd(t)

	f.assertHooksStillReach(t, daemon)

	out := daemon.stop(t)
	wantExit(t, out, 0)
	if !strings.Contains(out.stderr, storeUnavailableLog) {
		t.Fatalf("stderr %q, want a log that the store is unavailable", out.stderr)
	}
	got, err := os.ReadFile(f.dbPath)
	if err != nil {
		t.Fatalf("read corrupt store: %v", err)
	}
	if !bytes.Equal(got, []byte(corruptStoreBytes)) {
		t.Fatalf("corrupt store was modified: got %q", got)
	}
}

func TestOrrerydServesHooksWithTooNewStore(t *testing.T) {
	t.Parallel()
	f := newStoreFixture(t)
	prepareStore(t, f.dbPath)
	db, err := sql.Open("sqlite3", f.dbPath)
	if err != nil {
		t.Fatalf("open store to age it: %v", err)
	}
	// A store written by a newer binary records a migration this binary does not embed.
	if _, err = db.ExecContext(t.Context(),
		`INSERT INTO goose_db_version (version_id, is_applied) VALUES (99, 1)`); err != nil {
		db.Close()
		t.Fatalf("record newer migration: %v", err)
	}
	if err = db.Close(); err != nil {
		t.Fatalf("close aged store: %v", err)
	}
	before, err := os.ReadFile(f.dbPath)
	if err != nil {
		t.Fatalf("read newer store: %v", err)
	}
	daemon := f.startOrreryd(t)

	f.assertHooksStillReach(t, daemon)

	out := daemon.stop(t)
	wantExit(t, out, 0)
	if !strings.Contains(out.stderr, storeUnavailableLog) {
		t.Fatalf("stderr %q, want a log that the store is unavailable", out.stderr)
	}
	after, err := os.ReadFile(f.dbPath)
	if err != nil {
		t.Fatalf("read newer store after: %v", err)
	}
	if !bytes.Equal(after, before) {
		t.Fatalf("newer-schema store was modified")
	}
}

func TestOrrerydServesHooksWithWriteLockedStore(t *testing.T) {
	t.Parallel()
	f := newStoreFixture(t)
	prepareStore(t, f.dbPath)
	// A busy timeout of 0 fails each locked write at once, so the test waits out no timeout.
	daemon := f.startOrreryd(t, "--store-busy-timeout", "0s")
	// The lock is taken once orreryd has opened the store. Taken during the open, it would make the open fail.
	daemon.waitForStoreOpen(t)
	release := holdWriteLock(t, f.dbPath)

	f.assertHooksStillReach(t, daemon)

	// The lock is released once a sync it refused has been logged as deferred, and orreryd stops after that, so its stop
	// reruns the deferred sync, and writes the notice rows, to an unlocked store.
	waitUntil(t, func() bool { return strings.Contains(daemon.stderr.String(), mirrorBusyLog) })
	release()
	out := daemon.stop(t)
	wantExit(t, out, 0)
	if !strings.Contains(out.stderr, mirrorBusyLog) {
		t.Fatalf("stderr %q, want the locked write logged as deferred", out.stderr)
	}
	if line := lockedDrop(out.stderr); line != "" {
		t.Fatalf("stderr logs a locked write dropped: %q, want it deferred", line)
	}
	if strings.Contains(out.stderr, storeUnavailableLog) {
		t.Fatalf("stderr %q: the migrated store should open", out.stderr)
	}
	rows, err := sqlcgen.New(openStoreForInspection(t, f.dbPath)).ListSessions(t.Context())
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(rows) != 1 || rows[0].Pane != f.pane || rows[0].State != string(protocol.StateDone) {
		t.Fatalf("session rows after the lock freed = %+v, want pane %s stored done", rows, f.pane)
	}
}
