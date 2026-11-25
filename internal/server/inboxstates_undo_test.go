package server

import (
	"maps"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/store"
)

// Session ids of the unarchive tests, with invented values: a pane and a headless session for each case. The cases
// share one store, and each has ids of its own. The headless ids are 32 hex digits each.
const (
	undoEndedPane  = "orrery-undo-ended"
	undoEndedHead  = "3a3a3a3a3a3a3a3a3a3a3a3a3a3a3a3a"
	undoMarkedPane = "orrery-undo-marked"
	undoMarkedHead = "3b3b3b3b3b3b3b3b3b3b3b3b3b3b3b3b"
	undoAgedPane   = "orrery-undo-aged"
	undoAgedHead   = "3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c"
)

// seedUndoCases writes the rows of the cases of TestInboxStatesUndo into the store at path, at closureStart. The
// headless sessions are added in one write, and each pane is synced as done. The ended case's pane and headless session
// are closed for their change time.
func seedUndoCases(t *testing.T, path string) {
	t.Helper()
	at := closureStart()
	applyHeadlessAt(t, path, at,
		store.EngineAdd{ID: undoEndedHead, Dir: "/work/undo", State: "idle"},
		store.EngineAdd{ID: undoMarkedHead, Dir: "/work/undo", State: "idle"},
		store.EngineAdd{ID: undoAgedHead, Dir: "/work/undo", State: "stopped"},
	)
	for _, pane := range []string{undoEndedPane, undoMarkedPane, undoAgedPane} {
		syncEndedPane(t, path, pane, "done", at)
	}
	setClosureVerdict(t, path, closureVerdictOf(undoEndedHead, at, true), at)
	setClosureVerdict(t, path, closureVerdictOf(undoEndedPane, at, true), at)
}

// expectUndoListed lists the pane and the headless session of one case through sessions, on the clock they were built
// with, and fails the test unless they give the ended states wantEnded and the archived flags wantArchived, by id. Each
// map names both rows. The rows of the other cases are not this case's.
func expectUndoListed(
	t *testing.T, sessions orreryv1connect.SessionsClient, step, pane, headless string,
	wantEnded map[string]endedRowState, wantArchived map[string]bool,
) {
	t.Helper()
	panes := listSessionRows(t, sessions)
	headlessRows := listHeadlessRows(t, sessions)
	ended := endedStates(panes)
	maps.Copy(ended, endedStates(headlessRows))
	archived := archivedByID(panes)
	maps.Copy(archived, archivedByID(headlessRows))
	expectEnded(t, step, pickStates(ended, pane, headless), wantEnded)
	expectArchived(t, step, pickStates(archived, pane, headless), wantArchived)
}

// TestInboxStatesUndo holds the cases of Unarchive: an ended or archived row returns to the tree until it changes. Each
// case moves its own rows in order, so the cases run one after another on the shared store.
func TestInboxStatesUndo(t *testing.T) {
	path := archiveStorePath(t)
	seedUndoCases(t, path)

	t.Run("EndedRowListsUnendedUntilChange", func(t *testing.T) {
		clock := &closureClock{now: closureStart()}
		endedAt := clock.now
		sessions := sessionsClientAt(t, store.NewReader(path), clock.Now)
		ended := map[string]endedRowState{
			undoEndedPane: {ended: true, endedAt: endedAt.Unix()},
			undoEndedHead: {ended: true, endedAt: endedAt.Unix()},
		}
		unarchivedAt := endedAt.Add(time.Hour)
		clock.now = unarchivedAt
		expectUndoListed(t, sessions, "an hour after it ended, before the unarchive", undoEndedPane, undoEndedHead,
			ended, map[string]bool{undoEndedPane: false, undoEndedHead: false})

		unarchiveRowAt(t, path, archiveKindSession, undoEndedPane, unarchivedAt)
		unarchiveRowAt(t, path, archiveKindHeadless, undoEndedHead, unarchivedAt)
		clock.now = endedAt.Add(25 * time.Hour)
		unended := map[string]endedRowState{undoEndedPane: {}, undoEndedHead: {}}
		expectUndoListed(t, sessions, "25 hours after it ended, unarchived", undoEndedPane, undoEndedHead,
			unended, map[string]bool{undoEndedPane: false, undoEndedHead: false})

		changedAt := endedAt.Add(26 * time.Hour)
		applyHeadlessAt(t, path, changedAt, store.EngineSet{
			ID: undoEndedHead, Changes: store.EngineChanges{State: new("working")},
		})
		syncEndedPane(t, path, undoEndedPane, "working", changedAt)
		setClosureVerdict(t, path, closureVerdictOf(undoEndedHead, changedAt, true), changedAt)
		setClosureVerdict(t, path, closureVerdictOf(undoEndedPane, changedAt, true), changedAt)
		relisted := map[string]endedRowState{
			undoEndedPane: {ended: true, endedAt: changedAt.Unix()},
			undoEndedHead: {ended: true, endedAt: changedAt.Unix()},
		}

		clock.now = changedAt.Add(archiveDay - time.Hour)
		expectUndoListed(t, sessions, "23 hours after the change, closed for it", undoEndedPane, undoEndedHead,
			relisted, map[string]bool{undoEndedPane: false, undoEndedHead: false})

		clock.now = changedAt.Add(archiveDay)
		expectUndoListed(t, sessions, "a day after the change, closed for it", undoEndedPane, undoEndedHead,
			relisted, map[string]bool{undoEndedPane: true, undoEndedHead: true})
	})

	t.Run("MarkedRowListsUnarchivedUntilChange", func(t *testing.T) {
		clock := &closureClock{now: closureStart()}
		changedAt := clock.now
		sessions := sessionsClientAt(t, store.NewReader(path), clock.Now)
		unended := map[string]endedRowState{undoMarkedPane: {}, undoMarkedHead: {}}

		archivedAt := changedAt.Add(time.Hour)
		archiveRowAt(t, path, archiveKindHeadless, undoMarkedHead, archivedAt)
		archiveRowAt(t, path, archiveKindSession, undoMarkedPane, archivedAt)
		clock.now = archivedAt
		expectUndoListed(t, sessions, "archived by mark", undoMarkedPane, undoMarkedHead, unended,
			map[string]bool{undoMarkedPane: true, undoMarkedHead: true})

		unarchivedAt := archivedAt.Add(time.Hour)
		unarchiveRowAt(t, path, archiveKindHeadless, undoMarkedHead, unarchivedAt)
		unarchiveRowAt(t, path, archiveKindSession, undoMarkedPane, unarchivedAt)
		clock.now = changedAt.Add(7 * archiveDay)
		expectUndoListed(t, sessions, "a week on, unarchived and unchanged", undoMarkedPane, undoMarkedHead, unended,
			map[string]bool{undoMarkedPane: false, undoMarkedHead: false})

		changedAgainAt := changedAt.Add(8 * archiveDay)
		applyHeadlessAt(t, path, changedAgainAt, store.EngineSet{
			ID: undoMarkedHead, Changes: store.EngineChanges{State: new("working")},
		})
		syncEndedPane(t, path, undoMarkedPane, "working", changedAgainAt)
		clock.now = changedAgainAt
		expectUndoListed(t, sessions, "at the next change", undoMarkedPane, undoMarkedHead, unended,
			map[string]bool{undoMarkedPane: false, undoMarkedHead: false})

		clock.now = changedAgainAt.Add(archiveDay*7 - time.Second)
		expectUndoListed(t, sessions, "a week less a second after the change", undoMarkedPane, undoMarkedHead, unended,
			map[string]bool{undoMarkedPane: false, undoMarkedHead: false})

		clock.now = changedAgainAt.Add(archiveDay * 7)
		expectUndoListed(t, sessions, "a week after the change", undoMarkedPane, undoMarkedHead, unended,
			map[string]bool{undoMarkedPane: true, undoMarkedHead: true})
	})

	t.Run("AgedRowListsUnarchivedUntilChange", func(t *testing.T) {
		clock := &closureClock{now: closureStart()}
		changedAt := clock.now
		sessions := sessionsClientAt(t, store.NewReader(path), clock.Now)

		clock.now = changedAt.Add(archiveDay)
		expectUndoListed(t, sessions, "a day after it ended, aged", undoAgedPane, undoAgedHead,
			map[string]endedRowState{
				undoAgedPane: {},
				undoAgedHead: {ended: true, endedAt: changedAt.Unix()},
			},
			map[string]bool{undoAgedPane: false, undoAgedHead: true})

		unarchivedAt := changedAt.Add(archiveDay + time.Hour)
		unarchiveRowAt(t, path, archiveKindHeadless, undoAgedHead, unarchivedAt)
		unarchiveRowAt(t, path, archiveKindSession, undoAgedPane, unarchivedAt)
		unended := map[string]endedRowState{undoAgedPane: {}, undoAgedHead: {}}
		clock.now = unarchivedAt
		expectUndoListed(t, sessions, "an hour after the unarchive", undoAgedPane, undoAgedHead, unended,
			map[string]bool{undoAgedPane: false, undoAgedHead: false})

		clock.now = changedAt.Add(7 * archiveDay)
		expectUndoListed(t, sessions, "a week on, unarchived and unchanged", undoAgedPane, undoAgedHead, unended,
			map[string]bool{undoAgedPane: false, undoAgedHead: false})

		changedAgainAt := changedAt.Add(8 * archiveDay)
		applyHeadlessAt(t, path, changedAgainAt, store.EngineSet{
			ID: undoAgedHead, Changes: store.EngineChanges{State: new("working")},
		})
		syncEndedPane(t, path, undoAgedPane, "working", changedAgainAt)
		clock.now = changedAgainAt
		expectUndoListed(t, sessions, "at the next change", undoAgedPane, undoAgedHead, unended,
			map[string]bool{undoAgedPane: false, undoAgedHead: false})

		clock.now = changedAgainAt.Add(archiveDay*7 - time.Second)
		expectUndoListed(t, sessions, "a week less a second after the change", undoAgedPane, undoAgedHead, unended,
			map[string]bool{undoAgedPane: false, undoAgedHead: false})

		clock.now = changedAgainAt.Add(archiveDay * 7)
		expectUndoListed(t, sessions, "a week after the change", undoAgedPane, undoAgedHead, unended,
			map[string]bool{undoAgedPane: true, undoAgedHead: true})
	})
}
