// Package state applies session registry updates to the per-pane @orrery_* tmux options.
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

// Transition is a pane's state before and after one update. From is "" when the pane had no
// state; To is "" after an unbound update.
type Transition struct {
	From protocol.State
	To   protocol.State
}

// maxQuestionBytes bounds the stored question. tmux rejects a command over about 16 KB, so a
// larger value would make the set-option fail.
const maxQuestionBytes = 8192

// toolAskUserQuestion is the name of the AskUserQuestion tool, whose calls carry the questions a pane asks.
const toolAskUserQuestion = "AskUserQuestion"

// EndInterruptedTurn ends a working or waiting pane's turn when the transcript shows an interruption after the turn
// started. interrupted is the unix seconds of the newest interruption line, 0 when there is none. The turn
// started at the later of @orrery_changed and the newest prompt's @orrery_turn_start. When the pane is working or
// waiting and interrupted is later than that, the pane becomes done as a Stop leaves it, with its question unset, and
// the call returns true. Otherwise nothing is written and the call returns false. Done panes are left alone. It reads
// the pane in one tmux invocation and writes it in one more.
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
	if pane.state != protocol.StateWorking && pane.state != protocol.StateWaiting {
		return false, nil
	}
	changed, err := optionSecond(protocol.OptionChanged, pane.changed)
	if err != nil {
		return false, err
	}
	turnStart, err := optionSecond(protocol.OptionTurnStart, pane.turnStart)
	if err != nil {
		return false, err
	}
	started := max(changed, turnStart)
	if interrupted <= started {
		return false, nil
	}
	writes := []tmuxctl.OptionWrite{
		tmuxctl.Unset(protocol.OptionTool),
		tmuxctl.Unset(protocol.OptionQuestion),
		tmuxctl.Set(protocol.OptionState, string(protocol.StateDone)),
		tmuxctl.Set(protocol.OptionChanged, strconv.FormatInt(now.Unix(), 10)),
	}
	if err = client.WritePaneOptions(ctx, paneID, writes); err != nil {
		return false, fmt.Errorf("write pane %s: %w", paneID, err)
	}
	return true, nil
}

// SessionValueWrites returns the writes that record a session id and a transcript path: each option whose value is
// non-empty is set to it, with its newlines made spaces, since the stored values are single-line. An empty value leaves
// its option unchanged.
func SessionValueWrites(sessionID, transcript string) []tmuxctl.OptionWrite {
	var writes []tmuxctl.OptionWrite
	for _, option := range []struct{ name, value string }{
		{protocol.OptionClaudeSession, sessionID},
		{protocol.OptionTranscript, transcript},
	} {
		if option.value != "" {
			writes = append(writes, tmuxctl.Set(option.name, strings.ReplaceAll(option.value, "\n", " ")))
		}
	}
	return writes
}

// paneValues is what a transition reads of a pane: its state, and the raw @orrery_changed and @orrery_turn_start. An
// unset option reads as "".
type paneValues struct {
	state     protocol.State
	changed   string
	turnStart string
}

// readPane reads the options of paneValues in one tmux invocation.
func readPane(ctx context.Context, client *tmuxctl.Client, paneID string) (paneValues, error) {
	values, err := client.PaneValues(ctx, paneID,
		protocol.OptionState, protocol.OptionChanged, protocol.OptionTurnStart)
	if err != nil {
		return paneValues{}, fmt.Errorf("read pane %s: %w", paneID, err)
	}
	return paneValues{state: protocol.State(values[0]), changed: values[1], turnStart: values[2]}, nil
}

// optionSecond reads value, the option name's raw value, as unix seconds. An unset value reads as 0, and a value that
// is not an integer is an error.
func optionSecond(name, value string) (int64, error) {
	if value == "" {
		return 0, nil
	}
	seconds, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", name, err)
	}
	return seconds, nil
}

// sessionEndWrites removes all nine per-pane options of an unbound pane.
func sessionEndWrites() []tmuxctl.OptionWrite {
	names := []string{
		protocol.OptionCommand,
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

// encodeQuestion is the compact single-line @orrery_question value for a call of tool whose tool_input is toolInput.
// AskUserQuestion stores its questions; any other tool stores its input. A value over maxQuestionBytes is replaced by
// a truncated marker (see truncatedValue).
func encodeQuestion(tool string, toolInput json.RawMessage) (string, error) {
	payload, err := questionPayload(tool, toolInput)
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
			return nil, fmt.Errorf("decode tool input: %w", err)
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

// currentTool is the @orrery_tool value: the call that a pane's turn has open. Input is protocol.ToolInputText of the
// call's input, and Start is the Unix time the call started.
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
