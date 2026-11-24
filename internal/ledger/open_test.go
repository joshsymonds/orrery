package ledger

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// orreryState is the bytes and modification time of an orrery.db.
type orreryState struct {
	data  []byte
	mtime time.Time
}

// writeOrrery writes an invented orrery.db into a new temp dir, gives it a fixed modification time, so a write to it
// shows as a new time, and returns its path.
func writeOrrery(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "orrery.db")
	mtime := time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC)
	if err := os.WriteFile(path, []byte("invented orrery.db, not a real store"), 0o600); err != nil {
		t.Fatalf("write orrery.db: %v", err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatalf("set orrery.db mtime: %v", err)
	}
	return path
}

// stateOf returns the bytes and modification time of the file at path.
func stateOf(t *testing.T, path string) orreryState {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return orreryState{data: data, mtime: info.ModTime()}
}

// requireOrreryUnchanged fails the test when the orrery.db at path no longer matches want, naming the step.
func requireOrreryUnchanged(t *testing.T, path string, want orreryState, step string) {
	t.Helper()
	got := stateOf(t, path)
	if !bytes.Equal(got.data, want.data) {
		t.Fatalf("orrery.db bytes changed after %s", step)
	}
	if !got.mtime.Equal(want.mtime) {
		t.Fatalf("orrery.db mtime changed after %s: %v, want %v", step, got.mtime, want.mtime)
	}
}

// tableNames returns the names of the tables in the database db.
func tableNames(t *testing.T, db *sql.DB) map[string]bool {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type = 'table'`)
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

// writeNewerLedger writes a ledger file at path whose goose version table records version 99, above every migration
// this binary embeds, and none of those migrations ran.
func writeNewerLedger(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open newer ledger fixture: %v", err)
	}
	for _, stmt := range []string{
		`CREATE TABLE goose_db_version (id INTEGER PRIMARY KEY AUTOINCREMENT, version_id INTEGER NOT NULL,
			is_applied INTEGER NOT NULL, tstamp TIMESTAMP DEFAULT (datetime('now')))`,
		`INSERT INTO goose_db_version (version_id, is_applied) VALUES (0, 1), (99, 1)`,
	} {
		if _, err = db.ExecContext(context.Background(), stmt); err != nil {
			db.Close()
			t.Fatalf("write newer ledger fixture: %v", err)
		}
	}
	if err = db.Close(); err != nil {
		t.Fatalf("close newer ledger fixture: %v", err)
	}
}

func TestLedgerOpen(t *testing.T) {
	t.Run("creates and migrates ledger.db beside orrery.db", func(t *testing.T) {
		ctx := context.Background()
		orreryDB := writeOrrery(t)
		want := stateOf(t, orreryDB)
		ledgerPath := Path(orreryDB)

		l, err := Open(ctx, ledgerPath)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		requireOrreryUnchanged(t, orreryDB, want, "Open")

		tables := tableNames(t, l.db)
		for _, name := range []string{"goose_db_version", "requests", "request_claims", "limit_readings", "limit_claim_models"} {
			if !tables[name] {
				t.Errorf("table %q missing after migration; have %v", name, tables)
			}
		}
		var journal string
		if err := l.db.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&journal); err != nil {
			t.Fatalf("read journal mode: %v", err)
		}
		if journal != "wal" {
			t.Errorf("journal mode = %q, want wal", journal)
		}
		if info, err := os.Stat(ledgerPath); err != nil {
			t.Fatalf("stat ledger.db: %v", err)
		} else if got := info.Mode().Perm(); got != 0o600 {
			t.Errorf("ledger.db mode = %o, want 600", got)
		}

		if err := l.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		requireOrreryUnchanged(t, orreryDB, want, "Close")
	})

	t.Run("refuses a ledger with a newer migration without migrating it", func(t *testing.T) {
		ctx := context.Background()
		orreryDB := writeOrrery(t)
		want := stateOf(t, orreryDB)
		ledgerPath := Path(orreryDB)
		writeNewerLedger(t, ledgerPath)
		before, err := os.ReadFile(ledgerPath)
		if err != nil {
			t.Fatalf("read newer ledger: %v", err)
		}

		l, err := Open(ctx, ledgerPath)
		if err == nil {
			l.Close()
			t.Fatal("Open on newer ledger: want error, got nil")
		}
		if !errors.Is(err, ErrTooNew) {
			t.Fatalf("Open error = %v, want ErrTooNew", err)
		}
		requireOrreryUnchanged(t, orreryDB, want, "refused Open")

		after, err := os.ReadFile(ledgerPath)
		if err != nil {
			t.Fatalf("read refused ledger: %v", err)
		}
		if !bytes.Equal(after, before) {
			t.Fatal("refused ledger's bytes changed")
		}
		db, err := sql.Open("sqlite3", ledgerPath)
		if err != nil {
			t.Fatalf("open refused ledger for inspection: %v", err)
		}
		defer db.Close()
		if tables := tableNames(t, db); tables["requests"] {
			t.Fatalf("refused ledger was migrated: tables %v", tables)
		}
	})

	t.Run("reads empty when ledger.db is absent", func(t *testing.T) {
		ctx := context.Background()
		orreryDB := writeOrrery(t)
		dir := filepath.Dir(orreryDB)
		want := stateOf(t, orreryDB)
		r := NewReader(Path(orreryDB))

		readings, err := r.Readings(ctx)
		if err != nil || len(readings) != 0 {
			t.Fatalf("Readings = %v, %v; want empty, nil", readings, err)
		}
		requireOrreryUnchanged(t, orreryDB, want, "Readings")

		totals, err := r.SessionTotals(ctx, "session")
		if err != nil || len(totals) != 0 {
			t.Fatalf("SessionTotals = %v, %v; want empty, nil", totals, err)
		}
		requireOrreryUnchanged(t, orreryDB, want, "SessionTotals")

		spend, err := r.Spend(ctx, 0, 1)
		if err != nil || len(spend) != 0 {
			t.Fatalf("Spend = %v, %v; want empty, nil", spend, err)
		}
		requireOrreryUnchanged(t, orreryDB, want, "Spend")

		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("list temp dir: %v", err)
		}
		if len(entries) != 1 || entries[0].Name() != "orrery.db" {
			names := make([]string, 0, len(entries))
			for _, e := range entries {
				names = append(names, e.Name())
			}
			t.Fatalf("directory after reads holds %v, want only orrery.db", names)
		}
	})
}
