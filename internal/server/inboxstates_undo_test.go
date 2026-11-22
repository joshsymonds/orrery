package server

import (
	"maps"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// Session ids of the unarchive tests, with invented values: a headless session and a pane.
const (
	undoHeadlessID = "3a3a3a3a3a3a3a3a3a3a3a3a3a3a3a3a"
	undoPaneID     = "orrery-undo"
)

// expectUndoListed lists the pane rows and the headless rows of the store at path on the clock now, and fails the test
// unless they give the ended states wantEnded and the archived flags wantArchived, by id. Each map names every row.
func expectUndoListed(
	t *testing.T, path, step string, now func() time.Time,
	wantEnded map[string]endedRowState, wantArchived map[string]bool,
) {
	t.Helper()
	sessions := sessionsClientAt(t, store.NewReader(path), now)
	panes := listSessionRows(t, sessions)
	headless := listHeadlessRows(t, sessions)
	ended := endedStates(panes)
	maps.Copy(ended, endedStates(headless))
	archived := archivedByID(panes)
	maps.Copy(archived, archivedByID(headless))
	expectEnded(t, step, ended, wantEnded)
	expectArchived(t, step, archived, wantArchived)
}

// endUndoRows ends the pane and the headless session of the ended case at at, by a closed verdict for their change
// time at, in the store at path.
func endUndoRows(t *testing.T, path string, at time.Time) {
	t.Helper()
	applyHeadlessAt(t, path, at, store.EngineAdd{ID: undoHeadlessID, Dir: "/work/undo", State: "idle"})
	syncEndedPane(t, path, undoPaneID, "done", at)
	setClosureVerdict(t, path, closureVerdictOf(undoHeadlessID, at, true), at)
	setClosureVerdict(t, path, closureVerdictOf(undoPaneID, at, true), at)
}

// TestInboxStatesUndoEndedRowListsUnendedUntilChange ends a pane and a headless session by closed verdicts at the fixed
// clock, and unarchives both an hour on. Both list unended and unarchived 25 hours after they ended. A change after the
// unarchive, with a closed verdict for it, ends both again at that change, and each archives a day on.
func TestInboxStatesUndoEndedRowListsUnendedUntilChange(t *testing.T) {
	t.Parallel()
	path := archiveStorePath(t)
	clock := &closureClock{now: closureStart()}
	endedAt := clock.now
	endUndoRows(t, path, endedAt)

	ended := map[string]endedRowState{
		undoPaneID:     {ended: true, endedAt: endedAt.Unix()},
		undoHeadlessID: {ended: true, endedAt: endedAt.Unix()},
	}
	unarchivedAt := endedAt.Add(time.Hour)
	clock.now = unarchivedAt
	expectUndoListed(t, path, "an hour after it ended, before the unarchive", clock.Now, ended,
		map[string]bool{undoPaneID: false, undoHeadlessID: false})

	unarchiveRowAt(t, path, archiveKindSession, undoPaneID, unarchivedAt)
	unarchiveRowAt(t, path, archiveKindHeadless, undoHeadlessID, unarchivedAt)
	clock.now = endedAt.Add(25 * time.Hour)
	expectUndoListed(t, path, "25 hours after it ended, unarchived", clock.Now,
		map[string]endedRowState{undoPaneID: {}, undoHeadlessID: {}},
		map[string]bool{undoPaneID: false, undoHeadlessID: false})

	changedAt := endedAt.Add(26 * time.Hour)
	applyHeadlessAt(t, path, changedAt, store.EngineSet{
		ID: undoHeadlessID, Changes: store.EngineChanges{State: new("working")},
	})
	syncEndedPane(t, path, undoPaneID, "working", changedAt)
	setClosureVerdict(t, path, closureVerdictOf(undoHeadlessID, changedAt, true), changedAt)
	setClosureVerdict(t, path, closureVerdictOf(undoPaneID, changedAt, true), changedAt)
	relisted := map[string]endedRowState{
		undoPaneID:     {ended: true, endedAt: changedAt.Unix()},
		undoHeadlessID: {ended: true, endedAt: changedAt.Unix()},
	}

	clock.now = changedAt.Add(archiveDay - time.Hour)
	expectUndoListed(t, path, "23 hours after the change, closed for it", clock.Now, relisted,
		map[string]bool{undoPaneID: false, undoHeadlessID: false})

	clock.now = changedAt.Add(archiveDay)
	expectUndoListed(t, path, "a day after the change, closed for it", clock.Now, relisted,
		map[string]bool{undoPaneID: true, undoHeadlessID: true})
}

// TestInboxStatesUndoMarkedRowListsUnarchivedUntilChange archives a pane and a headless session by mark an hour after
// they changed at the fixed clock. Unarchiving both lists them unarchived, past the seven-day floor too, until they
// change. A change after the unarchive returns them to the ordinary rules, so they stay listed until the floor ages
// them a week on.
func TestInboxStatesUndoMarkedRowListsUnarchivedUntilChange(t *testing.T) {
	t.Parallel()
	path := archiveStorePath(t)
	clock := &closureClock{now: closureStart()}
	changedAt := clock.now
	applyHeadlessAt(t, path, changedAt, store.EngineAdd{ID: undoHeadlessID, Dir: "/work/undo", State: "idle"})
	syncEndedPane(t, path, undoPaneID, "done", changedAt)
	unended := map[string]endedRowState{undoPaneID: {}, undoHeadlessID: {}}

	archivedAt := changedAt.Add(time.Hour)
	archiveRowAt(t, path, archiveKindHeadless, undoHeadlessID, archivedAt)
	archiveRowAt(t, path, archiveKindSession, undoPaneID, archivedAt)
	clock.now = archivedAt
	expectUndoListed(t, path, "archived by mark", clock.Now, unended,
		map[string]bool{undoPaneID: true, undoHeadlessID: true})

	unarchivedAt := archivedAt.Add(time.Hour)
	unarchiveRowAt(t, path, archiveKindHeadless, undoHeadlessID, unarchivedAt)
	unarchiveRowAt(t, path, archiveKindSession, undoPaneID, unarchivedAt)
	clock.now = changedAt.Add(7 * archiveDay)
	expectUndoListed(t, path, "a week on, unarchived and unchanged", clock.Now, unended,
		map[string]bool{undoPaneID: false, undoHeadlessID: false})

	changedAgainAt := changedAt.Add(8 * archiveDay)
	applyHeadlessAt(t, path, changedAgainAt, store.EngineSet{
		ID: undoHeadlessID, Changes: store.EngineChanges{State: new("working")},
	})
	syncEndedPane(t, path, undoPaneID, "working", changedAgainAt)
	clock.now = changedAgainAt
	expectUndoListed(t, path, "at the next change", clock.Now, unended,
		map[string]bool{undoPaneID: false, undoHeadlessID: false})

	clock.now = changedAgainAt.Add(archiveDay*7 - time.Second)
	expectUndoListed(t, path, "a week less a second after the change", clock.Now, unended,
		map[string]bool{undoPaneID: false, undoHeadlessID: false})

	clock.now = changedAgainAt.Add(archiveDay * 7)
	expectUndoListed(t, path, "a week after the change", clock.Now, unended,
		map[string]bool{undoPaneID: true, undoHeadlessID: true})
}

// TestInboxStatesUndoAgedRowListsUnarchivedUntilChange lets a stopped headless session age by its ended day, and a done
// pane age by the seven-day floor. Unarchiving both lists them unarchived, unended and past the floor, until they
// change. A change after the unarchive returns them to the ordinary rules: the session is no longer stopped, so only
// the floor ages it.
func TestInboxStatesUndoAgedRowListsUnarchivedUntilChange(t *testing.T) {
	t.Parallel()
	path := archiveStorePath(t)
	clock := &closureClock{now: closureStart()}
	changedAt := clock.now
	applyHeadlessAt(t, path, changedAt, store.EngineAdd{ID: undoHeadlessID, Dir: "/work/undo", State: "stopped"})
	syncEndedPane(t, path, undoPaneID, "done", changedAt)

	clock.now = changedAt.Add(archiveDay)
	expectUndoListed(t, path, "a day after it ended, aged", clock.Now,
		map[string]endedRowState{
			undoPaneID:     {},
			undoHeadlessID: {ended: true, endedAt: changedAt.Unix()},
		},
		map[string]bool{undoPaneID: false, undoHeadlessID: true})

	unarchivedAt := changedAt.Add(archiveDay + time.Hour)
	unarchiveRowAt(t, path, archiveKindHeadless, undoHeadlessID, unarchivedAt)
	unarchiveRowAt(t, path, archiveKindSession, undoPaneID, unarchivedAt)
	unended := map[string]endedRowState{undoPaneID: {}, undoHeadlessID: {}}
	clock.now = unarchivedAt
	expectUndoListed(t, path, "an hour after the unarchive", clock.Now, unended,
		map[string]bool{undoPaneID: false, undoHeadlessID: false})

	clock.now = changedAt.Add(7 * archiveDay)
	expectUndoListed(t, path, "a week on, unarchived and unchanged", clock.Now, unended,
		map[string]bool{undoPaneID: false, undoHeadlessID: false})

	changedAgainAt := changedAt.Add(8 * archiveDay)
	applyHeadlessAt(t, path, changedAgainAt, store.EngineSet{
		ID: undoHeadlessID, Changes: store.EngineChanges{State: new("working")},
	})
	syncEndedPane(t, path, undoPaneID, "working", changedAgainAt)
	clock.now = changedAgainAt
	expectUndoListed(t, path, "at the next change", clock.Now, unended,
		map[string]bool{undoPaneID: false, undoHeadlessID: false})

	clock.now = changedAgainAt.Add(archiveDay*7 - time.Second)
	expectUndoListed(t, path, "a week less a second after the change", clock.Now, unended,
		map[string]bool{undoPaneID: false, undoHeadlessID: false})

	clock.now = changedAgainAt.Add(archiveDay * 7)
	expectUndoListed(t, path, "a week after the change", clock.Now, unended,
		map[string]bool{undoPaneID: true, undoHeadlessID: true})
}
