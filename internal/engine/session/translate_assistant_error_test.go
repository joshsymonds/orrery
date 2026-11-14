package session

import "testing"

// A turn that hits a usage limit gives its assistant line an error field, and the turn's text follows it.
func TestAssistantLineWithErrorYieldsAssistantErrorBeforeBlocks(t *testing.T) {
	t.Parallel()
	tr := newTranslator()
	got := translateLines(t, tr,
		`{"type":"assistant","message":{"id":"msg_limit","type":"message","role":"assistant","content":[`+
			`{"type":"text","text":"limit words"}]},`+
			`"error":"rate_limit","parent_tool_use_id":null,"session_id":"session_1"}`,
	)
	requireEvents(t, got, []Event{
		AssistantError{Error: "rate_limit"},
		Block{Kind: BlockText, Role: "assistant", MessageID: "msg_limit", Text: "limit words"},
	})
}

func TestAssistantLineWithoutErrorYieldsNoAssistantError(t *testing.T) {
	t.Parallel()
	tr := newTranslator()
	got := translateLines(t, tr,
		`{"type":"assistant","message":{"id":"msg_plain","type":"message","role":"assistant","content":[`+
			`{"type":"text","text":"plain words"}]},`+
			`"parent_tool_use_id":null,"session_id":"session_1"}`,
	)
	requireEvents(t, got, []Event{
		Block{Kind: BlockText, Role: "assistant", MessageID: "msg_plain", Text: "plain words"},
	})
}
