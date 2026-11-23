package store_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/storetest"
)

// epicCallEpic is the epic the epic session tests record calls of, and epicCallOther a second epic whose calls the
// listing of the first must leave out.
const (
	epicCallEpic  = "orrery-5e6f7a8/epic-calls"
	epicCallOther = "orrery-5e6f7a8/epic-other"
)

// epicCallStart is the second the epic session tests make their first call at.
func epicCallStart() time.Time {
	return time.Unix(1700002000, 0)
}

// openEpicCallStore opens a migrated store file and returns the store and the path of its file.
func openEpicCallStore(t *testing.T) (*store.Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "orrery.db")
	storetest.Seed(t, path)
	s, err := store.Open(context.Background(), path, store.NoSync())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return s, path
}

// epicSessionRowCount returns the number of rows of epic_sessions in the store file at path.
func epicSessionRowCount(t *testing.T, path string) int {
	t.Helper()
	raw, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open raw handle: %v", err)
	}
	defer func() {
		if err := raw.Close(); err != nil {
			t.Errorf("close raw handle: %v", err)
		}
	}()
	var count int
	if err := raw.QueryRow(`SELECT COUNT(*) FROM epic_sessions`).Scan(&count); err != nil {
		t.Fatalf("count epic_sessions: %v", err)
	}
	return count
}

func assertEpicSessionRows(t *testing.T, got, want []store.EpicSessionRow) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("EpicSessions = %+v, want %+v", got, want)
	}
}

func TestEpicSessionPaneCallerResolvesToItsRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, path := openEpicCallStore(t)
	pane := store.Pane{ID: "orrery-1", Pane: "%1", Fields: store.Fields{Label: "worker one"}}
	if err := s.SyncPane(ctx, "-L orrery", pane, epicCallStart()); err != nil {
		t.Fatalf("SyncPane: %v", err)
	}

	caller := store.EpicCaller{Server: "-L orrery", Pane: "%1"}
	recorded, err := s.RecordEpicCall(ctx, epicCallEpic, caller, "call one", epicCallStart())
	if err != nil {
		t.Fatalf("RecordEpicCall: %v", err)
	}
	if !recorded {
		t.Fatalf("RecordEpicCall by pane %%1 = false, want true for the pane's row")
	}

	got, err := store.NewReader(path).EpicSessions(ctx, epicCallEpic)
	if err != nil {
		t.Fatalf("EpicSessions: %v", err)
	}
	assertEpicSessionRows(t, got, []store.EpicSessionRow{{
		Session: "orrery-1", Server: "-L orrery", Pane: "%1", Label: "worker one", Call: "call one",
		At: epicCallStart().Unix(),
	}})
}

func TestEpicSessionCallerWithNoRowRecordsNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, path := openEpicCallStore(t)
	// The engine row has an empty server and pane, so a caller naming neither must not resolve to it.
	engine := store.EngineAdd{ID: "engine-1", Dir: "/work/engine", State: "idle"}
	if err := s.ApplyEngine(ctx, []store.EngineOp{engine}, epicCallStart()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}
	if err := s.SyncPane(ctx, "-L orrery", store.Pane{ID: "orrery-1", Pane: "%1"}, epicCallStart()); err != nil {
		t.Fatalf("SyncPane: %v", err)
	}

	callers := map[string]store.EpicCaller{
		"a pane with no row":          {Server: "-L orrery", Pane: "%9"},
		"a pane on another tmux":      {Server: "-L other", Pane: "%1"},
		"no session, server, or pane": {},
		"a session id with no row":    {Session: "orrery-9"},
	}
	for name, caller := range callers {
		recorded, err := s.RecordEpicCall(ctx, epicCallEpic, caller, "call one", epicCallStart())
		if err != nil {
			t.Fatalf("RecordEpicCall by %s: %v", name, err)
		}
		if recorded {
			t.Errorf("RecordEpicCall by %s = true, want false", name)
		}
	}

	got, err := store.NewReader(path).EpicSessions(ctx, epicCallEpic)
	if err != nil {
		t.Fatalf("EpicSessions: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("EpicSessions = %+v, want none", got)
	}
	if n := epicSessionRowCount(t, path); n != 0 {
		t.Errorf("epic_sessions rows = %d, want none", n)
	}
}

func TestEpicSessionIDCallerResolvesToItsRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, path := openEpicCallStore(t)
	engine := store.EngineAdd{ID: "engine-1", Dir: "/work/engine", State: "idle"}
	if err := s.ApplyEngine(ctx, []store.EngineOp{engine}, epicCallStart()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}

	recorded, err := s.RecordEpicCall(ctx, epicCallEpic, store.EpicCaller{Session: "engine-1"}, "call one",
		epicCallStart())
	if err != nil {
		t.Fatalf("RecordEpicCall: %v", err)
	}
	if !recorded {
		t.Fatalf("RecordEpicCall by session engine-1 = false, want true for the session's row")
	}

	got, err := store.NewReader(path).EpicSessions(ctx, epicCallEpic)
	if err != nil {
		t.Fatalf("EpicSessions: %v", err)
	}
	assertEpicSessionRows(t, got, []store.EpicSessionRow{{
		Session: "engine-1", Kind: "engine", Dir: "/work/engine", Call: "call one", At: epicCallStart().Unix(),
	}})
}

func TestEpicSessionSecondCallReplacesFirst(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, path := openEpicCallStore(t)
	if err := s.SyncPane(ctx, "-L orrery", store.Pane{ID: "orrery-1", Pane: "%1"}, epicCallStart()); err != nil {
		t.Fatalf("SyncPane: %v", err)
	}

	caller := store.EpicCaller{Server: "-L orrery", Pane: "%1"}
	if _, err := s.RecordEpicCall(ctx, epicCallEpic, caller, "call one", epicCallStart()); err != nil {
		t.Fatalf("first RecordEpicCall: %v", err)
	}
	later := time.Unix(epicCallStart().Unix()+60, 0)
	if _, err := s.RecordEpicCall(ctx, epicCallEpic, caller, "call two", later); err != nil {
		t.Fatalf("second RecordEpicCall: %v", err)
	}

	got, err := store.NewReader(path).EpicSessions(ctx, epicCallEpic)
	if err != nil {
		t.Fatalf("EpicSessions: %v", err)
	}
	assertEpicSessionRows(t, got, []store.EpicSessionRow{{
		Session: "orrery-1", Server: "-L orrery", Pane: "%1", Call: "call two", At: later.Unix(),
	}})
}

func TestEpicSessionsListNewestFirstAndOnlyThisEpic(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, path := openEpicCallStore(t)
	start := epicCallStart()
	later := time.Unix(start.Unix()+60, 0)
	panes := []store.Pane{
		{ID: "orrery-a", Pane: "%1"}, {ID: "orrery-b", Pane: "%2"}, {ID: "orrery-c", Pane: "%3"},
		{ID: "orrery-d", Pane: "%4"},
	}
	for _, pane := range panes {
		if err := s.SyncPane(ctx, "-L orrery", pane, start); err != nil {
			t.Fatalf("SyncPane %s: %v", pane.ID, err)
		}
	}

	calls := []struct {
		epic, session, call string
		at                  time.Time
	}{
		{epicCallEpic, "orrery-a", "call a", start},
		{epicCallEpic, "orrery-b", "call b", later},
		{epicCallEpic, "orrery-c", "call c", later},
		{epicCallOther, "orrery-d", "call d", time.Unix(start.Unix()+120, 0)},
	}
	for _, c := range calls {
		caller := store.EpicCaller{Session: c.session}
		if _, err := s.RecordEpicCall(ctx, c.epic, caller, c.call, c.at); err != nil {
			t.Fatalf("RecordEpicCall %s by %s: %v", c.call, c.session, err)
		}
	}

	got, err := store.NewReader(path).EpicSessions(ctx, epicCallEpic)
	if err != nil {
		t.Fatalf("EpicSessions: %v", err)
	}
	// orrery-b and orrery-c share a time, so they list by id; orrery-d called the other epic and is left out.
	assertEpicSessionRows(t, got, []store.EpicSessionRow{
		{Session: "orrery-b", Server: "-L orrery", Pane: "%2", Call: "call b", At: later.Unix()},
		{Session: "orrery-c", Server: "-L orrery", Pane: "%3", Call: "call c", At: later.Unix()},
		{Session: "orrery-a", Server: "-L orrery", Pane: "%1", Call: "call a", At: start.Unix()},
	})
}

func TestEpicSessionsLeaveOutRemovedSessions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, path := openEpicCallStore(t)
	start := epicCallStart()
	for _, pane := range []store.Pane{{ID: "orrery-1", Pane: "%1"}, {ID: "orrery-2", Pane: "%2"}} {
		if err := s.SyncPane(ctx, "-L orrery", pane, start); err != nil {
			t.Fatalf("SyncPane %s: %v", pane.ID, err)
		}
	}
	first := store.EpicCaller{Session: "orrery-1"}
	if _, err := s.RecordEpicCall(ctx, epicCallEpic, first, "call one", start); err != nil {
		t.Fatalf("RecordEpicCall by orrery-1: %v", err)
	}
	later := time.Unix(start.Unix()+60, 0)
	second := store.EpicCaller{Session: "orrery-2"}
	if _, err := s.RecordEpicCall(ctx, epicCallEpic, second, "call two", later); err != nil {
		t.Fatalf("RecordEpicCall by orrery-2: %v", err)
	}

	// orrery-1's pane is gone from the server, so its sessions row is removed and its call must not be listed.
	gone := time.Unix(start.Unix()+120, 0)
	if err := s.SyncServer(ctx, "-L orrery", []store.Pane{{ID: "orrery-2", Pane: "%2"}}, gone); err != nil {
		t.Fatalf("SyncServer: %v", err)
	}

	got, err := store.NewReader(path).EpicSessions(ctx, epicCallEpic)
	if err != nil {
		t.Fatalf("EpicSessions: %v", err)
	}
	assertEpicSessionRows(t, got, []store.EpicSessionRow{
		{Session: "orrery-2", Server: "-L orrery", Pane: "%2", Call: "call two", At: later.Unix()},
	})
}

func TestEpicSessionCreatorConversationMarksEpicCreateCaller(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, path := openEpicCallStore(t)
	start := epicCallStart()
	later := time.Unix(start.Unix()+60, 0)
	for _, pane := range []store.Pane{{ID: "orrery-1", Pane: "%1"}, {ID: "orrery-2", Pane: "%2"}} {
		if err := s.SyncPane(ctx, "-L orrery", pane, start); err != nil {
			t.Fatalf("SyncPane %s: %v", pane.ID, err)
		}
	}
	creator := store.EpicCaller{Session: "orrery-1"}
	if _, err := s.RecordEpicCall(ctx, epicCallEpic, creator, "epic_create", start); err != nil {
		t.Fatalf("RecordEpicCall epic_create: %v", err)
	}
	caller := store.EpicCaller{Session: "orrery-2"}
	if _, err := s.RecordEpicCall(ctx, epicCallEpic, caller, "epic_get", later); err != nil {
		t.Fatalf("RecordEpicCall epic_get: %v", err)
	}

	got, err := store.NewReader(path).EpicSessions(ctx, epicCallEpic)
	if err != nil {
		t.Fatalf("EpicSessions: %v", err)
	}
	assertEpicSessionRows(t, got, []store.EpicSessionRow{
		{Session: "orrery-2", Server: "-L orrery", Pane: "%2", Call: "epic_get", At: later.Unix()},
		{Session: "orrery-1", Server: "-L orrery", Pane: "%1", Call: "epic_create", At: start.Unix(), Creator: true},
	})
}
