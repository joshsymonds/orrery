package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

func openSyncStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "orrery.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return s
}

func sessionsOf(t *testing.T, s *Store) []sqlcgen.Session {
	t.Helper()
	rows, err := sqlcgen.New(s.db).ListSessions(context.Background())
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	return rows
}

func eventsOf(t *testing.T, s *Store) []sqlcgen.Event {
	t.Helper()
	events, err := sqlcgen.New(s.db).ListEvents(context.Background())
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	return events
}

func assertEvents(t *testing.T, s *Store, want []sqlcgen.Event) {
	t.Helper()
	got := eventsOf(t, s)
	if len(got) != len(want) {
		t.Fatalf("events: got %d, want %d\n got: %+v\nwant: %+v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("event %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

func assertSessions(t *testing.T, s *Store, want []sqlcgen.Session) {
	t.Helper()
	got := sessionsOf(t, s)
	if len(got) != len(want) {
		t.Fatalf("sessions: got %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("session %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

func setSessionField(t *testing.T, row *sqlcgen.Session, name, value string) {
	t.Helper()
	switch name {
	case "state":
		row.State = value
	case "changed":
		row.Changed = value
	case "seen":
		row.Seen = value
	case "question":
		row.Question = value
	case "summary":
		row.Summary = value
	case "label":
		row.Label = value
	case "context":
		row.Context = value
	case "tool":
		row.Tool = value
	case "claude_session":
		row.ClaudeSession = value
	case "transcript":
		row.Transcript = value
	default:
		t.Fatalf("unknown field %q in session_changed", name)
	}
}

// replaySessions rebuilds the sessions table from the events in seq order, using only the payloads.
func replaySessions(t *testing.T, events []sqlcgen.Event) map[string]sqlcgen.Session {
	t.Helper()
	rebuilt := map[string]sqlcgen.Session{}
	for _, e := range events {
		switch e.Kind {
		case EventSessionAdded:
			var p struct {
				Server string `json:"server"`
				Pane   string `json:"pane"`
			}
			if err := json.Unmarshal([]byte(e.Payload), &p); err != nil {
				t.Fatalf("session_added payload %q: %v", e.Payload, err)
			}
			rebuilt[e.SessionID] = sqlcgen.Session{ID: e.SessionID, Server: p.Server, Pane: p.Pane}
		case EventSessionChanged:
			var p struct {
				Fields map[string]string `json:"fields"`
			}
			if err := json.Unmarshal([]byte(e.Payload), &p); err != nil {
				t.Fatalf("session_changed payload %q: %v", e.Payload, err)
			}
			row, ok := rebuilt[e.SessionID]
			if !ok {
				t.Fatalf("session_changed for unknown session %q", e.SessionID)
			}
			for name, value := range p.Fields {
				setSessionField(t, &row, name, value)
			}
			rebuilt[e.SessionID] = row
		case EventSessionRemoved:
			if _, ok := rebuilt[e.SessionID]; !ok {
				t.Fatalf("session_removed for unknown session %q", e.SessionID)
			}
			delete(rebuilt, e.SessionID)
		default:
			t.Fatalf("unknown event kind %q", e.Kind)
		}
	}
	return rebuilt
}

func TestSyncPaneAddsRowAndEventsOnlyForChanges(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)
	now := time.Unix(1_700_000_000, 0)

	pane := Pane{ID: "orrery-a", Pane: "%1", Fields: Fields{State: "working", Label: "api"}}
	if err := s.SyncPane(ctx, "-L demo", pane, now); err != nil {
		t.Fatalf("SyncPane new pane: %v", err)
	}
	firstEvents := make([]sqlcgen.Event, 0, 3)
	firstEvents = append(firstEvents,
		sqlcgen.Event{Seq: 1, SessionID: "orrery-a", Kind: EventSessionAdded, At: now.Unix(),
			Payload: `{"server":"-L demo","pane":"%1"}`},
		sqlcgen.Event{Seq: 2, SessionID: "orrery-a", Kind: EventSessionChanged, At: now.Unix(),
			Payload: `{"fields":{"label":"api","state":"working"}}`},
	)
	assertEvents(t, s, firstEvents)
	assertSessions(t, s, []sqlcgen.Session{
		{ID: "orrery-a", Server: "-L demo", Pane: "%1", State: "working", Label: "api"},
	})

	if err := s.SyncPane(ctx, "-L demo", pane, now.Add(time.Second)); err != nil {
		t.Fatalf("SyncPane same snapshot: %v", err)
	}
	assertEvents(t, s, firstEvents)

	pane.Fields = Fields{State: "waiting", Question: "deploy?"}
	if err := s.SyncPane(ctx, "-L demo", pane, now.Add(2*time.Second)); err != nil {
		t.Fatalf("SyncPane changed snapshot: %v", err)
	}
	assertEvents(t, s, append(firstEvents, sqlcgen.Event{
		Seq: 3, SessionID: "orrery-a", Kind: EventSessionChanged, At: now.Add(2 * time.Second).Unix(),
		Payload: `{"fields":{"label":"","question":"deploy?","state":"waiting"}}`,
	}))
	assertSessions(t, s, []sqlcgen.Session{
		{ID: "orrery-a", Server: "-L demo", Pane: "%1", State: "waiting", Question: "deploy?"},
	})
}

func TestSyncPaneNewRowWithNoFieldsAppendsOnlyAdded(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)
	now := time.Unix(1_700_000_000, 0)

	if err := s.SyncPane(ctx, "", Pane{ID: "orrery-e", Pane: "%7"}, now); err != nil {
		t.Fatalf("SyncPane: %v", err)
	}
	assertEvents(t, s, []sqlcgen.Event{
		{Seq: 1, SessionID: "orrery-e", Kind: EventSessionAdded, At: now.Unix(), Payload: `{"server":"","pane":"%7"}`},
	})
}

func TestReplayRebuildsSessionsAfterMixedSequence(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)
	t0 := time.Unix(1_700_000_000, 0)

	if err := s.SyncServer(ctx, "-L a", []Pane{
		{ID: "orrery-1", Pane: "%1", Fields: Fields{State: "working"}},
		{ID: "orrery-2", Pane: "%2", Fields: Fields{Label: "web", Seen: "yes"}},
	}, t0); err != nil {
		t.Fatalf("SyncServer -L a: %v", err)
	}
	if err := s.SyncPane(ctx, "-L a", Pane{ID: "orrery-1", Pane: "%1",
		Fields: Fields{State: "waiting", Summary: "done"}}, t0.Add(time.Second)); err != nil {
		t.Fatalf("SyncPane: %v", err)
	}
	if err := s.SyncServer(ctx, "-L b", []Pane{
		{ID: "orrery-9", Pane: "%9", Fields: Fields{Tool: "bash", ClaudeSession: "abc", Transcript: "/t"}},
	}, t0.Add(2*time.Second)); err != nil {
		t.Fatalf("SyncServer -L b: %v", err)
	}
	if err := s.SyncServer(ctx, "-L a", []Pane{
		{ID: "orrery-1", Pane: "%1", Fields: Fields{State: "waiting", Context: "ctx"}},
	}, t0.Add(3*time.Second)); err != nil {
		t.Fatalf("SyncServer -L a removing orrery-2: %v", err)
	}
	if err := s.SyncPane(ctx, "-L a", Pane{ID: "orrery-3", Pane: "%3"}, t0.Add(4*time.Second)); err != nil {
		t.Fatalf("SyncPane empty: %v", err)
	}
	if err := s.SyncPane(ctx, "-L a", Pane{ID: "orrery-3", Pane: "%3",
		Fields: Fields{Label: "x"}}, t0.Add(5*time.Second)); err != nil {
		t.Fatalf("SyncPane label: %v", err)
	}
	if err := s.SyncServer(ctx, "-L b", nil, t0.Add(6*time.Second)); err != nil {
		t.Fatalf("SyncServer -L b removing orrery-9: %v", err)
	}

	rows := sessionsOf(t, s)
	if len(rows) != 2 {
		t.Fatalf("sessions after sequence: got %+v, want 2 rows", rows)
	}
	rebuilt := replaySessions(t, eventsOf(t, s))
	if len(rebuilt) != len(rows) {
		t.Fatalf("replay has %d rows, sessions has %d", len(rebuilt), len(rows))
	}
	for _, row := range rows {
		if rebuilt[row.ID] != row {
			t.Fatalf("replay of %s: got %+v, sessions has %+v", row.ID, rebuilt[row.ID], row)
		}
	}
}

func TestSyncServerRemovesOnlyAbsentRowsOfItsServer(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)
	t0 := time.Unix(1_700_000_000, 0)

	if err := s.SyncServer(ctx, "-L a", []Pane{
		{ID: "orrery-1", Pane: "%1", Fields: Fields{State: "working"}},
		{ID: "orrery-2", Pane: "%2"},
	}, t0); err != nil {
		t.Fatalf("SyncServer -L a: %v", err)
	}
	if err := s.SyncServer(ctx, "-L b", []Pane{
		{ID: "orrery-9", Pane: "%9", Fields: Fields{Label: "q"}},
	}, t0.Add(time.Second)); err != nil {
		t.Fatalf("SyncServer -L b: %v", err)
	}
	if err := s.SyncServer(ctx, "-L a", []Pane{
		{ID: "orrery-1", Pane: "%1", Fields: Fields{State: "working"}},
	}, t0.Add(2*time.Second)); err != nil {
		t.Fatalf("SyncServer -L a without orrery-2: %v", err)
	}

	assertSessions(t, s, []sqlcgen.Session{
		{ID: "orrery-1", Server: "-L a", Pane: "%1", State: "working"},
		{ID: "orrery-9", Server: "-L b", Pane: "%9", Label: "q"},
	})
	events := eventsOf(t, s)
	if len(events) != 6 {
		t.Fatalf("events: got %d, want 6: %+v", len(events), events)
	}
	last := events[len(events)-1]
	want := sqlcgen.Event{Seq: 6, SessionID: "orrery-2", Kind: EventSessionRemoved,
		At: t0.Add(2 * time.Second).Unix(), Payload: "{}"}
	if last != want {
		t.Fatalf("last event: got %+v, want %+v", last, want)
	}
}

func TestSyncServerLeavesEngineRowsOfTheDefaultServer(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)
	t0 := time.Unix(1_700_000_000, 0)

	if err := sqlcgen.New(s.db).InsertEngineSession(ctx, sqlcgen.InsertEngineSessionParams{
		ID: "engine-1", Dir: "/work", State: "working", Changed: "t0",
	}); err != nil {
		t.Fatalf("InsertEngineSession: %v", err)
	}
	// Every engine row has its engine_added event; without one the tidy's 14-day removal would take the row.
	if err := sqlcgen.New(s.db).InsertEvent(ctx, sqlcgen.InsertEventParams{
		SessionID: "engine-1", Kind: EventEngineAdded, At: t0.Unix(), Payload: `{"dir":"/work","state":"working"}`,
	}); err != nil {
		t.Fatalf("InsertEvent: %v", err)
	}
	if err := s.SyncServer(ctx, "", nil, t0.Add(time.Second)); err != nil {
		t.Fatalf("SyncServer with no panes: %v", err)
	}
	if err := s.SyncServer(ctx, "", []Pane{
		{ID: "orrery-9", Pane: "%9"},
	}, t0.Add(2*time.Second)); err != nil {
		t.Fatalf("SyncServer with an unrelated pane: %v", err)
	}

	row, err := sqlcgen.New(s.db).GetEngineSession(ctx, "engine-1")
	if err != nil {
		t.Fatalf("GetEngineSession after SyncServer: %v", err)
	}
	want := sqlcgen.Session{ID: "engine-1", Dir: "/work", State: "working", Changed: "t0", Kind: "engine"}
	if row != want {
		t.Fatalf("engine row: got %+v, want %+v", row, want)
	}
	for _, event := range eventsOf(t, s) {
		if event.Kind == EventSessionRemoved {
			t.Fatalf("SyncServer appended %s for a row it does not own: %+v", EventSessionRemoved, event)
		}
	}
}

func TestSyncServerPrunesEventsOlderThanRetention(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)
	now := time.Unix(1_700_000_000, 0)
	old := now.Add(-15 * 24 * time.Hour)
	edge := now.Add(-EventRetention)
	recent := now.Add(-13 * 24 * time.Hour)

	if err := s.SyncPane(ctx, "-L a", Pane{ID: "orrery-old", Pane: "%1",
		Fields: Fields{State: "working"}}, old); err != nil {
		t.Fatalf("SyncPane old: %v", err)
	}
	if err := s.SyncPane(ctx, "-L a", Pane{ID: "orrery-edge", Pane: "%2",
		Fields: Fields{State: "working"}}, edge); err != nil {
		t.Fatalf("SyncPane edge: %v", err)
	}
	if err := s.SyncPane(ctx, "-L a", Pane{ID: "orrery-new", Pane: "%3",
		Fields: Fields{State: "working"}}, recent); err != nil {
		t.Fatalf("SyncPane recent: %v", err)
	}
	if err := s.SyncServer(ctx, "-L a", []Pane{
		{ID: "orrery-edge", Pane: "%2", Fields: Fields{State: "working"}},
		{ID: "orrery-new", Pane: "%3", Fields: Fields{State: "working"}},
	}, now); err != nil {
		t.Fatalf("SyncServer: %v", err)
	}

	events := eventsOf(t, s)
	wantAts := []int64{edge.Unix(), edge.Unix(), recent.Unix(), recent.Unix(), now.Unix()}
	if len(events) != len(wantAts) {
		t.Fatalf("events: got %+v, want %d events", events, len(wantAts))
	}
	for i, at := range wantAts {
		if events[i].At != at {
			t.Fatalf("event %d at %d, want %d: %+v", i, events[i].At, at, events[i])
		}
	}
	if events[len(events)-1].Kind != EventSessionRemoved || events[len(events)-1].SessionID != "orrery-old" {
		t.Fatalf("last event: got %+v, want session_removed for orrery-old", events[len(events)-1])
	}
}

func TestSyncPaneReturnsErrorWhileStoreIsWriteLocked(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	other, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open second connection: %v", err)
	}
	t.Cleanup(func() {
		if err := other.Close(); err != nil {
			t.Errorf("close second connection: %v", err)
		}
	})
	conn, err := other.Conn(ctx)
	if err != nil {
		t.Fatalf("second connection: %v", err)
	}
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatalf("take write lock: %v", err)
	}

	start := time.Now()
	err = s.SyncPane(ctx, "-L a", Pane{ID: "orrery-1", Pane: "%1", Fields: Fields{State: "working"}}, time.Unix(1, 0))
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("SyncPane while write-locked: want error, got nil")
	}
	if elapsed >= time.Second {
		t.Fatalf("SyncPane while write-locked took %v, want under 1s", elapsed)
	}
	if rows := sessionsOf(t, s); len(rows) != 0 {
		t.Fatalf("sessions written while locked: %+v", rows)
	}
	if events := eventsOf(t, s); len(events) != 0 {
		t.Fatalf("events written while locked: %+v", events)
	}

	if _, err := conn.ExecContext(ctx, "ROLLBACK"); err != nil {
		t.Fatalf("release write lock: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("close second connection: %v", err)
	}
	if err := s.SyncPane(ctx, "-L a", Pane{ID: "orrery-1", Pane: "%1", Fields: Fields{State: "working"}},
		time.Unix(1, 0)); err != nil {
		t.Fatalf("SyncPane after lock released: %v", err)
	}
	assertSessions(t, s, []sqlcgen.Session{{ID: "orrery-1", Server: "-L a", Pane: "%1", State: "working"}})
}
