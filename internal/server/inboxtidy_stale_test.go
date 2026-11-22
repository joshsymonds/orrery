package server

import (
	"testing"
	"time"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	orreryv1connect "github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/store"
)

// Headless session ids of the stale tests, 32 hex digits each as session ids are. Each test writes its own store.
const (
	staleHeadlessWorkingSevenID = "3a3a3a3a3a3a3a3a3a3a3a3a3a3a3a3a"
	staleHeadlessWaitingSevenID = "3b3b3b3b3b3b3b3b3b3b3b3b3b3b3b3b"
	staleHeadlessDoneSevenID    = "3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c"
	staleHeadlessWorkingShortID = "3d3d3d3d3d3d3d3d3d3d3d3d3d3d3d3d"
	staleHeadlessWaitingShortID = "3e3e3e3e3e3e3e3e3e3e3e3e3e3e3e3e"
	staleHeadlessDoneShortID    = "3f3f3f3f3f3f3f3f3f3f3f3f3f3f3f3f"
)

// staleShortAge is one hour short of the seven-day floor: a row unchanged this long is 6 days 23 hours old.
const staleShortAge = 7*archiveDay - time.Hour

// staleRow is one row a stale test writes: its id, its state, its change time and whether its list archives it.
type staleRow struct {
	id       string
	state    string
	changed  time.Time
	archived bool
}

// staleKind is one kind of list row the stale tests write: a pane session that Sessions.List answers, or a headless
// session that ListHeadless answers. write adds row to the store at path. list answers the archived flag of each row
// the kind's list answers, by id.
type staleKind struct {
	name  string
	write func(t *testing.T, path string, row staleRow)
	list  func(t *testing.T, client orreryv1connect.SessionsClient) map[string]bool
}

// stalePane is a pane session row, listed by Sessions.List.
func stalePane() staleKind {
	return staleKind{
		name: "Sessions.List",
		write: func(t *testing.T, path string, row staleRow) {
			t.Helper()
			staleWritePane(t, path, row.id, row.state, stamp(row.changed))
		},
		list: func(t *testing.T, client orreryv1connect.SessionsClient) map[string]bool {
			t.Helper()
			return archivedByID(listSessionRows(t, client))
		},
	}
}

// staleHeadless is a headless session row, listed by ListHeadless.
func staleHeadless() staleKind {
	return staleKind{
		name: "ListHeadless",
		write: func(t *testing.T, path string, row staleRow) {
			t.Helper()
			applyHeadlessAt(t, path, row.changed,
				store.EngineAdd{ID: row.id, Dir: "/work/" + row.id, State: row.state})
		},
		list: func(t *testing.T, client orreryv1connect.SessionsClient) map[string]bool {
			t.Helper()
			return archivedByID(listHeadlessRows(t, client))
		},
	}
}

// staleWritePane syncs the pane row id, in state, to the store at path, with changed as its changed column. The column
// is written as given, so an empty or unparseable column is a row with no change time.
func staleWritePane(t *testing.T, path, id, state, changed string) {
	t.Helper()
	s := openHeadlessStore(t, path)
	pane := store.Pane{ID: id, Pane: "%1", Fields: store.Fields{State: state, Changed: changed}}
	if err := s.SyncPane(t.Context(), "-L stale", pane, epicsNow()); err != nil {
		t.Fatalf("SyncPane %s: %v", id, err)
	}
	closeStore(t, s)
}

// expectStaleRows writes rows as kind to a fresh store, and fails the test unless kind's list, read at the clock now,
// archives exactly the rows marked archived.
func expectStaleRows(t *testing.T, kind staleKind, now func() time.Time, rows []staleRow) {
	t.Helper()
	path := archiveStorePath(t)
	want := make(map[string]bool, len(rows))
	for _, row := range rows {
		kind.write(t, path, row)
		want[row.id] = row.archived
	}
	sessions := sessionsClientAt(t, store.NewReader(path), now)
	expectArchived(t, kind.name, kind.list(t, sessions), want)
}

// TestInboxTidyStaleLiveEpicArchivesAtSevenDays lists a stored accepted epic unchanged seven days as archived, and one
// unchanged 6 days 23 hours as unarchived. A live epic ages by the seven-day floor alone.
func TestInboxTidyStaleLiveEpicArchivesAtSevenDays(t *testing.T) {
	t.Parallel()
	stored := &epicStoreFake{stored: []*enginev1.StoredEpic{
		storedEpicOf("repoa/seven", "accepted", epicsAgo(7*archiveDay)),
		storedEpicOf("repoa/short", "accepted", epicsAgo(staleShortAge)),
	}}
	client := archiveEpicsClient(t, t.TempDir(), archiveStorePath(t), stored)
	expectArchived(t, "Epics.List", archivedByID(epicsList(t, client)), map[string]bool{
		"repoa/seven": true,
		"repoa/short": false,
	})
}

// TestInboxTidyStaleNotLiveEpicArchivesAtSevenDays lists a record epic whose outcome is not final, and is not live once
// a day has passed, as archived at seven days unchanged and as unarchived at 6 days 23 hours.
func TestInboxTidyStaleNotLiveEpicArchivesAtSevenDays(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	now := epicsNow()
	writeHead(t, root, "repoa", "seven", `{"outcome":"ended_with_gaps"}`, now.Add(-7*archiveDay))
	writeHead(t, root, "repoa", "short", `{"outcome":"ended_with_gaps"}`, now.Add(-staleShortAge))
	client := archiveEpicsClient(t, root, archiveStorePath(t), nil)
	expectArchived(t, "Epics.List", archivedByID(epicsList(t, client)), map[string]bool{
		"repoa/seven": true,
		"repoa/short": false,
	})
}

// TestInboxTidyStalePaneSevenDaysArchives lists each pane row in working, waiting and done, unchanged seven days, as
// archived.
func TestInboxTidyStalePaneSevenDaysArchives(t *testing.T) {
	t.Parallel()
	changed := epicsNow().Add(-7 * archiveDay)
	expectStaleRows(t, stalePane(), epicsNow, []staleRow{
		{id: "stale-pane-working-seven", state: "working", changed: changed, archived: true},
		{id: "stale-pane-waiting-seven", state: "waiting", changed: changed, archived: true},
		{id: "stale-pane-done-seven", state: "done", changed: changed, archived: true},
	})
}

// TestInboxTidyStalePaneSixDaysTwentyThreeHoursFollowsOtherRules lists each pane row unchanged 6 days 23 hours. A
// working or waiting row has no rule of its own, so it lists unarchived. A done row follows the done rule, which
// archives an unseen done row at three days, so it lists archived.
func TestInboxTidyStalePaneSixDaysTwentyThreeHoursFollowsOtherRules(t *testing.T) {
	t.Parallel()
	changed := epicsNow().Add(-staleShortAge)
	expectStaleRows(t, stalePane(), epicsNow, []staleRow{
		{id: "stale-pane-working-short", state: "working", changed: changed},
		{id: "stale-pane-waiting-short", state: "waiting", changed: changed},
		{id: "stale-pane-done-short", state: "done", changed: changed, archived: true},
	})
}

// TestInboxTidyStaleHeadlessSevenDaysArchives lists each headless session in working, waiting and done, unchanged
// seven days, as archived.
func TestInboxTidyStaleHeadlessSevenDaysArchives(t *testing.T) {
	t.Parallel()
	changed := epicsNow().Add(-7 * archiveDay)
	expectStaleRows(t, staleHeadless(), epicsNow, []staleRow{
		{id: staleHeadlessWorkingSevenID, state: "working", changed: changed, archived: true},
		{id: staleHeadlessWaitingSevenID, state: "waiting", changed: changed, archived: true},
		{id: staleHeadlessDoneSevenID, state: "done", changed: changed, archived: true},
	})
}

// TestInboxTidyStaleHeadlessSixDaysTwentyThreeHoursFollowsOtherRules lists each headless session unchanged 6 days 23
// hours. A working or waiting session has no rule of its own, so it lists unarchived. A done session follows the done
// rule, which archives an unseen done session at three days, so it lists archived.
func TestInboxTidyStaleHeadlessSixDaysTwentyThreeHoursFollowsOtherRules(t *testing.T) {
	t.Parallel()
	changed := epicsNow().Add(-staleShortAge)
	expectStaleRows(t, staleHeadless(), epicsNow, []staleRow{
		{id: staleHeadlessWorkingShortID, state: "working", changed: changed},
		{id: staleHeadlessWaitingShortID, state: "waiting", changed: changed},
		{id: staleHeadlessDoneShortID, state: "done", changed: changed, archived: true},
	})
}

// TestInboxTidyStalePaneWithoutChangeTimeNeverAges lists pane rows in working, waiting and done whose changed column is
// empty or not unix seconds, at a clock ten years on, as unarchived. A row with no change time has no rule that ages it.
func TestInboxTidyStalePaneWithoutChangeTimeNeverAges(t *testing.T) {
	t.Parallel()
	path := archiveStorePath(t)
	later := func() time.Time { return epicsNow().Add(3650 * archiveDay) }
	want := make(map[string]bool)
	for _, state := range []string{"working", "waiting", "done"} {
		for _, column := range []struct{ name, changed string }{{"empty", ""}, {"unparseable", "not a time"}} {
			id := "stale-pane-" + state + "-" + column.name
			staleWritePane(t, path, id, state, column.changed)
			want[id] = false
		}
	}
	sessions := sessionsClientAt(t, store.NewReader(path), later)
	expectArchived(t, "Sessions.List", archivedByID(listSessionRows(t, sessions)), want)
}
