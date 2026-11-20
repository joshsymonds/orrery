package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func int64Of(v int64) *int64 {
	return &v
}

func boolOf(v bool) *bool {
	return &v
}

func TestEngineReadoptNewRowIsUntracked(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openSyncStore(t)

	if err := s.ApplyEngine(ctx, []EngineOp{EngineAdd{ID: "e1", Dir: "/a", State: "idle"}}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}

	row, err := s.EngineSession(ctx, "e1")
	if err != nil {
		t.Fatalf("EngineSession: %v", err)
	}
	want := EngineRow{ID: "e1", Dir: "/a", State: "idle", Kind: KindEngine}
	if row != want {
		t.Fatalf("new row: got %+v, want untracked %+v", row, want)
	}
}

func TestEngineLaunchTracksRowAtZero(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openSyncStore(t)

	if err := s.ApplyEngine(ctx, []EngineOp{
		EngineAdd{ID: "e1", Dir: "/a", State: "idle"},
		EngineConsumed{ID: "e1", Consumed: 9, ReplayFrom: int64Of(4), Background: boolOf(true), Awaited: boolOf(true)},
		EngineLaunch{ID: "e1"},
	}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}

	row, err := s.EngineSession(ctx, "e1")
	if err != nil {
		t.Fatalf("EngineSession: %v", err)
	}
	if !row.Tracked || row.Consumed != 0 || row.ReplayFrom != 0 || row.Background || row.Awaited {
		t.Fatalf("launched row: got tracked %v consumed %d replayFrom %d background %v awaited %v, want tracked at 0 "+
			"with replay from 0 and background and awaited off",
			row.Tracked, row.Consumed, row.ReplayFrom, row.Background, row.Awaited)
	}
}

func TestEngineConsumedMovesOnlySetColumns(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)

	if err := s.ApplyEngine(ctx, []EngineOp{
		EngineAdd{ID: "e1", Dir: "/a", State: "idle"},
		EngineLaunch{ID: "e1"},
	}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine launch: %v", err)
	}
	eventsBefore := len(eventsOf(t, s))

	steps := []struct {
		name string
		op   EngineConsumed
		want EngineRow
	}{
		{
			name: "consumed with replay from and background",
			op:   EngineConsumed{ID: "e1", Consumed: 5, ReplayFrom: int64Of(3), Background: boolOf(true)},
			want: EngineRow{Tracked: true, Consumed: 5, ReplayFrom: 3, Background: true},
		},
		{
			name: "consumed with awaited leaves background",
			op:   EngineConsumed{ID: "e1", Consumed: 6, Awaited: boolOf(true)},
			want: EngineRow{Tracked: true, Consumed: 6, ReplayFrom: 3, Background: true, Awaited: true},
		},
		{
			name: "consumed with output only leaves replay from, background and awaited",
			op:   EngineConsumed{ID: "e1", Consumed: 7, Output: strOf("done")},
			want: EngineRow{
				Tracked: true, Consumed: 7, ReplayFrom: 3, Background: true, Awaited: true, TurnOutput: "done",
			},
		},
		{
			name: "consumed alone leaves every optional column",
			op:   EngineConsumed{ID: "e1", Consumed: 8},
			want: EngineRow{
				Tracked: true, Consumed: 8, ReplayFrom: 3, Background: true, Awaited: true, TurnOutput: "done",
			},
		},
	}
	for _, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			if err := s.ApplyEngine(ctx, []EngineOp{step.op}, engineNowAt()); err != nil {
				t.Fatalf("ApplyEngine: %v", err)
			}
			row, err := s.EngineSession(ctx, "e1")
			if err != nil {
				t.Fatalf("EngineSession: %v", err)
			}
			want := step.want
			want.ID, want.Dir, want.State, want.Kind = "e1", "/a", "idle", KindEngine
			if row != want {
				t.Fatalf("row: got %+v, want %+v", row, want)
			}
		})
	}

	if got := len(eventsOf(t, s)); got != eventsBefore {
		t.Fatalf("events after EngineConsumed: got %d, want %d (EngineConsumed appends no event)", got, eventsBefore)
	}
}

func TestEngineHolderPromptsListsLaterPromptsWithWriteIDs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openSyncStore(t)

	if err := s.ApplyEngine(ctx, []EngineOp{
		EngineAdd{ID: "e1", Dir: "/a", State: "idle"},
		EngineAdd{ID: "e2", Dir: "/b", State: "idle"},
		EnginePrompt{ID: "e1", Text: "before", WriteID: "w0"},
		EngineLaunch{ID: "e1"},
		EnginePrompt{ID: "e1", Text: "first", WriteID: "w1"},
		EnginePrompt{ID: "e2", Text: "other session", WriteID: "x1"},
		EnginePrompt{ID: "e1", Text: "no write id"},
		EnginePrompt{ID: "e1", Text: "second", WriteID: "w2"},
	}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}

	got, err := s.HolderPrompts(ctx, "e1")
	if err != nil {
		t.Fatalf("HolderPrompts: %v", err)
	}
	want := []HolderPrompt{{WriteID: "w1", Text: "first"}, {WriteID: "w2", Text: "second"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("HolderPrompts: got %+v, want %+v", got, want)
	}

	events := eventsOf(t, s)
	if payload := events[3].Payload; payload != `{"text":"first"}` {
		t.Fatalf("prompt payload: got %s, want {\"text\":\"first\"} (the write id is not in the payload)", payload)
	}

	if err := s.ApplyEngine(ctx, []EngineOp{EngineLaunch{ID: "e2"}}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine launch e2: %v", err)
	}
	empty, err := s.HolderPrompts(ctx, "e2")
	if err != nil {
		t.Fatalf("HolderPrompts e2: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("HolderPrompts after launch with no later prompts: got %+v, want none", empty)
	}
}

func TestEngineReadoptOpsFailOnMissingRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openSyncStore(t)

	if err := s.SyncPane(ctx, "", Pane{ID: "orrery-a", Pane: "%1"}, engineNowAt()); err != nil {
		t.Fatalf("SyncPane: %v", err)
	}

	cases := []struct {
		name string
		op   EngineOp
	}{
		{name: "consumed of unknown id", op: EngineConsumed{ID: "nope", Consumed: 1}},
		{name: "launch of unknown id", op: EngineLaunch{ID: "nope"}},
		{name: "consumed of pane row", op: EngineConsumed{ID: "orrery-a", Consumed: 1}},
		{name: "launch of pane row", op: EngineLaunch{ID: "orrery-a"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := s.ApplyEngine(ctx, []EngineOp{tc.op}, engineNowAt())
			if !errors.Is(err, ErrNoEngineSession) {
				t.Fatalf("ApplyEngine: got %v, want ErrNoEngineSession", err)
			}
		})
	}
}

// engineReadoptDownAndUp is the walk's check of 00013_engine_readopt.sql: its Down removes the readopt columns of
// sessions and the write_id of events, and its Up restores them.
func engineReadoptDownAndUp() migrationCheck {
	sessionReadopt := []string{"consumed", "replay_from", "background", "turn_output", "prompts_from"}
	return migrationCheck{
		down: func(t *testing.T, s *Store) {
			t.Helper()
			if sessionCols := tableColumns(t, s, "sessions"); hasAny(sessionCols, sessionReadopt...) {
				t.Errorf("sessions columns after Down: got %v, want no readopt columns", sessionCols)
			}
			if eventCols := tableColumns(t, s, "events"); hasAny(eventCols, "write_id") {
				t.Errorf("events columns after Down: got %v, want no write_id", eventCols)
			}
		},
		up: func(t *testing.T, s *Store) {
			t.Helper()
			if sessionCols := tableColumns(t, s, "sessions"); !hasAll(sessionCols, sessionReadopt...) {
				t.Errorf("sessions columns after Up: got %v, want the readopt columns", sessionCols)
			}
			if eventCols := tableColumns(t, s, "events"); !hasAll(eventCols, "write_id") {
				t.Errorf("events columns after Up: got %v, want write_id", eventCols)
			}
		},
	}
}

func tableColumns(t *testing.T, s *Store, table string) []string {
	t.Helper()
	rows, err := s.db.QueryContext(context.Background(), `SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		t.Fatalf("table_info %s: %v", table, err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			t.Errorf("close table_info rows: %v", err)
		}
	}()
	var cols []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan table_info %s: %v", table, err)
		}
		cols = append(cols, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate table_info %s: %v", table, err)
	}
	return cols
}

func hasAny(cols []string, names ...string) bool {
	for _, name := range names {
		if hasAll(cols, name) {
			return true
		}
	}
	return false
}

func hasAll(cols []string, names ...string) bool {
	for _, name := range names {
		found := false
		for _, col := range cols {
			if col == name {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
