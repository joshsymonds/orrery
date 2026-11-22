package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/store"
)

// inboxStatesEpicsClient serves Epics over the records under root, the epics stored holds, and the marks of the store
// at storePath, on the clock now.
func inboxStatesEpicsClient(
	t *testing.T, root, storePath string, stored EpicStoreClient, now func() time.Time,
) orreryv1connect.EpicsClient {
	t.Helper()
	path, handler := newEpicsHandler(
		root, &epicsFake{}, stored, nil, nil, store.NewReader(storePath), 0, now, newRetryClock(),
	)
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return orreryv1connect.NewEpicsClient(srv.Client(), srv.URL)
}

// endConversations ends the pane and the headless session of the closure tests at endedAt, by a closed verdict for
// their change time, in the store at path. A non-zero seenAt marks both seen at that time, and a zero seenAt leaves
// both unseen.
func endConversations(t *testing.T, path string, endedAt, seenAt time.Time) {
	t.Helper()
	applyHeadlessAt(t, path, endedAt, store.EngineAdd{ID: closureHeadlessID, Dir: "/work/ended", State: "idle"})
	at, seen := endedAt, ""
	if !seenAt.IsZero() {
		at, seen = seenAt, stamp(seenAt)
		applyHeadlessAt(t, path, at, store.EngineSet{
			ID: closureHeadlessID, Changes: store.EngineChanges{Seen: optionalString(seen)},
		})
	}
	syncClosurePane(t, path, stamp(endedAt), seen, at)
	setClosureVerdict(t, path, closureVerdictOf(closureHeadlessID, endedAt, true), endedAt)
	setClosureVerdict(t, path, closureVerdictOf(closurePaneID, endedAt, true), endedAt)
}

// TestInboxStatesArchiveEndedUnseen ends a pane and a headless session and never sees them. Both list unarchived 23
// hours after they ended and archived a day after.
func TestInboxStatesArchiveEndedUnseen(t *testing.T) {
	t.Parallel()
	path := archiveStorePath(t)
	clock := &closureClock{now: closureStart()}
	endedAt := clock.now
	endConversations(t, path, endedAt, time.Time{})
	sessions := sessionsClientAt(t, store.NewReader(path), clock.Now)

	clock.now = endedAt.Add(archiveDay - time.Hour)
	expectClosureArchived(t, sessions, "23 hours after it ended", false, false)

	clock.now = endedAt.Add(archiveDay)
	expectClosureArchived(t, sessions, "a day after it ended", true, true)
}

// TestInboxStatesArchiveEndedSeenAfterEnd ends a pane and a headless session, then sees both an hour later. Both list
// unarchived 23 hours after they ended and archived a day after.
func TestInboxStatesArchiveEndedSeenAfterEnd(t *testing.T) {
	t.Parallel()
	path := archiveStorePath(t)
	clock := &closureClock{now: closureStart()}
	endedAt := clock.now
	endConversations(t, path, endedAt, endedAt.Add(time.Hour))
	sessions := sessionsClientAt(t, store.NewReader(path), clock.Now)

	clock.now = endedAt.Add(archiveDay - time.Hour)
	expectClosureArchived(t, sessions, "23 hours after it ended", false, false)

	clock.now = endedAt.Add(archiveDay)
	expectClosureArchived(t, sessions, "a day after it ended", true, true)
}

// TestInboxStatesArchiveFinalEpicSeenAfterEnd lists each stored final epic, changed at the fixed clock and seen an hour
// after, as unarchived 23 hours after its change and archived a day after it.
func TestInboxStatesArchiveFinalEpicSeenAfterEnd(t *testing.T) {
	t.Parallel()
	changed := epicsNow()
	clock := &closureClock{now: changed}
	phases := inboxTidyFinalPhases()
	stored := make([]*enginev1.StoredEpic, 0, len(phases))
	beforeDay := make(map[string]bool, len(phases))
	aDayOn := make(map[string]bool, len(phases))
	for _, phase := range phases {
		id := "repoi/seen-" + phase
		stored = append(stored, inboxTidyFinalStored(id, phase, changed.Unix(), changed.Add(time.Hour).Unix()))
		beforeDay[id] = false
		aDayOn[id] = true
	}
	client := inboxStatesEpicsClient(t, t.TempDir(), archiveStorePath(t), &epicStoreFake{stored: stored}, clock.Now)

	clock.now = changed.Add(archiveDay - time.Hour)
	expectArchived(t, "Epics.List 23 hours after the change", archivedByID(epicsList(t, client)), beforeDay)

	clock.now = changed.Add(archiveDay)
	expectArchived(t, "Epics.List a day after the change", archivedByID(epicsList(t, client)), aDayOn)
}

// TestInboxStatesArchiveRecordFinalOutcomes lists record epics with outcomes released and retired, changed at the fixed
// clock, as unarchived 23 hours after their change and archived once past a day. A record has no seen time, so it is
// judged unseen. A retired record stays live through exactly a day (liveWindow), so it archives one second past it.
func TestInboxStatesArchiveRecordFinalOutcomes(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	changed := epicsNow()
	clock := &closureClock{now: changed}
	writeHead(t, root, "repoi", "released", `{"outcome":"released"}`, changed)
	writeHead(t, root, "repoi", "retired", `{"outcome":"retired"}`, changed)
	client := inboxStatesEpicsClient(t, root, archiveStorePath(t), nil, clock.Now)

	clock.now = changed.Add(archiveDay - time.Hour)
	expectArchived(t, "Epics.List 23 hours after the change", archivedByID(epicsList(t, client)), map[string]bool{
		"repoi/released": false,
		"repoi/retired":  false,
	})

	clock.now = changed.Add(archiveDay + time.Second)
	expectArchived(t, "Epics.List past a day after the change", archivedByID(epicsList(t, client)), map[string]bool{
		"repoi/released": true,
		"repoi/retired":  true,
	})
}
