// Command orrery is the orrery CLI. It parses the global flags and dispatches each subcommand to its
// internal package.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/joshsymonds/orrery/internal/cli"
	"github.com/joshsymonds/orrery/internal/hook"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/summary"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// Exit codes.
const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2
	exitRefused = 3
)

// anthropicBaseURLEnv names the variable that carries the session's patchbay base URL.
const anthropicBaseURLEnv = "ANTHROPIC_BASE_URL"

const usageText = `usage: orrery [-L NAME | -S PATH] [--socket PATH] SUBCOMMAND [ARGS]

global flags, before the subcommand:
  -L NAME         tmux server by name (tmux -L)
  -S PATH         tmux server by socket path (tmux -S); not together with -L
  --socket PATH   orreryd socket (default ORRERY_SOCKET, then $XDG_RUNTIME_DIR/orrery/orreryd.sock)

subcommands:
  hook                                          forward a Claude Code hook event from stdin; exits 0
  list --json                                   print the panes of the tmux server as a JSON array
  seen --pane ID                                mark a pane seen
  send --pane ID [--expect-question H] (--text T | --keys K...)
                                                send text or keys to a pane; --keys must be last
  debug summarize --pane ID --transcript PATH [--claude PATH]
                                                write a pane's summary and label
`

// usageError is a command line refused before any work. It exits 2 and prints the usage text.
type usageError struct {
	msg string
}

func (e usageError) Error() string {
	return e.msg
}

// usagef returns a usageError with the formatted message.
func usagef(format string, args ...any) error {
	return usageError{msg: fmt.Sprintf(format, args...)}
}

// globals holds the flags that come before the subcommand.
type globals struct {
	server       tmuxctl.Server // -L and -S
	daemonSocket string         // --socket
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.Getenv))
}

// run executes one orrery command line and returns its exit code.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer, getenv func(string) string) int {
	opts, rest, err := parseGlobal(args)
	if err == nil && len(rest) > 0 {
		return report(stderr, dispatch(context.Background(), opts, rest, stdin, stdout, getenv))
	}
	// A hook must never fail a Claude Code turn. A refused command line that names hook exits 0 silently
	// and forwards nothing.
	if slices.Contains(args, "hook") {
		return exitOK
	}
	if err != nil {
		return report(stderr, err)
	}
	return report(stderr, usagef("no subcommand"))
}

// report prints err to stderr and returns the exit code it maps to. A nil err exits 0 silently.
func report(stderr io.Writer, err error) int {
	var usage usageError
	switch {
	case err == nil:
		return exitOK
	case errors.As(err, &usage):
		fmt.Fprintf(stderr, "orrery: %s\n%s", usage.msg, usageText)
		return exitUsage
	case errors.Is(err, cli.ErrQuestionRefused):
		fmt.Fprintf(stderr, "orrery: %v\n", err)
		return exitRefused
	default:
		fmt.Fprintf(stderr, "orrery: %v\n", err)
		return exitFailure
	}
}

// parseGlobal reads the flags before the subcommand and returns them with the remaining arguments.
func parseGlobal(args []string) (globals, []string, error) {
	flags := flag.NewFlagSet("orrery", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	name := flags.String("L", "", "tmux server name")
	socket := flags.String("S", "", "tmux server socket path")
	daemonSocket := flags.String("socket", "", "orreryd socket path")
	if err := flags.Parse(args); err != nil {
		return globals{}, nil, usagef("%v", err)
	}
	if *name != "" && *socket != "" {
		return globals{}, nil, usagef("-L and -S are mutually exclusive")
	}
	g := globals{server: tmuxctl.Server{Name: *name, Socket: *socket}, daemonSocket: *daemonSocket}
	return g, flags.Args(), nil
}

// dispatch runs the subcommand named by rest[0] with the arguments after it.
func dispatch(ctx context.Context, opts globals, rest []string, stdin io.Reader, stdout io.Writer,
	getenv func(string) string) error {
	name, args := rest[0], rest[1:]
	switch name {
	case "hook":
		socket, err := protocol.SocketPath(opts.daemonSocket, getenv)
		if err != nil {
			socket = ""
		}
		hook.Run(stdin, getenv, socket)
		return nil
	case "list", "seen", "send", "debug":
		client, err := tmuxctl.New(opts.server)
		if err != nil {
			return fmt.Errorf("tmux server: %w", err)
		}
		return runTmux(ctx, client, name, args, stdout, getenv)
	default:
		return usagef("unknown subcommand %q", name)
	}
}

// runTmux runs a subcommand that works on the tmux server client talks to.
func runTmux(ctx context.Context, client *tmuxctl.Client, name string, args []string, out io.Writer,
	getenv func(string) string) error {
	// The cli errors already name their subcommand, so errors.Join passes them through unchanged while
	// keeping errors.Is working for cli.ErrQuestionRefused.
	switch name {
	case "list":
		return errors.Join(cli.List(ctx, client, args, out))
	case "seen":
		return errors.Join(cli.Seen(ctx, client, args, out))
	case "send":
		if err := refuseSendFlagsAfterKeys(args); err != nil {
			return err
		}
		return errors.Join(cli.Send(ctx, client, args, out))
	default:
		return runDebug(ctx, client, args, getenv)
	}
}

// refuseSendFlagsAfterKeys refuses a send that names a send flag after --keys. Go flag parsing stops at the
// first key, so such a flag would be typed into the pane instead of applied.
func refuseSendFlagsAfterKeys(args []string) error {
	keysAt := -1
	for i, arg := range args {
		if name, isFlag := flagName(arg); isFlag && name == "keys" {
			keysAt = i
			break
		}
	}
	if keysAt < 0 {
		return nil
	}
	for _, arg := range args[keysAt+1:] {
		if name, isFlag := flagName(arg); isFlag && isSendFlag(name) {
			return usagef("send: %s after --keys; --keys must be last", arg)
		}
	}
	return nil
}

// flagName returns the name of arg when it spells a flag with one or two leading dashes, with or without =value.
func flagName(arg string) (string, bool) {
	trimmed, isFlag := strings.CutPrefix(arg, "-")
	if !isFlag {
		return "", false
	}
	trimmed = strings.TrimPrefix(trimmed, "-")
	name, _, _ := strings.Cut(trimmed, "=")
	return name, true
}

// isSendFlag reports whether name is a flag of `orrery send`.
func isSendFlag(name string) bool {
	switch name {
	case "pane", "text", "keys", "expect-question":
		return true
	default:
		return false
	}
}

// runDebug runs `orrery debug summarize`, the only debug subcommand.
func runDebug(ctx context.Context, client *tmuxctl.Client, args []string, getenv func(string) string) error {
	if len(args) == 0 || args[0] != "summarize" {
		return usagef("debug: want subcommand summarize")
	}
	return debugSummarize(ctx, client, args[1:], getenv)
}

// debugSummarize writes the pane's summary and label from one transcript, running the claude binary once.
func debugSummarize(ctx context.Context, client *tmuxctl.Client, args []string, getenv func(string) string) error {
	flags := flag.NewFlagSet("debug summarize", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	pane := flags.String("pane", "", "pane ID to summarize")
	transcript := flags.String("transcript", "", "session transcript path")
	claude := flags.String("claude", "claude", "claude binary to run")
	if err := flags.Parse(args); err != nil {
		return usagef("debug summarize: %v", err)
	}
	if flags.NArg() > 0 {
		return usagef("debug summarize: unexpected argument %q", flags.Arg(0))
	}
	if *pane == "" || *transcript == "" {
		return usagef("debug summarize: --pane and --transcript are required")
	}

	excerpt, err := summary.ReadExcerpt(*transcript, "")
	if err != nil {
		return fmt.Errorf("debug summarize: %w", err)
	}
	summarizer, err := summary.New(summary.Config{Claude: *claude, Tmux: client})
	if err != nil {
		return fmt.Errorf("debug summarize: %w", err)
	}
	job := summary.Job{
		PaneID:    *pane,
		BaseURL:   getenv(anthropicBaseURLEnv),
		Input:     excerpt.Text,
		WantLabel: true,
	}
	if err = summarizer.Summarize(ctx, job); err != nil {
		return fmt.Errorf("debug summarize: %w", err)
	}
	return nil
}
