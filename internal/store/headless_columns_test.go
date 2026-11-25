package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// headlessT0 is the time of the first write of the headless column tests, in unix seconds.
const headlessT0 = 1_700_000_000

// headlessColumns holds the four sessions columns that carry the headless listing's values.
type headlessColumns struct {
	LatestReply     string
	NewestReplyAt   int64
	FirstPromptText string
	LatestRequestID string
}

// headlessColumnsOf returns the four columns of an engine session's row.
func headlessColumnsOf(t *testing.T, s *Store, id string) headlessColumns {
	t.Helper()
	row := engineRowOf(t, s, id)
	return headlessColumns{
		LatestReply: row.LatestReply, NewestReplyAt: row.NewestReplyAt,
		FirstPromptText: row.FirstPromptText, LatestRequestID: row.LatestRequestID,
	}
}

// applyHeadless applies ops at now and fails the test on an error.
func applyHeadless(t *testing.T, s *Store, now time.Time, ops ...EngineOp) {
	t.Helper()
	if err := s.ApplyEngine(context.Background(), ops, now); err != nil {
		t.Fatalf("ApplyEngine at %d: %v", now.Unix(), err)
	}
}

func TestHeadlessColumnsWrittenByApplyEngine(t *testing.T) {
	t.Parallel()
	s := openSyncStore(t)
	t0 := time.Unix(headlessT0, 0)
	t1 := t0.Add(time.Minute)
	t2 := t0.Add(2 * time.Minute)
	const id = "e1"

	applyHeadless(t, s, t0, EngineAdd{ID: id, Dir: "/a", State: "working"})
	if got, want := headlessColumnsOf(t, s, id), (headlessColumns{}); got != want {
		t.Fatalf("columns of a new session: got %+v, want %+v", got, want)
	}

	applyHeadless(t, s, t0,
		EnginePrompt{ID: id, Text: "first prompt"},
		EngineBlock{ID: id, Kind: "text", Role: "assistant", Text: "reply one"},
		EngineRequest{ID: id, Kind: "permission", RequestID: "r1", ToolName: "Bash"},
	)
	want := headlessColumns{LatestReply: "reply one", NewestReplyAt: headlessT0,
		FirstPromptText: "first prompt", LatestRequestID: "r1"}
	if got := headlessColumnsOf(t, s, id); got != want {
		t.Fatalf("columns after the first writes: got %+v, want %+v", got, want)
	}

	// A second prompt leaves the first prompt, and a later reply and request replace the latest reply and request.
	applyHeadless(t, s, t1,
		EnginePrompt{ID: id, Text: "second prompt"},
		EngineBlock{ID: id, Kind: "text", Role: "assistant", Text: "reply two"},
		EngineRequest{ID: id, Kind: "permission", RequestID: "r2", ToolName: "Bash"},
	)
	want = headlessColumns{LatestReply: "reply two", NewestReplyAt: t1.Unix(),
		FirstPromptText: "first prompt", LatestRequestID: "r2"}
	if got := headlessColumnsOf(t, s, id); got != want {
		t.Fatalf("columns after the second writes: got %+v, want %+v", got, want)
	}

	applyHeadless(t, s, t2, EngineBlock{ID: id, Kind: "text", Role: "assistant", Text: "reply three"})
	want = headlessColumns{LatestReply: "reply three", NewestReplyAt: t2.Unix(),
		FirstPromptText: "first prompt", LatestRequestID: "r2"}
	if got := headlessColumnsOf(t, s, id); got != want {
		t.Fatalf("columns after the third reply: got %+v, want %+v", got, want)
	}

	// A request written at an earlier time than the newest reply takes the request id, and the newest reply time stays.
	applyHeadless(t, s, t1, EngineRequest{ID: id, Kind: "permission", RequestID: "r3", ToolName: "Bash"})
	want = headlessColumns{LatestReply: "reply three", NewestReplyAt: t2.Unix(),
		FirstPromptText: "first prompt", LatestRequestID: "r3"}
	if got := headlessColumnsOf(t, s, id); got != want {
		t.Fatalf("columns after a request earlier than the newest reply: got %+v, want %+v", got, want)
	}
}

func TestHeadlessColumnsWrittenIgnoreSubagentUserAndRefusedWrites(t *testing.T) {
	t.Parallel()
	s := openSyncStore(t)
	t0 := time.Unix(headlessT0, 0)
	later := t0.Add(2 * time.Minute)
	const id = "e1"

	applyHeadless(t, s, t0,
		EngineAdd{ID: id, Dir: "/a", State: "working"},
		EnginePrompt{ID: id, Text: "first prompt"},
		EngineBlock{ID: id, Kind: "text", Role: "assistant", Text: "reply"},
		EngineRequest{ID: id, Kind: "permission", RequestID: "r1", ToolName: "Bash"},
	)
	before := headlessColumnsOf(t, s, id)
	wantBefore := headlessColumns{LatestReply: "reply", NewestReplyAt: headlessT0,
		FirstPromptText: "first prompt", LatestRequestID: "r1"}
	if before != wantBefore {
		t.Fatalf("columns before the ignored writes: got %+v, want %+v", before, wantBefore)
	}

	// Each write below is later than the newest reply, so a write that moved the columns would show it.
	applyHeadless(t, s, later,
		EngineBlock{ID: id, Kind: "text", Role: "assistant", ParentToolUseID: "toolu_sub", Text: "subagent reply"},
		EngineBlock{ID: id, Kind: "text", Role: "user", Text: "user text"},
		EngineBlock{ID: id, Kind: "thinking", Role: "assistant", Text: "thinking"},
		EngineRequest{ID: id, Kind: "permission", RequestID: "r-refused", ToolName: "Bash", Refused: true},
	)
	if got := headlessColumnsOf(t, s, id); got != before {
		t.Fatalf("columns after subagent, user and refused writes: got %+v, want %+v", got, before)
	}
}

func TestHeadlessColumnsWrittenKeptAfterEventPrune(t *testing.T) {
	t.Parallel()
	s := openSyncStore(t)
	t0 := time.Unix(headlessT0, 0)
	const id = "e1"

	applyHeadless(t, s, t0,
		EngineAdd{ID: id, Dir: "/a", State: "working"},
		EnginePrompt{ID: id, Text: "first prompt"},
		EngineBlock{ID: id, Kind: "text", Role: "assistant", Text: "reply"},
		EngineRequest{ID: id, Kind: "permission", RequestID: "r1", ToolName: "Bash"},
	)
	want := headlessColumns{LatestReply: "reply", NewestReplyAt: headlessT0,
		FirstPromptText: "first prompt", LatestRequestID: "r1"}

	// A write at the retention edge keeps the session, and its events at t0 fall out of the retention window.
	applyHeadless(t, s, t0.Add(EventRetention), EngineSet{ID: id, Changes: EngineChanges{Seen: strOf("later")}})
	if got := headlessColumnsOf(t, s, id); got != want {
		t.Fatalf("columns before the prune: got %+v, want %+v", got, want)
	}
	if n := eventsAt(t, s, id, t0.Unix()); n == 0 {
		t.Fatalf("session %s has no event at t0 before the prune, so the prune removes nothing", id)
	}

	ctx := context.Background()
	if err := s.SyncServer(ctx, "", "-L a", nil, t0.Add(EventRetention+time.Hour)); err != nil {
		t.Fatalf("SyncServer: %v", err)
	}
	if n := eventsAt(t, s, id, t0.Unix()); n != 0 {
		t.Fatalf("session %s has %d events at t0 after the prune, want 0", id, n)
	}
	if got := headlessColumnsOf(t, s, id); got != want {
		t.Fatalf("columns after the prune: got %+v, want %+v", got, want)
	}
}

// eventsAt counts the events of session id at the unix second at.
func eventsAt(t *testing.T, s *Store, id string, at int64) int {
	t.Helper()
	n := 0
	for _, event := range eventsOf(t, s) {
		if event.SessionID == id && event.At == at {
			n++
		}
	}
	return n
}

// insertHeadlessEvent appends an event of kind to session id at the unix second at, as a store at version 94 holds it.
func insertHeadlessEvent(t *testing.T, db *sql.DB, id, kind string, at int64, payload string) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO events (session_id, kind, at, payload) VALUES (?, ?, ?, ?)`, id, kind, at, payload,
	); err != nil {
		t.Fatalf("insert %s event of %s: %v", kind, id, err)
	}
}

func TestHeadlessColumnsWrittenBackfilledFromVersion94(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")

	fixture := openStoreAtVersion(t, path, 94)
	for _, stmt := range []string{
		`INSERT INTO sessions (id, server, pane, state, changed, kind, dir)
			VALUES ('e-1', '', '', 'waiting', '100', 'engine', '/a')`,
		`INSERT INTO sessions (id, server, pane, state, changed, kind, dir)
			VALUES ('e-2', '', '', 'idle', '200', 'engine', '/b')`,
	} {
		if _, err := fixture.ExecContext(ctx, stmt); err != nil {
			fixture.Close()
			t.Fatalf("seed session: %v", err)
		}
	}
	insertHeadlessEvent(t, fixture, "e-1", "engine_prompt", 100, `{"text":"first prompt"}`)
	insertHeadlessEvent(t, fixture, "e-1", "engine_prompt", 110, `{"text":"second prompt"}`)
	insertHeadlessEvent(t, fixture, "e-1", "engine_block", 120,
		`{"kind":"text","role":"assistant","parentToolUseId":"","text":"reply A"}`)
	insertHeadlessEvent(t, fixture, "e-1", "engine_block", 130,
		`{"kind":"text","role":"assistant","parentToolUseId":"toolu_sub","text":"subagent"}`)
	insertHeadlessEvent(t, fixture, "e-1", "engine_block", 140,
		`{"kind":"text","role":"user","parentToolUseId":"","text":"user"}`)
	insertHeadlessEvent(t, fixture, "e-1", "engine_request", 150, `{"requestId":"r-1","refused":false}`)
	insertHeadlessEvent(t, fixture, "e-1", "engine_request", 160, `{"requestId":"r-2","refused":true}`)
	// Reply B is written before reply C, and C has the earlier at: the listing takes B, the one with the latest at.
	insertHeadlessEvent(t, fixture, "e-1", "engine_block", 170,
		`{"kind":"text","role":"assistant","parentToolUseId":"","text":"reply B"}`)
	insertHeadlessEvent(t, fixture, "e-1", "engine_block", 165,
		`{"kind":"text","role":"assistant","parentToolUseId":"","text":"reply C"}`)
	insertHeadlessEvent(t, fixture, "e-2", "engine_request", 200, `{"requestId":"r-9","refused":false}`)
	insertHeadlessEvent(t, fixture, "e-2", "engine_block", 210,
		`{"kind":"text","role":"assistant","parentToolUseId":"","text":"e2 reply"}`)
	if err := fixture.Close(); err != nil {
		t.Fatalf("close fixture: %v", err)
	}

	s, err := Open(ctx, path, noSync())
	if err != nil {
		t.Fatalf("Open at version 94: %v", err)
	}
	defer s.Close()

	var version int64
	if err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(version_id), 0) FROM goose_db_version WHERE is_applied = 1`).Scan(&version); err != nil {
		t.Fatalf("read store version: %v", err)
	}
	if version != 111 {
		t.Fatalf("store version after Open = %d, want 111", version)
	}

	listing, err := s.fixtureQueries().ListHeadlessSessions(ctx, 0)
	if err != nil {
		t.Fatalf("ListHeadlessSessions: %v", err)
	}
	want := []headlessColumns{
		{LatestReply: "reply B", NewestReplyAt: 170, FirstPromptText: "first prompt", LatestRequestID: "r-1"},
		{LatestReply: "e2 reply", NewestReplyAt: 210, FirstPromptText: "", LatestRequestID: "r-9"},
	}
	got := make([]headlessColumns, 0, len(listing))
	for _, row := range listing {
		got = append(got, headlessColumnsOf(t, s, row.ID))
		listed := headlessColumns{LatestReply: row.Reply, NewestReplyAt: row.NewestReply,
			FirstPromptText: row.FirstPrompt, LatestRequestID: row.RequestID}
		if !reflect.DeepEqual(got[len(got)-1], listed) {
			t.Fatalf("session %s columns: got %+v, listing says %+v", row.ID, got[len(got)-1], listed)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("backfilled columns: got %+v, want %+v", got, want)
	}
}
