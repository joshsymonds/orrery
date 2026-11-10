package session

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// fixtureLines returns the stdout lines of the named capture fixture. It reads the file by path, so
// this package never imports the capture package.
func fixtureLines(t *testing.T, name string) [][]byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "capture", "testdata", name+".out.ndjson"))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	var lines [][]byte
	for _, line := range bytes.Split(data, []byte("\n")) {
		if len(line) > 0 {
			lines = append(lines, line)
		}
	}
	return lines
}

// translateLines feeds lines through one translator in order and returns every event they produce.
func translateLines(t *testing.T, tr *translator, lines ...string) []Event {
	t.Helper()
	events := make([]Event, 0, len(lines))
	for _, line := range lines {
		events = append(events, tr.translate([]byte(line))...)
	}
	return events
}

// requireEvents fails the test unless got equals want.
func requireEvents(t *testing.T, got, want []Event) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("events =\n%#v\nwant\n%#v", got, want)
	}
}

func TestFixturesTranslateWithoutUnknown(t *testing.T) {
	t.Parallel()
	for _, name := range []string{
		"interrupt", "mode-change", "permission-allow", "permission-deny", "question", "two-turns",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			tr := newTranslator()
			events := 0
			for number, line := range fixtureLines(t, name) {
				for _, event := range tr.translate(line) {
					events++
					if unknown, ok := event.(Unknown); ok {
						t.Errorf("line %d: Unknown: %v\n%s", number+1, unknown.Err, unknown.Line)
					}
				}
			}
			if events == 0 {
				t.Errorf("fixture %s translated to no events", name)
			}
		})
	}
}

func TestTwoTurnsFixtureSequence(t *testing.T) {
	t.Parallel()
	tr := newTranslator()
	lines := fixtureLines(t, "two-turns")
	got := make([]Event, 0, len(lines))
	for _, line := range lines {
		got = append(got, tr.translate(line)...)
	}
	kinds := make([]string, 0, len(got))
	for _, event := range got {
		kinds = append(kinds, fmt.Sprintf("%T", event))
	}
	// Each turn opens with its own init line: Started, then its text delta, text block and turn end.
	turn := []string{"session.Started", "session.TextDelta", "session.Block", "session.TurnEnded"}
	wantKinds := append(append([]string{}, turn...), turn...)
	if !reflect.DeepEqual(kinds, wantKinds) {
		t.Fatalf("event kinds =\n%q\nwant\n%q", kinds, wantKinds)
	}
	for number, messageID := range []string{"msg_1", "msg_2"} {
		base := number * len(turn)
		started, ok := got[base].(Started)
		if !ok || started.SessionID != "session_1" || started.PermissionMode != "default" {
			t.Errorf("turn %d start = %#v, want Started for session_1 in default mode", number+1, got[base])
		}
		delta, ok := got[base+1].(TextDelta)
		if !ok || delta.MessageID != messageID || delta.Index != 0 || delta.ParentToolUseID != "" {
			t.Errorf("turn %d delta = %#v, want text delta for %s at index 0", number+1, got[base+1], messageID)
		}
		block, ok := got[base+2].(Block)
		if !ok || block.Kind != BlockText || block.Role != "assistant" || block.MessageID != messageID {
			t.Errorf("turn %d block = %#v, want assistant text block for %s", number+1, got[base+2], messageID)
		}
		ended, ok := got[base+3].(TurnEnded)
		if !ok || ended.Subtype != "success" || ended.IsError {
			t.Errorf("turn %d end = %#v, want successful TurnEnded", number+1, got[base+3])
		}
	}
}

func TestInterleavedSubagentStreamsKeepTheirMessage(t *testing.T) {
	t.Parallel()
	tr := newTranslator()
	got := translateLines(
		t,
		tr,
		`{"type":"stream_event","event":{"type":"message_start","message":{"id":"msg_main","type":"message","role":"assistant","content":[]}},"parent_tool_use_id":null,"session_id":"session_1"}`,
		`{"type":"stream_event","event":{"type":"message_start","message":{"id":"msg_sub","type":"message","role":"assistant","content":[]}},"parent_tool_use_id":"toolu_task","session_id":"session_1"}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"main first"}},"parent_tool_use_id":null,"session_id":"session_1"}`,
		`{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}},"parent_tool_use_id":"toolu_task","session_id":"session_1"}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"sub first"}},"parent_tool_use_id":"toolu_task","session_id":"session_1"}`,
		`{"type":"stream_event","event":{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_inner","name":"Read","input":{}}},"parent_tool_use_id":"toolu_task","session_id":"session_1"}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"path\":"}},"parent_tool_use_id":"toolu_task","session_id":"session_1"}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":1,"delta":{"type":"thinking_delta","thinking":"main pondering"}},"parent_tool_use_id":null,"session_id":"session_1"}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":1,"delta":{"type":"signature_delta","signature":"sig-made-up"}},"parent_tool_use_id":null,"session_id":"session_1"}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"main second"}},"parent_tool_use_id":null,"session_id":"session_1"}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":2,"delta":{"type":"thinking_delta","thinking":"sub pondering"}},"parent_tool_use_id":"toolu_task","session_id":"session_1"}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":3,"delta":{"type":"text_delta","text":"sub second"}},"parent_tool_use_id":"toolu_task","session_id":"session_1"}`,
	)
	requireEvents(t, got, []Event{
		TextDelta{MessageID: "msg_main", Index: 0, Text: "main first"},
		TextDelta{MessageID: "msg_sub", Index: 0, ParentToolUseID: "toolu_task", Text: "sub first"},
		ToolCallStarted{
			MessageID:       "msg_sub",
			Index:           1,
			ParentToolUseID: "toolu_task",
			ToolUseID:       "toolu_inner",
			Name:            "Read",
		},
		ThinkingDelta{MessageID: "msg_main", Index: 1, Text: "main pondering"},
		TextDelta{MessageID: "msg_main", Index: 0, Text: "main second"},
		ThinkingDelta{MessageID: "msg_sub", Index: 2, ParentToolUseID: "toolu_task", Text: "sub pondering"},
		TextDelta{MessageID: "msg_sub", Index: 3, ParentToolUseID: "toolu_task", Text: "sub second"},
	})
}

func TestAssistantLineBlocksKeepOrderAndDropSignature(t *testing.T) {
	t.Parallel()
	tr := newTranslator()
	got := translateLines(t, tr,
		`{"type":"assistant","message":{"id":"msg_asst","type":"message","role":"assistant","content":[`+
			`{"type":"text","text":"alpha words"},`+
			`{"type":"thinking","thinking":"beta thought","signature":"sig-made-up"},`+
			`{"type":"tool_use","id":"toolu_bash","name":"Bash","input":{"command":"ls"}}]},`+
			`"parent_tool_use_id":"toolu_task","session_id":"session_1"}`,
	)
	requireEvents(t, got, []Event{
		Block{
			Kind:            BlockText,
			Role:            "assistant",
			MessageID:       "msg_asst",
			ParentToolUseID: "toolu_task",
			Text:            "alpha words",
		},
		Block{
			Kind:            BlockThinking,
			Role:            "assistant",
			MessageID:       "msg_asst",
			ParentToolUseID: "toolu_task",
			Text:            "beta thought",
		},
		Block{
			Kind: BlockToolCall, Role: "assistant", MessageID: "msg_asst", ParentToolUseID: "toolu_task",
			ToolUseID: "toolu_bash", Name: "Bash", Input: json.RawMessage(`{"command":"ls"}`),
		},
	})
}

func TestUserLineBlocks(t *testing.T) {
	t.Parallel()
	tr := newTranslator()
	got := translateLines(
		t,
		tr,
		`{"type":"user","message":{"role":"user","content":"hello there"},"parent_tool_use_id":null,"session_id":"session_1"}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"typed words"}]},"parent_tool_use_id":null,"session_id":"session_1"}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_bash","content":"listing made up","is_error":true}]},"parent_tool_use_id":"toolu_task","session_id":"session_1"}`,
	)
	requireEvents(t, got, []Event{
		Block{Kind: BlockText, Role: "user", Text: "hello there"},
		Block{Kind: BlockText, Role: "user", Text: "typed words"},
		Block{
			Kind: BlockToolResult, Role: "user", ParentToolUseID: "toolu_task", ToolUseID: "toolu_bash",
			Content: json.RawMessage(`"listing made up"`), IsError: true,
		},
	})
}

func TestTurnEndedSubtypes(t *testing.T) {
	t.Parallel()
	tr := newTranslator()
	got := translateLines(
		t,
		tr,
		`{"type":"result","subtype":"success","is_error":false,"result":"all done","terminal_reason":"completed","origin":{"kind":"task_notification"},"session_id":"session_1"}`,
		`{"type":"result","subtype":"error_during_execution","is_error":true,"terminal_reason":"aborted_streaming","origin":{"kind":"peer"},"errors":["made-up failure"],"session_id":"session_1"}`,
		`{"type":"result","subtype":"error_max_turns","is_error":true,"errors":[],"session_id":"session_1"}`,
	)
	requireEvents(t, got, []Event{
		TurnEnded{Subtype: "success", TerminalReason: "completed", Result: "all done", Origin: "task_notification"},
		TurnEnded{
			Subtype:        "error_during_execution",
			IsError:        true,
			TerminalReason: "aborted_streaming",
			Origin:         "peer",
		},
		TurnEnded{Subtype: "error_max_turns", IsError: true},
	})
}

func TestUnknownLinesDoNotStopTranslation(t *testing.T) {
	t.Parallel()
	badLines := []string{
		`this is not json`,
		`{"type":"made_up_message","session_id":"session_1"}`,
		`{"type":"assistant","message":"a string where a message belongs","parent_tool_use_id":null}`,
		`{"type":"assistant","message":{"id":"msg_odd","role":"assistant","content":[{"type":"server_tool_use","id":"toolu_web","name":"web_search"}]},"parent_tool_use_id":null}`,
		`{"type":"system","subtype":"made_up_subtype","session_id":"session_1"}`,
	}
	tr := newTranslator()
	lines := append([]string{}, badLines...)
	lines = append(
		lines,
		`{"type":"stream_event","event":{"type":"message_start","message":{"id":"msg_after","type":"message","role":"assistant","content":[]}},"parent_tool_use_id":null,"session_id":"session_1"}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"after the unknowns"}},"parent_tool_use_id":null,"session_id":"session_1"}`,
	)
	got := translateLines(t, tr, lines...)
	if len(got) != len(badLines)+1 {
		t.Fatalf("got %d events, want %d: %#v", len(got), len(badLines)+1, got)
	}
	for index, line := range badLines {
		unknown, ok := got[index].(Unknown)
		if !ok {
			t.Fatalf("event %d = %#v, want Unknown", index, got[index])
		}
		if !bytes.Equal(unknown.Line, []byte(line)) {
			t.Errorf("event %d Line = %q, want %q", index, unknown.Line, line)
		}
		if unknown.Err == nil {
			t.Errorf("event %d Unknown has no Err", index)
		}
	}
	requireEvents(t, got[len(badLines):], []Event{
		TextDelta{MessageID: "msg_after", Index: 0, Text: "after the unknowns"},
	})
}

func TestControlEnvelopesAndNonInitSystemYieldNothing(t *testing.T) {
	t.Parallel()
	tr := newTranslator()
	got := translateLines(
		t,
		tr,
		`{"type":"control_request","request_id":"req_1","request":{"subtype":"can_use_tool","tool_name":"Write","input":{},"tool_use_id":"toolu_1"}}`,
		`{"type":"control_response","response":{"subtype":"success","request_id":"req_1"}}`,
		`{"type":"control_cancel_request","request_id":"req_1"}`,
		`{"type":"system","subtype":"status","status":"requesting","session_id":"session_1"}`,
		`{"type":"command_lifecycle","command_uuid":"uuid_1","state":"queued","session_id":"session_1"}`,
		`{"type":"user","isReplay":true,"message":{"role":"user","content":"replayed words"},"parent_tool_use_id":null,"uuid":"uuid_2","session_id":"session_1"}`,
	)
	requireEvents(t, got, []Event{})
}
