// Package state applies Claude Code hook events to the per-pane @orrery_* tmux options.
package state

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// Transition is a pane's state before and after one hook event. From is "" when the pane had no
// state; To is "" after SessionEnd.
type Transition struct {
	From protocol.State
	To   protocol.State
}

// maxQuestionBytes bounds the stored question. tmux rejects a command over about 16 KB, so a
// larger value would make the set-option fail.
const maxQuestionBytes = 8192

// stopGraceWindow is how long after a done pane's Stop a tool event is still treated as a late frame
// from the finished turn. A tool event at or after it means Claude continued the turn.
const stopGraceWindow = 5 * time.Second

// Hook JSON keys and values the state machine reads.
const (
	keyEventName        = "hook_event_name"
	keyNotificationType = "notification_type"
	keyToolName         = "tool_name"
	keyToolInput        = "tool_input"

	eventUserPromptSubmit   = "UserPromptSubmit"
	eventPreToolUse         = "PreToolUse"
	eventPostToolUse        = "PostToolUse"
	eventPostToolUseFailure = "PostToolUseFailure"
	eventPermissionRequest  = "PermissionRequest"
	eventNotification       = "Notification"
	eventStop               = "Stop"
	eventSessionEnd         = "SessionEnd"
	toolAskUserQuestion     = "AskUserQuestion"
	notifyPermissionPrompt  = "permission_prompt"
	notifyElicitationDialog = "elicitation_dialog"
	notifyAgentNeedsInput   = "agent_needs_input"
)

// hookFields is a hook event decoded one level deep. Values stay raw until a field is read.
type hookFields map[string]json.RawMessage

// Apply applies one Claude Code hook event to the pane paneID and reports the transition. The bool
// is true exactly when the event is handled and the state changes. Unhandled events make no tmux
// call and return the zero Transition, false, nil. Malformed hook JSON is an error, and so is a
// malformed @orrery_changed when a tool event reaches a done pane.
func Apply(
	ctx context.Context,
	client *tmuxctl.Client,
	paneID string,
	hook json.RawMessage,
	now time.Time,
) (Transition, bool, error) {
	fields, err := decodeHook(hook)
	if err != nil {
		return Transition{}, false, err
	}
	event, err := fields.text(keyEventName)
	if err != nil {
		return Transition{}, false, err
	}
	notification, err := fields.text(keyNotificationType)
	if err != nil {
		return Transition{}, false, err
	}
	to, handled := targetState(event, notification)
	if !handled {
		return Transition{}, false, nil
	}

	from, err := currentState(ctx, client, paneID)
	if err != nil {
		return Transition{}, false, err
	}
	// A tool event that lands just after the turn's Stop is a late frame from that turn. It leaves
	// the pane and its options untouched. A later tool event means Claude continued the turn.
	if from == protocol.StateDone && isToolEvent(event) {
		late, lateErr := isLateToolEvent(ctx, client, paneID, now)
		if lateErr != nil {
			return Transition{}, false, lateErr
		}
		if late {
			return Transition{From: from, To: from}, false, nil
		}
	}
	transition := Transition{From: from, To: to}
	if event == eventSessionEnd {
		err = unsetSession(ctx, client, paneID)
	} else {
		err = writeTransition(ctx, client, paneID, fields, event, transition, now)
	}
	if err != nil {
		return Transition{}, false, err
	}
	return transition, from != to, nil
}

// targetState maps a hook event to the state it leaves the pane in. SessionEnd is handled with
// target "" (no state). ok is false for events the state machine ignores.
func targetState(event, notification string) (protocol.State, bool) {
	switch event {
	case eventUserPromptSubmit, eventPreToolUse, eventPostToolUse, eventPostToolUseFailure:
		return protocol.StateWorking, true
	case eventPermissionRequest:
		return protocol.StateWaiting, true
	case eventNotification:
		switch notification {
		case notifyPermissionPrompt, notifyElicitationDialog, notifyAgentNeedsInput:
			return protocol.StateWaiting, true
		}
	case eventStop:
		return protocol.StateDone, true
	case eventSessionEnd:
		return "", true
	}
	return "", false
}

// isLateToolEvent reports whether a tool event at now falls inside stopGraceWindow after the pane's
// Stop, read from @orrery_changed. An unset @orrery_changed is outside the window.
func isLateToolEvent(
	ctx context.Context,
	client *tmuxctl.Client,
	paneID string,
	now time.Time,
) (bool, error) {
	value, ok, err := client.PaneOption(ctx, paneID, protocol.OptionChanged)
	if err != nil {
		return false, fmt.Errorf("read %s: %w", protocol.OptionChanged, err)
	}
	if !ok {
		return false, nil
	}
	stopped, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return false, fmt.Errorf("parse %s: %w", protocol.OptionChanged, err)
	}
	return now.Sub(time.Unix(stopped, 0)) < stopGraceWindow, nil
}

// isToolEvent reports whether event is a tool-use event.
func isToolEvent(event string) bool {
	switch event {
	case eventPreToolUse, eventPostToolUse, eventPostToolUseFailure:
		return true
	}
	return false
}

// writeTransition writes the question, then the state and its change time. The state options are
// written only when the state changes; the question is written whenever its event applies.
func writeTransition(
	ctx context.Context,
	client *tmuxctl.Client,
	paneID string,
	fields hookFields,
	event string,
	transition Transition,
	now time.Time,
) error {
	if err := writeQuestion(ctx, client, paneID, fields, event, transition.To); err != nil {
		return err
	}
	if transition.From == transition.To {
		return nil
	}
	if err := client.SetPaneOption(ctx, paneID, protocol.OptionState, string(transition.To)); err != nil {
		return fmt.Errorf("set %s: %w", protocol.OptionState, err)
	}
	changed := strconv.FormatInt(now.Unix(), 10)
	if err := client.SetPaneOption(ctx, paneID, protocol.OptionChanged, changed); err != nil {
		return fmt.Errorf("set %s: %w", protocol.OptionChanged, err)
	}
	return nil
}

// writeQuestion captures the question on PermissionRequest and clears it on working or done.
// Notification-driven waiting leaves it unchanged.
func writeQuestion(
	ctx context.Context,
	client *tmuxctl.Client,
	paneID string,
	fields hookFields,
	event string,
	to protocol.State,
) error {
	if event == eventPermissionRequest {
		value, err := questionValue(fields)
		if err != nil {
			return err
		}
		err = client.SetPaneOption(ctx, paneID, protocol.OptionQuestion, value)
		if err != nil {
			return fmt.Errorf("set %s: %w", protocol.OptionQuestion, err)
		}
		return nil
	}
	if to == protocol.StateWorking || to == protocol.StateDone {
		if err := client.UnsetPaneOption(ctx, paneID, protocol.OptionQuestion); err != nil {
			return fmt.Errorf("unset %s: %w", protocol.OptionQuestion, err)
		}
	}
	return nil
}

// unsetSession removes all six per-pane options for SessionEnd.
func unsetSession(ctx context.Context, client *tmuxctl.Client, paneID string) error {
	for _, name := range []string{
		protocol.OptionState,
		protocol.OptionChanged,
		protocol.OptionSeen,
		protocol.OptionQuestion,
		protocol.OptionSummary,
		protocol.OptionLabel,
	} {
		if err := client.UnsetPaneOption(ctx, paneID, name); err != nil {
			return fmt.Errorf("unset %s: %w", name, err)
		}
	}
	return nil
}

// currentState reads the pane's state; an unset option reads as "".
func currentState(ctx context.Context, client *tmuxctl.Client, paneID string) (protocol.State, error) {
	value, _, err := client.PaneOption(ctx, paneID, protocol.OptionState)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", protocol.OptionState, err)
	}
	return protocol.State(value), nil
}

// questionValue is the compact single-line @orrery_question value for a PermissionRequest.
// AskUserQuestion stores its questions; any other tool stores its input. A value over
// maxQuestionBytes is replaced by the tool name and a truncated marker.
func questionValue(fields hookFields) (string, error) {
	tool, err := fields.text(keyToolName)
	if err != nil {
		return "", err
	}
	payload, err := questionPayload(tool, fields[keyToolInput])
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode question: %w", err)
	}
	if len(data) > maxQuestionBytes {
		data, err = json.Marshal(truncatedQuestion{Tool: tool, Truncated: true})
		if err != nil {
			return "", fmt.Errorf("encode truncated question: %w", err)
		}
	}
	return string(data), nil
}

// questionPayload returns the value to encode for tool with the given tool_input.
func questionPayload(tool string, toolInput json.RawMessage) (any, error) {
	if tool != toolAskUserQuestion {
		return toolCall{Tool: tool, Input: toolInput}, nil
	}
	var input struct {
		Questions json.RawMessage `json:"questions"`
	}
	if len(toolInput) > 0 {
		if err := json.Unmarshal(toolInput, &input); err != nil {
			return nil, fmt.Errorf("hook field %s: %w", keyToolInput, err)
		}
	}
	return askUserQuestion{Tool: tool, Questions: input.Questions}, nil
}

// toolCall is the stored question for a tool other than AskUserQuestion.
type toolCall struct {
	Tool  string          `json:"tool"`
	Input json.RawMessage `json:"input"`
}

// askUserQuestion is the stored question for AskUserQuestion.
type askUserQuestion struct {
	Tool      string          `json:"tool"`
	Questions json.RawMessage `json:"questions"`
}

// truncatedQuestion is the stored question when the full value exceeds maxQuestionBytes.
type truncatedQuestion struct {
	Tool      string `json:"tool"`
	Truncated bool   `json:"truncated"`
}

// decodeHook decodes a hook event one level deep. A null hook decodes to no fields.
func decodeHook(hook json.RawMessage) (hookFields, error) {
	var fields hookFields
	if err := json.Unmarshal(hook, &fields); err != nil {
		return nil, fmt.Errorf("decode hook: %w", err)
	}
	return fields, nil
}

// text returns the string value of key, or "" when key is absent or null.
func (f hookFields) text(key string) (string, error) {
	raw, ok := f[key]
	if !ok {
		return "", nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", fmt.Errorf("hook field %s: %w", key, err)
	}
	return value, nil
}
