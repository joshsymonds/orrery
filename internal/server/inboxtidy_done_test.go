package server

import (
	"testing"
	"time"

	orreryv1connect "github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/store"
)

// Done conversation ids of the inbox tidy tests, 32 hex digits each as session ids are. Each test writes its own store,
// so a test's ids need not differ from another's.
const (
	doneSeen12ID     = "1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a"
	doneSeen11ID     = "1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b"
	doneSeenBeforeID = "1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c"
	doneUnseen3dID   = "1d1d1d1d1d1d1d1d1d1d1d1d1d1d1d1d"
	doneUnseen2dID   = "1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e"
	doneSeen3dID     = "1f1f1f1f1f1f1f1f1f1f1f1f1f1f1f1f"
	doneMarkedID     = "2a2a2a2a2a2a2a2a2a2a2a2a2a2a2a2a"
	doneUnarchivedID = "2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b"
)

// doneKind is one kind of done row the tests write: a pane session that Sessions.List answers, or a headless session
// that ListHeadless answers. mark is the archive kind a mark on its rows names. write adds the row id in state done,
// changed and seen at those times, and a zero seen leaves it unseen. change sets the row's change time to at. list
// answers the archived flag of each row the kind's list answers, by id.
type doneKind struct {
	name   string
	mark   string
	write  func(t *testing.T, path, id string, changed, seen time.Time)
	change func(t *testing.T, path, id string, at time.Time)
	list   func(t *testing.T, client orreryv1connect.SessionsClient) map[string]bool
}

// inboxTidyPane is a pane session row, listed by Sessions.List.
var inboxTidyPane = doneKind{
	name:  "Sessions.List",
	mark:  archiveKindSession,
	write: writeDonePane,
	change: func(t *testing.T, path, id string, at time.Time) {
		t.Helper()
		writeDonePane(t, path, id, at, time.Time{})
	},
	list: func(t *testing.T, client orreryv1connect.SessionsClient) map[string]bool {
		t.Helper()
		return archivedByID(listSessionRows(t, client))
	},
}

// inboxTidyHeadless is a headless session row, listed by ListHeadless.
var inboxTidyHeadless = doneKind{
	name:  "ListHeadless",
	mark:  archiveKindHeadless,
	write: writeDoneHeadless,
	change: func(t *testing.T, path, id string, at time.Time) {
		t.Helper()
		applyHeadlessAt(t, path, at, store.EngineSet{ID: id, Changes: store.EngineChanges{State: optionalString("done")}})
	},
	list: func(t *testing.T, client orreryv1connect.SessionsClient) map[string]bool {
		t.Helper()
		return archivedByID(listHeadlessRows(t, client))
	},
}

// doneRow is one done row of a test: its id, its change and seen times (a zero seen is unseen), and whether its list
// archives it.
type doneRow struct {
	id       string
	changed  time.Time
	seen     time.Time
	archived bool
}

// optionalStamp is the unix seconds of at as a pane row's column carries it, or "" for a zero at.
func optionalStamp(at time.Time) string {
	if at.IsZero() {
		return ""
	}
	return stamp(at)
}

// writeDonePane writes the pane row id, in state done, to the store at path, changed and seen at those times.
func writeDonePane(t *testing.T, path, id string, changed, seen time.Time) {
	t.Helper()
	s := openHeadlessStore(t, path)
	pane := store.Pane{ID: id, Pane: "%1", Fields: store.Fields{
		State: "done", Changed: stamp(changed), Seen: optionalStamp(seen),
	}}
	if err := s.SyncPane(t.Context(), "-L tidy", pane, epicsNow()); err != nil {
		t.Fatalf("SyncPane %s: %v", id, err)
	}
	closeStore(t, s)
}

// writeDoneHeadless adds the headless session id to the store at path in state done at changed, and when seen is not
// zero sets its seen time to seen.
func writeDoneHeadless(t *testing.T, path, id string, changed, seen time.Time) {
	t.Helper()
	applyHeadlessAt(t, path, changed, store.EngineAdd{ID: id, Dir: "/work/" + id, State: "done"})
	if !seen.IsZero() {
		applyHeadlessAt(t, path, seen, store.EngineSet{ID: id, Changes: store.EngineChanges{
			Seen: optionalString(stamp(seen)),
		}})
	}
}

// expectDoneRows writes rows as kind to the store at path, and fails the test unless kind's list archives exactly the
// rows marked archived, at epicsNow.
func expectDoneRows(t *testing.T, kind doneKind, path string, rows []doneRow) {
	t.Helper()
	want := make(map[string]bool, len(rows))
	for _, row := range rows {
		kind.write(t, path, row.id, row.changed, row.seen)
		want[row.id] = row.archived
	}
	sessions := sessionsClientAt(t, store.NewReader(path), epicsNow)
	expectArchived(t, kind.name, kind.list(t, sessions), want)
}

// inboxTidyDoneSeenAtTwelveHours checks that a done row seen after its change ages out once it has been unchanged for
// twelve hours. A row seen before its change does not.
func inboxTidyDoneSeenAtTwelveHours(t *testing.T, kind doneKind) {
	t.Helper()
	now := epicsNow()
	expectDoneRows(t, kind, archiveStorePath(t), []doneRow{
		{id: doneSeen12ID, changed: now.Add(-12 * time.Hour), seen: now.Add(-6 * time.Hour), archived: true},
		{id: doneSeen11ID, changed: now.Add(-11 * time.Hour), seen: now.Add(-5 * time.Hour)},
		{id: doneSeenBeforeID, changed: now.Add(-12 * time.Hour), seen: now.Add(-13 * time.Hour)},
	})
}

// inboxTidyDoneUnseenAtThreeDays checks that a done row ages out once it has been unchanged for three days, seen or
// not. An unseen row unchanged for two days does not.
func inboxTidyDoneUnseenAtThreeDays(t *testing.T, kind doneKind) {
	t.Helper()
	now := epicsNow()
	expectDoneRows(t, kind, archiveStorePath(t), []doneRow{
		{id: doneUnseen3dID, changed: now.Add(-3 * archiveDay), archived: true},
		{id: doneUnseen2dID, changed: now.Add(-2 * archiveDay)},
		{id: doneSeen3dID, changed: now.Add(-3 * archiveDay), seen: now.Add(-3 * archiveDay), archived: true},
	})
}

// inboxTidyDoneChangedAfterMark checks that a row archived by a mark comes back once it changes after the mark.
func inboxTidyDoneChangedAfterMark(t *testing.T, kind doneKind) {
	t.Helper()
	path := archiveStorePath(t)
	now := epicsNow()
	kind.write(t, path, doneMarkedID, now.Add(-3*time.Hour), time.Time{})
	archiveRowAt(t, path, kind.mark, doneMarkedID, now.Add(-2*time.Hour))
	sessions := sessionsClientAt(t, store.NewReader(path), epicsNow)
	expectArchived(t, kind.name+" before the change", kind.list(t, sessions), map[string]bool{doneMarkedID: true})

	kind.change(t, path, doneMarkedID, now.Add(-time.Hour))
	expectArchived(t, kind.name+" after the change", kind.list(t, sessions), map[string]bool{doneMarkedID: false})
}

// inboxTidyDoneUnarchiveHolds checks that a row unarchived after its last change stays out of aging until it changes,
// and that a change after the unarchive ages it again.
func inboxTidyDoneUnarchiveHolds(t *testing.T, kind doneKind) {
	t.Helper()
	path := archiveStorePath(t)
	now := epicsNow()
	kind.write(t, path, doneUnarchivedID, now.Add(-10*archiveDay), time.Time{})
	unarchiveRowAt(t, path, kind.mark, doneUnarchivedID, now.Add(-9*archiveDay))
	sessions := sessionsClientAt(t, store.NewReader(path), epicsNow)
	expectArchived(t, kind.name+" after the unarchive", kind.list(t, sessions), map[string]bool{doneUnarchivedID: false})

	kind.change(t, path, doneUnarchivedID, now.Add(-5*archiveDay))
	expectArchived(t, kind.name+" after the change", kind.list(t, sessions), map[string]bool{doneUnarchivedID: true})
}

func TestInboxTidyDoneConversationPaneSeenAgesAtTwelveHours(t *testing.T) {
	t.Parallel()
	inboxTidyDoneSeenAtTwelveHours(t, inboxTidyPane)
}

func TestInboxTidyDoneConversationPaneUnseenAgesAtThreeDays(t *testing.T) {
	t.Parallel()
	inboxTidyDoneUnseenAtThreeDays(t, inboxTidyPane)
}

func TestInboxTidyDoneConversationPaneChangedAfterMarkComesBack(t *testing.T) {
	t.Parallel()
	inboxTidyDoneChangedAfterMark(t, inboxTidyPane)
}

func TestInboxTidyDoneConversationPaneUnarchiveHoldsUntilChange(t *testing.T) {
	t.Parallel()
	inboxTidyDoneUnarchiveHolds(t, inboxTidyPane)
}

func TestInboxTidyDoneConversationHeadlessSeenAgesAtTwelveHours(t *testing.T) {
	t.Parallel()
	inboxTidyDoneSeenAtTwelveHours(t, inboxTidyHeadless)
}

func TestInboxTidyDoneConversationHeadlessUnseenAgesAtThreeDays(t *testing.T) {
	t.Parallel()
	inboxTidyDoneUnseenAtThreeDays(t, inboxTidyHeadless)
}

func TestInboxTidyDoneConversationHeadlessChangedAfterMarkComesBack(t *testing.T) {
	t.Parallel()
	inboxTidyDoneChangedAfterMark(t, inboxTidyHeadless)
}

func TestInboxTidyDoneConversationHeadlessUnarchiveHoldsUntilChange(t *testing.T) {
	t.Parallel()
	inboxTidyDoneUnarchiveHolds(t, inboxTidyHeadless)
}
