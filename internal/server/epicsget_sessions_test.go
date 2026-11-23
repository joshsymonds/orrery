package server

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	orreryv1connect "github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/storetest"
)

// epicSessionsEpic is the epic the sessions tests are called on, and epicSessionsStart the second their first call is
// made at.
const epicSessionsEpic = "repoa/shared"

func epicSessionsStart() time.Time {
	return time.Unix(1_700_002_000, 0)
}

// openSessionsStore seeds a migrated store file in a fresh directory, opens it, and returns the store and the path of
// its file.
func openSessionsStore(t *testing.T) (*store.Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "orrery.db")
	storetest.Seed(t, path)
	s, err := store.Open(t.Context(), path, storetest.NoSync())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close store: %v", err)
		}
	})
	return s, path
}

// addSessionsRows writes the pane row orrery-1 of tmux server -L orrery, labeled worker one, and the headless engine
// row engine-session in /work/reviews.
func addSessionsRows(t *testing.T, s *store.Store) {
	t.Helper()
	engine := store.EngineAdd{ID: "engine-session", Dir: "/work/reviews", State: "idle"}
	if err := s.ApplyEngine(t.Context(), []store.EngineOp{engine}, epicSessionsStart()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}
	pane := store.Pane{ID: "orrery-1", Pane: "%1", Fields: store.Fields{Label: "worker one"}}
	if err := s.SyncPane(t.Context(), "-L orrery", pane, epicSessionsStart()); err != nil {
		t.Fatalf("SyncPane: %v", err)
	}
}

// recordSessionsCall records call of epicSessionsEpic by caller at at, and fails the test unless a row resolved.
func recordSessionsCall(t *testing.T, s *store.Store, caller store.EpicCaller, call string, at time.Time) {
	t.Helper()
	recorded, err := s.RecordEpicCall(t.Context(), epicSessionsEpic, caller, call, at)
	if err != nil {
		t.Fatalf("RecordEpicCall: %v", err)
	}
	if !recorded {
		t.Fatalf("RecordEpicCall by %+v = false, want true", caller)
	}
}

// epicSessionsClient serves Epics over an epics store that holds epicSessionsEpic, with reader as its store reader, and
// returns a generated Epics client.
func epicSessionsClient(t *testing.T, reader *store.Reader) orreryv1connect.EpicsClient {
	t.Helper()
	epics := &epicStoreFake{stored: []*enginev1.StoredEpic{
		storedEpicOf(epicSessionsEpic, "accepted", epicsAgo(time.Hour)),
	}}
	route, handler := newEpicsHandler(&epicsFake{}, epics, nil, nil, reader, 0, epicsNow, newRetryClock())
	mux := http.NewServeMux()
	mux.Handle(route, handler)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return orreryv1connect.NewEpicsClient(srv.Client(), srv.URL)
}

// assertEpicSessions reports each session of got that differs from the session at its index in want.
func assertEpicSessions(t *testing.T, got, want []*orreryv1.EpicSession) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("sessions = %d, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i].GetId() != want[i].GetId() {
			t.Errorf("sessions[%d] id = %q, want %q", i, got[i].GetId(), want[i].GetId())
		}
		if got[i].GetPane() != want[i].GetPane() {
			t.Errorf("sessions[%d] pane = %q, want %q", i, got[i].GetPane(), want[i].GetPane())
		}
		if got[i].GetLabel() != want[i].GetLabel() {
			t.Errorf("sessions[%d] label = %q, want %q", i, got[i].GetLabel(), want[i].GetLabel())
		}
		if got[i].GetCall() != want[i].GetCall() {
			t.Errorf("sessions[%d] call = %q, want %q", i, got[i].GetCall(), want[i].GetCall())
		}
		if got[i].GetAt() != want[i].GetAt() {
			t.Errorf("sessions[%d] at = %d, want %d", i, got[i].GetAt(), want[i].GetAt())
		}
		if got[i].GetCreator() != want[i].GetCreator() {
			t.Errorf("sessions[%d] creator = %t, want %t", i, got[i].GetCreator(), want[i].GetCreator())
		}
	}
}

func TestEpicsGetSessionsNewestCallFirst(t *testing.T) {
	t.Parallel()
	s, path := openSessionsStore(t)
	addSessionsRows(t, s)
	first := epicSessionsStart()
	second := first.Add(time.Minute)
	recordSessionsCall(t, s, store.EpicCaller{Session: "engine-session"}, "epic_get", first)
	recordSessionsCall(t, s, store.EpicCaller{Server: "-L orrery", Pane: "%1"}, "epic_step", second)

	got := epicsGet(t, epicSessionsClient(t, store.NewReader(path)), epicSessionsEpic).GetSessions()
	assertEpicSessions(t, got, []*orreryv1.EpicSession{
		{Id: "orrery-1", Pane: "%1", Label: "worker one", Call: "epic_step", At: second.Unix()},
		{Id: "engine-session", Label: "reviews", Call: "epic_get", At: first.Unix()},
	})
}

func TestEpicsGetSessionsLeavesOutRemovedSession(t *testing.T) {
	t.Parallel()
	s, path := openSessionsStore(t)
	addSessionsRows(t, s)
	first := epicSessionsStart()
	recordSessionsCall(t, s, store.EpicCaller{Session: "engine-session"}, "epic_get", first)
	recordSessionsCall(t, s, store.EpicCaller{Server: "-L orrery", Pane: "%1"}, "epic_step", first.Add(time.Minute))
	// The server now lists no panes, so orreryd removes the pane row orrery-1 and its call stays in the table.
	if err := s.SyncServer(t.Context(), "-L orrery", nil, first.Add(2*time.Minute)); err != nil {
		t.Fatalf("SyncServer: %v", err)
	}

	got := epicsGet(t, epicSessionsClient(t, store.NewReader(path)), epicSessionsEpic).GetSessions()
	assertEpicSessions(t, got, []*orreryv1.EpicSession{
		{Id: "engine-session", Label: "reviews", Call: "epic_get", At: first.Unix()},
	})
}

func TestEpicsGetSessionsSecondCallReplacesFirst(t *testing.T) {
	t.Parallel()
	s, path := openSessionsStore(t)
	addSessionsRows(t, s)
	first := epicSessionsStart()
	second := first.Add(time.Minute)
	pane := store.EpicCaller{Server: "-L orrery", Pane: "%1"}
	recordSessionsCall(t, s, pane, "epic_get", first)
	recordSessionsCall(t, s, pane, "epic_step", second)

	got := epicsGet(t, epicSessionsClient(t, store.NewReader(path)), epicSessionsEpic).GetSessions()
	assertEpicSessions(t, got, []*orreryv1.EpicSession{
		{Id: "orrery-1", Pane: "%1", Label: "worker one", Call: "epic_step", At: second.Unix()},
	})
}

func TestEpicsGetSessionsNilReaderListsNone(t *testing.T) {
	t.Parallel()
	got := epicsGet(t, epicSessionsClient(t, nil), epicSessionsEpic).GetSessions()
	assertEpicSessions(t, got, nil)
}

func TestEpicsGetSessionsFailedReadListsNone(t *testing.T) {
	t.Parallel()
	reader := store.NewReader(filepath.Join(t.TempDir(), "missing.db"))
	if _, err := reader.EpicSessions(t.Context(), epicSessionsEpic); err == nil {
		t.Fatal("EpicSessions of a missing file = nil error, want one")
	}

	got := epicsGet(t, epicSessionsClient(t, reader), epicSessionsEpic).GetSessions()
	assertEpicSessions(t, got, nil)
}

func TestEpicsGetSessionsCreatorConversationMarksCreator(t *testing.T) {
	t.Parallel()
	s, path := openSessionsStore(t)
	addSessionsRows(t, s)
	first := epicSessionsStart()
	second := first.Add(time.Minute)
	recordSessionsCall(t, s, store.EpicCaller{Server: "-L orrery", Pane: "%1"}, "epic_create", first)
	recordSessionsCall(t, s, store.EpicCaller{Session: "engine-session"}, "epic_get", second)

	got := epicsGet(t, epicSessionsClient(t, store.NewReader(path)), epicSessionsEpic).GetSessions()
	assertEpicSessions(t, got, []*orreryv1.EpicSession{
		{Id: "engine-session", Label: "reviews", Call: "epic_get", At: second.Unix()},
		{Id: "orrery-1", Pane: "%1", Label: "worker one", Call: "epic_create", At: first.Unix(), Creator: true},
	})
}
