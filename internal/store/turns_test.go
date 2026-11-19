package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// turnPane is a pane snapshot for session id in tmux pane "%" + id, carrying only the fields a test sets.
func turnPane(id string, fields Fields) Pane {
	return Pane{ID: id, Pane: "%" + id, Fields: fields}
}

func turnTime(sec int64) time.Time {
	return time.Unix(1_700_000_000+sec, 0)
}

// lastTurnEndsOf reads the turn ends of the store at path, failing the test on error.
func lastTurnEndsOf(t *testing.T, path string) map[string]int64 {
	t.Helper()
	ends, err := NewReader(path).LastTurnEnds(context.Background())
	if err != nil {
		t.Fatalf("LastTurnEnds: %v", err)
	}
	return ends
}

func TestLastTurnEndsAnswersTheNewestDoneAfterAFlip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openWriterAt(t, path)

	// The first done, then working, then done again. The label change last leaves state at done, so its event
	// does not set state and must not move the answer off the second done.
	steps := []struct {
		sec  int64
		pane Pane
	}{
		{0, turnPane("orrery-1", Fields{State: "done"})},
		{10, turnPane("orrery-1", Fields{State: "working"})},
		{20, turnPane("orrery-1", Fields{State: "done"})},
		{30, turnPane("orrery-1", Fields{State: "done", Label: "renamed"})},
	}
	for _, step := range steps {
		if err := s.SyncPane(ctx, "-L a", step.pane, turnTime(step.sec)); err != nil {
			t.Fatalf("SyncPane at %d: %v", step.sec, err)
		}
	}

	want := map[string]int64{"orrery-1": turnTime(20).Unix()}
	if got := lastTurnEndsOf(t, path); !reflect.DeepEqual(got, want) {
		t.Fatalf("turn ends: got %v, want %v", got, want)
	}
}

func TestLastTurnEndsOmitsSessionsThatNeverWentDone(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openWriterAt(t, path)

	if err := s.SyncPane(ctx, "-L a", turnPane("orrery-1", Fields{State: "working"}), turnTime(0)); err != nil {
		t.Fatalf("SyncPane: %v", err)
	}
	if err := s.SyncPane(
		ctx,
		"-L a",
		turnPane("orrery-1", Fields{State: "idle", Label: "x"}),
		turnTime(5),
	); err != nil {
		t.Fatalf("SyncPane: %v", err)
	}

	if got := lastTurnEndsOf(t, path); len(got) != 0 {
		t.Fatalf("turn ends: got %v, want none", got)
	}
}

func TestLastTurnEndsAnswersEachSessionIndependently(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openWriterAt(t, path)

	steps := []struct {
		sec  int64
		pane Pane
	}{
		{0, turnPane("orrery-1", Fields{State: "done"})},
		{2, turnPane("orrery-2", Fields{State: "working"})},
		{5, turnPane("orrery-2", Fields{State: "done"})},
		{8, turnPane("orrery-2", Fields{State: "working"})},
	}
	for _, step := range steps {
		if err := s.SyncPane(ctx, "-L a", step.pane, turnTime(step.sec)); err != nil {
			t.Fatalf("SyncPane at %d: %v", step.sec, err)
		}
	}

	want := map[string]int64{
		"orrery-1": turnTime(0).Unix(),
		"orrery-2": turnTime(5).Unix(),
	}
	if got := lastTurnEndsOf(t, path); !reflect.DeepEqual(got, want) {
		t.Fatalf("turn ends: got %v, want %v", got, want)
	}
}

func TestLastTurnEndsReturnsUnavailableWhenStoreIsMissing(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "absent")
	path := filepath.Join(dir, "orrery.db")

	_, err := NewReader(path).LastTurnEnds(context.Background())
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("LastTurnEnds of missing store: got %v, want ErrUnavailable", err)
	}
	if _, statErr := os.Stat(dir); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("missing store's directory was created or unreadable: %v", statErr)
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("missing store file was created or unreadable: %v", statErr)
	}
}

// lastTurnEndsSQL is the LastTurnEnds query of queries.sql, run under EXPLAIN QUERY PLAN.
const lastTurnEndsSQL = `SELECT events.session_id, CAST(MAX(events.at) AS INTEGER) AS at FROM events
WHERE events.kind = 'session_changed' AND json_extract(events.payload, '$.fields.state') = 'done'
GROUP BY events.session_id
ORDER BY events.session_id`

// queryPlanOf returns the detail column of each EXPLAIN QUERY PLAN row of query.
func queryPlanOf(t *testing.T, db *sql.DB, query string) []string {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), `EXPLAIN QUERY PLAN `+query)
	if err != nil {
		t.Fatalf("EXPLAIN QUERY PLAN: %v", err)
	}
	defer rows.Close()
	var details []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatalf("scan query plan: %v", err)
		}
		details = append(details, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read query plan: %v", err)
	}
	return details
}

// indexExists reports whether the store schema holds an index named name.
func indexExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var count int
	if err := db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?`, name).Scan(&count); err != nil {
		t.Fatalf("read index %s: %v", name, err)
	}
	return count == 1
}

func TestLastTurnEndsReadsThroughTurnEndIndex(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openWriterAt(t, path)

	// orrery-1 is done at 0 and 20, and its label change at 30 leaves state at done, so its newest done is 20.
	// orrery-2 is done at 5 and working at 8. orrery-3 never goes done.
	steps := []struct {
		sec  int64
		pane Pane
	}{
		{0, turnPane("orrery-1", Fields{State: "done"})},
		{5, turnPane("orrery-2", Fields{State: "done"})},
		{8, turnPane("orrery-2", Fields{State: "working"})},
		{10, turnPane("orrery-1", Fields{State: "working"})},
		{12, turnPane("orrery-3", Fields{State: "working"})},
		{20, turnPane("orrery-1", Fields{State: "done"})},
		{30, turnPane("orrery-1", Fields{State: "done", Label: "renamed"})},
	}
	for _, step := range steps {
		if err := s.SyncPane(ctx, "-L a", step.pane, turnTime(step.sec)); err != nil {
			t.Fatalf("SyncPane at %d: %v", step.sec, err)
		}
	}
	// An engine event whose payload carries state done is not a session_changed event, so it must not count.
	if _, err := s.fixtureExec(ctx,
		`INSERT INTO events (session_id, kind, at, payload) VALUES (?, ?, ?, ?)`,
		"engine-1", EventEngineChanged, turnTime(40).Unix(), `{"fields":{"state":"done"}}`); err != nil {
		t.Fatalf("insert engine event: %v", err)
	}

	want := map[string]int64{
		"orrery-1": turnTime(20).Unix(),
		"orrery-2": turnTime(5).Unix(),
	}
	if got := lastTurnEndsOf(t, path); !reflect.DeepEqual(got, want) {
		t.Fatalf("turn ends: got %v, want %v", got, want)
	}
	plan := strings.Join(queryPlanOf(t, s.db.DB, lastTurnEndsSQL), "\n")
	if !strings.Contains(plan, "events_turn_end") {
		t.Fatalf("LastTurnEnds plan: got %q, want it to name events_turn_end", plan)
	}
}

func TestLastTurnEndsAppliesTurnEndIndexToStoreWithEvents(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")

	fixture := openStoreAtVersion(t, path, 23)
	events := []struct {
		id, kind, payload string
		sec               int64
	}{
		{"orrery-1", "session_changed", `{"fields":{"state":"done"}}`, 0},
		{"orrery-1", "session_changed", `{"fields":{"state":"working"}}`, 10},
		{"orrery-1", "session_changed", `{"fields":{"state":"done"}}`, 20},
		{"orrery-2", "session_changed", `{"fields":{"label":"x"}}`, 25},
		{"engine-1", EventEngineChanged, `{"fields":{"state":"done"}}`, 30},
	}
	for _, e := range events {
		if _, err := fixture.ExecContext(ctx,
			`INSERT INTO events (session_id, kind, at, payload) VALUES (?, ?, ?, ?)`,
			e.id, e.kind, turnTime(e.sec).Unix(), e.payload); err != nil {
			fixture.Close()
			t.Fatalf("seed fixture event: %v", err)
		}
	}
	if err := fixture.Close(); err != nil {
		t.Fatalf("close fixture: %v", err)
	}

	s, err := Open(ctx, path, noSync())
	if err != nil {
		t.Fatalf("Open at version 23: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close writer: %v", err)
		}
	})
	if !indexExists(t, s.db.DB, "events_turn_end") {
		t.Fatalf("events_turn_end after Open at version 23: absent, want present")
	}

	want := map[string]int64{"orrery-1": turnTime(20).Unix()}
	if got := lastTurnEndsOf(t, path); !reflect.DeepEqual(got, want) {
		t.Fatalf("turn ends: got %v, want %v", got, want)
	}
}

// turnEndIndexDownAndUp is the walk's check of 00024_turn_end_index.sql: its Down removes the events_turn_end index,
// and its Up restores it.
func turnEndIndexDownAndUp() migrationCheck {
	return migrationCheck{
		down: func(t *testing.T, s *Store) {
			t.Helper()
			if indexExists(t, s.db.DB, "events_turn_end") {
				t.Errorf("events_turn_end after Down: present, want absent")
			}
		},
		up: func(t *testing.T, s *Store) {
			t.Helper()
			if !indexExists(t, s.db.DB, "events_turn_end") {
				t.Errorf("events_turn_end after Up: absent, want present")
			}
		},
	}
}
