package server

import (
	"maps"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/store"
)

// archiveDay is one day, the unit the aging tests count in.
const archiveDay = 24 * time.Hour

// Headless session ids of the archive tests, 32 hex digits each as session ids are.
const (
	archiveAgedID    = "0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a"
	archiveYoungerID = "0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b"
	archiveWaitingID = "0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c"
	archiveMarkedID  = "0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e"
)

// archiveStorePath returns the path of a store with no rows.
func archiveStorePath(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "orrery.db")
	closeStore(t, openHeadlessStore(t, path))
	return path
}

// closeStore closes s, so that the reads that follow see what it wrote.
func closeStore(t *testing.T, s *store.Store) {
	t.Helper()
	if err := s.Close(); err != nil {
		t.Fatalf("Close store: %v", err)
	}
}

// applyHeadlessAt applies ops to the store at path in one write stamped at at, and closes the store.
func applyHeadlessAt(t *testing.T, path string, at time.Time, ops ...store.EngineOp) {
	t.Helper()
	s := openHeadlessStore(t, path)
	applyHeadless(t, s, at, ops...)
	closeStore(t, s)
}

// archiveRowAt archives the row of kind and id in the store at path at at, and closes the store.
func archiveRowAt(t *testing.T, path, kind, id string, at time.Time) {
	t.Helper()
	s := openHeadlessStore(t, path)
	if err := s.ArchiveRow(t.Context(), kind, id, at); err != nil {
		t.Fatalf("ArchiveRow %s %s: %v", kind, id, err)
	}
	closeStore(t, s)
}

// unarchiveRowAt unarchives the row of kind and id in the store at path at at, and closes the store.
func unarchiveRowAt(t *testing.T, path, kind, id string, at time.Time) {
	t.Helper()
	s := openHeadlessStore(t, path)
	if err := s.UnarchiveRow(t.Context(), kind, id, at); err != nil {
		t.Fatalf("UnarchiveRow %s %s: %v", kind, id, err)
	}
	closeStore(t, s)
}

// archiveEpicsClient serves Epics over the records under root, the epics stored holds, and the marks of the store at
// storePath, at epicsNow. A nil stored has no database epics.
func archiveEpicsClient(t *testing.T, root, storePath string, stored EpicStoreClient) orreryv1connect.EpicsClient {
	t.Helper()
	path, handler := newEpicsHandler(
		root, &epicsFake{}, stored, nil, nil, store.NewReader(storePath), 0, epicsNow, newRetryClock(),
	)
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return orreryv1connect.NewEpicsClient(srv.Client(), srv.URL)
}

// archivableRow is a list row with an id and an archived flag.
type archivableRow interface {
	GetId() string
	GetArchived() bool
}

// archivedByID maps the id of each row a list answered to whether the list archived it.
func archivedByID[T archivableRow](rows []T) map[string]bool {
	flags := make(map[string]bool, len(rows))
	for _, row := range rows {
		flags[row.GetId()] = row.GetArchived()
	}
	return flags
}

// expectArchived fails the test unless list gives the archived flags of want, and no other row.
func expectArchived(t *testing.T, list string, got, want map[string]bool) {
	t.Helper()
	if !maps.Equal(got, want) {
		t.Fatalf("%s archived = %v, want %v", list, got, want)
	}
}

// listSessionRows calls Sessions.List and returns the rows it answered.
func listSessionRows(t *testing.T, client orreryv1connect.SessionsClient) []*orreryv1.Session {
	t.Helper()
	resp, err := client.List(t.Context(), connect.NewRequest(&orreryv1.ListRequest{}))
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	return resp.Msg.GetSessions()
}

// listHeadlessRows calls Sessions.ListHeadless and returns the rows it answered.
func listHeadlessRows(t *testing.T, client orreryv1connect.SessionsClient) []*orreryv1.HeadlessSession {
	t.Helper()
	resp, err := client.ListHeadless(t.Context(), connect.NewRequest(&orreryv1.ListHeadlessRequest{}))
	if err != nil {
		t.Fatalf("ListHeadless: %v", err)
	}
	return resp.Msg.GetSessions()
}

// TestAutoArchiveLiveEpicArchivesAfterSevenDays expects accepted and ended_with_gaps epics, both live, unchanged 400 days
// to list archived: the seven-day floor ages a live epic too.
func TestAutoArchiveLiveEpicArchivesAfterSevenDays(t *testing.T) {
	t.Parallel()
	stored := &epicStoreFake{stored: []*enginev1.StoredEpic{
		storedEpicOf("repoa/accepted", "accepted", epicsAgo(400*archiveDay)),
		storedEpicOf("repoa/gaps", "ended_with_gaps", epicsAgo(400*archiveDay)),
	}}
	client := archiveEpicsClient(t, t.TempDir(), archiveStorePath(t), stored)
	expectArchived(t, "Epics.List", archivedByID(epicsList(t, client)), map[string]bool{
		"repoa/accepted": true,
		"repoa/gaps":     true,
	})
}

func TestAutoArchiveNonLiveRecordEpicAfterSevenDays(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeHead(t, root, "repoa", "aged", `{"outcome":"shipped"}`, epicsNow().Add(-7*archiveDay))
	writeHead(t, root, "repoa", "younger", `{"outcome":"shipped"}`, epicsNow().Add(-7*archiveDay+time.Second))
	client := archiveEpicsClient(t, root, archiveStorePath(t), nil)
	expectArchived(t, "Epics.List", archivedByID(epicsList(t, client)), map[string]bool{
		"repoa/aged":    true,
		"repoa/younger": false,
	})
}

// TestAutoArchiveStoppedHeadlessAfterOneDay expects a stopped session unchanged one day to list archived, and one
// changed a second later to list unarchived. A waiting session unchanged 13 days lists archived: the seven-day floor
// ages it, though its state has no rule of its own.
func TestAutoArchiveStoppedHeadlessAfterOneDay(t *testing.T) {
	t.Parallel()
	storePath := archiveStorePath(t)
	now := epicsNow()
	applyHeadlessAt(t, storePath, now.Add(-archiveDay),
		store.EngineAdd{ID: archiveAgedID, Dir: "/work/aged", State: "stopped"})
	applyHeadlessAt(t, storePath, now.Add(-archiveDay+time.Second),
		store.EngineAdd{ID: archiveYoungerID, Dir: "/work/younger", State: "stopped"})
	applyHeadlessAt(t, storePath, now.Add(-13*archiveDay),
		store.EngineAdd{ID: archiveWaitingID, Dir: "/work/waiting", State: "waiting"})
	sessions := sessionsClientAt(t, store.NewReader(storePath), epicsNow)
	expectArchived(t, "ListHeadless", archivedByID(listHeadlessRows(t, sessions)), map[string]bool{
		archiveAgedID:    true,
		archiveYoungerID: false,
		archiveWaitingID: true,
	})
}

func TestAutoArchiveArchivedRowChangedPastMarkComesBack(t *testing.T) {
	t.Parallel()
	storePath := archiveStorePath(t)
	now := epicsNow()
	stored := &epicStoreFake{stored: []*enginev1.StoredEpic{
		storedEpicOf("repob/done", "released", epicsAgo(3*time.Hour)),
	}}
	archiveRowAt(t, storePath, archiveKindEpic, "repob/done", now.Add(-2*time.Hour))
	client := archiveEpicsClient(t, t.TempDir(), storePath, stored)
	expectArchived(t, "Epics.List before the change", archivedByID(epicsList(t, client)),
		map[string]bool{"repob/done": true})

	stored.stored[0] = storedEpicOf("repob/done", "released", epicsAgo(time.Hour))
	expectArchived(t, "Epics.List after the change", archivedByID(epicsList(t, client)),
		map[string]bool{"repob/done": false})
}

func TestAutoArchiveUnarchivedRowWaitsForChange(t *testing.T) {
	t.Parallel()
	storePath := archiveStorePath(t)
	now := epicsNow()
	stored := &epicStoreFake{stored: []*enginev1.StoredEpic{
		storedEpicOf("repob/done", "released", epicsAgo(10*archiveDay)),
	}}
	unarchiveRowAt(t, storePath, archiveKindEpic, "repob/done", now.Add(-9*archiveDay))
	client := archiveEpicsClient(t, t.TempDir(), storePath, stored)
	expectArchived(t, "Epics.List after the unarchive", archivedByID(epicsList(t, client)),
		map[string]bool{"repob/done": false})

	stored.stored[0] = storedEpicOf("repob/done", "released", epicsAgo(8*archiveDay))
	expectArchived(t, "Epics.List after the change", archivedByID(epicsList(t, client)),
		map[string]bool{"repob/done": true})
}

func TestArchiveRowsMarkedRowsStayListedAndReadArchived(t *testing.T) {
	t.Parallel()
	storePath := filepath.Join(t.TempDir(), "orrery.db")
	writeStore(t, storePath)
	now := epicsNow()
	applyHeadlessAt(t, storePath, now.Add(-time.Hour),
		store.EngineAdd{ID: archiveMarkedID, Dir: "/work/marked", State: "waiting"})
	stored := &epicStoreFake{stored: []*enginev1.StoredEpic{
		storedEpicOf("repoa/marked", "accepted", epicsAgo(2*time.Hour)),
	}}
	epics := archiveEpicsClient(t, t.TempDir(), storePath, stored)
	sessions := sessionsClientAt(t, store.NewReader(storePath), epicsNow)

	archiveRowAt(t, storePath, archiveKindSession, "orrery-1", now)
	archiveRowAt(t, storePath, archiveKindHeadless, archiveMarkedID, now)
	archiveRowAt(t, storePath, archiveKindEpic, "repoa/marked", now)
	expectArchived(t, "Epics.List", archivedByID(epicsList(t, epics)), map[string]bool{"repoa/marked": true})
	expectArchived(t, "Sessions.List", archivedByID(listSessionRows(t, sessions)), map[string]bool{
		"orrery-1": true,
		"orrery-2": false,
		"orrery-3": false,
	})
	expectArchived(t, "ListHeadless", archivedByID(listHeadlessRows(t, sessions)),
		map[string]bool{archiveMarkedID: true})

	unarchiveRowAt(t, storePath, archiveKindSession, "orrery-1", now)
	unarchiveRowAt(t, storePath, archiveKindHeadless, archiveMarkedID, now)
	unarchiveRowAt(t, storePath, archiveKindEpic, "repoa/marked", now)
	expectArchived(t, "Epics.List after the unarchive", archivedByID(epicsList(t, epics)),
		map[string]bool{"repoa/marked": false})
	expectArchived(t, "Sessions.List after the unarchive", archivedByID(listSessionRows(t, sessions)),
		map[string]bool{"orrery-1": false, "orrery-2": false, "orrery-3": false})
	expectArchived(t, "ListHeadless after the unarchive", archivedByID(listHeadlessRows(t, sessions)),
		map[string]bool{archiveMarkedID: false})
}

func TestArchiveRowsRecordOnlyEpicArchivedByID(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeHead(t, root, "repoa", "record", `{"outcome":"shipped"}`, epicsNow().Add(-time.Hour))
	writeHead(t, root, "repoa", "other", `{"outcome":"shipped"}`, epicsNow().Add(-time.Hour))
	storePath := archiveStorePath(t)
	archiveRowAt(t, storePath, archiveKindEpic, "repoa/record", epicsNow())
	client := archiveEpicsClient(t, root, storePath, nil)
	expectArchived(t, "Epics.List", archivedByID(epicsList(t, client)), map[string]bool{
		"repoa/record": true,
		"repoa/other":  false,
	})
}

func TestArchiveRowsMissingStoreAnswersUnavailable(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "absent.db")
	client := archiveEpicsClient(t, t.TempDir(), missing, nil)
	_, err := client.List(t.Context(), connect.NewRequest(&orreryv1.EpicsListRequest{}))
	if code := connect.CodeOf(err); code != connect.CodeUnavailable {
		t.Fatalf("Epics.List code = %v (%v), want %v", code, err, connect.CodeUnavailable)
	}
}
