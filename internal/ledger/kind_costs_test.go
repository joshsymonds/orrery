package ledger

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

// kindCostColumns returns the per-kind list-cost columns migration 00002 adds to requests.
func kindCostColumns() []string {
	return []string{
		"list_input_nano_usd", "list_output_nano_usd", "list_cache_read_nano_usd", "list_cache_write_nano_usd",
	}
}

// requestColumns returns each column of the requests table in db with whether it may hold NULL.
func requestColumns(t *testing.T, db *sql.DB) map[string]bool {
	t.Helper()
	rows, err := db.Query(`SELECT name, "notnull" FROM pragma_table_info('requests')`)
	if err != nil {
		t.Fatalf("read requests columns: %v", err)
	}
	defer rows.Close()
	nullable := map[string]bool{}
	for rows.Next() {
		var name string
		var notNull int
		if err := rows.Scan(&name, &notNull); err != nil {
			t.Fatalf("scan requests column: %v", err)
		}
		nullable[name] = notNull == 0
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate requests columns: %v", err)
	}
	return nullable
}

// writeVersionOneLedger writes a ledger file at path at migration version 1, as a binary before migration 00002 left
// it, holding one request with a list cost.
func writeVersionOneLedger(ctx context.Context, t *testing.T, path string) {
	t.Helper()
	up, err := MigrationFS.ReadFile("migrations/00001_ledger.sql")
	if err != nil {
		t.Fatalf("read migration 00001: %v", err)
	}
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open version 1 ledger fixture: %v", err)
	}
	defer db.Close()
	upOnly, _, found := strings.Cut(string(up), "-- +goose Down")
	if !found {
		t.Fatal("migration 00001 has no Down section")
	}
	for _, stmt := range []string{
		upOnly,
		`CREATE TABLE goose_db_version (id INTEGER PRIMARY KEY AUTOINCREMENT, version_id INTEGER NOT NULL,
			is_applied INTEGER NOT NULL, tstamp TIMESTAMP DEFAULT (datetime('now')))`,
		`INSERT INTO goose_db_version (version_id, is_applied) VALUES (0, 1), (1, 1)`,
		`INSERT INTO requests (id, started_at, account, seat, context, requested_model, claude_session, agent_id,
			agent_type, list_cost_nano_usd) VALUES (7, 1, 'acct-a', 'seat-a', 'ctx-a', 'model-a', 'sess-a', '', '', 900)`,
	} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("write version 1 ledger fixture: %v", err)
		}
	}
}

// TestLedgerKindCostColumns checks that migration 00002 gives requests four nullable per-kind list-cost columns, on a
// new ledger and on a ledger at version 1, whose stored request keeps its list cost and reads NULL in each new column.
func TestLedgerKindCostColumns(t *testing.T) {
	ctx := context.Background()
	t.Run("a new ledger has the columns", func(t *testing.T) {
		l, err := Open(ctx, filepath.Join(t.TempDir(), "ledger.db"))
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		defer l.Close()
		columns := requestColumns(t, l.db)
		for _, name := range kindCostColumns() {
			if nullable, ok := columns[name]; !ok || !nullable {
				t.Errorf("requests column %s: present %v, nullable %v; want a nullable column", name, ok, nullable)
			}
		}
	})
	t.Run("a version 1 ledger migrates and keeps its request", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "ledger.db")
		writeVersionOneLedger(ctx, t, path)
		l, err := Open(ctx, path)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		defer l.Close()
		var list, input, output, cacheRead, cacheWrite sql.NullInt64
		err = l.db.QueryRowContext(ctx, `SELECT list_cost_nano_usd, list_input_nano_usd, list_output_nano_usd,
			list_cache_read_nano_usd, list_cache_write_nano_usd FROM requests WHERE id = 7`).
			Scan(&list, &input, &output, &cacheRead, &cacheWrite)
		if err != nil {
			t.Fatalf("read migrated request: %v", err)
		}
		if !list.Valid || list.Int64 != 900 {
			t.Errorf("list cost = %+v, want 900", list)
		}
		for i, kind := range []sql.NullInt64{input, output, cacheRead, cacheWrite} {
			if kind.Valid {
				t.Errorf("%s = %d, want NULL", kindCostColumns()[i], kind.Int64)
			}
		}
	})
}
