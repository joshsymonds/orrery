package wire

// SDKNotificationMessage carries one loop-side text notification.
type SDKNotificationMessage struct {
	sdkSystemEnvelope
	Key       string  `json:"key"`
	Text      string  `json:"text"`
	Priority  string  `json:"priority"`
	Color     *string `json:"color,omitempty"`
	TimeoutMS *int64  `json:"timeoutMs,omitempty" wire:"timeout_ms"`
}

// SDKFilesPersistedEvent reports successful and failed file persistence.
type SDKFilesPersistedEvent struct {
	sdkSystemEnvelope
	Files       []SDKPersistedFile        `json:"files"`
	Failed      []SDKPersistedFileFailure `json:"failed"`
	ProcessedAt string                    `json:"processedAt" wire:"processed_at"`
}

// SDKMemoryRecallMessage reports memories surfaced into the turn.
type SDKMemoryRecallMessage struct {
	sdkSystemEnvelope
	Mode     string      `json:"mode"`
	Memories []SDKMemory `json:"memories"`
}

// SDKElicitationCompleteMessage reports completion of a URL-mode MCP elicitation.
type SDKElicitationCompleteMessage struct {
	sdkSystemEnvelope
	MCPServerName string `json:"mcpServerName" wire:"mcp_server_name"`
	ElicitationID string `json:"elicitationId" wire:"elicitation_id"`
}

// SDKPermissionDeniedMessage reports an automatic tool permission denial.
type SDKPermissionDeniedMessage struct {
	sdkSystemEnvelope
	ToolName           string  `json:"toolName"                     wire:"tool_name"`
	ToolUseID          string  `json:"toolUseId"                    wire:"tool_use_id"`
	AgentID            *string `json:"agentId,omitempty"            wire:"agent_id"`
	DecisionReasonType *string `json:"decisionReasonType,omitempty" wire:"decision_reason_type"`
	DecisionReason     *string `json:"decisionReason,omitempty"     wire:"decision_reason"`
	Message            string  `json:"message"`
}

// SDKMirrorErrorMessage reports data loss after a transcript mirror write failure.
type SDKMirrorErrorMessage struct {
	sdkSystemEnvelope
	Error string       `json:"error"`
	Key   SDKMirrorKey `json:"key"`
}

// SDKInformationalMessage carries a generic printable loop banner.
type SDKInformationalMessage struct {
	sdkSystemEnvelope
	Content             string  `json:"content"`
	Level               string  `json:"level"`
	ToolUseID           *string `json:"toolUseId,omitempty"           wire:"tool_use_id"`
	PreventContinuation *bool   `json:"preventContinuation,omitempty" wire:"prevent_continuation"`
}
