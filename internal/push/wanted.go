package push

import (
	"encoding/json"
	"fmt"

	"github.com/joshsymonds/orrery/internal/protocol"
)

// Wanted reports whether a transition into to sends a Web Push. Waiting always does. Done does unless the hook's
// background_tasks holds a subagent or workflow task, which the turn ended with still running. Other states never do.
// A hook or background_tasks that does not decode yields true with an error, so the caller notifies and logs it.
func Wanted(to protocol.State, hook json.RawMessage) (bool, error) {
	switch to {
	case protocol.StateWaiting:
		return true, nil
	case protocol.StateDone:
		return doneWanted(hook)
	default:
		return false, nil
	}
}

// backgroundTask is the part of one background_tasks element that Wanted reads.
type backgroundTask struct {
	Type string `json:"type"`
}

// doneWanted decides a done transition from the Stop hook's background tasks.
func doneWanted(hook json.RawMessage) (bool, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(hook, &fields); err != nil {
		return true, fmt.Errorf("decode hook: %w", err)
	}
	raw := fields["background_tasks"]
	if len(raw) == 0 || string(raw) == "null" {
		return true, nil
	}
	var tasks []backgroundTask
	if err := json.Unmarshal(raw, &tasks); err != nil {
		return true, fmt.Errorf("decode background_tasks: %w", err)
	}
	for _, task := range tasks {
		if task.Type == "subagent" || task.Type == "workflow" {
			return false, nil
		}
	}
	return true, nil
}
