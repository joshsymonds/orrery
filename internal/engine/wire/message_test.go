package wire

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// variantFixture is one hand-written line that decodes to a declared variant. name is the decoder
// map key or subtype it covers, want is a zero value of the concrete type, and field is a wire key
// (a dotted path for nested values) whose decoded value must print as value.
type variantFixture struct {
	name  string
	line  string
	want  any
	field string
	value string
}

func variantFixtures() []variantFixture {
	return []variantFixture{
		{
			name:  "assistant",
			want:  SDKAssistantMessage{},
			field: "request_id",
			value: "req_fixture",
			line:  `{"type":"assistant","uuid":"00000000-0000-4000-8000-000000000002","session_id":"00000000-0000-4000-8000-000000000001","message":{"id":"fixture","type":"message","role":"assistant","model":"fixture-model","content":[],"usage":{}},"parent_tool_use_id":null,"request_id":"req_fixture"}`,
		},
		{
			name:  "stream_event",
			want:  SDKPartialAssistantMessage{},
			field: "ttft_ms",
			value: "12",
			line:  `{"type":"stream_event","uuid":"00000000-0000-4000-8000-000000000002","session_id":"00000000-0000-4000-8000-000000000001","event":{"type":"content_block_stop","index":0},"ttft_ms":12}`,
		},
		{
			name:  "tool_progress",
			want:  SDKToolProgressMessage{},
			field: "tool_name",
			value: "Bash",
			line:  `{"type":"tool_progress","uuid":"00000000-0000-4000-8000-000000000002","session_id":"00000000-0000-4000-8000-000000000001","tool_use_id":"toolu_fixture","tool_name":"Bash","parent_tool_use_id":null,"elapsed_time_seconds":1.5}`,
		},
		{
			name:  "auth_status",
			want:  SDKAuthStatusMessage{},
			field: "output.0",
			value: "fixture output",
			line:  `{"type":"auth_status","uuid":"00000000-0000-4000-8000-000000000002","session_id":"00000000-0000-4000-8000-000000000001","isAuthenticating":true,"output":["fixture output"]}`,
		},
		{
			name:  "tool_use_summary",
			want:  SDKToolUseSummaryMessage{},
			field: "preceding_tool_use_ids",
			value: "[toolu_fixture]",
			line:  `{"type":"tool_use_summary","uuid":"00000000-0000-4000-8000-000000000002","session_id":"00000000-0000-4000-8000-000000000001","summary":"fixture summary","preceding_tool_use_ids":["toolu_fixture"]}`,
		},
		{
			name:  "rate_limit_event",
			want:  SDKRateLimitEvent{},
			field: "rate_limit_info.status",
			value: "allowed_warning",
			line:  `{"type":"rate_limit_event","uuid":"00000000-0000-4000-8000-000000000002","session_id":"00000000-0000-4000-8000-000000000001","rate_limit_info":{"status":"allowed_warning","utilization":0.5}}`,
		},
		{
			name:  "prompt_suggestion",
			want:  SDKPromptSuggestionMessage{},
			field: "suggestion",
			value: "fixture prompt",
			line:  `{"type":"prompt_suggestion","uuid":"00000000-0000-4000-8000-000000000002","session_id":"00000000-0000-4000-8000-000000000001","suggestion":"fixture prompt"}`,
		},
		{
			name:  "conversation_reset",
			want:  SDKConversationResetMessage{},
			field: "new_conversation_id",
			value: "00000000-0000-4000-8000-000000000003",
			line:  `{"type":"conversation_reset","uuid":"00000000-0000-4000-8000-000000000002","session_id":"00000000-0000-4000-8000-000000000001","new_conversation_id":"00000000-0000-4000-8000-000000000003"}`,
		},
		{
			name:  "command_lifecycle",
			want:  SDKCommandLifecycleMessage{},
			field: "command_uuid",
			value: "00000000-0000-4000-8000-000000000004",
			line:  `{"type":"command_lifecycle","uuid":"00000000-0000-4000-8000-000000000002","session_id":"00000000-0000-4000-8000-000000000001","command_uuid":"00000000-0000-4000-8000-000000000004","state":"completed"}`,
		},
		{
			name:  "init",
			want:  SDKSystemMessage{},
			field: "claude_code_version",
			value: "fixture-version",
			line:  `{"type":"system","subtype":"init","uuid":"00000000-0000-4000-8000-000000000002","session_id":"00000000-0000-4000-8000-000000000001","apiKeySource":"none","claude_code_version":"fixture-version","cwd":"/home/user","tools":["Bash"],"mcp_servers":[{"name":"fixture","status":"connected"}],"model":"fixture-model","permissionMode":"default","slash_commands":[],"output_style":"default","skills":[],"plugins":[{"name":"fixture","path":"/home/user/plugin"}]}`,
		},
		{
			name:  "compact_boundary",
			want:  SDKCompactBoundaryMessage{},
			field: "compact_metadata.pre_tokens",
			value: "100",
			line:  `{"type":"system","subtype":"compact_boundary","uuid":"00000000-0000-4000-8000-000000000002","session_id":"00000000-0000-4000-8000-000000000001","compact_metadata":{"trigger":"manual","pre_tokens":100}}`,
		},
		{
			name:  "status",
			want:  SDKStatusMessage{},
			field: "status",
			value: "compacting",
			line:  `{"type":"system","subtype":"status","uuid":"00000000-0000-4000-8000-000000000002","session_id":"00000000-0000-4000-8000-000000000001","status":"compacting","permissionMode":"default"}`,
		},
		{
			name:  "api_retry",
			want:  SDKAPIRetryMessage{},
			field: "retry_delay_ms",
			value: "500",
			line:  `{"type":"system","subtype":"api_retry","uuid":"00000000-0000-4000-8000-000000000002","session_id":"00000000-0000-4000-8000-000000000001","attempt":1,"max_retries":3,"retry_delay_ms":500,"error_status":529,"error":"fixture error"}`,
		},
		{
			name:  "control_request_progress",
			want:  SDKControlRequestProgressMessage{},
			field: "request_id",
			value: "req_fixture",
			line:  `{"type":"system","subtype":"control_request_progress","uuid":"00000000-0000-4000-8000-000000000002","session_id":"00000000-0000-4000-8000-000000000001","request_id":"req_fixture","status":"retrying","attempt":2,"max_retries":4,"retry_delay_ms":250}`,
		},
		{
			name:  "model_refusal_fallback",
			want:  SDKModelRefusalFallbackMessage{},
			field: "fallback_model",
			value: "fixture-fallback",
			line:  `{"type":"system","subtype":"model_refusal_fallback","uuid":"00000000-0000-4000-8000-000000000002","session_id":"00000000-0000-4000-8000-000000000001","trigger":"fixture","direction":"fallback","original_model":"fixture-model","fallback_model":"fixture-fallback","request_id":null,"content":"fixture content"}`,
		},
		{
			name:  "model_refusal_no_fallback",
			want:  SDKModelRefusalNoFallbackMessage{},
			field: "api_refusal_category",
			value: "fixture",
			line:  `{"type":"system","subtype":"model_refusal_no_fallback","uuid":"00000000-0000-4000-8000-000000000002","session_id":"00000000-0000-4000-8000-000000000001","original_model":"fixture-model","request_id":"req_fixture","api_refusal_category":"fixture","content":"fixture content"}`,
		},
		{
			name:  "local_command_output",
			want:  SDKLocalCommandOutputMessage{},
			field: "content",
			value: "fixture output",
			line:  `{"type":"system","subtype":"local_command_output","uuid":"00000000-0000-4000-8000-000000000002","session_id":"00000000-0000-4000-8000-000000000001","content":"fixture output"}`,
		},
		{
			name:  "hook_started",
			want:  SDKHookStartedMessage{},
			field: "hook_event",
			value: "PreToolUse",
			line:  `{"type":"system","subtype":"hook_started","uuid":"00000000-0000-4000-8000-000000000002","session_id":"00000000-0000-4000-8000-000000000001","hook_id":"hook_fixture","hook_name":"fixture hook","hook_event":"PreToolUse"}`,
		},
		{
			name:  "hook_progress",
			want:  SDKHookProgressMessage{},
			field: "stdout",
			value: "fixture stdout",
			line:  `{"type":"system","subtype":"hook_progress","uuid":"00000000-0000-4000-8000-000000000002","session_id":"00000000-0000-4000-8000-000000000001","hook_id":"hook_fixture","hook_name":"fixture hook","hook_event":"PreToolUse","stdout":"fixture stdout","stderr":"","output":""}`,
		},
		{
			name:  "hook_response",
			want:  SDKHookResponseMessage{},
			field: "exit_code",
			value: "0",
			line:  `{"type":"system","subtype":"hook_response","uuid":"00000000-0000-4000-8000-000000000002","session_id":"00000000-0000-4000-8000-000000000001","hook_id":"hook_fixture","hook_name":"fixture hook","hook_event":"PreToolUse","output":"","stdout":"","stderr":"","exit_code":0,"outcome":"success"}`,
		},
		{
			name:  "plugin_install",
			want:  SDKPluginInstallMessage{},
			field: "name",
			value: "fixture-plugin",
			line:  `{"type":"system","subtype":"plugin_install","uuid":"00000000-0000-4000-8000-000000000002","session_id":"00000000-0000-4000-8000-000000000001","status":"installed","name":"fixture-plugin"}`,
		},
		{
			name:  "task_notification",
			want:  SDKTaskNotificationMessage{},
			field: "output_file",
			value: "/home/user/out.txt",
			line:  `{"type":"system","subtype":"task_notification","uuid":"00000000-0000-4000-8000-000000000002","session_id":"00000000-0000-4000-8000-000000000001","task_id":"task_fixture","tool_use_id":null,"status":"completed","output_file":"/home/user/out.txt","summary":"fixture summary"}`,
		},
		{
			name:  "task_started",
			want:  SDKTaskStartedMessage{},
			field: "task_type",
			value: "local",
			line:  `{"type":"system","subtype":"task_started","uuid":"00000000-0000-4000-8000-000000000002","session_id":"00000000-0000-4000-8000-000000000001","task_id":"task_fixture","description":"fixture task","task_type":"local","skip_transcript":true}`,
		},
		{
			name:  "task_updated",
			want:  SDKTaskUpdatedMessage{},
			field: "patch.is_backgrounded",
			value: "true",
			line:  `{"type":"system","subtype":"task_updated","uuid":"00000000-0000-4000-8000-000000000002","session_id":"00000000-0000-4000-8000-000000000001","task_id":"task_fixture","patch":{"status":"running","is_backgrounded":true}}`,
		},
		{
			name:  "task_progress",
			want:  SDKTaskProgressMessage{},
			field: "usage.total_tokens",
			value: "10",
			line:  `{"type":"system","subtype":"task_progress","uuid":"00000000-0000-4000-8000-000000000002","session_id":"00000000-0000-4000-8000-000000000001","task_id":"task_fixture","description":"fixture task","usage":{"total_tokens":10,"tool_uses":1,"duration_ms":5},"last_tool_name":"Read"}`,
		},
		{
			name:  "background_tasks_changed",
			want:  SDKBackgroundTasksChangedMessage{},
			field: "tasks.0.task_type",
			value: "local",
			line:  `{"type":"system","subtype":"background_tasks_changed","uuid":"00000000-0000-4000-8000-000000000002","session_id":"00000000-0000-4000-8000-000000000001","tasks":[{"task_id":"task_fixture","task_type":"local","description":"fixture task"}]}`,
		},
		{
			name:  "thinking_tokens",
			want:  SDKThinkingTokensMessage{},
			field: "estimated_tokens_delta",
			value: "7",
			line:  `{"type":"system","subtype":"thinking_tokens","uuid":"00000000-0000-4000-8000-000000000002","session_id":"00000000-0000-4000-8000-000000000001","estimated_tokens":42,"estimated_tokens_delta":7}`,
		},
		{
			name:  "session_state_changed",
			want:  SDKSessionStateChangedMessage{},
			field: "state",
			value: "idle",
			line:  `{"type":"system","subtype":"session_state_changed","uuid":"00000000-0000-4000-8000-000000000002","session_id":"00000000-0000-4000-8000-000000000001","state":"idle"}`,
		},
		{
			name:  "worker_shutting_down",
			want:  SDKWorkerShuttingDownMessage{},
			field: "reason",
			value: "fixture reason",
			line:  `{"type":"system","subtype":"worker_shutting_down","uuid":"00000000-0000-4000-8000-000000000002","session_id":"00000000-0000-4000-8000-000000000001","reason":"fixture reason"}`,
		},
		{
			name:  "commands_changed",
			want:  SDKCommandsChangedMessage{},
			field: "commands.0.argumentHint",
			value: "<file>",
			line:  `{"type":"system","subtype":"commands_changed","uuid":"00000000-0000-4000-8000-000000000002","session_id":"00000000-0000-4000-8000-000000000001","commands":[{"name":"fixture","description":"fixture command","argumentHint":"<file>","aliases":["fx"]}]}`,
		},
		{
			name:  "notification",
			want:  SDKNotificationMessage{},
			field: "timeout_ms",
			value: "1000",
			line:  `{"type":"system","subtype":"notification","uuid":"00000000-0000-4000-8000-000000000002","session_id":"00000000-0000-4000-8000-000000000001","key":"fixture-key","text":"fixture text","priority":"low","timeout_ms":1000}`,
		},
		{
			name:  "files_persisted",
			want:  SDKFilesPersistedEvent{},
			field: "files.0.file_id",
			value: "file_fixture",
			line:  `{"type":"system","subtype":"files_persisted","uuid":"00000000-0000-4000-8000-000000000002","session_id":"00000000-0000-4000-8000-000000000001","files":[{"filename":"fixture.txt","file_id":"file_fixture"}],"failed":[],"processed_at":"2026-01-01T00:00:00Z"}`,
		},
		{
			name:  "memory_recall",
			want:  SDKMemoryRecallMessage{},
			field: "memories.0.scope",
			value: "project",
			line:  `{"type":"system","subtype":"memory_recall","uuid":"00000000-0000-4000-8000-000000000002","session_id":"00000000-0000-4000-8000-000000000001","mode":"select","memories":[{"path":"/home/user/notes.md","scope":"project"}]}`,
		},
		{
			name:  "elicitation_complete",
			want:  SDKElicitationCompleteMessage{},
			field: "elicitation_id",
			value: "elicit_fixture",
			line:  `{"type":"system","subtype":"elicitation_complete","uuid":"00000000-0000-4000-8000-000000000002","session_id":"00000000-0000-4000-8000-000000000001","mcp_server_name":"fixture","elicitation_id":"elicit_fixture"}`,
		},
		{
			name:  "permission_denied",
			want:  SDKPermissionDeniedMessage{},
			field: "decision_reason_type",
			value: "rule",
			line:  `{"type":"system","subtype":"permission_denied","uuid":"00000000-0000-4000-8000-000000000002","session_id":"00000000-0000-4000-8000-000000000001","tool_name":"Bash","tool_use_id":"toolu_fixture","agent_id":null,"decision_reason_type":"rule","message":"fixture message"}`,
		},
		{
			name:  "mirror_error",
			want:  SDKMirrorErrorMessage{},
			field: "key.projectKey",
			value: "fixture-project",
			line:  `{"type":"system","subtype":"mirror_error","uuid":"00000000-0000-4000-8000-000000000002","session_id":"00000000-0000-4000-8000-000000000001","error":"fixture error","key":{"projectKey":"fixture-project","sessionId":"00000000-0000-4000-8000-000000000001"}}`,
		},
		{
			name:  "informational",
			want:  SDKInformationalMessage{},
			field: "level",
			value: "info",
			line:  `{"type":"system","subtype":"informational","uuid":"00000000-0000-4000-8000-000000000002","session_id":"00000000-0000-4000-8000-000000000001","content":"fixture banner","level":"info","tool_use_id":null}`,
		},
		{
			name:  "user",
			want:  SDKUserMessage{},
			field: "session_id",
			value: "00000000-0000-4000-8000-000000000001",
			line:  `{"type":"user","message":{"role":"user","content":"fixture prompt"},"parent_tool_use_id":null,"uuid":"00000000-0000-4000-8000-000000000002","session_id":"00000000-0000-4000-8000-000000000001","priority":"next"}`,
		},
		{
			name:  "user_replay",
			want:  SDKUserMessageReplay{},
			field: "session_id",
			value: "00000000-0000-4000-8000-000000000001",
			line:  `{"type":"user","isReplay":true,"message":{"role":"user","content":"fixture prompt"},"parent_tool_use_id":null,"uuid":"00000000-0000-4000-8000-000000000002","session_id":"00000000-0000-4000-8000-000000000001","file_attachments":[]}`,
		},
		{
			name:  "success",
			want:  SDKResultSuccess{},
			field: "duration_api_ms",
			value: "8",
			line:  `{"type":"result","subtype":"success","uuid":"00000000-0000-4000-8000-000000000002","session_id":"00000000-0000-4000-8000-000000000001","duration_ms":10,"duration_api_ms":8,"is_error":false,"num_turns":1,"stop_reason":"end_turn","total_cost_usd":0.01,"usage":{},"modelUsage":{},"permission_denials":[],"result":"fixture result"}`,
		},
		{
			name:  "error_during_execution",
			want:  SDKResultError{},
			field: "errors.0",
			value: "fixture error",
			line:  `{"type":"result","subtype":"error_during_execution","uuid":"00000000-0000-4000-8000-000000000002","session_id":"00000000-0000-4000-8000-000000000001","duration_ms":10,"duration_api_ms":8,"is_error":true,"num_turns":1,"stop_reason":null,"total_cost_usd":0.01,"usage":{},"modelUsage":{},"permission_denials":[],"errors":["fixture error"]}`,
		},
		{
			name:  "error_max_turns",
			want:  SDKResultError{},
			field: "errors.0",
			value: "fixture error",
			line:  `{"type":"result","subtype":"error_max_turns","uuid":"00000000-0000-4000-8000-000000000002","session_id":"00000000-0000-4000-8000-000000000001","duration_ms":10,"duration_api_ms":8,"is_error":true,"num_turns":1,"stop_reason":null,"total_cost_usd":0.01,"usage":{},"modelUsage":{},"permission_denials":[],"errors":["fixture error"]}`,
		},
		{
			name:  "error_max_budget_usd",
			want:  SDKResultError{},
			field: "errors.0",
			value: "fixture error",
			line:  `{"type":"result","subtype":"error_max_budget_usd","uuid":"00000000-0000-4000-8000-000000000002","session_id":"00000000-0000-4000-8000-000000000001","duration_ms":10,"duration_api_ms":8,"is_error":true,"num_turns":1,"stop_reason":null,"total_cost_usd":0.01,"usage":{},"modelUsage":{},"permission_denials":[],"errors":["fixture error"]}`,
		},
		{
			name:  "error_max_structured_output_retries",
			want:  SDKResultError{},
			field: "errors.0",
			value: "fixture error",
			line:  `{"type":"result","subtype":"error_max_structured_output_retries","uuid":"00000000-0000-4000-8000-000000000002","session_id":"00000000-0000-4000-8000-000000000001","duration_ms":10,"duration_api_ms":8,"is_error":true,"num_turns":1,"stop_reason":null,"total_cost_usd":0.01,"usage":{},"modelUsage":{},"permission_denials":[],"errors":["fixture error"]}`,
		},
	}
}

// fieldAt follows a dotted path of wire keys through structs, pointers, and slice indexes. It
// reports false when a key is unknown, an index is out of range, or a pointer on the path is nil.
func fieldAt(value reflect.Value, path string) (reflect.Value, bool) {
	for _, key := range strings.Split(path, ".") {
		value = indirect(value)
		switch value.Kind() {
		case reflect.Struct:
			next, ok := structField(value, key)
			if !ok {
				return reflect.Value{}, false
			}
			value = next
		case reflect.Slice:
			index, err := strconv.Atoi(key)
			if err != nil || index < 0 || index >= value.Len() {
				return reflect.Value{}, false
			}
			value = value.Index(index)
		default:
			return reflect.Value{}, false
		}
	}
	value = indirect(value)
	return value, value.IsValid()
}

// indirect dereferences pointers and interfaces. A nil pointer yields the invalid zero Value.
func indirect(value reflect.Value) reflect.Value {
	for value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface {
		if value.IsNil() {
			return reflect.Value{}
		}
		value = value.Elem()
	}
	return value
}

// structField finds the field whose wire key is key, searching embedded structs as the decoder does.
func structField(value reflect.Value, key string) (reflect.Value, bool) {
	valueType := value.Type()
	for index := range value.NumField() {
		field := valueType.Field(index)
		if field.Anonymous && field.Type.Kind() == reflect.Struct {
			if found, ok := structField(value.Field(index), key); ok {
				return found, true
			}
			continue
		}
		if wireKey(field) == key {
			return value.Field(index), true
		}
	}
	return reflect.Value{}, false
}

func wireKey(field reflect.StructField) string {
	name := field.Tag.Get("wire")
	if name == "" {
		name, _, _ = strings.Cut(field.Tag.Get("json"), ",")
	}
	return name
}

func TestDecodesEveryDeclaredVariant(t *testing.T) {
	for _, fixture := range variantFixtures() {
		t.Run(fixture.name, func(t *testing.T) {
			var message SDKMessage
			if err := json.Unmarshal([]byte(fixture.line), &message); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			variant, err := message.Variant()
			if err != nil {
				t.Fatalf("Variant: %v", err)
			}
			if got, want := reflect.TypeOf(variant), reflect.TypeOf(fixture.want); got != want {
				t.Fatalf("Variant type = %v, want %v", got, want)
			}
			field, ok := fieldAt(reflect.ValueOf(variant), fixture.field)
			if !ok {
				t.Fatalf("field %q is missing or nil", fixture.field)
			}
			if got := fmt.Sprint(field.Interface()); got != fixture.value {
				t.Errorf("%s = %q, want %q", fixture.field, got, fixture.value)
			}
			checkEnvelope(t, fixture.line, variant)
		})
	}
}

// checkEnvelope fails the test unless every envelope key the line carries decodes into the variant.
func checkEnvelope(t *testing.T, line string, variant any) {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(line), &object); err != nil {
		t.Fatalf("decode fixture line: %v", err)
	}
	for _, key := range []string{"type", "subtype", "uuid", "session_id"} {
		raw, present := object[key]
		if !present {
			continue
		}
		var want string
		if err := json.Unmarshal(raw, &want); err != nil {
			t.Fatalf("decode fixture %s: %v", key, err)
		}
		field, ok := fieldAt(reflect.ValueOf(variant), key)
		if !ok {
			t.Errorf("envelope key %q is missing or nil", key)
			continue
		}
		if got := fmt.Sprint(field.Interface()); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestEveryDecoderKeyHasFixture(t *testing.T) {
	named := make(map[string]bool, len(variantFixtures()))
	for _, fixture := range variantFixtures() {
		named[fixture.name] = true
	}
	for key := range directVariantDecoders() {
		if !named[key] {
			t.Errorf("direct decoder key %q has no fixture line", key)
		}
	}
	for key := range systemVariantDecoders() {
		if !named[key] {
			t.Errorf("system decoder key %q has no fixture line", key)
		}
	}
}

// TestEveryMarkedVariantHasFixture fails when a type carrying the variant marker in variants.go has
// no fixture whose want is that type. UnknownMessage is exempt because its own test covers it.
func TestEveryMarkedVariantHasFixture(t *testing.T) {
	marked, err := markedVariantTypes("variants.go")
	if err != nil {
		t.Fatalf("read variant markers: %v", err)
	}
	if len(marked) == 0 {
		t.Fatal("no variant markers found in variants.go")
	}
	fixtured := make(map[string]bool, len(variantFixtures()))
	for _, fixture := range variantFixtures() {
		fixtured[reflect.TypeOf(fixture.want).Name()] = true
	}
	for _, name := range marked {
		if name == "UnknownMessage" || fixtured[name] {
			continue
		}
		t.Errorf("marked variant %s has no fixture", name)
	}
}

// markedVariantTypes returns the receiver type of every isSDKMessageVariant method in the file.
func markedVariantTypes(path string) ([]string, error) {
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv == nil || fn.Name.Name != "isSDKMessageVariant" || len(fn.Recv.List) != 1 {
			continue
		}
		if receiver, ok := fn.Recv.List[0].Type.(*ast.Ident); ok {
			names = append(names, receiver.Name)
		}
	}
	return names, nil
}

func TestUnknownDiscriminatorsKeepTheLine(t *testing.T) {
	cases := []struct {
		name string
		line string
	}{
		{
			name: "type",
			line: `{"type":"fixture_future_type","uuid":"00000000-0000-4000-8000-000000000002","session_id":"00000000-0000-4000-8000-000000000001","payload":{"count":1}}`,
		},
		{
			name: "system subtype",
			line: `{"type":"system","subtype":"fixture_future_subtype","uuid":"00000000-0000-4000-8000-000000000002","session_id":"00000000-0000-4000-8000-000000000001"}`,
		},
		{
			name: "result subtype",
			line: `{"type":"result","subtype":"fixture_future_subtype","uuid":"00000000-0000-4000-8000-000000000002","session_id":"00000000-0000-4000-8000-000000000001"}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var message SDKMessage
			if err := json.Unmarshal([]byte(tc.line), &message); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			variant, err := message.Variant()
			if err != nil {
				t.Fatalf("Variant: %v", err)
			}
			unknown, ok := variant.(UnknownMessage)
			if !ok {
				t.Fatalf("Variant = %T, want UnknownMessage", variant)
			}
			if !bytes.Equal(unknown.Raw, []byte(tc.line)) {
				t.Fatalf("Raw = %s, want %s", unknown.Raw, tc.line)
			}
			unknown.Raw[0] = ' '
			if !bytes.Equal(message.BorrowedJSON(), []byte(tc.line)) {
				t.Errorf("UnknownMessage.Raw aliases the stored line")
			}
		})
	}
}

func TestMalformedLinesReturnErrors(t *testing.T) {
	cases := []struct {
		name string
		line string
	}{
		{name: "missing type", line: `{"uuid":"00000000-0000-4000-8000-000000000002"}`},
		{name: "missing system subtype", line: `{"type":"system","uuid":"00000000-0000-4000-8000-000000000002"}`},
		{name: "missing result subtype", line: `{"type":"result","uuid":"00000000-0000-4000-8000-000000000002"}`},
		{name: "not an object", line: `[1]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var message SDKMessage
			if err := json.Unmarshal([]byte(tc.line), &message); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			if _, err := message.Variant(); err == nil {
				t.Errorf("Variant error = nil, want error")
			}
		})
	}
}

func TestInvalidJSONAndUnsetMessageReturnErrors(t *testing.T) {
	var message SDKMessage
	if err := message.UnmarshalJSON([]byte(`{"type":`)); err == nil {
		t.Errorf("UnmarshalJSON error = nil for invalid JSON, want error")
	}
	if _, err := message.Variant(); err == nil {
		t.Errorf("Variant error = nil for unset message, want error")
	}
	if _, err := message.MarshalJSON(); err == nil {
		t.Errorf("MarshalJSON error = nil for unset message, want error")
	}
}
