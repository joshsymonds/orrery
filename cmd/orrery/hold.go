package main

import (
	"context"
	"flag"
	"fmt"
	"io"

	"connectrpc.com/connect"

	"github.com/joshsymonds/orrery/internal/cli"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/protocol"
)

// runHold runs `orrery hold [--off] MEMBER`. It places an owner hold on MEMBER through orreryd's Pools.Hold, or with
// --off frees the hold through Pools.Free. Neither reads pool files; orreryd judges MEMBER. Success writes nothing. A
// refusal by orreryd, or orreryd unreachable, writes the reason to stderr and exits 1.
func runHold(ctx context.Context, opts globals, args []string, stderr io.Writer, getenv func(string) string) error {
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
		_, err = holds.Free(ctx, connect.NewRequest(&v1.PoolsFreeRequest{Member: member}))
	} else {
		_, err = holds.Hold(ctx, connect.NewRequest(&v1.PoolsHoldRequest{Member: member}))
	}
	if err != nil {
		return refuseHold(stderr, err)
	}
	return nil
}

// refuseHold writes why the hold did not happen and returns the error that exits 1.
func refuseHold(stderr io.Writer, err error) error {
	fmt.Fprintf(stderr, "orrery hold: %v\n", err)
	return statusError{code: exitFailure}
}
