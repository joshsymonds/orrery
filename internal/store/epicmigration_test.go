package store

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/pressly/goose/v3"

	"github.com/joshsymonds/orrery/internal/evidence"
)

// epicTableColumns returns, keyed by table, the columns in declaration order of every table migrations 00014, 00016,
// 00019, 00028, 00030, 00035, 00036, 00037, 00038, 00040 and 00041 create, and of the columns those migrations and
// 00020, 00021, 00022, 00025, 00029, 00033, 00034, 00035, 00039, 00040 and 00042 add to epic tables.
func epicTableColumns() map[string][]string {
	return map[string][]string{
		"epics": {
			"id", "repository", "title", "approved", "intent", "care", "approach", "release_then", "checks",
			"phase", "created", "changed", "paused", "parked", "retried", "base", "branch", "workflow",
			"workflow_text", "workflow_sha256", "pause_source", "stored_base", "candidate", "summary", "phase_at",
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
		"epic_turn_decisions": {"epic", "turn", "position", "text"},
		"epic_tasks": {
			"epic", "seq", "turn", "title", "deliverable", "status", "gate", "evidence", "stage", "base", "attempt",
			"rerun", "no_verdict", "held", "step", "red_run", "green_run", "revision", "failure", "review", "refs",
			"gate_pos", "finding_pos", "batch", "guard",
		},
		"epic_task_files":        {"epic", "task", "position", "path"},
		"epic_task_requirements": {"epic", "task", "position", "requirement"},
		"epic_task_contributes":  {"epic", "task", "position", "requirement"},
		"epic_task_test_changes": {"epic", "task", "position", "path"},
		"epic_task_reviews":      {"epic", "task", "attempt", "position", "gate", "step", "verdict"},
		"epic_task_findings": {
			"epic", "task", "attempt", "review", "position", "id", "claim", "citation", "location", "evidence",
			"verify_by", "step", "result", "reason", "unverified",
		},
		"epic_reviewers": {
			"epic", "position", "name", "enabled", "stage", "must", "must_not", "prompt", "agent", "model", "tools",
			"effort", "limit_seconds", "verify",
		},
		"epic_workflow_nodes": {"epic", "position", "id", "parent", "kind"},
		"epic_node_runs": {
			"epic", "path", "id", "node", "kind", "task", "round", "state", "outcome", "loop_round", "granted",
			"mark", "started", "ended", "attempt", "revision", "refs", "refs_checked", "result", "output",
		},
		"epic_node_checks": {
			"id", "epic", "path", "revision", "argv", "limit_seconds", "needs", "state", "grant_id", "holder", "exit",
			"timed_out", "not_run", "stdout", "stderr", "created", "ended",
		},
		"epic_release_runs": {
			"epic", "part", "position", "attempt", "step", "start", "head", "finish", "commits", "evidence",
			"passed", "reason", "at",
		},
		"epic_release_slots": {"repository", "epic"},
		"epic_tells":         {"epic", "seq", "text", "at", "told", "question"},
		"epic_questions": {
			"epic", "seq", "turn", "text", "state", "label", "choices", "recommended", "why", "confidence", "replaces",
			"replaced_by", "choice",
		},
		"epic_reviews":         {"epic", "frozen", "clean"},
		"epic_review_verdicts": {"epic", "position", "reviewer", "step", "verdict", "reason"},
		"epic_ledger": {
			"epic", "id", "position", "reviewer", "finding", "claim", "citation", "location", "evidence", "verify_by",
			"step", "result", "unverified", "reason",
		},
		"epic_review_rounds": {"epic", "round", "revision", "tasks", "outcome"},
		"epic_closures":      {"epic", "round", "finding", "step", "result", "reason"},
		"epic_review_holds":  {"epic", "step"},
		"epic_sessions":      {"epic", "session", "call", "at"},
		"engine_rate_limits": {"session_id", "status", "kind", "resets_at", "at"},
		"evidence_runs": {
			"id", "epic", "item", "revision", "phase", "source", "state", "spec", "grant_id", "holder", "exit",
			"timed_out", "not_captured", "log_hash", "log_bytes", "judge_step", "judges", "verdict", "judge_reason",
			"passed", "created", "ended", "stderr_hash", "stderr_bytes",
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

// seededEpic is the id of the epic seedEpic inserts.
const seededEpic = "orrery-0123456/epic-one"

// seedEpic inserts the epic seededEpic with one decision, the rows the append-only triggers guard.
func seedEpic(t *testing.T, db *sql.DB) {
	t.Helper()
	const id = seededEpic
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
	t.Parallel()
	// The migrated image is the file Open makes of a fresh path, so the store opened from it is a fresh Open's.
	s := openEpicStore(t)

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

// TestReviewersMigrationDownAndUp removes the agents column of steps, the unverified column of epic_task_findings and
// the epic_reviewers table with the Down of 00035_reviewers.sql, and Up restores them.
func TestReviewersMigrationDownAndUp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, provider := storeAtVersion(t, 35)

	if _, err := provider.DownTo(ctx, 34); err != nil {
		t.Fatalf("DownTo 34: %v", err)
	}
	if hasAny(tableColumns(t, s, "steps"), "agents") {
		t.Fatalf("steps columns after Down = %v, want no agents column", tableColumns(t, s, "steps"))
	}
	if hasAny(tableColumns(t, s, "epic_task_findings"), "unverified") {
		t.Fatalf("epic_task_findings columns after Down = %v, want no unverified column",
			tableColumns(t, s, "epic_task_findings"))
	}
	if tableNames(t, s)["epic_reviewers"] {
		t.Fatalf("epic_reviewers after Down: present, want absent")
	}

	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if !hasAll(tableColumns(t, s, "steps"), "agents") {
		t.Fatalf("steps columns after Up = %v, want the agents column", tableColumns(t, s, "steps"))
	}
	if !hasAll(tableColumns(t, s, "epic_task_findings"), "unverified") {
		t.Fatalf("epic_task_findings columns after Up = %v, want the unverified column",
			tableColumns(t, s, "epic_task_findings"))
	}
	if !tableNames(t, s)["epic_reviewers"] {
		t.Fatalf("epic_reviewers after Up: absent, want present")
	}
}

func TestPlannerControlColumnsDefaultOnFreshOpen(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	// The migrated image is the file Open makes of a fresh path, so the store opened from it is a fresh Open's.
	s := openEpicStore(t)

	added := []struct {
		table, column, typ, dflt string
	}{
		{"epics", "retried", "INTEGER", "0"},
		{"epics", "stored_base", "TEXT", "''"},
		{"epics", "candidate", "TEXT", "''"},
		{"epic_tasks", "batch", "TEXT", "''"},
		{"epic_turns", "told", "INTEGER", "0"},
		{"epic_turns", "stop", "INTEGER", "0"},
		{"epic_turns", "resets", "INTEGER", "0"},
		{"epic_decisions", "question", "INTEGER", "0"},
		{"pending_notices", "epic", "TEXT", "''"},
		{"epic_questions", "label", "TEXT", "''"},
		{"epic_questions", "choices", "TEXT", "'[]'"},
		{"epic_questions", "recommended", "TEXT", "''"},
		{"epic_questions", "why", "TEXT", "''"},
		{"epic_questions", "confidence", "TEXT", "''"},
		{"epic_questions", "replaces", "INTEGER", "0"},
		{"epic_questions", "replaced_by", "INTEGER", "0"},
		{"epic_questions", "choice", "TEXT", "''"},
		{"epic_tells", "question", "INTEGER", "0"},
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
	path := filepath.Join(t.TempDir(), "orrery.db")
	// The constraint under test is in the migrated schema, so the store starts from the migrated image.
	seedMigrated(t, path)
	s, err := Open(ctx, path, noSync())
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
	if got := rowsOf(t, s.db.DB, "pushed_epic_notices"); !reflect.DeepEqual(got, want) {
		t.Fatalf("pushed notices after refused insert = %v, want %v", got, want)
	}
}

func TestEpicTriggerStateAcceptsOnlyPendingClaimedConsumed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	// The constraint under test is in the migrated schema, so the store starts from the migrated image.
	seedMigrated(t, path)
	s, err := Open(ctx, path, noSync())
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
	if got := rowsOf(t, s.db.DB, "epic_triggers"); !reflect.DeepEqual(got, want) {
		t.Fatalf("triggers after refused inserts = %v, want %v", got, want)
	}
}

func TestEpicTurnsRefuseSecondRunningTurnPerEpic(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	// The constraint under test is in the migrated schema, so the store starts from the migrated image.
	seedMigrated(t, path)
	s, err := Open(ctx, path, noSync())
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
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	// The constraint under test is in the migrated schema, so the store starts from the migrated image.
	seedMigrated(t, path)
	s, err := Open(ctx, path, noSync())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	const id = seededEpic
	seedEpic(t, s.db.DB)
	want := [][]any{{id, int64(1), "D1", "Use the store", int64(1700000000), int64(0)}}

	if got := rowsOf(t, s.db.DB, "epic_decisions"); !reflect.DeepEqual(got, want) {
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

	if got := rowsOf(t, s.db.DB, "epic_decisions"); !reflect.DeepEqual(got, want) {
		t.Fatalf("decisions after refused UPDATE and DELETE = %v, want %v", got, want)
	}
}

// openStoreAtVersion creates the store file at path with the embedded migrations applied only up to version,
// the schema a binary from before the epic migration left behind, and returns the open database. The file is a copy
// of the version's image (see storeImages), so the fixture costs its copy, not a migration.
func openStoreAtVersion(t *testing.T, path string, version int64) *sql.DB {
	t.Helper()
	images, err := storeImages.versions()
	if err != nil {
		t.Fatalf("make store version images: %v", err)
	}
	image, ok := images[version]
	if !ok {
		t.Fatalf("no store image at version %d", version)
	}
	if err = os.WriteFile(path, image, 0o600); err != nil {
		t.Fatalf("write store at version %d: %v", version, err)
	}
	// The fixture is a throwaway file, so it skips the fsyncs, as noSync's stores do.
	db, err := sql.Open("sqlite", path+"?_synchronous=OFF")
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	db.SetMaxOpenConns(1)
	return db
}

func TestEpicMigrationKeepsExistingRows(t *testing.T) {
	t.Parallel()
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

	s, err := Open(ctx, path, noSync())
	if err != nil {
		t.Fatalf("Open at version 13: %v", err)
	}
	defer s.Close()

	var version int64
	if err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(version_id), 0) FROM goose_db_version WHERE is_applied = 1`).Scan(&version); err != nil {
		t.Fatalf("read store version: %v", err)
	}
	// Open applies 00014_epics.sql and every migration after it: 00042_endings.sql is the newest.
	if version != 42 {
		t.Fatalf("store version after Open = %d, want 42", version)
	}

	// A later migration may add columns at the end of an existing table, as 00015_step_readopt.sql adds to steps and
	// grants, so each row is compared over the columns it had before the migrations.
	for _, table := range existing {
		if got := leadingColumns(rowsOf(t, s.db.DB, table), before[table]); !reflect.DeepEqual(got, before[table]) {
			t.Errorf("%s after migration = %v, want %v", table, got, before[table])
		}
	}
}

// TestEpicStoredAtVersion32ReadsStoredBaseAsItsBase stores an epic with a base at migration 32, before the integration
// migration, and reads it back after Open migrates the store: its stored base is its base and its candidate is empty.
func TestEpicStoredAtVersion32ReadsStoredBaseAsItsBase(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")

	const base = "0123456789abcdef0123456789abcdef01234567"
	fixture := openStoreAtVersion(t, path, 32)
	const id = seededEpic
	seedEpic(t, fixture)
	if _, err := fixture.ExecContext(ctx, `UPDATE epics SET base = ? WHERE id = ?`, base, id); err != nil {
		fixture.Close()
		t.Fatalf("seed base: %v", err)
	}
	if err := fixture.Close(); err != nil {
		t.Fatalf("close fixture: %v", err)
	}

	s, err := Open(ctx, path, noSync())
	if err != nil {
		t.Fatalf("Open at version 32: %v", err)
	}
	defer s.Close()

	got, err := s.Epic(ctx, id)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	if got.Base != base || got.StoredBase != base {
		t.Errorf("base = %q, stored base = %q; want both %q", got.Base, got.StoredBase, base)
	}
	if got.Candidate != "" {
		t.Errorf("candidate = %q, want empty", got.Candidate)
	}
}

// TestEpicStoredAtVersion16ReadsBackWithEmptyEvidence stores an epic and a Requirement at migration 16, before the
// evidence migration, and reads them back after Open migrates the store: the base is empty and the item the zero Item.
func TestEpicStoredAtVersion16ReadsBackWithEmptyEvidence(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")

	fixture := openStoreAtVersion(t, path, 16)
	const id = seededEpic
	seedEpic(t, fixture)
	if _, err := fixture.ExecContext(ctx, `INSERT INTO epic_requirements (epic, position, id, text, evidence, kind)
		VALUES (?, 1, 'R1', 'Stored before evidence', 'store test', 'check')`, id); err != nil {
		fixture.Close()
		t.Fatalf("seed requirement: %v", err)
	}
	if err := fixture.Close(); err != nil {
		t.Fatalf("close fixture: %v", err)
	}

	s, err := Open(ctx, path, noSync())
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
		// The epic predates migration 00042, so its phase time is the unix second 0.
		PhaseAt: time.Unix(0, 0),
		Requirements: []EpicRequirement{
			{ID: "R1", Text: "Stored before evidence", Evidence: "store test", Kind: "check"},
		},
		Decisions: []EpicDecision{{Seq: 1, ID: "D1", Text: "Use the store", Time: at}},
		Workflow:  builtInEpicWorkflow(),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("epic stored at version 16 = %+v, want %+v", got, want)
	}
}

// TestEpicTaskStoredAtVersion16MigratesCheckToEvidence stores a task with a check at migration 16 and reads it back
// after Open migrates the store: the check is the conclusion of its evidence, its gate is empty, and it has no judge.
func TestEpicTaskStoredAtVersion16MigratesCheckToEvidence(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")

	fixture := openStoreAtVersion(t, path, 16)
	const id = seededEpic
	seedEpic(t, fixture)
	if _, err := fixture.ExecContext(ctx, `INSERT INTO epic_tasks
		(epic, seq, turn, title, deliverable, task_check, status)
		VALUES (?, 1, 1, 'Build it', 'the migration', 'go test ./x', 'open')`, id); err != nil {
		fixture.Close()
		t.Fatalf("seed task: %v", err)
	}
	if err := fixture.Close(); err != nil {
		t.Fatalf("close fixture: %v", err)
	}

	s, err := Open(ctx, path, noSync())
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

// TestEvidenceMigrationDownAndUp removes the epic gate tables, the base and item columns, and the task gate and evidence
// with the Down of 00020_evidence.sql, and Up restores them. The task it stores with an evidence item reads its
// conclusion back as task_check after the Down.
func TestEvidenceMigrationDownAndUp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, provider := storeAtVersion(t, 20)
	if _, err := s.db.ExecContext(ctx, `INSERT INTO epic_tasks
		(epic, seq, turn, title, deliverable, status, gate, evidence)
		VALUES ('orrery-0123456/epic-one', 1, 1, 'Build it', 'the down', 'open', 'check',
		'{"capture":["scripts/check"],"limit":60,"artifacts":null,"supplied":null,"conclusion":"go test ./restored",
		"judge":"a judge"}')`); err != nil {
		t.Fatalf("insert task: %v", err)
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

// TestQuestionsMigrationDownAndUp removes the question columns and the tell's question column with the Down of
// 00039_questions.sql, and Up restores them.
func TestQuestionsMigrationDownAndUp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	questionColumns := []string{
		"label", "choices", "recommended", "why", "confidence", "replaces", "replaced_by", "choice",
	}

	migrationsFS, err := fs.Sub(migrationFS, "migrations")
	if err != nil {
		t.Fatalf("sub migrations fs: %v", err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, s.db.DB, migrationsFS)
	if err != nil {
		t.Fatalf("create goose provider: %v", err)
	}

	if _, err := provider.DownTo(ctx, 38); err != nil {
		t.Fatalf("DownTo 38: %v", err)
	}
	if hasAny(columnsOf(t, s, "epic_questions"), questionColumns...) {
		t.Errorf("epic_questions columns after Down = %v, want no question columns", columnsOf(t, s, "epic_questions"))
	}
	if hasAny(columnsOf(t, s, "epic_tells"), "question") {
		t.Errorf("epic_tells columns after Down = %v, want no question column", columnsOf(t, s, "epic_tells"))
	}

	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if !hasAll(columnsOf(t, s, "epic_questions"), questionColumns...) {
		t.Errorf("epic_questions columns after Up = %v, want the question columns", columnsOf(t, s, "epic_questions"))
	}
	if !hasAll(columnsOf(t, s, "epic_tells"), "question") {
		t.Errorf("epic_tells columns after Up = %v, want the question column", columnsOf(t, s, "epic_tells"))
	}
}

// TestTaskBuildMigrationDownAndUp removes the branch and build columns and the review, finding and tell tables with
// the Down of 00021_task_build.sql, and Up restores them.
func TestTaskBuildMigrationDownAndUp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, provider := storeAtVersion(t, 21)
	buildColumns := []string{
		"stage", "base", "attempt", "rerun", "no_verdict", "held", "step", "red_run", "green_run", "revision",
		"failure", "review", "refs", "gate_pos", "finding_pos",
	}

	if _, err := provider.DownTo(ctx, 20); err != nil {
		t.Fatalf("DownTo 20: %v", err)
	}
	tables := tableNames(t, s)
	if tables["epic_task_reviews"] || tables["epic_task_findings"] || tables["epic_tells"] {
		t.Errorf("build tables after Down: epic_task_reviews %v, epic_task_findings %v, epic_tells %v; want none",
			tables["epic_task_reviews"], tables["epic_task_findings"], tables["epic_tells"])
	}
	if hasAny(columnsOf(t, s, "epics"), "branch") {
		t.Errorf("epics columns after Down = %v, want no branch", columnsOf(t, s, "epics"))
	}
	if hasAny(columnsOf(t, s, "epic_tasks"), buildColumns...) {
		t.Errorf("epic_tasks columns after Down = %v, want no build columns", columnsOf(t, s, "epic_tasks"))
	}

	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("Up: %v", err)
	}
	tables = tableNames(t, s)
	if !tables["epic_task_reviews"] || !tables["epic_task_findings"] || !tables["epic_tells"] {
		t.Errorf("build tables after Up: epic_task_reviews %v, epic_task_findings %v, epic_tells %v; want all",
			tables["epic_task_reviews"], tables["epic_task_findings"], tables["epic_tells"])
	}
	if !hasAll(columnsOf(t, s, "epics"), "branch") {
		t.Errorf("epics columns after Up = %v, want branch", columnsOf(t, s, "epics"))
	}
	if !hasAll(columnsOf(t, s, "epic_tasks"), buildColumns...) {
		t.Errorf("epic_tasks columns after Up = %v, want the build columns", columnsOf(t, s, "epic_tasks"))
	}
}

// TestWorkflowMigrationDownAndUp removes the workflow columns and the node table with the Down of 00022_workflow.sql,
// and Up restores them and pins the built-in epic to the epic stored before the Down.
func TestWorkflowMigrationDownAndUp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, provider := storeAtVersion(t, 22)
	seedEpic(t, s.db.DB)

	if _, err := provider.DownTo(ctx, 21); err != nil {
		t.Fatalf("DownTo 21: %v", err)
	}
	if tables := tableNames(t, s); tables["epic_workflow_nodes"] {
		t.Errorf("epic_workflow_nodes after Down: present, want absent")
	}
	if hasAny(columnsOf(t, s, "epics"), "workflow", "workflow_text", "workflow_sha256") {
		t.Errorf("epics columns after Down = %v, want no workflow columns", columnsOf(t, s, "epics"))
	}

	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if tables := tableNames(t, s); !tables["epic_workflow_nodes"] {
		t.Errorf("epic_workflow_nodes after Up: absent, want present")
	}
	if !hasAll(columnsOf(t, s, "epics"), "workflow", "workflow_text", "workflow_sha256") {
		t.Errorf("epics columns after Up = %v, want the workflow columns", columnsOf(t, s, "epics"))
	}
	got, err := s.Epic(ctx, seededEpic)
	if err != nil {
		t.Fatalf("Epic after Up: %v", err)
	}
	if want := builtInEpicWorkflow(); !reflect.DeepEqual(got.Workflow, want) {
		t.Errorf("workflow of seeded epic after Up = %+v, want %+v", got.Workflow, want)
	}
}

// TestDueResumesMigrationDownAndUp removes the due_resumes table and the epics pause_source column with the Down of
// 00025_due_resumes.sql, and Up restores them.
func TestDueResumesMigrationDownAndUp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, provider := storeAtVersion(t, 25)

	if _, err := provider.DownTo(ctx, 24); err != nil {
		t.Fatalf("DownTo 24: %v", err)
	}
	if tableNames(t, s)["due_resumes"] {
		t.Errorf("due_resumes after Down: present, want removed")
	}
	if hasAny(columnsOf(t, s, "epics"), "pause_source") {
		t.Errorf("epics columns after Down = %v, want no pause_source", columnsOf(t, s, "epics"))
	}

	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if !tableNames(t, s)["due_resumes"] {
		t.Errorf("due_resumes after Up: absent, want present")
	}
	if !hasAll(columnsOf(t, s, "epics"), "pause_source") {
		t.Errorf("epics columns after Up = %v, want pause_source", columnsOf(t, s, "epics"))
	}
}

// TestLimitToldMigrationDownAndUp removes the told limit steps table and the limit key column with the Down of
// 00026_limit_told.sql, and Up restores them.
func TestLimitToldMigrationDownAndUp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, provider := storeAtVersion(t, 26)

	if _, err := provider.DownTo(ctx, 25); err != nil {
		t.Fatalf("DownTo 25: %v", err)
	}
	if tableNames(t, s)["told_limit_steps"] {
		t.Errorf("told_limit_steps after Down: want no table")
	}
	if hasAny(columnsOf(t, s, "pending_notices"), "limit_key") {
		t.Errorf("pending_notices columns after Down = %v, want no limit_key", columnsOf(t, s, "pending_notices"))
	}

	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if !tableNames(t, s)["told_limit_steps"] {
		t.Errorf("told_limit_steps after Up: want the table")
	}
	if !hasAll(columnsOf(t, s, "pending_notices"), "limit_key") {
		t.Errorf("pending_notices columns after Up = %v, want limit_key", columnsOf(t, s, "pending_notices"))
	}
}

// TestReleaseMigrationDownAndUp removes the release run and release slot tables with the Down of 00038_release.sql,
// and Up restores them.
func TestReleaseMigrationDownAndUp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)

	migrationsFS, err := fs.Sub(migrationFS, "migrations")
	if err != nil {
		t.Fatalf("sub migrations fs: %v", err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, s.db.DB, migrationsFS)
	if err != nil {
		t.Fatalf("create goose provider: %v", err)
	}

	if _, err := provider.DownTo(ctx, 37); err != nil {
		t.Fatalf("DownTo 37: %v", err)
	}
	if tables := tableNames(t, s); tables["epic_release_runs"] || tables["epic_release_slots"] {
		t.Errorf("release tables after Down: epic_release_runs %v, epic_release_slots %v; want neither",
			tables["epic_release_runs"], tables["epic_release_slots"])
	}

	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if tables := tableNames(t, s); !tables["epic_release_runs"] || !tables["epic_release_slots"] {
		t.Errorf("release tables after Up: epic_release_runs %v, epic_release_slots %v; want both",
			tables["epic_release_runs"], tables["epic_release_slots"])
	}
}

func TestPlannerControlsDefaultOnRowsStoredAtVersion16(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")

	const epic = seededEpic
	fixture := openStoreAtVersion(t, path, 16)
	seedEpic(t, fixture)
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

	s, err := Open(ctx, path, noSync())
	if err != nil {
		t.Fatalf("Open at version 16: %v", err)
	}
	defer s.Close()

	// Each stored row keeps its values and gains what later migrations give it at the end of its table: the epic
	// stored at version 16 pins the built-in epic workflow.
	added := map[string][]any{
		"epics": {
			int64(0),
			"",
			"",
			"epic",
			builtInEpicText,
			textSHA256(builtInEpicText),
			"",
			"",
			"",
			"",
			int64(0),
		},
		"epic_turns":      {int64(0), int64(0), int64(0)},
		"epic_decisions":  {int64(0)},
		"pending_notices": {"", ""},
	}
	for _, table := range tables {
		want := make([][]any, len(before[table]))
		for i, row := range before[table] {
			want[i] = append(append([]any{}, row...), added[table]...)
		}
		if got := rowsOf(t, s.db.DB, table); !reflect.DeepEqual(got, want) {
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
