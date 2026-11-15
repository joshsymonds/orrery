package store

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
)

// answeredRow is one event of a session read back with its kind, payload and answered column.
type answeredRow struct {
	Kind     string
	Payload  string
	Answered sql.NullString
}

// answeredRowsOf returns the events of session id in order, each with its answered column.
func answeredRowsOf(t *testing.T, s *Store, id string) []answeredRow {
	t.Helper()
	rows, err := s.db.QueryContext(context.Background(),
		`SELECT kind, payload, answered FROM events WHERE session_id = ? ORDER BY seq`, id)
	if err != nil {
		t.Fatalf("read events of session %s: %v", id, err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			t.Errorf("close event rows: %v", err)
		}
	}()
	var out []answeredRow
	for rows.Next() {
		var row answeredRow
		if err := rows.Scan(&row.Kind, &row.Payload, &row.Answered); err != nil {
			t.Fatalf("scan event of session %s: %v", id, err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate events of session %s: %v", id, err)
	}
	return out
}

// TestEngineTurnEndedStoresAnsweredUUIDs reads back the answered column of two turn ends: the uuids of the one that
// lists them as a JSON array, and an empty array for the one that lists none. Each payload is the one a turn end
// stored before the column existed, and the row of another kind holds NULL.
func TestEngineTurnEndedStoresAnsweredUUIDs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openSyncStore(t)
	ops := []EngineOp{
		EngineAdd{ID: "e1", Dir: "/a", State: "working"},
		EngineTurnEnded{ID: "e1", Subtype: "success", TerminalReason: "completed", Result: "done", Origin: "user",
			Answered: []string{"u-1", "u-2"}},
		EngineTurnEnded{ID: "e1", Subtype: "success"},
	}
	if err := s.ApplyEngine(ctx, ops, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}

	want := []answeredRow{
		{EventEngineAdded, `{"dir":"/a","state":"working"}`, sql.NullString{}},
		{EventEngineTurnEnded,
			`{"subtype":"success","isError":false,"terminalReason":"completed","result":"done","origin":"user"}`,
			sql.NullString{String: `["u-1","u-2"]`, Valid: true}},
		{EventEngineTurnEnded,
			`{"subtype":"success","isError":false,"terminalReason":"","result":"","origin":""}`,
			sql.NullString{String: `[]`, Valid: true}},
	}
	if got := answeredRowsOf(t, s, "e1"); !reflect.DeepEqual(got, want) {
		t.Fatalf("events of e1:\n got %+v\nwant %+v", got, want)
	}
}
