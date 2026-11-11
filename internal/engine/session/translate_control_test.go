package session

import (
	"bytes"
	"encoding/json"
	"testing"
)

// translateFixture returns the events of a capture fixture and fails the test if any line is Unknown.
func translateFixture(t *testing.T, name string) []Event {
	t.Helper()
	tr := newTranslator()
	lines := fixtureLines(t, name)
	events := make([]Event, 0, len(lines))
	for _, line := range lines {
		events = append(events, tr.translate(line)...)
	}
	for _, event := range events {
		if unknown, ok := event.(Unknown); ok {
			t.Fatalf("fixture %s: Unknown: %v\n%s", name, unknown.Err, unknown.Line)
		}
	}
	return events
}

func TestQuestionFixtureYieldsQuestionRequestAfterToolCall(t *testing.T) {
	t.Parallel()
	events := translateFixture(t, "question")
	requestIndex := -1
	endIndex := -1
	toolCallIndex := -1
	for index, event := range events {
		switch typed := event.(type) {
		case Block:
			if typed.Kind == BlockToolCall && typed.ToolUseID == "toolu_1" {
				toolCallIndex = index
			}
		case Request:
			requestIndex = index
			if typed.Kind != RequestQuestion || typed.RequestID != "req_3" || typed.ToolUseID != "toolu_1" ||
				typed.ToolName != "AskUserQuestion" || typed.Subtype != "can_use_tool" || typed.Refused {
				t.Errorf("request = %#v, want question req_3 for toolu_1 AskUserQuestion", typed)
			}
			requireQuestionInput(t, typed.Input)
		case TurnEnded:
			endIndex = index
		}
	}
	if requestIndex < 0 {
		t.Fatalf("fixture yields no Request: %#v", events)
	}
	if toolCallIndex < 0 || requestIndex <= toolCallIndex || endIndex <= requestIndex {
		t.Errorf("tool call at %d, request at %d, turn end at %d; want tool call < request < turn end",
			toolCallIndex, requestIndex, endIndex)
	}
}

func requireQuestionInput(t *testing.T, input json.RawMessage) {
	t.Helper()
	want := `{"questions":[{"question":"redacted","header":"redacted","options":[{"label":"redacted",` +
		`"description":"redacted"}],"multiSelect":false}]}`
	if string(input) != want {
		t.Errorf("question input = %s, want %s", input, want)
	}
}

func TestPermissionFixturesYieldPermissionRequests(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"permission-allow", "permission-deny"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var got []Event
			for _, event := range translateFixture(t, name) {
				if _, ok := event.(Request); ok {
					got = append(got, event)
				}
			}
			want := []Event{Request{
				Kind:      RequestPermission,
				RequestID: "req_3",
				ToolUseID: "toolu_1",
				ToolName:  "Write",
				Subtype:   "can_use_tool",
				Input:     json.RawMessage(`{"file_path":"redacted","content":"redacted"}`),
			}}
			requireEvents(t, got, want)
		})
	}
}

func TestPermissionRequestCarriesItsOwnFields(t *testing.T) {
	t.Parallel()
	tr := newTranslator()
	got := translateLines(t, tr,
		`{"type":"control_request","request_id":"req_perm_7","request":{"subtype":"can_use_tool",`+
			`"tool_name":"Edit","display_name":"made-up display","input":{"file_path":"made-up-path"},`+
			`"description":"made-up description","tool_use_id":"toolu_perm_7"}}`,
	)
	requireEvents(t, got, []Event{Request{
		Kind:      RequestPermission,
		RequestID: "req_perm_7",
		ToolUseID: "toolu_perm_7",
		ToolName:  "Edit",
		Subtype:   "can_use_tool",
		Input:     json.RawMessage(`{"file_path":"made-up-path"}`),
	}})
}

func TestAskUserQuestionWithMadeUpFieldsIsQuestion(t *testing.T) {
	t.Parallel()
	tr := newTranslator()
	got := translateLines(t, tr,
		`{"type":"control_request","request_id":"req_ask_8","request":{"subtype":"can_use_tool",`+
			`"tool_name":"AskUserQuestion","input":{"questions":[{"question":"made-up question"}]},`+
			`"tool_use_id":"toolu_ask_8"}}`,
	)
	requireEvents(t, got, []Event{Request{
		Kind:      RequestQuestion,
		RequestID: "req_ask_8",
		ToolUseID: "toolu_ask_8",
		ToolName:  "AskUserQuestion",
		Subtype:   "can_use_tool",
		Input:     json.RawMessage(`{"questions":[{"question":"made-up question"}]}`),
	}})
}

func TestElicitationRequestIsRefusedOther(t *testing.T) {
	t.Parallel()
	tr := newTranslator()
	got := translateLines(t, tr,
		`{"type":"control_request","request_id":"req_elicit_4","request":{"subtype":"elicitation",`+
			`"mcp_server_name":"made-up-server","message":"made-up message"}}`,
	)
	requireEvents(t, got, []Event{Request{
		Kind:      RequestOther,
		RequestID: "req_elicit_4",
		Subtype:   "elicitation",
		Refused:   true,
	}})
}

func TestCanUseToolWithMalformedBodyIsRefusedOther(t *testing.T) {
	t.Parallel()
	tr := newTranslator()
	got := translateLines(t, tr,
		`{"type":"control_request","request_id":"req_bad_5","request":{"subtype":"can_use_tool",`+
			`"tool_name":42,"input":{},"tool_use_id":"toolu_bad_5"}}`,
	)
	requireEvents(t, got, []Event{Request{
		Kind:      RequestOther,
		RequestID: "req_bad_5",
		Subtype:   "can_use_tool",
		Refused:   true,
	}})
}

func TestControlRequestWithoutRequestIDIsUnknown(t *testing.T) {
	t.Parallel()
	line := `{"type":"control_request","request":{"subtype":"can_use_tool","tool_name":"Write","input":{},` +
		`"tool_use_id":"toolu_none_6"}}`
	tr := newTranslator()
	got := tr.translate([]byte(line))
	if len(got) != 1 {
		t.Fatalf("got %d events, want one Unknown: %#v", len(got), got)
	}
	unknown, ok := got[0].(Unknown)
	if !ok {
		t.Fatalf("event = %#v, want Unknown", got[0])
	}
	if !bytes.Equal(unknown.Line, []byte(line)) || unknown.Err == nil {
		t.Errorf("Unknown = %q with err %v, want the line with an error", unknown.Line, unknown.Err)
	}
}

func TestCancelRequestGivesRequestCancelled(t *testing.T) {
	t.Parallel()
	tr := newTranslator()
	got := translateLines(t, tr, `{"type":"control_cancel_request","request_id":"req_cancel_9"}`)
	requireEvents(t, got, []Event{RequestCancelled{RequestID: "req_cancel_9"}})
}

func TestControlResponseGivesNothing(t *testing.T) {
	t.Parallel()
	tr := newTranslator()
	got := translateLines(t, tr,
		`{"type":"control_response","response":{"subtype":"success","request_id":"req_10",`+
			`"response":{"still_queued":[]}}}`,
	)
	requireEvents(t, got, []Event{})
}

func TestNoticesCarryTheirText(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		line string
		want Notice
	}{
		{
			name: "compaction",
			line: `{"type":"system","subtype":"compact_boundary","compact_metadata":{"trigger":"made-up-trigger",` +
				`"pre_tokens":1234},"session_id":"session_1","uuid":"uuid_c"}`,
			want: Notice{Kind: NoticeCompaction, Text: "made-up-trigger"},
		},
		{
			name: "api retry",
			line: `{"type":"system","subtype":"api_retry","attempt":2,"max_retries":5,"retry_delay_ms":700,` +
				`"error_status":503,"error":"made-up overload text","session_id":"session_1","uuid":"uuid_r"}`,
			want: Notice{Kind: NoticeAPIRetry, Text: "made-up overload text"},
		},
		{
			name: "auth status with error",
			line: `{"type":"auth_status","isAuthenticating":false,"output":["made-up output"],` +
				`"error":"made-up auth failure","session_id":"session_1","uuid":"uuid_a"}`,
			want: Notice{Kind: NoticeAuthStatus, Text: "made-up auth failure"},
		},
		{
			name: "auth status output",
			line: `{"type":"auth_status","isAuthenticating":true,"output":["made-up line one","made-up line two"],` +
				`"session_id":"session_1","uuid":"uuid_b"}`,
			want: Notice{Kind: NoticeAuthStatus, Text: "made-up line one\nmade-up line two"},
		},
		{
			name: "informational",
			line: `{"type":"system","subtype":"informational","content":"made-up banner text","level":"warning",` +
				`"session_id":"session_1","uuid":"uuid_i"}`,
			want: Notice{Kind: NoticeInformational, Text: "made-up banner text"},
		},
		{
			name: "failed hook",
			line: `{"type":"system","subtype":"hook_response","hook_id":"hook_h","hook_name":"PreToolUse:made-up",` +
				`"hook_event":"PreToolUse","output":"made-up output","stdout":"","stderr":"made-up stderr",` +
				`"exit_code":2,"outcome":"error","session_id":"session_1","uuid":"uuid_h"}`,
			want: Notice{Kind: NoticeHookFailed, Text: "PreToolUse:made-up"},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			tr := newTranslator()
			got := tr.translate([]byte(testCase.line))
			if len(got) != 1 {
				t.Fatalf("got %d events, want one Notice: %#v", len(got), got)
			}
			notice, ok := got[0].(Notice)
			if !ok {
				t.Fatalf("event = %#v, want Notice", got[0])
			}
			if notice.Kind != testCase.want.Kind || notice.Text != testCase.want.Text {
				t.Errorf("notice = kind %q text %q, want kind %q text %q",
					notice.Kind, notice.Text, testCase.want.Kind, testCase.want.Text)
			}
			if !bytes.Equal(notice.Line, []byte(testCase.line)) {
				t.Errorf("Line = %q, want the whole line", notice.Line)
			}
		})
	}
}

func TestSuccessfulHookResponseGivesNothing(t *testing.T) {
	t.Parallel()
	tr := newTranslator()
	got := translateLines(t, tr,
		`{"type":"system","subtype":"hook_response","hook_id":"hook_ok","hook_name":"PreToolUse:made-up-ok",`+
			`"hook_event":"PreToolUse","output":"","stdout":"","stderr":"","exit_code":0,"outcome":"success",`+
			`"session_id":"session_1","uuid":"uuid_ok"}`,
	)
	requireEvents(t, got, []Event{})
}

func TestBackgroundTasksChangedGivesBackgroundTasksInOrder(t *testing.T) {
	t.Parallel()
	tr := newTranslator()
	got := translateLines(t, tr,
		`{"type":"system","subtype":"background_tasks_changed","tasks":[`+
			`{"task_id":"task_made_1","task_type":"local_bash","description":"made-up sleep"},`+
			`{"task_id":"task_made_2","task_type":"local_agent","description":"made-up review"}],`+
			`"session_id":"session_1","uuid":"uuid_t1"}`,
		`{"type":"system","subtype":"background_tasks_changed","tasks":[],"session_id":"session_1","uuid":"uuid_t2"}`,
	)
	requireEvents(t, got, []Event{
		BackgroundTasks{Tasks: []BackgroundTask{
			{TaskID: "task_made_1", TaskType: "local_bash", Description: "made-up sleep"},
			{TaskID: "task_made_2", TaskType: "local_agent", Description: "made-up review"},
		}},
		BackgroundTasks{Tasks: []BackgroundTask{}},
	})
}
