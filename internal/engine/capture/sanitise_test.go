package capture

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// sanitiseWant runs one line through s and fails unless the result decodes to the same JSON value as
// want. Object key order is not compared.
func sanitiseWant(t *testing.T, s *Sanitiser, line, want string) {
	t.Helper()
	clean, keep, err := s.Line([]byte(line))
	if err != nil {
		t.Fatalf("Line(%s): %v", line, err)
	}
	if !keep {
		t.Fatalf("Line(%s) dropped the line", line)
	}
	if got, wantValue := sanitiseDecode(t, clean), sanitiseDecode(t, []byte(want)); !reflect.DeepEqual(got, wantValue) {
		t.Errorf("Line(%s)\n got %s\nwant %s", line, clean, want)
	}
}

func sanitiseDecode(t *testing.T, raw []byte) any {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return value
}

// sanitisePair is one input line and the line it must sanitize to. A case chains pairs through one
// Sanitiser, so ids stay consistent across them.
type sanitisePair struct {
	line string
	want string
}

// sanitiseRowCase covers one allow-list row. Deleting the row, or giving its id row another prefix,
// makes the case fail.
type sanitiseRowCase struct {
	row   string
	pairs []sanitisePair
}

// sanitiseKeepCase plants a distinctive value at a keep path, which must come out unchanged.
func sanitiseKeepCase(row, line string) sanitiseRowCase {
	return sanitiseRowCase{row: row, pairs: []sanitisePair{{line: line, want: line}}}
}

// sanitiseKnownCase covers a known-list row. template holds one %s for the value: the listed value
// comes out unchanged, and an unlisted one is replaced by "redacted".
func sanitiseKnownCase(row, template, listed, unlisted string) sanitiseRowCase {
	return sanitiseRowCase{row: row, pairs: []sanitisePair{
		{line: fmt.Sprintf(template, listed), want: fmt.Sprintf(template, listed)},
		{line: fmt.Sprintf(template, unlisted), want: fmt.Sprintf(template, "redacted")},
	}}
}

// sanitiseIDCase sends the first id, the second id, then the first id again through one Sanitiser.
// template holds one %s for the id.
func sanitiseIDCase(row, template, prefix, first, second string) sanitiseRowCase {
	return sanitiseRowCase{row: row, pairs: []sanitisePair{
		{line: fmt.Sprintf(template, first), want: fmt.Sprintf(template, prefix+"_1")},
		{line: fmt.Sprintf(template, second), want: fmt.Sprintf(template, prefix+"_2")},
		{line: fmt.Sprintf(template, first), want: fmt.Sprintf(template, prefix+"_1")},
	}}
}

func sanitiseKeepRows() []sanitiseRowCase {
	return []sanitiseRowCase{
		sanitiseKeepCase("type", `{"type":"system"}`),
		sanitiseKeepCase("subtype", `{"subtype":"init"}`),
		sanitiseKeepCase("permissionMode", `{"permissionMode":"plan"}`),
		sanitiseKeepCase("claude_code_version", `{"claude_code_version":"2.1.321"}`),
		sanitiseKnownCase("model", `{"model":"%s"}`, "claude-haiku-5-5", "claude-opus-5-5"),
		sanitiseKeepCase("status", `{"status":"allowed"}`),
		sanitiseKeepCase("state", `{"state":"idle"}`),
		sanitiseKeepCase("is_error", `{"is_error":true}`),
		sanitiseKeepCase("num_turns", `{"num_turns":7}`),
		sanitiseKeepCase("stop_reason", `{"stop_reason":"end_turn"}`),
		sanitiseKeepCase("terminal_reason", `{"terminal_reason":"completed"}`),
		sanitiseKeepCase("message.type", `{"message":{"type":"message"}}`),
		sanitiseKeepCase("message.role", `{"message":{"role":"assistant"}}`),
		sanitiseKnownCase("message.model", `{"message":{"model":"%s"}}`, "claude-haiku-5-5", "claude-opus-5-5"),
		sanitiseKeepCase("message.stop_reason", `{"message":{"stop_reason":"end_turn"}}`),
		sanitiseKeepCase("message.content.*.type", `{"message":{"content":[{"type":"text"}]}}`),
		sanitiseKnownCase("message.content.*.name", `{"message":{"content":[{"name":"%s"}]}}`, "Write", "Bash"),
		sanitiseKeepCase("message.content.*.is_error", `{"message":{"content":[{"is_error":true}]}}`),
	}
}

func sanitiseEventRows() []sanitiseRowCase {
	return []sanitiseRowCase{
		sanitiseKeepCase("event.type", `{"event":{"type":"message_start"}}`),
		sanitiseKeepCase("event.index", `{"event":{"index":2}}`),
		sanitiseKeepCase("event.message.type", `{"event":{"message":{"type":"message"}}}`),
		sanitiseKeepCase("event.message.role", `{"event":{"message":{"role":"assistant"}}}`),
		sanitiseKnownCase(
			"event.message.model", `{"event":{"message":{"model":"%s"}}}`, "claude-haiku-5-5", "claude-opus-5-5",
		),
		sanitiseKeepCase("event.content_block.type", `{"event":{"content_block":{"type":"text"}}}`),
		sanitiseKnownCase("event.content_block.name", `{"event":{"content_block":{"name":"%s"}}}`, "Write", "Bash"),
		sanitiseKeepCase("event.delta.type", `{"event":{"delta":{"type":"text_delta"}}}`),
		sanitiseKeepCase("event.delta.stop_reason", `{"event":{"delta":{"stop_reason":"end_turn"}}}`),
	}
}

func sanitiseRequestRows() []sanitiseRowCase {
	return []sanitiseRowCase{
		sanitiseKnownCase(
			"permission_denials.*.tool_name", `{"permission_denials":[{"tool_name":"%s"}]}`, "Write", "Bash",
		),
		sanitiseKeepCase("request.subtype", `{"request":{"subtype":"can_use_tool"}}`),
		sanitiseKnownCase("request.tool_name", `{"request":{"tool_name":"%s"}}`, "Write", "Bash"),
		sanitiseKeepCase("request.mode", `{"request":{"mode":"plan"}}`),
		sanitiseKeepCase("request.requires_user_interaction", `{"request":{"requires_user_interaction":true}}`),
		sanitiseKeepCase("request.supportedDialogKinds.*", `{"request":{"supportedDialogKinds":["confirm"]}}`),
		sanitiseKeepCase("response.subtype", `{"response":{"subtype":"success"}}`),
		sanitiseKeepCase("response.response.behavior", `{"response":{"response":{"behavior":"allow"}}}`),
		sanitiseKeepCase("response.response.mode", `{"response":{"response":{"mode":"plan"}}}`),
		sanitiseKnownCase("request.display_name", `{"request":{"display_name":"%s"}}`, "Write", "Bash"),
		sanitiseKeepCase(
			"request.permission_suggestions.*.type",
			`{"request":{"permission_suggestions":[{"type":"setMode"}]}}`,
		),
		sanitiseKeepCase(
			"request.permission_suggestions.*.mode",
			`{"request":{"permission_suggestions":[{"mode":"acceptEdits"}]}}`,
		),
		sanitiseKeepCase(
			"request.permission_suggestions.*.destination",
			`{"request":{"permission_suggestions":[{"destination":"session"}]}}`,
		),
		sanitiseKeepCase(
			"response.response.current_permission_mode",
			`{"response":{"response":{"current_permission_mode":"bypassPermissions"}}}`,
		),
	}
}

func sanitiseIDRows() []sanitiseRowCase {
	const uuidFirst, uuidSecond = "00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000002"
	return []sanitiseRowCase{
		sanitiseIDCase("uuid", `{"uuid":"%s"}`, "uuid", uuidFirst, uuidSecond),
		sanitiseIDCase("command_uuid", `{"command_uuid":"%s"}`, "uuid", uuidFirst, uuidSecond),
		sanitiseIDCase("session_id", `{"session_id":"%s"}`, "session", "sess-a", "sess-b"),
		sanitiseIDCase("request_id", `{"request_id":"%s"}`, "req", "req-a", "req-b"),
		sanitiseIDCase("response.request_id", `{"response":{"request_id":"%s"}}`, "req", "req-a", "req-b"),
		sanitiseIDCase("message.id", `{"message":{"id":"%s"}}`, "msg", "msg-a", "msg-b"),
		sanitiseIDCase("event.message.id", `{"event":{"message":{"id":"%s"}}}`, "msg", "msg-a", "msg-b"),
		sanitiseIDCase("parent_tool_use_id", `{"parent_tool_use_id":"%s"}`, "toolu", "toolu-a", "toolu-b"),
		sanitiseIDCase("message.content.*.id", `{"message":{"content":[{"id":"%s"}]}}`, "toolu", "toolu-a", "toolu-b"),
		sanitiseIDCase(
			"message.content.*.tool_use_id",
			`{"message":{"content":[{"tool_use_id":"%s"}]}}`,
			"toolu", "toolu-a", "toolu-b",
		),
		sanitiseIDCase(
			"event.content_block.id", `{"event":{"content_block":{"id":"%s"}}}`, "toolu", "toolu-a", "toolu-b",
		),
		sanitiseIDCase(
			"permission_denials.*.tool_use_id",
			`{"permission_denials":[{"tool_use_id":"%s"}]}`,
			"toolu", "toolu-a", "toolu-b",
		),
		sanitiseIDCase("request.tool_use_id", `{"request":{"tool_use_id":"%s"}}`, "toolu", "toolu-a", "toolu-b"),
		sanitiseIDCase("user_message_uuid", `{"user_message_uuid":"%s"}`, "uuid", uuidFirst, uuidSecond),
		sanitiseIDCase("user_message_uuids.*", `{"user_message_uuids":["%s"]}`, "uuid", uuidFirst, uuidSecond),
	}
}

func sanitiseDataRows() []sanitiseRowCase {
	return []sanitiseRowCase{
		{row: "model_usage_since_last_result", pairs: []sanitisePair{
			{
				line: `{"model_usage_since_last_result":{"claude-opus-5-5":{"inputTokens":10}}}`,
				want: `{"model_usage_since_last_result":{"key_1":{"inputTokens":0}}}`,
			},
			{
				line: `{"model_usage_since_last_result":{"claude-haiku-5-5":{"inputTokens":3}}}`,
				want: `{"model_usage_since_last_result":{"key_2":{"inputTokens":0}}}`,
			},
			{
				line: `{"model_usage_since_last_result":{"claude-opus-5-5":{"inputTokens":4}}}`,
				want: `{"model_usage_since_last_result":{"key_1":{"inputTokens":0}}}`,
			},
		}},
		{row: "tool_use_result.answers", pairs: []sanitisePair{
			{
				line: `{"type":"user","tool_use_result":{"answers":{"Which?":"A"}}}`,
				want: `{"type":"user","tool_use_result":{"answers":{"key_1":"redacted"}}}`,
			},
			{
				line: `{"type":"user","tool_use_result":{"answers":{"Name?":"Ada"}}}`,
				want: `{"type":"user","tool_use_result":{"answers":{"key_2":"redacted"}}}`,
			},
			{
				line: `{"type":"user","tool_use_result":{"answers":{"Which?":"A"}}}`,
				want: `{"type":"user","tool_use_result":{"answers":{"key_1":"redacted"}}}`,
			},
		}},
		{row: "wire_tool_inputs", pairs: []sanitisePair{
			{
				line: `{"type":"assistant","wire_tool_inputs":{"toolu_a":{"content":"x"}}}`,
				want: `{"type":"assistant","wire_tool_inputs":{"key_1":{"content":"redacted"}}}`,
			},
			{
				line: `{"type":"assistant","wire_tool_inputs":{"toolu_b":{"content":"y"}}}`,
				want: `{"type":"assistant","wire_tool_inputs":{"key_2":{"content":"redacted"}}}`,
			},
			{
				line: `{"type":"assistant","wire_tool_inputs":{"toolu_a":{"content":"x"}}}`,
				want: `{"type":"assistant","wire_tool_inputs":{"key_1":{"content":"redacted"}}}`,
			},
		}},
		{row: "modelUsage", pairs: []sanitisePair{
			{
				line: `{"modelUsage":{"claude-opus-5-5":{"inputTokens":10}}}`,
				want: `{"modelUsage":{"key_1":{"inputTokens":0}}}`,
			},
			{
				line: `{"modelUsage":{"claude-haiku-5-5":{"inputTokens":3}}}`,
				want: `{"modelUsage":{"key_2":{"inputTokens":0}}}`,
			},
			{
				line: `{"modelUsage":{"claude-opus-5-5":{"inputTokens":4}}}`,
				want: `{"modelUsage":{"key_1":{"inputTokens":0}}}`,
			},
		}},
		{row: "subagent_stats.by_type", pairs: []sanitisePair{
			{
				line: `{"subagent_stats":{"by_type":{"general-purpose":{"count":2}}}}`,
				want: `{"subagent_stats":{"by_type":{"key_1":{"count":0}}}}`,
			},
			{
				line: `{"subagent_stats":{"by_type":{"Explore":{"count":1},"general-purpose":{"count":3}}}}`,
				want: `{"subagent_stats":{"by_type":{"key_2":{"count":0},"key_1":{"count":0}}}}`,
			},
		}},
		{row: "response.response.updatedInput.answers", pairs: []sanitisePair{
			{
				line: `{"response":{"response":{"updatedInput":{"answers":{"Q1":"Postgres"}}}}}`,
				want: `{"response":{"response":{"updatedInput":{"answers":{"key_1":"redacted"}}}}}`,
			},
			{
				line: `{"response":{"response":{"updatedInput":{"answers":{"Q2":"Ada"}}}}}`,
				want: `{"response":{"response":{"updatedInput":{"answers":{"key_2":"redacted"}}}}}`,
			},
			{
				line: `{"response":{"response":{"updatedInput":{"answers":{"Q1":"Postgres"}}}}}`,
				want: `{"response":{"response":{"updatedInput":{"answers":{"key_1":"redacted"}}}}}`,
			},
		}},
	}
}

func sanitiseDropRows() []sanitiseRowCase {
	return []sanitiseRowCase{
		{row: "credential_expires_at", pairs: []sanitisePair{
			{
				line: `{"type":"result","credential_expires_at":"soon","num_turns":1}`,
				want: `{"type":"result","num_turns":1}`,
			},
			{
				line: `{"type":"result","num_turns":1,"credential_expires_at":"soon","total_cost_usd":0.5}`,
				want: `{"type":"result","num_turns":1,"total_cost_usd":0}`,
			},
			{
				line: `{"type":"result","num_turns":1,"credential_expires_at":"soon"}`,
				want: `{"type":"result","num_turns":1}`,
			},
			{line: `{"credential_expires_at":"soon"}`, want: `{}`},
			{
				line: `{"credential_expires_at":{"uuid":"00000000-0000-4000-8000-000000000009"}}`,
				want: `{}`,
			},
			{
				line: `{"uuid":"00000000-0000-4000-8000-000000000001"}`,
				want: `{"uuid":"uuid_1"}`,
			},
		}},
		{row: "response.response.account", pairs: []sanitisePair{
			{
				line: `{"type":"control_response","response":{"subtype":"success","response":{` +
					`"account":{"email":"user@example.test","subscriptionType":"x"},"pid":1}}}`,
				want: `{"type":"control_response","response":{"subtype":"success","response":{"account":{},"pid":0}}}`,
			},
			{
				line: `{"response":{"response":{"account":"user@example.test"}}}`,
				want: `{"response":{"response":{"account":"redacted"}}}`,
			},
			{
				line: `{"response":{"response":{"account":["user@example.test"]}}}`,
				want: `{"response":{"response":{"account":["redacted"]}}}`,
			},
			{
				line: `{"response":{"response":{"account":{}}}}`,
				want: `{"response":{"response":{"account":{}}}}`,
			},
			{
				line: `{"response":{"response":{"account":{"uuid":"00000000-0000-4000-8000-000000000009"}}}}`,
				want: `{"response":{"response":{"account":{}}}}`,
			},
			{
				line: `{"uuid":"00000000-0000-4000-8000-000000000001"}`,
				want: `{"uuid":"uuid_1"}`,
			},
		}},
	}
}

// sanitiseRowCases returns one case per allow-list row.
func sanitiseRowCases() []sanitiseRowCase {
	var cases []sanitiseRowCase
	cases = append(cases, sanitiseKeepRows()...)
	cases = append(cases, sanitiseEventRows()...)
	cases = append(cases, sanitiseRequestRows()...)
	cases = append(cases, sanitiseIDRows()...)
	cases = append(cases, sanitiseDataRows()...)
	return append(cases, sanitiseDropRows()...)
}

func TestSanitiserAllowListRows(t *testing.T) {
	for _, tc := range sanitiseRowCases() {
		t.Run(tc.row, func(t *testing.T) {
			s := NewSanitiser()
			for _, pair := range tc.pairs {
				sanitiseWant(t, s, pair.line, pair.want)
			}
		})
	}
}

// TestSanitiserEveryAllowListRowHasACase fails when a row is added to the allow-list without a case,
// or a case names a row the allow-list does not hold.
func TestSanitiserEveryAllowListRowHasACase(t *testing.T) {
	cases := map[string]bool{}
	for _, tc := range sanitiseRowCases() {
		if cases[tc.row] {
			t.Errorf("two cases name row %s", tc.row)
		}
		cases[tc.row] = true
	}
	entries := NewSanitiser().entries
	for _, entry := range entries {
		name := strings.Join(entry.path, ".")
		if !cases[name] {
			t.Errorf("allow-list row %s has no case", name)
		}
		delete(cases, name)
	}
	for name := range cases {
		t.Errorf("case %s names no allow-list row", name)
	}
}

// TestSanitiserKnownListsHoldOnlyAtTheirOwnPaths checks that a model id is not kept at a tool-name
// path, a tool name is not kept at a model path, and an mcp__ style name is replaced.
func TestSanitiserKnownListsHoldOnlyAtTheirOwnPaths(t *testing.T) {
	sanitiseWant(t, NewSanitiser(),
		`{"model":"Write","message":{"model":"AskUserQuestion"},"request":{"tool_name":"claude-haiku-5-5"}}`,
		`{"model":"redacted","message":{"model":"redacted"},"request":{"tool_name":"redacted"}}`)
	sanitiseWant(t, NewSanitiser(),
		`{"event":{"content_block":{"name":"mcp__notes__search"}}}`,
		`{"event":{"content_block":{"name":"redacted"}}}`)
}

// TestSanitiserKnownPathsMakeUpNonStringValues checks that a known-list path follows the ordinary
// JSON-type rules for a value that is not a string.
func TestSanitiserKnownPathsMakeUpNonStringValues(t *testing.T) {
	sanitiseWant(t, NewSanitiser(),
		`{"model":7,"request":{"display_name":true},"event":{"content_block":{"name":null}}}`,
		`{"model":0,"request":{"display_name":false},"event":{"content_block":{"name":null}}}`)
	sanitiseWant(t, NewSanitiser(),
		`{"model":["claude-haiku-5-5"],"request":{"tool_name":{"nested":"Write"}}}`,
		`{"model":["redacted"],"request":{"tool_name":{"nested":"redacted"}}}`)
}

func TestSanitiserKeepsListedFieldsAndRedactsTheRest(t *testing.T) {
	line := `{"type":"system","subtype":"init","cwd":"/home/user/work","session_id":"sess-real-1",` +
		`"model":"claude-haiku-5-5","permissionMode":"default","claude_code_version":"2.1.321",` +
		`"tools":["Bash","Read"],"capabilities":["streaming"],"apiKeySource":"user@example.test",` +
		`"mcp_servers":[{"name":"notes","status":"connected","url":"http://192.0.2.10:8080"}],` +
		`"duration_ms":12345678901234567890,"total_cost_usd":0.0100}`
	want := `{"type":"system","subtype":"init","cwd":"redacted","session_id":"session_1",` +
		`"model":"claude-haiku-5-5","permissionMode":"default","claude_code_version":"2.1.321",` +
		`"tools":["redacted"],"capabilities":["redacted"],"apiKeySource":"redacted",` +
		`"mcp_servers":[{"name":"redacted","status":"redacted","url":"redacted"}],` +
		`"duration_ms":0,"total_cost_usd":0}`
	sanitiseWant(t, NewSanitiser(), line, want)
}

func TestSanitiserMakesUpUnlistedValuesByJSONType(t *testing.T) {
	line := `{"type":"assistant","message":{"role":"assistant","model":"claude-sonnet-5-5","id":"msg-orig-1",` +
		`"content":[{"type":"text","text":"/home/user/notes host.example.test","citations":null,` +
		`"score":3.5,"flag":true},{"type":"tool_use","id":"toolu-orig-1","name":"Bash",` +
		`"input":{"command":"cat user@example.test","timeout":5000,"background":false}}],` +
		`"usage":{"input_tokens":12,"output_tokens":34,"cache_creation":{"ephemeral_5m_input_tokens":0}}},` +
		`"extra":[[{"deep":"/home/user/deep"}]]}`
	want := `{"type":"assistant","message":{"role":"assistant","model":"redacted","id":"msg_1",` +
		`"content":[{"type":"text","text":"redacted","citations":null,"score":0,"flag":false},` +
		`{"type":"tool_use","id":"toolu_1","name":"redacted",` +
		`"input":{"command":"redacted","timeout":0,"background":false}}],` +
		`"usage":{"input_tokens":0,"output_tokens":0,"cache_creation":{"ephemeral_5m_input_tokens":0}}},` +
		`"extra":[[{"deep":"redacted"}]]}`
	sanitiseWant(t, NewSanitiser(), line, want)
}

func TestSanitiserKeepAppliesToScalarsOnly(t *testing.T) {
	line := `{"capabilities":[{"path":"/home/user/x"},"streaming"],"request":{"tool_name":{"nested":"/home/user/y"}},` +
		`"event":{"type":{"nested":"/home/user/z"}},"model":["/home/user/a",{"b":"/home/user/b"}]}`
	want := `{"capabilities":[{"path":"redacted"}],"request":{"tool_name":{"nested":"redacted"}},` +
		`"event":{"type":{"nested":"redacted"}},"model":["redacted"]}`
	sanitiseWant(t, NewSanitiser(), line, want)
}

func TestSanitiserWildcardsMatchOnlyArrayPositions(t *testing.T) {
	line := `{"message":{"content":{"first":{"type":"/home/user/x"}}}}`
	want := `{"message":{"content":{"first":{"type":"redacted"}}}}`
	sanitiseWant(t, NewSanitiser(), line, want)
}

func TestSanitiserKeepsOnlyTheFirstElementOfUnnamedArrays(t *testing.T) {
	for _, tc := range []struct{ name, line, want string }{
		{name: "root array", line: `["Bash","Read","Write"]`, want: `["redacted"]`},
		{name: "field of the root object", line: `{"tools":["Bash","Read","Write"]}`, want: `{"tools":["redacted"]}`},
		{
			name: "inside another unnamed array",
			line: `{"tools":[["Bash","Read"],{"name":"Write"},"Edit"]}`,
			want: `{"tools":[["redacted"]]}`,
		},
		{name: "inside an object", line: `{"skills":{"list":[{"name":"a"},{"name":"b"}]}}`, want: `{"skills":{"list":[{"name":"redacted"}]}}`},
		{name: "empty array", line: `{"tools":[]}`, want: `{"tools":[]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sanitiseWant(t, NewSanitiser(), tc.line, tc.want)
		})
	}
}

func TestSanitiserMakesUpDurationsCostAndUsageNumbers(t *testing.T) {
	line := `{"type":"result","num_turns":7,"duration_ms":12345,"duration_api_ms":678,"total_cost_usd":0.25,` +
		`"usage":{"output_tokens":34,"iterations":[{"output_tokens":6}]},` +
		`"message":{"usage":{"cache_read_input_tokens":9,"iterations":[{"input_tokens":5}]}},` +
		`"event":{"index":2,"usage":{"input_tokens":12,"iterations":[{"input_tokens":5}]}}}`
	want := `{"type":"result","num_turns":7,"duration_ms":0,"duration_api_ms":0,"total_cost_usd":0,` +
		`"usage":{"output_tokens":0,"iterations":[{"output_tokens":0}]},` +
		`"message":{"usage":{"cache_read_input_tokens":0,"iterations":[{"input_tokens":0}]}},` +
		`"event":{"index":2,"usage":{"input_tokens":0,"iterations":[{"input_tokens":0}]}}}`
	sanitiseWant(t, NewSanitiser(), line, want)
}

func TestSanitiserRedactsCapabilityStringsAndKeepsOneElement(t *testing.T) {
	sanitiseWant(t, NewSanitiser(),
		`{"capabilities":["streaming","tools"]}`,
		`{"capabilities":["redacted"]}`)
	sanitiseWant(t, NewSanitiser(),
		`{"capabilities":[{"name":"streaming"},"tools"]}`,
		`{"capabilities":[{"name":"redacted"}]}`)
}

func TestSanitiserKeepsEveryElementOfNamedArrays(t *testing.T) {
	sanitiseWant(t, NewSanitiser(),
		`{"message":{"content":[{"type":"text"},{"type":"tool_use","name":"Write"}]}}`,
		`{"message":{"content":[{"type":"text"},{"type":"tool_use","name":"Write"}]}}`)
	sanitiseWant(t, NewSanitiser(),
		`{"permission_denials":[{"tool_name":"Write"},{"tool_name":"AskUserQuestion"}]}`,
		`{"permission_denials":[{"tool_name":"Write"},{"tool_name":"AskUserQuestion"}]}`)
	sanitiseWant(t, NewSanitiser(),
		`{"user_message_uuids":["00000000-0000-4000-8000-000000000001","00000000-0000-4000-8000-000000000002"]}`,
		`{"user_message_uuids":["uuid_1","uuid_2"]}`)
}

func TestSanitiserDroppedElementsConsumeNoIds(t *testing.T) {
	s := NewSanitiser()
	sanitiseWant(t, s,
		`{"tools":[{"name":"Bash"},{"uuid":"00000000-0000-4000-8000-000000000009"}]}`,
		`{"tools":[{"name":"redacted"}]}`)
	sanitiseWant(t, s,
		`{"uuid":"00000000-0000-4000-8000-000000000001"}`,
		`{"uuid":"uuid_1"}`)
}

func TestSanitiserIdsStayConsistentAcrossLines(t *testing.T) {
	s := NewSanitiser()
	sanitiseWant(
		t,
		s,
		`{"type":"system","subtype":"init","session_id":"sess-a","uuid":"00000000-0000-4000-8000-000000000001",`+
			`"request_id":"req-a"}`,
		`{"type":"system","subtype":"init","session_id":"session_1","uuid":"uuid_1","request_id":"req_1"}`,
	)
	sanitiseWant(
		t,
		s,
		`{"type":"result","session_id":"sess-a","uuid":"00000000-0000-4000-8000-000000000002",`+
			`"request_id":"req-b","num_turns":2}`,
		`{"type":"result","session_id":"session_1","uuid":"uuid_2","request_id":"req_2","num_turns":2}`,
	)
	sanitiseWant(
		t,
		s,
		`{"type":"control_response","response":{"subtype":"success","request_id":"req-a",`+
			`"response":{"behavior":"allow"}}}`,
		`{"type":"control_response","response":{"subtype":"success","request_id":"req_1",`+
			`"response":{"behavior":"allow"}}}`,
	)
}

func TestSanitiserSharesMessageAndToolIdsAcrossPaths(t *testing.T) {
	s := NewSanitiser()
	sanitiseWant(
		t,
		s,
		`{"type":"assistant","message":{"id":"msg-x","role":"assistant",`+
			`"content":[{"type":"tool_use","id":"toolu-x","name":"Write"}]}}`,
		`{"type":"assistant","message":{"id":"msg_1","role":"assistant",`+
			`"content":[{"type":"tool_use","id":"toolu_1","name":"Write"}]}}`,
	)
	sanitiseWant(
		t,
		s,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu-x",`+
			`"is_error":false,"content":"/home/user/out"}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1",`+
			`"is_error":false,"content":"redacted"}]}}`,
	)
	sanitiseWant(
		t,
		s,
		`{"type":"stream_event","event":{"type":"message_start","message":{"id":"msg-x","role":"assistant"}}}`,
		`{"type":"stream_event","event":{"type":"message_start","message":{"id":"msg_1","role":"assistant"}}}`,
	)
}

func TestSanitiserReplacesDataKeysInFirstAppearanceOrder(t *testing.T) {
	s := NewSanitiser()
	sanitiseWant(
		t,
		s,
		`{"type":"result","modelUsage":{"claude-opus-5-5":{"inputTokens":10,"costUSD":0.5},`+
			`"claude-haiku-5-5":{"inputTokens":3}}}`,
		`{"type":"result","modelUsage":{"key_1":{"inputTokens":0,"costUSD":0},"key_2":{"inputTokens":0}}}`,
	)
	sanitiseWant(
		t,
		s,
		`{"type":"result","modelUsage":{"claude-haiku-5-5":{"inputTokens":4}}}`,
		`{"type":"result","modelUsage":{"key_2":{"inputTokens":0}}}`,
	)
	sanitiseWant(
		t,
		s,
		`{"type":"control_response","response":{"subtype":"success","request_id":"req-c",`+
			`"response":{"behavior":"allow","updatedInput":{"answers":{"Which database?":"Postgres",`+
			`"Name?":"Ada Lovelace"}}}}}`,
		`{"type":"control_response","response":{"subtype":"success","request_id":"req_1",`+
			`"response":{"behavior":"allow","updatedInput":{"answers":{"key_3":"redacted","key_4":"redacted"}}}}}`,
	)
}

func TestSanitiserDropsRateLimitEvents(t *testing.T) {
	s := NewSanitiser()
	for _, line := range []string{
		`{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","resetsAt":1700000000}}`,
		`{"type":"system","type":"rate_limit_event"}`,
		`{"type":"rate_limit_event"}   `,
	} {
		clean, keep, err := s.Line([]byte(line))
		if err != nil {
			t.Fatalf("Line(%s): %v", line, err)
		}
		if keep || clean != nil {
			t.Errorf("Line(%s) kept the line as %s", line, clean)
		}
	}
	sanitiseWant(t, s,
		`{"type":"system","subtype":"init","rate_limit_event":"kept"}`,
		`{"type":"system","subtype":"init","rate_limit_event":"redacted"}`)
}

func TestSanitiserRejectsInvalidJSON(t *testing.T) {
	s := NewSanitiser()
	for _, line := range []string{
		``,
		`{"type":`,
		`not json`,
		`{"type":"system"} {"type":"result"}`,
		`{"type":"system"} x`,
		`{"type":"rate_limit_event"`,
		`{"type":"rate_limit_event","rate_limit_info":{"status":"allowed"}`,
		`{"type":"rate_limit_event"} x`,
		`{"type":"rate_limit_event"}}`,
		`{"type":"rate_limit_event"} {"type":"system"}`,
	} {
		if _, _, err := s.Line([]byte(line)); err == nil {
			t.Errorf("Line(%q) accepted invalid JSON", line)
		}
	}
}

func TestSanitiserDroppedMembersLeaveCompactJSON(t *testing.T) {
	for _, tc := range []struct{ name, line, want string }{
		{
			name: "first member",
			line: `{"credential_expires_at":"soon","type":"result","num_turns":1}`,
			want: `{"type":"result","num_turns":1}`,
		},
		{
			name: "middle member",
			line: `{"type":"result","credential_expires_at":"soon","num_turns":1}`,
			want: `{"type":"result","num_turns":1}`,
		},
		{
			name: "last member",
			line: `{"type":"result","num_turns":1,"credential_expires_at":"soon"}`,
			want: `{"type":"result","num_turns":1}`,
		},
		{name: "only member", line: `{"credential_expires_at":"soon"}`, want: `{}`},
		{
			name: "account object",
			line: `{"response":{"response":{"account":{"email":"user@example.test"},"pid":1}}}`,
			want: `{"response":{"response":{"account":{},"pid":0}}}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clean, keep, err := NewSanitiser().Line([]byte(tc.line))
			if err != nil {
				t.Fatalf("Line: %v", err)
			}
			if !keep {
				t.Fatal("Line dropped the line")
			}
			if string(clean) != tc.want {
				t.Errorf("clean = %s, want %s", clean, tc.want)
			}
		})
	}
}

func TestSanitiserDropAndEmptyRulesApplyOnlyAtTheirPaths(t *testing.T) {
	sanitiseWant(t, NewSanitiser(),
		`{"credential_expires_at":"soon","nested":{"credential_expires_at":"soon"},`+
			`"account":{"email":"user@example.test"},`+
			`"response":{"response":{"credential_expires_at":"soon","account":{"email":"user@example.test"}}}}`,
		`{"nested":{"credential_expires_at":"redacted"},"account":{"email":"redacted"},`+
			`"response":{"response":{"credential_expires_at":"redacted","account":{}}}}`)
}

func TestSanitiserWritesCompactJSONInInputOrder(t *testing.T) {
	clean, keep, err := NewSanitiser().Line([]byte("{ \"type\" : \"system\" , \"subtype\" : \"init\" }\n"))
	if err != nil {
		t.Fatalf("Line: %v", err)
	}
	if !keep {
		t.Fatal("Line dropped a system line")
	}
	if want := `{"type":"system","subtype":"init"}`; string(clean) != want {
		t.Errorf("clean = %s, want %s", clean, want)
	}
}
