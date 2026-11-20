package stepmcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
)

// cronRefusedAnswer begins every refusal orreryd gives a cron call. The answer is text, not an error, so a tool tells a
// refusal by this prefix.
const cronRefusedAnswer = "orrery refused this call"

// cronListArgs is the arguments of cron_list, which takes none. Its input schema is an empty object, and the handler
// decodes the arguments with decodeArguments, which refuses any key.
type cronListArgs struct{}

// cronDeleteArgs is the arguments of cron_delete, described for its input schema.
type cronDeleteArgs struct {
	ID string `json:"id" jsonschema:"the id of the cron job to cancel"`
}

// cronDeleteInput is the tool_input of a CronDelete call: the id of the job to cancel.
type cronDeleteInput struct {
	ID string `json:"id"`
}

// cronTools returns the cron tools, each calling crons. A nil crons answers each call with a tool error.
func cronTools(crons enginev1connect.CronsClient) ([]Tool, error) {
	listInput, err := argumentSchema[cronListArgs](nil)
	if err != nil {
		return nil, err
	}
	deleteInput, err := argumentSchema[cronDeleteArgs](nil)
	if err != nil {
		return nil, err
	}
	return []Tool{
		{
			Name:        "cron_list",
			Description: "List this session's orrery cron jobs: the jobs CronCreate scheduled through orrery.",
			InputSchema: listInput,
			Handler:     cronListHandler(crons),
		},
		{
			Name: "cron_delete",
			Description: "Cancel one of this session's orrery cron jobs by id. " +
				"Use it instead of CronDelete, which never reaches orrery.",
			InputSchema: deleteInput,
			Handler:     cronDeleteHandler(crons),
		},
	}, nil
}

// cronListHandler answers cron_list with the CronList answer of the session.
func cronListHandler(crons enginev1connect.CronsClient) ToolHandler {
	return func(ctx context.Context, call Call) (ToolResult, error) {
		var args cronListArgs
		if err := decodeArguments(call.Arguments, &args); err != nil {
			return ToolResult{}, err
		}
		return cronCall(ctx, crons, "CronList", "{}")
	}
}

// cronDeleteHandler answers cron_delete with the CronDelete answer of the session for the id it names.
func cronDeleteHandler(crons enginev1connect.CronsClient) ToolHandler {
	return func(ctx context.Context, call Call) (ToolResult, error) {
		var args cronDeleteArgs
		if err := decodeArguments(call.Arguments, &args); err != nil {
			return ToolResult{}, err
		}
		if err := requireString("id", args.ID); err != nil {
			return ToolResult{}, err
		}
		input, err := json.Marshal(cronDeleteInput(args))
		if err != nil {
			return ToolResult{}, fmt.Errorf("encode cron input: %w", err)
		}
		return cronCall(ctx, crons, "CronDelete", string(input))
	}
}

// cronCall makes one CronCall RPC for toolName with toolInput, and returns its answer as the tool's text. A refusal is
// a tool error whose text is the whole answer. An RPC error is a tool error, and so is a nil crons.
func cronCall(ctx context.Context, crons enginev1connect.CronsClient, toolName, toolInput string) (ToolResult, error) {
	if crons == nil {
		return toolFailure(errors.New("orrery cron tools have no orreryd connection")), nil
	}
	res, err := crons.CronCall(ctx, connect.NewRequest(&v1.CronCallRequest{
		ToolName:  toolName,
		ToolInput: toolInput,
	}))
	if err != nil {
		return toolFailure(err), nil
	}
	answer := res.Msg.GetReason()
	if strings.HasPrefix(answer, cronRefusedAnswer) {
		return ToolResult{Content: []Content{{Type: textContentType, Text: answer}}, IsError: true}, nil
	}
	return ToolResult{Content: []Content{{Type: textContentType, Text: answer}}}, nil
}
