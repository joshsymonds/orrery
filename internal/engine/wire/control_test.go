package wire_test

import (
	"testing"

	"github.com/joshsymonds/orrery/internal/engine/wire"
)

const (
	exampleRequestID   = "00000000-0000-4000-8000-000000000001"
	exampleRequestID2  = "00000000-0000-4000-8000-000000000002"
	exampleElicitation = "00000000-0000-4000-8000-000000000003"
	exampleToolUseID   = "00000000-0000-4000-8000-000000000004"
)

func TestUnmarshalControlRequestDecodesEnvelope(t *testing.T) {
	line := `{"type":"control_request","request_id":"` + exampleRequestID + `","request":{"subtype":"initialize"}}`

	var got wire.SDKControlRequest
	if err := wire.UnmarshalControlJSON([]byte(line), &got); err != nil {
		t.Fatalf("UnmarshalControlJSON: %v", err)
	}
	if got.Type != "control_request" {
		t.Errorf("Type = %q, want %q", got.Type, "control_request")
	}
	if got.RequestID != exampleRequestID {
		t.Errorf("RequestID = %q, want %q", got.RequestID, exampleRequestID)
	}
	if string(got.Request) != `{"subtype":"initialize"}` {
		t.Errorf("Request = %s, want raw subtype object", got.Request)
	}
}

func TestUnmarshalControlInitializeRequestDecodesFields(t *testing.T) {
	line := `{"subtype":"initialize",` +
		`"supportedDialogKinds":["confirm","choice"],` +
		`"hooks":{"PreToolUse":{"enabled":true}},` +
		`"systemPrompt":["first","second"]}`

	var got wire.SDKControlInitializeRequest
	if err := wire.UnmarshalControlJSON([]byte(line), &got); err != nil {
		t.Fatalf("UnmarshalControlJSON: %v", err)
	}
	if got.Subtype != "initialize" {
		t.Errorf("Subtype = %q, want %q", got.Subtype, "initialize")
	}
	if len(got.SupportedDialogKinds) != 2 ||
		got.SupportedDialogKinds[0] != "confirm" ||
		got.SupportedDialogKinds[1] != "choice" {
		t.Errorf("SupportedDialogKinds = %v, want [confirm choice]", got.SupportedDialogKinds)
	}
	if _, ok := got.Hooks["PreToolUse"]; !ok {
		t.Errorf("Hooks = %v, want key PreToolUse", got.Hooks)
	}
	if len(got.SystemPrompt) != 2 || got.SystemPrompt[0] != "first" || got.SystemPrompt[1] != "second" {
		t.Errorf("SystemPrompt = %v, want [first second]", got.SystemPrompt)
	}
}

func TestUnmarshalControlElicitationRequestDecodesSnakeCaseFields(t *testing.T) {
	line := `{"subtype":"elicitation",` +
		`"mcp_server_name":"example-server",` +
		`"message":"Enter a value",` +
		`"mode":"form",` +
		`"url":"https://example.test/form",` +
		`"elicitation_id":"` + exampleElicitation + `",` +
		`"requested_schema":{"type":"object"},` +
		`"title":"Input",` +
		`"display_name":"Example prompt",` +
		`"description":"Provide the requested value"}`

	var got wire.SDKControlElicitationRequest
	if err := wire.UnmarshalControlJSON([]byte(line), &got); err != nil {
		t.Fatalf("UnmarshalControlJSON: %v", err)
	}
	if got.Subtype != "elicitation" {
		t.Errorf("Subtype = %q, want %q", got.Subtype, "elicitation")
	}
	if got.MCPServerName != "example-server" {
		t.Errorf("MCPServerName = %q, want %q", got.MCPServerName, "example-server")
	}
	if got.Message != "Enter a value" {
		t.Errorf("Message = %q, want %q", got.Message, "Enter a value")
	}
	if got.Mode != "form" {
		t.Errorf("Mode = %q, want %q", got.Mode, "form")
	}
	if got.URL != "https://example.test/form" {
		t.Errorf("URL = %q, want %q", got.URL, "https://example.test/form")
	}
	if got.ElicitationID != exampleElicitation {
		t.Errorf("ElicitationID = %q, want %q", got.ElicitationID, exampleElicitation)
	}
	if string(got.RequestedSchema) != `{"type":"object"}` {
		t.Errorf("RequestedSchema = %s, want raw schema object", got.RequestedSchema)
	}
	if got.Title != "Input" {
		t.Errorf("Title = %q, want %q", got.Title, "Input")
	}
	if got.DisplayName != "Example prompt" {
		t.Errorf("DisplayName = %q, want %q", got.DisplayName, "Example prompt")
	}
	if got.Description != "Provide the requested value" {
		t.Errorf("Description = %q, want %q", got.Description, "Provide the requested value")
	}
}

func TestUnmarshalControlUserDialogRequestDecodesSnakeCaseFields(t *testing.T) {
	line := `{"subtype":"request_user_dialog",` +
		`"dialog_kind":"confirm",` +
		`"payload":{"question":"Proceed?"},` +
		`"tool_use_id":"` + exampleToolUseID + `"}`

	var got wire.SDKControlRequestUserDialogRequest
	if err := wire.UnmarshalControlJSON([]byte(line), &got); err != nil {
		t.Fatalf("UnmarshalControlJSON: %v", err)
	}
	if got.Subtype != "request_user_dialog" {
		t.Errorf("Subtype = %q, want %q", got.Subtype, "request_user_dialog")
	}
	if got.DialogKind != "confirm" {
		t.Errorf("DialogKind = %q, want %q", got.DialogKind, "confirm")
	}
	if string(got.Payload) != `{"question":"Proceed?"}` {
		t.Errorf("Payload = %s, want raw payload object", got.Payload)
	}
	if got.ToolUseID != exampleToolUseID {
		t.Errorf("ToolUseID = %q, want %q", got.ToolUseID, exampleToolUseID)
	}
}

func TestUnmarshalControlResponseDecodesNestedPendingRequests(t *testing.T) {
	line := `{"type":"control_response","response":{` +
		`"subtype":"success",` +
		`"request_id":"` + exampleRequestID + `",` +
		`"response":{"ok":true},` +
		`"pending_user_dialog_requests":[` +
		`{"type":"control_request","request_id":"` + exampleRequestID2 + `","request":{"subtype":"elicitation"}}` +
		`]}}`

	var got wire.SDKControlResponse
	if err := wire.UnmarshalControlJSON([]byte(line), &got); err != nil {
		t.Fatalf("UnmarshalControlJSON: %v", err)
	}
	if got.Type != "control_response" {
		t.Errorf("Type = %q, want %q", got.Type, "control_response")
	}
	if got.Response.Subtype != "success" {
		t.Errorf("Response.Subtype = %q, want %q", got.Response.Subtype, "success")
	}
	if got.Response.RequestID != exampleRequestID {
		t.Errorf("Response.RequestID = %q, want %q", got.Response.RequestID, exampleRequestID)
	}
	if string(got.Response.Response) != `{"ok":true}` {
		t.Errorf("Response.Response = %s, want raw ok object", got.Response.Response)
	}
	pending := got.Response.PendingUserDialogRequests
	if len(pending) != 1 {
		t.Fatalf("PendingUserDialogRequests has %d entries, want 1", len(pending))
	}
	if pending[0].Type != "control_request" {
		t.Errorf("pending[0].Type = %q, want %q", pending[0].Type, "control_request")
	}
	if pending[0].RequestID != exampleRequestID2 {
		t.Errorf("pending[0].RequestID = %q, want %q", pending[0].RequestID, exampleRequestID2)
	}
	if string(pending[0].Request) != `{"subtype":"elicitation"}` {
		t.Errorf("pending[0].Request = %s, want raw elicitation object", pending[0].Request)
	}
}

func TestUnmarshalControlCancelRequestDecodesRequestID(t *testing.T) {
	line := `{"type":"control_cancel_request","request_id":"` + exampleRequestID + `"}`

	var got wire.SDKControlCancelRequest
	if err := wire.UnmarshalControlJSON([]byte(line), &got); err != nil {
		t.Fatalf("UnmarshalControlJSON: %v", err)
	}
	if got.Type != "control_cancel_request" {
		t.Errorf("Type = %q, want %q", got.Type, "control_cancel_request")
	}
	if got.RequestID != exampleRequestID {
		t.Errorf("RequestID = %q, want %q", got.RequestID, exampleRequestID)
	}
}

func TestUnmarshalControlJSONRejectsNonPointerTarget(t *testing.T) {
	var got wire.SDKControlCancelRequest
	if err := wire.UnmarshalControlJSON([]byte(`{"request_id":"x"}`), got); err == nil {
		t.Fatal("UnmarshalControlJSON with non-pointer target returned nil error")
	}
}

func TestUnmarshalControlJSONRejectsMalformedLine(t *testing.T) {
	var got wire.SDKControlRequest
	if err := wire.UnmarshalControlJSON([]byte(`{"type":"control_request",`), &got); err == nil {
		t.Fatal("UnmarshalControlJSON with malformed line returned nil error")
	}
}
