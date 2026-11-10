package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/joshsymonds/orrery/internal/engine/wire"
)

// Wire spellings of the message roles, content blocks, stream events and deltas the translator reads.
const (
	roleAssistant = "assistant"
	roleUser      = "user"

	contentText       = "text"
	contentThinking   = "thinking"
	contentToolUse    = "tool_use"
	contentToolResult = "tool_result"

	streamMessageStart   = "message_start"
	streamBlockStart     = "content_block_start"
	streamBlockDelta     = "content_block_delta"
	deltaText            = "text_delta"
	deltaThinking        = "thinking_delta"
	deltaInputJSON       = "input_json_delta"
	deltaSignature       = "signature_delta"
	controlRequest       = "control_request"
	controlResponse      = "control_response"
	controlCancelRequest = "control_cancel_request"
)

// translator turns the stdout lines of one session into events. It remembers the message id that
// each parent tool use id streams under, so that a delta names the message it belongs to.
type translator struct {
	messages map[string]string
}

func newTranslator() *translator {
	return &translator{messages: make(map[string]string)}
}

// translate returns the events one stdout line carries, in order. A line that does not decode or fit
// the declared messages yields one Unknown, and translation goes on with the next line. Control
// envelopes and the messages that carry no event yield none.
func (t *translator) translate(line []byte) []Event {
	var message wire.SDKMessage
	if err := json.Unmarshal(line, &message); err != nil {
		return unknownLine(line, err)
	}
	variant, err := message.Variant()
	if err != nil {
		return unknownLine(line, err)
	}
	events, err := t.variantEvents(variant)
	if err != nil {
		return unknownLine(line, err)
	}
	return events
}

// unknownLine returns the one Unknown a line that was not translated becomes. The copy keeps the event
// independent of the caller's buffer.
func unknownLine(line []byte, err error) []Event {
	return []Event{Unknown{Line: bytes.Clone(line), Err: err}}
}

func (t *translator) variantEvents(variant wire.SDKMessageVariant) ([]Event, error) {
	switch message := variant.(type) {
	case wire.SDKSystemMessage:
		return []Event{Started{SessionID: message.SessionID, PermissionMode: message.PermissionMode}}, nil
	case wire.SDKAssistantMessage:
		return assistantEvents(message)
	case wire.SDKUserMessage:
		return userEvents(message)
	case wire.SDKPartialAssistantMessage:
		return t.streamEvents(message)
	case wire.SDKResultSuccess:
		return []Event{turnEnded(message.SDKResultMessage, message.Result)}, nil
	case wire.SDKResultError:
		return []Event{turnEnded(message.SDKResultMessage, "")}, nil
	case wire.UnknownMessage:
		return undeclaredEvents(message.Raw)
	default:
		return nil, nil
	}
}

// undeclaredEvents returns the events of a line the wire package does not declare. A control envelope
// yields none, and any other line is an error that names its type and subtype.
func undeclaredEvents(raw json.RawMessage) ([]Event, error) {
	var head struct {
		Type    string `json:"type"`
		Subtype string `json:"subtype"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return nil, fmt.Errorf("decode undeclared message head: %w", err)
	}
	switch head.Type {
	case controlRequest, controlResponse, controlCancelRequest:
		return nil, nil
	}
	return nil, fmt.Errorf("undeclared stream-json message type %q subtype %q", head.Type, head.Subtype)
}

func turnEnded(result wire.SDKResultMessage, text string) TurnEnded {
	ended := TurnEnded{Subtype: result.Subtype, IsError: result.IsError, Result: text}
	if result.TerminalReason != nil {
		ended.TerminalReason = *result.TerminalReason
	}
	if result.Origin != nil {
		ended.Origin = result.Origin.Kind
	}
	return ended
}

func assistantEvents(message wire.SDKAssistantMessage) ([]Event, error) {
	base := Block{
		Role:            roleAssistant,
		MessageID:       message.Message.ID,
		ParentToolUseID: derefString(message.ParentToolUseID),
	}
	return blockEvents(message.Message.Content, base, assistantBlock)
}

func userEvents(message wire.SDKUserMessage) ([]Event, error) {
	parent := derefString(message.ParentToolUseID)
	if message.Message.Content.Text != nil {
		return []Event{Block{
			Kind:            BlockText,
			Role:            roleUser,
			ParentToolUseID: parent,
			Text:            *message.Message.Content.Text,
		}}, nil
	}
	base := Block{Role: roleUser, ParentToolUseID: parent}
	return blockEvents(message.Message.Content.Blocks, base, userBlock)
}

// blockEvents returns one Block per content block, in order. Each block starts from base and takes its
// own fields from build; the first block build rejects fails the whole line.
func blockEvents(
	content []wire.ContentBlock,
	base Block,
	build func(wire.ContentBlock, Block) (Block, error),
) ([]Event, error) {
	events := make([]Event, 0, len(content))
	for _, item := range content {
		block, err := build(item, base)
		if err != nil {
			return nil, err
		}
		events = append(events, block)
	}
	return events, nil
}

func assistantBlock(item wire.ContentBlock, base Block) (Block, error) {
	switch item.Type {
	case contentText:
		base.Kind, base.Text = BlockText, item.Text
	case contentThinking:
		base.Kind, base.Text = BlockThinking, item.Thinking
	case contentToolUse:
		base.Kind, base.ToolUseID, base.Name, base.Input = BlockToolCall, item.ID, item.Name, item.Input
	default:
		return Block{}, fmt.Errorf("undeclared assistant content block type %q", item.Type)
	}
	return base, nil
}

func userBlock(item wire.ContentBlock, base Block) (Block, error) {
	switch item.Type {
	case contentText:
		base.Kind, base.Text = BlockText, item.Text
	case contentToolResult:
		base.Kind, base.ToolUseID, base.Content = BlockToolResult, item.ToolUseID, item.Content
		base.IsError = item.IsError != nil && *item.IsError
	default:
		return Block{}, fmt.Errorf("undeclared user content block type %q", item.Type)
	}
	return base, nil
}

func (t *translator) streamEvents(message wire.SDKPartialAssistantMessage) ([]Event, error) {
	parent := derefString(message.ParentToolUseID)
	event := message.Event
	switch event.Type {
	case streamMessageStart:
		if event.Message == nil {
			return nil, errors.New("message_start carries no message")
		}
		t.messages[parent] = event.Message.ID
		return nil, nil
	case streamBlockStart:
		return t.blockStartEvents(event, parent)
	case streamBlockDelta:
		return t.deltaEvents(event, parent)
	default:
		return nil, nil
	}
}

// blockStartEvents reports a tool call's start. A text or thinking block starts silently, because its
// deltas carry its text.
func (t *translator) blockStartEvents(event wire.BetaRawMessageStreamEvent, parent string) ([]Event, error) {
	block := event.ContentBlock
	if block == nil {
		return nil, errors.New("content_block_start carries no content block")
	}
	switch block.Type {
	case contentText, contentThinking:
		return nil, nil
	case contentToolUse:
		index, err := blockIndex(event)
		if err != nil {
			return nil, err
		}
		return []Event{ToolCallStarted{
			MessageID:       t.messages[parent],
			Index:           index,
			ParentToolUseID: parent,
			ToolUseID:       block.ID,
			Name:            block.Name,
		}}, nil
	default:
		return nil, fmt.Errorf("undeclared stream content block type %q", block.Type)
	}
}

// deltaEvents reports streamed text and thinking. Input JSON and signatures are partial or secret
// material, so they yield nothing.
func (t *translator) deltaEvents(event wire.BetaRawMessageStreamEvent, parent string) ([]Event, error) {
	if event.Delta == nil {
		return nil, errors.New("content_block_delta carries no delta")
	}
	switch event.Delta.Type {
	case deltaText, deltaThinking:
		return t.textEvents(event, parent)
	case deltaInputJSON, deltaSignature:
		return nil, nil
	default:
		return nil, fmt.Errorf("undeclared stream delta type %q", event.Delta.Type)
	}
}

func (t *translator) textEvents(event wire.BetaRawMessageStreamEvent, parent string) ([]Event, error) {
	index, err := blockIndex(event)
	if err != nil {
		return nil, err
	}
	if event.Delta.Type == deltaThinking {
		return []Event{ThinkingDelta{
			MessageID:       t.messages[parent],
			Index:           index,
			ParentToolUseID: parent,
			Text:            event.Delta.Thinking,
		}}, nil
	}
	return []Event{TextDelta{
		MessageID:       t.messages[parent],
		Index:           index,
		ParentToolUseID: parent,
		Text:            event.Delta.Text,
	}}, nil
}

func blockIndex(event wire.BetaRawMessageStreamEvent) (int, error) {
	if event.Index == nil {
		return 0, fmt.Errorf("stream event %q carries no block index", event.Type)
	}
	return *event.Index, nil
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
