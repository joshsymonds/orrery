package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

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
	verbWait   = "wait"
)

// waitTimeout is the wait each call of `orrery step wait` asks orreryd for: stepmcp.MaxWait, in whole seconds.
const waitTimeout = uint32(stepmcp.MaxWait / time.Second)

// stepUsage is the argument list of `orrery step`, as a usage error names it.
const stepUsage = "step: want resume ID, status ID or wait [--all] ID..."

// stepCommand is a parsed `orrery step` command line.
type stepCommand struct {
	verb string   // verbResume, verbStatus or verbWait
	all  bool     // wait: whether every ID must end, not the first
	ids  []string // the step ids the command names
}

// runStep runs `orrery step resume ID`, `orrery step status ID` or `orrery step wait [--all] ID...` through orreryd's
// Steps service on the session socket beside the daemon socket. Resume writes nothing. Status and wait write each step
// as one line of JSON on stdout, the step object step_status returns. A refusal by orreryd, or orreryd unreachable,
// writes the reason to stderr and exits 1. Each call orreryd answers Unavailable, or a restart cuts, is retried.
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
	case verbWait:
		err = waitForSteps(ctx, clock, steps, cmd.ids, cmd.all, stdout)
	}
	if err != nil {
		return refuseStep(stderr, err)
	}
	return nil
}

// parseStep parses the arguments after `orrery step`. Each form takes its ids exactly as its usage names them, and a
// wait takes --all before its ids. Any other line is a usage error.
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
	case verbWait:
		flags := flag.NewFlagSet("orrery step wait", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		all := flags.Bool("all", false, "wait until every ID has ended")
		if err := flags.Parse(rest); err != nil {
			return stepCommand{}, usagef("step: wait: %v", err)
		}
		if flags.NArg() == 0 {
			return stepCommand{}, usagef("step: want wait [--all] ID...")
		}
		return stepCommand{verb: verbWait, all: *all, ids: flags.Args()}, nil
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

// waitForSteps calls Steps.WaitSteps for ids until a step ends, and writes each step that ends to stdout. Without all it
// returns once the first named step has ended. With all it names only the steps still running in each call, and returns
// once every named step has ended. A WaitSteps answer that names no ended step asks again.
func waitForSteps(
	ctx context.Context,
	clock rpcretry.Clock,
	steps enginev1connect.StepsClient,
	ids []string,
	all bool,
	stdout io.Writer,
) error {
	pending := ids
	for len(pending) > 0 {
		waited, err := stepmcp.WaitSteps(ctx, steps, clock, pending, waitTimeout)
		if err != nil {
			return errors.Join(err)
		}
		ended := waited.Msg.GetEnded()
		if ended == nil {
			continue
		}
		if !slices.Contains(pending, ended.GetId()) {
			return fmt.Errorf("wait: orreryd reported step %s ended, which was not named", ended.GetId())
		}
		if err := writeStep(stdout, ended); err != nil {
			return err
		}
		if !all {
			return nil
		}
		pending = slices.DeleteFunc(slices.Clone(pending), func(id string) bool { return id == ended.GetId() })
	}
	return nil
}

// writeStep writes step to stdout as its step object, as step_status reports it, on one line.
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
