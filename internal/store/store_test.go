package store

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPathUsesXDGStateHome(t *testing.T) {
	t.Parallel()
	env := map[string]string{"XDG_STATE_HOME": "/xdg", "HOME": "/home/user"}
	got, err := Path(func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	want := filepath.Join("/xdg", "orrery", "orrery.db")
	if got != want {
		t.Fatalf("Path = %q, want %q", got, want)
	}
}

func TestPathFallsBackToHome(t *testing.T) {
	t.Parallel()
	env := map[string]string{"HOME": "/home/user"}
	got, err := Path(func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	want := filepath.Join("/home/user", ".local", "state", "orrery", "orrery.db")
	if got != want {
		t.Fatalf("Path = %q, want %q", got, want)
	}
}

func TestPathErrorsWhenNoHomeOrXDG(t *testing.T) {
	t.Parallel()
	if _, err := Path(func(string) string { return "" }); err == nil {
		t.Fatal("Path with neither XDG_STATE_HOME nor HOME: want error, got nil")
	}
}

func mode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info.Mode().Perm()
}

func tableNames(t *testing.T, s *Store) map[string]bool {
	t.Helper()
	rows, err := s.db.Query(`SELECT name FROM sqlite_master WHERE type = 'table'`)
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	defer rows.Close()
	names := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan table name: %v", err)
		}
		names[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate tables: %v", err)
	}
	return names
}

func TestOpenCreatesPrivateStoreAndMigrates(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "state", "orrery")
	path := filepath.Join(dir, "orrery.db")

	s, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	if got := mode(t, dir); got != 0o700 {
		t.Errorf("directory mode = %o, want 700", got)
	}
	if got := mode(t, path); got != 0o600 {
		t.Errorf("database mode = %o, want 600", got)
	}

	tables := tableNames(t, s)
	for _, name := range []string{"sessions", "events"} {
		if !tables[name] {
			t.Errorf("table %q missing after migration; have %v", name, tables)
		}
	}

	if _, err := s.fixtureExec(ctx, `INSERT INTO sessions (id, server, pane) VALUES ('s1', '', '%1')`); err != nil {
		t.Fatalf("insert session: %v", err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if got := mode(t, path+suffix); got != 0o600 {
			t.Errorf("%s mode = %o, want 600", suffix, got)
		}
	}
}

func goodRows(t *testing.T, path string) string {
	t.Helper()
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open for inspection: %v", err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT id, version_id, is_applied, tstamp FROM goose_db_version ORDER BY id`)
	if err != nil {
		t.Fatalf("read goose_db_version: %v", err)
	}
	defer rows.Close()
	var out string
	for rows.Next() {
		var id, version, applied int64
		var tstamp string
		if err := rows.Scan(&id, &version, &applied, &tstamp); err != nil {
			t.Fatalf("scan goose row: %v", err)
		}
		out += fmt.Sprintf("%d|%d|%d|%s\n", id, version, applied, tstamp)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate goose rows: %v", err)
	}
	return out
}

func TestOpenAlreadyMigratedStoreChangesNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")

	// The migrated image is a store Open created and fully migrated, closed, so it is already migrated.
	seedMigrated(t, path)
	before := goodRows(t, path)

	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open of the migrated store: %v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if after := goodRows(t, path); after != before {
		t.Fatalf("goose_db_version changed on reopen:\nbefore:\n%safter:\n%s", before, after)
	}
}

func TestOpenRejectsNonSQLiteFileUntouched(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	garbage := []byte("this is not a sqlite database, it is plain text kept for the test\n")
	if err := os.WriteFile(path, garbage, 0o600); err != nil {
		t.Fatalf("write garbage: %v", err)
	}

	s, err := Open(ctx, path)
	if err == nil {
		s.Close()
		t.Fatal("Open on non-SQLite bytes: want error, got nil")
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !bytes.Equal(got, garbage) {
		t.Fatalf("non-SQLite file was modified: got %q", got)
	}
}

func TestOpenRejectsNewerSchemaWithoutMigrating(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")

	// A store written by a newer binary: its goose version is 99 and none of this binary's migrations ran.
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	for _, stmt := range []string{
		`CREATE TABLE goose_db_version (id INTEGER PRIMARY KEY AUTOINCREMENT, version_id INTEGER NOT NULL,
			is_applied INTEGER NOT NULL, tstamp TIMESTAMP DEFAULT (datetime('now')))`,
		`INSERT INTO goose_db_version (version_id, is_applied) VALUES (0, 1), (99, 1)`,
	} {
		if _, err = db.ExecContext(ctx, stmt); err != nil {
			db.Close()
			t.Fatalf("write fixture: %v", err)
		}
	}
	if err = db.Close(); err != nil {
		t.Fatalf("close fixture: %v", err)
	}
	before := goodRows(t, path)

	reopened, err := Open(ctx, path)
	if err == nil {
		reopened.Close()
		t.Fatal("Open on newer schema: want error, got nil")
	}
	if !errors.Is(err, ErrTooNew) {
		t.Fatalf("Open error = %v, want ErrTooNew", err)
	}
	if after := goodRows(t, path); after != before {
		t.Fatalf("newer-schema store was modified:\nbefore:\n%safter:\n%s", before, after)
	}
	if tables := fixtureTables(t, path); tables["sessions"] || tables["events"] {
		t.Fatalf("newer-schema store was migrated: tables %v", tables)
	}
}

// fixtureTables returns the names of the tables in the database at path.
func fixtureTables(t *testing.T, path string) map[string]bool {
	t.Helper()
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open for inspection: %v", err)
	}
	defer db.Close()
	return tableNames(t, &Store{db: &pool{DB: db}})
}

// synchronousOf returns the store connection's PRAGMA synchronous: 0 OFF, 1 NORMAL, 2 FULL.
func synchronousOf(t *testing.T, s *Store) int {
	t.Helper()
	var mode int
	if err := s.db.QueryRowContext(context.Background(), `PRAGMA synchronous`).Scan(&mode); err != nil {
		t.Fatalf("PRAGMA synchronous: %v", err)
	}
	return mode
}

func TestOpenKeepsSynchronousFullByDefault(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "orrery.db")
	seedMigrated(t, path)
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	if got := synchronousOf(t, s); got != 2 {
		t.Fatalf("synchronous = %d, want 2 (FULL)", got)
	}
}

func TestOpenWithSynchronousSetsTheMode(t *testing.T) {
	t.Parallel()
	for mode, want := range map[Synchronous]int{SynchronousOff: 0, SynchronousNormal: 1, SynchronousFull: 2} {
		t.Run(string(mode), func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "orrery.db")
			seedMigrated(t, path)
			s, err := Open(context.Background(), path, WithSynchronous(mode))
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			defer s.Close()
			if got := synchronousOf(t, s); got != want {
				t.Fatalf("synchronous = %d, want %d (%s)", got, want, mode)
			}
		})
	}
}

// busyTimeoutOf returns the store connection's PRAGMA busy_timeout, in milliseconds.
func busyTimeoutOf(t *testing.T, s *Store) int64 {
	t.Helper()
	var millis int64
	if err := s.db.QueryRowContext(context.Background(), `PRAGMA busy_timeout`).Scan(&millis); err != nil {
		t.Fatalf("PRAGMA busy_timeout: %v", err)
	}
	return millis
}

func TestOpenWaitsBusyTimeoutForTheWriteLockByDefault(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "orrery.db")
	seedMigrated(t, path)
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	if got := busyTimeoutOf(t, s); got != BusyTimeout.Milliseconds() || got != 5000 {
		t.Fatalf("busy_timeout = %d ms, want BusyTimeout, 5000 ms", got)
	}
}

func TestOpenWithBusyTimeoutSetsTheWait(t *testing.T) {
	t.Parallel()
	for _, d := range []time.Duration{0, 250 * time.Millisecond} {
		t.Run(d.String(), func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "orrery.db")
			seedMigrated(t, path)
			s, err := Open(context.Background(), path, WithBusyTimeout(d))
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			defer s.Close()
			if got := busyTimeoutOf(t, s); got != d.Milliseconds() {
				t.Fatalf("busy_timeout = %d ms, want %d", got, d.Milliseconds())
			}
		})
	}
}

func TestOpenRefusesNegativeBusyTimeoutBeforeCreatingTheFile(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "state")
	s, err := Open(context.Background(), filepath.Join(dir, "orrery.db"), WithBusyTimeout(-time.Millisecond))
	if err == nil {
		s.Close()
		t.Fatal("Open with a negative busy timeout: want error, got nil")
	}
	if _, statErr := os.Stat(dir); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("store directory after refused Open: stat err %v, want not exist", statErr)
	}
}

func TestBusyReportsOnlyBusyAndLockedResults(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "orrery.db")
	seedMigrated(t, path)
	s, err := Open(context.Background(), path, noSync(), WithBusyTimeout(0))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	other, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open second connection: %v", err)
	}
	defer other.Close()
	conn, err := other.Conn(context.Background())
	if err != nil {
		t.Fatalf("second connection: %v", err)
	}
	defer conn.Close()
	if _, err = conn.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
		t.Fatalf("take write lock: %v", err)
	}
	locked := s.AddHold(context.Background(), HoldRow{Member: "m1", Since: time.Unix(1, 0)})
	if !Busy(locked) || !Busy(fmt.Errorf("wrapped: %w", locked)) {
		t.Fatalf("Busy(%v) = false, want true for a write the held lock refused, wrapped or not", locked)
	}
	if _, err = conn.ExecContext(context.Background(), "ROLLBACK"); err != nil {
		t.Fatalf("release write lock: %v", err)
	}
	_, malformed := s.db.ExecContext(context.Background(), "NOT SQL")
	for _, err := range []error{nil, errors.New("plain"), context.DeadlineExceeded, malformed} {
		if Busy(err) {
			t.Fatalf("Busy(%v) = true, want false", err)
		}
	}
}

func TestOpenRefusesUnknownSynchronousModeBeforeCreatingTheFile(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "state")
	path := filepath.Join(dir, "orrery.db")
	s, err := Open(context.Background(), path, WithSynchronous("SOMETIMES"))
	if err == nil {
		s.Close()
		t.Fatal("Open with synchronous SOMETIMES: want error, got nil")
	}
	if _, statErr := os.Stat(dir); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("store directory after refused Open: stat err %v, want not exist", statErr)
	}
}
