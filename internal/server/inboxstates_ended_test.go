package server

import (
	"maps"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// Headless and pane ids of the ended tests, with invented values. The headless ids are 32 hex digits each.
const (
	endedWorkingID = "2a2a2a2a2a2a2a2a2a2a2a2a2a2a2a2a"
	endedWaitingID = "2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b"
	endedDoneID    = "2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c"
	endedFailedID  = "2d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2d"
	endedPaneWork  = "orrery-ended-working"
	endedPaneWait  = "orrery-ended-waiting"
	endedPaneDone  = "orrery-ended-done"
)

// endableRow is a list row with an id and an ended flag and time.
type endableRow interface {
	GetId() string
	GetEnded() bool
	GetEndedAt() int64
}

// endedRowState is a row's ended flag and ended time as a list answered them.
type endedRowState struct {
	ended   bool
	endedAt int64
}

// endedStates maps the id of each row a list answered to its ended flag and ended time.
func endedStates[T endableRow](rows []T) map[string]endedRowState {
	states := make(map[string]endedRowState, len(rows))
	for _, row := range rows {
		states[row.GetId()] = endedRowState{ended: row.GetEnded(), endedAt: row.GetEndedAt()}
	}
	return states
}

// expectEnded fails the test unless list gives the ended states of want, and no other row.
func expectEnded(t *testing.T, list string, got, want map[string]endedRowState) {
	t.Helper()
	if !maps.Equal(got, want) {
		t.Fatalf("%s ended = %v, want %v", list, got, want)
	}
}

// syncEndedPane writes the pane row id in state, with changed as its change time and no seen time, in the store at
// path at changed, and closes the store.
func syncEndedPane(t *testing.T, path, id, state string, changed time.Time) {
	t.Helper()
	s := openHeadlessStore(t, path)
	pane := store.Pane{ID: id, Pane: "%10", Fields: store.Fields{State: state, Changed: stamp(changed)}}
	if err := s.SyncPane(t.Context(), "-L ended", pane, changed); err != nil {
		t.Fatalf("SyncPane %s: %v", id, err)
	}
	closeStore(t, s)
}

// listEndedSessions calls Sessions.List and ListHeadless over the store at path on the clock now, and returns the
// ended states of the pane rows and of the headless rows.
func listEndedSessions(
	t *testing.T, path string, now func() time.Time,
) (panes, headless map[string]endedRowState) {
	t.Helper()
	sessions := sessionsClientAt(t, store.NewReader(path), now)
	return endedStates(listSessionRows(t, sessions)), endedStates(listHeadlessRows(t, sessions))
}

func TestInboxStatesEndedClosedAtCurrentChange(t *testing.T) {
	t.Parallel()
	path := archiveStorePath(t)
	clock := &closureClock{now: closureStart()}
	changed := clock.now.Add(-10 * time.Minute)
	applyHeadlessAt(t, path, changed, store.EngineAdd{ID: closureHeadlessID, Dir: "/work/ended", State: "done"})
	syncClosurePane(t, path, stamp(changed), "", changed)
	setClosureVerdict(t, path, closureVerdictOf(closureHeadlessID, changed, true), clock.now)
	setClosureVerdict(t, path, closureVerdictOf(closurePaneID, changed, true), clock.now)

	panes, headless := listEndedSessions(t, path, clock.Now)
	want := endedRowState{ended: true, endedAt: changed.Unix()}
	expectEnded(t, "Sessions.List", panes, map[string]endedRowState{closurePaneID: want})
	expectEnded(t, "ListHeadless", headless, map[string]endedRowState{closureHeadlessID: want})
}

func TestInboxStatesEndedStoppedHeadless(t *testing.T) {
	t.Parallel()
	path := archiveStorePath(t)
	clock := &closureClock{now: closureStart()}
	changed := clock.now.Add(-10 * time.Minute)
	applyHeadlessAt(t, path, changed, store.EngineAdd{ID: closureHeadlessID, Dir: "/work/stopped", State: "stopped"})

	_, headless := listEndedSessions(t, path, clock.Now)
	want := endedRowState{ended: true, endedAt: changed.Unix()}
	expectEnded(t, "ListHeadless", headless, map[string]endedRowState{closureHeadlessID: want})
}

func TestInboxStatesEndedVerdictForEarlierChange(t *testing.T) {
	t.Parallel()
	path := archiveStorePath(t)
	clock := &closureClock{now: closureStart()}
	closedAt := clock.now.Add(-2 * time.Hour)
	laterAt := clock.now.Add(-10 * time.Minute)
	applyHeadlessAt(t, path, closedAt, store.EngineAdd{ID: closureHeadlessID, Dir: "/work/earlier", State: "idle"})
	syncClosurePane(t, path, stamp(closedAt), "", closedAt)
	setClosureVerdict(t, path, closureVerdictOf(closureHeadlessID, closedAt, true), closedAt)
	setClosureVerdict(t, path, closureVerdictOf(closurePaneID, closedAt, true), closedAt)
	applyHeadlessAt(t, path, laterAt, store.EngineSet{
		ID: closureHeadlessID, Changes: store.EngineChanges{State: new("working")},
	})
	syncClosurePane(t, path, stamp(laterAt), "", laterAt)

	panes, headless := listEndedSessions(t, path, clock.Now)
	want := endedRowState{}
	expectEnded(t, "Sessions.List", panes, map[string]endedRowState{closurePaneID: want})
	expectEnded(t, "ListHeadless", headless, map[string]endedRowState{closureHeadlessID: want})
}

func TestInboxStatesEndedNotClosedVerdict(t *testing.T) {
	t.Parallel()
	path := archiveStorePath(t)
	clock := &closureClock{now: closureStart()}
	changed := clock.now.Add(-10 * time.Minute)
	applyHeadlessAt(t, path, changed, store.EngineAdd{ID: closureHeadlessID, Dir: "/work/open", State: "idle"})
	syncClosurePane(t, path, stamp(changed), "", changed)
	setClosureVerdict(t, path, closureVerdictOf(closureHeadlessID, changed, false), clock.now)
	setClosureVerdict(t, path, closureVerdictOf(closurePaneID, changed, false), clock.now)

	panes, headless := listEndedSessions(t, path, clock.Now)
	want := endedRowState{}
	expectEnded(t, "Sessions.List", panes, map[string]endedRowState{closurePaneID: want})
	expectEnded(t, "ListHeadless", headless, map[string]endedRowState{closureHeadlessID: want})
}

func TestInboxStatesEndedOpenStatesWithoutVerdict(t *testing.T) {
	t.Parallel()
	path := archiveStorePath(t)
	clock := &closureClock{now: closureStart()}
	changed := clock.now.Add(-10 * time.Minute)
	for id, state := range map[string]string{
		endedWorkingID: "working", endedWaitingID: "waiting", endedDoneID: "done", endedFailedID: "failed",
	} {
		applyHeadlessAt(t, path, changed, store.EngineAdd{ID: id, Dir: "/work/" + state, State: state})
	}
	for id, state := range map[string]string{endedPaneWork: "working", endedPaneWait: "waiting", endedPaneDone: "done"} {
		syncEndedPane(t, path, id, state, changed)
	}

	panes, headless := listEndedSessions(t, path, clock.Now)
	none := endedRowState{}
	expectEnded(t, "Sessions.List", panes, map[string]endedRowState{
		endedPaneWork: none, endedPaneWait: none, endedPaneDone: none,
	})
	expectEnded(t, "ListHeadless", headless, map[string]endedRowState{
		endedWorkingID: none, endedWaitingID: none, endedDoneID: none, endedFailedID: none,
	})
}
