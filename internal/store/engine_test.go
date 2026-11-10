package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

const engineNowText = "1700000000"

func engineNowAt() time.Time {
	return time.Unix(1_700_000_000, 0)
}

func strOf(s string) *string {
	return &s
}

func payloadOf(t *testing.T, payload string) map[string]any {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal([]byte(payload), &got); err != nil {
		t.Fatalf("payload %q is not a JSON object: %v", payload, err)
	}
	return got
}

type wantEngineEvent struct {
	session string
	kind    string
	payload map[string]any
}

func assertEngineEvents(t *testing.T, s *Store, at time.Time, want []wantEngineEvent) {
	t.Helper()
	got := eventsOf(t, s)
	if len(got) != len(want) {
		t.Fatalf("events: got %d, want %d\n got: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].SessionID != w.session || got[i].Kind != w.kind || got[i].At != at.Unix() {
			t.Fatalf("event %d: got session %s kind %s at %d, want session %s kind %s at %d",
				i, got[i].SessionID, got[i].Kind, got[i].At, w.session, w.kind, at.Unix())
		}
		if payload := payloadOf(t, got[i].Payload); !reflect.DeepEqual(payload, w.payload) {
			t.Fatalf("event %d payload: got %v, want %v", i, payload, w.payload)
		}
	}
}

func engineRowOf(t *testing.T, s *Store, id string) sqlcgen.Session {
	t.Helper()
	row, err := sqlcgen.New(s.db).GetEngineSession(context.Background(), id)
	if err != nil {
		t.Fatalf("GetEngineSession %s: %v", id, err)
	}
	return row
}

func TestApplyEngineWritesEveryOpForTwoSessions(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)

	ops := []EngineOp{
		EngineAdd{ID: "e1", Dir: "/a", State: "working"},
		EngineAdd{ID: "e2", Dir: "/b", State: "idle"},
		EngineSet{ID: "e1", Changes: EngineChanges{State: strOf("waiting"), Question: strOf("ok?")}},
		EngineSet{ID: "e2", Changes: EngineChanges{Seen: strOf("yes")}},
		EnginePrompt{ID: "e1", Text: "hi"},
		EngineText{ID: "e1", MessageID: "m1", Index: 0, Kind: "text", Text: "partial"},
		EngineText{ID: "e2", MessageID: "m9", Index: 0, Kind: "text", Text: "stale"},
		EngineTextClear{ID: "e2"},
		EngineText{ID: "e2", MessageID: "m2", Index: 0, Kind: "thinking", Text: "pending"},
		EngineBlock{ID: "e1", Kind: "text", Role: "assistant", MessageID: "m1", Text: "done"},
		EngineTextDone{ID: "e1", MessageID: "m1", Index: 0},
		EngineRequest{ID: "e2", Kind: "permission", RequestID: "r1", ToolUseID: "t1",
			ToolName: "Bash", Input: `{"command":"ls"}`},
		EngineRequestCancelled{ID: "e2", RequestID: "r1", ToolUseID: "t1"},
		EngineTurnEnded{ID: "e2", Subtype: "success", TerminalReason: "done", Result: "ok", Origin: "user"},
		EngineSet{ID: "e2", Changes: EngineChanges{ClaudeSession: strOf("c-2"), Reason: strOf("x")}},
	}
	if err := s.ApplyEngine(ctx, ops, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}

	wantE1 := sqlcgen.Session{ID: "e1", Dir: "/a", State: "waiting", Changed: engineNowText,
		Question: "ok?", Kind: "engine"}
	if row := engineRowOf(t, s, "e1"); row != wantE1 {
		t.Fatalf("e1 row: got %+v, want %+v", row, wantE1)
	}
	wantE2 := sqlcgen.Session{ID: "e2", Dir: "/b", State: "idle", Changed: engineNowText,
		Seen: "yes", ClaudeSession: "c-2", Reason: "x", Kind: "engine"}
	if row := engineRowOf(t, s, "e2"); row != wantE2 {
		t.Fatalf("e2 row: got %+v, want %+v", row, wantE2)
	}

	assertEngineEvents(t, s, engineNowAt(), []wantEngineEvent{
		{"e1", EventEngineAdded, map[string]any{"dir": "/a", "state": "working"}},
		{"e2", EventEngineAdded, map[string]any{"dir": "/b", "state": "idle"}},
		{"e1", EventEngineChanged, map[string]any{"fields": map[string]any{
			"state": "waiting", "changed": engineNowText, "question": "ok?"}}},
		{"e2", EventEngineChanged, map[string]any{"fields": map[string]any{"seen": "yes"}}},
		{"e1", EventEnginePrompt, map[string]any{"text": "hi"}},
		{"e1", EventEngineBlock, map[string]any{"kind": "text", "role": "assistant", "messageId": "m1",
			"parentToolUseId": "", "text": "done", "toolUseId": "", "name": "", "input": "", "content": "",
			"isError": false}},
		{"e2", EventEngineRequest, map[string]any{"kind": "permission", "requestId": "r1", "toolUseId": "t1",
			"toolName": "Bash", "subtype": "", "input": `{"command":"ls"}`, "refused": false}},
		{"e2", EventEngineRequestCancelled, map[string]any{"requestId": "r1", "toolUseId": "t1"}},
		{"e2", EventEngineTurnEnded, map[string]any{"subtype": "success", "isError": false,
			"terminalReason": "done", "result": "ok", "origin": "user"}},
		{"e2", EventEngineChanged, map[string]any{"fields": map[string]any{
			"claudeSession": "c-2", "reason": "x"}}},
	})

	rows, err := sqlcgen.New(s.db).ListEngineText(ctx, "e1")
	if err != nil {
		t.Fatalf("ListEngineText e1: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("e1 unfinished text after EngineTextDone: got %+v, want none", rows)
	}
	rows, err = sqlcgen.New(s.db).ListEngineText(ctx, "e2")
	if err != nil {
		t.Fatalf("ListEngineText e2: %v", err)
	}
	wantText := []sqlcgen.EngineText{{SessionID: "e2", MessageID: "m2", BlockIndex: 0,
		Kind: "thinking", Text: "pending"}}
	if !reflect.DeepEqual(rows, wantText) {
		t.Fatalf("e2 unfinished text: got %+v, want %+v", rows, wantText)
	}
}

func TestApplyEngineSetStampsChangedOnlyWhenStateIsSet(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)
	later := engineNowAt().Add(time.Minute)

	if err := s.ApplyEngine(ctx, []EngineOp{EngineAdd{ID: "e1", Dir: "/a", State: "idle"}}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine add: %v", err)
	}
	if err := s.ApplyEngine(ctx, []EngineOp{EngineSet{ID: "e1", Changes: EngineChanges{Seen: strOf("yes")}}},
		later); err != nil {
		t.Fatalf("ApplyEngine seen: %v", err)
	}
	if row := engineRowOf(t, s, "e1"); row.Changed != engineNowText || row.Seen != "yes" {
		t.Fatalf("after seen-only set: got changed %q seen %q, want changed %q seen %q",
			row.Changed, row.Seen, engineNowText, "yes")
	}
	seenEvent := eventsOf(t, s)[1]
	if got := payloadOf(t, seenEvent.Payload); !reflect.DeepEqual(got,
		map[string]any{"fields": map[string]any{"seen": "yes"}}) {
		t.Fatalf("seen-only event payload: got %v, want only seen named", got)
	}

	if err := s.ApplyEngine(ctx, []EngineOp{EngineSet{ID: "e1", Changes: EngineChanges{State: strOf("working")}}},
		later); err != nil {
		t.Fatalf("ApplyEngine state: %v", err)
	}
	if row := engineRowOf(t, s, "e1"); row.Changed != strconv.FormatInt(later.Unix(), 10) {
		t.Fatalf("after state set: got changed %q, want %d", row.Changed, later.Unix())
	}
	stateEvent := eventsOf(t, s)[2]
	want := map[string]any{"fields": map[string]any{"state": "working", "changed": strconv.FormatInt(later.Unix(), 10)}}
	if got := payloadOf(t, stateEvent.Payload); !reflect.DeepEqual(got, want) {
		t.Fatalf("state event payload: got %v, want %v", got, want)
	}
}

func TestApplyEngineTextOverwritesTheSameBlock(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)

	ops := []EngineOp{
		EngineAdd{ID: "e1", Dir: "/a", State: "working"},
		EngineText{ID: "e1", MessageID: "m1", Index: 0, Kind: "text", Text: "a"},
		EngineText{ID: "e1", MessageID: "m1", Index: 0, Kind: "text", Text: "ab"},
	}
	if err := s.ApplyEngine(ctx, ops, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}
	rows, err := sqlcgen.New(s.db).ListEngineText(ctx, "e1")
	if err != nil {
		t.Fatalf("ListEngineText: %v", err)
	}
	want := []sqlcgen.EngineText{{SessionID: "e1", MessageID: "m1", BlockIndex: 0, Kind: "text", Text: "ab"}}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("unfinished text: got %+v, want %+v", rows, want)
	}
}

func TestApplyEngineTextDoneInSameBatchLeavesNoRow(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)

	ops := []EngineOp{
		EngineAdd{ID: "e1", Dir: "/a", State: "working"},
		EngineText{ID: "e1", MessageID: "m1", Index: 0, Kind: "text", Text: "partial"},
		EngineBlock{ID: "e1", Kind: "text", Role: "assistant", MessageID: "m1", Text: "full"},
		EngineTextDone{ID: "e1", MessageID: "m1", Index: 0},
	}
	if err := s.ApplyEngine(ctx, ops, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}
	rows, err := sqlcgen.New(s.db).ListEngineText(ctx, "e1")
	if err != nil {
		t.Fatalf("ListEngineText: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("unfinished text after EngineTextDone: got %+v, want none", rows)
	}
}

func TestApplyEngineCapsBlockAndTextAtUTF8Boundary(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)
	// "a" then 20000 two-byte runes: byte 32768 falls inside a rune, so the longest valid cut is 32767 bytes.
	long := "a" + strings.Repeat("é", 20000)
	wantCut := long[:32767]

	ops := []EngineOp{
		EngineAdd{ID: "e1", Dir: "/a", State: "working"},
		EngineText{ID: "e1", MessageID: "m1", Index: 0, Kind: "text", Text: long},
		EngineBlock{ID: "e1", Kind: "text", Role: "assistant", MessageID: "m2", Text: long},
	}
	if err := s.ApplyEngine(ctx, ops, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}

	rows, err := sqlcgen.New(s.db).ListEngineText(ctx, "e1")
	if err != nil {
		t.Fatalf("ListEngineText: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("unfinished text rows: got %d, want 1", len(rows))
	}
	if rows[0].Text != wantCut {
		t.Fatalf("unfinished text: got %d bytes, want %d bytes cut at a rune boundary", len(rows[0].Text), len(wantCut))
	}
	assertEngineEvents(t, s, engineNowAt(), []wantEngineEvent{
		{"e1", EventEngineAdded, map[string]any{"dir": "/a", "state": "working"}},
		{"e1", EventEngineBlock, map[string]any{"kind": "text", "role": "assistant", "messageId": "m2",
			"parentToolUseId": "", "text": wantCut, "toolUseId": "", "name": "", "input": "", "content": "",
			"isError": false, "truncated": true}},
	})
}

func TestApplyEngineCapsBlockInputAndContent(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)
	big := strings.Repeat("x", 40000)

	ops := []EngineOp{
		EngineAdd{ID: "e1", Dir: "/a", State: "working"},
		EngineBlock{ID: "e1", Kind: "tool_use", Role: "assistant", MessageID: "m1", ToolUseID: "t1",
			Name: "Bash", Input: big, Content: big},
	}
	if err := s.ApplyEngine(ctx, ops, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}
	events := eventsOf(t, s)
	if len(events) != 2 {
		t.Fatalf("events: got %d, want 2", len(events))
	}
	payload := payloadOf(t, events[1].Payload)
	input, _ := payload["input"].(string)
	content, _ := payload["content"].(string)
	if len(input) != EngineTextCap || len(content) != EngineTextCap {
		t.Fatalf("input len %d, content len %d, want both %d", len(input), len(content), EngineTextCap)
	}
	if payload["truncated"] != true {
		t.Fatalf("truncated: got %v, want true", payload["truncated"])
	}
	if !utf8.ValidString(input) || !utf8.ValidString(content) {
		t.Fatal("capped block text is not valid UTF-8")
	}
}

func TestApplyEngineRollsBackTheWholeBatchOnError(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)

	ops := []EngineOp{
		EngineAdd{ID: "e1", Dir: "/a", State: "working"},
		EngineSet{ID: "ghost", Changes: EngineChanges{State: strOf("idle")}},
	}
	err := s.ApplyEngine(ctx, ops, engineNowAt())
	if err == nil {
		t.Fatal("ApplyEngine with an unknown session: want error, got nil")
	}
	if _, err := sqlcgen.New(s.db).GetEngineSession(ctx, "e1"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("e1 after failed batch: got %v, want sql.ErrNoRows", err)
	}
	if events := eventsOf(t, s); len(events) != 0 {
		t.Fatalf("events after failed batch: got %+v, want none", events)
	}
}

func TestApplyEngineReturnsErrorWhileStoreIsWriteLocked(t *testing.T) {
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
	ops := []EngineOp{
		EngineAdd{ID: "e1", Dir: "/a", State: "working"},
		EnginePrompt{ID: "e1", Text: "hi"},
	}
	err = s.ApplyEngine(ctx, ops, engineNowAt())
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("ApplyEngine while write-locked: want error, got nil")
	}
	if elapsed >= time.Second {
		t.Fatalf("ApplyEngine while write-locked took %v, want under 1s", elapsed)
	}
	if _, err := sqlcgen.New(s.db).GetEngineSession(ctx, "e1"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("engine row written while locked: got %v, want sql.ErrNoRows", err)
	}
	if events := eventsOf(t, s); len(events) != 0 {
		t.Fatalf("events written while locked: %+v", events)
	}
}

func TestIsEngineKind(t *testing.T) {
	for _, kind := range []string{EventEngineAdded, EventEngineRequestCancelled, EventEngineTurnEnded} {
		if !IsEngineKind(kind) {
			t.Fatalf("IsEngineKind(%q): got false, want true", kind)
		}
	}
	for _, kind := range []string{EventSessionAdded, KindEngine, ""} {
		if IsEngineKind(kind) {
			t.Fatalf("IsEngineKind(%q): got true, want false", kind)
		}
	}
}
