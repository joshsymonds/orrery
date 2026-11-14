package main

import (
	"context"
	"errors"
	"flag"
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

// notHeldMessage is the message orreryd sends with a not_found refusal of a Free for a member no hold keeps. It is the
// text of grants.ErrNotHeld, which orreryd wraps into that message; the CLI does not import the grants package.
const notHeldMessage = "member not held"

// runHold runs `orrery hold [--off] MEMBER`. It places an owner hold on MEMBER through orreryd's Pools.Hold, or with
// --off frees the hold through Pools.Free. Neither reads pool files; orreryd judges MEMBER. Success writes nothing. A
// refusal by orreryd, or orreryd unreachable, writes the reason to stderr and exits 1. Each call orreryd answers
// Unavailable, or a restart cuts, is retried for up to rpcretry.Window.
func runHold(ctx context.Context, opts globals, args []string, stderr io.Writer, getenv func(string) string) error {
	return runHoldWith(ctx, rpcretry.SystemClock{}, opts, args, stderr, getenv)
}

// runHoldWith is runHold with the clock its retries wait on supplied.
func runHoldWith(
	ctx context.Context,
	clock rpcretry.Clock,
	opts globals,
	args []string,
	stderr io.Writer,
	getenv func(string) string,
) error {
	flags := flag.NewFlagSet("orrery hold", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	off := flags.Bool("off", false, "free the member's hold")
	if err := flags.Parse(args); err != nil {
		return usagef("hold: %v", err)
	}
	if flags.NArg() != 1 {
		return usagef("hold: want [--off] MEMBER")
	}
	member := flags.Arg(0)
	socket, err := protocol.SocketPath(opts.daemonSocket, getenv)
	if err != nil {
		return refuseHold(stderr, err)
	}
	sessionSocket := protocol.SessionSocketPath(socket)
	holds := enginev1connect.NewPoolsClient(cli.SessionHTTPClient(sessionSocket), cli.SessionBaseURL)
	if *off {
		err = freeHold(ctx, clock, holds, member)
	} else {
		err = placeHold(ctx, clock, holds, member)
	}
	if err != nil {
		return refuseHold(stderr, err)
	}
	return nil
}

// placeHold calls Pools.Hold for member. A hold is idempotent, so a call that orreryd answers Unavailable or a restart
// cuts is simply sent again within rpcretry.Window.
func placeHold(ctx context.Context, clock rpcretry.Clock, holds enginev1connect.PoolsClient, member string) error {
	return errors.Join(rpcretry.Do(ctx, clock, rpcretry.Unavailable, func(callCtx context.Context) error {
		_, callErr := holds.Hold(callCtx, connect.NewRequest(&v1.PoolsHoldRequest{Member: member}))
		return errors.Join(callErr)
	}))
}

// freeHold calls Pools.Free for member and retries as placeHold does. A cut attempt may have freed the member before
// the connection dropped, so when a later attempt hears that the member is not held, the member counts as freed. An
// attempt that never connected sent nothing, so its later "not held" is the answer and fails as it would at once.
func freeHold(ctx context.Context, clock rpcretry.Clock, holds enginev1connect.PoolsClient, member string) error {
	cut := false
	err := rpcretry.Do(ctx, clock, rpcretry.Unavailable, func(callCtx context.Context) error {
		_, callErr := holds.Free(callCtx, connect.NewRequest(&v1.PoolsFreeRequest{Member: member}))
		if rpcretry.Unavailable(callErr) && !rpcretry.NeverConnected(callErr) {
			cut = true
		}
		return errors.Join(callErr)
	})
	if cut && err != nil && isNotHeld(err) {
		return nil
	}
	return errors.Join(err)
}

// isNotHeld reports whether err is orreryd's not_found refusal that the member is not held.
func isNotHeld(err error) bool {
	return connect.CodeOf(err) == connect.CodeNotFound && strings.Contains(err.Error(), notHeldMessage)
}

// refuseHold writes why the hold did not happen and returns the error that exits 1.
func refuseHold(stderr io.Writer, err error) error {
	fmt.Fprintf(stderr, "orrery hold: %v\n", err)
	return statusError{code: exitFailure}
}
