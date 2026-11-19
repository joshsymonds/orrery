package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

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

// TestEpicRevisionCountsEveryEpicTableAndNoTriggerDoes reads the migrated schema: no revision trigger is left, and the
// tables the store's pre-update hook counts, as the store reads each table's key, are every table with an epic column
// but those revisionUncounted names, epics, and evidence_artifacts, whose runs name the epic. The store reads a
// table's key from its columns, so a table added later with an epic column counts without a change here.
func TestEpicRevisionCountsEveryEpicTableAndNoTriggerDoes(t *testing.T) {
	t.Parallel()
	s := openSyncStore(t)
	if triggers := schemaNames(t, s,
		`SELECT name FROM sqlite_master WHERE type = 'trigger' AND name LIKE '%_revision_%'`); len(triggers) != 0 {
		t.Errorf("revision triggers = %v, want none", triggers)
	}
	keyed := schemaNames(t, s, `
		SELECT m.name FROM sqlite_master AS m, pragma_table_info(m.name) AS p
		WHERE m.type = 'table' AND p.name = 'epic' ORDER BY m.name`)
	if len(keyed) < 40 {
		t.Fatalf("tables keyed by epic = %v, want the epic tables of the schema", keyed)
	}
	want := append(slices.DeleteFunc(keyed, revisionUncounted), "epics", "evidence_artifacts")
	slices.Sort(want)
	var got []string
	for _, table := range schemaNames(t, s, `SELECT name FROM sqlite_master WHERE type = 'table' ORDER BY name`) {
		key, err := s.connector.keys.resolve(t.Context(), s.db.QueryContext, table)
		if err != nil {
			t.Fatalf("resolve %s: %v", table, err)
		}
		if key.counts {
			got = append(got, table)
		}
	}
	if !slices.Equal(got, want) {
		t.Errorf("tables the hook counts = %v, want %v", got, want)
	}
	if key, _ := s.connector.keys.get("evidence_artifacts"); !key.byRun {
		t.Errorf("evidence_artifacts names its epic by %+v, want by its run", key)
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

// createRevisionEpics creates each of ids, an epic in phase accepted.
func createRevisionEpics(t *testing.T, s *Store, ids ...string) {
	t.Helper()
	created := time.Unix(1_700_000_000, 0)
	for _, id := range ids {
		if err := s.CreateEpic(context.Background(), EpicRow{
			ID: id, Repository: "/repo", Title: "synthetic epic", Phase: "accepted", Created: created, Changed: created,
		}); err != nil {
			t.Fatalf("CreateEpic %s: %v", id, err)
		}
	}
}

// TestEpicRevisionCountsTheEpicsWritesAndNotItsSteps creates two epics, which moves each to revision 1, and writes to
// one: a park, which writes its row and its parks, moves its revision and not the other's, and a step labeled with it,
// started, returned and ended, moves neither.
func TestEpicRevisionCountsTheEpicsWritesAndNotItsSteps(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openSyncStore(t)
	createRevisionEpics(t, s, "repo/a", "repo/b")
	startA, startB := epicRevision(t, s, "repo/a"), epicRevision(t, s, "repo/b")
	if startA != 1 || startB != 1 {
		t.Fatalf("revisions after the creates = %d and %d, want 1 each", startA, startB)
	}

	if err := s.ParkEpic(ctx, "repo/a", ParkClassBranch, "Synthetic park.", time.Unix(1_700_000_060, 0)); err != nil {
		t.Fatalf("ParkEpic: %v", err)
	}
	if got := epicRevision(t, s, "repo/a"); got != startA+1 {
		t.Errorf("revision of repo/a after its park = %d, want %d", got, startA+1)
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
	if got := epicRevision(t, s, "repo/a"); got != startA+1 {
		t.Errorf("revision of repo/a after its step's writes = %d, want %d", got, startA+1)
	}
}

// changes records each call of the hook OnEpicsChanged sets.
type changes struct {
	mu    sync.Mutex
	calls [][]string
}

func (c *changes) record(epics []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, slices.Clone(epics))
}

// take returns the calls recorded since the last take.
func (c *changes) take() [][]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	calls := c.calls
	c.calls = nil
	return calls
}

// park is a write statement that parks epic.
func park(ctx context.Context, queries *sqlcgen.Queries, epic, reason string) error {
	if _, err := queries.SetEpicParked(
		ctx,
		sqlcgen.SetEpicParkedParams{Parked: reason, ParkClass: "branch", ID: epic},
	); err != nil {
		return fmt.Errorf("park %s: %w", epic, err)
	}
	return nil
}

// TestOnEpicsChangedFiresOncePerCommitWithTheMovedEpics writes through the store's write and counts the hook's calls
// and the revisions. A write of three rows of one epic and one row of another calls the hook once with both and moves
// each revision by one, since a write moves an epic once however many of its rows it writes. A write that rolls back
// calls nothing and moves nothing, a write of a step only calls nothing, and an evidence artifact moves the epic of its
// run.
func TestOnEpicsChangedFiresOncePerCommitWithTheMovedEpics(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openSyncStore(t)
	createRevisionEpics(t, s, "repo/a", "repo/b", "repo/c")
	seen := &changes{}
	s.OnEpicsChanged(seen.record)

	if err := s.write(ctx, func(queries *sqlcgen.Queries) error {
		for _, reason := range []string{"one", "two", "three"} {
			if err := park(ctx, queries, "repo/b", reason); err != nil {
				return err
			}
		}
		return park(ctx, queries, "repo/a", "one")
	}); err != nil {
		t.Fatalf("write of two epics: %v", err)
	}
	if calls := seen.take(); len(calls) != 1 || !slices.Equal(calls[0], []string{"repo/a", "repo/b"}) {
		t.Errorf("hook calls after a write of two epics = %v, want one with repo/a and repo/b", calls)
	}
	for id, want := range map[string]int64{"repo/a": 2, "repo/b": 2, "repo/c": 1} {
		if got := epicRevision(t, s, id); got != want {
			t.Errorf("revision of %s = %d, want %d", id, got, want)
		}
	}

	refused := errors.New("synthetic refusal")
	if err := s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := park(ctx, queries, "repo/c", "rolled back"); err != nil {
			return err
		}
		return refused
	}); !errors.Is(err, refused) {
		t.Fatalf("rolled back write = %v, want the refusal", err)
	}
	if err := s.ApplyEngine(
		ctx,
		[]EngineOp{labelledStepOf("synthetic-step", "repo/c", "T1")},
		engineNowAt(),
	); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}
	if calls := seen.take(); len(calls) != 0 {
		t.Errorf("hook calls after a rolled back write and a step's write = %v, want none", calls)
	}
	if got := epicRevision(t, s, "repo/c"); got != 1 {
		t.Errorf("revision of repo/c after a rolled back write and a step's write = %d, want 1", got)
	}

	if err := s.write(ctx, func(queries *sqlcgen.Queries) error {
		return queries.InsertEvidenceRun(ctx, sqlcgen.InsertEvidenceRunParams{
			ID: "run-1", Epic: "repo/c", Item: "R1", Phase: "red", Source: "captured", Spec: "{}", Created: 1,
		})
	}); err != nil {
		t.Fatalf("insert evidence run: %v", err)
	}
	if err := s.write(ctx, func(queries *sqlcgen.Queries) error {
		return queries.InsertEvidenceArtifact(ctx, sqlcgen.InsertEvidenceArtifactParams{
			Run: "run-1", Position: 0, Path: "out.txt", State: "missing",
		})
	}); err != nil {
		t.Fatalf("insert evidence artifact: %v", err)
	}
	if calls := seen.take(); len(calls) != 2 || !slices.Equal(calls[1], []string{"repo/c"}) {
		t.Errorf("hook calls after a run and its artifact = %v, want two, the second with repo/c", calls)
	}
	if got := epicRevision(t, s, "repo/c"); got != 3 {
		t.Errorf("revision of repo/c after its run and its artifact = %d, want 3", got)
	}
}

// TestCloseReportsAnEpicWriteOutsideAStoreWrite writes an epic's row on the store's own connection outside Store.write,
// which moves no revision, and requires Close to report it by table. Every store test's Close checks its error, so a
// write path that bypassed the hook's write would fail the tests that exercise it.
func TestCloseReportsAnEpicWriteOutsideAStoreWrite(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	seedMigrated(t, path)
	s, err := Open(ctx, path, noSync())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	createRevisionEpics(t, s, "repo/a")
	if _, err = s.db.ExecContext(ctx, `UPDATE epics SET summary = 'Synthetic.' WHERE id = ?`, "repo/a"); err != nil {
		t.Fatalf("write outside a store write: %v", err)
	}
	err = s.Close()
	if !errors.Is(err, ErrEpicWriteOutsideWrite) || !strings.Contains(err.Error(), "epics (1)") {
		t.Fatalf("Close = %v, want %v naming epics (1)", err, ErrEpicWriteOutsideWrite)
	}
}

// fixtureExec runs query with args as a store write, as a fixture writes rows the store's API has no call for, so the
// write is recorded as any store write is and moves the revision of each epic it writes.
func (s *Store) fixtureExec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	var result sql.Result
	err := s.writeDB(ctx, func(db sqlcgen.DBTX) error {
		var err error
		result, err = db.ExecContext(ctx, query, args...)
		return err
	})
	return result, err
}

// fixtureDB is the store as a fixture's queries run on it: each statement that writes runs as a store write
// (fixtureExec), and each read runs on the store's pool.
type fixtureDB struct {
	s *Store
}

// fixtureQueries returns the queries a fixture writes rows through, each write a store write.
func (s *Store) fixtureQueries() *sqlcgen.Queries {
	return sqlcgen.New(fixtureDB{s: s})
}

func (f fixtureDB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return f.s.fixtureExec(ctx, query, args...)
}

func (f fixtureDB) PrepareContext(ctx context.Context, query string) (*sql.Stmt, error) {
	stmt, err := f.s.db.PrepareContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("prepare fixture query: %w", err)
	}
	return stmt, nil
}

func (f fixtureDB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return f.s.db.QueryContext(ctx, query, args...)
}

func (f fixtureDB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return f.s.db.QueryRowContext(ctx, query, args...)
}
