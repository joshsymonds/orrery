package wire

import "encoding/json"

type sdkMessageEnvelope struct {
	Type      string `json:"type"`
	UUID      string `json:"uuid"`
	SessionID string `json:"sessionId" wire:"session_id"`
}

type sdkSystemEnvelope struct {
	sdkMessageEnvelope
	Subtype string `json:"subtype"`
}

// SDKAssistantMessage carries one authoritative assistant message.
type SDKAssistantMessage struct {
	sdkMessageEnvelope
	Message                       BetaMessage `json:"message"`
	ParentToolUseID               *string     `json:"parentToolUseId"                         wire:"parent_tool_use_id"`
	Error                         *string     `json:"error,omitempty"`
	RequestID                     *string     `json:"requestId,omitempty"                     wire:"request_id"`
	ResumedFromIncompleteThinking *bool       `json:"resumedFromIncompleteThinking,omitempty" wire:"resumed_from_incomplete_thinking"`
	Supersedes                    []string    `json:"supersedes,omitempty"`
	SubagentType                  *string     `json:"subagentType,omitempty"                  wire:"subagent_type"`
	TaskDescription               *string     `json:"taskDescription,omitempty"               wire:"task_description"`
	Timestamp                     *string     `json:"timestamp,omitempty"`
}

// SDKUserMessage carries a user-role message that may begin a query.
type SDKUserMessage struct {
	Type            string            `json:"type"`
	Message         MessageParam      `json:"message"`
	ParentToolUseID *string           `json:"parentToolUseId"           wire:"parent_tool_use_id"`
	IsSynthetic     *bool             `json:"isSynthetic,omitempty"`
	ToolUseResult   json.RawMessage   `json:"toolUseResult,omitempty"   wire:"tool_use_result"`
	Priority        *string           `json:"priority,omitempty"`
	Origin          *SDKMessageOrigin `json:"origin,omitempty"`
	ShouldQuery     *bool             `json:"shouldQuery,omitempty"`
	Timestamp       *string           `json:"timestamp,omitempty"`
	UUID            *string           `json:"uuid,omitempty"`
	SessionID       *string           `json:"sessionId,omitempty"       wire:"session_id"`
	SubagentType    *string           `json:"subagentType,omitempty"    wire:"subagent_type"`
	TaskDescription *string           `json:"taskDescription,omitempty" wire:"task_description"`
}

// SDKUserMessageReplay acknowledges a replayed user-role message.
type SDKUserMessageReplay struct {
	Type            string            `json:"type"`
	Message         MessageParam      `json:"message"`
	ParentToolUseID *string           `json:"parentToolUseId"           wire:"parent_tool_use_id"`
	IsSynthetic     *bool             `json:"isSynthetic,omitempty"`
	ToolUseResult   json.RawMessage   `json:"toolUseResult,omitempty"   wire:"tool_use_result"`
	Priority        *string           `json:"priority,omitempty"`
	Origin          *SDKMessageOrigin `json:"origin,omitempty"`
	ShouldQuery     *bool             `json:"shouldQuery,omitempty"`
	Timestamp       *string           `json:"timestamp,omitempty"`
	UUID            string            `json:"uuid"`
	SessionID       string            `json:"sessionId"                 wire:"session_id"`
	IsReplay        bool              `json:"isReplay"`
	FileAttachments []json.RawMessage `json:"fileAttachments,omitempty" wire:"file_attachments"`
}

// SDKPartialAssistantMessage carries one streamed model event.
type SDKPartialAssistantMessage struct {
	sdkMessageEnvelope
	Event           BetaRawMessageStreamEvent `json:"event"`
	ParentToolUseID *string                   `json:"parentToolUseId"  wire:"parent_tool_use_id"`
	TTFTMS          *int64                    `json:"ttftMs,omitempty" wire:"ttft_ms"`
}

// SDKToolProgressMessage reports elapsed time for a running tool use.
type SDKToolProgressMessage struct {
	sdkMessageEnvelope
	ToolUseID          string  `json:"toolUseId"          wire:"tool_use_id"`
	ToolName           string  `json:"toolName"           wire:"tool_name"`
	ParentToolUseID    *string `json:"parentToolUseId"    wire:"parent_tool_use_id"`
	ElapsedTimeSeconds float64 `json:"elapsedTimeSeconds" wire:"elapsed_time_seconds"`
	TaskID             *string `json:"taskId,omitempty"   wire:"task_id"`
}

// SDKAuthStatusMessage reports an authentication attempt's current output.
type SDKAuthStatusMessage struct {
	sdkMessageEnvelope
	IsAuthenticating bool     `json:"isAuthenticating"`
	Output           []string `json:"output"`
	Error            *string  `json:"error,omitempty"`
}

// SDKToolUseSummaryMessage summarizes preceding tool uses.
type SDKToolUseSummaryMessage struct {
	sdkMessageEnvelope
	Summary             string   `json:"summary"`
	PrecedingToolUseIDs []string `json:"precedingToolUseIds" wire:"preceding_tool_use_ids"`
}

// SDKRateLimitEvent carries the current subscription rate-limit state.
type SDKRateLimitEvent struct {
	sdkMessageEnvelope
	RateLimitInfo SDKRateLimitInfo `json:"rateLimitInfo" wire:"rate_limit_info"`
}

// SDKPromptSuggestionMessage proposes a next user prompt.
type SDKPromptSuggestionMessage struct {
	sdkMessageEnvelope
	Suggestion string `json:"suggestion"`
}

// SDKConversationResetMessage starts a fresh transcript conversation.
type SDKConversationResetMessage struct {
	sdkMessageEnvelope
	NewConversationID string `json:"newConversationId" wire:"new_conversation_id"`
}

// SDKCommandLifecycleMessage reports the fate of a queued command (slash
// command or queued user prompt). It is emitted only for inbound messages
// that carried a client-supplied uuid.
type SDKCommandLifecycleMessage struct {
	sdkMessageEnvelope
	CommandUUID string `json:"commandUuid" wire:"command_uuid"`
	State       string `json:"state"`
}

// SDKResultMessage contains fields common to every terminal result subtype.
type SDKResultMessage struct {
	sdkMessageEnvelope
	Subtype           string                `json:"subtype"`
	DurationMS        int64                 `json:"durationMs"               wire:"duration_ms"`
	DurationAPIMS     int64                 `json:"durationApiMs"            wire:"duration_api_ms"`
	IsError           bool                  `json:"isError"                  wire:"is_error"`
	NumTurns          int64                 `json:"numTurns"                 wire:"num_turns"`
	StopReason        *string               `json:"stopReason"               wire:"stop_reason"`
	TotalCostUSD      float64               `json:"totalCostUsd"             wire:"total_cost_usd"`
	Usage             BetaUsage             `json:"usage"`
	ModelUsage        map[string]ModelUsage `json:"modelUsage"`
	PermissionDenials []SDKPermissionDenial `json:"permissionDenials"        wire:"permission_denials"`
	TerminalReason    *string               `json:"terminalReason,omitempty" wire:"terminal_reason"`
	FastModeState     *string               `json:"fastModeState,omitempty"  wire:"fast_mode_state"`
	Origin            *SDKMessageOrigin     `json:"origin,omitempty"`
}

// SDKResultSuccess is a successful terminal result.
type SDKResultSuccess struct {
	SDKResultMessage
	TTFTMS                   *int64              `json:"ttftMs,omitempty"                   wire:"ttft_ms"`
	TTFTStreamMS             *int64              `json:"ttftStreamMs,omitempty"             wire:"ttft_stream_ms"`
	TimeToRequestMS          *int64              `json:"timeToRequestMs,omitempty"          wire:"time_to_request_ms"`
	TimeToRequestFromSpawnMS *int64              `json:"timeToRequestFromSpawnMs,omitempty" wire:"time_to_request_from_spawn_ms"`
	WarmSpareClaimed         *bool               `json:"warmSpareClaimed,omitempty"         wire:"warm_spare_claimed"`
	TimeOriginMS             *int64              `json:"timeOriginMs,omitempty"             wire:"time_origin_ms"`
	APIErrorStatus           *int                `json:"apiErrorStatus,omitempty"           wire:"api_error_status"`
	Result                   string              `json:"result"`
	StructuredOutput         json.RawMessage     `json:"structuredOutput,omitempty"         wire:"structured_output"`
	DeferredToolUse          *SDKDeferredToolUse `json:"deferredToolUse,omitempty"          wire:"deferred_tool_use"`
}

// SDKResultError is an unsuccessful terminal result.
type SDKResultError struct {
	SDKResultMessage
	Errors []string `json:"errors"`
}
