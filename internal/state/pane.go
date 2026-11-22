package state

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/joshsymonds/orrery/internal/paneregistry"
	"github.com/joshsymonds/orrery/internal/panetranscript"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// Pane is what one session registry update says of a pane. Status and WaitingFor are the registry entry's status and
// waitingFor, and Turn is the turn its transcript shows. Unbound is set when the registry no longer binds the pane.
type Pane struct {
	Unbound    bool
	Status     string
	WaitingFor string
	Turn       panetranscript.Turn
}

// ApplyPane applies one session registry update to the pane paneID and reports the transition: the bool is true
// exactly when the state changes, a handled update reads the pane's state in one tmux invocation and writes its own
// options, then extra, then runs then, all in one more, and its stdout is what then printed. A dialog-open update and
// an idle or shell update to a pane that is neither working nor waiting make no transition and write none of the
// pane's own options, so they write only extra and run then.
func ApplyPane(
	ctx context.Context,
	client *tmuxctl.Client,
	paneID string,
	pane Pane,
	now time.Time,
	extra []tmuxctl.OptionWrite,
	then []string,
) (Transition, bool, string, error) {
	transition, own, err := planPane(ctx, client, paneID, pane, now)
	if err != nil {
		return Transition{}, false, "", err
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

// planPane returns the transition a registry update makes and the writes of the pane's own options that make it. An
// unbound pane is cleared by unboundWrites. A dialog-open update changes nothing and reads nothing. Any other update
// reads the pane's state, and an idle or shell update to a pane that is neither working nor waiting returns the pane's
// state as both ends of its transition and no writes.
func planPane(
	ctx context.Context, client *tmuxctl.Client, paneID string, pane Pane, now time.Time,
) (Transition, []tmuxctl.OptionWrite, error) {
	if pane.Unbound {
		current, err := readPane(ctx, client, paneID)
		if err != nil {
			return Transition{}, nil, err
		}
		return Transition{From: current.state}, unboundWrites(), nil
	}
	if pane.Status == paneregistry.StatusWaiting && pane.WaitingFor == paneregistry.WaitingDialog {
		return Transition{}, nil, nil
	}
	switch pane.Status {
	case paneregistry.StatusBusy, paneregistry.StatusWaiting, paneregistry.StatusIdle, paneregistry.StatusShell:
	default:
		return Transition{}, nil, fmt.Errorf("pane status %q is not busy, waiting, idle or shell", pane.Status)
	}
	current, err := readPane(ctx, client, paneID)
	if err != nil {
		return Transition{}, nil, err
	}
	from := current.state
	to, moves := registryTarget(pane.Status, from)
	if !moves {
		return Transition{From: from, To: from}, nil, nil
	}
	transition := Transition{From: from, To: to}
	writes, err := registryWrites(pane, transition, now)
	if err != nil {
		return Transition{}, nil, err
	}
	return transition, writes, nil
}

// registryTarget returns the state that a registry status leaves a pane in, given the pane's state from. moves is false
// when the status leaves the pane as it is: idle or shell ends only a working or waiting pane, so a pane with no state
// or a done pane is left alone.
func registryTarget(status string, from protocol.State) (to protocol.State, moves bool) {
	switch status {
	case paneregistry.StatusBusy:
		return protocol.StateWorking, true
	case paneregistry.StatusWaiting:
		return protocol.StateWaiting, true
	}
	if from == protocol.StateWorking || from == protocol.StateWaiting {
		return protocol.StateDone, true
	}
	return from, false
}

// registryWrites returns the writes of the tool, the question, the turn start, then the state and its change time. The
// tool is the turn's open call, unset when there is none and on done. The state options are written only when the
// state changes.
func registryWrites(pane Pane, transition Transition, now time.Time) ([]tmuxctl.OptionWrite, error) {
	tool, err := toolOption(pane.Turn.Tool, transition.To)
	if err != nil {
		return nil, err
	}
	question, err := questionOption(pane)
	if err != nil {
		return nil, err
	}
	writes := []tmuxctl.OptionWrite{tool, question}
	if pane.Turn.TurnStart != 0 {
		writes = append(writes, tmuxctl.Set(protocol.OptionTurnStart, strconv.FormatInt(pane.Turn.TurnStart, 10)))
	}
	if transition.From == transition.To {
		return writes, nil
	}
	return append(writes,
		tmuxctl.Set(protocol.OptionState, string(transition.To)),
		tmuxctl.Set(protocol.OptionChanged, strconv.FormatInt(now.Unix(), 10)),
	), nil
}

// toolOption returns the write of the @orrery_tool option for the turn's open call tool, which is unset when there is no
// call or the pane is done.
func toolOption(tool *panetranscript.Tool, to protocol.State) (tmuxctl.OptionWrite, error) {
	if tool == nil || to == protocol.StateDone {
		return tmuxctl.Unset(protocol.OptionTool), nil
	}
	data, err := json.Marshal(currentTool{ID: tool.ID, Name: tool.Name, Input: tool.Input, Start: tool.Start})
	if err != nil {
		return tmuxctl.OptionWrite{}, fmt.Errorf("encode %s: %w", protocol.OptionTool, err)
	}
	return tmuxctl.Set(protocol.OptionTool, string(data)), nil
}

// questionOption returns the write of the @orrery_question option: the question of questionCall, or unset when there is
// none.
func questionOption(pane Pane) (tmuxctl.OptionWrite, error) {
	call, ok := questionCall(pane)
	if !ok {
		return tmuxctl.Unset(protocol.OptionQuestion), nil
	}
	value, err := encodeQuestion(call.Name, call.Input)
	if err != nil {
		return tmuxctl.OptionWrite{}, err
	}
	return tmuxctl.Set(protocol.OptionQuestion, value), nil
}

// questionCall returns the pending call that a waiting pane's question holds: the oldest pending call for a permission
// prompt, and the oldest pending AskUserQuestion call for input needed. It returns false for a pane that is not
// waiting, and when no such call is pending.
func questionCall(pane Pane) (panetranscript.Call, bool) {
	if pane.Status != paneregistry.StatusWaiting {
		return panetranscript.Call{}, false
	}
	switch pane.WaitingFor {
	case paneregistry.WaitingPermission:
		if len(pane.Turn.Pending) > 0 {
			return pane.Turn.Pending[0], true
		}
	case paneregistry.WaitingInput:
		for _, call := range pane.Turn.Pending {
			if call.Name == toolAskUserQuestion {
				return call, true
			}
		}
	}
	return panetranscript.Call{}, false
}

// unboundWrites clears the pane's options as sessionEndWrites does, and then unsets its session options.
func unboundWrites() []tmuxctl.OptionWrite {
	return append(sessionEndWrites(),
		tmuxctl.Unset(protocol.OptionClaudeSession), tmuxctl.Unset(protocol.OptionTranscript))
}
