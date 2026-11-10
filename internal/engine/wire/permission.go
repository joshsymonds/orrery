package wire

import "encoding/json"

// SDKControlPermissionRequest asks the client whether a tool may run. Input and
// permission suggestions are kept as raw JSON for the client to render.
type SDKControlPermissionRequest struct {
	Subtype                 string          `json:"subtype"`
	ToolName                string          `json:"toolName"                          wire:"tool_name"`
	DisplayName             string          `json:"displayName,omitempty"             wire:"display_name"`
	Input                   json.RawMessage `json:"input"`
	ToolUseID               string          `json:"toolUseId"                         wire:"tool_use_id"`
	Title                   string          `json:"title,omitempty"`
	Description             string          `json:"description,omitempty"`
	PermissionSuggestions   json.RawMessage `json:"permissionSuggestions,omitempty"   wire:"permission_suggestions"`
	BlockedPath             string          `json:"blockedPath,omitempty"             wire:"blocked_path"`
	DecisionReason          string          `json:"decisionReason,omitempty"          wire:"decision_reason"`
	RequiresUserInteraction *bool           `json:"requiresUserInteraction,omitempty" wire:"requires_user_interaction"`
}

// SDKControlSetPermissionModeRequest asks the client to switch the permission mode.
type SDKControlSetPermissionModeRequest struct {
	Subtype string `json:"subtype"`
	Mode    string `json:"mode"`
}

// SDKPermissionAllow is the body that approves a can_use_tool request. Behavior
// must be "allow".
type SDKPermissionAllow struct {
	Behavior     string          `json:"behavior"`
	UpdatedInput json.RawMessage `json:"updatedInput"`
}

// SDKPermissionDeny is the body that rejects a can_use_tool request. Behavior
// must be "deny".
type SDKPermissionDeny struct {
	Behavior string `json:"behavior"`
	Message  string `json:"message"`
}
