package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/joshsymonds/orrery/internal/holder"
)

// maxSignal is the highest signal number childStatus looks for. Linux numbers its real-time signals up to 64.
const maxSignal = 64

// runHolder runs `orrery holder --session NAME [--state-dir DIR] -- PATH [ARGS...]`. It runs PATH with ARGS as the
// session's held child, in the holder's working directory and environment, and serves the session's holder socket until
// the child ends. SIGINT or SIGTERM stops the child first. The session lives in the holders directory of the state
// directory DIR when --state-dir names one, and in the holder package's otherwise. A refusal by holder.Run writes its
// error to stderr and exits 1. When the child ended on its own, the holder exits with the child's status, as
// childStatus gives it.
func runHolder(ctx context.Context, args []string, stderr io.Writer, getenv func(string) string) error {
	flagArgs, argv, found := cutCommand(args)
	if !found || len(argv) == 0 {
		return usagef("holder: want --session NAME [--state-dir DIR] -- PATH [ARGS...]")
	}
	flags := flag.NewFlagSet("orrery holder", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	session := flags.String("session", "", "session the holder serves")
	stateDir := flags.String("state-dir", "", "state directory whose holders subdirectory holds the session")
	if err := flags.Parse(flagArgs); err != nil {
		return usagef("holder: %v", err)
	}
	if flags.NArg() > 0 {
		return usagef("holder: unexpected argument %q before --", flags.Arg(0))
	}
	if *session == "" {
		return usagef("holder: --session is required")
	}
	holders := filepath.Join(*stateDir, "holders")
	if *stateDir == "" {
		var err error
		holders, err = holder.HoldersDir(getenv)
		if err != nil {
			return fmt.Errorf("holder: %w", err)
		}
	}
	dir, err := holder.SessionDir(holders, *session)
	if err != nil {
		return usagef("holder: %v", err)
	}
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	err = holder.Run(ctx, holder.Config{
		Dir:     dir,
		Session: *session,
		Child:   holder.ChildConfig{Path: argv[0], Args: argv[1:]},
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
