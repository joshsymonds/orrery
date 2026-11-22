package server

import (
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/store"
)

// Conversation ids of the no-timer tests, 32 hex digits each as session ids are. The two cases share one store, so the
// seven-day case's ids differ from the under-seven-days case's.
const (
	noTimerPaneSeenID       = "4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a"
	noTimerPaneUnseenID     = "4b4b4b4b4b4b4b4b4b4b4b4b4b4b4b4b"
	noTimerHeadlessSeenID   = "4c4c4c4c4c4c4c4c4c4c4c4c4c4c4c4c"
	noTimerHeadlessUnseenID = "4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d"
	noTimerFailedID         = "4e4e4e4e4e4e4e4e4e4e4e4e4e4e4e4e"

	noTimerSevenPaneSeenID       = "6a6a6a6a6a6a6a6a6a6a6a6a6a6a6a6a"
	noTimerSevenPaneUnseenID     = "6b6b6b6b6b6b6b6b6b6b6b6b6b6b6b6b"
	noTimerSevenHeadlessSeenID   = "6c6c6c6c6c6c6c6c6c6c6c6c6c6c6c6c"
	noTimerSevenHeadlessUnseenID = "6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d6d"
	noTimerSevenFailedID         = "6e6e6e6e6e6e6e6e6e6e6e6e6e6e6e6e"
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

// restrictTo keeps the flags of the ids in want. A case that shares a store with others lists their rows too, so it
// checks its own rows here, and each other case checks its rows in its own subtest.
func restrictTo[V any](flags, want map[string]V) map[string]V {
	kept := make(map[string]V, len(want))
	for id := range want {
		if flag, ok := flags[id]; ok {
			kept[id] = flag
		}
	}
	return kept
}

// expectNoTimerLists fails the test unless sessions, read on the clock it was built with, gives the archived flags of
// wantPane for the pane rows it names, and of wantHeadless for the headless rows it names.
func expectNoTimerLists(
	t *testing.T, sessions orreryv1connect.SessionsClient, wantPane, wantHeadless map[string]bool,
) {
	t.Helper()
	expectArchived(t, "Sessions.List", restrictTo(archivedByID(listSessionRows(t, sessions)), wantPane), wantPane)
	expectArchived(t, "ListHeadless",
		restrictTo(archivedByID(listHeadlessRows(t, sessions)), wantHeadless), wantHeadless)
}

// TestInboxStatesNoTimer writes both no-timer cases into one store and reads it on the fixed clock epicsNow. The first
// case writes a done pane and a done headless session, each seen at its change and unseen, unchanged 6 days 23 hours,
// and a failed headless session seen and unchanged 2 days. The second writes the same done rows and a failed headless
// session seen after it failed, each unchanged 7 days. No timer ages a done or failed row short of the seven-day floor,
// and the seven-day floor archives every one of them.
func TestInboxStatesNoTimer(t *testing.T) {
	t.Parallel()
	path := archiveStorePath(t)
	now := epicsNow()
	short := now.Add(-staleShortAge)
	week := now.Add(-7 * archiveDay)
	applyHeadlessAt(t, path, short,
		store.EngineAdd{ID: noTimerHeadlessSeenID, Dir: "/work/" + noTimerHeadlessSeenID, State: "done"},
		store.EngineSet{ID: noTimerHeadlessSeenID, Changes: store.EngineChanges{Seen: new(stamp(short))}},
		store.EngineAdd{ID: noTimerHeadlessUnseenID, Dir: "/work/" + noTimerHeadlessUnseenID, State: "done"},
	)
	writeDonePane(t, path, noTimerPaneSeenID, short, short)
	writeDonePane(t, path, noTimerPaneUnseenID, short, time.Time{})
	writeFailedHeadless(t, path, noTimerFailedID, now.Add(-2*archiveDay), now.Add(-archiveDay))
	applyHeadlessAt(t, path, week,
		store.EngineAdd{ID: noTimerSevenHeadlessSeenID, Dir: "/work/" + noTimerSevenHeadlessSeenID, State: "done"},
		store.EngineSet{ID: noTimerSevenHeadlessSeenID, Changes: store.EngineChanges{Seen: new(stamp(week))}},
		store.EngineAdd{ID: noTimerSevenHeadlessUnseenID, Dir: "/work/" + noTimerSevenHeadlessUnseenID, State: "done"},
	)
	writeDonePane(t, path, noTimerSevenPaneSeenID, week, week)
	writeDonePane(t, path, noTimerSevenPaneUnseenID, week, time.Time{})
	writeFailedHeadless(t, path, noTimerSevenFailedID, week, now.Add(-6*archiveDay))

	sessions := sessionsClientAt(t, store.NewReader(path), epicsNow)
	t.Run("DoneSixDaysTwentyThreeHoursStaysUnarchived", func(t *testing.T) {
		t.Parallel()
		expectNoTimerLists(t, sessions,
			map[string]bool{noTimerPaneSeenID: false, noTimerPaneUnseenID: false},
			map[string]bool{noTimerHeadlessSeenID: false, noTimerHeadlessUnseenID: false, noTimerFailedID: false})
	})
	t.Run("SevenDaysArchives", func(t *testing.T) {
		t.Parallel()
		expectNoTimerLists(t, sessions,
			map[string]bool{noTimerSevenPaneSeenID: true, noTimerSevenPaneUnseenID: true},
			map[string]bool{
				noTimerSevenHeadlessSeenID: true, noTimerSevenHeadlessUnseenID: true, noTimerSevenFailedID: true,
			})
	})
}
