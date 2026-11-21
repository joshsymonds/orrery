package store

import (
	"context"
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// sendBackHistoryKeys returns the rows query reads over seededEpic, each as the strings its columns join to, in the
// order the query gives.
func sendBackHistoryKeys(t *testing.T, s *Store, query string) []string {
	t.Helper()
	rows, err := s.db.QueryContext(context.Background(), query, seededEpic)
	if err != nil {
		t.Fatalf("read rows: %v", err)
	}
	var got []string
	for rows.Next() {
		var key string
		if err = rows.Scan(&key); err != nil {
			t.Fatalf("scan rows: %v", err)
		}
		got = append(got, key)
	}
	if err = rowsDone(rows); err != nil {
		t.Fatalf("read rows: %v", err)
	}
	return got
}

// TestSendBackHistoryMigrationAssignsGenerations checks the migration that keys a task's reviews, findings, rungs and
// rung outcomes by its reopen generation. A task's generation is the count of its send_back versions. A rung belongs to
// the generation in force when it was created, and an outcome to the one in force when it ended: the count of the
// task's send_back versions created at or before that second. Reviews and findings hold no time, so they are
// generation 0.
//
// Task 1 is the shape task-branches T1 was stranded in: reviewed at attempt 1 position 0, then sent back once, with the
// send_back's rung 1 open. Task 2 was never sent back. Task 3 was sent back twice, with a grant version between, and
// holds rows from before, between and after the send_backs. Once migrated, task 1's reopened review stores at attempt 1
// position 0, at generation 1, beside the review it collided with. The Down keeps each task's current generation.
func TestSendBackHistoryMigrationAssignsGenerations(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, provider := storeAtVersion(t, 86)
	seedEpic(t, s.db.DB)
	for _, insert := range []string{
		`INSERT INTO epic_tasks (epic, seq, turn, title, deliverable, status, stage, attempt)
			VALUES (?1, 1, 1, 'Branches', 'one', 'open', 'review', 1),
			(?1, 2, 1, 'Never sent back', 'two', 'open', 'implement', 2),
			(?1, 3, 1, 'Sent back twice', 'three', 'open', 'implement', 2)`,
		`INSERT INTO epic_task_versions (epic, task, version, source, reason, created) VALUES
			(?1, 1, 2, 'send_back', 'the first reopen of task 1', 2000),
			(?1, 3, 2, 'send_back', 'the first reopen of task 3', 1000),
			(?1, 3, 3, 'grant', 'a grant between the reopens', 1500),
			(?1, 3, 4, 'send_back', 'the second reopen of task 3', 3000)`,
		`INSERT INTO epic_task_reviews (epic, task, attempt, position, gate, step, verdict) VALUES
			(?1, 1, 1, 0, 'review', 'step-t1-review', 'holds'),
			(?1, 2, 1, 0, 'review', 'step-t2-review', 'fails'),
			(?1, 3, 1, 0, 'review', 'step-t3-review', 'holds')`,
		`INSERT INTO epic_task_findings (epic, task, attempt, review, position, id, claim, citation, location, evidence,
			verify_by) VALUES (?1, 2, 1, 0, 0, 'F1', 'the claim', 'R1', 'a.go:1', 'line 1', 'run it')`,
		`INSERT INTO epic_task_rungs (epic, task, rung, kind, moved_by, created) VALUES
			(?1, 1, 1, 'attempt', 'send_back', 2000),
			(?1, 2, 1, 'attempt', '', 900),
			(?1, 2, 2, 'attempt', 'unit', 950),
			(?1, 3, 4, 'approach', 'unit', 500),
			(?1, 3, 3, 'rescue', 'unit', 1200),
			(?1, 3, 1, 'attempt', 'send_back', 3000),
			(?1, 3, 2, 'attempt', 'fail_task', 3100)`,
		`INSERT INTO epic_task_rung_outcomes (epic, task, rung, outcome, ended) VALUES
			(?1, 2, 1, 'failed', 950),
			(?1, 3, 4, 'failed', 999),
			(?1, 3, 3, 'failed', 1250),
			(?1, 3, 1, 'failed', 3100)`,
	} {
		if _, err := s.db.ExecContext(ctx, insert, seededEpic); err != nil {
			t.Fatalf("seed at version 86: %v\n%s", err, insert)
		}
	}

	if _, err := provider.UpByOne(ctx); err != nil {
		t.Fatalf("Up of 00087: %v", err)
	}
	for _, check := range []struct {
		name, query string
		want        []string
	}{
		{"task generations", `SELECT seq || ':' || generation FROM epic_tasks WHERE epic = ? ORDER BY seq`,
			[]string{"1:1", "2:0", "3:2"}},
		{"review generations", `SELECT task || ':' || generation || ':' || step FROM epic_task_reviews
			WHERE epic = ? ORDER BY task, generation`,
			[]string{"1:0:step-t1-review", "2:0:step-t2-review", "3:0:step-t3-review"}},
		{"finding generations", `SELECT task || ':' || generation || ':' || id FROM epic_task_findings
			WHERE epic = ? ORDER BY task, generation`, []string{"2:0:F1"}},
		{"rung generations", `SELECT task || ':' || generation || ':' || rung FROM epic_task_rungs
			WHERE epic = ? ORDER BY task, generation, rung`,
			[]string{"1:1:1", "2:0:1", "2:0:2", "3:0:4", "3:1:3", "3:2:1", "3:2:2"}},
		{"outcome generations", `SELECT task || ':' || generation || ':' || rung FROM epic_task_rung_outcomes
			WHERE epic = ? ORDER BY task, generation, rung`,
			[]string{"2:0:1", "3:0:4", "3:1:3", "3:2:1"}},
	} {
		if got := sendBackHistoryKeys(t, s, check.query); !reflect.DeepEqual(got, check.want) {
			t.Errorf("%s after Up = %q, want %q", check.name, got, check.want)
		}
	}

	if err := s.fixtureQueries().InsertEpicTaskReview(ctx, sqlcgen.InsertEpicTaskReviewParams{
		Epic: seededEpic, Task: 1, Attempt: 1, Position: 0, Gate: "review", Step: "step-t1-reopened",
		Verdict: "holds",
	}); err != nil {
		t.Fatalf("insert the reopened review of task 1 at attempt 1 position 0: %v", err)
	}
	reviewsQuery := `SELECT task || ':' || generation || ':' || step FROM epic_task_reviews
		WHERE epic = ? ORDER BY task, generation`
	want := []string{"1:0:step-t1-review", "1:1:step-t1-reopened", "2:0:step-t2-review", "3:0:step-t3-review"}
	if got := sendBackHistoryKeys(t, s, reviewsQuery); !reflect.DeepEqual(got, want) {
		t.Errorf("reviews after the reopened review = %q, want %q", got, want)
	}

	if _, err := provider.Down(ctx); err != nil {
		t.Fatalf("Down of 00087: %v", err)
	}
	for _, check := range []struct {
		name, query string
		want        []string
	}{
		{"reviews", `SELECT task || ':' || step FROM epic_task_reviews WHERE epic = ? ORDER BY task`,
			[]string{"1:step-t1-reopened", "2:step-t2-review"}},
		{"findings", `SELECT task || ':' || id FROM epic_task_findings WHERE epic = ? ORDER BY task`,
			[]string{"2:F1"}},
		{"rungs", `SELECT task || ':' || rung FROM epic_task_rungs WHERE epic = ? ORDER BY task, rung`,
			[]string{"1:1", "2:1", "2:2", "3:1", "3:2"}},
		{"outcomes", `SELECT task || ':' || rung FROM epic_task_rung_outcomes WHERE epic = ? ORDER BY task, rung`,
			[]string{"2:1", "3:1"}},
	} {
		if got := sendBackHistoryKeys(t, s, check.query); !reflect.DeepEqual(got, check.want) {
			t.Errorf("%s after Down = %q, want %q", check.name, got, check.want)
		}
	}
}
