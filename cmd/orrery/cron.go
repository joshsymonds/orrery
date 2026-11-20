package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"connectrpc.com/connect"

	"github.com/joshsymonds/orrery/internal/cli"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/stepmcp"
)

// cronAnswerRefused begins the answer orreryd gives a cron call that it refuses. The answer is text, not an error, so
// the command tells a refusal by this prefix.
const cronAnswerRefused = "orrery refused this call"

// cronVerb is the subcommand `orrery cron`.
const cronVerb = "cron"

// The cron tools `orrery cron` calls.
const (
	cronListToolName   = "CronList"
	cronDeleteToolName = "CronDelete"
)

// cronDeleteArgs is the tool_input of a CronDelete call: the id of the job to cancel.
type cronDeleteArgs struct {
	ID string `json:"id"`
}

// runCron runs `orrery cron`. It calls the cron tools on the session socket beside the daemon socket, as runContract
// does, so the command opens no listener and no store.
func runCron(ctx context.Context, opts globals, args []string, stdout io.Writer, getenv func(string) string) error {
	socket, err := protocol.SocketPath(opts.daemonSocket, getenv)
	if err != nil {
		return fmt.Errorf("cron: %w", err)
	}
	sessionSocket := protocol.SessionSocketPath(socket)
	crons := enginev1connect.NewCronsClient(cli.SessionHTTPClient(sessionSocket), cli.SessionBaseURL,
		connect.WithInterceptors(stepmcp.CallerInterceptor(getenv)))
	return cronCommand(ctx, crons, args, stdout)
}

// cronCommand implements `orrery cron`. It makes the call that args name, and prints the answer on stdout with a
// newline. A refusal returns an error carrying the answer and prints nothing.
func cronCommand(ctx context.Context, crons enginev1connect.CronsClient, args []string, stdout io.Writer) error {
	tool, toolInput, err := cronToolCall(args)
	if err != nil {
		return err
	}
	res, err := crons.CronCall(ctx, connect.NewRequest(&v1.CronCallRequest{ToolName: tool, ToolInput: toolInput}))
	if err != nil {
		return fmt.Errorf("cron: %w", err)
	}
	answer := res.Msg.GetReason()
	if strings.HasPrefix(answer, cronAnswerRefused) {
		return errors.New(answer)
	}
	if _, err := fmt.Fprintln(stdout, answer); err != nil {
		return fmt.Errorf("cron: %w", err)
	}
	return nil
}

// cronToolCall returns the cron tool and its tool_input that args name: `list` with no argument, or `delete` with one
// id. Any other argument shape is a usage error.
func cronToolCall(args []string) (string, string, error) {
	switch {
	case len(args) == 1 && args[0] == epicListVerb:
		return cronListToolName, "{}", nil
	case len(args) == 2 && args[0] == "delete":
		input, err := json.Marshal(cronDeleteArgs{ID: args[1]})
		if err != nil {
			return "", "", fmt.Errorf("cron: encode arguments: %w", err)
		}
		return cronDeleteToolName, string(input), nil
	default:
		return "", "", usagef("cron: want list or delete ID")
	}
}
