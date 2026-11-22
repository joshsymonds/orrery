package store

import (
	"context"
	"errors"
	"testing"
)

// TestStepPurposeStoresWithTheStepAndListsIt checks that a step added with a purpose reads it back from its row and
// from its listing, and that a step added with none reads an empty purpose.
func TestStepPurposeStoresWithTheStepAndListsIt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openSyncStore(t)
	purposed := stepAddOf("s1")
	purposed.Epic = "repo/epic"
	purposed.Purpose = "Review the diff of T3."
	bare := stepAddOf("s2")
	bare.Epic = "repo/epic"
	if err := s.ApplyEngine(ctx, []EngineOp{purposed, bare}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}

	for id, want := range map[string]string{"s1": "Review the diff of T3.", "s2": ""} {
		row, err := s.Step(ctx, id)
		if err != nil {
			t.Fatalf("Step %s: %v", id, err)
		}
		if row.Purpose != want {
			t.Errorf("step %s row purpose = %q, want %q", id, row.Purpose, want)
		}
	}

	listings, err := s.StepListings(ctx, "repo/epic", "")
	if err != nil {
		t.Fatalf("StepListings: %v", err)
	}
	got := map[string]string{}
	for _, listing := range listings {
		got[listing.ID] = listing.Purpose
	}
	if got["s1"] != "Review the diff of T3." || got["s2"] != "" || len(got) != 2 {
		t.Errorf("listing purposes = %v, want s1 with its purpose and s2 with none", got)
	}
}

// TestStepPurposeWritesOnlyWhileEmpty checks that SetStepPurpose stores a purpose while the stored one is empty, and
// that a second write leaves the first purpose as it is.
func TestStepPurposeWritesOnlyWhileEmpty(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openSyncStore(t)
	if err := s.ApplyEngine(ctx, []EngineOp{stepAddOf("s1")}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}
	if err := s.SetStepPurpose(ctx, "s1", "Review the diff."); err != nil {
		t.Fatalf("SetStepPurpose first: %v", err)
	}
	if err := s.SetStepPurpose(ctx, "s1", "Something else."); err != nil {
		t.Fatalf("SetStepPurpose second: %v", err)
	}
	row, err := s.Step(ctx, "s1")
	if err != nil {
		t.Fatalf("Step: %v", err)
	}
	if row.Purpose != "Review the diff." {
		t.Errorf("purpose = %q, want the first write %q", row.Purpose, "Review the diff.")
	}
}

// TestStepPurposeMigrationDownAndUp checks the migration that adds the purpose column. Its Down drops the column, and
// step_listings reads on without it, and its Up adds the column back empty, so a purpose does not survive a Down.
func TestStepPurposeMigrationDownAndUp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, provider := storeAtVersion(t, 95)
	purposed := stepAddOf("s1")
	purposed.Purpose = "Review the diff of T3."
	if err := s.ApplyEngine(ctx, []EngineOp{purposed}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}

	if _, err := provider.Down(ctx); err != nil {
		t.Fatalf("Down of 00095: %v", err)
	}
	if stepPurposeColumn(t, s, "steps") || stepPurposeColumn(t, s, "step_listings") {
		t.Errorf("purpose column after Down: present in steps or step_listings, want absent from both")
	}
	var listed int
	if err := s.db.DB.QueryRow(`SELECT COUNT(*) FROM step_listings`).Scan(&listed); err != nil || listed != 1 {
		t.Fatalf("step_listings after Down counts %d (err %v), want the one step", listed, err)
	}

	if _, err := provider.UpByOne(ctx); err != nil {
		t.Fatalf("Up of 00095: %v", err)
	}
	if !stepPurposeColumn(t, s, "steps") || !stepPurposeColumn(t, s, "step_listings") {
		t.Fatalf("purpose column after Up: absent from steps or step_listings, want present in both")
	}
	var purpose string
	if err := s.db.DB.QueryRow(`SELECT purpose FROM step_listings WHERE id = 's1'`).Scan(&purpose); err != nil {
		t.Fatalf("read purpose after Up: %v", err)
	}
	if purpose != "" {
		t.Errorf("purpose after Down and Up = %q, want empty", purpose)
	}
}

// stepPurposeColumn reports whether table has a purpose column.
func stepPurposeColumn(t *testing.T, s *Store, table string) bool {
	t.Helper()
	rows, err := s.db.DB.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		t.Fatalf("list columns of %s: %v", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan column of %s: %v", table, err)
		}
		if name == "purpose" {
			return true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate columns of %s: %v", table, err)
	}
	return false
}

// TestStepPurposeUnknownStepIsErrNoStep checks that a purpose for a step no row holds is refused with ErrNoStep.
func TestStepPurposeUnknownStepIsErrNoStep(t *testing.T) {
	t.Parallel()
	s := openSyncStore(t)
	err := s.SetStepPurpose(context.Background(), "missing", "Review the diff.")
	if !errors.Is(err, ErrNoStep) {
		t.Fatalf("SetStepPurpose on an unknown step = %v, want ErrNoStep", err)
	}
}
