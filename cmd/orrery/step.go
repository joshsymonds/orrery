package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"connectrpc.com/connect"

	"github.com/joshsymonds/orrery/internal/cli"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/rpcretry"
	"github.com/joshsymonds/orrery/internal/stepmcp"
)

// notLimitedMessage is the message orreryd sends with a failed_precondition refusal of a resume for a step no usage
// limit holds. It is the text of enginehost.ErrStepNotLimited, which orreryd wraps into that message; the CLI does not
// import the enginehost package.
const notLimitedMessage = "step is not limited"

// The verbs of `orrery step`.
const (
	verbResume = "resume"
	verbStatus = "status"
)

// stepUsage is the argument list of `orrery step`, as a usage error names it.
const stepUsage = "step: want resume ID or status ID"

// stepCommand is a parsed `orrery step` command line.
type stepCommand struct {
	verb string   // verbResume or verbStatus
	ids  []string // the step ids the command names
}

// runStep runs `orrery step resume ID` or `orrery step status ID` through orreryd's Steps service on the session socket
// beside the daemon socket. Resume writes nothing. Status writes the step's state as one line of JSON on stdout, the
// state-only step object step_status returns. A refusal by orreryd, or orreryd unreachable, writes the reason to stderr
// and exits 1. Each call orreryd answers Unavailable, or a restart cuts, is retried.
func runStep(
	ctx context.Context,
	opts globals,
	args []string,
	stdout, stderr io.Writer,
	getenv func(string) string,
) error {
	return runStepWith(ctx, rpcretry.SystemClock{}, opts, args, stdout, stderr, getenv)
}

// runStepWith is runStep with the clock its retries wait on supplied.
func runStepWith(
	ctx context.Context,
	clock rpcretry.Clock,
	opts globals,
	args []string,
	stdout, stderr io.Writer,
	getenv func(string) string,
) error {
	cmd, err := parseStep(args)
	if err != nil {
		return err
	}
	socket, err := protocol.SocketPath(opts.daemonSocket, getenv)
	if err != nil {
		return refuseStep(stderr, err)
	}
	sessionSocket := protocol.SessionSocketPath(socket)
	steps := enginev1connect.NewStepsClient(cli.SessionHTTPClient(sessionSocket), cli.SessionBaseURL)
	switch cmd.verb {
	case verbResume:
		err = resumeStep(ctx, clock, steps, cmd.ids[0])
	case verbStatus:
		err = statusStep(ctx, clock, steps, cmd.ids[0], stdout)
	}
	if err != nil {
		return refuseStep(stderr, err)
	}
	return nil
}

// parseStep parses the arguments after `orrery step`. Each form takes exactly the one id its usage names. Any other line
// is a usage error.
func parseStep(args []string) (stepCommand, error) {
	if len(args) == 0 {
		return stepCommand{}, usagef("%s", stepUsage)
	}
	verb, rest := args[0], args[1:]
	switch verb {
	case verbResume, verbStatus:
		if len(rest) != 1 {
			return stepCommand{}, usagef("step: want %s ID", verb)
		}
		return stepCommand{verb: verb, ids: rest}, nil
	default:
		return stepCommand{}, usagef("%s", stepUsage)
	}
}

// resumeStep calls Steps.ResumeStep for id and retries as placeHold does. A cut attempt may have resumed the step before
// the connection dropped, so when a later attempt hears that the step is not limited, the step counts as resumed. An
// attempt that never connected sent nothing, so its later "not limited" is the answer and fails as it would at once.
func resumeStep(ctx context.Context, clock rpcretry.Clock, steps enginev1connect.StepsClient, id string) error {
	cut := false
	err := rpcretry.Do(ctx, clock, rpcretry.Unavailable, func(callCtx context.Context) error {
		_, callErr := steps.ResumeStep(callCtx, connect.NewRequest(&v1.ResumeStepRequest{Id: id}))
		if rpcretry.Unavailable(callErr) && !rpcretry.NeverConnected(callErr) {
			cut = true
		}
		return errors.Join(callErr)
	})
	if cut && err != nil && isNotLimited(err) {
		return nil
	}
	return errors.Join(err)
}

// isNotLimited reports whether err is orreryd's failed_precondition refusal that the step is not limited.
func isNotLimited(err error) bool {
	return connect.CodeOf(err) == connect.CodeFailedPrecondition && strings.Contains(err.Error(), notLimitedMessage)
}

// statusStep calls Steps.StepStatus for id, retries an Unavailable answer as step_status does, and writes the step to
// stdout as its step object.
func statusStep(
	ctx context.Context,
	clock rpcretry.Clock,
	steps enginev1connect.StepsClient,
	id string,
	stdout io.Writer,
) error {
	var status *connect.Response[v1.StepStatusResponse]
	err := rpcretry.Do(ctx, clock, rpcretry.Unavailable, func(callCtx context.Context) error {
		var callErr error
		status, callErr = steps.StepStatus(callCtx, connect.NewRequest(&v1.StepStatusRequest{Id: id}))
		return errors.Join(callErr)
	})
	if err != nil {
		return errors.Join(err)
	}
	return writeStep(stdout, status.Msg.GetStep())
}

// writeStep writes step to stdout as its state-only step object, as step_status reports it, on one line.
func writeStep(stdout io.Writer, step *v1.Step) error {
	text, err := stepmcp.StepObject(step)
	if err != nil {
		return errors.Join(err)
	}
	_, err = stdout.Write(append(text, '\n'))
	return errors.Join(err)
}

// refuseStep writes why the command did not finish and returns the error that exits 1.
func refuseStep(stderr io.Writer, err error) error {
	fmt.Fprintf(stderr, "orrery step: %v\n", err)
	return statusError{code: exitFailure}
}
