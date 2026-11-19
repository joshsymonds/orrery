package store

import (
	"context"
	"slices"
	"testing"
	"time"
)

// revisionUncounted names the tables that hold an epic column and whose writes leave the epic's revision alone: the
// steps, which a list of the epics carries by their running steps and a count of their ended ones, and the notice
// bookkeeping, which no epic read answers.
func revisionUncounted() []string {
	return []string{"pending_notices", "pushed_epic_notices", "steps"}
}

// schemaNames returns the first column of each row query reads from s, in order.
func schemaNames(t *testing.T, s *Store, query string) []string {
	t.Helper()
	rows, err := s.db.QueryContext(context.Background(), query)
	if err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			t.Errorf("close rows: %v", err)
		}
	}()
	var names []string
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		names = append(names, name)
	}
	if err = rows.Err(); err != nil {
		t.Fatalf("read rows: %v", err)
	}
	return names
}

// TestEpicRevisionTriggersCoverEveryTableKeyedByEpic reads the migrated schema and requires an insert, an update and a
// delete trigger named <table>_revision_<op> on every table with an epic column but those revisionUncounted names, on
// evidence_artifacts, whose runs name the epic, and an update trigger on epics itself. A table added later with an epic
// column, or one rebuilt without its triggers, fails here until it counts or is named uncounted.
func TestEpicRevisionTriggersCoverEveryTableKeyedByEpic(t *testing.T) {
	t.Parallel()
	s := openSyncStore(t)
	keyed := schemaNames(t, s, `
		SELECT m.name FROM sqlite_master AS m, pragma_table_info(m.name) AS p
		WHERE m.type = 'table' AND p.name = 'epic' ORDER BY m.name`)
	if len(keyed) < 40 {
		t.Fatalf("tables keyed by epic = %v, want the epic tables of the schema", keyed)
	}
	triggers := map[string]bool{}
	for _, name := range schemaNames(t, s, `SELECT name FROM sqlite_master WHERE type = 'trigger'`) {
		triggers[name] = true
	}
	uncounted := revisionUncounted()
	counted := append(slices.DeleteFunc(keyed, func(name string) bool {
		return slices.Contains(uncounted, name)
	}), "evidence_artifacts")
	for _, table := range counted {
		for _, op := range []string{"insert", "update", "delete"} {
			if name := table + "_revision_" + op; !triggers[name] {
				t.Errorf("trigger %s is missing, so a write to %s leaves the epic's revision", name, table)
			}
		}
	}
	if !triggers["epics_revision_update"] {
		t.Error("trigger epics_revision_update is missing, so a write to an epic's row leaves its revision")
	}
}

// epicRevision reads the revision of epic as Epics answers it.
func epicRevision(t *testing.T, s *Store, epic string) int64 {
	t.Helper()
	rows, err := s.Epics(context.Background())
	if err != nil {
		t.Fatalf("Epics: %v", err)
	}
	for _, row := range rows {
		if row.ID == epic {
			return row.Revision
		}
	}
	t.Fatalf("Epics lacks %s", epic)
	return 0
}

// TestEpicRevisionCountsTheEpicsWritesAndNotItsSteps creates two epics and writes to one: a park, which writes its row
// and its parks, moves its revision and not the other's, and a step labeled with it, started, returned and ended, moves
// neither.
func TestEpicRevisionCountsTheEpicsWritesAndNotItsSteps(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openSyncStore(t)
	created := time.Unix(1_700_000_000, 0)
	for _, id := range []string{"repo/a", "repo/b"} {
		if err := s.CreateEpic(ctx, EpicRow{
			ID: id, Repository: "/repo", Title: "synthetic epic", Phase: "accepted", Created: created, Changed: created,
		}); err != nil {
			t.Fatalf("CreateEpic %s: %v", id, err)
		}
	}
	startA, startB := epicRevision(t, s, "repo/a"), epicRevision(t, s, "repo/b")

	if err := s.ParkEpic(ctx, "repo/a", ParkClassBranch, "Synthetic park.", created.Add(time.Minute)); err != nil {
		t.Fatalf("ParkEpic: %v", err)
	}
	parkedA := epicRevision(t, s, "repo/a")
	if parkedA <= startA {
		t.Errorf("revision of repo/a after its park = %d, want past %d", parkedA, startA)
	}
	if got := epicRevision(t, s, "repo/b"); got != startB {
		t.Errorf("revision of repo/b after repo/a's park = %d, want %d", got, startB)
	}

	step := labelledStepOf("synthetic-step", "repo/a", "T1")
	ops := []EngineOp{
		step,
		StepReturn{ID: step.ID, Result: `{"summary":"synthetic"}`},
		StepEnd{ID: step.ID, End: "done", Status: "DONE", Reason: "returned"},
	}
	if err := s.ApplyEngine(ctx, ops, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}
	if got := epicRevision(t, s, "repo/a"); got != parkedA {
		t.Errorf("revision of repo/a after its step's writes = %d, want %d", got, parkedA)
	}
}
