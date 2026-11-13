package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/joshsymonds/orrery/internal/holder"
)

// runHolder runs `orrery holder --session NAME [--state-dir DIR] -- PATH [ARGS...]`. It runs PATH with ARGS as the
// session's held child, in the holder's working directory and environment, and serves the session's holder socket until
// the child ends. SIGINT or SIGTERM stops the child first. The session lives in the holders directory of the state
// directory DIR when --state-dir names one, and in the holder package's otherwise. A refusal by holder.Run writes its
// error to stderr and exits 1.
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
	return nil
}
