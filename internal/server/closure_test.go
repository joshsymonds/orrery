package server

import (
	"testing"
	"time"

	orreryv1connect "github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/store"
)

// Session ids of the closure tests: a headless session and a pane, with invented values.
const (
	closureHeadlessID = "1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c"
	closurePaneID     = "orrery-closed"
)

// closureClock is the fixed clock of the closure tests. A test moves now between list calls.
type closureClock struct {
	now time.Time
}

// Now returns the clock's time.
func (c *closureClock) Now() time.Time {
	return c.now
}

// closureStart is the time the closure tests start their clock at.
func closureStart() time.Time {
	return time.Unix(1_700_000_000, 0)
}

// syncClosurePane writes the pane row closurePaneID with the changed and seen columns given, as unix seconds, in the
// store at path at at. An empty seen is unseen.
func syncClosurePane(t *testing.T, path, changed, seen string, at time.Time) {
	t.Helper()
	s := openHeadlessStore(t, path)
	pane := store.Pane{
		ID: closurePaneID, Pane: "%9", Fields: store.Fields{Changed: changed, Seen: seen},
	}
	if err := s.SyncPane(t.Context(), "-L closure", pane, at); err != nil {
		t.Fatalf("SyncPane %s: %v", closurePaneID, err)
	}
	closeStore(t, s)
}

// setClosureVerdict stores verdict in the store at path, stamped at at, and closes the store.
func setClosureVerdict(t *testing.T, path string, verdict store.ClosureVerdict, at time.Time) {
	t.Helper()
	s := openHeadlessStore(t, path)
	if err := s.SetClosureVerdict(t.Context(), verdict, at); err != nil {
		t.Fatalf("SetClosureVerdict %s: %v", verdict.Session, err)
	}
	closeStore(t, s)
}

// closureVerdictOf is the verdict of session id that was written for the change time changed.
func closureVerdictOf(id string, changed time.Time, closed bool) store.ClosureVerdict {
	return store.ClosureVerdict{Session: id, Changed: changed.Unix(), Closed: closed, Why: "the question is answered"}
}

// expectClosureArchived calls ListHeadless and Sessions.List over sessions and fails the test unless the headless
// session and the pane have the archived flags wantHeadless and wantPane.
func expectClosureArchived(
	t *testing.T, sessions orreryv1connect.SessionsClient, step string, wantHeadless, wantPane bool,
) {
	t.Helper()
	expectArchived(t, step+" ListHeadless", archivedByID(listHeadlessRows(t, sessions)),
		map[string]bool{closureHeadlessID: wantHeadless})
	expectArchived(t, step+" Sessions.List", archivedByID(listSessionRows(t, sessions)),
		map[string]bool{closurePaneID: wantPane})
}

func TestInboxClosureArchiveOnceSeen(t *testing.T) {
	t.Parallel()
	path := archiveStorePath(t)
	clock := &closureClock{now: closureStart()}
	changed := clock.now.Add(-10 * time.Minute)
	applyHeadlessAt(t, path, changed, store.EngineAdd{ID: closureHeadlessID, Dir: "/work/closed", State: "idle"})
	syncClosurePane(t, path, stamp(changed), "", changed)
	setClosureVerdict(t, path, closureVerdictOf(closureHeadlessID, changed, true), clock.now)
	setClosureVerdict(t, path, closureVerdictOf(closurePaneID, changed, true), clock.now)
	sessions := sessionsClientAt(t, store.NewReader(path), clock.Now)
	expectClosureArchived(t, sessions, "unseen", false, false)

	seenBefore := stamp(changed.Add(-time.Second))
	applyHeadlessAt(t, path, clock.now, store.EngineSet{
		ID: closureHeadlessID, Changes: store.EngineChanges{Seen: optionalString(seenBefore)},
	})
	syncClosurePane(t, path, stamp(changed), seenBefore, clock.now)
	expectClosureArchived(t, sessions, "seen a second before the change", false, false)

	seenAt := stamp(changed)
	applyHeadlessAt(t, path, clock.now, store.EngineSet{
		ID: closureHeadlessID, Changes: store.EngineChanges{Seen: optionalString(seenAt)},
	})
	syncClosurePane(t, path, stamp(changed), seenAt, clock.now)
	expectClosureArchived(t, sessions, "seen at the change", true, true)
}

func TestInboxClosureArchiveUnseenAtOneHour(t *testing.T) {
	t.Parallel()
	path := archiveStorePath(t)
	clock := &closureClock{now: closureStart()}
	changed := clock.now
	applyHeadlessAt(t, path, changed, store.EngineAdd{ID: closureHeadlessID, Dir: "/work/closed", State: "idle"})
	syncClosurePane(t, path, stamp(changed), "", changed)
	setClosureVerdict(t, path, closureVerdictOf(closureHeadlessID, changed, true), changed)
	setClosureVerdict(t, path, closureVerdictOf(closurePaneID, changed, true), changed)
	sessions := sessionsClientAt(t, store.NewReader(path), clock.Now)

	clock.now = changed.Add(time.Hour - time.Minute)
	expectClosureArchived(t, sessions, "59 minutes after the change", false, false)

	clock.now = changed.Add(time.Hour)
	expectClosureArchived(t, sessions, "an hour after the change", true, true)
}

func TestInboxClosureArchiveNotClosedArchivesNothing(t *testing.T) {
	t.Parallel()
	path := archiveStorePath(t)
	clock := &closureClock{now: closureStart()}
	changed := clock.now.Add(-2 * time.Hour)
	applyHeadlessAt(t, path, changed, store.EngineAdd{ID: closureHeadlessID, Dir: "/work/open", State: "idle"})
	applyHeadlessAt(t, path, changed, store.EngineSet{
		ID: closureHeadlessID, Changes: store.EngineChanges{Seen: optionalString(stamp(changed))},
	})
	syncClosurePane(t, path, stamp(changed), stamp(changed), changed)
	setClosureVerdict(t, path, closureVerdictOf(closureHeadlessID, changed, false), clock.now)
	setClosureVerdict(t, path, closureVerdictOf(closurePaneID, changed, false), clock.now)
	sessions := sessionsClientAt(t, store.NewReader(path), clock.Now)
	expectClosureArchived(t, sessions, "not closed, seen, two hours on", false, false)
}

func TestInboxClosureReturnsWhenChangedSinceVerdict(t *testing.T) {
	t.Parallel()
	path := archiveStorePath(t)
	clock := &closureClock{now: closureStart()}
	closedAt := clock.now.Add(-4 * time.Hour)
	laterAt := clock.now.Add(-2 * time.Hour)
	applyHeadlessAt(t, path, closedAt, store.EngineAdd{ID: closureHeadlessID, Dir: "/work/later", State: "idle"})
	syncClosurePane(t, path, stamp(closedAt), "", closedAt)
	setClosureVerdict(t, path, closureVerdictOf(closureHeadlessID, closedAt, true), closedAt)
	setClosureVerdict(t, path, closureVerdictOf(closurePaneID, closedAt, true), closedAt)

	applyHeadlessAt(t, path, laterAt, store.EngineSet{
		ID: closureHeadlessID, Changes: store.EngineChanges{State: optionalString("working")},
	})
	applyHeadlessAt(t, path, laterAt, store.EngineSet{
		ID: closureHeadlessID, Changes: store.EngineChanges{Seen: optionalString(stamp(laterAt))},
	})
	syncClosurePane(t, path, stamp(laterAt), stamp(laterAt), laterAt)
	sessions := sessionsClientAt(t, store.NewReader(path), clock.Now)
	expectClosureArchived(t, sessions, "changed since the closed turn, seen and past an hour", false, false)
}

func TestInboxClosureReturnsWhenUnarchived(t *testing.T) {
	t.Parallel()
	path := archiveStorePath(t)
	clock := &closureClock{now: closureStart()}
	closedAt := clock.now.Add(-3 * time.Hour)
	seenAt := closedAt.Add(time.Minute)
	applyHeadlessAt(t, path, closedAt, store.EngineAdd{ID: closureHeadlessID, Dir: "/work/back", State: "idle"})
	applyHeadlessAt(t, path, seenAt, store.EngineSet{
		ID: closureHeadlessID, Changes: store.EngineChanges{Seen: optionalString(stamp(seenAt))},
	})
	syncClosurePane(t, path, stamp(closedAt), stamp(seenAt), seenAt)
	setClosureVerdict(t, path, closureVerdictOf(closureHeadlessID, closedAt, true), closedAt)
	setClosureVerdict(t, path, closureVerdictOf(closurePaneID, closedAt, true), closedAt)
	sessions := sessionsClientAt(t, store.NewReader(path), clock.Now)
	expectClosureArchived(t, sessions, "closed, seen and past an hour", true, true)

	unarchivedAt := clock.now.Add(-time.Hour)
	unarchiveRowAt(t, path, archiveKindHeadless, closureHeadlessID, unarchivedAt)
	unarchiveRowAt(t, path, archiveKindSession, closurePaneID, unarchivedAt)
	expectClosureArchived(t, sessions, "unarchived after the closed turn", false, false)

	// A new turn changes both rows after the unarchive. The verdict is for the closed turn, so the rows stay listed
	// even seen and two hours past the new change.
	turnAt := clock.now
	applyHeadlessAt(t, path, turnAt, store.EngineSet{
		ID: closureHeadlessID, Changes: store.EngineChanges{State: optionalString("working")},
	})
	applyHeadlessAt(t, path, turnAt, store.EngineSet{
		ID: closureHeadlessID, Changes: store.EngineChanges{Seen: optionalString(stamp(turnAt))},
	})
	syncClosurePane(t, path, stamp(turnAt), stamp(turnAt), turnAt)
	clock.now = turnAt.Add(2 * time.Hour)
	expectClosureArchived(t, sessions, "a later turn two hours on", false, false)
}
