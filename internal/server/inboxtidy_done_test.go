package server

import (
	"testing"
	"time"

	orreryv1connect "github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/store"
)

// Done conversation ids of the inbox tidy tests, 32 hex digits each as session ids are. Each test writes its own store,
// so a test's ids need not differ from another's.
const doneMarkedID = "2a2a2a2a2a2a2a2a2a2a2a2a2a2a2a2a"

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
func inboxTidyPane() doneKind {
	return doneKind{
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
}

// inboxTidyHeadless is a headless session row, listed by ListHeadless.
func inboxTidyHeadless() doneKind {
	return doneKind{
		name:  "ListHeadless",
		mark:  archiveKindHeadless,
		write: writeDoneHeadless,
		change: func(t *testing.T, path, id string, at time.Time) {
			t.Helper()
			applyHeadlessAt(t, path, at, store.EngineSet{
				ID: id, Changes: store.EngineChanges{State: optionalString("done")},
			})
		},
		list: func(t *testing.T, client orreryv1connect.SessionsClient) map[string]bool {
			t.Helper()
			return archivedByID(listHeadlessRows(t, client))
		},
	}
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

func TestInboxTidyDoneConversationPaneChangedAfterMarkComesBack(t *testing.T) {
	t.Parallel()
	inboxTidyDoneChangedAfterMark(t, inboxTidyPane())
}

func TestInboxTidyDoneConversationHeadlessChangedAfterMarkComesBack(t *testing.T) {
	t.Parallel()
	inboxTidyDoneChangedAfterMark(t, inboxTidyHeadless())
}
