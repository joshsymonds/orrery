package wire

import (
	"encoding/json"
	"strings"
	"testing"
)

const (
	testMessageID = "00000000-0000-4000-8000-000000000001"
	testToolUseID = "00000000-0000-4000-8000-000000000002"
	testModel     = "example-model"
)

// rawPayload is a payload type that retains its wire bytes.
type rawPayload interface {
	RawJSON() json.RawMessage
	BorrowedJSON() json.RawMessage
	MarshalJSON() ([]byte, error)
}

// marshaler is a payload type that encodes itself.
type marshaler interface {
	MarshalJSON() ([]byte, error)
}

func TestBetaMessageDecodesModelledFields(t *testing.T) {
	data := []byte(`{"id":"` + testMessageID + `","type":"message","role":"assistant","model":"` + testModel +
		`","content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn","stop_sequence":null,` +
		`"container":{"id":"example"},"context_management":{"applied":true},"diagnostics":{"trace":"example"},` +
		`"stop_details":{"kind":"example"},"usage":{"input_tokens":12,"output_tokens":34}}`)
	var got BetaMessage
	if err := unmarshalWire(data, &got); err != nil {
		t.Fatalf("unmarshalWire: %v", err)
	}
	if got.ID != testMessageID || got.Type != "message" || got.Role != "assistant" || got.Model != testModel {
		t.Fatalf("header fields = %+v", got)
	}
	if len(got.Content) != 1 || got.Content[0].Text != "hello" {
		t.Fatalf("Content = %+v, want one text block with hello", got.Content)
	}
	if got.StopReason == nil || *got.StopReason != "end_turn" {
		t.Fatalf("StopReason = %v, want end_turn", got.StopReason)
	}
	if got.StopSequence != nil {
		t.Fatalf("StopSequence = %q, want nil for JSON null", *got.StopSequence)
	}
	if string(got.Container) != `{"id":"example"}` || string(got.ContextManagement) != `{"applied":true}` {
		t.Fatalf("Container = %s, ContextManagement = %s", got.Container, got.ContextManagement)
	}
	if string(got.Diagnostics) != `{"trace":"example"}` || string(got.StopDetails) != `{"kind":"example"}` {
		t.Fatalf("Diagnostics = %s, StopDetails = %s", got.Diagnostics, got.StopDetails)
	}
	if got.Usage.InputTokens != 12 || got.Usage.OutputTokens != 34 {
		t.Fatalf("Usage = %+v, want input 12 and output 34", got.Usage)
	}
}

func TestBetaRawMessageStreamEventDecodesModelledFields(t *testing.T) {
	data := []byte(`{"type":"content_block_start","index":2,"content_block":{"type":"tool_use",` +
		`"id":"` + testToolUseID + `","name":"example-tool","input":{"path":"example"}},` +
		`"context_management":{"applied":false}}`)
	var got BetaRawMessageStreamEvent
	if err := unmarshalWire(data, &got); err != nil {
		t.Fatalf("unmarshalWire: %v", err)
	}
	if got.Type != "content_block_start" || got.Index == nil || *got.Index != 2 {
		t.Fatalf("Type = %q, Index = %v", got.Type, got.Index)
	}
	if got.ContentBlock == nil || got.ContentBlock.ID != testToolUseID || got.ContentBlock.Name != "example-tool" {
		t.Fatalf("ContentBlock = %+v", got.ContentBlock)
	}
	if string(got.ContentBlock.Input) != `{"path":"example"}` {
		t.Fatalf("ContentBlock.Input = %s", got.ContentBlock.Input)
	}
	if string(got.ContextManagement) != `{"applied":false}` {
		t.Fatalf("ContextManagement = %s", got.ContextManagement)
	}
}

func TestBetaRawMessageStreamEventDecodesMessageStart(t *testing.T) {
	data := []byte(`{"type":"message_start","message":{"id":"` + testMessageID + `","type":"message",` +
		`"role":"assistant","model":"` + testModel + `","content":[],"usage":{"input_tokens":3}}}`)
	var got BetaRawMessageStreamEvent
	if err := unmarshalWire(data, &got); err != nil {
		t.Fatalf("unmarshalWire: %v", err)
	}
	if got.Message == nil || got.Message.ID != testMessageID || got.Message.Usage.InputTokens != 3 {
		t.Fatalf("Message = %+v", got.Message)
	}
}

func TestBetaStreamDeltaDecodesModelledFields(t *testing.T) {
	data := []byte(`{"type":"input_json_delta","text":"","partial_json":"{\"a\"","estimated_tokens":5,` +
		`"stop_reason":"tool_use","stop_sequence":null,"stop_details":{"kind":"example"},` +
		`"container":{"id":"example"}}`)
	var got BetaStreamDelta
	if err := unmarshalWire(data, &got); err != nil {
		t.Fatalf("unmarshalWire: %v", err)
	}
	if got.Type != "input_json_delta" || got.PartialJSON != `{"a"` {
		t.Fatalf("Type = %q, PartialJSON = %q", got.Type, got.PartialJSON)
	}
	if got.EstimatedTokens == nil || *got.EstimatedTokens != 5 {
		t.Fatalf("EstimatedTokens = %v, want 5", got.EstimatedTokens)
	}
	if got.StopReason == nil || *got.StopReason != "tool_use" || got.StopSequence != nil {
		t.Fatalf("StopReason = %v, StopSequence = %v", got.StopReason, got.StopSequence)
	}
	if string(got.StopDetails) != `{"kind":"example"}` || string(got.Container) != `{"id":"example"}` {
		t.Fatalf("StopDetails = %s, Container = %s", got.StopDetails, got.Container)
	}
}

func TestBetaMessageDeltaUsageDecodesModelledFields(t *testing.T) {
	data := []byte(`{"cache_creation_input_tokens":1,"cache_read_input_tokens":2,"input_tokens":3,` +
		`"iterations":[{"kind":"example"}],"output_tokens":4,"output_tokens_details":{"thinking_tokens":5},` +
		`"server_tool_use":{"web_fetch_requests":6,"web_search_requests":7}}`)
	var got BetaMessageDeltaUsage
	if err := unmarshalWire(data, &got); err != nil {
		t.Fatalf("unmarshalWire: %v", err)
	}
	if got.CacheCreationInputTokens == nil || *got.CacheCreationInputTokens != 1 {
		t.Fatalf("CacheCreationInputTokens = %v, want 1", got.CacheCreationInputTokens)
	}
	if got.CacheReadInputTokens == nil || *got.CacheReadInputTokens != 2 {
		t.Fatalf("CacheReadInputTokens = %v, want 2", got.CacheReadInputTokens)
	}
	if got.InputTokens == nil || *got.InputTokens != 3 || got.OutputTokens != 4 {
		t.Fatalf("InputTokens = %v, OutputTokens = %d", got.InputTokens, got.OutputTokens)
	}
	if string(got.Iterations) != `[{"kind":"example"}]` {
		t.Fatalf("Iterations = %s", got.Iterations)
	}
	if got.OutputTokensDetails == nil || got.OutputTokensDetails.ThinkingTokens != 5 {
		t.Fatalf("OutputTokensDetails = %+v, want thinking 5", got.OutputTokensDetails)
	}
	if got.ServerToolUse == nil || got.ServerToolUse.WebFetchRequests != 6 || got.ServerToolUse.WebSearchRequests != 7 {
		t.Fatalf("ServerToolUse = %+v, want fetch 6 and search 7", got.ServerToolUse)
	}
}

func TestBetaUsageDecodesModelledFields(t *testing.T) {
	data := []byte(`{"cache_creation":{"ephemeral_1h_input_tokens":8,"ephemeral_5m_input_tokens":9},` +
		`"cache_creation_input_tokens":10,"cache_read_input_tokens":11,"inference_geo":"example-geo",` +
		`"input_tokens":12,"iterations":[],"output_tokens":13,"output_tokens_details":{"thinking_tokens":14},` +
		`"server_tool_use":{"web_fetch_requests":15,"web_search_requests":16},"service_tier":"example-tier",` +
		`"speed":"example-speed"}`)
	var got BetaUsage
	if err := unmarshalWire(data, &got); err != nil {
		t.Fatalf("unmarshalWire: %v", err)
	}
	if got.CacheCreation == nil || got.CacheCreation.Ephemeral1HInputTokens != 8 ||
		got.CacheCreation.Ephemeral5MInputTokens != 9 {
		t.Fatalf("CacheCreation = %+v", got.CacheCreation)
	}
	if got.CacheCreationInputTokens == nil || *got.CacheCreationInputTokens != 10 {
		t.Fatalf("CacheCreationInputTokens = %v, want 10", got.CacheCreationInputTokens)
	}
	if got.CacheReadInputTokens == nil || *got.CacheReadInputTokens != 11 {
		t.Fatalf("CacheReadInputTokens = %v, want 11", got.CacheReadInputTokens)
	}
	if got.InferenceGeo == nil || *got.InferenceGeo != "example-geo" {
		t.Fatalf("InferenceGeo = %v, want example-geo", got.InferenceGeo)
	}
	if got.InputTokens != 12 || got.OutputTokens != 13 {
		t.Fatalf("InputTokens = %d, OutputTokens = %d", got.InputTokens, got.OutputTokens)
	}
	if got.OutputTokensDetails == nil || got.OutputTokensDetails.ThinkingTokens != 14 {
		t.Fatalf("OutputTokensDetails = %+v, want thinking 14", got.OutputTokensDetails)
	}
	if got.ServerToolUse == nil || got.ServerToolUse.WebFetchRequests != 15 ||
		got.ServerToolUse.WebSearchRequests != 16 {
		t.Fatalf("ServerToolUse = %+v, want fetch 15 and search 16", got.ServerToolUse)
	}
	if got.ServiceTier == nil || *got.ServiceTier != "example-tier" {
		t.Fatalf("ServiceTier = %v, want example-tier", got.ServiceTier)
	}
	if got.Speed == nil || *got.Speed != "example-speed" {
		t.Fatalf("Speed = %v, want example-speed", got.Speed)
	}
}

func TestMessageParamDecodesModelledFields(t *testing.T) {
	data := []byte(`{"role":"user","content":[{"type":"tool_result","tool_use_id":"` + testToolUseID +
		`","is_error":true,"content":"example"}]}`)
	var got MessageParam
	if err := unmarshalWire(data, &got); err != nil {
		t.Fatalf("unmarshalWire: %v", err)
	}
	if got.Role != "user" {
		t.Fatalf("Role = %q, want user", got.Role)
	}
	if len(got.Content.Blocks) != 1 {
		t.Fatalf("Content.Blocks = %+v, want one block", got.Content.Blocks)
	}
	block := got.Content.Blocks[0]
	if block.Type != "tool_result" || block.ToolUseID != testToolUseID {
		t.Fatalf("block = %+v, want tool_result with tool_use_id", block)
	}
	if block.IsError == nil || !*block.IsError {
		t.Fatalf("block IsError = %v, want true", block.IsError)
	}
}

func TestContentBlockDecodesModelledFields(t *testing.T) {
	data := []byte(`{"type":"tool_result","tool_use_id":"` + testToolUseID + `","is_error":false,` +
		`"content":[{"type":"text","text":"example"}],"signature":"example-signature","data":"example-data"}`)
	var got ContentBlock
	if err := unmarshalWire(data, &got); err != nil {
		t.Fatalf("unmarshalWire: %v", err)
	}
	if got.Type != "tool_result" || got.ToolUseID != testToolUseID {
		t.Fatalf("got %+v, want tool_result with tool_use_id", got)
	}
	if got.IsError == nil || *got.IsError {
		t.Fatalf("IsError = %v, want false", got.IsError)
	}
	if string(got.Content) != `[{"type":"text","text":"example"}]` {
		t.Fatalf("Content = %s", got.Content)
	}
	if got.Signature != "example-signature" || got.Data != "example-data" {
		t.Fatalf("Signature = %q, Data = %q", got.Signature, got.Data)
	}
}

func TestBetaCacheCreationDecodesModelledFields(t *testing.T) {
	var got BetaCacheCreation
	data := []byte(`{"ephemeral_1h_input_tokens":21,"ephemeral_5m_input_tokens":22}`)
	if err := unmarshalWire(data, &got); err != nil {
		t.Fatalf("unmarshalWire: %v", err)
	}
	if got.Ephemeral1HInputTokens != 21 || got.Ephemeral5MInputTokens != 22 {
		t.Fatalf("got %+v, want 1h 21 and 5m 22", got)
	}
}

func TestBetaOutputTokensDetailsDecodesModelledFields(t *testing.T) {
	var got BetaOutputTokensDetails
	if err := unmarshalWire([]byte(`{"thinking_tokens":23}`), &got); err != nil {
		t.Fatalf("unmarshalWire: %v", err)
	}
	if got.ThinkingTokens != 23 {
		t.Fatalf("ThinkingTokens = %d, want 23", got.ThinkingTokens)
	}
}

func TestBetaServerToolUsageDecodesModelledFields(t *testing.T) {
	var got BetaServerToolUsage
	if err := unmarshalWire([]byte(`{"web_fetch_requests":24,"web_search_requests":25}`), &got); err != nil {
		t.Fatalf("unmarshalWire: %v", err)
	}
	if got.WebFetchRequests != 24 || got.WebSearchRequests != 25 {
		t.Fatalf("got %+v, want fetch 24 and search 25", got)
	}
}

// rawInputKey is a key that no field models, so the retained payload must include it.
const rawInputKey = `"x_unmodelled":{"nested":[1,2]}`

func TestRawPayloadsKeepFullPayload(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		payload rawPayload
	}{
		{
			name: "BetaMessage",
			input: `{"id":"` + testMessageID + `","type":"message","role":"assistant","model":"` + testModel +
				`","content":[],"usage":{"input_tokens":1,"output_tokens":2},` + rawInputKey + `}`,
			payload: &BetaMessage{},
		},
		{
			name: "BetaRawMessageStreamEvent",
			input: `{"type":"message_start","index":0,"message":{"id":"` + testMessageID +
				`","type":"message","role":"assistant","model":"` + testModel + `","content":[]},` +
				rawInputKey + `}`,
			payload: &BetaRawMessageStreamEvent{},
		},
		{
			name:    "BetaUsage",
			input:   `{"input_tokens":3,"output_tokens":4,"iterations":[{"kind":"example"}],` + rawInputKey + `}`,
			payload: &BetaUsage{},
		},
		{
			name:    "MessageParam",
			input:   `{"role":"user","content":"hello",` + rawInputKey + `}`,
			payload: &MessageParam{},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := unmarshalWire([]byte(test.input), test.payload); err != nil {
				t.Fatalf("unmarshalWire: %v", err)
			}
			if got := string(test.payload.BorrowedJSON()); got != test.input {
				t.Fatalf("BorrowedJSON = %s, want %s", got, test.input)
			}
			if got := string(test.payload.RawJSON()); got != test.input {
				t.Fatalf("RawJSON = %s, want %s", got, test.input)
			}
			marshaled, err := test.payload.MarshalJSON()
			if err != nil {
				t.Fatalf("MarshalJSON: %v", err)
			}
			if string(marshaled) != test.input {
				t.Fatalf("MarshalJSON = %s, want %s", marshaled, test.input)
			}
		})
	}
}

func TestRawJSONReturnsCopy(t *testing.T) {
	input := `{"id":"` + testMessageID + `","type":"message","role":"assistant","model":"` + testModel +
		`","content":[],"usage":{},` + rawInputKey + `}`
	var message BetaMessage
	if err := unmarshalWire([]byte(input), &message); err != nil {
		t.Fatalf("unmarshalWire: %v", err)
	}
	copied := message.RawJSON()
	if len(copied) == 0 {
		t.Fatal("RawJSON returned no bytes for a decoded message")
	}
	copied[0] = 'X'
	if got := string(message.BorrowedJSON()); got != input {
		t.Fatalf("BorrowedJSON after mutating RawJSON copy = %s, want %s", got, input)
	}
}

func TestUnsetRawPayloadsFailToMarshal(t *testing.T) {
	tests := map[string]marshaler{
		"BetaMessage":               &BetaMessage{},
		"BetaRawMessageStreamEvent": &BetaRawMessageStreamEvent{},
		"BetaUsage":                 &BetaUsage{},
		"MessageParam":              &MessageParam{},
	}
	for name, payload := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := payload.MarshalJSON(); err == nil {
				t.Fatalf("MarshalJSON on unset %s returned nil error", name)
			}
		})
	}
}

func TestMessageContentDecodesString(t *testing.T) {
	var got MessageContent
	if err := unmarshalWire([]byte(`"hello"`), &got); err != nil {
		t.Fatalf("UnmarshalJSON: %v", err)
	}
	if got.Text == nil || *got.Text != "hello" {
		t.Fatalf("Text = %v, want hello", got.Text)
	}
	if got.Blocks != nil {
		t.Fatalf("Blocks = %+v, want nil for string content", got.Blocks)
	}
}

func TestMessageContentDecodesBlockArray(t *testing.T) {
	data := []byte(`[{"type":"text","text":"hi"},{"type":"tool_result","tool_use_id":"` + testToolUseID +
		`","is_error":true}]`)
	var got MessageContent
	if err := unmarshalWire(data, &got); err != nil {
		t.Fatalf("unmarshalWire: %v", err)
	}
	if got.Text != nil {
		t.Fatalf("Text = %q, want nil for block content", *got.Text)
	}
	if len(got.Blocks) != 2 || got.Blocks[0].Text != "hi" {
		t.Fatalf("Blocks = %+v, want text hi then a second block", got.Blocks)
	}
	if got.Blocks[1].ToolUseID != testToolUseID {
		t.Fatalf("second block ToolUseID = %q, want %q", got.Blocks[1].ToolUseID, testToolUseID)
	}
	if got.Blocks[1].IsError == nil || !*got.Blocks[1].IsError {
		t.Fatalf("second block IsError = %v, want true", got.Blocks[1].IsError)
	}
}

func TestMessageContentRejectsInvalidJSON(t *testing.T) {
	var got MessageContent
	err := unmarshalWire([]byte(`{`), &got)
	if err == nil {
		t.Fatal("UnmarshalJSON on invalid JSON returned nil error")
	}
	if !strings.HasPrefix(err.Error(), "wire: ") {
		t.Fatalf("error = %q, want prefix %q", err, "wire: ")
	}
}

func TestMessageContentRejectsNeitherStringNorBlocks(t *testing.T) {
	for _, input := range []string{`123`, `{}`, `[1]`} {
		var got MessageContent
		err := got.UnmarshalJSON([]byte(input))
		if err == nil {
			t.Fatalf("UnmarshalJSON(%s) returned nil error", input)
		}
		if !strings.HasPrefix(err.Error(), "wire: decode MessageContent") {
			t.Fatalf("UnmarshalJSON(%s) error = %q, want prefix %q", input, err, "wire: decode MessageContent")
		}
	}
}

func TestMessageContentMarshalsRawPayload(t *testing.T) {
	for _, input := range []string{`"hello"`, `[{"type":"text","text":"hi"}]`} {
		var got MessageContent
		if err := unmarshalWire([]byte(input), &got); err != nil {
			t.Fatalf("unmarshalWire(%s): %v", input, err)
		}
		marshaled, err := got.MarshalJSON()
		if err != nil {
			t.Fatalf("MarshalJSON: %v", err)
		}
		if string(marshaled) != input {
			t.Fatalf("MarshalJSON = %s, want %s", marshaled, input)
		}
	}
}

func TestMessageContentMarshalsText(t *testing.T) {
	text := "hello"
	marshaled, err := (&MessageContent{Text: &text}).MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	if string(marshaled) != `"hello"` {
		t.Fatalf("MarshalJSON = %s, want %q", marshaled, `"hello"`)
	}
}

func TestMessageContentMarshalsBlocks(t *testing.T) {
	content := MessageContent{Blocks: []ContentBlock{{Type: "text", Text: "hi"}}}
	marshaled, err := content.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	if string(marshaled) != `[{"type":"text","text":"hi"}]` {
		t.Fatalf("MarshalJSON = %s", marshaled)
	}
}
