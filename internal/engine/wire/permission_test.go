package wire_test

import (
	"encoding/json"
	"testing"

	"github.com/joshsymonds/orrery/internal/engine/wire"
)

func TestUnmarshalPermissionRequestDecodesSnakeCaseFields(t *testing.T) {
	line := `{"subtype":"can_use_tool",` +
		`"tool_name":"Edit",` +
		`"display_name":"Edit file",` +
		`"input":{"file_path":"/home/user/example.txt","old_string":"a","new_string":"b"},` +
		`"tool_use_id":"toolu_1",` +
		`"title":"Allow edit",` +
		`"description":"Edit example.txt",` +
		`"permission_suggestions":[{"type":"setMode","mode":"acceptEdits"}],` +
		`"blocked_path":"/home/user/example.txt",` +
		`"decision_reason":"no matching rule",` +
		`"requires_user_interaction":true}`

	var got wire.SDKControlPermissionRequest
	if err := wire.UnmarshalControlJSON([]byte(line), &got); err != nil {
		t.Fatalf("UnmarshalControlJSON: %v", err)
	}
	if got.Subtype != "can_use_tool" {
		t.Errorf("Subtype = %q, want %q", got.Subtype, "can_use_tool")
	}
	if got.ToolName != "Edit" {
		t.Errorf("ToolName = %q, want %q", got.ToolName, "Edit")
	}
	if got.DisplayName != "Edit file" {
		t.Errorf("DisplayName = %q, want %q", got.DisplayName, "Edit file")
	}
	wantInput := `{"file_path":"/home/user/example.txt","old_string":"a","new_string":"b"}`
	if string(got.Input) != wantInput {
		t.Errorf("Input = %s, want %s", got.Input, wantInput)
	}
	if got.ToolUseID != "toolu_1" {
		t.Errorf("ToolUseID = %q, want %q", got.ToolUseID, "toolu_1")
	}
	if got.Title != "Allow edit" {
		t.Errorf("Title = %q, want %q", got.Title, "Allow edit")
	}
	if got.Description != "Edit example.txt" {
		t.Errorf("Description = %q, want %q", got.Description, "Edit example.txt")
	}
	if string(got.PermissionSuggestions) != `[{"type":"setMode","mode":"acceptEdits"}]` {
		t.Errorf("PermissionSuggestions = %s, want raw suggestions array", got.PermissionSuggestions)
	}
	if got.BlockedPath != "/home/user/example.txt" {
		t.Errorf("BlockedPath = %q, want %q", got.BlockedPath, "/home/user/example.txt")
	}
	if got.DecisionReason != "no matching rule" {
		t.Errorf("DecisionReason = %q, want %q", got.DecisionReason, "no matching rule")
	}
	if got.RequiresUserInteraction == nil || !*got.RequiresUserInteraction {
		t.Errorf("RequiresUserInteraction = %v, want true", got.RequiresUserInteraction)
	}
}

func TestUnmarshalPermissionRequestLeavesOptionalInteractionNil(t *testing.T) {
	line := `{"subtype":"can_use_tool","tool_name":"Bash","input":{"command":"ls"},"tool_use_id":"toolu_2"}`

	var got wire.SDKControlPermissionRequest
	if err := wire.UnmarshalControlJSON([]byte(line), &got); err != nil {
		t.Fatalf("UnmarshalControlJSON: %v", err)
	}
	if got.RequiresUserInteraction != nil {
		t.Errorf("RequiresUserInteraction = %v, want nil when absent", *got.RequiresUserInteraction)
	}
}

func TestMarshalSetPermissionModeRequestWritesWireBody(t *testing.T) {
	got, err := json.Marshal(wire.SDKControlSetPermissionModeRequest{
		Subtype: "set_permission_mode",
		Mode:    "acceptEdits",
	})
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	want := `{"subtype":"set_permission_mode","mode":"acceptEdits"}`
	if string(got) != want {
		t.Errorf("body = %s, want %s", got, want)
	}
}

func TestMarshalPermissionAllowWritesWireBody(t *testing.T) {
	got, err := json.Marshal(wire.SDKPermissionAllow{
		Behavior:     "allow",
		UpdatedInput: json.RawMessage(`{"file_path":"/home/user/example.txt"}`),
	})
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	want := `{"behavior":"allow","updatedInput":{"file_path":"/home/user/example.txt"}}`
	if string(got) != want {
		t.Errorf("body = %s, want %s", got, want)
	}
}

func TestMarshalPermissionDenyWritesWireBody(t *testing.T) {
	got, err := json.Marshal(wire.SDKPermissionDeny{
		Behavior: "deny",
		Message:  "user declined",
	})
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	want := `{"behavior":"deny","message":"user declined"}`
	if string(got) != want {
		t.Errorf("body = %s, want %s", got, want)
	}
}
