package wire

import "encoding/json"

// SDKControlRequest preserves the request body of one control_request envelope.
type SDKControlRequest struct {
	Type      string          `json:"type"`
	RequestID string          `json:"requestId" wire:"request_id"`
	Request   json.RawMessage `json:"request"`
}

// SDKControlInitializeRequest advertises the interactive dialogs rendered by the client.
type SDKControlInitializeRequest struct {
	Subtype              string         `json:"subtype"`
	SupportedDialogKinds []string       `json:"supportedDialogKinds"`
	Hooks                map[string]any `json:"hooks"`
	SystemPrompt         []string       `json:"systemPrompt"`
}

// SDKControlElicitationRequest asks the client to collect MCP-requested user input.
type SDKControlElicitationRequest struct {
	Subtype         string          `json:"subtype"`
	MCPServerName   string          `json:"mcpServerName"             wire:"mcp_server_name"`
	Message         string          `json:"message"`
	Mode            string          `json:"mode,omitempty"`
	URL             string          `json:"url,omitempty"`
	ElicitationID   string          `json:"elicitationId,omitempty"   wire:"elicitation_id"`
	RequestedSchema json.RawMessage `json:"requestedSchema,omitempty" wire:"requested_schema"`
	Title           string          `json:"title,omitempty"`
	DisplayName     string          `json:"displayName,omitempty"     wire:"display_name"`
	Description     string          `json:"description,omitempty"`
}

// SDKControlRequestUserDialogRequest asks the client to render a tool-driven dialog.
type SDKControlRequestUserDialogRequest struct {
	Subtype    string          `json:"subtype"`
	DialogKind string          `json:"dialogKind"          wire:"dialog_kind"`
	Payload    json.RawMessage `json:"payload"`
	ToolUseID  string          `json:"toolUseId,omitempty" wire:"tool_use_id"`
}

// SDKControlResponse is one successful, request-correlated control response envelope.
type SDKControlResponse struct {
	Type     string                    `json:"type"`
	Response SDKControlSuccessResponse `json:"response"`
}

// SDKControlSuccessResponse carries a successful response to a control request.
type SDKControlSuccessResponse struct {
	Subtype                   string              `json:"subtype"`
	RequestID                 string              `json:"requestId"                           wire:"request_id"`
	Response                  json.RawMessage     `json:"response,omitempty"`
	PendingUserDialogRequests []SDKControlRequest `json:"pendingUserDialogRequests,omitempty" wire:"pending_user_dialog_requests"`
}

// SDKControlCancelRequest cancels a currently open control request.
type SDKControlCancelRequest struct {
	Type      string `json:"type"`
	RequestID string `json:"requestId" wire:"request_id"`
}

// UnmarshalControlJSON decodes a control-channel message into target, using the
// snake_case wire spellings of its fields. target must be a non-nil pointer.
func UnmarshalControlJSON(data []byte, target any) error {
	return unmarshalWire(data, target)
}
