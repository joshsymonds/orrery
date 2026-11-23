package store

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// This file checks migration 00097_drop_release_slots.sql, which drops epic_release_slots and changes no other table. A
// store at version 95 holds epics in each release phase, each with a release run, and the slot rows of an epic and of
// an outside label. Open migrates it to the newest version: the table is gone, and every epic reads back with the phase,
// park and release run it had.

// dropSeed is an epic TestReleaseSlotsDropKeepsEpics seeds at version 95, with the phase, park and release run it reads
// back with after the migration.
type dropSeed struct {
	id        string
	phase     string
	phaseAt   int64
	parked    string
	parkClass ParkClass
	passed    bool
}

func TestReleaseSlotsDropKeepsEpics(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")

	seeds := []dropSeed{
		{id: "orrery-0123456/releasing", phase: PhaseReleasing, phaseAt: 1700000300},
		{
			id: "orrery-0123456/stopped", phase: PhaseStopped, phaseAt: 1700000400,
			parked: "stopped by its owner", parkClass: ParkClassReleaseOwnerStop,
		},
		{id: "orrery-0123456/clean", phase: PhaseClean, phaseAt: 1700000500, passed: true},
	}
	fixture := openStoreAtVersion(t, path, 95)
	for _, seed := range seeds {
		if _, err := fixture.ExecContext(ctx, `INSERT INTO epics
			(id, repository, title, approved, intent, care, approach, release_then, checks, phase, created, changed,
			phase_at, parked, park_class)
			VALUES (?, '/work/repo', 'Epic', 'approved line', 'intent', 'serious', 'approach', 'then', 'checks', ?,
			1700000000, 1700000000, ?, ?, ?)`,
			seed.id, seed.phase, seed.phaseAt, seed.parked, string(seed.parkClass)); err != nil {
			fixture.Close()
			t.Fatalf("seed epic %s: %v", seed.id, err)
		}
		if _, err := fixture.ExecContext(ctx, `INSERT INTO epic_release_runs
			(epic, part, position, attempt, step, start, head, finish, commits, evidence, passed, reason, at)
			VALUES (?, 'release', 1, 1, 'tag', 'start-sha', 'head-sha', 'finish-sha', '["abc1234"]', 'evidence line', ?,
			'reason line', 1700000600)`, seed.id, seed.passed); err != nil {
			fixture.Close()
			t.Fatalf("seed release run of %s: %v", seed.id, err)
		}
	}
	if _, err := fixture.ExecContext(ctx, `INSERT INTO epic_release_slots (repository, epic, holder_kind)
		VALUES ('/work/repo', ?, 'epic')`, seeds[0].id); err != nil {
		fixture.Close()
		t.Fatalf("seed epic slot row: %v", err)
	}
	if _, err := fixture.ExecContext(ctx, `INSERT INTO epic_release_slots (repository, epic, holder_kind, label, deadline)
		VALUES ('/work/outside', '', 'outside', 'nightly', 1700009000)`); err != nil {
		fixture.Close()
		t.Fatalf("seed outside slot row: %v", err)
	}
	if err := fixture.Close(); err != nil {
		t.Fatalf("close fixture: %v", err)
	}

	s, err := Open(ctx, path, noSync())
	if err != nil {
		t.Fatalf("Open at version 95: %v", err)
	}
	defer s.Close()

	if tableNames(t, s)["epic_release_slots"] {
		t.Errorf("epic_release_slots after Open = present, want dropped")
	}
	for _, seed := range seeds {
		got, err := s.Epic(ctx, seed.id)
		if err != nil {
			t.Fatalf("Epic %s: %v", seed.id, err)
		}
		if got.Phase != seed.phase {
			t.Errorf("phase of %s = %q, want %q", seed.id, got.Phase, seed.phase)
		}
		if got.Parked != seed.parked || got.ParkClass != seed.parkClass {
			t.Errorf("park of %s = %q %q, want %q %q", seed.id, got.Parked, got.ParkClass, seed.parked, seed.parkClass)
		}
		runs, err := readReleaseRuns(ctx, s.fixtureQueries(), seed.id)
		if err != nil {
			t.Fatalf("release runs of %s: %v", seed.id, err)
		}
		want := []ReleaseRun{{
			Part: "release", Position: 1, Attempt: 1, Step: "tag", Start: "start-sha", Head: "head-sha", End: "finish-sha",
			Commits: []string{"abc1234"}, Evidence: "evidence line", Passed: seed.passed, Reason: "reason line",
			At: time.Unix(1700000600, 0),
		}}
		if !reflect.DeepEqual(runs, want) {
			t.Errorf("release runs of %s = %+v, want %+v", seed.id, runs, want)
		}
	}
}
