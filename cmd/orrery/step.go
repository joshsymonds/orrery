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
)

// notLimitedMessage is the message orreryd sends with a failed_precondition refusal of a resume for a step no usage
// limit holds. It is the text of enginehost.ErrStepNotLimited, which orreryd wraps into that message; the CLI does not
// import the enginehost package.
const notLimitedMessage = "step is not limited"

// runStep runs `orrery step resume ID`. It resumes the limited step ID through orreryd's Steps.ResumeStep, with the
// session socket beside the daemon socket. Success writes nothing. A refusal by orreryd, or orreryd unreachable, writes
// the reason to stderr and exits 1. Each call orreryd answers Unavailable, or a restart cuts, is retried for up to
// rpcretry.Window.
func runStep(ctx context.Context, opts globals, args []string, stderr io.Writer, getenv func(string) string) error {
	return runStepWith(ctx, rpcretry.SystemClock{}, opts, args, stderr, getenv)
}

// runStepWith is runStep with the clock its retries wait on supplied.
func runStepWith(
	ctx context.Context,
	clock rpcretry.Clock,
	opts globals,
	args []string,
	stderr io.Writer,
	getenv func(string) string,
) error {
	if len(args) != 2 || args[0] != "resume" {
		return usagef("step: want resume ID")
	}
	socket, err := protocol.SocketPath(opts.daemonSocket, getenv)
	if err != nil {
		return refuseStep(stderr, err)
	}
	sessionSocket := protocol.SessionSocketPath(socket)
	steps := enginev1connect.NewStepsClient(cli.SessionHTTPClient(sessionSocket), cli.SessionBaseURL)
	err = resumeStep(ctx, clock, steps, args[1])
	if err != nil {
		return refuseStep(stderr, err)
	}
	return nil
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

// refuseStep writes why the resume did not happen and returns the error that exits 1.
func refuseStep(stderr io.Writer, err error) error {
	fmt.Fprintf(stderr, "orrery step: %v\n", err)
	return statusError{code: exitFailure}
}
