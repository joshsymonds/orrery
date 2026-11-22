package server

import (
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// Conversation ids of the no-timer tests, 32 hex digits each as session ids are. Each test writes its own store.
const (
	noTimerPaneSeenID       = "4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a"
	noTimerPaneUnseenID     = "4b4b4b4b4b4b4b4b4b4b4b4b4b4b4b4b"
	noTimerHeadlessSeenID   = "4c4c4c4c4c4c4c4c4c4c4c4c4c4c4c4c"
	noTimerHeadlessUnseenID = "4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d"
	noTimerFailedID         = "4e4e4e4e4e4e4e4e4e4e4e4e4e4e4e4e"
)

// writeFailedHeadless adds the headless session id to the store at path in state failed at changed, and sets its seen
// time to seen.
func writeFailedHeadless(t *testing.T, path, id string, changed, seen time.Time) {
	t.Helper()
	applyHeadlessAt(t, path, changed, store.EngineAdd{ID: id, Dir: "/work/" + id, State: "failed"})
	applyHeadlessAt(t, path, seen, store.EngineSet{ID: id, Changes: store.EngineChanges{
		Seen: optionalString(stamp(seen)),
	}})
}

// expectNoTimerLists fails the test unless Sessions.List, read at the fixed clock, archives exactly the pane rows in
// wantPane, and ListHeadless archives exactly the headless rows in wantHeadless, of the store at path.
func expectNoTimerLists(t *testing.T, path string, wantPane, wantHeadless map[string]bool) {
	t.Helper()
	sessions := sessionsClientAt(t, store.NewReader(path), epicsNow)
	expectArchived(t, "Sessions.List", archivedByID(listSessionRows(t, sessions)), wantPane)
	expectArchived(t, "ListHeadless", archivedByID(listHeadlessRows(t, sessions)), wantHeadless)
}

// TestInboxStatesNoTimerDoneSixDaysTwentyThreeHoursStaysUnarchived writes a done pane and a done headless session, each
// seen at its change and unseen, unchanged 6 days 23 hours. Neither is archived: no timer ages a done row short of the
// seven-day floor.
func TestInboxStatesNoTimerDoneSixDaysTwentyThreeHoursStaysUnarchived(t *testing.T) {
	t.Parallel()
	path := archiveStorePath(t)
	changed := epicsNow().Add(-staleShortAge)
	writeDonePane(t, path, noTimerPaneSeenID, changed, changed)
	writeDonePane(t, path, noTimerPaneUnseenID, changed, time.Time{})
	writeDoneHeadless(t, path, noTimerHeadlessSeenID, changed, changed)
	writeDoneHeadless(t, path, noTimerHeadlessUnseenID, changed, time.Time{})
	expectNoTimerLists(t, path,
		map[string]bool{noTimerPaneSeenID: false, noTimerPaneUnseenID: false},
		map[string]bool{noTimerHeadlessSeenID: false, noTimerHeadlessUnseenID: false})
}

// TestInboxStatesNoTimerFailedSeenTwoDaysStaysUnarchived writes a failed headless session the owner saw after it failed,
// unchanged 2 days. It is not archived: no timer ages a failed row short of the seven-day floor.
func TestInboxStatesNoTimerFailedSeenTwoDaysStaysUnarchived(t *testing.T) {
	t.Parallel()
	path := archiveStorePath(t)
	now := epicsNow()
	writeFailedHeadless(t, path, noTimerFailedID, now.Add(-2*archiveDay), now.Add(-archiveDay))
	expectNoTimerLists(t, path, map[string]bool{}, map[string]bool{noTimerFailedID: false})
}

// TestInboxStatesNoTimerSevenDaysArchives writes the same done pane and done headless sessions, and a failed headless
// session seen after it failed, each unchanged 7 days. All are archived by the seven-day floor.
func TestInboxStatesNoTimerSevenDaysArchives(t *testing.T) {
	t.Parallel()
	path := archiveStorePath(t)
	now := epicsNow()
	changed := now.Add(-7 * archiveDay)
	writeDonePane(t, path, noTimerPaneSeenID, changed, changed)
	writeDonePane(t, path, noTimerPaneUnseenID, changed, time.Time{})
	writeDoneHeadless(t, path, noTimerHeadlessSeenID, changed, changed)
	writeDoneHeadless(t, path, noTimerHeadlessUnseenID, changed, time.Time{})
	writeFailedHeadless(t, path, noTimerFailedID, changed, now.Add(-6*archiveDay))
	expectNoTimerLists(t, path,
		map[string]bool{noTimerPaneSeenID: true, noTimerPaneUnseenID: true},
		map[string]bool{noTimerHeadlessSeenID: true, noTimerHeadlessUnseenID: true, noTimerFailedID: true})
}
