package store

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
)

// TestSteererMigrationSteersEachEpicByItsCreator seeds at version 77 one epic whose creator mark names a session, and
// a later session of that epic that did not create it, and one ended epic with no creator mark. Migrating the store up
// steers the first epic by its creator, and leaves the ended epic unsteered. The creator's sessions row is not seeded,
// so the steerer names a session the store no longer holds, as the migration does not join sessions.
func TestSteererMigrationSteersEachEpicByItsCreator(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")

	const (
		creator  = "claude-creator"
		follower = "claude-follower"
		ended    = "orrery-0123456/epic-ended"
	)
	fixture := openStoreAtVersion(t, path, 77)
	seedEpic(t, fixture)
	if _, err := fixture.ExecContext(ctx, `INSERT INTO epic_sessions (epic, session, call, at, creator)
		VALUES (?, ?, 'epic_create', 1700000100, 1), (?, ?, 'epic_answer', 1700000200, 0)`,
		seededEpic, creator, seededEpic, follower); err != nil {
		fixture.Close()
		t.Fatalf("seed sessions: %v", err)
	}
	if _, err := fixture.ExecContext(ctx, `INSERT INTO epics
		(id, repository, title, approved, intent, care, approach, release_then, checks, phase, created, changed)
		VALUES (?, '/work/repo', 'Ended epic', 'approved line', 'intent', 'serious', 'approach', 'then', 'checks',
		'done', 1700000000, 1700000000)`, ended); err != nil {
		fixture.Close()
		t.Fatalf("seed ended epic: %v", err)
	}
	if err := fixture.Close(); err != nil {
		t.Fatalf("close fixture: %v", err)
	}

	s, err := Open(ctx, path, noSync())
	if err != nil {
		t.Fatalf("Open at version 77: %v", err)
	}
	defer s.Close()

	for _, tc := range []struct{ id, want string }{{seededEpic, creator}, {ended, ""}} {
		var got string
		if err := s.db.QueryRowContext(ctx, `SELECT steerer FROM epics WHERE id = ?`, tc.id).Scan(&got); err != nil {
			t.Fatalf("read steerer of %s: %v", tc.id, err)
		}
		if got != tc.want {
			t.Errorf("steerer of %s = %q, want %q", tc.id, got, tc.want)
		}
	}
}

// TestSteererMigrationDownAndUpLeavesSchemaUnchanged steps migration 00078 down from a store at version 78 and back up:
// the Down leaves the schema of version 77, and the Up restores the schema of version 78.
func TestSteererMigrationDownAndUpLeavesSchemaUnchanged(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	before := openStoreAtVersion(t, filepath.Join(t.TempDir(), "before.db"), 77)
	atVersion77 := schemaLines(t, before)
	closeDB(t, before)

	s, provider := storeAtVersion(t, 78)
	atVersion78 := schemaLines(t, s.db.DB)

	if _, err := provider.Down(ctx); err != nil {
		t.Fatalf("Down of 00078: %v", err)
	}
	if got := schemaLines(t, s.db.DB); !slices.Equal(got, atVersion77) {
		t.Fatalf("schema after the Down of 00078 differs from version 77:\n%s", lineDiff(got, atVersion77))
	}
	if _, err := provider.UpByOne(ctx); err != nil {
		t.Fatalf("Up of 00078: %v", err)
	}
	if got := schemaLines(t, s.db.DB); !slices.Equal(got, atVersion78) {
		t.Fatalf("schema after the Down and Up of 00078 differs from version 78:\n%s", lineDiff(got, atVersion78))
	}
}

// TestSteererSetStoresCreatedSteerer creates an epic with a steerer and reads it back: Store.Epic returns it.
func TestSteererSetStoresCreatedSteerer(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	const (
		id      = "orrery-1a2b3c4/epic-steered"
		steerer = "claude-steerer"
	)
	row := fullEpic(id)
	row.Steerer = steerer
	createEpic(t, s, row)

	got, err := s.Epic(ctx, id)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	if got.Steerer != steerer {
		t.Errorf("Steerer = %q, want %q", got.Steerer, steerer)
	}
}

// TestSteererSetReadsEmptyWhenCreatedWithoutSteerer creates an epic with no steerer and reads it back empty.
func TestSteererSetReadsEmptyWhenCreatedWithoutSteerer(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	const id = "orrery-1a2b3c4/epic-unsteered"
	createEpic(t, s, fullEpic(id))

	got, err := s.Epic(ctx, id)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	if got.Steerer != "" {
		t.Errorf("Steerer = %q, want empty", got.Steerer)
	}
}
