package store_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/storetest"
)

// usageEpic is the epic the usage tests list, and usageOther a second epic whose rows the listing of the first must
// leave out.
const (
	usageEpic  = "orrery-3c4d5e6/epic-usage"
	usageOther = "orrery-3c4d5e6/epic-other"
)

// openUsageStore seeds a migrated store file at a fresh path. It returns the path and a raw handle that inserts the
// invented rows of a test into the file.
func openUsageStore(t *testing.T) (*sql.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "orrery.db")
	storetest.Seed(t, path)
	raw, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open raw handle: %v", err)
	}
	t.Cleanup(func() {
		if err := raw.Close(); err != nil {
			t.Errorf("close raw handle: %v", err)
		}
	})
	return raw, path
}

// execUsageRow runs one insert of an invented row on raw.
func execUsageRow(t *testing.T, raw *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := raw.Exec(query, args...); err != nil {
		t.Fatalf("insert row: %v", err)
	}
}

// insertUsageSession inserts a sessions row with id, of kind, whose claude session is claude.
func insertUsageSession(t *testing.T, raw *sql.DB, id, kind, claude string) {
	t.Helper()
	execUsageRow(t, raw, `INSERT INTO sessions (id, server, pane, kind, claude_session) VALUES (?, '', '', ?, ?)`,
		id, kind, claude)
}

// insertUsageStep inserts a steps row with id, of epic, whose caller is caller.
func insertUsageStep(t *testing.T, raw *sql.DB, id, epic, caller string) {
	t.Helper()
	execUsageRow(t, raw, `INSERT INTO steps (id, agent, prompt, output_schema, workspace, revision, worktree,
		done_value, read_only, owned, gate, gate_limit, started, checks, result, status, end_value, reason, ended,
		epic, caller)
		VALUES (?, 'haiku', 'Do the task', '{}', '/work/ws', 'abc1234', '/work/wt', 'done', 0, '[]', '[]', 0,
		1700000000, '[]', '{}', 'passed', '', '', 1700000600, ?, ?)`, id, epic, caller)
}

// insertUsageEpic inserts an epics row with id whose steerer is steerer, which is "" for no steerer.
func insertUsageEpic(t *testing.T, raw *sql.DB, id, steerer string) {
	t.Helper()
	execUsageRow(t, raw, `INSERT INTO epics (id, repository, title, approved, intent, care, approach, release_then,
		checks, phase, created, changed, steerer)
		VALUES (?, '/work/repo', 'Epic', 'approved', 'intent', 'serious', 'approach', 'then', 'checks', 'design',
		1700000000, 1700000000, ?)`, id, steerer)
}

// insertUsageCall inserts an epic_sessions row that links session to epic by a call of call.
func insertUsageCall(t *testing.T, raw *sql.DB, epic, session, call string) {
	t.Helper()
	execUsageRow(t, raw, `INSERT INTO epic_sessions (epic, session, call, at) VALUES (?, ?, ?, 1700000100)`,
		epic, session, call)
}

// usageSessions lists the sessions of epic in the store file at path.
func usageSessions(t *testing.T, path, epic string) []store.EpicUsageSession {
	t.Helper()
	got, err := store.NewReader(path).EpicUsageSessions(context.Background(), epic)
	if err != nil {
		t.Fatalf("EpicUsageSessions(%s): %v", epic, err)
	}
	return got
}

// assertUsageSessions fails unless got lists the sessions of want, in order. Two empty lists are equal.
func assertUsageSessions(t *testing.T, got, want []store.EpicUsageSession) {
	t.Helper()
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("EpicUsageSessions = %+v, want %+v", got, want)
	}
}

func TestStoreEpicUsageSessions(t *testing.T) {
	t.Parallel()

	t.Run("steerer and two steps, a step with an epic_sessions row listed once", func(t *testing.T) {
		raw, path := openUsageStore(t)
		insertUsageEpic(t, raw, usageEpic, "steer-1")
		insertUsageSession(t, raw, "steer-1", "", "claude-steer")
		insertUsageSession(t, raw, "step-a", "step", "claude-a")
		insertUsageSession(t, raw, "step-b", "step", "claude-b")
		insertUsageStep(t, raw, "step-a", usageEpic, "steer-1")
		insertUsageStep(t, raw, "step-b", usageEpic, "steer-1")
		insertUsageCall(t, raw, usageEpic, "step-a", "epic_create")

		assertUsageSessions(t, usageSessions(t, path, usageEpic), []store.EpicUsageSession{
			{Session: "step-a", ClaudeSession: "claude-a"},
			{Session: "step-b", ClaudeSession: "claude-b"},
			{Session: "steer-1", ClaudeSession: "claude-steer", Shared: true},
		})
	})

	t.Run("a session linked only by epic_get is absent", func(t *testing.T) {
		raw, path := openUsageStore(t)
		insertUsageEpic(t, raw, usageEpic, "")
		insertUsageSession(t, raw, "step-a", "step", "claude-a")
		insertUsageStep(t, raw, "step-a", usageEpic, "")
		insertUsageSession(t, raw, "get-1", "", "claude-get")
		insertUsageCall(t, raw, usageEpic, "get-1", "epic_get")

		assertUsageSessions(t, usageSessions(t, path, usageEpic), []store.EpicUsageSession{
			{Session: "step-a", ClaudeSession: "claude-a"},
		})
	})

	t.Run("a steerer that steers two epics is shared in both", func(t *testing.T) {
		raw, path := openUsageStore(t)
		insertUsageEpic(t, raw, usageEpic, "steer-1")
		insertUsageEpic(t, raw, usageOther, "steer-1")
		insertUsageSession(t, raw, "steer-1", "", "claude-steer")
		insertUsageSession(t, raw, "step-a", "step", "claude-a")
		insertUsageSession(t, raw, "step-b", "step", "claude-b")
		insertUsageStep(t, raw, "step-a", usageEpic, "")
		insertUsageStep(t, raw, "step-b", usageOther, "")

		assertUsageSessions(t, usageSessions(t, path, usageEpic), []store.EpicUsageSession{
			{Session: "step-a", ClaudeSession: "claude-a"},
			{Session: "steer-1", ClaudeSession: "claude-steer", Shared: true},
		})
		assertUsageSessions(t, usageSessions(t, path, usageOther), []store.EpicUsageSession{
			{Session: "step-b", ClaudeSession: "claude-b"},
			{Session: "steer-1", ClaudeSession: "claude-steer", Shared: true},
		})
	})

	t.Run("a steerer that is also a step is listed once, as a step", func(t *testing.T) {
		raw, path := openUsageStore(t)
		insertUsageEpic(t, raw, usageEpic, "step-a")
		insertUsageSession(t, raw, "step-a", "step", "claude-a")
		insertUsageSession(t, raw, "step-b", "step", "claude-b")
		insertUsageStep(t, raw, "step-a", usageEpic, "")
		insertUsageStep(t, raw, "step-b", usageEpic, "")

		assertUsageSessions(t, usageSessions(t, path, usageEpic), []store.EpicUsageSession{
			{Session: "step-a", ClaudeSession: "claude-a"},
			{Session: "step-b", ClaudeSession: "claude-b"},
		})
	})

	t.Run("a record epic's callers of its steps are listed shared", func(t *testing.T) {
		raw, path := openUsageStore(t)
		// usageEpic has no epics row, so it is a record epic. step-q's caller is step-r, one of the epic's own steps,
		// so step-r is listed as a step and not as a caller.
		insertUsageSession(t, raw, "caller-c", "", "claude-c")
		insertUsageSession(t, raw, "step-p", "step", "claude-p")
		insertUsageSession(t, raw, "step-q", "step", "claude-q")
		insertUsageSession(t, raw, "step-r", "step", "claude-r")
		insertUsageStep(t, raw, "step-r", usageEpic, "caller-c")
		insertUsageStep(t, raw, "step-q", usageEpic, "step-r")
		insertUsageStep(t, raw, "step-p", usageEpic, "caller-c")

		assertUsageSessions(t, usageSessions(t, path, usageEpic), []store.EpicUsageSession{
			{Session: "step-p", ClaudeSession: "claude-p"},
			{Session: "step-q", ClaudeSession: "claude-q"},
			{Session: "step-r", ClaudeSession: "claude-r"},
			{Session: "caller-c", ClaudeSession: "claude-c", Shared: true},
		})
	})

	t.Run("an epic with an empty steerer lists its callers shared", func(t *testing.T) {
		raw, path := openUsageStore(t)
		insertUsageEpic(t, raw, usageEpic, "")
		insertUsageSession(t, raw, "caller-c", "", "claude-c")
		insertUsageSession(t, raw, "step-a", "step", "claude-a")
		insertUsageStep(t, raw, "step-a", usageEpic, "caller-c")

		assertUsageSessions(t, usageSessions(t, path, usageEpic), []store.EpicUsageSession{
			{Session: "step-a", ClaudeSession: "claude-a"},
			{Session: "caller-c", ClaudeSession: "claude-c", Shared: true},
		})
	})

	t.Run("another epic's steps are absent", func(t *testing.T) {
		raw, path := openUsageStore(t)
		insertUsageSession(t, raw, "step-1", "step", "claude-1")
		insertUsageSession(t, raw, "step-2", "step", "claude-2")
		insertUsageSession(t, raw, "caller-2", "", "claude-2c")
		insertUsageStep(t, raw, "step-1", usageEpic, "")
		insertUsageStep(t, raw, "step-2", usageOther, "caller-2")

		assertUsageSessions(t, usageSessions(t, path, usageEpic), []store.EpicUsageSession{
			{Session: "step-1", ClaudeSession: "claude-1"},
		})
	})

	t.Run("a step with no sessions row has an empty Claude session", func(t *testing.T) {
		raw, path := openUsageStore(t)
		insertUsageStep(t, raw, "step-1", usageEpic, "")

		assertUsageSessions(t, usageSessions(t, path, usageEpic), []store.EpicUsageSession{
			{Session: "step-1", ClaudeSession: ""},
		})
	})

	t.Run("an epic nothing names is empty", func(t *testing.T) {
		raw, path := openUsageStore(t)
		insertUsageEpic(t, raw, usageOther, "steer-2")
		insertUsageSession(t, raw, "steer-2", "", "claude-steer")
		insertUsageSession(t, raw, "step-2", "step", "claude-2")
		insertUsageStep(t, raw, "step-2", usageOther, "steer-2")

		assertUsageSessions(t, usageSessions(t, path, usageEpic), nil)
	})
}

// sessionClaudeOf reads SessionClaude of id in the store file at path and fails on an error.
func sessionClaudeOf(t *testing.T, path, id string) (string, bool) {
	t.Helper()
	claude, found, err := store.NewReader(path).SessionClaude(context.Background(), id)
	if err != nil {
		t.Fatalf("SessionClaude(%s): %v", id, err)
	}
	return claude, found
}

func TestStoreSessionClaude(t *testing.T) {
	t.Parallel()

	t.Run("a pane session is found with its Claude session", func(t *testing.T) {
		raw, path := openUsageStore(t)
		insertUsageSession(t, raw, "pane-1", "", "claude-pane")

		claude, found := sessionClaudeOf(t, path, "pane-1")
		if !found || claude != "claude-pane" {
			t.Errorf("SessionClaude(pane-1) = %q, %v; want %q, true", claude, found, "claude-pane")
		}
	})

	t.Run("an engine session is found with its Claude session", func(t *testing.T) {
		raw, path := openUsageStore(t)
		insertUsageSession(t, raw, "engine-1", store.KindEngine, "claude-engine")

		claude, found := sessionClaudeOf(t, path, "engine-1")
		if !found || claude != "claude-engine" {
			t.Errorf("SessionClaude(engine-1) = %q, %v; want %q, true", claude, found, "claude-engine")
		}
	})

	t.Run("a step session is found with its Claude session", func(t *testing.T) {
		raw, path := openUsageStore(t)
		insertUsageSession(t, raw, "step-1", store.KindStep, "claude-step")

		claude, found := sessionClaudeOf(t, path, "step-1")
		if !found || claude != "claude-step" {
			t.Errorf("SessionClaude(step-1) = %q, %v; want %q, true", claude, found, "claude-step")
		}
	})

	t.Run("a session with no Claude session is found with an empty one", func(t *testing.T) {
		raw, path := openUsageStore(t)
		insertUsageSession(t, raw, "pane-1", "", "")

		claude, found := sessionClaudeOf(t, path, "pane-1")
		if !found || claude != "" {
			t.Errorf("SessionClaude(pane-1) = %q, %v; want \"\", true", claude, found)
		}
	})

	t.Run("an unknown id is not found", func(t *testing.T) {
		raw, path := openUsageStore(t)
		insertUsageSession(t, raw, "pane-1", "", "claude-pane")

		claude, found := sessionClaudeOf(t, path, "missing")
		if found || claude != "" {
			t.Errorf("SessionClaude(missing) = %q, %v; want \"\", false", claude, found)
		}
	})
}
