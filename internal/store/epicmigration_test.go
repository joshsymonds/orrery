package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// epicTableColumns returns, keyed by table, the columns in declaration order of every table migrations 00014, 00016,
// 00019, 00028, 00030, 00035, 00036, 00037, 00038, 00040, 00041, 00043, 00044, 00049, 00051, 00052, 00079 and 00081
// create, and of the columns those migrations and 00020, 00021, 00022, 00025, 00029, 00033, 00034, 00035, 00039, 00040,
// 00042, 00044, 00045, 00048, 00050, 00051, 00052, 00053, 00054, 00057, 00065, 00070, 00078, 00079, 00080, 00081,
// 00083 and 00085 add to epic tables.
func epicTableColumns() map[string][]string {
	return map[string][]string{
		"epics": {
			"id", "repository", "title", "approved", "intent", "care", "approach", "release_then", "checks",
			"phase", "created", "changed", "paused", "parked", "retried", "base", "branch", "workflow",
			"workflow_text", "workflow_sha256", "pause_source", "stored_base", "candidate", "summary", "phase_at",
			"report_phase", "report_shipped", "report_not_shipped", "report_why",
			// R6: closed_by and superseded_by, the closer and the epic that replaces it, are 00048_epic_close.sql.
			"closed_by", "superseded_by",
			// park_class, the class of the park, which says who answers it, is 00050_park_class.sql.
			"park_class",
			// steerer, the session that steers the epic, is 00080_epic_steerer.sql.
			"steerer",
			// host, the host the epic's work lives on, is 00097_engine_hosts.sql.
			"host",
		},
		// Each epic's revision, which 00072_epic_revisions_table.sql moves out of epics.
		"epic_revisions": {"epic", "revision"},
		"epic_failures":  {"epic", "position", "id", "text", "rating", "response"},
		"epic_additions": {"epic", "position", "location", "what", "why", "cost"},
		// R4: dropped_by, the decision that dropped a Requirement as met at base, is 00044_met_at_base_question.sql.
		"epic_requirements":  {"epic", "position", "id", "text", "evidence", "kind", "item", "dropped_by"},
		"epic_premises":      {"epic", "position", "id", "text", "if_wrong"},
		"epic_exclusions":    {"epic", "position", "text"},
		"epic_alternatives":  {"epic", "position", "alternative", "why_not", "reconsider"},
		"epic_release_steps": {"epic", "position", "step", "target", "undo", "kind", "params"},
		"epic_gates": {
			"epic", "position", "name", "argv", "limit_seconds", "needs", "agent", "must", "must_not",
		},
		"epic_gate_lists": {"epic", "list", "position", "gate"},
		// The recipient an owner's decision is owed to, from 00081_epic_actions.sql.
		"epic_decisions": {
			"epic", "seq", "client_id", "text", "time", "question", "maker", "maker_session", "recipient",
		},
		// The epic's action log: each move and owner or orreryd control, from 00081_epic_actions.sql.
		"epic_actions": {
			"epic", "seq", "kind", "maker", "maker_session", "from_session", "to_session", "recipient", "at",
		},
		"epic_triggers": {"epic", "id", "cause", "state", "turn", "created"},
		"epic_turns": {
			"epic", "seq", "step", "state", "reason", "seen", "outline", "outcome", "started", "ended",
			"told", "stop", "resets",
		},
		"epic_turn_decisions": {"epic", "turn", "position", "text"},
		"epic_tasks": {
			"epic", "seq", "turn", "title", "deliverable", "status", "gate", "evidence", "stage", "base", "attempt",
			"rerun", "no_verdict", "held", "step", "red_run", "green_run", "revision", "failure", "review", "refs",
			"gate_pos", "finding_pos", "batch", "guard", "needs_files", "decisions", "after_tasks", "question",
			// The declared gate-needed changes, from 00065_task_gate_changes.sql.
			"gate_changes",
			// The first line of each failure a re-brief answered, from 00077_gambit_ladder.sql.
			"rebriefs",
			// The notes a task's build holds, a JSON list, from 00078_task_review_undetermined.sql.
			"notes",
			// The task's reopen generation, from 00087_task_generation.sql.
			"generation",
			// The seqs of the tasks the task depends on, from 00088_task_depends_on.sql.
			"depends_on",
			// The task's kind and the seq of the design task it revises, from 00091_task_kind.sql.
			"kind",
			"revises",
			// The seq of the task this task was split from, from 00104_task_split_test_runs.sql.
			"split_from",
		},
		"epic_task_files":        {"epic", "task", "position", "path"},
		"epic_task_requirements": {"epic", "task", "position", "requirement"},
		"epic_task_contributes":  {"epic", "task", "position", "requirement"},
		"epic_task_test_changes": {"epic", "task", "position", "path"},
		// The task spec versions, from 00063_task_versions.sql.
		"epic_task_versions": {
			"epic", "task", "version", "source", "decision", "step", "reason", "deliverable", "files", "test_changes",
			"gate", "evidence", "state", "red_run", "refusal", "created", "settled",
		},
		// The reason a review gate with no holds or fails verdict ended so, from 00078_task_review_undetermined.sql. The
		// generation each review, finding, rung and outcome was written under is from 00087_task_generation.sql.
		"epic_task_reviews": {
			"epic", "task", "generation", "attempt", "position", "gate", "step", "verdict", "reason",
		},
		"epic_task_findings": {
			"epic", "task", "generation", "attempt", "review", "position", "id", "claim", "citation", "location",
			"evidence", "verify_by", "step", "result", "reason", "unverified",
		},
		// The test runs of a task's attempts and reviews, from 00104_task_split_test_runs.sql.
		"epic_task_test_runs": {
			"epic", "task", "generation", "attempt", "review", "position", "command", "revision", "step", "output",
		},
		// The task ladder's rungs and their outcomes, from 00070_task_ladder.sql.
		"epic_task_rungs": {
			"epic", "task", "generation", "rung", "kind", "moved_by", "cause", "gate", "assertion", "step", "created",
		},
		"epic_task_rung_outcomes": {"epic", "task", "generation", "rung", "outcome", "reason", "ended"},
		// The files orchestrator decisions refused per task, from 00074_task_repairs.sql.
		"epic_task_refused": {"epic", "task", "path", "decision", "created"},
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
		"epic_tells":  {"epic", "seq", "text", "at", "told", "question", "steerer_delivered"},
		"epic_stalls": {"epic", "seq", "at", "tasks", "runs", "actions", "parked"},
		"epic_questions": {
			"epic", "seq", "turn", "text", "state", "label", "choices", "recommended", "why", "confidence", "replaces",
			"replaced_by", "choice",
			// The escalation of a question, from 00085_question_escalation.sql.
			"escalated", "escalation_reason", "escalation_maker", "escalation_session", "escalation_told",
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
		"epic_sessions":      {"epic", "session", "call", "at", "creator"},
		"epic_seen":          {"epic", "at"},
		// R4: the parked run and the Requirements found met under a met-at-base park question, from 00044.
		"epic_met_at_base": {"epic", "question", "path", "attempt", "requirements"},
		// The node run each park question names, from 00071_park_question_runs.sql.
		"epic_park_question_runs": {"epic", "question", "path"},
		// R2: the actions a planner turn returned, from 00049_planner_actions.sql. park, the planner park an action
		// answers, is 00051_park_bound.sql. requirement and item, what an amend_evidence action asks for, are
		// 00052_evidence_amendments.sql. deliverable, what a change_approach action asks for, is 00070_task_ladder.sql.
		// files and remove_files, the files a change_approach action adds and removes, are 00077_gambit_ladder.sql.
		// reviewer, the reviewer a rerun_review action names, is 00079_gap_reconcile.sql.
		"planner_actions": {
			"epic", "turn", "position", "kind", "node", "task", "close_kind", "by_epic", "undo_none", "reason", "state",
			"refusal", "park", "requirement", "item", "deliverable", "files", "remove_files", "reviewer",
			// The tasks a split action's descendants are, from 00104_task_split_test_runs.sql.
			"split_tasks",
		},
		// The gaps a planner turn declared it cannot resolve without the owner, from 00079_gap_reconcile.sql.
		"planner_unresolvable": {"epic", "turn", "position", "gap", "reason", "recommended"},
		// The planner turns each gap was given, and the reruns of the review a rerun_review action asked for, from
		// 00079_gap_reconcile.sql.
		"epic_gap_turns":     {"epic", "gap", "turn", "reason", "created"},
		"epic_review_reruns": {"epic", "reviewer", "turn", "position", "created"},
		// The amendments of the epics' Requirement evidence items, from 00052_evidence_amendments.sql.
		"epic_amendments": {
			"epic", "seq", "requirement", "amender", "turn", "position", "prior_item", "item", "reason", "base",
			"state", "red_attempt", "red_run", "stored_base", "green_attempt", "green_run", "repro_check", "refusal",
			"created", "settled",
		},
		// R5: the planner-class parks of each epic and their chains, from 00051_park_bound.sql.
		"epic_parks":         {"epic", "seq", "class", "subject", "chain", "reason", "created"},
		"engine_rate_limits": {"session_id", "status", "kind", "resets_at", "at"},
		"evidence_runs": {
			"id", "epic", "item", "revision", "phase", "source", "state", "spec", "grant_id", "holder", "exit",
			"timed_out", "not_captured", "log_hash", "log_bytes", "judge_step", "judges", "verdict", "judge_reason",
			"passed", "created", "ended", "stderr_hash", "stderr_bytes", "capture", "gatherer", "gatherers",
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

// addSessionBeforeHost inserts an engine session row the way EngineAdd did before 00103_session_host.sql added the host
// column, for a store migrated to a version before it. It appends no engine_added event.
func addSessionBeforeHost(t *testing.T, s *Store, id, dir, state string, at time.Time) {
	t.Helper()
	if _, err := s.db.ExecContext(context.Background(),
		`INSERT INTO sessions (id, server, pane, kind, dir, state, changed) VALUES (?, '', '', 'engine', ?, ?, ?)`,
		id, dir, state, engineStamp(at)); err != nil {
		t.Fatalf("insert session %s: %v", id, err)
	}
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
func seedEpic(t *testing.T, db sqlcgen.DBTX) {
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

// reviewersDownAndUp is the walk's check of 00035_reviewers.sql: its Down removes the agents column of steps, the
// unverified column of epic_task_findings and the epic_reviewers table, and its Up restores them.
func reviewersDownAndUp() migrationCheck {
	return migrationCheck{
		down: func(t *testing.T, s *Store) {
			t.Helper()
			if hasAny(tableColumns(t, s, "steps"), "agents") {
				t.Errorf("steps columns after Down = %v, want no agents column", tableColumns(t, s, "steps"))
			}
			if hasAny(tableColumns(t, s, "epic_task_findings"), "unverified") {
				t.Errorf("epic_task_findings columns after Down = %v, want no unverified column",
					tableColumns(t, s, "epic_task_findings"))
			}
			if tableNames(t, s)["epic_reviewers"] {
				t.Errorf("epic_reviewers after Down: present, want absent")
			}
		},
		up: func(t *testing.T, s *Store) {
			t.Helper()
			if !hasAll(tableColumns(t, s, "steps"), "agents") {
				t.Errorf("steps columns after Up = %v, want the agents column", tableColumns(t, s, "steps"))
			}
			if !hasAll(tableColumns(t, s, "epic_task_findings"), "unverified") {
				t.Errorf("epic_task_findings columns after Up = %v, want the unverified column",
					tableColumns(t, s, "epic_task_findings"))
			}
			if !tableNames(t, s)["epic_reviewers"] {
				t.Errorf("epic_reviewers after Up: absent, want present")
			}
		},
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
		_, err := s.fixtureExec(ctx, `INSERT INTO pushed_epic_notices (epic, time) VALUES (?, ?)`, epic, at)
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
		_, err := s.fixtureExec(ctx, `INSERT INTO epic_triggers (epic, id, cause, state, created)
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
		_, err := s.fixtureExec(ctx, `INSERT INTO epic_turns (epic, seq, state, seen, started)
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
	want := [][]any{{id, int64(1), "D1", "Use the store", int64(1700000000), int64(0), "", "", ""}}

	if got := rowsOf(t, s.db.DB, "epic_decisions"); !reflect.DeepEqual(got, want) {
		t.Fatalf("seeded decisions = %v, want %v", got, want)
	}

	_, err = s.fixtureExec(ctx, `UPDATE epic_decisions SET text = 'rewritten' WHERE epic = ?`, id)
	if err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("UPDATE on epic_decisions: err = %v, want the append-only refusal", err)
	}
	_, err = s.fixtureExec(ctx, `DELETE FROM epic_decisions WHERE epic = ?`, id)
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
	db, err := sql.Open("sqlite3", path+"?_synchronous=OFF")
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
	// Open applies 00014_epics.sql and every migration after it: 00115_step_purpose.sql is the newest.
	// The newest was 00114_epic_summaries_rewritten.sql before the step purpose migration added 00115_step_purpose.sql,
	// 00113_evidence_briefs.sql before the epic summaries rewrite added 00114_epic_summaries_rewritten.sql,
	// 00112_evidence_gatherer.sql before the evidence briefs migration added 00113_evidence_briefs.sql,
	// 00111_steps_epic_open.sql before the evidence gatherer migration added 00112_evidence_gatherer.sql,
	// 00110_steerer_tells_stalls.sql before the open steps index of an epic added
	// 00111_steps_epic_open.sql,
	// 00109_step_listings_host.sql before the steerer tells and stalls migration added 00110_steerer_tells_stalls.sql,
	// 00108_notice_host.sql before the step listings host migration added 00109_step_listings_host.sql,
	// 00107_stage_records.sql before the notice host migration added 00108_notice_host.sql,
	// 00106_contract_stage.sql before the stage records migration added 00107_stage_records.sql,
	// 00105_designs.sql before the contract stage migration added 00106_contract_stage.sql,
	// 00104_task_split_test_runs.sql before the designs migration added 00105_designs.sql,
	// 00103_session_host.sql before the task split migration added 00104_task_split_test_runs.sql,
	// 00102_session_held.sql before the session host column added 00103_session_host.sql,
	// 00101_drop_sync_columns.sql before the held sessions migration added 00102_session_held.sql,
	// 00100_epic_base_legacy_sync.sql before the sync columns' drop added 00101_drop_sync_columns.sql,
	// 00099_sync_integrate_rounds.sql before the epic base migration added 00100_epic_base_legacy_sync.sql,
	// 00098_drop_release_slots.sql before the sync integrate rounds migration added 00099,
	// 00097_engine_hosts.sql before the release slots' drop added 00098,
	// 00096_llm_accounts.sql before the engine hosts migration added 00097,
	// 00095_headless_columns.sql before the LLM accounts migration added 00096_llm_accounts.sql,
	// 00094_step_listings_no_evidence.sql before the headless columns migration added 00095,
	// 00093_design_stage.sql before the step listings lost their evidence in 00094,
	// 00092_integrate_rounds.sql before the design stage migration added 00093,
	// 00091_task_kind.sql before the integrate rounds migration added 00092,
	// 00090_closure_verdicts.sql before the task kind migration added 00091,
	// 00089_pane_sends.sql before the closure verdicts migration added 00090,
	// 00088_task_depends_on.sql before the pane sends migration added 00089,
	// 00087_task_generation.sql before the task dependencies migration added 00088,
	// 00086_amender_makers.sql before the task generation migration added 00087,
	// 00085_question_escalation.sql before the amender makers migration added 00086, 00084_contract_base.sql before
	// the question escalation migration added 00085, 00083 before the contract base migration added 00084,
	// 00082_contract_record.sql before the 00083 migration added 00083, 00081_epic_actions.sql
	// before the contract record migration added 00082, 00080_epic_steerer.sql before the
	// epic actions migration added 00081, 00079_gap_reconcile.sql before the steerer migration added 00080,
	// 00078_task_review_undetermined.sql before the gap reconciliation added 00079, 00077_gambit_ladder.sql before the
	// undetermined reviews added 00078, 00076_notice_history.sql before the ladder migration added 00077,
	// 00075_step_facts.sql before the notice history added 00076, 00074_task_repairs.sql before the step facts added
	// 00075, 00073_session_awaited.sql before the task repairs added 00074, 00072_epic_revisions_table.sql before the
	// awaited flag added 00073, 00071_park_question_runs.sql before the revisions table added 00072,
	// 00070_task_ladder.sql before the park question runs added 00071, 00069_epic_revisions.sql before the task ladder
	// added 00070, 00068_step_listings.sql before the epic revisions added 00069, 00067_drop_caller_read.sql before the
	// step listings added 00068, 00066_put_off_questions.sql before the drop of caller_read added 00067,
	// 00065_task_gate_changes.sql before the put-off marks added 00066, 00064_step_indexes.sql before the gate changes
	// added 00065, 00063_task_versions.sql before the step indexes added 00064, 00062_drop_engine_held.sql before the
	// task versions added 00063, 00061_prompt_states.sql before the drop of engine_held added 00062, and
	// 00060_cron_jobs.sql before the prompt states added 00061.
	if version != 115 {
		t.Fatalf("store version after Open = %d, want 115", version)
	}

	// A later migration may add columns at the end of an existing table, as 00015_step_readopt.sql adds to steps and
	// grants, so each row is compared over the columns it had before the migrations.
	for _, table := range existing {
		if got := leadingColumns(rowsOf(t, s.db.DB, table), before[table]); !reflect.DeepEqual(got, before[table]) {
			t.Errorf("%s after migration = %v, want %v", table, got, before[table])
		}
	}
}

// TestEpicSeenMigrationSeedsEveryEndedEpicAtItsPhaseAt stores one epic in each stored phase at migration 42, before
// epic_seen exists, and reads them after Open migrates the store: each ended epic was seen at its phase time, and each
// other epic was never seen.
func TestEpicSeenMigrationSeedsEveryEndedEpicAtItsPhaseAt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")

	phases := []struct {
		phase string
		seen  bool
	}{
		{"accepted", false},
		{"reviewing", false},
		{"correcting", false},
		{"built", false},
		{"releasing", false},
		{"clean", true},
		{"ended_with_gaps", true},
		{"stopped", true},
		{"released", true},
		{"done", true},
		{"met_at_base", true},
	}
	type seed struct {
		id, phase string
		phaseAt   int64
		seen      bool
	}
	seeds := make([]seed, len(phases))
	for i, p := range phases {
		seeds[i] = seed{
			id:      "orrery-0123456/epic-" + p.phase,
			phase:   p.phase,
			phaseAt: 1700000300 + int64(i)*100,
			seen:    p.seen,
		}
	}
	fixture := openStoreAtVersion(t, path, 42)
	for _, seed := range seeds {
		if _, err := fixture.ExecContext(ctx, `INSERT INTO epics
			(id, repository, title, approved, intent, care, approach, release_then, checks, phase, created, changed,
			phase_at)
			VALUES (?, '/work/repo', 'Epic', 'approved line', 'intent', 'serious', 'approach', 'then', 'checks', ?,
			1700000000, 1700000000, ?)`, seed.id, seed.phase, seed.phaseAt); err != nil {
			fixture.Close()
			t.Fatalf("seed epic %s: %v", seed.id, err)
		}
	}
	if err := fixture.Close(); err != nil {
		t.Fatalf("close fixture: %v", err)
	}

	s, err := Open(ctx, path, noSync())
	if err != nil {
		t.Fatalf("Open at version 42: %v", err)
	}
	defer s.Close()

	for _, seed := range seeds {
		got, err := s.Epic(ctx, seed.id)
		if err != nil {
			t.Fatalf("Epic %s: %v", seed.id, err)
		}
		if seed.seen {
			if want := time.Unix(seed.phaseAt, 0); !got.Seen.Equal(want) {
				t.Errorf("Seen of %s %s = %v, want its phase time %v", seed.phase, seed.id, got.Seen, want)
			}
		} else if !got.Seen.IsZero() {
			t.Errorf("Seen of %s %s = %v, want zero", seed.phase, seed.id, got.Seen)
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
		Evidence: evidence.Item{Conclusion: "go test ./x"}, Version: 1,
	}}
	if !reflect.DeepEqual(got.Tasks, want) {
		t.Errorf("tasks stored at version 16 = %+v, want %+v", got.Tasks, want)
	}
}

// evidenceDownAndUp is the walk's check of 00020_evidence.sql: its Down removes the epic gate tables, the base and item
// columns, the task gate and evidence, and the evidence tables, and its Up restores them. The task it stores with an
// evidence item reads its conclusion back as task_check after the Down.
func evidenceDownAndUp() migrationCheck {
	return migrationCheck{
		seed: func(t *testing.T, s *Store) {
			t.Helper()
			if _, err := s.fixtureExec(context.Background(), `INSERT INTO epic_tasks
				(epic, seq, turn, title, deliverable, status, gate, evidence)
				VALUES ('orrery-0123456/epic-one', 1, 1, 'Build it', 'the down', 'open', 'check',
				'{"capture":["scripts/check"],"limit":60,"artifacts":null,"supplied":null,"conclusion":"go test ./restored",
				"judge":"a judge"}')`); err != nil {
				t.Fatalf("insert task: %v", err)
			}
		},
		down: func(t *testing.T, s *Store) {
			t.Helper()
			var check string
			if err := s.db.QueryRowContext(context.Background(),
				`SELECT task_check FROM epic_tasks WHERE seq = 1`).Scan(&check); err != nil {
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
				t.Errorf(
					"gate tables after Down: epic_gates %v, epic_gate_lists %v; want neither",
					tables["epic_gates"],
					tables["epic_gate_lists"],
				)
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
		},
		up: func(t *testing.T, s *Store) {
			t.Helper()
			tables := tableNames(t, s)
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
		},
	}
}

// questionsDownAndUp is the walk's check of 00039_questions.sql: its Down removes the question columns and the tell's
// question column, and its Up restores them.
func questionsDownAndUp() migrationCheck {
	questionColumns := []string{
		"label", "choices", "recommended", "why", "confidence", "replaces", "replaced_by", "choice",
	}
	return migrationCheck{
		down: func(t *testing.T, s *Store) {
			t.Helper()
			if hasAny(columnsOf(t, s, "epic_questions"), questionColumns...) {
				t.Errorf("epic_questions columns after Down = %v, want no question columns",
					columnsOf(t, s, "epic_questions"))
			}
			if hasAny(columnsOf(t, s, "epic_tells"), "question") {
				t.Errorf("epic_tells columns after Down = %v, want no question column", columnsOf(t, s, "epic_tells"))
			}
		},
		up: func(t *testing.T, s *Store) {
			t.Helper()
			if !hasAll(columnsOf(t, s, "epic_questions"), questionColumns...) {
				t.Errorf("epic_questions columns after Up = %v, want the question columns",
					columnsOf(t, s, "epic_questions"))
			}
			if !hasAll(columnsOf(t, s, "epic_tells"), "question") {
				t.Errorf("epic_tells columns after Up = %v, want the question column", columnsOf(t, s, "epic_tells"))
			}
		},
	}
}

// taskBuildDownAndUp is the walk's check of 00021_task_build.sql: its Down removes the branch and build columns and the
// review, finding and tell tables, and its Up restores them.
func taskBuildDownAndUp() migrationCheck {
	buildColumns := []string{
		"stage", "base", "attempt", "rerun", "no_verdict", "held", "step", "red_run", "green_run", "revision",
		"failure", "review", "refs", "gate_pos", "finding_pos",
	}
	return migrationCheck{
		down: func(t *testing.T, s *Store) {
			t.Helper()
			tables := tableNames(t, s)
			if tables["epic_task_reviews"] || tables["epic_task_findings"] || tables["epic_tells"] {
				t.Errorf("build tables after Down: epic_task_reviews %v, epic_task_findings %v, epic_tells %v; "+
					"want none", tables["epic_task_reviews"], tables["epic_task_findings"], tables["epic_tells"])
			}
			if hasAny(columnsOf(t, s, "epics"), "branch") {
				t.Errorf("epics columns after Down = %v, want no branch", columnsOf(t, s, "epics"))
			}
			if hasAny(columnsOf(t, s, "epic_tasks"), buildColumns...) {
				t.Errorf("epic_tasks columns after Down = %v, want no build columns", columnsOf(t, s, "epic_tasks"))
			}
		},
		up: func(t *testing.T, s *Store) {
			t.Helper()
			tables := tableNames(t, s)
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
		},
	}
}

// workflowDownAndUp is the walk's check of 00022_workflow.sql: its Down removes the workflow columns and the node
// table, and its Up restores them and pins the built-in epic workflow, its text, the text's sha256 and its two nodes,
// to the epic stored before the Down.
func workflowDownAndUp() migrationCheck {
	return migrationCheck{
		seed: func(t *testing.T, s *Store) {
			t.Helper()
			seedEpic(t, s.db.DB)
		},
		down: func(t *testing.T, s *Store) {
			t.Helper()
			if tables := tableNames(t, s); tables["epic_workflow_nodes"] {
				t.Errorf("epic_workflow_nodes after Down: present, want absent")
			}
			if hasAny(columnsOf(t, s, "epics"), "workflow", "workflow_text", "workflow_sha256") {
				t.Errorf("epics columns after Down = %v, want no workflow columns", columnsOf(t, s, "epics"))
			}
		},
		up: func(t *testing.T, s *Store) {
			t.Helper()
			if tables := tableNames(t, s); !tables["epic_workflow_nodes"] {
				t.Fatalf("epic_workflow_nodes after Up: absent, want present")
			}
			if !hasAll(columnsOf(t, s, "epics"), "workflow", "workflow_text", "workflow_sha256") {
				t.Fatalf("epics columns after Up = %v, want the workflow columns", columnsOf(t, s, "epics"))
			}
			want := builtInEpicWorkflow()
			var got EpicWorkflow
			if err := s.db.QueryRowContext(context.Background(),
				`SELECT workflow, workflow_text, workflow_sha256 FROM epics WHERE id = ?`, seededEpic).Scan(
				&got.Name, &got.Text, &got.SHA256); err != nil {
				t.Fatalf("read workflow of seeded epic after Up: %v", err)
			}
			if got.Name != want.Name || got.Text != want.Text || got.SHA256 != want.SHA256 {
				t.Errorf("workflow of seeded epic after Up = %+v, want %+v", got, want)
			}
			// A node row is the epic, the node's position, its id, its parent and its kind.
			wantNodes := make([][]any, 0, len(want.Nodes))
			for position, node := range want.Nodes {
				wantNodes = append(wantNodes, []any{seededEpic, int64(position), node.ID, node.Parent, node.Kind})
			}
			if nodes := rowsOf(t, s.db.DB, "epic_workflow_nodes"); !reflect.DeepEqual(nodes, wantNodes) {
				t.Errorf("workflow nodes after Up = %v, want %v", nodes, wantNodes)
			}
		},
	}
}

// dueResumesDownAndUp is the walk's check of 00025_due_resumes.sql: its Down removes the due_resumes table and the
// epics pause_source column, and its Up restores them.
func dueResumesDownAndUp() migrationCheck {
	return migrationCheck{
		down: func(t *testing.T, s *Store) {
			t.Helper()
			if tableNames(t, s)["due_resumes"] {
				t.Errorf("due_resumes after Down: present, want removed")
			}
			if hasAny(columnsOf(t, s, "epics"), "pause_source") {
				t.Errorf("epics columns after Down = %v, want no pause_source", columnsOf(t, s, "epics"))
			}
		},
		up: func(t *testing.T, s *Store) {
			t.Helper()
			if !tableNames(t, s)["due_resumes"] {
				t.Errorf("due_resumes after Up: absent, want present")
			}
			if !hasAll(columnsOf(t, s, "epics"), "pause_source") {
				t.Errorf("epics columns after Up = %v, want pause_source", columnsOf(t, s, "epics"))
			}
		},
	}
}

// limitToldDownAndUp is the walk's check of 00026_limit_told.sql: its Down removes the told limit steps table and the
// limit key column, and its Up restores them.
func limitToldDownAndUp() migrationCheck {
	return migrationCheck{
		down: func(t *testing.T, s *Store) {
			t.Helper()
			if tableNames(t, s)["told_limit_steps"] {
				t.Errorf("told_limit_steps after Down: want no table")
			}
			if hasAny(columnsOf(t, s, "pending_notices"), "limit_key") {
				t.Errorf("pending_notices columns after Down = %v, want no limit_key",
					columnsOf(t, s, "pending_notices"))
			}
		},
		up: func(t *testing.T, s *Store) {
			t.Helper()
			if !tableNames(t, s)["told_limit_steps"] {
				t.Errorf("told_limit_steps after Up: want the table")
			}
			if !hasAll(columnsOf(t, s, "pending_notices"), "limit_key") {
				t.Errorf("pending_notices columns after Up = %v, want limit_key", columnsOf(t, s, "pending_notices"))
			}
		},
	}
}

// releaseDownAndUp is the walk's check of 00038_release.sql: its Down removes the release run table, and its Up
// restores it.
func releaseDownAndUp() migrationCheck {
	return migrationCheck{
		down: func(t *testing.T, s *Store) {
			t.Helper()
			if tableNames(t, s)["epic_release_runs"] {
				t.Errorf("epic_release_runs after Down = present, want absent")
			}
		},
		up: func(t *testing.T, s *Store) {
			t.Helper()
			if !tableNames(t, s)["epic_release_runs"] {
				t.Errorf("epic_release_runs after Up = absent, want present")
			}
		},
	}
}

// plannerActionsDownAndUp is the walk's check of 00049_planner_actions.sql: its Down removes the planner_actions table,
// and its Up restores it.
func plannerActionsDownAndUp() migrationCheck {
	return migrationCheck{
		down: func(t *testing.T, s *Store) {
			t.Helper()
			if tableNames(t, s)["planner_actions"] {
				t.Errorf("planner_actions after Down = present, want absent")
			}
		},
		up: func(t *testing.T, s *Store) {
			t.Helper()
			if !tableNames(t, s)["planner_actions"] {
				t.Errorf("planner_actions after Up = absent, want present")
			}
		},
	}
}

// epicCloseDownAndUp is the walk's check of 00048_epic_close.sql: its Down removes the closer and the replacing epic
// from the epics table, and its Up restores them.
func epicCloseDownAndUp() migrationCheck {
	return migrationCheck{
		down: func(t *testing.T, s *Store) {
			t.Helper()
			if hasAny(columnsOf(t, s, "epics"), "closed_by", "superseded_by") {
				t.Errorf("epics columns after Down = %v, want no closed_by or superseded_by", columnsOf(t, s, "epics"))
			}
		},
		up: func(t *testing.T, s *Store) {
			t.Helper()
			if !hasAll(columnsOf(t, s, "epics"), "closed_by", "superseded_by") {
				t.Errorf("epics columns after Up = %v, want closed_by and superseded_by", columnsOf(t, s, "epics"))
			}
		},
	}
}

// parkClassDownAndUp is the walk's check of 00050_park_class.sql: its Down removes the park class from the epics
// table, and its Up restores it.
func parkClassDownAndUp() migrationCheck {
	return migrationCheck{
		down: func(t *testing.T, s *Store) {
			t.Helper()
			if hasAny(columnsOf(t, s, "epics"), "park_class") {
				t.Errorf("epics columns after Down = %v, want no park_class", columnsOf(t, s, "epics"))
			}
		},
		up: func(t *testing.T, s *Store) {
			t.Helper()
			if !hasAll(columnsOf(t, s, "epics"), "park_class") {
				t.Errorf("epics columns after Up = %v, want park_class", columnsOf(t, s, "epics"))
			}
		},
	}
}

// parkBoundDownAndUp is the walk's check of 00051_park_bound.sql: its Down removes the planner parks table and the park
// of planner_actions, and its Up restores them.
func parkBoundDownAndUp() migrationCheck {
	return migrationCheck{
		down: func(t *testing.T, s *Store) {
			t.Helper()
			if tableNames(t, s)["epic_parks"] {
				t.Errorf("epic_parks after Down = present, want absent")
			}
			if hasAny(columnsOf(t, s, "planner_actions"), "park") {
				t.Errorf("planner_actions columns after Down = %v, want no park", columnsOf(t, s, "planner_actions"))
			}
		},
		up: func(t *testing.T, s *Store) {
			t.Helper()
			if !tableNames(t, s)["epic_parks"] {
				t.Errorf("epic_parks after Up = absent, want present")
			}
			if !hasAll(columnsOf(t, s, "planner_actions"), "park") {
				t.Errorf("planner_actions columns after Up = %v, want park", columnsOf(t, s, "planner_actions"))
			}
		},
	}
}

// evidenceAmendmentsDownAndUp is the walk's check of 00052_evidence_amendments.sql: its Down removes the amendments
// table and the requirement and item of planner_actions, and its Up restores them.
func evidenceAmendmentsDownAndUp() migrationCheck {
	return migrationCheck{
		down: func(t *testing.T, s *Store) {
			t.Helper()
			if tableNames(t, s)["epic_amendments"] {
				t.Errorf("epic_amendments after Down = present, want absent")
			}
			if hasAny(columnsOf(t, s, "planner_actions"), "requirement", "item") {
				t.Errorf("planner_actions columns after Down = %v, want no requirement or item",
					columnsOf(t, s, "planner_actions"))
			}
		},
		up: func(t *testing.T, s *Store) {
			t.Helper()
			if !tableNames(t, s)["epic_amendments"] {
				t.Errorf("epic_amendments after Up = absent, want present")
			}
			if !hasAll(columnsOf(t, s, "planner_actions"), "requirement", "item") {
				t.Errorf("planner_actions columns after Up = %v, want requirement and item",
					columnsOf(t, s, "planner_actions"))
			}
		},
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
			"",
			"[]",
			"[]",
			"",
			"",
			"",
			"",
			"",
			"",
		},
		"epic_turns":      {int64(0), int64(0), int64(0)},
		"epic_decisions":  {int64(0), "", "", ""},
		"pending_notices": {"", "", ""},
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
