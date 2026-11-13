package server

import (
	"encoding/json"
	"fmt"

	"google.golang.org/protobuf/encoding/protojson"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// sessionItem maps one engine event of a headless session's main thread to the conversation item it shows, stamped
// with the event's time. It returns false when the event makes no item: a sub-agent block, a thinking block, a text
// block that is not the assistant's, and every event kind that is not a prompt or a block.
func sessionItem(event store.EngineEvent) (*orreryv1.ConversationItem, bool, error) {
	switch event.Kind {
	case store.EventEnginePrompt:
		body := &orreryv1.EnginePrompt{}
		if err := protojson.Unmarshal([]byte(event.Payload), body); err != nil {
			return nil, false, fmt.Errorf("decode %s event %d: %w", event.Kind, event.Seq, err)
		}
		return &orreryv1.ConversationItem{
			Kind: "prompt", Time: event.At, Text: body.Text, Sender: body.Sender, Round: body.Round,
		}, true, nil
	case store.EventEngineBlock:
		body := &orreryv1.EngineBlock{}
		if err := protojson.Unmarshal([]byte(event.Payload), body); err != nil {
			return nil, false, fmt.Errorf("decode %s event %d: %w", event.Kind, event.Seq, err)
		}
		if body.ParentToolUseId != "" {
			return nil, false, nil
		}
		switch body.Kind {
		case "text":
			if body.Role != "assistant" {
				return nil, false, nil
			}
			return &orreryv1.ConversationItem{Kind: "reply", Time: event.At, Text: body.Text}, true, nil
		case "tool_call":
			input := protocol.ToolInputText(json.RawMessage(body.Input))
			return &orreryv1.ConversationItem{
				Kind: "tool", Time: event.At, Tool: body.Name, Input: input, Id: body.ToolUseId,
			}, true, nil
		case "tool_result":
			return &orreryv1.ConversationItem{
				Kind: "result", Time: event.At, Id: body.ToolUseId, Text: body.Content, Error: body.IsError,
			}, true, nil
		default:
			return nil, false, nil
		}
	default:
		return nil, false, nil
	}
}

// sessionRequest maps an engine_request payload to the open request the phone answers. It returns false when the
// request was refused or is of a kind the phone does not answer.
func sessionRequest(payload string) (*orreryv1.OpenRequest, bool, error) {
	body := &orreryv1.EngineRequest{}
	if err := protojson.Unmarshal([]byte(payload), body); err != nil {
		return nil, false, fmt.Errorf("decode engine request: %w", err)
	}
	if body.Refused {
		return nil, false, nil
	}
	switch body.Kind {
	case "permission":
		return &orreryv1.OpenRequest{
			Id: body.RequestId, Kind: body.Kind, Question: body.ToolName, Options: []string{"allow", "deny"},
		}, true, nil
	case "question":
		question, options := questionShape(body.Input)
		return &orreryv1.OpenRequest{
			Id:       body.RequestId,
			Kind:     body.Kind,
			Question: question,
			Options:  options,
		}, true, nil
	default:
		return nil, false, nil
	}
}

// askUserQuestionInput is the part of an AskUserQuestion input that an open request shows.
type askUserQuestionInput struct {
	Questions []struct {
		Question    string `json:"question"`
		MultiSelect bool   `json:"multiSelect"`
		Options     []struct {
			Label string `json:"label"`
		} `json:"options"`
	} `json:"questions"`
}

// questionShape returns the question text and the option labels of an AskUserQuestion input. The labels are the
// answers the phone may send only when the input has exactly one question that is not multiSelect; any other shape
// shows its first question's text, or none when it has no question or its input does not decode, and no labels.
func questionShape(input string) (string, []string) {
	var fields askUserQuestionInput
	if err := json.Unmarshal([]byte(input), &fields); err != nil || len(fields.Questions) == 0 {
		return "", nil
	}
	first := fields.Questions[0]
	if len(fields.Questions) != 1 || first.MultiSelect {
		return first.Question, nil
	}
	labels := make([]string, 0, len(first.Options))
	for _, option := range first.Options {
		labels = append(labels, option.Label)
	}
	return first.Question, labels
}
