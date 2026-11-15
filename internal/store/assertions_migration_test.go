package store

import (
	"context"
	"database/sql"
	"io/fs"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/pressly/goose/v3"

	"github.com/joshsymonds/orrery/internal/evidence"
)

// assertionsProvider returns the goose provider over the embedded migrations, for the Down and Up of a store's tests.
func assertionsProvider(t *testing.T, db *sql.DB) *goose.Provider {
	t.Helper()
	migrationsFS, err := fs.Sub(migrationFS, "migrations")
	if err != nil {
		t.Fatalf("sub migrations fs: %v", err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, migrationsFS)
	if err != nil {
		t.Fatalf("create goose provider: %v", err)
	}
	return provider
}

// textOf returns the text the query returns as its only value.
func textOf(t *testing.T, db *sql.DB, query string, args ...any) string {
	t.Helper()
	var text string
	if err := db.QueryRowContext(context.Background(), query, args...).Scan(&text); err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	return text
}

// storedKinds counts the stored items, task evidence and run specs that hold a kind.
func storedKinds(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	var kinds int64
	if err := db.QueryRowContext(context.Background(), `SELECT
		(SELECT COUNT(*) FROM epic_requirements WHERE instr(item, '"kind":') > 0)
		+ (SELECT COUNT(*) FROM epic_tasks WHERE instr(evidence, '"kind":') > 0)
		+ (SELECT COUNT(*) FROM evidence_runs WHERE instr(spec, '"kind":') > 0)`).Scan(&kinds); err != nil {
		t.Fatalf("count stored kinds: %v", err)
	}
	return kinds
}

// TestAssertionsMigrationGivesItemsAndRunSpecsAKindAlike stores items and a run spec at version 33 and reads them back
// after Open migrates the store: an item or spec whose capture is a list gains the capture kind, one whose supplied
// files are a list gains the supplied kind, and the item with neither and the empty item stay as they were. The run
// kept its Requirement's item, and both migrate to the same kinded item, so the run stays current with it.
func TestAssertionsMigrationGivesItemsAndRunSpecsAKindAlike(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")

	const (
		captured = `{"capture":["scripts/check"],"limit":60,"artifacts":null,"supplied":null,` +
			`"conclusion":"the capture holds","judge":"a judge"}`
		supplied = `{"capture":null,"limit":0,"artifacts":null,"supplied":["docs/proof.md"],` +
			`"conclusion":"the files hold","judge":"a judge"}`
		neither = `{"capture":[],"limit":0,"artifacts":null,"supplied":null,"conclusion":"nothing runs",` +
			`"judge":"a judge"}`
		task = `{"capture":["scripts/task"],"limit":30,"artifacts":null,"supplied":null,` +
			`"conclusion":"the task holds","judge":"a judge"}`
	)
	const id = seededEpic
	fixture := openStoreAtVersion(t, path, 33)
	seedEpic(t, fixture)
	seeds := []struct {
		stmt string
		args []any
	}{
		{`INSERT INTO epic_requirements (epic, position, id, text, evidence, kind, item)
			VALUES (?, 1, 'R1', 'Captured', 'store test', 'check', ?)`, []any{id, captured}},
		{`INSERT INTO epic_requirements (epic, position, id, text, evidence, kind, item)
			VALUES (?, 2, 'R2', 'Supplied', 'store test', 'check', ?)`, []any{id, supplied}},
		{`INSERT INTO epic_requirements (epic, position, id, text, evidence, kind, item)
			VALUES (?, 3, 'R3', 'Neither', 'store test', 'check', ?)`, []any{id, neither}},
		{`INSERT INTO epic_requirements (epic, position, id, text, evidence, kind)
			VALUES (?, 4, 'R4', 'No item', 'store test', 'check')`, []any{id}},
		{`INSERT INTO epic_tasks (epic, seq, turn, title, deliverable, status, gate, evidence)
			VALUES (?, 1, 1, 'Build it', 'the migration', 'open', 'check', ?)`, []any{id, task}},
		{`INSERT INTO evidence_runs (id, epic, item, revision, phase, source, state, spec, created, ended)
			VALUES ('run-1', ?, 'R1', 'abc1234', 'red', 'captured', 'ended', ?, 1700000000, 1700000100)`,
			[]any{id, captured}},
	}
	for _, seed := range seeds {
		if _, err := fixture.ExecContext(ctx, seed.stmt, seed.args...); err != nil {
			fixture.Close()
			t.Fatalf("seed fixture: %v", err)
		}
	}
	if err := fixture.Close(); err != nil {
		t.Fatalf("close fixture: %v", err)
	}

	s, err := Open(ctx, path, noSync())
	if err != nil {
		t.Fatalf("Open at version 33: %v", err)
	}
	defer s.Close()

	row, err := s.Epic(ctx, id)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	requirements := make(map[string]EpicRequirement)
	for _, requirement := range row.Requirements {
		requirements[requirement.ID] = requirement
	}
	wantCaptured := evidence.Item{
		Capture: []string{"scripts/check"}, Limit: 60, Conclusion: "the capture holds", Judge: "a judge",
		Kind: evidence.KindCapture,
	}
	wantSupplied := evidence.Item{
		Supplied: []string{"docs/proof.md"}, Conclusion: "the files hold", Judge: "a judge",
		Kind: evidence.KindSupplied,
	}
	if got := requirements["R1"].Item; !reflect.DeepEqual(got, wantCaptured) {
		t.Errorf("captured requirement item = %+v, want %+v", got, wantCaptured)
	}
	if got := requirements["R2"].Item; !reflect.DeepEqual(got, wantSupplied) {
		t.Errorf("supplied requirement item = %+v, want %+v", got, wantSupplied)
	}
	if got := textOf(t, s.db.DB, `SELECT item FROM epic_requirements WHERE epic = ? AND id = 'R3'`, id); got != neither {
		t.Errorf("neither requirement item after migration = %s, want %s", got, neither)
	}
	if got := textOf(t, s.db.DB, `SELECT item FROM epic_requirements WHERE epic = ? AND id = 'R4'`, id); got != "" {
		t.Errorf("no-item requirement item after migration = %q, want empty", got)
	}

	wantTask := evidence.Item{
		Capture: []string{"scripts/task"}, Limit: 30, Conclusion: "the task holds", Judge: "a judge",
		Kind: evidence.KindCapture,
	}
	if got := row.Tasks[0].Evidence; !reflect.DeepEqual(got, wantTask) {
		t.Errorf("task evidence after migration = %+v, want %+v", got, wantTask)
	}

	run, err := s.EvidenceRun(ctx, "run-1")
	if err != nil {
		t.Fatalf("EvidenceRun: %v", err)
	}
	if !reflect.DeepEqual(run.Spec, wantCaptured) {
		t.Errorf("run spec after migration = %+v, want %+v", run.Spec, wantCaptured)
	}
}

// TestAssertionsMigrationKeepsOnlyTheLowestUnfailedClaim stores three tasks that claim R1 at version 33, seq 1 failed,
// seq 2 open and seq 3 done, and reads them back after Open migrates the store: the failed task and the lowest
// unfailed task keep R1, the later unfailed task holds it as a contribution, and the claims that are not R1 stay.
func TestAssertionsMigrationKeepsOnlyTheLowestUnfailedClaim(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")

	const id = seededEpic
	fixture := openStoreAtVersion(t, path, 33)
	seedEpic(t, fixture)
	seeds := []struct {
		stmt string
		args []any
	}{
		{`INSERT INTO epic_requirements (epic, position, id, text, evidence, kind)
			VALUES (?, 1, 'R1', 'Claimed by three', 'store test', 'check')`, []any{id}},
		{`INSERT INTO epic_requirements (epic, position, id, text, evidence, kind)
			VALUES (?, 2, 'R2', 'Claimed by one', 'store test', 'check')`, []any{id}},
		{`INSERT INTO epic_tasks (epic, seq, turn, title, deliverable, status) VALUES
			(?, 1, 1, 'Failed claim', 'the failure', 'failed'),
			(?, 2, 1, 'Open claim', 'the open work', 'open'),
			(?, 3, 1, 'Done claim', 'the done work', 'done')`, []any{id, id, id}},
		{`INSERT INTO epic_task_requirements (epic, task, position, requirement) VALUES
			(?, 1, 1, 'R1'), (?, 2, 1, 'R1'), (?, 3, 1, 'R1'), (?, 3, 2, 'R2')`, []any{id, id, id, id}},
	}
	for _, seed := range seeds {
		if _, err := fixture.ExecContext(ctx, seed.stmt, seed.args...); err != nil {
			fixture.Close()
			t.Fatalf("seed fixture: %v", err)
		}
	}
	if err := fixture.Close(); err != nil {
		t.Fatalf("close fixture: %v", err)
	}

	s, err := Open(ctx, path, noSync())
	if err != nil {
		t.Fatalf("Open at version 33: %v", err)
	}
	defer s.Close()

	row, err := s.Epic(ctx, id)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	want := []struct {
		seq          int64
		requirements []string
		contributes  []string
	}{
		{seq: 1, requirements: []string{"R1"}},
		{seq: 2, requirements: []string{"R1"}},
		{seq: 3, requirements: []string{"R2"}, contributes: []string{"R1"}},
	}
	if len(row.Tasks) != len(want) {
		t.Fatalf("tasks after migration = %d, want %d", len(row.Tasks), len(want))
	}
	for i, wantTask := range want {
		task := row.Tasks[i]
		if task.Seq != wantTask.seq || !reflect.DeepEqual(task.Requirements, wantTask.requirements) ||
			!reflect.DeepEqual(task.Contributes, wantTask.contributes) {
			t.Errorf("task %d after migration: requirements %v, contributes %v; want %v and %v",
				task.Seq, task.Requirements, task.Contributes, wantTask.requirements, wantTask.contributes)
		}
	}
}

// TestEpicTaskContributesReadBackInOrder stores two tasks with contributions and reads them back: each task keeps its
// Requirements apart from its contributions, and the contributions come back in the order the plan gave them.
func TestEpicTaskContributesReadBackInOrder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	const id = claimEpic
	createEpic(t, s, fullEpic(id))
	if _, err := s.ClaimTurn(ctx, id, claimedAt()); err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	plan := EpicPlan{
		Tasks: []EpicTaskInput{
			{
				Title: "Contribute in order", Deliverable: "contributions keep their order",
				Requirements: []string{"R3"}, Contributes: []string{"R9", "R2", "R7"}, Gate: "check",
			},
			{
				Title: "Contribute once", Deliverable: "one contribution",
				Requirements: []string{"R5"}, Contributes: []string{"R4"}, Gate: "race",
			},
		},
		Outline: "Contribute, then read", Outcome: "work",
	}
	if err := s.CompleteTurn(ctx, id, 1, plan, completedAt()); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}

	row, err := s.Epic(ctx, id)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	wantRequirements := [][]string{{"R3"}, {"R5"}}
	wantContributes := [][]string{{"R9", "R2", "R7"}, {"R4"}}
	if len(row.Tasks) != len(wantContributes) {
		t.Fatalf("tasks read back = %d, want %d", len(row.Tasks), len(wantContributes))
	}
	for i, task := range row.Tasks {
		if !reflect.DeepEqual(task.Requirements, wantRequirements[i]) ||
			!reflect.DeepEqual(task.Contributes, wantContributes[i]) {
			t.Errorf("task %d read back: requirements %v, contributes %v; want %v and %v",
				i+1, task.Requirements, task.Contributes, wantRequirements[i], wantContributes[i])
		}
	}
}

// TestAssertionsMigrationDownAndUpRoundTrips stores kinds, a claim and a contribution to it, and a run spec, then takes
// the store down to version 33 and back up: the kinds are removed by the Down and derived again by the Up, the
// contribution moves back to a claim after the task's own claim and returns as a contribution, and the epic reads back
// as it did before.
func TestAssertionsMigrationDownAndUpRoundTrips(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	const id = claimEpic
	row := fullEpic(id)
	row.Requirements = []EpicRequirement{{
		ID: "R1", Text: "Captured", Evidence: "store test", Kind: "check",
		Item: evidence.Item{
			Capture: []string{"scripts/check"}, Limit: 60, Conclusion: "the capture holds", Judge: "a judge",
			Kind: evidence.KindCapture,
		},
	}}
	createEpic(t, s, row)
	if _, err := s.ClaimTurn(ctx, id, claimedAt()); err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	plan := EpicPlan{
		Tasks: []EpicTaskInput{
			{
				Title: "Claim it", Deliverable: "the claim", Requirements: []string{"R1"}, Gate: "check",
				Evidence: evidence.Item{
					Capture: []string{"scripts/check"}, Limit: 60, Conclusion: "the claim holds", Judge: "a judge",
					Kind: evidence.KindCapture,
				},
			},
			{
				Title: "Contribute to it", Deliverable: "the contribution", Requirements: []string{"R2"},
				Contributes: []string{"R1"}, Gate: "check",
				Evidence: evidence.Item{
					Supplied: []string{"docs/proof.md"}, Conclusion: "the files hold", Judge: "a judge",
					Kind: evidence.KindSupplied,
				},
			},
		},
		Outline: "Claim, then contribute", Outcome: "work",
	}
	if err := s.CompleteTurn(ctx, id, 1, plan, completedAt()); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
	if err := s.AddEvidenceRun(ctx, EvidenceRun{
		ID: "run-1", Epic: id, Item: "R1", Revision: "abc1234", Phase: "red", Source: "captured",
		Spec: row.Requirements[0].Item, Created: completedAt(),
	}); err != nil {
		t.Fatalf("AddEvidenceRun: %v", err)
	}

	before, err := s.Epic(ctx, id)
	if err != nil {
		t.Fatalf("Epic before Down: %v", err)
	}
	beforeRun, err := s.EvidenceRun(ctx, "run-1")
	if err != nil {
		t.Fatalf("EvidenceRun before Down: %v", err)
	}
	if kinds := storedKinds(t, s.db.DB); kinds != 4 {
		t.Fatalf("stored kinds before Down = %d, want 4: the requirement, both tasks and the run spec", kinds)
	}

	provider := assertionsProvider(t, s.db.DB)
	if _, err := provider.DownTo(ctx, 33); err != nil {
		t.Fatalf("DownTo 33: %v", err)
	}
	if tableNames(t, s)["epic_task_contributes"] {
		t.Errorf("epic_task_contributes after Down: present, want absent")
	}
	if kinds := storedKinds(t, s.db.DB); kinds != 0 {
		t.Errorf("stored kinds after Down = %d, want 0", kinds)
	}
	// The contribution returns as a claim after the task's own claims, so no position is taken twice.
	wantClaims := [][]any{
		{id, int64(1), int64(1), "R1"}, {id, int64(2), int64(1), "R2"}, {id, int64(2), int64(2), "R1"},
	}
	if got := rowsOf(t, s.db.DB, "epic_task_requirements"); !reflect.DeepEqual(got, wantClaims) {
		t.Errorf("claims after Down = %v, want %v", got, wantClaims)
	}

	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if !tableNames(t, s)["epic_task_contributes"] {
		t.Errorf("epic_task_contributes after Up: absent, want present")
	}
	after, err := s.Epic(ctx, id)
	if err != nil {
		t.Fatalf("Epic after Up: %v", err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Errorf("epic after Down and Up = %+v, want %+v", after, before)
	}
	afterRun, err := s.EvidenceRun(ctx, "run-1")
	if err != nil {
		t.Fatalf("EvidenceRun after Up: %v", err)
	}
	if !reflect.DeepEqual(afterRun, beforeRun) {
		t.Errorf("run after Down and Up = %+v, want %+v", afterRun, beforeRun)
	}
}
