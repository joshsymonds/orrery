package store

import (
	"context"
	"database/sql"
	"io/fs"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/pressly/goose/v3"
)

// epicTableColumns returns, keyed by table, the columns in declaration order of every table migration 00014 creates.
func epicTableColumns() map[string][]string {
	return map[string][]string{
		"epics": {
			"id", "repository", "title", "approved", "intent", "care", "approach", "release_then", "checks",
			"phase", "created", "changed",
		},
		"epic_failures":      {"epic", "position", "id", "text", "rating", "response"},
		"epic_additions":     {"epic", "position", "location", "what", "why", "cost"},
		"epic_requirements":  {"epic", "position", "id", "text", "evidence", "kind"},
		"epic_premises":      {"epic", "position", "id", "text", "if_wrong"},
		"epic_exclusions":    {"epic", "position", "text"},
		"epic_alternatives":  {"epic", "position", "alternative", "why_not", "reconsider"},
		"epic_release_steps": {"epic", "position", "step", "target", "undo"},
		"epic_decisions":     {"epic", "seq", "client_id", "text", "time"},
	}
}

// columnsOf returns the column names of table in declaration order.
func columnsOf(t *testing.T, s *Store, table string) []string {
	t.Helper()
	rows, err := s.db.Query(`SELECT name FROM pragma_table_info(?) ORDER BY cid`, table)
	if err != nil {
		t.Fatalf("list columns of %s: %v", table, err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan column of %s: %v", table, err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate columns of %s: %v", table, err)
	}
	return names
}

// rowsOf reads every row of table in rowid order, each as the values of its columns in declaration order.
func rowsOf(t *testing.T, db *sql.DB, table string) [][]any {
	t.Helper()
	rows, err := db.Query(`SELECT * FROM ` + table + ` ORDER BY rowid`)
	if err != nil {
		t.Fatalf("read %s: %v", table, err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatalf("columns of %s: %v", table, err)
	}
	var out [][]any
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatalf("scan %s: %v", table, err)
		}
		out = append(out, vals)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate %s: %v", table, err)
	}
	return out
}

// seedEpic inserts one epic with one decision, the rows the append-only triggers guard.
func seedEpic(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `INSERT INTO epics
		(id, repository, title, approved, intent, care, approach, release_then, checks, phase, created, changed)
		VALUES (?, '/work/repo', 'Epic one', 'approved line', 'intent', 'serious', 'approach', 'then', 'checks',
		'accepted', 1700000000, 1700000000)`, id); err != nil {
		t.Fatalf("insert epic: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO epic_decisions (epic, seq, client_id, text, time)
		VALUES (?, 1, 'D1', 'Use the store', 1700000000)`, id); err != nil {
		t.Fatalf("insert decision: %v", err)
	}
}

func TestEpicTablesCreatedOnFreshOpen(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "orrery.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	tables := tableNames(t, s)
	columns := epicTableColumns()
	for name, want := range columns {
		if !tables[name] {
			t.Errorf("table %q missing after migration", name)
			continue
		}
		if got := columnsOf(t, s, name); !reflect.DeepEqual(got, want) {
			t.Errorf("columns of %s = %v, want %v", name, got, want)
		}
	}
	for name := range tables {
		if strings.HasPrefix(name, "epic") && columns[name] == nil {
			t.Errorf("unexpected epic table %q", name)
		}
	}
}

func TestEpicDecisionsRefuseUpdateAndDelete(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "orrery.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	const id = "orrery-0123456/epic-one"
	seedEpic(t, s.db, id)
	want := [][]any{{id, int64(1), "D1", "Use the store", int64(1700000000)}}

	if got := rowsOf(t, s.db, "epic_decisions"); !reflect.DeepEqual(got, want) {
		t.Fatalf("seeded decisions = %v, want %v", got, want)
	}

	_, err = s.db.ExecContext(ctx, `UPDATE epic_decisions SET text = 'rewritten' WHERE epic = ?`, id)
	if err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("UPDATE on epic_decisions: err = %v, want the append-only refusal", err)
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM epic_decisions WHERE epic = ?`, id)
	if err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("DELETE on epic_decisions: err = %v, want the append-only refusal", err)
	}

	if got := rowsOf(t, s.db, "epic_decisions"); !reflect.DeepEqual(got, want) {
		t.Fatalf("decisions after refused UPDATE and DELETE = %v, want %v", got, want)
	}
}

// openStoreAtVersion creates the store file at path with the embedded migrations applied only up to version,
// the schema a binary from before the epic migration left behind, and returns the open database.
func openStoreAtVersion(t *testing.T, path string, version int64) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	db.SetMaxOpenConns(1)
	migrationsFS, err := fs.Sub(migrationFS, "migrations")
	if err != nil {
		db.Close()
		t.Fatalf("sub migrations fs: %v", err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, migrationsFS)
	if err != nil {
		db.Close()
		t.Fatalf("create fixture provider: %v", err)
	}
	if _, err = provider.UpTo(context.Background(), version); err != nil {
		db.Close()
		t.Fatalf("migrate fixture to %d: %v", version, err)
	}
	return db
}

func TestEpicMigrationKeepsExistingRows(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")

	fixture := openStoreAtVersion(t, path, 13)
	seeds := []struct {
		stmt string
		args []any
	}{
		{`INSERT INTO sessions (id, server, pane, state, changed, seen, question, summary, label, context, tool,
			claude_session, transcript, kind, dir, reason)
			VALUES ('claude-1', 'srv', '%1', 'waiting', '2026-11-12T09:00:00Z', '2026-11-12T09:01:00Z', 'Ship it?',
			'Waiting on review', 'one', 'some context', 'Bash', 'cs-1', '/tmp/transcript.jsonl', '', '/work/one', 'stop')`, nil},
		{`INSERT INTO sessions (id, server, pane, state, changed, kind, dir)
			VALUES ('engine-1', '', '', 'waiting', '2026-11-12T09:02:00Z', 'engine', '/work/two')`, nil},
		{`INSERT INTO steps (id, agent, prompt, output_schema, workspace, revision, worktree, done_value, read_only,
			owned, gate, gate_limit, started, checks, result, status, end_value, reason, ended, model_limit, doing,
			verdicts, epic, task, gate_name, gate_source, gate_needs)
			VALUES ('step-1', 'haiku', 'Implement the thing', '{"type":"object"}', '/work/ws', 'abc1234', '/work/wt',
			'done', 0, '["a.go"]', '["just","check"]', 1800, 1700000000, '[]', '{}', 'passed', '{"ok":true}', '',
			1700000600, 60, 'tests', '[{"ok":true}]', 'epic-1', 'T2', 'check', 'argv', '["x"]')`, nil},
		{`INSERT INTO pending_notices (id, pane, session, state, time, phase, due, delay_served, watch_logged,
			source_kind, source, silent)
			VALUES (7, '%3', 'claude-1', 'waiting', 1700000100, 'delayed', 1700000190000, 1, 0, 'session', ?, 1)`,
			[]any{[]byte(`{"event":"stop"}`)}},
		{`INSERT INTO pushed_notices (pane, session, time) VALUES ('%3', 'claude-1', 1700000200)`, nil},
	}
	for _, seed := range seeds {
		if _, err := fixture.ExecContext(ctx, seed.stmt, seed.args...); err != nil {
			fixture.Close()
			t.Fatalf("seed fixture: %v", err)
		}
	}
	existing := []string{"sessions", "steps", "pending_notices", "pushed_notices"}
	before := map[string][][]any{}
	for _, table := range existing {
		before[table] = rowsOf(t, fixture, table)
		if len(before[table]) == 0 {
			fixture.Close()
			t.Fatalf("%s has no fixture row to compare", table)
		}
	}
	if err := fixture.Close(); err != nil {
		t.Fatalf("close fixture: %v", err)
	}

	s, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open at version 13: %v", err)
	}
	defer s.Close()

	var version int64
	if err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(version_id), 0) FROM goose_db_version WHERE is_applied = 1`).Scan(&version); err != nil {
		t.Fatalf("read store version: %v", err)
	}
	if version != 14 {
		t.Fatalf("store version after Open = %d, want 14", version)
	}

	for _, table := range existing {
		if got := rowsOf(t, s.db, table); !reflect.DeepEqual(got, before[table]) {
			t.Errorf("%s after migration = %v, want %v", table, got, before[table])
		}
	}
}
