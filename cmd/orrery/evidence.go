package main

import (
	"context"
	"errors"
	"fmt"
	"io"

	"connectrpc.com/connect"

	"github.com/joshsymonds/orrery/internal/cli"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/rpcretry"
	"github.com/joshsymonds/orrery/internal/stepmcp"
)

// The subcommands of `orrery evidence`.
const (
	evidenceVerbRun     = "run"
	evidenceVerbList    = "list"
	evidenceVerbGet     = "get"
	evidenceVerbCapture = "capture"
	evidenceVerbAttach  = "attach"
)

// revisionWidth is how many characters of a run's revision `orrery evidence list` prints.
const revisionWidth = 12

// runEvidence runs `orrery evidence run EPIC ITEM REVISION PHASE`, `orrery evidence list EPIC`,
// `orrery evidence get RUN [NAME]`, `orrery evidence capture --commit SHA --brief RUN... [--runner NAME] [--file
// PATH]... -- ARGV...` or `orrery evidence attach CAPTURE RUN...` through orreryd's Evidence service on the session
// socket, as the caller session the environment names (stepmcp.CallerInterceptor). run prints the run on one line,
// list prints each run with its artifacts, get writes the bytes of one artifact, or of the run's log, to stdout,
// capture prints the capture's id and what comes next, and attach prints each run it attached the capture to. capture
// and attach are the evidence gatherer's: orreryd answers them only for the epic's running gatherer step. A refusal by
// orreryd, or orreryd unreachable, writes the reason to stderr and exits 1. A wrong argument count, a malformed capture
// or an unknown subcommand exits 2.
func runEvidence(
	ctx context.Context,
	opts globals,
	args []string,
	stdout, stderr io.Writer,
	getenv func(string) string,
) error {
	return runEvidenceWith(ctx, rpcretry.SystemClock{}, opts, args, stdout, stderr, getenv)
}

// runEvidenceWith is runEvidence with the clock its retries wait on supplied.
func runEvidenceWith(
	ctx context.Context,
	clock rpcretry.Clock,
	opts globals,
	args []string,
	stdout, stderr io.Writer,
	getenv func(string) string,
) error {
	if len(args) == 0 {
		return usagef("evidence: want run, list, get, capture or attach")
	}
	verb, rest := args[0], args[1:]
	if err := checkEvidenceArgs(verb, rest); err != nil {
		return err
	}
	socket, err := protocol.SocketPath(opts.daemonSocket, getenv)
	if err != nil {
		return refuseEvidence(stderr, verb, err)
	}
	evidence := enginev1connect.NewEvidenceClient(
		cli.SessionHTTPClient(protocol.SessionSocketPath(socket)), cli.SessionBaseURL,
		connect.WithInterceptors(stepmcp.CallerInterceptor(getenv)))
	if err = callEvidence(ctx, clock, evidence, stdout, verb, rest); err != nil {
		return refuseEvidence(stderr, verb, err)
	}
	return nil
}

// checkEvidenceArgs returns the usage error of the arguments rest of the subcommand verb, or nil when they fit it. A
// capture's arguments fit when parseCapture reads them.
func checkEvidenceArgs(verb string, rest []string) error {
	switch verb {
	case evidenceVerbCapture:
		_, err := parseCapture(rest)
		return err
	case evidenceVerbAttach:
		if len(rest) < 2 {
			return usagef("evidence attach: want CAPTURE RUN...")
		}
	case evidenceVerbRun:
		if len(rest) != 4 {
			return usagef("evidence run: want EPIC ITEM REVISION PHASE")
		}
	case evidenceVerbList:
		if len(rest) != 1 {
			return usagef("evidence list: want EPIC")
		}
	case evidenceVerbGet:
		if len(rest) != 1 && len(rest) != 2 {
			return usagef("evidence get: want RUN [NAME]")
		}
	default:
		return usagef("evidence: unknown subcommand %q", verb)
	}
	return nil
}

// callEvidence calls the Evidence service for the subcommand verb with its arguments rest, which checkEvidenceArgs
// accepted, and writes what it prints to stdout.
func callEvidence(
	ctx context.Context,
	clock rpcretry.Clock,
	evidence enginev1connect.EvidenceClient,
	stdout io.Writer,
	verb string,
	rest []string,
) error {
	switch verb {
	case evidenceVerbRun:
		return evidenceRun(ctx, clock, evidence, stdout, rest)
	case evidenceVerbCapture:
		req, err := parseCapture(rest)
		if err != nil {
			return err
		}
		return evidenceCapture(ctx, clock, evidence, stdout, req)
	case evidenceVerbAttach:
		return evidenceAttach(ctx, clock, evidence, stdout, rest[0], rest[1:])
	case evidenceVerbList:
		return evidenceList(ctx, clock, evidence, stdout, rest[0])
	default:
		return evidenceGet(ctx, clock, evidence, stdout, rest)
	}
}

// evidenceCall calls call and retries it while retryable accepts its error and rpcretry.Window has not passed on
// clock. It returns the response of the call that succeeded.
func evidenceCall[Reply any](
	ctx context.Context,
	clock rpcretry.Clock,
	retryable func(error) bool,
	call func(context.Context) (*connect.Response[Reply], error),
) (*connect.Response[Reply], error) {
	var res *connect.Response[Reply]
	err := rpcretry.Do(ctx, clock, retryable, func(callCtx context.Context) error {
		var callErr error
		res, callErr = call(callCtx)
		return callErr
	})
	return res, errors.Join(err)
}

// evidenceRun calls RunEvidence with args, the epic, item, revision and phase, and prints the run's id, item, phase and
// state. A run is not idempotent: each call orreryd answers stores and starts a new capture, so a call orreryd may have
// received is never sent again. Only a call that never connected is sent again.
func evidenceRun(
	ctx context.Context,
	clock rpcretry.Clock,
	evidence enginev1connect.EvidenceClient,
	stdout io.Writer,
	args []string,
) error {
	msg := &v1.RunEvidenceRequest{Epic: args[0], Item: args[1], Revision: args[2], Phase: args[3]}
	ran, err := evidenceCall(ctx, clock, rpcretry.NeverConnected,
		func(callCtx context.Context) (*connect.Response[v1.RunEvidenceResponse], error) {
			return evidence.RunEvidence(callCtx, connect.NewRequest(msg))
		})
	if err != nil {
		return err
	}
	run := ran.Msg.GetRun()
	_, err = fmt.Fprintf(stdout, "%s %s %s %s\n", run.GetId(), run.GetItem(), run.GetPhase(), run.GetState())
	return errors.Join(err)
}

// evidenceList calls ListEvidence for epic and prints each run on one line, in the order orreryd gives, then each of
// its artifacts on an indented line. A read is sent again when orreryd answers Unavailable or a restart cuts the call.
func evidenceList(
	ctx context.Context,
	clock rpcretry.Clock,
	evidence enginev1connect.EvidenceClient,
	stdout io.Writer,
	epic string,
) error {
	msg := &v1.ListEvidenceRequest{Epic: epic}
	listed, err := evidenceCall(ctx, clock, rpcretry.Unavailable,
		func(callCtx context.Context) (*connect.Response[v1.ListEvidenceResponse], error) {
			return evidence.ListEvidence(callCtx, connect.NewRequest(msg))
		})
	if err != nil {
		return err
	}
	for _, run := range listed.Msg.GetRuns() {
		_, err := fmt.Fprintf(stdout, "%s %s %s %s %s %s %s %s\n",
			run.GetId(), run.GetItem(), run.GetPhase(), run.GetSource(), run.GetState(),
			verdictOrDash(run.GetVerdict()), passedWord(run.GetPassed()), shortRevision(run.GetRevision()))
		if err != nil {
			return errors.Join(err)
		}
		for _, artifact := range run.GetArtifacts() {
			_, err := fmt.Fprintf(stdout, "  %s %s %d\n", artifact.GetPath(), artifact.GetState(), artifact.GetSize())
			if err != nil {
				return errors.Join(err)
			}
		}
	}
	return nil
}

// evidenceGet calls GetEvidenceArtifact for the run in args and writes the bytes it returns to stdout unchanged. The
// artifact is the one named by the second argument, or the run's log when there is none. A read is sent again when
// orreryd answers Unavailable or a restart cuts the call.
func evidenceGet(
	ctx context.Context,
	clock rpcretry.Clock,
	evidence enginev1connect.EvidenceClient,
	stdout io.Writer,
	args []string,
) error {
	name := ""
	if len(args) == 2 {
		name = args[1]
	}
	msg := &v1.GetEvidenceArtifactRequest{Run: args[0], Name: name}
	got, err := evidenceCall(ctx, clock, rpcretry.Unavailable,
		func(callCtx context.Context) (*connect.Response[v1.GetEvidenceArtifactResponse], error) {
			return evidence.GetEvidenceArtifact(callCtx, connect.NewRequest(msg))
		})
	if err != nil {
		return err
	}
	_, err = stdout.Write(got.Msg.GetData())
	return errors.Join(err)
}

// parseCapture reads the arguments of `orrery evidence capture`: --commit SHA once, --brief RUN at least once,
// --runner NAME at most once and --file PATH any number of times, each flag and its value as two arguments, then --
// and the argv, which is not empty. Anything else is a usage error.
func parseCapture(args []string) (*v1.CaptureEvidenceRequest, error) {
	req := &v1.CaptureEvidenceRequest{}
	for i := 0; i < len(args); i += 2 {
		flag := args[i]
		if flag == "--" {
			req.Argv = args[i+1:]
			break
		}
		if i+1 >= len(args) {
			return nil, usagef("evidence capture: %s wants a value", flag)
		}
		value := args[i+1]
		switch flag {
		case "--commit":
			if req.GetCommit() != "" {
				return nil, usagef("evidence capture: --commit given twice")
			}
			req.Commit = value
		case "--runner":
			if req.GetRunner() != "" {
				return nil, usagef("evidence capture: --runner given twice")
			}
			req.Runner = value
		case "--brief":
			req.Briefs = append(req.Briefs, value)
		case "--file":
			req.Files = append(req.Files, value)
		default:
			return nil, usagef("evidence capture: unknown flag %q", flag)
		}
	}
	switch {
	case req.GetCommit() == "":
		return nil, usagef("evidence capture: want --commit SHA")
	case len(req.GetBriefs()) == 0:
		return nil, usagef("evidence capture: want --brief RUN")
	case len(req.GetArgv()) == 0:
		return nil, usagef("evidence capture: want -- ARGV...")
	}
	return req, nil
}

// evidenceCapture calls CaptureEvidence with req and prints the capture's id on one line, then stepmcp.CaptureNext. A
// capture is not idempotent, so a call orreryd may have received is never sent again; only a call that never connected
// is.
func evidenceCapture(
	ctx context.Context,
	clock rpcretry.Clock,
	evidence enginev1connect.EvidenceClient,
	stdout io.Writer,
	req *v1.CaptureEvidenceRequest,
) error {
	captured, err := evidenceCall(ctx, clock, rpcretry.NeverConnected,
		func(callCtx context.Context) (*connect.Response[v1.CaptureEvidenceResponse], error) {
			return evidence.CaptureEvidence(callCtx, connect.NewRequest(req))
		})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "%s\n%s\n", captured.Msg.GetCapture(), stepmcp.CaptureNext)
	return errors.Join(err)
}

// evidenceAttach calls AttachEvidence with capture and briefs and prints each run it attached the capture to on one
// line: its id, item, phase and state. Only a call that never connected is sent again.
func evidenceAttach(
	ctx context.Context,
	clock rpcretry.Clock,
	evidence enginev1connect.EvidenceClient,
	stdout io.Writer,
	capture string,
	briefs []string,
) error {
	msg := &v1.AttachEvidenceRequest{Capture: capture, Briefs: briefs}
	attached, err := evidenceCall(ctx, clock, rpcretry.NeverConnected,
		func(callCtx context.Context) (*connect.Response[v1.AttachEvidenceResponse], error) {
			return evidence.AttachEvidence(callCtx, connect.NewRequest(msg))
		})
	if err != nil {
		return err
	}
	for _, run := range attached.Msg.GetRuns() {
		if _, err := fmt.Fprintf(stdout, "%s %s %s %s\n",
			run.GetId(), run.GetItem(), run.GetPhase(), run.GetState()); err != nil {
			return errors.Join(err)
		}
	}
	return nil
}

// refuseEvidence writes why the subcommand verb did not happen and returns the error that exits 1.
func refuseEvidence(stderr io.Writer, verb string, err error) error {
	fmt.Fprintf(stderr, "orrery evidence %s: %v\n", verb, err)
	return statusError{code: exitFailure}
}

// verdictOrDash returns verdict, or - when the judge gave none.
func verdictOrDash(verdict string) string {
	if verdict == "" {
		return "-"
	}
	return verdict
}

// passedWord returns the word `orrery evidence list` prints for whether a run passes its phase.
func passedWord(passed bool) string {
	if passed {
		return "passed"
	}
	return "not passed"
}

// shortRevision returns the first revisionWidth characters of revision, or revision whole when it is shorter.
func shortRevision(revision string) string {
	if len(revision) > revisionWidth {
		return revision[:revisionWidth]
	}
	return revision
}
