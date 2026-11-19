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

// stepCheckArgs is the arguments of step_check. Id and Check name the step and its check. Round names the round to read
// the check in, zero for its last round. Lines and Before bound the slice of its output, and Match keeps only the lines
// that contain it.
type stepCheckArgs struct {
	ID     string `json:"id"`
	Check  string `json:"check"`
	Round  uint32 `json:"round,omitempty"`
	Lines  uint32 `json:"lines,omitempty"`
	Before uint32 `json:"before,omitempty"`
	Match  string `json:"match,omitempty"`
}

// stepCheckHandler returns the handler of step_check, which reads one check of a step and makes one call for it. The
// call is retried on Unavailable, as step_result is.
func stepCheckHandler(steps enginev1connect.StepsClient, clock rpcretry.Clock) ToolHandler {
	return func(ctx context.Context, call Call) (ToolResult, error) {
		var args stepCheckArgs
		if err := decodeArguments(call.Arguments, &args); err != nil {
			return ToolResult{}, err
		}
		if err := requireString("id", args.ID); err != nil {
			return ToolResult{}, err
		}
		if err := requireString("check", args.Check); err != nil {
			return ToolResult{}, err
		}
		var read *connect.Response[v1.ReadStepCheckResponse]
		err := rpcretry.Do(ctx, clock, rpcretry.Unavailable, func(callCtx context.Context) error {
			var callErr error
			read, callErr = steps.ReadStepCheck(callCtx, connect.NewRequest(&v1.ReadStepCheckRequest{
				Id:     args.ID,
				Check:  args.Check,
				Round:  args.Round,
				Lines:  args.Lines,
				Before: args.Before,
				Match:  args.Match,
			}))
			return errors.Join(callErr)
		})
		if err != nil {
			return toolFailure(err), nil
		}
		text := renderStepCheck(read.Msg, args.Match)
		return ToolResult{Content: []Content{{Type: textContentType, Text: text}}}, nil
	}
}

// renderStepCheck returns the text of reply, the read of a request that matched match. The first line names the check's
// round and whether it passed. Its detail follows when it is not empty, then the line that says which output the slice is
// taken from. The slice comes next, and a line naming the before to continue from ends it when more output remains. A
// slice with no lines reads as a line saying so, naming match when the request gave one.
func renderStepCheck(reply *v1.ReadStepCheckResponse, match string) string {
	verdict := "failed"
	if reply.GetOk() {
		verdict = "passed"
	}
	lines := []string{fmt.Sprintf("%s round %d: %s", reply.GetName(), reply.GetRound(), verdict)}
	if detail := reply.GetDetail(); detail != "" {
		lines = append(lines, detail)
	}
	if reply.GetFirstLine() == 0 {
		empty := "[no output lines]"
		if match != "" {
			empty = fmt.Sprintf(`[no output lines match "%s"]`, match)
		}
		return strings.Join(append(lines, empty), "\n")
	}
	source := "from the stored tails"
	if reply.GetKept() {
		source = "from the kept log"
	}
	// The slice's lines are its output's lines, joined by newlines, so its last line is the first plus the newlines.
	first := int(reply.GetFirstLine())
	last := first + strings.Count(reply.GetOutput(), "\n")
	lines = append(lines,
		fmt.Sprintf("[output lines %d-%d of %d %s]", first, last, reply.GetTotal(), source),
		reply.GetOutput())
	if next := reply.GetNextBefore(); next != 0 {
		lines = append(lines, fmt.Sprintf("[earlier lines: continue with before %d]", next))
	}
	return strings.Join(lines, "\n")
}
