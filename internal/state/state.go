// Package state applies Claude Code hook events to the per-pane @orrery_* tmux options.
package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
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

// optionTurnStart holds the unix seconds of the newest UserPromptSubmit. The state options move only when the
// state changes, so a prompt sent to a pane that is already working leaves @orrery_changed at the earlier
// turn's start. This option moves on every prompt.
const optionTurnStart = "@orrery_turn_start"

// Hook JSON keys and values the state machine reads.
const (
	keyEventName        = "hook_event_name"
	keyNotificationType = "notification_type"
	keyToolName         = "tool_name"
	keyToolInput        = "tool_input"
	keyToolUseID        = "tool_use_id"
	keyReason           = "reason"
	keySessionID        = "session_id"
	keyTranscriptPath   = "transcript_path"

	reasonClear = "clear"

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

// EndInterruptedTurn ends a working pane's turn when the transcript shows an interruption after the turn
// started. interrupted is the unix seconds of the newest interruption line, 0 when there is none. The turn
// started at the later of @orrery_changed and the newest prompt's @orrery_turn_start. When the pane is working
// and interrupted is later than that, the pane becomes done as a Stop leaves it, and the call returns true.
// Otherwise nothing is written and the call returns false.
func EndInterruptedTurn(
	ctx context.Context,
	client *tmuxctl.Client,
	paneID string,
	interrupted int64,
	now time.Time,
) (bool, error) {
	from, err := currentState(ctx, client, paneID)
	if err != nil {
		return false, err
	}
	if from != protocol.StateWorking {
		return false, nil
	}
	changed, _, err := optionSecond(ctx, client, paneID, protocol.OptionChanged)
	if err != nil {
		return false, err
	}
	turnStart, _, err := optionSecond(ctx, client, paneID, optionTurnStart)
	if err != nil {
		return false, err
	}
	started := max(changed, turnStart)
	if interrupted <= started {
		return false, nil
	}
	// The Stop path reads no hook fields, so no hook is passed.
	transition := Transition{From: from, To: protocol.StateDone}
	err = writeTransition(ctx, client, paneID, nil, eventStop, transition, now)
	if err != nil {
		return false, err
	}
	return true, nil
}

// RecordSession records the Claude Code session id and transcript path that one hook event carries,
// so a later close can archive them. A SessionEnd with reason clear unsets both options. Any other
// event sets each option whose hook value is non-empty; an absent or empty value leaves it unchanged.
// The whole hook is decoded before any option is written, so a malformed or mistyped field writes
// nothing.
func RecordSession(ctx context.Context, client *tmuxctl.Client, paneID string, hook json.RawMessage) error {
	fields, err := decodeHook(hook)
	if err != nil {
		return err
	}
	event, err := fields.text(keyEventName)
	if err != nil {
		return err
	}
	reason, err := fields.text(keyReason)
	if err != nil {
		return err
	}
	sessionID, err := fields.text(keySessionID)
	if err != nil {
		return err
	}
	transcript, err := fields.text(keyTranscriptPath)
	if err != nil {
		return err
	}

	if event == eventSessionEnd && reason == reasonClear {
		for _, name := range []string{protocol.OptionClaudeSession, protocol.OptionTranscript} {
			if err = client.UnsetPaneOption(ctx, paneID, name); err != nil {
				return fmt.Errorf("unset %s: %w", name, err)
			}
		}
		return nil
	}
	return setSessionValues(ctx, client, paneID, sessionID, transcript)
}

// setSessionValues sets @orrery_claude_session and @orrery_transcript from the hook's session id and
// transcript path. Newlines become spaces, because the stored values are single-line. An empty value
// leaves its option unchanged.
func setSessionValues(ctx context.Context, client *tmuxctl.Client, paneID, sessionID, transcript string) error {
	values := []struct{ name, value string }{
		{protocol.OptionClaudeSession, sessionID},
		{protocol.OptionTranscript, transcript},
	}
	for _, option := range values {
		if option.value == "" {
			continue
		}
		value := strings.ReplaceAll(option.value, "\n", " ")
		if err := client.SetPaneOption(ctx, paneID, option.name, value); err != nil {
			return fmt.Errorf("set %s: %w", option.name, err)
		}
	}
	return nil
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
	stopped, ok, err := optionSecond(ctx, client, paneID, protocol.OptionChanged)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}
	return now.Sub(time.Unix(stopped, 0)) < stopGraceWindow, nil
}

// optionSecond reads the pane option name as unix seconds. ok is false when the option is unset, and a value
// that is not an integer is an error.
func optionSecond(ctx context.Context, client *tmuxctl.Client, paneID, name string) (int64, bool, error) {
	value, ok, err := client.PaneOption(ctx, paneID, name)
	if err != nil {
		return 0, false, fmt.Errorf("read %s: %w", name, err)
	}
	if !ok {
		return 0, false, nil
	}
	seconds, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, false, fmt.Errorf("parse %s: %w", name, err)
	}
	return seconds, true, nil
}

// isToolEvent reports whether event is a tool-use event.
func isToolEvent(event string) bool {
	switch event {
	case eventPreToolUse, eventPostToolUse, eventPostToolUseFailure:
		return true
	}
	return false
}

// writeTransition writes the tool, the question, the turn start, then the state and its change time. The
// state options are written only when the state changes; the tool, the question and the turn start are
// written whenever their event applies. The tool is written first, so a malformed tool field fails before
// any option changes.
func writeTransition(
	ctx context.Context,
	client *tmuxctl.Client,
	paneID string,
	fields hookFields,
	event string,
	transition Transition,
	now time.Time,
) error {
	if err := writeTool(ctx, client, paneID, fields, event, now); err != nil {
		return err
	}
	if err := writeQuestion(ctx, client, paneID, fields, event, transition.To); err != nil {
		return err
	}
	if event == eventUserPromptSubmit {
		if err := client.SetPaneOption(ctx, paneID, optionTurnStart, strconv.FormatInt(now.Unix(), 10)); err != nil {
			return fmt.Errorf("set %s: %w", optionTurnStart, err)
		}
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

// writeTool records the tool a PreToolUse starts, clears it when its PostToolUse or
// PostToolUseFailure ends it, and clears it on Stop. Other events leave it unchanged.
func writeTool(
	ctx context.Context,
	client *tmuxctl.Client,
	paneID string,
	fields hookFields,
	event string,
	now time.Time,
) error {
	switch event {
	case eventPreToolUse:
		return setTool(ctx, client, paneID, fields, now)
	case eventPostToolUse, eventPostToolUseFailure:
		return clearTool(ctx, client, paneID, fields)
	case eventStop:
		return unsetTool(ctx, client, paneID)
	}
	return nil
}

// setTool stores the tool a PreToolUse starts as a single-line @orrery_tool value. A PreToolUse without
// tool_use_id leaves the option unchanged.
func setTool(ctx context.Context, client *tmuxctl.Client, paneID string, fields hookFields, now time.Time) error {
	id, err := fields.text(keyToolUseID)
	if err != nil {
		return err
	}
	if id == "" {
		return nil
	}
	name, err := fields.text(keyToolName)
	if err != nil {
		return err
	}
	data, err := json.Marshal(currentTool{
		ID:    id,
		Name:  name,
		Input: protocol.ToolInputText(fields[keyToolInput]),
		Start: now.Unix(),
	})
	if err != nil {
		return fmt.Errorf("encode %s: %w", protocol.OptionTool, err)
	}
	err = client.SetPaneOption(ctx, paneID, protocol.OptionTool, string(data))
	if err != nil {
		return fmt.Errorf("set %s: %w", protocol.OptionTool, err)
	}
	return nil
}

// clearTool unsets the tool when its id equals the event's tool_use_id. An event without an id, and a
// stored value whose id does not match or does not decode, leave the option unchanged.
func clearTool(ctx context.Context, client *tmuxctl.Client, paneID string, fields hookFields) error {
	id, err := fields.text(keyToolUseID)
	if err != nil {
		return err
	}
	if id == "" {
		return nil
	}
	value, ok, err := client.PaneOption(ctx, paneID, protocol.OptionTool)
	if err != nil {
		return fmt.Errorf("read %s: %w", protocol.OptionTool, err)
	}
	if !ok || storedToolID(value) != id {
		return nil
	}
	return unsetTool(ctx, client, paneID)
}

// storedToolID returns the id in a stored @orrery_tool value, or "" when the value does not decode.
func storedToolID(value string) string {
	var stored currentTool
	if err := json.Unmarshal([]byte(value), &stored); err != nil {
		return ""
	}
	return stored.ID
}

// unsetTool removes @orrery_tool.
func unsetTool(ctx context.Context, client *tmuxctl.Client, paneID string) error {
	if err := client.UnsetPaneOption(ctx, paneID, protocol.OptionTool); err != nil {
		return fmt.Errorf("unset %s: %w", protocol.OptionTool, err)
	}
	return nil
}

// unsetSession removes all eight per-pane options for SessionEnd.
func unsetSession(ctx context.Context, client *tmuxctl.Client, paneID string) error {
	for _, name := range []string{
		protocol.OptionState,
		protocol.OptionChanged,
		protocol.OptionSeen,
		protocol.OptionQuestion,
		protocol.OptionSummary,
		protocol.OptionLabel,
		protocol.OptionTool,
		optionTurnStart,
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
// maxQuestionBytes is replaced by a truncated marker (see truncatedValue).
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
	if len(data) <= maxQuestionBytes {
		return string(data), nil
	}
	return truncatedValue(tool, data)
}

// truncatedValue is the stored question for the encoded question full, which exceeds
// maxQuestionBytes. It holds the tool name and the lowercase hex SHA-256 of full, so distinct
// oversized questions store distinct values.
func truncatedValue(tool string, full []byte) (string, error) {
	sum := sha256.Sum256(full)
	data, err := json.Marshal(truncatedQuestion{Tool: tool, Truncated: true, SHA256: hex.EncodeToString(sum[:])})
	if err != nil {
		return "", fmt.Errorf("encode truncated question: %w", err)
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

// currentTool is the @orrery_tool value: the tool a PreToolUse started and no end event has cleared.
// Input is protocol.ToolInputText of the tool's input, and Start is the Unix time of the PreToolUse.
type currentTool struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Input string `json:"input"`
	Start int64  `json:"start"`
}

// truncatedQuestion is the stored question when the full value exceeds maxQuestionBytes. SHA256
// is the lowercase hex SHA-256 of the full encoded question.
type truncatedQuestion struct {
	Tool      string `json:"tool"`
	Truncated bool   `json:"truncated"`
	SHA256    string `json:"sha256"`
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
