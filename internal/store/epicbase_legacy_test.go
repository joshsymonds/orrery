package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// The epics the EpicBaseLegacy migration test seeds at version 96. legacyRunningEpic holds a running sync run and a
// running integrate run, legacyPassedEpic a passed sync run, and legacySyncedEpic a synced commit and no runs. Each
// epic's stored base is legacyStoredBase, and its base legacyBase, except that legacySyncedEpic's synced commit is
// legacySynced.
const (
	legacyRunningEpic = "orrery-0123456/epic-legacy-running"
	legacyPassedEpic  = "orrery-0123456/epic-legacy-passed"
	legacySyncedEpic  = "orrery-0123456/epic-legacy-synced"
	legacyBase        = "1111111111111111111111111111111111111111"
	legacyStoredBase  = "2222222222222222222222222222222222222222"
	legacySynced      = "3333333333333333333333333333333333333333"
	legacySkipOutput  = "the sync was removed at deploy"
)

// legacyRunFields are the node-run fields the EpicBaseLegacy migration test reads of one run.
type legacyRunFields struct {
	State, Outcome, Output, Revision string
	Attempt, Ended                   int64
}

// insertLegacyEpic inserts the epic id at version 96 with base, stored base and synced commit, as the stored epic holds
// them. An epic with no synced commit is given an empty synced column.
func insertLegacyEpic(t *testing.T, db *sql.DB, id, base, storedBase, synced string) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), `INSERT INTO epics
		(id, repository, title, approved, intent, care, approach, release_then, checks, phase, created, changed,
		base, stored_base, synced)
		VALUES (?, '/work/repo', 'Legacy epic', 'approved line', 'intent', 'serious', 'approach', 'then', 'checks',
		'accepted', 1700000000, 1700000000, ?, ?, ?)`, id, base, storedBase, synced); err != nil {
		t.Fatalf("insert epic %s: %v", id, err)
	}
}

// insertLegacyRun inserts the node run at path of epic at version 96, in state with outcome, output and revision as
// given. Its ended time is ended, zero while it runs.
func insertLegacyRun(t *testing.T, db *sql.DB, epic, path, id, kind, state, outcome, output, revision string,
	ended int64) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), `INSERT INTO epic_node_runs
		(epic, path, id, node, kind, state, outcome, output, revision, attempt, started, ended)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 1, 1700000100, ?)`,
		epic, path, id, kind, kind, state, outcome, output, revision, ended); err != nil {
		t.Fatalf("insert node run %s of %s: %v", path, epic, err)
	}
}

// legacyRunOf reads the fields of the node run at path of epic.
func legacyRunOf(t *testing.T, s *Store, epic, path string) legacyRunFields {
	t.Helper()
	var run legacyRunFields
	if err := s.db.QueryRowContext(context.Background(), `SELECT state, outcome, output, revision, attempt, ended
		FROM epic_node_runs WHERE epic = ? AND path = ?`, epic, path).Scan(
		&run.State, &run.Outcome, &run.Output, &run.Revision, &run.Attempt, &run.Ended); err != nil {
		t.Fatalf("read node run %s of %s: %v", path, epic, err)
	}
	return run
}

// TestEpicBaseLegacyMigrationEndsRunningSyncsAndMovesSyncedBase seeds at version 96 three epics: one with a running
// sync run and a running integrate run, one with a passed sync run, and one with a synced commit. Migrating the store
// up ends the running sync run as skipped, and leaves every other run as it was. The base of each epic with a synced
// commit becomes that commit, and its stored base stays where it was. Each epic with no synced commit keeps its base.
func TestEpicBaseLegacyMigrationEndsRunningSyncsAndMovesSyncedBase(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")

	fixture := openStoreAtVersion(t, path, 96)
	insertLegacyEpic(t, fixture, legacyRunningEpic, legacyBase, legacyStoredBase, "")
	insertLegacyEpic(t, fixture, legacyPassedEpic, legacyBase, legacyStoredBase, "")
	insertLegacyEpic(t, fixture, legacySyncedEpic, legacyBase, legacyStoredBase, legacySynced)
	insertLegacyRun(t, fixture, legacyRunningEpic, "plan/M1", "legacy-sync-running", "sync", "running", "", "", "", 0)
	insertLegacyRun(t, fixture, legacyRunningEpic, "plan/S2/integrate", "legacy-integrate", "integrate", "running",
		"", "", "", 0)
	insertLegacyRun(t, fixture, legacyPassedEpic, "plan/M2", "legacy-sync-passed", "sync", "ended", "passed",
		"merged", "4444444444444444444444444444444444444444", 1700000210)
	if err := fixture.Close(); err != nil {
		t.Fatalf("close fixture: %v", err)
	}

	s, err := Open(ctx, path, noSync())
	if err != nil {
		t.Fatalf("Open at version 96: %v", err)
	}
	defer s.Close()

	for _, tc := range []struct{ epic, base string }{
		{legacyRunningEpic, legacyBase},
		{legacyPassedEpic, legacyBase},
		{legacySyncedEpic, legacySynced},
	} {
		var base, storedBase string
		if err := s.db.QueryRowContext(ctx, `SELECT base, stored_base FROM epics WHERE id = ?`, tc.epic).Scan(
			&base, &storedBase); err != nil {
			t.Fatalf("read base of %s: %v", tc.epic, err)
		}
		if base != tc.base {
			t.Errorf("base of %s = %s, want %s", tc.epic, base, tc.base)
		}
		if storedBase != legacyStoredBase {
			t.Errorf("stored base of %s = %s, want %s kept", tc.epic, storedBase, legacyStoredBase)
		}
	}

	skipped := legacyRunOf(t, s, legacyRunningEpic, "plan/M1")
	if skipped.State != "ended" || skipped.Outcome != "skipped" || skipped.Output != legacySkipOutput {
		t.Errorf("running sync run = %+v, want ended skipped with %q", skipped, legacySkipOutput)
	}
	if skipped.Ended == 0 {
		t.Errorf("running sync run ended = 0, want the time it was ended")
	}
	if got := legacyRunOf(t, s, legacyRunningEpic, "plan/S2/integrate"); got.State != "running" || got.Outcome != "" {
		t.Errorf("running integrate run = %+v, want it still running with no outcome", got)
	}
	want := legacyRunFields{
		State: "ended", Outcome: "passed", Output: "merged", Revision: "4444444444444444444444444444444444444444",
		Attempt: 1, Ended: 1700000210,
	}
	if got := legacyRunOf(t, s, legacyPassedEpic, "plan/M2"); got != want {
		t.Errorf("passed sync run = %+v, want it unchanged as %+v", got, want)
	}
}
