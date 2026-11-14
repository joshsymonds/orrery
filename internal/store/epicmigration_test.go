package store

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/pressly/goose/v3"

	"github.com/joshsymonds/orrery/internal/evidence"
)

// epicTableColumns returns, keyed by table, the columns in declaration order of every table migrations 00014, 00016
// and 00019 create, and of the columns those migrations and 00020 add to epic tables.
func epicTableColumns() map[string][]string {
	return map[string][]string{
		"epics": {
			"id", "repository", "title", "approved", "intent", "care", "approach", "release_then", "checks",
			"phase", "created", "changed", "paused", "parked", "retried", "base",
		},
		"epic_failures":      {"epic", "position", "id", "text", "rating", "response"},
		"epic_additions":     {"epic", "position", "location", "what", "why", "cost"},
		"epic_requirements":  {"epic", "position", "id", "text", "evidence", "kind", "item"},
		"epic_premises":      {"epic", "position", "id", "text", "if_wrong"},
		"epic_exclusions":    {"epic", "position", "text"},
		"epic_alternatives":  {"epic", "position", "alternative", "why_not", "reconsider"},
		"epic_release_steps": {"epic", "position", "step", "target", "undo"},
		"epic_gates": {
			"epic", "position", "name", "argv", "limit_seconds", "needs", "agent", "must", "must_not",
		},
		"epic_gate_lists": {"epic", "list", "position", "gate"},
		"epic_decisions":  {"epic", "seq", "client_id", "text", "time", "question"},
		"epic_triggers":   {"epic", "id", "cause", "state", "turn", "created"},
		"epic_turns": {
			"epic", "seq", "step", "state", "reason", "seen", "outline", "outcome", "started", "ended",
			"told", "stop", "resets",
		},
		"epic_turn_decisions":    {"epic", "turn", "position", "text"},
		"epic_tasks":             {"epic", "seq", "turn", "title", "deliverable", "status", "gate", "evidence"},
		"epic_task_files":        {"epic", "task", "position", "path"},
		"epic_task_requirements": {"epic", "task", "position", "requirement"},
		"epic_questions":         {"epic", "seq", "turn", "text", "state"},
		"engine_rate_limits":     {"session_id", "status", "kind", "resets_at", "at"},
		"evidence_runs": {
			"id", "epic", "item", "revision", "phase", "source", "state", "spec", "grant_id", "holder", "exit",
			"timed_out", "not_captured", "log_hash", "log_bytes", "judge_step", "judges", "verdict", "judge_reason",
			"passed", "created", "ended",
		},
		"evidence_artifacts":  {"run", "position", "path", "state", "hash", "blob", "size"},
		"pushed_epic_notices": {"epic", "time"},
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

func TestPlannerControlColumnsDefaultOnFreshOpen(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "orrery.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	added := []struct {
		table, column, typ, dflt string
	}{
		{"epics", "retried", "INTEGER", "0"},
		{"epic_turns", "told", "INTEGER", "0"},
		{"epic_turns", "stop", "INTEGER", "0"},
		{"epic_turns", "resets", "INTEGER", "0"},
		{"epic_decisions", "question", "INTEGER", "0"},
		{"pending_notices", "epic", "TEXT", "''"},
	}
	for _, c := range added {
		var typ, dflt string
		var notNull int
		if err := s.db.QueryRowContext(ctx,
			`SELECT type, "notnull", dflt_value FROM pragma_table_info(?) WHERE name = ?`,
			c.table, c.column).Scan(&typ, &notNull, &dflt); err != nil {
			t.Fatalf("read column %s.%s: %v", c.table, c.column, err)
		}
		if typ != c.typ || notNull != 1 || dflt != c.dflt {
			t.Errorf("%s.%s = type %q, notnull %d, default %q; want type %q, NOT NULL, default %q",
				c.table, c.column, typ, notNull, dflt, c.typ, c.dflt)
		}
	}
}

func TestPushedEpicNoticesKeepOneRowPerEpic(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "orrery.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	const epic = "orrery-0123456/epic-one"
	insert := func(at int64) error {
		_, err := s.db.ExecContext(ctx, `INSERT INTO pushed_epic_notices (epic, time) VALUES (?, ?)`, epic, at)
		return err
	}
	if err := insert(1700000000); err != nil {
		t.Fatalf("first pushed notice of %s: %v", epic, err)
	}
	err = insert(1700000100)
	if err == nil || !strings.Contains(err.Error(), "UNIQUE constraint failed") {
		t.Fatalf("second pushed notice of %s: err = %v, want the one-row-per-epic refusal", epic, err)
	}
	want := [][]any{{epic, int64(1700000000)}}
	if got := rowsOf(t, s.db, "pushed_epic_notices"); !reflect.DeepEqual(got, want) {
		t.Fatalf("pushed notices after refused insert = %v, want %v", got, want)
	}
}

func TestEpicTriggerStateAcceptsOnlyPendingClaimedConsumed(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "orrery.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	const epic = "orrery-0123456/epic-one"
	insert := func(id, state string) error {
		_, err := s.db.ExecContext(ctx, `INSERT INTO epic_triggers (epic, id, cause, state, created)
			VALUES (?, ?, 'epic created', ?, 1700000000)`, epic, id, state)
		return err
	}

	accepted := []string{"pending", "claimed", "consumed"}
	for _, state := range accepted {
		if err := insert("trigger-"+state, state); err != nil {
			t.Fatalf("insert trigger in state %q: %v", state, err)
		}
	}
	refused := []string{"", "open", "Pending", "free", "running"}
	for i, state := range refused {
		err := insert(fmt.Sprintf("refused-%d", i), state)
		if err == nil || !strings.Contains(err.Error(), "CHECK constraint failed") {
			t.Fatalf("insert trigger in state %q: err = %v, want the state CHECK refusal", state, err)
		}
	}

	want := [][]any{
		{epic, "trigger-pending", "epic created", "pending", int64(0), int64(1700000000)},
		{epic, "trigger-claimed", "epic created", "claimed", int64(0), int64(1700000000)},
		{epic, "trigger-consumed", "epic created", "consumed", int64(0), int64(1700000000)},
	}
	if got := rowsOf(t, s.db, "epic_triggers"); !reflect.DeepEqual(got, want) {
		t.Fatalf("triggers after refused inserts = %v, want %v", got, want)
	}
}

func TestEpicTurnsRefuseSecondRunningTurnPerEpic(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "orrery.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	const first, second = "orrery-0123456/epic-one", "orrery-0123456/epic-two"
	turn := func(epic string, seq int, state string) error {
		_, err := s.db.ExecContext(ctx, `INSERT INTO epic_turns (epic, seq, state, seen, started)
			VALUES (?, ?, ?, 0, 1700000000)`, epic, seq, state)
		return err
	}

	if err := turn(first, 1, "running"); err != nil {
		t.Fatalf("first running turn of %s: %v", first, err)
	}
	if err := turn(second, 1, "running"); err != nil {
		t.Fatalf("running turn of %s while %s runs: %v", second, first, err)
	}
	err = turn(first, 2, "running")
	if err == nil || !strings.Contains(err.Error(), "UNIQUE constraint failed") {
		t.Fatalf("second running turn of %s: err = %v, want the one-running-turn refusal", first, err)
	}
	for seq, state := range []string{"succeeded", "failed", "free"} {
		if err := turn(first, seq+2, state); err != nil {
			t.Fatalf("%s turn of %s while a turn runs: %v", state, first, err)
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
	want := [][]any{{id, int64(1), "D1", "Use the store", int64(1700000000), int64(0)}}

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
	// Open applies 00014_epics.sql and every migration after it: 00020_evidence.sql is the newest.
	if version != 20 {
		t.Fatalf("store version after Open = %d, want 20", version)
	}

	// A later migration may add columns at the end of an existing table, as 00015_step_readopt.sql adds to steps and
	// grants, so each row is compared over the columns it had before the migrations.
	for _, table := range existing {
		if got := leadingColumns(rowsOf(t, s.db, table), before[table]); !reflect.DeepEqual(got, before[table]) {
			t.Errorf("%s after migration = %v, want %v", table, got, before[table])
		}
	}
}

// TestEpicStoredAtVersion16ReadsBackWithEmptyEvidence stores an epic and a Requirement at migration 16, before the
// evidence migration, and reads them back after Open migrates the store: the base is empty and the item the zero Item.
func TestEpicStoredAtVersion16ReadsBackWithEmptyEvidence(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")

	fixture := openStoreAtVersion(t, path, 16)
	const id = "orrery-0123456/epic-one"
	seedEpic(t, fixture, id)
	if _, err := fixture.ExecContext(ctx, `INSERT INTO epic_requirements (epic, position, id, text, evidence, kind)
		VALUES (?, 1, 'R1', 'Stored before evidence', 'store test', 'check')`, id); err != nil {
		fixture.Close()
		t.Fatalf("seed requirement: %v", err)
	}
	if err := fixture.Close(); err != nil {
		t.Fatalf("close fixture: %v", err)
	}

	s, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open at version 16: %v", err)
	}
	defer s.Close()

	got, err := s.Epic(ctx, id)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	at := time.Unix(1700000000, 0)
	want := EpicRow{
		ID: id, Repository: "/work/repo", Title: "Epic one", Approved: "approved line", Intent: "intent",
		Care: "serious", Approach: "approach", ReleaseThen: "then", Checks: "checks", Phase: "accepted",
		Created: at, Changed: at,
		Requirements: []EpicRequirement{
			{ID: "R1", Text: "Stored before evidence", Evidence: "store test", Kind: "check"},
		},
		Decisions: []EpicDecision{{Seq: 1, ID: "D1", Text: "Use the store", Time: at}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("epic stored at version 16 = %+v, want %+v", got, want)
	}
}

// TestEpicTaskStoredAtVersion16MigratesCheckToEvidence stores a task with a check at migration 16 and reads it back
// after Open migrates the store: the check is the conclusion of its evidence, its gate is empty, and it has no judge.
func TestEpicTaskStoredAtVersion16MigratesCheckToEvidence(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")

	fixture := openStoreAtVersion(t, path, 16)
	const id = "orrery-0123456/epic-one"
	seedEpic(t, fixture, id)
	if _, err := fixture.ExecContext(ctx, `INSERT INTO epic_tasks
		(epic, seq, turn, title, deliverable, task_check, status)
		VALUES (?, 1, 1, 'Build it', 'the migration', 'go test ./x', 'open')`, id); err != nil {
		fixture.Close()
		t.Fatalf("seed task: %v", err)
	}
	if err := fixture.Close(); err != nil {
		t.Fatalf("close fixture: %v", err)
	}

	s, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open at version 16: %v", err)
	}
	defer s.Close()

	got, err := s.Epic(ctx, id)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	want := []EpicTask{{
		Seq: 1, Turn: 1, Title: "Build it", Deliverable: "the migration", Status: "open",
		Evidence: evidence.Item{Conclusion: "go test ./x"},
	}}
	if !reflect.DeepEqual(got.Tasks, want) {
		t.Errorf("tasks stored at version 16 = %+v, want %+v", got.Tasks, want)
	}
}

// TestEpicTaskCheckRestoredByEvidenceMigrationDown stores a task with an evidence item at migration 20 and reads its
// conclusion back as task_check after the Down of 00020_evidence.sql, which drops gate and evidence.
func TestEpicTaskCheckRestoredByEvidenceMigrationDown(t *testing.T) {
	ctx := context.Background()
	s := openEpicStore(t)
	if _, err := s.db.ExecContext(ctx, `INSERT INTO epic_tasks
		(epic, seq, turn, title, deliverable, status, gate, evidence)
		VALUES ('orrery-0123456/epic-one', 1, 1, 'Build it', 'the down', 'open', 'check',
		'{"capture":["scripts/check"],"limit":60,"artifacts":null,"supplied":null,"conclusion":"go test ./restored",
		"judge":"a judge"}')`); err != nil {
		t.Fatalf("insert task: %v", err)
	}

	migrationsFS, err := fs.Sub(migrationFS, "migrations")
	if err != nil {
		t.Fatalf("sub migrations fs: %v", err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, s.db, migrationsFS)
	if err != nil {
		t.Fatalf("create goose provider: %v", err)
	}
	if _, err := provider.DownTo(ctx, 19); err != nil {
		t.Fatalf("DownTo 19: %v", err)
	}

	var check string
	if err := s.db.QueryRowContext(ctx, `SELECT task_check FROM epic_tasks WHERE seq = 1`).Scan(&check); err != nil {
		t.Fatalf("read task_check after Down: %v", err)
	}
	if want := "go test ./restored"; check != want {
		t.Errorf("task_check after Down = %q, want %q", check, want)
	}
	if columns := columnsOf(t, s, "epic_tasks"); hasAny(columns, "gate", "evidence") {
		t.Errorf("epic_tasks columns after Down = %v, want no gate or evidence", columns)
	}
}

// TestEvidenceMigrationDownAndUp removes the epic gate tables and the base and item columns with the Down of
// 00020_evidence.sql, and Up restores them.
func TestEvidenceMigrationDownAndUp(t *testing.T) {
	ctx := context.Background()
	s := openEpicStore(t)

	migrationsFS, err := fs.Sub(migrationFS, "migrations")
	if err != nil {
		t.Fatalf("sub migrations fs: %v", err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, s.db, migrationsFS)
	if err != nil {
		t.Fatalf("create goose provider: %v", err)
	}

	if _, err := provider.DownTo(ctx, 19); err != nil {
		t.Fatalf("DownTo 19: %v", err)
	}
	tables := tableNames(t, s)
	if tables["epic_gates"] || tables["epic_gate_lists"] {
		t.Errorf("gate tables after Down: epic_gates %v, epic_gate_lists %v; want neither", tables["epic_gates"],
			tables["epic_gate_lists"])
	}
	if hasAny(columnsOf(t, s, "epics"), "base") {
		t.Errorf("epics columns after Down still hold base")
	}
	if hasAny(columnsOf(t, s, "epic_requirements"), "item") {
		t.Errorf("epic_requirements columns after Down still hold item")
	}
	if tables["evidence_runs"] || tables["evidence_artifacts"] {
		t.Errorf("evidence tables after Down: evidence_runs %v, evidence_artifacts %v; want neither",
			tables["evidence_runs"], tables["evidence_artifacts"])
	}

	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("Up: %v", err)
	}
	tables = tableNames(t, s)
	if !tables["epic_gates"] || !tables["epic_gate_lists"] {
		t.Errorf("gate tables after Up: epic_gates %v, epic_gate_lists %v; want both", tables["epic_gates"],
			tables["epic_gate_lists"])
	}
	if !tables["evidence_runs"] || !tables["evidence_artifacts"] {
		t.Errorf("evidence tables after Up: evidence_runs %v, evidence_artifacts %v; want both",
			tables["evidence_runs"], tables["evidence_artifacts"])
	}
	if !hasAll(columnsOf(t, s, "epics"), "base") {
		t.Errorf("epics columns after Up = %v, want base", columnsOf(t, s, "epics"))
	}
	if !hasAll(columnsOf(t, s, "epic_requirements"), "item") {
		t.Errorf("epic_requirements columns after Up = %v, want item", columnsOf(t, s, "epic_requirements"))
	}
}

func TestPlannerControlsDefaultOnRowsStoredAtVersion16(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")

	const epic = "orrery-0123456/epic-one"
	fixture := openStoreAtVersion(t, path, 16)
	seedEpic(t, fixture, epic)
	if _, err := fixture.ExecContext(ctx, `INSERT INTO epic_turns
		(epic, seq, state, seen, outline, outcome, started, ended)
		VALUES (?, 1, 'succeeded', 0, 'outline', 'outcome', 1700000000, 1700000100)`, epic); err != nil {
		fixture.Close()
		t.Fatalf("seed turn: %v", err)
	}
	if _, err := fixture.ExecContext(ctx, `INSERT INTO pending_notices
		(id, pane, session, state, time, phase, due, delay_served, watch_logged, source_kind, source, silent)
		VALUES (7, '%3', 'claude-1', 'waiting', 1700000100, 'delayed', 1700000190000, 1, 0, 'session', ?, 1)`,
		[]byte(`{"event":"stop"}`)); err != nil {
		fixture.Close()
		t.Fatalf("seed pending notice: %v", err)
	}
	tables := []string{"epics", "epic_turns", "epic_decisions", "pending_notices"}
	before := map[string][][]any{}
	for _, table := range tables {
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
		t.Fatalf("Open at version 16: %v", err)
	}
	defer s.Close()

	// Each stored row keeps its values and gains the new column defaults at the end of its table.
	added := map[string][]any{
		"epics":           {int64(0), ""},
		"epic_turns":      {int64(0), int64(0), int64(0)},
		"epic_decisions":  {int64(0)},
		"pending_notices": {""},
	}
	for _, table := range tables {
		want := make([][]any, len(before[table]))
		for i, row := range before[table] {
			want[i] = append(append([]any{}, row...), added[table]...)
		}
		if got := rowsOf(t, s.db, table); !reflect.DeepEqual(got, want) {
			t.Errorf("%s after migration = %v, want %v", table, got, want)
		}
	}
}

// leadingColumns returns rows with each row cut to the width of the rows in want, the columns a table had before later
// migrations added more. Rows of any other width, or with no rows in want, are returned as they are.
func leadingColumns(rows, want [][]any) [][]any {
	if len(want) == 0 {
		return rows
	}
	width := len(want[0])
	out := make([][]any, 0, len(rows))
	for _, row := range rows {
		if len(row) > width {
			row = row[:width]
		}
		out = append(out, row)
	}
	return out
}
