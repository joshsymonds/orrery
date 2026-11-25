package store

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"
)

// stallEpic is the invented epic the stall tests create.
const stallEpic = "orrery-5e6f7a8/epic-stall"

// stallAt is the second the stall tests stamp a stall at.
func stallAt() time.Time {
	return time.Unix(1700000700, 0)
}

// openStallStore opens a store with stallEpic created from fullEpic.
func openStallStore(t *testing.T) *Store {
	t.Helper()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(stallEpic))
	return s
}

// storedTrigger reads the cause, state and created second of trigger id of stallEpic.
func storedTrigger(t *testing.T, s *Store, id string) (string, string, int64) {
	t.Helper()
	var cause, state string
	var created int64
	if err := s.db.QueryRow(
		`SELECT cause, state, created FROM epic_triggers WHERE epic = ? AND id = ?`, stallEpic, id,
	).Scan(&cause, &state, &created); err != nil {
		t.Fatalf("read trigger %s of %s: %v", id, stallEpic, err)
	}
	return cause, state, created
}

// storedParkOf reads the park reason and park class of stallEpic.
func storedParkOf(t *testing.T, s *Store) (string, string) {
	t.Helper()
	var parked, class string
	if err := s.db.QueryRow(
		`SELECT parked, park_class FROM epics WHERE id = ?`, stallEpic,
	).Scan(&parked, &class); err != nil {
		t.Fatalf("read park of %s: %v", stallEpic, err)
	}
	return parked, class
}

// storedStrings returns the first text column of each row query gives.
func storedStrings(t *testing.T, s *Store, query string, args ...any) []string {
	t.Helper()
	rows, err := s.db.Query(query, args...)
	if err != nil {
		t.Fatalf("query %s: %v", query, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var text string
		if err := rows.Scan(&text); err != nil {
			t.Fatalf("scan %s: %v", query, err)
		}
		out = append(out, text)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read %s: %v", query, err)
	}
	return out
}

func TestEpicStallWakesMarkCountsEachTaskRunAndAction(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openStallStore(t)
	steps := []struct {
		name  string
		query string
		want  StallMark
	}{
		{
			name: "a task",
			query: `INSERT INTO epic_tasks (epic, seq, turn, title, deliverable, status)
				VALUES (?, 1, 1, 'Task', 'Deliverable', 'open')`,
			want: StallMark{Tasks: 1},
		},
		{
			name: "a node run with two attempts",
			query: `INSERT INTO epic_node_runs (epic, path, id, node, kind, state, attempt)
				VALUES (?, 'plan/S1/verify', 'id-verify', 'verify', 'gate', 'running', 2)`,
			want: StallMark{Runs: 3},
		},
		{
			name: "a planner action",
			query: `INSERT INTO planner_actions (epic, turn, position, kind, reason, state)
				VALUES (?, 1, 1, 'retry_epic', 'retry it', 'taken')`,
			want: StallMark{Actions: 1},
		},
		{
			name:  "an epic action",
			query: `INSERT INTO epic_actions (epic, seq, kind, maker, at) VALUES (?, 1, 'pause', 'owner', 1700000000)`,
			want:  StallMark{Actions: 1},
		},
	}
	before, err := s.StallMark(ctx, stallEpic)
	if err != nil {
		t.Fatalf("StallMark: %v", err)
	}
	for _, step := range steps {
		if _, err := s.fixtureExec(ctx, step.query, stallEpic); err != nil {
			t.Fatalf("add %s: %v", step.name, err)
		}
		after, err := s.StallMark(ctx, stallEpic)
		if err != nil {
			t.Fatalf("StallMark after %s: %v", step.name, err)
		}
		got := StallMark{Tasks: after.Tasks - before.Tasks, Runs: after.Runs - before.Runs,
			Actions: after.Actions - before.Actions}
		if got != step.want {
			t.Errorf("StallMark moved by %+v after %s, want %+v", got, step.name, step.want)
		}
		before = after
	}
}

func TestEpicStallWakesRecordsTheStallWithItsMarkAndPendingTrigger(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openStallStore(t)
	const cause = "no progress since the last pass"
	mark := StallMark{Tasks: 2, Runs: 5, Actions: 3}

	if _, ok, err := s.LatestStall(ctx, stallEpic); err != nil || ok {
		t.Fatalf("LatestStall before any wake = %v, %v; want no stall", ok, err)
	}
	if err := s.WakeStall(ctx, stallEpic, cause, mark, stallAt()); err != nil {
		t.Fatalf("WakeStall: %v", err)
	}
	latest, ok, err := s.LatestStall(ctx, stallEpic)
	if err != nil {
		t.Fatalf("LatestStall: %v", err)
	}
	want := EpicStall{Seq: 1, At: stallAt(), Mark: mark, Parked: false}
	if !ok || !reflect.DeepEqual(latest, want) {
		t.Errorf("LatestStall = %+v, %v; want %+v, true", latest, ok, want)
	}
	gotCause, state, created := storedTrigger(t, s, "stall:1")
	if gotCause != cause || state != "pending" || created != stallAt().Unix() {
		t.Errorf("trigger stall:1 = cause %q, state %q, created %d; want cause %q, pending, created %d",
			gotCause, state, created, cause, stallAt().Unix())
	}

	// A later wake numbers the next seq and its trigger from it.
	if err := s.WakeStall(ctx, stallEpic, cause, mark, stallAt()); err != nil {
		t.Fatalf("second WakeStall: %v", err)
	}
	latest, _, err = s.LatestStall(ctx, stallEpic)
	if err != nil {
		t.Fatalf("LatestStall after second wake: %v", err)
	}
	if latest.Seq != 2 {
		t.Errorf("LatestStall seq after second wake = %d, want 2", latest.Seq)
	}
	if _, state, _ := storedTrigger(t, s, "stall:2"); state != "pending" {
		t.Errorf("trigger stall:2 state = %q, want pending", state)
	}
}

func TestEpicStallParksOpensAStallQuestionWithTheReason(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openStallStore(t)
	const reason = "idle twice with nothing changed"
	mark := StallMark{Tasks: 1, Runs: 4, Actions: 2}

	if err := s.ParkStall(ctx, stallEpic, reason, mark, stallAt()); err != nil {
		t.Fatalf("ParkStall: %v", err)
	}
	parked, class := storedParkOf(t, s)
	if parked != reason || class != string(ParkClassStall) {
		t.Errorf("park of %s = %q, class %q; want %q, class %q", stallEpic, parked, class, reason, ParkClassStall)
	}
	questions := storedStrings(t, s,
		`SELECT text FROM epic_questions WHERE epic = ? AND label = ? AND state = 'open' ORDER BY seq`,
		stallEpic, ParkLabel)
	if !reflect.DeepEqual(questions, []string{reason}) {
		t.Errorf("open park questions = %q, want %q", questions, []string{reason})
	}
	tells := storedStrings(t, s, `SELECT text FROM epic_tells WHERE epic = ? ORDER BY seq`, stallEpic)
	if !reflect.DeepEqual(tells, []string{reason}) {
		t.Errorf("tells = %q, want %q", tells, []string{reason})
	}
	latest, ok, err := s.LatestStall(ctx, stallEpic)
	if err != nil {
		t.Fatalf("LatestStall: %v", err)
	}
	want := EpicStall{Seq: 1, At: stallAt(), Mark: mark, Parked: true}
	if !ok || !reflect.DeepEqual(latest, want) {
		t.Errorf("LatestStall = %+v, %v; want %+v, true", latest, ok, want)
	}
}

func TestEpicStallParksRetryEndsThePark(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openStallStore(t)
	const reason = "idle with no plan run to wake"

	if err := s.ParkStall(ctx, stallEpic, reason, StallMark{Tasks: 1}, stallAt()); err != nil {
		t.Fatalf("ParkStall: %v", err)
	}
	if err := s.RetryEpic(ctx, stallEpic, Maker{By: MakerOwner}, stallAt()); err != nil {
		t.Fatalf("RetryEpic: %v", err)
	}
	if parked, _ := storedParkOf(t, s); parked != "" {
		t.Errorf("park of %s after retry = %q, want none", stallEpic, parked)
	}
	cause, state, created := storedTrigger(t, s, "stall-retry:1")
	if !strings.Contains(cause, "the stall park was retried") || state != "pending" || created != stallAt().Unix() {
		t.Errorf("trigger stall-retry:1 = cause %q, state %q, created %d; want a cause saying the stall park"+
			" was retried, pending, created %d", cause, state, created, stallAt().Unix())
	}
}
