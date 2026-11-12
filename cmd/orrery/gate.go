package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/joshsymonds/orrery/internal/cli"
	"github.com/joshsymonds/orrery/internal/gates"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/pools"
	"github.com/joshsymonds/orrery/internal/protocol"
)

// runGate runs `orrery gate [--gates FILE] NAME`. It resolves NAME from FILE, then from the project's gates, and reads
// the pool files of the working directory. A gate that cannot run, because NAME is unknown, a file is refused, or a
// need names a pool no file declares, exits with cli.ExitNoGrant and runs nothing. Otherwise the gate's argv runs under
// a grant of its needs from orreryd, with the gate's limit, and the command's status is the exit status. One line
// names the gate, its file, its argv, its needs and that status.
func runGate(
	ctx context.Context,
	opts globals,
	args []string,
	stdin *os.File,
	stdout, stderr io.Writer,
	getenv func(string) string,
) error {
	gatesFile, name, err := parseGate(args)
	if err != nil {
		return err
	}
	gate, err := gates.Resolve(ctx, name, gatesFile, ".", getenv)
	if err != nil {
		return refuseGate(stderr, err)
	}
	project, defined, err := pools.Load(ctx, ".", getenv)
	if err != nil {
		return refuseGate(stderr, err)
	}
	needs := make([]*v1.PoolNeed, 0, len(gate.Needs))
	for _, need := range gate.Needs {
		if !declaresPool(defined, need.Pool) {
			return refuseGate(stderr, fmt.Errorf("pool %q is not declared by the pool files", need.Pool))
		}
		needs = append(needs, &v1.PoolNeed{Pool: need.Pool, Count: int32(need.Count)})
	}
	definitions, err := poolDefinitions(defined)
	if err != nil {
		return refuseGate(stderr, err)
	}
	argvJSON, err := json.Marshal(gate.Argv)
	if err != nil {
		return refuseGate(stderr, fmt.Errorf("gate %q: argv: %w", gate.Name, err))
	}
	socket, err := protocol.SocketPath(opts.daemonSocket, getenv)
	if err != nil {
		return refuseGate(stderr, err)
	}
	sessionSocket := protocol.SessionSocketPath(socket)
	grants := enginev1connect.NewPoolsClient(cli.SessionHTTPClient(sessionSocket), cli.SessionBaseURL)
	// SIGINT, SIGTERM and SIGHUP end the grant: the command gets SIGTERM, and gate exits with the command's status.
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	status := cli.RunWithGrant(ctx, cli.RunConfig{
		Pools: grants,
		Request: &v1.AcquireRequest{
			Project:   project,
			Pools:     definitions,
			Needs:     needs,
			ClientPid: int32(os.Getpid()),
			Command:   gate.Argv,
		},
		Argv:    gate.Argv,
		Limit:   gate.Limit,
		Environ: os.Environ(),
		Stdin:   stdin,
		Stdout:  stdout,
		Stderr:  stderr,
	})
	fmt.Fprintf(stderr, "orrery gate: %s from %s %s: argv %s needing %s; exit %d\n",
		gate.Name, gate.Source, gate.File, argvJSON, gateNeedsText(gate.Needs), status)
	return statusError{code: status}
}

// parseGate reads the arguments after `gate`: an optional --gates FILE, then the one NAME. It returns the gates file,
// which is empty when none is named, and the name.
func parseGate(args []string) (string, string, error) {
	flags := flag.NewFlagSet("orrery gate", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	gatesFile := flags.String("gates", "", "gates file read before the project's")
	if err := flags.Parse(args); err != nil {
		return "", "", usagef("gate: %v", err)
	}
	if flags.NArg() != 1 {
		return "", "", usagef("gate: want [--gates FILE] NAME")
	}
	return *gatesFile, flags.Arg(0), nil
}

// gateNeedsText returns the needs as POOL=N pairs, comma-separated, in the order the gate lists them.
func gateNeedsText(needs []gates.Need) string {
	parts := make([]string, 0, len(needs))
	for _, need := range needs {
		parts = append(parts, fmt.Sprintf("%s=%d", need.Pool, need.Count))
	}
	return strings.Join(parts, ",")
}

// refuseGate writes why the gate did not run and returns the error that exits cli.ExitNoGrant.
func refuseGate(stderr io.Writer, err error) error {
	fmt.Fprintf(stderr, "orrery gate: %v\n", err)
	return statusError{code: cli.ExitNoGrant}
}
