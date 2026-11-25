package store

import (
	"context"
	"testing"
)

// epicSummaryClearOther is a second epic the clear test stores, with a summary, beside seededEpic.
const epicSummaryClearOther = "orrery-0123456/epic-two"

// TestEpicSummaryClearedByTheRewriteMigration stages the store at version 113, stores a summary on each of two epics,
// and steps it up to the rewrite migration at 00114. It shows that the migration leaves every stored summary empty, and
// that SetEpicSummary then writes a summary only while the summary is empty: the first write stays and a second is
// refused.
func TestEpicSummaryClearedByTheRewriteMigration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, provider := storeAtVersion(t, 113)
	seedEpic(t, s.db.DB)
	if _, err := s.fixtureExec(ctx, `INSERT INTO epics
		(id, repository, title, approved, intent, care, approach, release_then, checks, phase, created, changed, summary)
		VALUES (?, '/work/repo', 'Epic two', 'approved line', 'intent', 'serious', 'approach', 'then', 'checks',
		'accepted', 1700000000, 1700000000, 'Second summary before the rewrite.')`, epicSummaryClearOther); err != nil {
		t.Fatalf("insert second epic: %v", err)
	}
	if _, err := s.fixtureExec(ctx, `UPDATE epics SET summary = ? WHERE id = ?`,
		"Summary before the rewrite.", seededEpic); err != nil {
		t.Fatalf("set summary before the rewrite: %v", err)
	}

	if _, err := provider.UpByOne(ctx); err != nil {
		t.Fatalf("Up of 00114: %v", err)
	}
	var stored int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM epics WHERE summary <> ''`).Scan(&stored); err != nil {
		t.Fatalf("count summaries after the Up: %v", err)
	}
	if stored != 0 {
		t.Fatalf("%d epics keep a summary after the Up, want none", stored)
	}
	got, err := s.Epic(ctx, seededEpic)
	if err != nil {
		t.Fatalf("Epic after the Up: %v", err)
	}
	if got.Summary != "" {
		t.Fatalf("summary after the Up = %q, want empty", got.Summary)
	}

	if err := s.SetEpicSummary(ctx, seededEpic, "Summary after the rewrite."); err != nil {
		t.Fatalf("SetEpicSummary on the cleared epic: %v", err)
	}
	if err := s.SetEpicSummary(ctx, seededEpic, "Second summary after the rewrite."); err != nil {
		t.Fatalf("SetEpicSummary again: %v", err)
	}
	got, err = s.Epic(ctx, seededEpic)
	if err != nil {
		t.Fatalf("Epic after SetEpicSummary: %v", err)
	}
	if got.Summary != "Summary after the rewrite." {
		t.Fatalf("summary after two SetEpicSummary calls = %q, want the first", got.Summary)
	}
}
