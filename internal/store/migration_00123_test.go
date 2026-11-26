package store

import (
	"context"
	"path/filepath"
	"testing"
)

// This file checks migration 00123_drop_undo_none.sql, which drops planner_actions.undo_none and moves each epic parked
// release_undo_none to release_step. A store at version 122 holds an epic parked release_undo_none, an epic parked
// release_step that the migration leaves as it is, and a pending planner action that names an undo-none position. Open
// migrates it to the newest version: the column is gone, the action reads back with its other fields, and the epic
// parked release_undo_none reads release_step while the other keeps its park class.

func TestMigration00123DropsUndoNone(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")

	const (
		undoEpic = "orrery-0123456/parked-undo-none"
		stepEpic = "orrery-0123456/parked-release-step"
	)
	fixture := openStoreAtVersion(t, path, 122)
	for _, seed := range []struct{ id, parkClass string }{
		{id: undoEpic, parkClass: "release_undo_none"},
		{id: stepEpic, parkClass: "release_step"},
	} {
		if _, err := fixture.ExecContext(ctx, `INSERT INTO epics
			(id, repository, title, approved, intent, care, approach, release_then, checks, phase, created, changed,
			phase_at, parked, park_class)
			VALUES (?, '/work/repo', 'Epic', 'approved line', 'intent', 'serious', 'approach', 'then', 'checks', 'stopped',
			1700000000, 1700000000, 1700000400, 'publish rejected', ?)`, seed.id, seed.parkClass); err != nil {
			fixture.Close()
			t.Fatalf("seed epic %s: %v", seed.id, err)
		}
	}
	if _, err := fixture.ExecContext(ctx, `INSERT INTO planner_actions
		(epic, turn, position, kind, node, task, close_kind, by_epic, undo_none, reason, state)
		VALUES (?, 1, 1, 'resume_release', '', 0, '', '', '[2]', 'the publish step must run again', 'pending')`,
		undoEpic); err != nil {
		fixture.Close()
		t.Fatalf("seed planner action: %v", err)
	}
	if err := fixture.Close(); err != nil {
		t.Fatalf("close fixture: %v", err)
	}

	s, err := Open(ctx, path, noSync())
	if err != nil {
		t.Fatalf("Open at version 122: %v", err)
	}
	defer s.Close()

	const countUndoNone = `SELECT COUNT(*) FROM pragma_table_info('planner_actions') WHERE name = 'undo_none'`
	var columns int
	if err = s.db.QueryRowContext(ctx, countUndoNone).Scan(&columns); err != nil {
		t.Fatalf("read columns of planner_actions: %v", err)
	}
	if columns != 0 {
		t.Errorf("undo_none columns of planner_actions after Open = %d, want none", columns)
	}

	actions, err := s.PendingActions(ctx)
	if err != nil {
		t.Fatalf("PendingActions: %v", err)
	}
	if len(actions) != 1 || actions[0].Kind != ActionResumeRelease || actions[0].Epic != undoEpic ||
		actions[0].Reason != "the publish step must run again" {
		t.Errorf("pending actions after Open = %+v, want the one resume_release of %s", actions, undoEpic)
	}

	for _, want := range []struct{ id, parkClass string }{
		{id: undoEpic, parkClass: "release_step"},
		{id: stepEpic, parkClass: "release_step"},
	} {
		got, err := s.Epic(ctx, want.id)
		if err != nil {
			t.Fatalf("Epic %s: %v", want.id, err)
		}
		if string(got.ParkClass) != want.parkClass {
			t.Errorf("park class of epic %s after Open = %q, want %q", want.id, got.ParkClass, want.parkClass)
		}
	}
}
