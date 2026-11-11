package server_test

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/store"
)

// applyEngineOps applies ops to the store in one write, stamped at eventAt.
func applyEngineOps(t *testing.T, s *store.Store, ops ...store.EngineOp) {
	t.Helper()
	if err := s.ApplyEngine(t.Context(), ops, time.Unix(eventAt, 0)); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}
}

// insertRawEvent appends one event of kind with payload to the store at path through a second connection, so a test
// can store a payload the store's writer would not write.
func insertRawEvent(t *testing.T, path, kind, payload string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open store for raw event: %v", err)
	}
	_, execErr := db.ExecContext(
		t.Context(),
		"INSERT INTO events (session_id, kind, at, payload) VALUES (?, ?, ?, ?)",
		eventEngine, kind, eventAt, payload,
	)
	if closeErr := db.Close(); closeErr != nil {
		t.Fatalf("close store after raw event: %v", closeErr)
	}
	if execErr != nil {
		t.Fatalf("insert raw %s event: %v", kind, execErr)
	}
}

// engineText is the unfinished block message of eventEngine with the given text, as Watch sends it at seq.
func engineText(seq int64, text string) *orreryv1.WatchResponse {
	return &orreryv1.WatchResponse{Seq: seq, SessionId: eventEngine, Event: &orreryv1.WatchResponse_EngineText{
		EngineText: &orreryv1.EngineText{MessageId: "msg-1", Index: 0, Kind: "text", Text: text},
	}}
}

// wantMessage fails the test unless got equals want.
func wantMessage(t *testing.T, got, want *orreryv1.WatchResponse) {
	t.Helper()
	if !proto.Equal(got, want) {
		t.Fatalf("message: got %v, want %v", got, want)
	}
}

func TestWatchSendsEachEngineKindTyped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	applyEngineOps(t, s, store.EngineAdd{ID: eventEngine, Dir: "/a", State: "idle"})
	applyEngineOps(t, s, store.EngineSet{ID: eventEngine, Changes: store.EngineChanges{
		State: optional("working"), Seen: optional("1700000100"), Question: optional("Ok?"),
		ClaudeSession: optional("cs-1"), Reason: optional("resumed"),
	}})
	applyEngineOps(t, s, store.EnginePrompt{ID: eventEngine, Text: "fix the bug", PromptID: "p-1"})
	applyEngineOps(t, s, store.EngineBlock{
		ID: eventEngine, Kind: "tool_result", Role: "user", MessageID: "msg-1", ParentToolUseID: "toolu-parent",
		Text: strings.Repeat("t", store.EngineTextCap+1), ToolUseID: "toolu-1", Name: "Bash",
		Input: `{"cmd":"ls"}`, Content: "listing", IsError: true,
	})
	applyEngineOps(t, s, store.EngineRequest{
		ID: eventEngine, Kind: "permission", RequestID: "req-1", ToolUseID: "toolu-2", ToolName: "Write",
		Subtype: "can_use_tool", Input: `{"path":"/x"}`, Refused: true,
	})
	applyEngineOps(t, s, store.EngineRequestCancelled{ID: eventEngine, RequestID: "req-1", ToolUseID: "toolu-2"})
	applyEngineOps(t, s, store.EngineTurnEnded{
		ID: eventEngine, Subtype: "error_max_turns", IsError: true, TerminalReason: "max_turns",
		Result: "stopped", Origin: "claude",
	})
	events := orreryv1connect.NewEventsClient(guardedHTTPClient(), startServer(t, path))

	at := int64(eventAt)
	want := []*orreryv1.WatchResponse{
		{Seq: 1, SessionId: eventEngine, At: at, Event: &orreryv1.WatchResponse_EngineAdded{
			EngineAdded: &orreryv1.EngineAdded{Dir: "/a", State: "idle"},
		}},
		{Seq: 2, SessionId: eventEngine, At: at, Event: &orreryv1.WatchResponse_EngineChanged{
			EngineChanged: &orreryv1.EngineChanged{
				State: optional("working"), Changed: optional("1700000000"), Seen: optional("1700000100"),
				Question: optional("Ok?"), ClaudeSession: optional("cs-1"), Reason: optional("resumed"),
			},
		}},
		{Seq: 3, SessionId: eventEngine, At: at, Event: &orreryv1.WatchResponse_EnginePrompt{
			EnginePrompt: &orreryv1.EnginePrompt{Text: "fix the bug", PromptId: "p-1"},
		}},
		{Seq: 4, SessionId: eventEngine, At: at, Event: &orreryv1.WatchResponse_EngineBlock{
			EngineBlock: &orreryv1.EngineBlock{
				Kind: "tool_result", Role: "user", MessageId: "msg-1", ParentToolUseId: "toolu-parent",
				Text: strings.Repeat("t", store.EngineTextCap), ToolUseId: "toolu-1", Name: "Bash",
				Input: `{"cmd":"ls"}`, Content: "listing", IsError: true, Truncated: true,
			},
		}},
		{Seq: 5, SessionId: eventEngine, At: at, Event: &orreryv1.WatchResponse_EngineRequest{
			EngineRequest: &orreryv1.EngineRequest{
				Kind: "permission", RequestId: "req-1", ToolUseId: "toolu-2", ToolName: "Write",
				Subtype: "can_use_tool", Input: `{"path":"/x"}`, Refused: true,
			},
		}},
		{Seq: 6, SessionId: eventEngine, At: at, Event: &orreryv1.WatchResponse_EngineRequestCanceled{
			EngineRequestCanceled: &orreryv1.EngineRequestCanceled{RequestId: "req-1", ToolUseId: "toolu-2"},
		}},
		{Seq: 7, SessionId: eventEngine, At: at, Event: &orreryv1.WatchResponse_EngineTurnEnded{
			EngineTurnEnded: &orreryv1.EngineTurnEnded{
				Subtype:        "error_max_turns",
				IsError:        true,
				TerminalReason: "max_turns",
				Result:         "stopped",
				Origin:         "claude",
			},
		}},
	}
	w := openWatch(t, events, 0)
	for _, wantMsg := range want {
		wantMessage(t, w.next(t), wantMsg)
	}
}

func TestWatchSendsUnfinishedTextToLateJoiner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	addEngine(t, s)
	applyEngineOps(t, s, store.EngineText{
		ID: eventEngine, MessageID: "msg-1", Index: 0, Kind: "text", Text: "partial",
	})
	events := orreryv1connect.NewEventsClient(guardedHTTPClient(), startServer(t, path))

	w := openWatch(t, events, 0)
	if got := w.next(t).GetSeq(); got != 1 {
		t.Fatalf("engine_added: seq %d, want 1", got)
	}
	wantMessage(t, w.next(t), engineText(1, "partial"))

	applyEngineOps(t, s,
		store.EngineBlock{ID: eventEngine, Kind: "text", Role: "assistant", MessageID: "msg-1", Text: "partial done"},
		store.EngineTextDone{ID: eventEngine, MessageID: "msg-1", Index: 0},
	)
	block := w.next(t)
	if block.GetSeq() != 2 || block.GetEngineBlock().GetText() != "partial done" {
		t.Fatalf("finished block: got %v, want seq 2 with text %q", block, "partial done")
	}

	// The finished block's text is not sent again once the block has left Unfinished.
	promptEngine(t, s, 1)
	if got := w.next(t).GetSeq(); got != 3 {
		t.Fatalf("message after the finished block: seq %d, want 3 with no repeated text", got)
	}
}

func TestWatchSendsUnfinishedTextOnlyWhenItChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	addEngine(t, s)
	applyEngineOps(t, s, store.EngineText{
		ID: eventEngine, MessageID: "msg-1", Index: 0, Kind: "text", Text: "a",
	})
	events := orreryv1connect.NewEventsClient(guardedHTTPClient(), startServer(t, path))

	w := openWatch(t, events, 0)
	if got := w.next(t).GetSeq(); got != 1 {
		t.Fatalf("engine_added: seq %d, want 1", got)
	}
	wantMessage(t, w.next(t), engineText(1, "a"))

	// Ten polls pass with "a" unchanged. A stream that resent "a" on those polls would have a repeat queued, and the
	// next message read would be that repeat rather than the changed text.
	time.Sleep(10 * eventPoll)
	applyEngineOps(t, s, store.EngineText{
		ID: eventEngine, MessageID: "msg-1", Index: 0, Kind: "text", Text: "ab",
	})
	wantMessage(t, w.next(t), engineText(1, "ab"))

	// Ten polls pass with "ab" unchanged, then a later event arrives. A repeat of "ab" would be queued ahead of it.
	time.Sleep(10 * eventPoll)
	promptEngine(t, s, 1)
	if got := w.next(t).GetSeq(); got != 2 {
		t.Fatalf("message after the changed text: seq %d, want 2 with no repeated text", got)
	}
}

func TestWatchSendsBlockTextAgainAfterItLeavesUnfinished(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	addEngine(t, s)
	applyEngineOps(t, s, store.EngineText{
		ID: eventEngine, MessageID: "msg-1", Index: 0, Kind: "text", Text: "a",
	})
	events := orreryv1connect.NewEventsClient(guardedHTTPClient(), startServer(t, path))

	w := openWatch(t, events, 0)
	w.next(t)
	wantMessage(t, w.next(t), engineText(1, "a"))
	applyEngineOps(t, s,
		store.EngineTextDone{ID: eventEngine, MessageID: "msg-1", Index: 0},
		store.EngineBlock{ID: eventEngine, Kind: "text", Role: "assistant", MessageID: "msg-1", Text: "a"},
	)
	if got := w.next(t).GetSeq(); got != 2 {
		t.Fatalf("finished block: seq %d, want 2", got)
	}

	// The block's row returns with the text it had when it was sent. Because the block left Unfinished, the
	// stream sends the text again.
	applyEngineOps(t, s, store.EngineText{
		ID: eventEngine, MessageID: "msg-1", Index: 0, Kind: "text", Text: "a",
	})
	wantMessage(t, w.next(t), engineText(2, "a"))
}

func TestWatchResumedSendsLaterEventsThenCurrentText(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	addEngine(t, s)
	promptEngine(t, s, 1)
	applyEngineOps(t, s, store.EngineText{
		ID: eventEngine, MessageID: "msg-1", Index: 0, Kind: "text", Text: "resumed text",
	})
	events := orreryv1connect.NewEventsClient(guardedHTTPClient(), startServer(t, path))

	w := openWatch(t, events, 1)
	if got := w.next(t).GetSeq(); got != 2 {
		t.Fatalf("event after the cursor: seq %d, want 2", got)
	}
	wantMessage(t, w.next(t), engineText(2, "resumed text"))
}

func TestWatchEndsInternalOnMalformedEnginePayload(t *testing.T) {
	cases := []struct {
		name    string
		kind    string
		payload string
	}{
		{name: "prompt with an unknown key", kind: store.EventEnginePrompt, payload: `{"text":"hi","extra":1}`},
		{name: "prompt with a wrong type", kind: store.EventEnginePrompt, payload: `{"text":1}`},
		{name: "changed with an unknown top-level key", kind: store.EventEngineChanged,
			payload: `{"fields":{"state":"idle"},"extra":1}`},
		{name: "changed with an unknown field", kind: store.EventEngineChanged, payload: `{"fields":{"bogus":"x"}}`},
		{name: "canceled with a wrong type", kind: store.EventEngineRequestCancelled,
			payload: `{"requestId":1,"toolUseId":"t"}`},
		{name: "payload that is not JSON", kind: store.EventEngineTurnEnded, payload: `not json`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "orrery.db")
			s := openEventStore(t, path)
			addEngine(t, s)
			insertRawEvent(t, path, tc.kind, tc.payload)
			events := orreryv1connect.NewEventsClient(guardedHTTPClient(), startServer(t, path))

			w := openWatch(t, events, 0)
			if got := w.next(t).GetSeq(); got != 1 {
				t.Fatalf("engine_added before the malformed event: seq %d, want 1", got)
			}
			wantCode(t, w.finish(t), connect.CodeInternal)
		})
	}
}
