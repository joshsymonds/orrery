package wire

import (
	"encoding/json"
	"errors"
	"fmt"
)

// SDKMessage keeps the encoded JSON of one line of Claude Code's stream-json output.
type SDKMessage struct {
	raw json.RawMessage
}

// SDKMessageVariant is a concrete member of the SDKMessage union.
type SDKMessageVariant interface {
	isSDKMessageVariant()
}

// UnknownMessage holds a copy of a line whose type or subtype this package does not declare.
type UnknownMessage struct {
	Raw json.RawMessage
}

type variantDecoder func(json.RawMessage) (interface{ SDKMessageVariant }, error)

// UnmarshalJSON preserves the union payload for typed variant decoding.
func (message *SDKMessage) UnmarshalJSON(data []byte) error {
	if !json.Valid(data) {
		return errors.New("wire: decode SDKMessage: invalid JSON")
	}
	message.raw = append(message.raw[:0], data...)
	return nil
}

// MarshalJSON returns the preserved union payload.
func (message *SDKMessage) MarshalJSON() ([]byte, error) {
	if len(message.raw) == 0 {
		return nil, errors.New("wire: cannot marshal unset SDKMessage")
	}
	return append([]byte(nil), message.raw...), nil
}

// RawJSON returns a copy of the preserved union payload.
func (message *SDKMessage) RawJSON() json.RawMessage {
	return append(json.RawMessage(nil), message.raw...)
}

// BorrowedJSON returns a read-only view of the preserved union payload without copying.
// The view aliases message and remains valid until another payload is unmarshaled into it.
func (message *SDKMessage) BorrowedJSON() json.RawMessage {
	return message.raw
}

// Variant decodes and returns the concrete typed union variant. A line whose type, system subtype,
// or result subtype is not declared returns an UnknownMessage holding a copy of the line.
func (message *SDKMessage) Variant() (interface{ SDKMessageVariant }, error) {
	if len(message.raw) == 0 {
		return nil, errors.New("wire: cannot decode unset SDKMessage")
	}
	var discriminator struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(message.raw, &discriminator); err != nil {
		return nil, fmt.Errorf("wire: decode SDKMessage object: %w", err)
	}
	if discriminator.Type == "" {
		return nil, errors.New("wire: SDKMessage is missing discriminator type")
	}

	switch discriminator.Type {
	case "system":
		return decodeSystemMessage(message.raw)
	case "result":
		return decodeResultMessage(message.raw)
	case "user":
		return decodeUserMessage(message.raw)
	default:
		return decodeDirectMessage(discriminator.Type, message.raw)
	}
}

func decodeDirectMessage(
	discriminator string,
	raw json.RawMessage,
) (interface{ SDKMessageVariant }, error) {
	decoder, ok := directVariantDecoders()[discriminator]
	if !ok {
		return unknownVariant(raw), nil
	}
	return decoder(raw)
}

func decodeSystemMessage(raw json.RawMessage) (interface{ SDKMessageVariant }, error) {
	subtype, err := messageSubtype(raw)
	if err != nil {
		return nil, err
	}
	decoder, ok := systemVariantDecoders()[subtype]
	if !ok {
		return unknownVariant(raw), nil
	}
	return decoder(raw)
}

func decodeResultMessage(raw json.RawMessage) (interface{ SDKMessageVariant }, error) {
	subtype, err := messageSubtype(raw)
	if err != nil {
		return nil, err
	}
	switch subtype {
	case "success":
		return decodeVariant[SDKResultSuccess](raw)
	case "error_during_execution", "error_max_turns", "error_max_budget_usd",
		"error_max_structured_output_retries":
		return decodeVariant[SDKResultError](raw)
	default:
		return unknownVariant(raw), nil
	}
}

// unknownVariant keeps a copy of a line that no declared variant decodes.
func unknownVariant(raw json.RawMessage) UnknownMessage {
	return UnknownMessage{Raw: append(json.RawMessage(nil), raw...)}
}

func decodeUserMessage(raw json.RawMessage) (interface{ SDKMessageVariant }, error) {
	var discriminator struct {
		IsReplay bool `json:"isReplay"`
	}
	if err := json.Unmarshal(raw, &discriminator); err != nil {
		return nil, fmt.Errorf("wire: decode user replay discriminator: %w", err)
	}
	if discriminator.IsReplay {
		return decodeVariant[SDKUserMessageReplay](raw)
	}
	return decodeVariant[SDKUserMessage](raw)
}

func messageSubtype(raw json.RawMessage) (string, error) {
	var discriminator struct {
		Subtype string `json:"subtype"`
	}
	if err := json.Unmarshal(raw, &discriminator); err != nil {
		return "", fmt.Errorf("wire: decode SDKMessage subtype: %w", err)
	}
	if discriminator.Subtype == "" {
		return "", errors.New("wire: SDKMessage is missing discriminator subtype")
	}
	return discriminator.Subtype, nil
}

func decodeVariant[T SDKMessageVariant](
	raw json.RawMessage,
) (interface{ SDKMessageVariant }, error) {
	var variant T
	if err := unmarshalWire(raw, &variant); err != nil {
		return nil, fmt.Errorf("wire: decode SDKMessage variant: %w", err)
	}
	return variant, nil
}

func directVariantDecoders() map[string]variantDecoder {
	return map[string]variantDecoder{
		"assistant":          decodeVariant[SDKAssistantMessage],
		"stream_event":       decodeVariant[SDKPartialAssistantMessage],
		"tool_progress":      decodeVariant[SDKToolProgressMessage],
		"auth_status":        decodeVariant[SDKAuthStatusMessage],
		"tool_use_summary":   decodeVariant[SDKToolUseSummaryMessage],
		"rate_limit_event":   decodeVariant[SDKRateLimitEvent],
		"prompt_suggestion":  decodeVariant[SDKPromptSuggestionMessage],
		"conversation_reset": decodeVariant[SDKConversationResetMessage],
		"command_lifecycle":  decodeVariant[SDKCommandLifecycleMessage],
	}
}

func systemVariantDecoders() map[string]variantDecoder {
	return map[string]variantDecoder{
		"init":                      decodeVariant[SDKSystemMessage],
		"compact_boundary":          decodeVariant[SDKCompactBoundaryMessage],
		"status":                    decodeVariant[SDKStatusMessage],
		"api_retry":                 decodeVariant[SDKAPIRetryMessage],
		"control_request_progress":  decodeVariant[SDKControlRequestProgressMessage],
		"model_refusal_fallback":    decodeVariant[SDKModelRefusalFallbackMessage],
		"model_refusal_no_fallback": decodeVariant[SDKModelRefusalNoFallbackMessage],
		"local_command_output":      decodeVariant[SDKLocalCommandOutputMessage],
		"hook_started":              decodeVariant[SDKHookStartedMessage],
		"hook_progress":             decodeVariant[SDKHookProgressMessage],
		"hook_response":             decodeVariant[SDKHookResponseMessage],
		"plugin_install":            decodeVariant[SDKPluginInstallMessage],
		"task_notification":         decodeVariant[SDKTaskNotificationMessage],
		"task_started":              decodeVariant[SDKTaskStartedMessage],
		"task_updated":              decodeVariant[SDKTaskUpdatedMessage],
		"task_progress":             decodeVariant[SDKTaskProgressMessage],
		"background_tasks_changed":  decodeVariant[SDKBackgroundTasksChangedMessage],
		"thinking_tokens":           decodeVariant[SDKThinkingTokensMessage],
		"session_state_changed":     decodeVariant[SDKSessionStateChangedMessage],
		"worker_shutting_down":      decodeVariant[SDKWorkerShuttingDownMessage],
		"commands_changed":          decodeVariant[SDKCommandsChangedMessage],
		"notification":              decodeVariant[SDKNotificationMessage],
		"files_persisted":           decodeVariant[SDKFilesPersistedEvent],
		"memory_recall":             decodeVariant[SDKMemoryRecallMessage],
		"elicitation_complete":      decodeVariant[SDKElicitationCompleteMessage],
		"permission_denied":         decodeVariant[SDKPermissionDeniedMessage],
		"mirror_error":              decodeVariant[SDKMirrorErrorMessage],
		"informational":             decodeVariant[SDKInformationalMessage],
	}
}
