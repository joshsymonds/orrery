package wire

import (
	"encoding/json"
	"errors"
	"fmt"
)

// BetaMessage is the event-facing subset of a model message.
// RawJSON retains fields outside that subset.
type BetaMessage struct {
	ID                string          `json:"id"`
	Container         json.RawMessage `json:"container"`
	Content           []ContentBlock  `json:"content"`
	ContextManagement json.RawMessage `json:"contextManagement" wire:"context_management"`
	Diagnostics       json.RawMessage `json:"diagnostics"`
	Model             string          `json:"model"`
	Role              string          `json:"role"`
	StopDetails       json.RawMessage `json:"stopDetails"       wire:"stop_details"`
	StopReason        *string         `json:"stopReason"        wire:"stop_reason"`
	StopSequence      *string         `json:"stopSequence"      wire:"stop_sequence"`
	Type              string          `json:"type"`
	Usage             BetaUsage       `json:"usage"`
	raw               json.RawMessage
}

// UnmarshalJSON decodes the modeled BetaMessage fields and retains the payload.
func (message *BetaMessage) UnmarshalJSON(data []byte) error {
	type decoded BetaMessage
	var value decoded
	if err := unmarshalWire(data, &value); err != nil {
		return fmt.Errorf("wire: decode BetaMessage: %w", err)
	}
	*message = BetaMessage(value)
	message.raw = append(message.raw[:0], data...)
	return nil
}

// MarshalJSON returns the retained BetaMessage payload when one was decoded.
func (message *BetaMessage) MarshalJSON() ([]byte, error) {
	if len(message.raw) != 0 {
		return append([]byte(nil), message.raw...), nil
	}
	return nil, errors.New("wire: cannot marshal unset BetaMessage")
}

// RawJSON returns a copy of the retained BetaMessage payload.
func (message *BetaMessage) RawJSON() json.RawMessage {
	return append(json.RawMessage(nil), message.raw...)
}

// BorrowedJSON returns a read-only view of the retained BetaMessage payload.
func (message *BetaMessage) BorrowedJSON() json.RawMessage {
	return message.raw
}

// BetaRawMessageStreamEvent is the event-facing subset of a streamed message event.
// RawJSON retains fields outside that subset.
type BetaRawMessageStreamEvent struct {
	Type              string                 `json:"type"`
	Message           *BetaMessage           `json:"message,omitempty"`
	ContentBlock      *ContentBlock          `json:"contentBlock,omitempty"      wire:"content_block"`
	Delta             *BetaStreamDelta       `json:"delta,omitempty"`
	Index             *int                   `json:"index,omitempty"`
	ContextManagement json.RawMessage        `json:"contextManagement,omitempty" wire:"context_management"`
	Usage             *BetaMessageDeltaUsage `json:"usage,omitempty"`
	raw               json.RawMessage
}

// UnmarshalJSON decodes the modeled stream-event fields and retains the payload.
func (event *BetaRawMessageStreamEvent) UnmarshalJSON(data []byte) error {
	type decoded BetaRawMessageStreamEvent
	var value decoded
	if err := unmarshalWire(data, &value); err != nil {
		return fmt.Errorf("wire: decode BetaRawMessageStreamEvent: %w", err)
	}
	*event = BetaRawMessageStreamEvent(value)
	event.raw = append(event.raw[:0], data...)
	return nil
}

// MarshalJSON returns the retained stream-event payload when one was decoded.
func (event *BetaRawMessageStreamEvent) MarshalJSON() ([]byte, error) {
	if len(event.raw) != 0 {
		return append([]byte(nil), event.raw...), nil
	}
	return nil, errors.New("wire: cannot marshal unset BetaRawMessageStreamEvent")
}

// RawJSON returns a copy of the retained stream-event payload.
func (event *BetaRawMessageStreamEvent) RawJSON() json.RawMessage {
	return append(json.RawMessage(nil), event.raw...)
}

// BorrowedJSON returns a read-only view of the retained stream-event payload.
func (event *BetaRawMessageStreamEvent) BorrowedJSON() json.RawMessage {
	return event.raw
}

// BetaUsage is the event-facing subset of token usage.
// RawJSON retains fields outside that subset.
type BetaUsage struct {
	CacheCreation            *BetaCacheCreation       `json:"cacheCreation"            wire:"cache_creation"`
	CacheCreationInputTokens *int64                   `json:"cacheCreationInputTokens" wire:"cache_creation_input_tokens"`
	CacheReadInputTokens     *int64                   `json:"cacheReadInputTokens"     wire:"cache_read_input_tokens"`
	InferenceGeo             *string                  `json:"inferenceGeo"             wire:"inference_geo"`
	InputTokens              int64                    `json:"inputTokens"              wire:"input_tokens"`
	Iterations               json.RawMessage          `json:"iterations"`
	OutputTokens             int64                    `json:"outputTokens"             wire:"output_tokens"`
	OutputTokensDetails      *BetaOutputTokensDetails `json:"outputTokensDetails"      wire:"output_tokens_details"`
	ServerToolUse            *BetaServerToolUsage     `json:"serverToolUse"            wire:"server_tool_use"`
	ServiceTier              *string                  `json:"serviceTier"              wire:"service_tier"`
	Speed                    *string                  `json:"speed"`
	raw                      json.RawMessage
}

// UnmarshalJSON decodes the modeled usage fields and retains the payload.
func (usage *BetaUsage) UnmarshalJSON(data []byte) error {
	type decoded BetaUsage
	var value decoded
	if err := unmarshalWire(data, &value); err != nil {
		return fmt.Errorf("wire: decode BetaUsage: %w", err)
	}
	*usage = BetaUsage(value)
	usage.raw = append(usage.raw[:0], data...)
	return nil
}

// MarshalJSON returns the retained usage payload when one was decoded.
func (usage *BetaUsage) MarshalJSON() ([]byte, error) {
	if len(usage.raw) != 0 {
		return append([]byte(nil), usage.raw...), nil
	}
	return nil, errors.New("wire: cannot marshal unset BetaUsage")
}

// RawJSON returns a copy of the retained usage payload.
func (usage *BetaUsage) RawJSON() json.RawMessage {
	return append(json.RawMessage(nil), usage.raw...)
}

// BorrowedJSON returns a read-only view of the retained usage payload.
func (usage *BetaUsage) BorrowedJSON() json.RawMessage {
	return usage.raw
}

// MessageParam is the event-facing subset of a message sent to the model.
// RawJSON retains fields outside that subset.
type MessageParam struct {
	Content MessageContent `json:"content"`
	Role    string         `json:"role"`
	raw     json.RawMessage
}

// UnmarshalJSON decodes the modeled MessageParam fields and retains the payload.
func (message *MessageParam) UnmarshalJSON(data []byte) error {
	type decoded MessageParam
	var value decoded
	if err := unmarshalWire(data, &value); err != nil {
		return fmt.Errorf("wire: decode MessageParam: %w", err)
	}
	*message = MessageParam(value)
	message.raw = append(message.raw[:0], data...)
	return nil
}

// MarshalJSON returns the retained MessageParam payload when one was decoded.
func (message *MessageParam) MarshalJSON() ([]byte, error) {
	if len(message.raw) != 0 {
		return append([]byte(nil), message.raw...), nil
	}
	return nil, errors.New("wire: cannot marshal unset MessageParam")
}

// RawJSON returns a copy of the retained MessageParam payload.
func (message *MessageParam) RawJSON() json.RawMessage {
	return append(json.RawMessage(nil), message.raw...)
}

// BorrowedJSON returns a read-only view of the retained MessageParam payload.
func (message *MessageParam) BorrowedJSON() json.RawMessage {
	return message.raw
}

// MessageContent holds either a string message body or structured content blocks.
type MessageContent struct {
	Text   *string
	Blocks []ContentBlock
	raw    json.RawMessage
}

// UnmarshalJSON decodes either supported content representation and retains the payload.
func (content *MessageContent) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		content.Text = &text
		content.Blocks = nil
		content.raw = append(content.raw[:0], data...)
		return nil
	}
	var blocks []ContentBlock
	if err := unmarshalWire(data, &blocks); err != nil {
		return fmt.Errorf("wire: decode MessageContent blocks: %w", err)
	}
	content.Text = nil
	content.Blocks = blocks
	content.raw = append(content.raw[:0], data...)
	return nil
}

// MarshalJSON returns the retained content payload when one was decoded, otherwise the text or blocks.
func (content *MessageContent) MarshalJSON() ([]byte, error) {
	if len(content.raw) != 0 {
		return append([]byte(nil), content.raw...), nil
	}
	if content.Text != nil {
		data, err := json.Marshal(*content.Text)
		if err != nil {
			return nil, fmt.Errorf("wire: encode MessageContent text: %w", err)
		}
		return data, nil
	}
	data, err := json.Marshal(content.Blocks)
	if err != nil {
		return nil, fmt.Errorf("wire: encode MessageContent blocks: %w", err)
	}
	return data, nil
}

// ContentBlock models text, thinking, tool-use, and tool-result block fields.
// Unmodeled block fields remain available through the containing payload's RawJSON.
type ContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	Thinking  string          `json:"thinking,omitempty"`
	Signature string          `json:"signature,omitempty"`
	Data      string          `json:"data,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"toolUseId,omitempty" wire:"tool_use_id"`
	Content   json.RawMessage `json:"content,omitempty"`
	IsError   *bool           `json:"isError,omitempty"   wire:"is_error"`
}

// BetaStreamDelta models text, thinking, tool-input, and message-level deltas.
type BetaStreamDelta struct {
	Type            string          `json:"type,omitempty"`
	Text            string          `json:"text,omitempty"`
	Thinking        string          `json:"thinking,omitempty"`
	PartialJSON     string          `json:"partialJson,omitempty"     wire:"partial_json"`
	Signature       string          `json:"signature,omitempty"`
	EstimatedTokens *int64          `json:"estimatedTokens,omitempty" wire:"estimated_tokens"`
	Container       json.RawMessage `json:"container,omitempty"`
	StopDetails     json.RawMessage `json:"stopDetails,omitempty"     wire:"stop_details"`
	StopReason      *string         `json:"stopReason,omitempty"      wire:"stop_reason"`
	StopSequence    *string         `json:"stopSequence,omitempty"    wire:"stop_sequence"`
}

// BetaMessageDeltaUsage models cumulative usage carried by message-delta events.
type BetaMessageDeltaUsage struct {
	CacheCreationInputTokens *int64                   `json:"cacheCreationInputTokens" wire:"cache_creation_input_tokens"`
	CacheReadInputTokens     *int64                   `json:"cacheReadInputTokens"     wire:"cache_read_input_tokens"`
	InputTokens              *int64                   `json:"inputTokens"              wire:"input_tokens"`
	Iterations               json.RawMessage          `json:"iterations"`
	OutputTokens             int64                    `json:"outputTokens"             wire:"output_tokens"`
	OutputTokensDetails      *BetaOutputTokensDetails `json:"outputTokensDetails"      wire:"output_tokens_details"`
	ServerToolUse            *BetaServerToolUsage     `json:"serverToolUse"            wire:"server_tool_use"`
}

// BetaCacheCreation is the token-count breakdown by cache lifetime.
type BetaCacheCreation struct {
	Ephemeral1HInputTokens int64 `json:"ephemeral1hInputTokens" wire:"ephemeral_1h_input_tokens"`
	Ephemeral5MInputTokens int64 `json:"ephemeral5mInputTokens" wire:"ephemeral_5m_input_tokens"`
}

// BetaOutputTokensDetails is the modeled output-token breakdown.
type BetaOutputTokensDetails struct {
	ThinkingTokens int64 `json:"thinkingTokens" wire:"thinking_tokens"`
}

// BetaServerToolUsage is the server-tool request-count breakdown.
type BetaServerToolUsage struct {
	WebFetchRequests  int64 `json:"webFetchRequests"  wire:"web_fetch_requests"`
	WebSearchRequests int64 `json:"webSearchRequests" wire:"web_search_requests"`
}
