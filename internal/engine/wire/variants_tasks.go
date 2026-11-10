package wire

// SDKPluginInstallMessage reports headless plugin installation progress.
type SDKPluginInstallMessage struct {
	sdkSystemEnvelope
	Status string  `json:"status"`
	Name   *string `json:"name,omitempty"`
	Error  *string `json:"error,omitempty"`
}

// SDKTaskNotificationMessage reports a task's terminal edge.
type SDKTaskNotificationMessage struct {
	sdkSystemEnvelope
	TaskID         string        `json:"taskId"                   wire:"task_id"`
	ToolUseID      *string       `json:"toolUseId,omitempty"      wire:"tool_use_id"`
	Status         string        `json:"status"`
	OutputFile     string        `json:"outputFile"               wire:"output_file"`
	Summary        string        `json:"summary"`
	Usage          *SDKTaskUsage `json:"usage,omitempty"`
	SkipTranscript *bool         `json:"skipTranscript,omitempty" wire:"skip_transcript"`
}

// SDKTaskStartedMessage announces a new task.
type SDKTaskStartedMessage struct {
	sdkSystemEnvelope
	TaskID         string  `json:"taskId"                   wire:"task_id"`
	ToolUseID      *string `json:"toolUseId,omitempty"      wire:"tool_use_id"`
	Description    string  `json:"description"`
	SubagentType   *string `json:"subagentType,omitempty"   wire:"subagent_type"`
	TaskType       *string `json:"taskType,omitempty"       wire:"task_type"`
	WorkflowName   *string `json:"workflowName,omitempty"   wire:"workflow_name"`
	Prompt         *string `json:"prompt,omitempty"`
	SkipTranscript *bool   `json:"skipTranscript,omitempty" wire:"skip_transcript"`
}

// SDKTaskUpdatedMessage carries a merge patch for task state.
type SDKTaskUpdatedMessage struct {
	sdkSystemEnvelope
	TaskID string       `json:"taskId" wire:"task_id"`
	Patch  SDKTaskPatch `json:"patch"`
}

// SDKTaskProgressMessage reports current task work and usage.
type SDKTaskProgressMessage struct {
	sdkSystemEnvelope
	TaskID       string       `json:"taskId"                 wire:"task_id"`
	ToolUseID    *string      `json:"toolUseId,omitempty"    wire:"tool_use_id"`
	Description  string       `json:"description"`
	SubagentType *string      `json:"subagentType,omitempty" wire:"subagent_type"`
	Usage        SDKTaskUsage `json:"usage"`
	LastToolName *string      `json:"lastToolName,omitempty" wire:"last_tool_name"`
	Summary      *string      `json:"summary,omitempty"`
}

// SDKBackgroundTasksChangedMessage replaces the set of live background tasks.
type SDKBackgroundTasksChangedMessage struct {
	sdkSystemEnvelope
	Tasks []SDKBackgroundTask `json:"tasks"`
}

// SDKThinkingTokensMessage reports a lossy live thinking-token estimate.
type SDKThinkingTokensMessage struct {
	sdkSystemEnvelope
	EstimatedTokens      int64 `json:"estimatedTokens"      wire:"estimated_tokens"`
	EstimatedTokensDelta int64 `json:"estimatedTokensDelta" wire:"estimated_tokens_delta"`
}

// SDKSessionStateChangedMessage reports the authoritative native session state.
type SDKSessionStateChangedMessage struct {
	sdkSystemEnvelope
	State string `json:"state"`
}

// SDKWorkerShuttingDownMessage reports a graceful worker teardown reason.
type SDKWorkerShuttingDownMessage struct {
	sdkSystemEnvelope
	Reason string `json:"reason"`
}

// SDKCommandsChangedMessage replaces the advertised slash-command set.
type SDKCommandsChangedMessage struct {
	sdkSystemEnvelope
	Commands []SlashCommand `json:"commands"`
}
