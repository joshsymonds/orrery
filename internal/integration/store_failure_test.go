package integration_test

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// storeUnavailableLog is the message orreryd logs when it serves hooks and notifications without its store.
const storeUnavailableLog = "store unavailable"

// mirrorDroppedLog is the message orreryd logs when a store write is dropped.
const mirrorDroppedLog = "mirror sync dropped"

// corruptStoreBytes is not a SQLite database. A store file holding it must stay byte-identical.
const corruptStoreBytes = "this is not a sqlite database, it is plain text kept for the test\n"

// noticeWithin returns the next notice the stream sent, failing the test unless it arrives within limit of since.
func noticeWithin(t *testing.T, r *noticeReader, since time.Time, limit time.Duration) notice {
	t.Helper()
	got := noticeOf(t, r.receive(t, time.Until(since.Add(limit))))
	if elapsed := time.Since(since); elapsed > limit {
		t.Fatalf("notice arrived after %v, want within %v", elapsed, limit)
	}
	return got
}

// assertHooksStillReach sends a PermissionRequest and fails unless the pane reads waiting and its waiting notice
// arrives within one second of the send. It then sends a Stop and fails unless the pane reads done.
func (f *storeFixture) assertHooksStillReach(t *testing.T) {
	t.Helper()
	sent := time.Now()
	f.hook(t, f.permissionHook())
	waitForOption(t, f.client, f.pane, protocol.OptionState, string(protocol.StateWaiting))
	waiting := noticeWithin(t, f.notices, sent, time.Second)
	question := readOption(t, f.client, f.pane, protocol.OptionQuestion)
	want := notice{state: string(protocol.StateWaiting), title: exampleTitle, body: question}
	if question == "" || waiting != want {
		t.Fatalf("waiting notice %+v, want state waiting, title %q, body %q", waiting, exampleTitle, question)
	}

	f.hook(t, f.stopHook())
	waitForOption(t, f.client, f.pane, protocol.OptionState, string(protocol.StateDone))
}

// prepareStore creates a migrated store at path and closes it.
func prepareStore(t *testing.T, path string) {
	t.Helper()
	s, err := store.Open(t.Context(), path)
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
	f := newStoreFixture(t)
	daemon := f.startOrreryd(t)

	f.assertHooksStillReach(t)

	wantExit(t, daemon.stop(t), 0)
	if got := fileMode(t, f.dbPath); got != 0o600 {
		t.Fatalf("created store mode = %o, want 600", got)
	}
}

func TestOrrerydServesHooksWithCorruptStore(t *testing.T) {
	f := newStoreFixture(t)
	if err := os.MkdirAll(filepath.Dir(f.dbPath), 0o700); err != nil {
		t.Fatalf("make store directory: %v", err)
	}
	if err := os.WriteFile(f.dbPath, []byte(corruptStoreBytes), 0o600); err != nil {
		t.Fatalf("write corrupt store: %v", err)
	}
	daemon := f.startOrreryd(t)

	f.assertHooksStillReach(t)

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
	f := newStoreFixture(t)
	prepareStore(t, f.dbPath)
	db, err := sql.Open("sqlite", f.dbPath)
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

	f.assertHooksStillReach(t)

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
	f := newStoreFixture(t)
	prepareStore(t, f.dbPath)
	daemon := f.startOrreryd(t)
	release := holdWriteLock(t, f.dbPath)

	f.assertHooksStillReach(t)

	// Serve waits for every in-flight frame before it returns, so each sync that the lock fails has been logged by the
	// time stop returns. The lock is released only after that.
	out := daemon.stop(t)
	release()
	wantExit(t, out, 0)
	if !strings.Contains(out.stderr, mirrorDroppedLog) {
		t.Fatalf("stderr %q, want the locked write logged as dropped", out.stderr)
	}
	if strings.Contains(out.stderr, storeUnavailableLog) {
		t.Fatalf("stderr %q: the migrated store should open", out.stderr)
	}
}
