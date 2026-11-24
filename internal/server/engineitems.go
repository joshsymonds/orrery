package server

import (
	"encoding/json"
	"fmt"

	"google.golang.org/protobuf/encoding/protojson"

	"github.com/joshsymonds/orrery/internal/conversation"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// The status of a background task that runs, and the type of an agent task, as engine_task events carry them.
const (
	taskRunning = "running"
	taskAgent   = "agent"
)

// sessionItem maps one engine event of a headless session's main thread to the conversation item it shows, stamped
// with the event's time. It returns false when the event makes no item: a sub-agent block, a thinking block, a text
// block that is not the assistant's, a turn end that the owner's interrupt did not end, an engine_task that does not
// end an agent task (see finishedTaskStatus), and every event kind that is not a prompt, a block, a turn end, a task or a
// design record.
func sessionItem(event store.EngineEvent) (*orreryv1.ConversationItem, bool, error) {
	switch event.Kind {
	case store.EventEnginePrompt:
		body := &orreryv1.EnginePrompt{}
		if err := protojson.Unmarshal([]byte(event.Payload), body); err != nil {
			return nil, false, fmt.Errorf("decode %s event %d: %w", event.Kind, event.Seq, err)
		}
		return &orreryv1.ConversationItem{
			Kind:     "prompt",
			Time:     event.At,
			Text:     body.Text,
			Sender:   body.Sender,
			Round:    body.Round,
			PromptId: body.PromptId,
			Client:   body.Client,
		}, true, nil
	case store.EventEngineTurnEnded:
		body := &orreryv1.EngineTurnEnded{}
		if err := protojson.Unmarshal([]byte(event.Payload), body); err != nil {
			return nil, false, fmt.Errorf("decode %s event %d: %w", event.Kind, event.Seq, err)
		}
		if !body.OwnerInterrupted {
			return nil, false, nil
		}
		return &orreryv1.ConversationItem{Kind: "interrupted", Time: event.At}, true, nil
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
				Kind:     "tool",
				Time:     event.At,
				Tool:     body.Name,
				Input:    input,
				Id:       body.ToolUseId,
				Epic:     conversation.CreatedEpic(body.Name, json.RawMessage(body.Input)),
				Contract: conversation.WrittenContract(body.Name, json.RawMessage(body.Input)),
			}, true, nil
		case "tool_result":
			return &orreryv1.ConversationItem{
				Kind: "result", Time: event.At, Id: body.ToolUseId, Text: body.Content, Error: body.IsError,
				Added: body.Added, Removed: body.Removed,
			}, true, nil
		default:
			return nil, false, nil
		}
	case store.EventEngineTask:
		return taskItem(event)
	case store.EventEngineDesign:
		return designItem(event)
	default:
		return nil, false, nil
	}
}

// designItem maps an engine_design event to the design item it shows: the record of a line of a design's log, stamped
// with the event's time. Its design is the design's id, with the line's what and the ids it touched, and its text is
// the line's what, then its words on a new line when the line has any.
func designItem(event store.EngineEvent) (*orreryv1.ConversationItem, bool, error) {
	body := &orreryv1.EngineDesign{}
	if err := protojson.Unmarshal([]byte(event.Payload), body); err != nil {
		return nil, false, fmt.Errorf("decode %s event %d: %w", event.Kind, event.Seq, err)
	}
	text := body.What
	if body.Words != "" {
		text += "\n" + body.Words
	}
	return &orreryv1.ConversationItem{
		Kind:   "design",
		Time:   event.At,
		Text:   text,
		Design: &orreryv1.EngineDesign{Design: body.Design, What: body.What, Ids: body.Ids},
	}, true, nil
}

// taskItem maps an engine_task event to the agent_finished item it shows: the end of an agent task, stamped with the
// event's time. It returns false when the event does not end an agent task.
func taskItem(event store.EngineEvent) (*orreryv1.ConversationItem, bool, error) {
	body, err := sessionTask(event)
	if err != nil {
		return nil, false, err
	}
	if body.Type != taskAgent || !finishedTaskStatus(body.Status) {
		return nil, false, nil
	}
	return &orreryv1.ConversationItem{
		Kind:   "agent_finished",
		Time:   event.At,
		Text:   body.Description,
		Id:     body.ToolUseId,
		Tool:   body.SubagentType,
		Notice: body.Status,
	}, true, nil
}

// sessionTask decodes the payload of an engine_task event.
func sessionTask(event store.EngineEvent) (*orreryv1.EngineTask, error) {
	body := &orreryv1.EngineTask{}
	if err := protojson.Unmarshal([]byte(event.Payload), body); err != nil {
		return nil, fmt.Errorf("decode %s event %d: %w", event.Kind, event.Seq, err)
	}
	return body, nil
}

// finishedTaskStatus reports whether status ends a background task: done, failed or stopped.
func finishedTaskStatus(status string) bool {
	switch status {
	case epicEndDone, "failed", "stopped":
		return true
	default:
		return false
	}
}

// sessionInterruptTurning returns the turning flag of an engine_interrupt payload: whether a turn ran when the owner's
// interrupt was recorded.
func sessionInterruptTurning(payload string) (bool, error) {
	body := &orreryv1.EngineInterrupt{}
	if err := protojson.Unmarshal([]byte(payload), body); err != nil {
		return false, fmt.Errorf("decode engine interrupt: %w", err)
	}
	return body.Turning, nil
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
