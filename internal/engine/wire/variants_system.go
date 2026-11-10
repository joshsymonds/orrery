package wire

// SDKSystemMessage initializes one Claude session.
type SDKSystemMessage struct {
	sdkSystemEnvelope
	Agents            []string       `json:"agents,omitempty"`
	APIKeySource      string         `json:"apiKeySource"`
	Betas             []string       `json:"betas,omitempty"`
	ClaudeCodeVersion string         `json:"claudeCodeVersion"       wire:"claude_code_version"`
	CWD               string         `json:"cwd"`
	Tools             []string       `json:"tools"`
	MCPServers        []SDKMCPServer `json:"mcpServers"              wire:"mcp_servers"`
	Model             string         `json:"model"`
	PermissionMode    string         `json:"permissionMode"`
	SlashCommands     []string       `json:"slashCommands"           wire:"slash_commands"`
	OutputStyle       string         `json:"outputStyle"             wire:"output_style"`
	Skills            []string       `json:"skills"`
	Plugins           []SDKPlugin    `json:"plugins"`
	FastModeState     *string        `json:"fastModeState,omitempty" wire:"fast_mode_state"`
	Capabilities      []string       `json:"capabilities,omitempty"`
}

// SDKCompactBoundaryMessage reports an authoritative compaction boundary.
type SDKCompactBoundaryMessage struct {
	sdkSystemEnvelope
	CompactMetadata SDKCompactMetadata `json:"compactMetadata" wire:"compact_metadata"`
}

// SDKStatusMessage reports transient request or compaction status.
type SDKStatusMessage struct {
	sdkSystemEnvelope
	Status         *string `json:"status"`
	PermissionMode *string `json:"permissionMode,omitempty"`
	CompactResult  *string `json:"compactResult,omitempty"  wire:"compact_result"`
	CompactError   *string `json:"compactError,omitempty"   wire:"compact_error"`
}

// SDKAPIRetryMessage reports a retryable assistant API failure.
type SDKAPIRetryMessage struct {
	sdkSystemEnvelope
	Attempt      int64  `json:"attempt"`
	MaxRetries   int64  `json:"maxRetries"   wire:"max_retries"`
	RetryDelayMS int64  `json:"retryDelayMs" wire:"retry_delay_ms"`
	ErrorStatus  *int   `json:"errorStatus"  wire:"error_status"`
	Error        string `json:"error"`
}

// SDKControlRequestProgressMessage reports progress for an in-flight control request.
type SDKControlRequestProgressMessage struct {
	sdkSystemEnvelope
	RequestID    string `json:"requestId"              wire:"request_id"`
	Status       string `json:"status"`
	Attempt      *int64 `json:"attempt,omitempty"`
	MaxRetries   *int64 `json:"maxRetries,omitempty"   wire:"max_retries"`
	RetryDelayMS *int64 `json:"retryDelayMs,omitempty" wire:"retry_delay_ms"`
	ErrorStatus  *int   `json:"errorStatus,omitempty"  wire:"error_status"`
}

// SDKModelRefusalFallbackMessage reports a refusal retry on a fallback model.
type SDKModelRefusalFallbackMessage struct {
	sdkSystemEnvelope
	Trigger                string   `json:"trigger"`
	Direction              string   `json:"direction"`
	OriginalModel          string   `json:"originalModel"                    wire:"original_model"`
	FallbackModel          string   `json:"fallbackModel"                    wire:"fallback_model"`
	RequestID              *string  `json:"requestId"                        wire:"request_id"`
	APIRefusalCategory     *string  `json:"apiRefusalCategory,omitempty"     wire:"api_refusal_category"`
	APIRefusalExplanation  *string  `json:"apiRefusalExplanation,omitempty"  wire:"api_refusal_explanation"`
	RetractedMessageUUIDs  []string `json:"retractedMessageUuids,omitempty"  wire:"retracted_message_uuids"`
	RefusedUserMessageUUID *string  `json:"refusedUserMessageUuid,omitempty" wire:"refused_user_message_uuid"`
	Content                string   `json:"content"`
}

// SDKModelRefusalNoFallbackMessage reports a refusal for which no retry ran.
type SDKModelRefusalNoFallbackMessage struct {
	sdkSystemEnvelope
	OriginalModel          string  `json:"originalModel"                    wire:"original_model"`
	RequestID              *string `json:"requestId"                        wire:"request_id"`
	APIRefusalCategory     *string `json:"apiRefusalCategory,omitempty"     wire:"api_refusal_category"`
	APIRefusalExplanation  *string `json:"apiRefusalExplanation,omitempty"  wire:"api_refusal_explanation"`
	RefusedUserMessageUUID *string `json:"refusedUserMessageUuid,omitempty" wire:"refused_user_message_uuid"`
	Content                string  `json:"content"`
}

// SDKLocalCommandOutputMessage carries printable output from a local slash command.
type SDKLocalCommandOutputMessage struct {
	sdkSystemEnvelope
	Content string `json:"content"`
}

// SDKHookStartedMessage announces hook execution.
type SDKHookStartedMessage struct {
	sdkSystemEnvelope
	HookID    string `json:"hookId"    wire:"hook_id"`
	HookName  string `json:"hookName"  wire:"hook_name"`
	HookEvent string `json:"hookEvent" wire:"hook_event"`
}

// SDKHookProgressMessage reports intermediate hook output.
type SDKHookProgressMessage struct {
	sdkSystemEnvelope
	HookID    string `json:"hookId"    wire:"hook_id"`
	HookName  string `json:"hookName"  wire:"hook_name"`
	HookEvent string `json:"hookEvent" wire:"hook_event"`
	Stdout    string `json:"stdout"`
	Stderr    string `json:"stderr"`
	Output    string `json:"output"`
}

// SDKHookResponseMessage reports a completed hook invocation.
type SDKHookResponseMessage struct {
	sdkSystemEnvelope
	HookID    string `json:"hookId"             wire:"hook_id"`
	HookName  string `json:"hookName"           wire:"hook_name"`
	HookEvent string `json:"hookEvent"          wire:"hook_event"`
	Output    string `json:"output"`
	Stdout    string `json:"stdout"`
	Stderr    string `json:"stderr"`
	ExitCode  *int   `json:"exitCode,omitempty" wire:"exit_code"`
	Outcome   string `json:"outcome"`
}
