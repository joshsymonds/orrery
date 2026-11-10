package wire

import "encoding/json"

// SDKMessageOrigin records provenance attached to user and result messages.
type SDKMessageOrigin struct {
	Kind         string  `json:"kind"`
	Server       *string `json:"server,omitempty"`
	From         *string `json:"from,omitempty"`
	Name         *string `json:"name,omitempty"`
	SenderTaskID *string `json:"senderTaskId,omitempty"`
	Body         *string `json:"body,omitempty"`
}

// SDKPermissionDenial records one tool permission rejected during a result.
type SDKPermissionDenial struct {
	ToolName  string          `json:"toolName"  wire:"tool_name"`
	ToolUseID string          `json:"toolUseId" wire:"tool_use_id"`
	ToolInput json.RawMessage `json:"toolInput" wire:"tool_input"`
}

// SDKDeferredToolUse describes a tool call deferred by a successful result.
type SDKDeferredToolUse struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// ModelUsage is the per-model cost and token breakdown in a terminal result.
type ModelUsage struct {
	InputTokens              int64   `json:"inputTokens"`
	OutputTokens             int64   `json:"outputTokens"`
	CacheReadInputTokens     int64   `json:"cacheReadInputTokens"`
	CacheCreationInputTokens int64   `json:"cacheCreationInputTokens"`
	WebSearchRequests        int64   `json:"webSearchRequests"`
	CostUSD                  float64 `json:"costUsd"                  wire:"costUSD"`
	ContextWindow            int64   `json:"contextWindow"`
	MaxOutputTokens          int64   `json:"maxOutputTokens"`
}

// SDKRateLimitInfo describes Claude subscription rate-limit state.
type SDKRateLimitInfo struct {
	Status                          string   `json:"status"`
	ResetsAt                        *float64 `json:"resetsAt,omitempty"`
	RateLimitType                   *string  `json:"rateLimitType,omitempty"`
	Utilization                     *float64 `json:"utilization,omitempty"`
	OverageStatus                   *string  `json:"overageStatus,omitempty"`
	OverageResetsAt                 *float64 `json:"overageResetsAt,omitempty"`
	OverageDisabledReason           *string  `json:"overageDisabledReason,omitempty"`
	IsUsingOverage                  *bool    `json:"isUsingOverage,omitempty"`
	OverageInUse                    *bool    `json:"overageInUse,omitempty"`
	SurpassedThreshold              *float64 `json:"surpassedThreshold,omitempty"`
	ErrorCode                       *string  `json:"errorCode,omitempty"`
	CanUserPurchaseCredits          *bool    `json:"canUserPurchaseCredits,omitempty"`
	HasChargeableSavedPaymentMethod *bool    `json:"hasChargeableSavedPaymentMethod,omitempty"`
}

// SlashCommand describes one command advertised by a commands-changed message.
type SlashCommand struct {
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	ArgumentHint string   `json:"argumentHint"`
	Aliases      []string `json:"aliases,omitempty"`
}

// SDKMCPServer describes one MCP server in the initialization message.
type SDKMCPServer struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

// SDKPlugin describes one loaded plugin in the initialization message.
type SDKPlugin struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// SDKCompactMetadata describes token counts and transcript relinking at compaction.
type SDKCompactMetadata struct {
	Trigger           string                `json:"trigger"`
	PreTokens         int64                 `json:"preTokens"                   wire:"pre_tokens"`
	PostTokens        *int64                `json:"postTokens,omitempty"        wire:"post_tokens"`
	DurationMS        *int64                `json:"durationMs,omitempty"        wire:"duration_ms"`
	PreservedSegment  *SDKPreservedSegment  `json:"preservedSegment,omitempty"  wire:"preserved_segment"`
	PreservedMessages *SDKPreservedMessages `json:"preservedMessages,omitempty" wire:"preserved_messages"`
}

// SDKPreservedSegment identifies a retained contiguous transcript segment.
type SDKPreservedSegment struct {
	HeadUUID   string `json:"headUuid"   wire:"head_uuid"`
	AnchorUUID string `json:"anchorUuid" wire:"anchor_uuid"`
	TailUUID   string `json:"tailUuid"   wire:"tail_uuid"`
}

// SDKPreservedMessages identifies retained transcript messages in source order.
type SDKPreservedMessages struct {
	AnchorUUID string   `json:"anchorUuid" wire:"anchor_uuid"`
	UUIDs      []string `json:"uuids"`
}

// SDKTaskUsage contains task token, tool, and duration totals.
type SDKTaskUsage struct {
	TotalTokens int64 `json:"totalTokens" wire:"total_tokens"`
	ToolUses    int64 `json:"toolUses"    wire:"tool_uses"`
	DurationMS  int64 `json:"durationMs"  wire:"duration_ms"`
}

// SDKTaskPatch contains the wire-safe fields changed on a task.
type SDKTaskPatch struct {
	Status         *string `json:"status,omitempty"`
	Description    *string `json:"description,omitempty"`
	EndTime        *int64  `json:"endTime,omitempty"        wire:"end_time"`
	TotalPausedMS  *int64  `json:"totalPausedMs,omitempty"  wire:"total_paused_ms"`
	Error          *string `json:"error,omitempty"`
	IsBackgrounded *bool   `json:"isBackgrounded,omitempty" wire:"is_backgrounded"`
}

// SDKBackgroundTask identifies one live background task.
type SDKBackgroundTask struct {
	TaskID      string `json:"taskId"      wire:"task_id"`
	TaskType    string `json:"taskType"    wire:"task_type"`
	Description string `json:"description"`
}

// SDKPersistedFile identifies one successfully persisted file.
type SDKPersistedFile struct {
	Filename string `json:"filename"`
	FileID   string `json:"fileId"   wire:"file_id"`
}

// SDKPersistedFileFailure identifies one file that could not be persisted.
type SDKPersistedFileFailure struct {
	Filename string `json:"filename"`
	Error    string `json:"error"`
}

// SDKMemory is one surfaced memory reference and optional body.
type SDKMemory struct {
	Path    string  `json:"path"`
	Scope   string  `json:"scope"`
	Content *string `json:"content,omitempty"`
}

// SDKMirrorKey identifies the transcript store target for a mirror failure.
type SDKMirrorKey struct {
	ProjectKey string  `json:"projectKey"`
	SessionID  string  `json:"sessionId"`
	Subpath    *string `json:"subpath,omitempty"`
}
