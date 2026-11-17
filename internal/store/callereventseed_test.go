package store

import (
	"context"
	"reflect"
	"testing"

	"github.com/pressly/goose/v3"
)

// callerSeedEvent is one caller_events row the seed tests read back.
type callerSeedEvent struct {
	event   string
	session string
	at      int64
}

// seedCallerEpic inserts the epic id in phase at version 56.
func seedCallerEpic(t *testing.T, s *Store, id, phase string) {
	t.Helper()
	if _, err := s.db.ExecContext(context.Background(), `INSERT INTO epics
		(id, repository, title, approved, intent, care, approach, release_then, checks, phase, created, changed)
		VALUES (?, '/work/repo', 'Epic', 'approved line', 'intent', 'serious', 'approach', 'then', 'checks', ?,
		1700000000, 1700000000)`, id, phase); err != nil {
		t.Fatalf("insert epic %s: %v", id, err)
	}
}

// seedCallerSession inserts a session of epic whose newest call of the epic is call, at unix seconds at.
func seedCallerSession(t *testing.T, s *Store, epic, session, call string, at int64) {
	t.Helper()
	if _, err := s.db.ExecContext(context.Background(), `INSERT INTO epic_sessions (epic, session, call, at)
		VALUES (?, ?, ?, ?)`, epic, session, call, at); err != nil {
		t.Fatalf("insert epic session %s of %s: %v", session, epic, err)
	}
}

// seedCallerQuestion inserts question seq of epic at version 56, in state.
func seedCallerQuestion(t *testing.T, s *Store, epic string, seq int, state string) {
	t.Helper()
	if _, err := s.db.ExecContext(context.Background(), `INSERT INTO epic_questions (epic, seq, turn, text, state)
		VALUES (?, ?, 1, 'question', ?)`, epic, seq, state); err != nil {
		t.Fatalf("insert question %d of %s: %v", seq, epic, err)
	}
}

// applyCallerEventSeed runs 00057's Up on the store at version 56 and returns the caller_events rows, ordered by event
// and then session.
func applyCallerEventSeed(t *testing.T, s *Store, provider *goose.Provider) []callerSeedEvent {
	t.Helper()
	ctx := context.Background()
	if _, err := provider.UpByOne(ctx); err != nil {
		t.Fatalf("Up of 00057: %v", err)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT event, session, at FROM caller_events ORDER BY event, session`)
	if err != nil {
		t.Fatalf("read caller_events: %v", err)
	}
	var out []callerSeedEvent
	for rows.Next() {
		var e callerSeedEvent
		if err = rows.Scan(&e.event, &e.session, &e.at); err != nil {
			t.Fatalf("scan caller_events: %v", err)
		}
		out = append(out, e)
	}
	if err = rowsDone(rows); err != nil {
		t.Fatalf("read caller_events: %v", err)
	}
	return out
}

// TestCallerEventSeedMarksCreatorQuestionsAndEndedPhase checks that the Up marks each question of an epic whose creator
// is a session that called epic_create, in any state, and the epic's ended phase.
func TestCallerEventSeedMarksCreatorQuestionsAndEndedPhase(t *testing.T) {
	t.Parallel()
	s, provider := storeAtVersion(t, 56)
	const epic = "orrery-1/ended"
	seedCallerEpic(t, s, epic, "done")
	seedCallerSession(t, s, epic, "creator", "epic_create", 1700000100)
	seedCallerQuestion(t, s, epic, 1, "answered")
	seedCallerQuestion(t, s, epic, 2, "open")

	got := applyCallerEventSeed(t, s, provider)
	want := []callerSeedEvent{
		{"epic:orrery-1/ended:Q1", "creator", 1700000100},
		{"epic:orrery-1/ended:Q2", "creator", 1700000100},
		{"epic:orrery-1/ended:done", "creator", 1700000100},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("caller_events after Up = %+v, want %+v", got, want)
	}
}

// TestCallerEventSeedSkipsCleanAndRunningPhases checks that the Up marks the questions of a clean epic and of a running
// epic, but no phase event for either: clean is not an ended phase, and accepted is the phase of a running epic.
func TestCallerEventSeedSkipsCleanAndRunningPhases(t *testing.T) {
	t.Parallel()
	s, provider := storeAtVersion(t, 56)
	const clean = "orrery-2/clean"
	const running = "orrery-2/running"
	seedCallerEpic(t, s, clean, "clean")
	seedCallerSession(t, s, clean, "creator-clean", "epic_create", 1700000200)
	seedCallerQuestion(t, s, clean, 1, "answered")
	seedCallerEpic(t, s, running, "accepted")
	seedCallerSession(t, s, running, "creator-running", "epic_create", 1700000300)
	seedCallerQuestion(t, s, running, 1, "open")

	got := applyCallerEventSeed(t, s, provider)
	want := []callerSeedEvent{
		{"epic:orrery-2/clean:Q1", "creator-clean", 1700000200},
		{"epic:orrery-2/running:Q1", "creator-running", 1700000300},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("caller_events after Up = %+v, want %+v", got, want)
	}
}

// TestCallerEventSeedSkipsSessionsWhoseNewestCallIsNotCreate checks that the Up marks nothing for an epic whose only
// session's newest call is epic_get, though the epic has a question and an ended phase.
func TestCallerEventSeedSkipsSessionsWhoseNewestCallIsNotCreate(t *testing.T) {
	t.Parallel()
	s, provider := storeAtVersion(t, 56)
	const epic = "orrery-3/ended"
	seedCallerEpic(t, s, epic, "done")
	seedCallerSession(t, s, epic, "reader", "epic_get", 1700000400)
	seedCallerQuestion(t, s, epic, 1, "answered")

	got := applyCallerEventSeed(t, s, provider)
	if len(got) != 0 {
		t.Fatalf("caller_events after Up = %+v, want none", got)
	}
}
