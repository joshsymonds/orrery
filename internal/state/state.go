// Package state applies Claude Code hook events to the per-pane @orrery_* tmux options.
package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
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

// stopGraceWindow is how long after a done pane's Stop or StopFailure a tool event is still treated as a late frame
// from the finished turn. A tool event at or after it means Claude continued the turn.
const stopGraceWindow = 5 * time.Second

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
	eventStopFailure        = "StopFailure"
	eventSessionEnd         = "SessionEnd"
	toolAskUserQuestion     = "AskUserQuestion"
	notifyPermissionPrompt  = "permission_prompt"
	notifyElicitationDialog = "elicitation_dialog"
	notifyAgentNeedsInput   = "agent_needs_input"
)

// hookFields is a hook event decoded one level deep. Values stay raw until a field is read.
type hookFields map[string]json.RawMessage

// Apply applies one Claude Code hook event to the pane paneID and reports the transition, as ApplyWith does with no
// extra writes and no command after them.
func Apply(
	ctx context.Context,
	client *tmuxctl.Client,
	paneID string,
	hook json.RawMessage,
	now time.Time,
) (Transition, bool, error) {
	transition, changed, _, err := ApplyWith(ctx, client, paneID, hook, now, nil, nil)
	return transition, changed, err
}

// ApplyWith applies one Claude Code hook event to the pane paneID and reports the transition. The bool is true exactly
// when the event is handled and the state changes. A handled event reads the pane's options in one tmux invocation,
// then writes its own options, then extra, then runs then, all in one more; its stdout is what then printed. An
// unhandled event reads nothing, writes only extra and runs then, and returns the zero Transition and false; with no
// extra and no then it makes no tmux call. Malformed hook JSON is an error and writes nothing, and so is a malformed
// @orrery_changed when a tool event reaches a done pane.
func ApplyWith(
	ctx context.Context,
	client *tmuxctl.Client,
	paneID string,
	hook json.RawMessage,
	now time.Time,
	extra []tmuxctl.OptionWrite,
	then []string,
) (Transition, bool, string, error) {
	fields, err := decodeHook(hook)
	if err != nil {
		return Transition{}, false, "", err
	}
	event, err := fields.text(keyEventName)
	if err != nil {
		return Transition{}, false, "", err
	}
	notification, err := fields.text(keyNotificationType)
	if err != nil {
		return Transition{}, false, "", err
	}
	var transition Transition
	var own []tmuxctl.OptionWrite
	if to, handled := targetState(event, notification); handled {
		pane, readErr := readPane(ctx, client, paneID)
		if readErr != nil {
			return Transition{}, false, "", readErr
		}
		if transition, own, err = planEvent(fields, event, pane, to, now); err != nil {
			return Transition{}, false, "", err
		}
	}
	cmds := tmuxctl.WriteCommands(paneID, slices.Concat(own, extra))
	if then != nil {
		cmds = append(cmds, then)
	}
	out, err := client.Batch(ctx, cmds...)
	if err != nil {
		return Transition{}, false, "", fmt.Errorf("write pane %s: %w", paneID, err)
	}
	return transition, transition.From != transition.To, out, nil
}

// planEvent returns the transition a handled event makes from the pane's options and the writes that make it. A tool
// event that lands just after the turn's Stop is a late frame from that turn: it leaves the pane and its options
// untouched, and its transition is from the pane's state to the same. A later tool event means Claude continued the
// turn.
func planEvent(
	fields hookFields, event string, pane paneValues, to protocol.State, now time.Time,
) (Transition, []tmuxctl.OptionWrite, error) {
	from := pane.state
	if from == protocol.StateDone && isToolEvent(event) {
		late, err := isLateToolEvent(pane.changed, now)
		if err != nil {
			return Transition{}, nil, err
		}
		if late {
			return Transition{From: from, To: from}, nil, nil
		}
	}
	transition := Transition{From: from, To: to}
	if event == eventSessionEnd {
		return transition, sessionEndWrites(), nil
	}
	writes, err := transitionWrites(fields, event, transition, pane.tool, now)
	if err != nil {
		return Transition{}, nil, err
	}
	return transition, writes, nil
}

// EndInterruptedTurn ends a working pane's turn when the transcript shows an interruption after the turn
// started. interrupted is the unix seconds of the newest interruption line, 0 when there is none. The turn
// started at the later of @orrery_changed and the newest prompt's @orrery_turn_start. When the pane is working
// and interrupted is later than that, the pane becomes done as a Stop leaves it, and the call returns true.
// Otherwise nothing is written and the call returns false. It reads the pane in one tmux invocation and writes it in
// one more.
func EndInterruptedTurn(
	ctx context.Context,
	client *tmuxctl.Client,
	paneID string,
	interrupted int64,
	now time.Time,
) (bool, error) {
	pane, err := readPane(ctx, client, paneID)
	if err != nil {
		return false, err
	}
	if pane.state != protocol.StateWorking {
		return false, nil
	}
	changed, _, err := optionSecond(protocol.OptionChanged, pane.changed)
	if err != nil {
		return false, err
	}
	turnStart, _, err := optionSecond(protocol.OptionTurnStart, pane.turnStart)
	if err != nil {
		return false, err
	}
	started := max(changed, turnStart)
	if interrupted <= started {
		return false, nil
	}
	// The Stop path reads no hook fields, so no hook is passed.
	transition := Transition{From: pane.state, To: protocol.StateDone}
	writes, err := transitionWrites(nil, eventStop, transition, pane.tool, now)
	if err != nil {
		return false, err
	}
	if err = client.WritePaneOptions(ctx, paneID, writes); err != nil {
		return false, fmt.Errorf("write pane %s: %w", paneID, err)
	}
	return true, nil
}

// RecordSession records the Claude Code session id and transcript path that one hook event carries, in one tmux
// invocation, as SessionWrites decides them. A hook that makes no write makes no tmux call.
func RecordSession(ctx context.Context, client *tmuxctl.Client, paneID string, hook json.RawMessage) error {
	writes, err := SessionWrites(hook)
	if err != nil {
		return err
	}
	if err = client.WritePaneOptions(ctx, paneID, writes); err != nil {
		return fmt.Errorf("write pane %s session: %w", paneID, err)
	}
	return nil
}

// SessionWrites returns the writes that record the Claude Code session id and transcript path one hook event carries,
// so a later close can archive them. A SessionEnd with reason clear unsets both options. Any other event sets each
// option whose hook value is non-empty, with its newlines made spaces, since the stored values are single-line; an
// absent or empty value leaves it unchanged. The whole hook is decoded first, so a malformed or mistyped field returns
// an error and no writes.
func SessionWrites(hook json.RawMessage) ([]tmuxctl.OptionWrite, error) {
	fields, err := decodeHook(hook)
	if err != nil {
		return nil, err
	}
	event, err := fields.text(keyEventName)
	if err != nil {
		return nil, err
	}
	reason, err := fields.text(keyReason)
	if err != nil {
		return nil, err
	}
	sessionID, err := fields.text(keySessionID)
	if err != nil {
		return nil, err
	}
	transcript, err := fields.text(keyTranscriptPath)
	if err != nil {
		return nil, err
	}
	if event == eventSessionEnd && reason == reasonClear {
		return []tmuxctl.OptionWrite{
			tmuxctl.Unset(protocol.OptionClaudeSession), tmuxctl.Unset(protocol.OptionTranscript),
		}, nil
	}
	var writes []tmuxctl.OptionWrite
	for _, option := range []struct{ name, value string }{
		{protocol.OptionClaudeSession, sessionID},
		{protocol.OptionTranscript, transcript},
	} {
		if option.value != "" {
			writes = append(writes, tmuxctl.Set(option.name, strings.ReplaceAll(option.value, "\n", " ")))
		}
	}
	return writes, nil
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
	case eventStop, eventStopFailure:
		return protocol.StateDone, true
	case eventSessionEnd:
		return "", true
	}
	return "", false
}

// paneValues is what a transition reads of a pane: its state, and the raw @orrery_changed, @orrery_turn_start and
// @orrery_tool. An unset option reads as "".
type paneValues struct {
	state     protocol.State
	changed   string
	turnStart string
	tool      string
}

// readPane reads the options of paneValues in one tmux invocation.
func readPane(ctx context.Context, client *tmuxctl.Client, paneID string) (paneValues, error) {
	values, err := client.PaneValues(ctx, paneID,
		protocol.OptionState, protocol.OptionChanged, protocol.OptionTurnStart, protocol.OptionTool)
	if err != nil {
		return paneValues{}, fmt.Errorf("read pane %s: %w", paneID, err)
	}
	return paneValues{state: protocol.State(values[0]), changed: values[1], turnStart: values[2], tool: values[3]}, nil
}

// isLateToolEvent reports whether a tool event at now falls inside stopGraceWindow after the pane's
// Stop or StopFailure, read from @orrery_changed, the raw value changed. An unset @orrery_changed is outside the
// window.
func isLateToolEvent(changed string, now time.Time) (bool, error) {
	stopped, ok, err := optionSecond(protocol.OptionChanged, changed)
	if err != nil || !ok {
		return false, err
	}
	return now.Sub(time.Unix(stopped, 0)) < stopGraceWindow, nil
}

// optionSecond reads value, the option name's raw value, as unix seconds. ok is false when the option is unset, and
// a value that is not an integer is an error.
func optionSecond(name, value string) (int64, bool, error) {
	if value == "" {
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

// transitionWrites returns the writes of the tool, the question, the turn start, then the state and its change time.
// The state options are written only when the state changes; the tool, the question and the turn start are written
// whenever their event applies. storedTool is the pane's raw @orrery_tool. A malformed field returns an error and no
// writes.
func transitionWrites(
	fields hookFields, event string, transition Transition, storedTool string, now time.Time,
) ([]tmuxctl.OptionWrite, error) {
	writes, err := toolWrites(fields, event, storedTool, now)
	if err != nil {
		return nil, err
	}
	question, err := questionWrites(fields, event, transition.To)
	if err != nil {
		return nil, err
	}
	writes = append(writes, question...)
	if event == eventUserPromptSubmit {
		writes = append(writes, tmuxctl.Set(protocol.OptionTurnStart, strconv.FormatInt(now.Unix(), 10)))
	}
	if transition.From == transition.To {
		return writes, nil
	}
	return append(writes,
		tmuxctl.Set(protocol.OptionState, string(transition.To)),
		tmuxctl.Set(protocol.OptionChanged, strconv.FormatInt(now.Unix(), 10)),
	), nil
}

// questionWrites captures the question on PermissionRequest and clears it on working or done.
// Notification-driven waiting leaves it unchanged.
func questionWrites(fields hookFields, event string, to protocol.State) ([]tmuxctl.OptionWrite, error) {
	if event == eventPermissionRequest {
		value, err := questionValue(fields)
		if err != nil {
			return nil, err
		}
		return []tmuxctl.OptionWrite{tmuxctl.Set(protocol.OptionQuestion, value)}, nil
	}
	if to == protocol.StateWorking || to == protocol.StateDone {
		return []tmuxctl.OptionWrite{tmuxctl.Unset(protocol.OptionQuestion)}, nil
	}
	return nil, nil
}

// toolWrites records the tool a PreToolUse starts, clears it when its PostToolUse or PostToolUseFailure ends it, and
// clears it on Stop or StopFailure. Other events leave it unchanged. storedTool is the pane's raw @orrery_tool.
func toolWrites(fields hookFields, event, storedTool string, now time.Time) ([]tmuxctl.OptionWrite, error) {
	switch event {
	case eventPreToolUse:
		return setTool(fields, now)
	case eventPostToolUse, eventPostToolUseFailure:
		return clearTool(fields, storedTool)
	case eventStop, eventStopFailure:
		return []tmuxctl.OptionWrite{tmuxctl.Unset(protocol.OptionTool)}, nil
	}
	return nil, nil
}

// setTool stores the tool a PreToolUse starts as a single-line @orrery_tool value. A PreToolUse without
// tool_use_id leaves the option unchanged.
func setTool(fields hookFields, now time.Time) ([]tmuxctl.OptionWrite, error) {
	id, err := fields.text(keyToolUseID)
	if err != nil {
		return nil, err
	}
	if id == "" {
		return nil, nil
	}
	name, err := fields.text(keyToolName)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(currentTool{
		ID:    id,
		Name:  name,
		Input: protocol.ToolInputText(fields[keyToolInput]),
		Start: now.Unix(),
	})
	if err != nil {
		return nil, fmt.Errorf("encode %s: %w", protocol.OptionTool, err)
	}
	return []tmuxctl.OptionWrite{tmuxctl.Set(protocol.OptionTool, string(data))}, nil
}

// clearTool unsets the tool when the id in storedTool, the pane's raw @orrery_tool, equals the event's tool_use_id. An
// event without an id, and a stored value that is unset, whose id does not match or that does not decode, leave the
// option unchanged.
func clearTool(fields hookFields, storedTool string) ([]tmuxctl.OptionWrite, error) {
	id, err := fields.text(keyToolUseID)
	if err != nil {
		return nil, err
	}
	if id == "" || storedTool == "" || storedToolID(storedTool) != id {
		return nil, nil
	}
	return []tmuxctl.OptionWrite{tmuxctl.Unset(protocol.OptionTool)}, nil
}

// storedToolID returns the id in a stored @orrery_tool value, or "" when the value does not decode.
func storedToolID(value string) string {
	var stored currentTool
	if err := json.Unmarshal([]byte(value), &stored); err != nil {
		return ""
	}
	return stored.ID
}

// sessionEndWrites removes all eight per-pane options for SessionEnd.
func sessionEndWrites() []tmuxctl.OptionWrite {
	names := []string{
		protocol.OptionState,
		protocol.OptionChanged,
		protocol.OptionSeen,
		protocol.OptionQuestion,
		protocol.OptionSummary,
		protocol.OptionLabel,
		protocol.OptionTool,
		protocol.OptionTurnStart,
	}
	writes := make([]tmuxctl.OptionWrite, 0, len(names))
	for _, name := range names {
		writes = append(writes, tmuxctl.Unset(name))
	}
	return writes
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
