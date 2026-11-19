package stepmcp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/rpcretry"
)

// stepListArgs is the arguments of step_list. Epic, when it is not empty, narrows the listing to that epic's steps.
type stepListArgs struct {
	Epic string `json:"epic,omitempty"`
}

// stepListBytes is the most bytes of step_list's text.
const stepListBytes = 4000

// stepListValueRunes is the most runes of a step's agent, epic, task, end or status that its line carries. A longer
// value is cut to its first stepListValueRunes-1 runes and the ellipsis rune.
const stepListValueRunes = 40

// stepListHandler returns the handler of step_list, which lists the steps the call's caller session started and makes
// one call for them. The call is retried on Unavailable, as step_status is.
func stepListHandler(steps enginev1connect.StepsClient, clock rpcretry.Clock) ToolHandler {
	return func(ctx context.Context, call Call) (ToolResult, error) {
		var args stepListArgs
		if err := decodeArguments(call.Arguments, &args); err != nil {
			return ToolResult{}, err
		}
		var listed *connect.Response[v1.ListStepsResponse]
		err := rpcretry.Do(ctx, clock, rpcretry.Unavailable, func(callCtx context.Context) error {
			var callErr error
			listed, callErr = steps.ListSteps(callCtx, connect.NewRequest(&v1.ListStepsRequest{
				Epic:   args.Epic,
				Caller: true,
			}))
			return errors.Join(callErr)
		})
		if err != nil {
			return toolFailure(err), nil
		}
		text := renderStepList(listed.Msg.GetSteps(), args.Epic)
		return ToolResult{Content: []Content{{Type: textContentType, Text: text}}}, nil
	}
}

// renderStepList returns the text of step_list for steps, oldest first, which the request narrowed to epic when it is
// not empty. Each step is one line. When the lines do not all fit in stepListBytes, the earliest go: the text opens
// with a line counting them, then keeps as many of the newest lines as fit under that line.
func renderStepList(steps []*v1.Step, epic string) string {
	if len(steps) == 0 {
		if epic == "" {
			return "This session started no steps."
		}
		return "This session started no steps in epic " + epic + "."
	}
	lines := make([]string, 0, len(steps))
	for _, step := range steps {
		lines = append(lines, stepListLine(step))
	}
	if whole := strings.Join(lines, "\n"); len(whole) <= stepListBytes {
		return whole
	}
	kept, size := 0, 0
	for kept < len(lines) {
		grown := size + 1 + len(lines[len(lines)-1-kept])
		if len(stepListNotice(len(lines)-kept-1))+grown > stepListBytes {
			break
		}
		size = grown
		kept++
	}
	out := append([]string{stepListNotice(len(lines) - kept)}, lines[len(lines)-kept:]...)
	return strings.Join(out, "\n")
}

// stepListNotice returns the first line of a step_list text that drops dropped earlier steps.
func stepListNotice(dropped int) string {
	return fmt.Sprintf("[%d earlier steps not shown; pass epic to narrow]", dropped)
}

// stepListLine returns step as one line: its id, then its agent, epic, task, end and status. An empty end is running,
// and an empty epic, task or status is "-".
func stepListLine(step *v1.Step) string {
	end := step.GetEnd()
	if end == "" {
		end = "running"
	}
	return fmt.Sprintf("%s agent=%s epic=%s task=%s end=%s status=%s",
		step.GetId(),
		stepListValue(step.GetAgent()),
		stepListDashed(step.GetEpic()),
		stepListDashed(step.GetTask()),
		stepListValue(end),
		stepListDashed(step.GetStatus()),
	)
}

// stepListDashed returns value as stepListValue does, or "-" when value is empty.
func stepListDashed(value string) string {
	if value == "" {
		return "-"
	}
	return stepListValue(value)
}

// stepListValue returns value cut to stepListValueRunes runes, the last of them the ellipsis rune when it is cut.
func stepListValue(value string) string {
	runes := []rune(value)
	if len(runes) <= stepListValueRunes {
		return value
	}
	return string(runes[:stepListValueRunes-1]) + "…"
}
