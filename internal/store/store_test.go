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
)

func TestPathUsesXDGStateHome(t *testing.T) {
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

	if _, err := s.db.ExecContext(ctx, `INSERT INTO sessions (id, server, pane) VALUES ('s1', '', '%1')`); err != nil {
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
	db, err := sql.Open("sqlite", path)
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
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")

	first, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	before := goodRows(t, path)

	second, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if after := goodRows(t, path); after != before {
		t.Fatalf("goose_db_version changed on reopen:\nbefore:\n%safter:\n%s", before, after)
	}
}

func TestOpenRejectsNonSQLiteFileUntouched(t *testing.T) {
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
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")

	// A store written by a newer binary: its goose version is 99 and none of this binary's migrations ran.
	db, err := sql.Open("sqlite", path)
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
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open for inspection: %v", err)
	}
	defer db.Close()
	return tableNames(t, &Store{db: db})
}
