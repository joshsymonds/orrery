package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"connectrpc.com/connect"

	"github.com/joshsymonds/orrery/internal/cli"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/protocol"
)

// releaseSlotMaxLimit is the longest a hold may run. orreryd refuses a longer limit too.
const releaseSlotMaxLimit = 2 * time.Hour

// The verbs `orrery release-slot` takes, and the kind of an epic holder.
const (
	releaseSlotHoldVerb = "hold"
	releaseSlotGiveVerb = "give"
	releaseSlotShowVerb = "show"
	releaseSlotEpicKind = "epic"
)

// releaseSlotCall is one `orrery release-slot` command line that parsed: its verb, its repository, the holder it names,
// the seconds a hold lasts, and whether a hold waits.
type releaseSlotCall struct {
	verb    string
	repo    string
	holder  string
	seconds int64
	wait    bool
}

// runReleaseSlot runs `orrery release-slot hold|give|show REPO`. It calls the ReleaseSlots service on the session
// socket beside the daemon socket, as runCron does, so the command opens no listener and no store. A refused command
// line is a usage error and makes no call. A call that fails, or an orreryd that cannot be reached, writes why to
// stderr and exits 1. No call is retried.
func runReleaseSlot(
	ctx context.Context,
	opts globals,
	args []string,
	stdout, stderr io.Writer,
	getenv func(string) string,
) error {
	call, err := parseReleaseSlot(args)
	if err != nil {
		return err
	}
	repo, err := filepath.Abs(call.repo)
	if err != nil {
		return refuseReleaseSlot(stderr, err)
	}
	call.repo = repo
	socket, err := protocol.SocketPath(opts.daemonSocket, getenv)
	if err != nil {
		return refuseReleaseSlot(stderr, err)
	}
	slots := enginev1connect.NewReleaseSlotsClient(
		cli.SessionHTTPClient(protocol.SessionSocketPath(socket)), cli.SessionBaseURL)
	return releaseSlotCommand(ctx, slots, call, stdout, stderr)
}

// parseReleaseSlot checks the arguments after `release-slot`: the verb, then REPO, then the flags of the verb. Every
// refusal is a usage error.
func parseReleaseSlot(args []string) (releaseSlotCall, error) {
	if len(args) < 2 {
		return releaseSlotCall{}, usagef("release-slot: want hold, give or show, then REPO")
	}
	call := releaseSlotCall{verb: args[0], repo: args[1]}
	flags := flag.NewFlagSet("orrery release-slot "+call.verb, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var limit string
	switch call.verb {
	case releaseSlotHoldVerb:
		flags.StringVar(&call.holder, "holder", "", "the label that holds the slot")
		flags.StringVar(&limit, "limit", "", "how long the hold lasts, as a duration such as 30m")
		flags.BoolVar(&call.wait, "wait", false, "wait while another holder keeps the slot")
	case releaseSlotGiveVerb:
		flags.StringVar(&call.holder, "holder", "", "the label whose hold is given back")
	case releaseSlotShowVerb:
		// show takes no flags, so any flag it is given fails to parse below.
	default:
		return releaseSlotCall{}, usagef("release-slot: unknown verb %q, want hold, give or show", call.verb)
	}
	if err := flags.Parse(args[2:]); err != nil {
		return releaseSlotCall{}, usagef("release-slot %s: %v", call.verb, err)
	}
	if flags.NArg() > 0 {
		return releaseSlotCall{}, usagef("release-slot %s: unexpected argument %q", call.verb, flags.Arg(0))
	}
	if call.verb == releaseSlotShowVerb {
		return call, nil
	}
	if call.holder == "" {
		return releaseSlotCall{}, usagef("release-slot %s: --holder is required", call.verb)
	}
	if call.verb != releaseSlotHoldVerb {
		return call, nil
	}
	if limit == "" {
		return releaseSlotCall{}, usagef("release-slot hold: --limit is required")
	}
	span, err := time.ParseDuration(limit)
	if err != nil || span <= 0 || span > releaseSlotMaxLimit {
		return releaseSlotCall{}, usagef(
			"release-slot hold: --limit %q wants a duration above 0 and at most %v", limit, releaseSlotMaxLimit)
	}
	// The limit goes to orreryd in whole seconds, rounded up, so a limit is never shorter than the one given.
	call.seconds = int64((span + time.Second - 1) / time.Second)
	return call, nil
}

// releaseSlotCommand makes the call that call names, and writes its answer.
func releaseSlotCommand(
	ctx context.Context,
	slots enginev1connect.ReleaseSlotsClient,
	call releaseSlotCall,
	stdout, stderr io.Writer,
) error {
	switch call.verb {
	case releaseSlotHoldVerb:
		if call.wait {
			return releaseSlotHoldWait(ctx, slots, call, stdout, stderr)
		}
		return releaseSlotHold(ctx, slots, call, stdout, stderr)
	case releaseSlotGiveVerb:
		return releaseSlotGive(ctx, slots, call, stdout, stderr)
	default:
		// parseReleaseSlot admits no other verb than show here.
		return releaseSlotShow(ctx, slots, call, stdout, stderr)
	}
}

// releaseSlotHold calls Hold. A hold that takes the slot writes the held line to stdout. A slot another holder keeps
// writes that holder to stderr and exits 1.
func releaseSlotHold(
	ctx context.Context,
	slots enginev1connect.ReleaseSlotsClient,
	call releaseSlotCall,
	stdout, stderr io.Writer,
) error {
	res, err := slots.Hold(ctx, connect.NewRequest(&v1.ReleaseSlotsHoldRequest{
		Repository: call.repo, Holder: call.holder, LimitSeconds: call.seconds,
	}))
	if err != nil {
		return refuseReleaseSlot(stderr, err)
	}
	if !res.Msg.GetHeld() {
		fmt.Fprintf(stderr, "orrery release-slot: %s is held by %s\n",
			call.repo, releaseSlotHolderText(res.Msg.GetHolder()))
		return statusError{code: exitFailure}
	}
	writeReleaseSlotHeld(stdout, call, res.Msg.GetHolder())
	return nil
}

// releaseSlotHoldWait calls HoldWait and reads its stream. Each waiting event writes the holder that keeps the slot to
// stderr. The held event writes the held line to stdout and exits 0. A stream that ends before the held event, or with
// an error, exits 1.
func releaseSlotHoldWait(
	ctx context.Context,
	slots enginev1connect.ReleaseSlotsClient,
	call releaseSlotCall,
	stdout, stderr io.Writer,
) error {
	stream, err := slots.HoldWait(ctx, connect.NewRequest(&v1.ReleaseSlotsHoldWaitRequest{
		Repository: call.repo, Holder: call.holder, LimitSeconds: call.seconds,
	}))
	if err != nil {
		return refuseReleaseSlot(stderr, err)
	}
	held, err := releaseSlotReadWait(stream, call.repo, stderr)
	if closeErr := stream.Close(); closeErr != nil {
		fmt.Fprintf(stderr, "orrery release-slot: close the wait's stream: %v\n", closeErr)
	}
	if err != nil {
		return refuseReleaseSlot(stderr, err)
	}
	writeReleaseSlotHeld(stdout, call, held)
	return nil
}

// releaseSlotReadWait reads the events of a HoldWait stream. It writes each waiting event to stderr, and returns the
// holder of the held event. A stream that ends before the held event returns the error it met, or an error that says
// the wait ended with the slot still not held.
func releaseSlotReadWait(
	stream *connect.ServerStreamForClient[v1.ReleaseSlotsHoldWaitResponse],
	repo string,
	stderr io.Writer,
) (*v1.ReleaseSlotHolder, error) {
	for stream.Receive() {
		switch event := stream.Msg().GetEvent().(type) {
		case *v1.ReleaseSlotsHoldWaitResponse_Waiting:
			fmt.Fprintf(stderr, "orrery release-slot: waiting: %s is held by %s\n",
				repo, releaseSlotHolderText(event.Waiting))
		case *v1.ReleaseSlotsHoldWaitResponse_Held:
			return event.Held, nil
		}
	}
	if err := stream.Err(); err != nil {
		return nil, errors.Join(err)
	}
	return nil, fmt.Errorf("the wait for %s ended before the slot was held", repo)
}

// releaseSlotGive calls Give, and writes freed, or that nothing was freed. Both exit 0.
func releaseSlotGive(
	ctx context.Context,
	slots enginev1connect.ReleaseSlotsClient,
	call releaseSlotCall,
	stdout, stderr io.Writer,
) error {
	res, err := slots.Give(ctx, connect.NewRequest(&v1.ReleaseSlotsGiveRequest{
		Repository: call.repo, Holder: call.holder,
	}))
	if err != nil {
		return refuseReleaseSlot(stderr, err)
	}
	if res.Msg.GetFreed() {
		fmt.Fprintf(stdout, "freed %s\n", call.repo)
		return nil
	}
	fmt.Fprintf(stdout, "%s is not held by %s; nothing freed\n", call.repo, call.holder)
	return nil
}

// releaseSlotShow calls Show, and writes the holder of the slot, or that it is free. Both exit 0.
func releaseSlotShow(
	ctx context.Context,
	slots enginev1connect.ReleaseSlotsClient,
	call releaseSlotCall,
	stdout, stderr io.Writer,
) error {
	res, err := slots.Show(ctx, connect.NewRequest(&v1.ReleaseSlotsShowRequest{Repository: call.repo}))
	if err != nil {
		return refuseReleaseSlot(stderr, err)
	}
	if !res.Msg.GetHeld() {
		fmt.Fprintf(stdout, "%s: free\n", call.repo)
		return nil
	}
	fmt.Fprintf(stdout, "%s: held by %s\n", call.repo, releaseSlotHolderText(res.Msg.GetHolder()))
	return nil
}

// writeReleaseSlotHeld writes the held line of a hold that took the slot: the repository, the label and the deadline.
func writeReleaseSlotHeld(stdout io.Writer, call releaseSlotCall, holder *v1.ReleaseSlotHolder) {
	fmt.Fprintf(stdout, "held %s for %s until %s\n",
		call.repo, call.holder, releaseSlotDeadline(holder.GetDeadline()))
}

// releaseSlotHolderText names a holder as the command writes it: `epic <id>`, or `outside <label> until <deadline>`.
func releaseSlotHolderText(holder *v1.ReleaseSlotHolder) string {
	if holder.GetKind() == releaseSlotEpicKind {
		return "epic " + holder.GetEpic()
	}
	return fmt.Sprintf("outside %s until %s", holder.GetLabel(), releaseSlotDeadline(holder.GetDeadline()))
}

// releaseSlotDeadline writes a hold's deadline, which is unix seconds, as UTC in RFC 3339.
func releaseSlotDeadline(seconds int64) string {
	return time.Unix(seconds, 0).UTC().Format(time.RFC3339)
}

// refuseReleaseSlot writes why a release-slot command did not finish, and returns the error that exits 1.
func refuseReleaseSlot(stderr io.Writer, err error) error {
	fmt.Fprintf(stderr, "orrery release-slot: %v\n", err)
	return statusError{code: exitFailure}
}
