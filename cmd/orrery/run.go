package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/signal"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/joshsymonds/orrery/internal/cli"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/pools"
	"github.com/joshsymonds/orrery/internal/protocol"
)

// runPoolName matches a pool name as a pool file spells it. The pools package keeps its pattern unexported, so the
// command line checks the same form before it sends a need.
var runPoolName = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// runCommand runs `orrery run`. It reads the pool files of the working directory, asks orreryd for the members the
// --needs flags name, and runs the command after -- under that grant. It exits with the command's status, or with
// cli.ExitNoGrant when the command did not run for want of a grant.
func runCommand(
	ctx context.Context,
	opts globals,
	args []string,
	stdin *os.File,
	stdout, stderr io.Writer,
	getenv func(string) string,
) error {
	needs, argv, err := parseRun(args)
	if err != nil {
		return err
	}
	project, defined, err := pools.Load(ctx, ".", getenv)
	if err != nil {
		return refuseRun(stderr, err)
	}
	for _, need := range needs {
		if !declaresPool(defined, need.GetPool()) {
			return refuseRun(stderr, fmt.Errorf("pool %q is not declared by the pool files", need.GetPool()))
		}
	}
	definitions, err := poolDefinitions(defined)
	if err != nil {
		return refuseRun(stderr, err)
	}
	socket, err := protocol.SocketPath(opts.daemonSocket, getenv)
	if err != nil {
		return refuseRun(stderr, err)
	}
	sessionSocket := protocol.SessionSocketPath(socket)
	grants := enginev1connect.NewPoolsClient(cli.SessionHTTPClient(sessionSocket), cli.SessionBaseURL)
	// SIGINT, SIGTERM and SIGHUP end the grant: the command gets SIGTERM, and run exits with the command's status.
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	status := cli.RunWithGrant(ctx, cli.RunConfig{
		Pools: grants,
		Request: &v1.AcquireRequest{
			Project:   project,
			Pools:     definitions,
			Needs:     needs,
			ClientPid: int32(os.Getpid()),
		},
		Argv:    argv,
		Environ: os.Environ(),
		Stdin:   stdin,
		Stdout:  stdout,
		Stderr:  stderr,
	})
	return statusError{code: status}
}

// parseRun reads the arguments after `run`: --needs POOL=N flags, then -- and the command. Every flag must come before
// the --, and the command must follow it. It returns the needs in the order given and the command's argv.
func parseRun(args []string) ([]*v1.PoolNeed, []string, error) {
	flagArgs, argv, found := cutCommand(args)
	if !found || len(argv) == 0 {
		return nil, nil, usagef("run: want -- CMD [ARGS]")
	}
	flags := flag.NewFlagSet("orrery run", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var needFlags listFlag
	flags.Var(&needFlags, "needs", "POOL=N, repeatable")
	if err := flags.Parse(flagArgs); err != nil {
		return nil, nil, usagef("run: %v", err)
	}
	if flags.NArg() > 0 {
		return nil, nil, usagef("run: unexpected argument %q before --", flags.Arg(0))
	}
	if len(needFlags) == 0 {
		return nil, nil, usagef("run: --needs POOL=N is required")
	}
	needs := make([]*v1.PoolNeed, 0, len(needFlags))
	named := make(map[string]bool, len(needFlags))
	for _, raw := range needFlags {
		pool, text, ok := strings.Cut(raw, "=")
		count, err := strconv.ParseInt(text, 10, 32)
		if !ok || !runPoolName.MatchString(pool) || err != nil || count < 1 {
			return nil, nil, usagef("run: --needs %q: want POOL=N with a lowercase POOL and N of 1 or more", raw)
		}
		if named[pool] {
			return nil, nil, usagef("run: pool %q is named twice", pool)
		}
		named[pool] = true
		needs = append(needs, &v1.PoolNeed{Pool: pool, Count: int32(count)})
	}
	return needs, argv, nil
}

// cutCommand splits args at the first --. It returns the arguments before it, the arguments after it, and whether a --
// was found.
func cutCommand(args []string) ([]string, []string, bool) {
	i := slices.Index(args, "--")
	if i < 0 {
		return args, nil, false
	}
	return args[:i], args[i+1:], true
}

// declaresPool reports whether one of the defined pools is named name.
func declaresPool(defined []pools.Pool, name string) bool {
	return slices.ContainsFunc(defined, func(pool pools.Pool) bool { return pool.Name == name })
}

// poolDefinitions returns the defined pools as the request carries them, with each limit in whole seconds. A cap too
// large for the request's int32 is refused rather than wrapped.
func poolDefinitions(defined []pools.Pool) ([]*v1.PoolDefinition, error) {
	out := make([]*v1.PoolDefinition, 0, len(defined))
	for _, pool := range defined {
		if pool.Cap > math.MaxInt32 {
			return nil, fmt.Errorf("pool %q: cap %d is too large", pool.Name, pool.Cap)
		}
		out = append(out, &v1.PoolDefinition{
			Name:               pool.Name,
			Host:               pool.Host,
			Cap:                int32(pool.Cap),
			Members:            pool.Members,
			Wake:               pool.Wake,
			WakeLimitSeconds:   int64(pool.WakeLimit / time.Second),
			Status:             pool.Status,
			StatusLimitSeconds: int64(pool.StatusLimit / time.Second),
			Admit:              pool.Admit,
			AdmitLimitSeconds:  int64(pool.AdmitLimit / time.Second),
		})
	}
	return out, nil
}

// refuseRun writes why the command did not run and returns the error that exits cli.ExitNoGrant.
func refuseRun(stderr io.Writer, err error) error {
	fmt.Fprintf(stderr, "orrery run: no grant: %v\n", err)
	return statusError{code: cli.ExitNoGrant}
}
