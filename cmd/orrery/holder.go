package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/joshsymonds/orrery/internal/holder"
)

// maxSignal is the highest signal number childStatus looks for. Linux numbers its real-time signals up to 64.
const maxSignal = 64

// maxGateSeconds is the largest --gate-limit, in whole seconds, whose duration fits in a time.Duration.
const maxGateSeconds = math.MaxInt64 / int64(time.Second)

// runHolder runs `orrery holder --session NAME [--state-dir DIR] [--gate-limit SECONDS [--gate-tail BYTES]] -- PATH
// [ARGS...]`. It runs PATH with ARGS as the session's held child, in the holder's working directory and environment,
// and serves the session's holder socket until the child ends. SIGINT or SIGTERM stops the child first. The session
// lives in the holders directory of the state directory DIR when --state-dir names one, and in the holder package's
// otherwise. A refusal by holder.Run writes its error to stderr and exits 1. When the child ended on its own, the
// holder exits with the child's status, as childStatus gives it. A positive --gate-limit runs the child as a gate with
// that limit in seconds; a gate the limit stopped exits 0, as any child the holder stopped does. --gate-tail BYTES
// keeps that many of the gate's newest output bytes in its exit record, where the default is 8 KiB, and needs
// --gate-limit.
func runHolder(ctx context.Context, args []string, stderr io.Writer, getenv func(string) string) error {
	parsed, err := parseHolder(args)
	if err != nil {
		return err
	}
	holders := filepath.Join(parsed.stateDir, "holders")
	if parsed.stateDir == "" {
		holders, err = holder.HoldersDir(getenv)
		if err != nil {
			return fmt.Errorf("holder: %w", err)
		}
	}
	dir, err := holder.SessionDir(holders, parsed.session)
	if err != nil {
		return usagef("holder: %v", err)
	}
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	err = holder.Run(ctx, holder.Config{
		Dir:     dir,
		Session: parsed.session,
		Child: holder.ChildConfig{
			Path:      parsed.child[0],
			Args:      parsed.child[1:],
			GateLimit: parsed.gateLimit,
			GateTail:  parsed.gateTail,
		},
	})
	if err != nil {
		fmt.Fprintf(stderr, "orrery holder: %v\n", err)
		return statusError{code: exitFailure}
	}
	status, err := childStatus(dir)
	if err != nil {
		fmt.Fprintf(stderr, "orrery holder: %v\n", err)
		return statusError{code: exitFailure}
	}
	if status != exitOK {
		return statusError{code: status}
	}
	return nil
}

// holderArgs is the command line of orrery holder, as parseHolder reads it.
type holderArgs struct {
	session   string
	stateDir  string
	gateLimit time.Duration
	gateTail  int
	// child is PATH followed by its ARGS.
	child []string
}

// parseHolder reads the flags before the separator of orrery holder and takes the child's argv after it. Each refusal
// is a usage error.
func parseHolder(args []string) (holderArgs, error) {
	flagArgs, argv, found := cutCommand(args)
	if !found || len(argv) == 0 {
		return holderArgs{}, usagef(
			"holder: want --session NAME [--state-dir DIR] [--gate-limit SECONDS [--gate-tail BYTES]] -- PATH [ARGS...]",
		)
	}
	flags := flag.NewFlagSet("orrery holder", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	session := flags.String("session", "", "session the holder serves")
	stateDir := flags.String("state-dir", "", "state directory whose holders subdirectory holds the session")
	var gateLimit time.Duration
	flags.Func("gate-limit", "run the child as a gate limited to SECONDS", func(value string) error {
		seconds, err := strconv.ParseInt(value, 10, 64)
		if err != nil || seconds <= 0 || seconds > maxGateSeconds {
			return errors.New("want a positive whole number of seconds")
		}
		gateLimit = time.Duration(seconds) * time.Second
		return nil
	})
	var gateTail int
	flags.Func("gate-tail", "keep the gate's newest BYTES of output, from 1 to the maximum", func(value string) error {
		count, err := strconv.ParseInt(value, 10, 64)
		if err != nil || count <= 0 || count > holder.MaxGateTail {
			return fmt.Errorf("want a whole number of bytes from 1 to %d", holder.MaxGateTail)
		}
		gateTail = int(count)
		return nil
	})
	if err := flags.Parse(flagArgs); err != nil {
		return holderArgs{}, usagef("holder: %v", err)
	}
	if flags.NArg() > 0 {
		return holderArgs{}, usagef("holder: unexpected argument %q before --", flags.Arg(0))
	}
	if gateTail > 0 && gateLimit == 0 {
		return holderArgs{}, usagef("holder: --gate-tail needs --gate-limit")
	}
	if *session == "" {
		return holderArgs{}, usagef("holder: --session is required")
	}
	return holderArgs{
		session:   *session,
		stateDir:  *stateDir,
		gateLimit: gateLimit,
		gateTail:  gateTail,
		child:     argv,
	}, nil
}

// childStatus returns the status the holder exits with, read from the exit record in dir. A child the holder stopped
// exits 0. A child that ended on its own exits with its exit code, or with 128 plus the number of the signal that ended
// it.
func childStatus(dir string) (int, error) {
	data, err := os.ReadFile(filepath.Join(dir, holder.ExitName))
	if err != nil {
		return 0, fmt.Errorf("read exit record: %w", err)
	}
	var exit holder.Exit
	if err = json.Unmarshal(data, &exit); err != nil {
		return 0, fmt.Errorf("decode exit record: %w", err)
	}
	switch {
	case exit.Reason != holder.ReasonExited:
		return exitOK, nil
	case exit.Signal != "":
		num, sigErr := signalNumber(exit.Signal)
		if sigErr != nil {
			return 0, sigErr
		}
		return 128 + num, nil
	case exit.Code < 0:
		return 0, errors.New("child ended with no exit status")
	default:
		return exit.Code, nil
	}
}

// signalNumber returns the number of the signal that syscall.Signal.String names name.
func signalNumber(name string) (int, error) {
	for sig := syscall.Signal(1); sig <= maxSignal; sig++ {
		if sig.String() == name {
			return int(sig), nil
		}
	}
	return 0, fmt.Errorf("unknown signal %q", name)
}
