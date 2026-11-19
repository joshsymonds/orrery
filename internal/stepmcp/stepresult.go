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

// stepResultArgs is the arguments of step_result. Field names one top-level field of the step's result, and Item one
// element of that array field, by its id property or 0-based index. Offset and Limit page a value over its runes.
type stepResultArgs struct {
	ID     string `json:"id"`
	Field  string `json:"field,omitempty"`
	Item   string `json:"item,omitempty"`
	Offset uint32 `json:"offset,omitempty"`
	Limit  uint32 `json:"limit,omitempty"`
}

// outlineHint is the last line of an outline: how to read one part of the result.
const outlineHint = "Read a field with field, or one element of an array field with field and item."

// noResultText is the text of a step whose whole result is empty.
const noResultText = "The step has no result."

// stepResultHandler returns the handler of step_result, which reads one part of a step's result and makes one call for
// it. The call is retried on Unavailable, as step_status is.
func stepResultHandler(steps enginev1connect.StepsClient, clock rpcretry.Clock) ToolHandler {
	return func(ctx context.Context, call Call) (ToolResult, error) {
		var args stepResultArgs
		if err := decodeArguments(call.Arguments, &args); err != nil {
			return ToolResult{}, err
		}
		if err := requireString("id", args.ID); err != nil {
			return ToolResult{}, err
		}
		var read *connect.Response[v1.ReadStepResultResponse]
		err := rpcretry.Do(ctx, clock, rpcretry.Unavailable, func(callCtx context.Context) error {
			var callErr error
			read, callErr = steps.ReadStepResult(callCtx, connect.NewRequest(&v1.ReadStepResultRequest{
				Id:     args.ID,
				Field:  args.Field,
				Item:   args.Item,
				Offset: args.Offset,
				Limit:  args.Limit,
			}))
			return errors.Join(callErr)
		})
		if err != nil {
			return toolFailure(err), nil
		}
		text := renderStepResult(read.Msg, args.Field)
		return ToolResult{Content: []Content{{Type: textContentType, Text: text}}}, nil
	}
}

// renderStepResult returns the text of reply, the read of a request that named field. An outline lists each field on
// its own line, with its type and size, and then the hint. Any other read is its value, with a cut line after it when
// more of the part remains. A request with no field whose result is empty reads as having no result.
func renderStepResult(reply *v1.ReadStepResultResponse, field string) string {
	if outline := reply.GetOutline(); len(outline) > 0 {
		lines := make([]string, 0, len(outline)+1)
		for _, entry := range outline {
			lines = append(lines, fmt.Sprintf("%s (%s, %d %s)",
				entry.GetName(), entry.GetType(), entry.GetSize(), sizeUnit(entry.GetType())))
		}
		return strings.Join(append(lines, outlineHint), "\n")
	}
	if field == "" && reply.GetTotal() == 0 {
		return noResultText
	}
	text := reply.GetValue()
	if next := reply.GetNextOffset(); next != 0 {
		text += fmt.Sprintf("\n[cut: runes %d-%d of %d; continue with offset %d]",
			reply.GetOffset(), next, reply.GetTotal(), next)
	}
	return text
}

// sizeUnit returns the unit an outline counts a field of JSON type kind in: runes for a string, items for an array and
// keys for an object. The other types, number, boolean and null, are counted in bytes.
func sizeUnit(kind string) string {
	switch kind {
	case "string":
		return "runes"
	case "array":
		return "items"
	case "object":
		return "keys"
	default:
		return "bytes"
	}
}
