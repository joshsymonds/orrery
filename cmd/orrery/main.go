// Command orrery is the orrery CLI. It parses the global flags and dispatches each subcommand to its
// internal package.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/joshsymonds/orrery/internal/archive"
	"github.com/joshsymonds/orrery/internal/cli"
	"github.com/joshsymonds/orrery/internal/desk"
	"github.com/joshsymonds/orrery/internal/deskstream"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/help"
	"github.com/joshsymonds/orrery/internal/hook"
	"github.com/joshsymonds/orrery/internal/hoststatus"
	"github.com/joshsymonds/orrery/internal/open"
	"github.com/joshsymonds/orrery/internal/picker"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/push"
	"github.com/joshsymonds/orrery/internal/server"
	"github.com/joshsymonds/orrery/internal/shell"
	"github.com/joshsymonds/orrery/internal/stepmcp"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/summary"
	"github.com/joshsymonds/orrery/internal/tmuxconf"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
	"github.com/joshsymonds/orrery/internal/tui"
	"github.com/joshsymonds/orrery/internal/webui"
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

// Serve timeouts. Request headers must arrive within serveHeaderTimeout, and shutdown waits at most
// serveShutdownTimeout for requests in flight.
const (
	serveHeaderTimeout   = 10 * time.Second
	serveShutdownTimeout = 5 * time.Second
)

const usageText = `usage: orrery [-L NAME | -S PATH] [--socket PATH] SUBCOMMAND [ARGS]

global flags, before the subcommand:
  -L NAME         tmux server by name (tmux -L)
  -S PATH         tmux server by socket path (tmux -S); not together with -L
  --socket PATH   orreryd socket (default ORRERY_SOCKET, then $XDG_RUNTIME_DIR/orrery/orreryd.sock)

subcommands:
  hook                                          forward a Claude Code hook event from stdin; exits 0
  notices                                       print desk notices as JSON lines on stdout; relay activity from stdin
  desk --sound FILE [--host HOST]...            show each notice as a popup and play FILE with it, from this machine
                                                and each HOST's notices, until SIGINT or SIGTERM
  list --json                                   print the panes of the tmux server as a JSON array
  events --url URL [--after N] [--follow]       print the event stream of serve as JSON lines
  session [--url URL] VERB ...                  run a session VERB below through serve at URL, not orreryd's socket
  session start [--model MODEL] DIR PROMPT      start a headless session in DIR and print its id
  session say ID TEXT                           send TEXT to a session
  session answer ID REQUEST_ID ANSWER           answer a session's open question
  session interrupt ID                          stop a session's current turn
  session stop ID                               end a session's process
  session seen ID                               mark a session's conversation as read
  session watch ID                              print a session's events as JSON lines until it ends
  seen --pane ID [--focus]                      mark a pane seen
  send --pane ID [--expect-question H] (--text T | --keys K...)
                                                send text or keys to a pane; --keys must be last
  debug summarize --pane ID --transcript PATH [--claude PATH]
                                                write a pane's summary and label
  shell --dir DIR --client TTY                  open DIR's shell popup on the client at TTY
  tree --client TTY                             show the pane tree on the client at TTY; N picks a pane
  open [--remote] [--intent TEXT] [ARGS...]     switch to the plan for ARGS, or to the picker; attach outside tmux
  tmux-conf                                     print the orrery tmux plugin for this binary
  help [--html]                                 print the help card for the tmux server, or the HTML page with --html
  mcp                                           serve the step tools over MCP on stdio until stdin closes
  serve --listen ADDR --allow-login LOGIN [--allow-login LOGIN]... [--push-host HOST]...
        [--host-status-cmd PATH]
                                                serve the HTTP API on a loopback address until SIGINT or SIGTERM
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
func run(args []string, stdin *os.File, stdout, stderr io.Writer, getenv func(string) string) int {
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
	case errors.Is(err, cli.ErrUsage):
		fmt.Fprintf(stderr, "orrery: %v\n%s", err, usageText)
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
func dispatch(ctx context.Context, opts globals, rest []string, stdin *os.File, stdout io.Writer,
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
	case "tmux-conf":
		return tmuxConf(args, stdout)
	case "help":
		return helpCommand(ctx, opts.server, args, stdout, getenv)
	case "open":
		client, err := tmuxctl.New(opts.server)
		if err != nil {
			return fmt.Errorf("tmux server: %w", err)
		}
		return openCommand(ctx, client, opts.server, args, stdin, stdout, getenv)
	case "serve":
		return runServe(ctx, opts, args, getenv)
	case "events":
		return errors.Join(cli.Events(ctx, args, stdout))
	case "notices":
		return runNotices(ctx, opts, args, stdin, stdout, getenv)
	case "desk":
		return runDesk(ctx, opts, args)
	case "mcp":
		return runMCP(ctx, opts, args, stdin, stdout, getenv)
	case "session":
		return runSession(ctx, opts, args, stdout, getenv)
	case "list", "seen", "send", "debug", "shell", "tree":
		client, err := tmuxctl.New(opts.server)
		if err != nil {
			return fmt.Errorf("tmux server: %w", err)
		}
		return runTmux(ctx, client, name, args, stdin, stdout, getenv, seenReporter(opts.daemonSocket, getenv))
	default:
		return usagef("unknown subcommand %q", name)
	}
}

// runSession runs `orrery session` with the session socket beside the daemon socket, unless the command names serve
// with --url.
func runSession(ctx context.Context, opts globals, args []string, stdout io.Writer, getenv func(string) string) error {
	sessionSocket := ""
	if !sessionViaURL(args) {
		socket, err := protocol.SocketPath(opts.daemonSocket, getenv)
		if err != nil {
			return fmt.Errorf("session: %w", err)
		}
		sessionSocket = protocol.SessionSocketPath(socket)
	}
	return errors.Join(cli.Session(ctx, sessionSocket, args, stdout, getenv))
}

// runNotices runs `orrery notices` against the notices socket beside the daemon socket.
func runNotices(ctx context.Context, opts globals, args []string, stdin io.Reader, stdout io.Writer,
	getenv func(string) string) error {
	socket, err := protocol.SocketPath(opts.daemonSocket, getenv)
	if err != nil {
		return fmt.Errorf("notices: %w", err)
	}
	return errors.Join(cli.Notices(ctx, deskstream.SocketPath(socket), args, stdin, stdout))
}

// runDesk runs `orrery desk`. It keeps this machine's notice stream and each --host's open, shows each notice as a
// popup that plays --sound, and returns nil on SIGINT or SIGTERM once the streams have closed.
func runDesk(ctx context.Context, opts globals, args []string) error {
	flags := flag.NewFlagSet("orrery desk", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	sound := flags.String("sound", "", "sound file played with each popup")
	var hosts listFlag
	flags.Var(&hosts, "host", "host whose notice stream the desk keeps, repeatable")
	if err := flags.Parse(args); err != nil {
		return usagef("desk: %v", err)
	}
	if flags.NArg() > 0 {
		return usagef("desk: unexpected argument %q", flags.Arg(0))
	}
	if *sound == "" {
		return usagef("desk: --sound is required")
	}
	tmux, err := tmuxctl.New(opts.server)
	if err != nil {
		return fmt.Errorf("tmux server: %w", err)
	}
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("desk: %w", err)
	}
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	err = desk.Run(ctx, desk.Config{
		Orrery: executable,
		Socket: opts.daemonSocket,
		Hosts:  hosts,
		Sound:  *sound,
		Input: func(ctx context.Context) (int64, bool, error) {
			return desk.NewestInput(ctx, tmux)
		},
		Clock:  desk.SystemClock{},
		Logger: slog.New(slog.NewTextHandler(os.Stderr, nil)),
	})
	if err != nil {
		return fmt.Errorf("desk: %w", err)
	}
	return nil
}

// sessionViaURL reports whether a session command line names serve with --url, which needs no daemon socket.
func sessionViaURL(args []string) bool {
	return len(args) > 0 && args[0] == "--url"
}

// listFlag is a repeatable string flag. It collects the values in the order given.
type listFlag []string

// String implements flag.Value.
func (l *listFlag) String() string {
	return strings.Join(*l, ",")
}

// Set implements flag.Value.
func (l *listFlag) Set(value string) error {
	*l = append(*l, value)
	return nil
}

// openServeStores returns the push store that registration writes and the reader of orreryd's store file that
// List reads. Both resolve their paths from getenv.
func openServeStores(getenv func(string) string, pushHosts []string) (*push.Store, *store.Reader, error) {
	storePath, err := push.DefaultStorePath(getenv)
	if err != nil {
		return nil, nil, fmt.Errorf("push store: %w", err)
	}
	pushStore, err := push.NewStore(storePath, pushHosts)
	if err != nil {
		return nil, nil, fmt.Errorf("push store: %w", err)
	}
	dbPath, err := store.Path(getenv)
	if err != nil {
		return nil, nil, fmt.Errorf("store: %w", err)
	}
	return pushStore, store.NewReader(dbPath), nil
}

// runMCP runs `orrery mcp`. It serves the step tools over MCP on stdin and stdout until stdin closes. The step
// service is the one on the session socket beside the daemon socket, so the command opens no listener and no store.
func runMCP(ctx context.Context, opts globals, args []string, stdin io.Reader, stdout io.Writer,
	getenv func(string) string) error {
	if len(args) > 0 {
		return usagef("mcp takes no arguments")
	}
	socket, err := protocol.SocketPath(opts.daemonSocket, getenv)
	if err != nil {
		return fmt.Errorf("mcp: %w", err)
	}
	sessionSocket := protocol.SessionSocketPath(socket)
	steps := enginev1connect.NewStepsClient(cli.SessionHTTPClient(sessionSocket), cli.SessionBaseURL)
	server, err := stepmcp.NewServer(steps)
	if err != nil {
		return fmt.Errorf("mcp: %w", err)
	}
	if err := server.Serve(ctx, stdin, stdout); err != nil {
		return fmt.Errorf("mcp: %w", err)
	}
	return nil
}

// runServe runs `orrery serve`. It serves the HTTP API for the tmux server named by opts on the loopback address
// --listen until SIGINT or SIGTERM, then shuts down and returns nil.
func runServe(ctx context.Context, opts globals, args []string, getenv func(string) string) error {
	flags := flag.NewFlagSet("orrery serve", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	listen := flags.String("listen", "", "loopback address to serve on")
	var logins listFlag
	flags.Var(&logins, "allow-login", "Tailscale login the API admits, repeatable")
	var pushHosts listFlag
	flags.Var(&pushHosts, "push-host", "push relay host a subscription may name, repeatable")
	var hostStatus *hoststatus.Runner
	flags.Func("host-status-cmd", "absolute path of the command Host.Status runs", func(value string) error {
		if !filepath.IsAbs(value) {
			return errors.New("host status command must be an absolute path")
		}
		hostStatus = hoststatus.New(value, time.Now)
		return nil
	})
	if err := flags.Parse(args); err != nil {
		return usagef("serve: %v", err)
	}
	if flags.NArg() > 0 {
		return usagef("serve: unexpected argument %q", flags.Arg(0))
	}
	if *listen == "" {
		return usagef("serve: --listen is required")
	}
	if len(logins) == 0 {
		return usagef("serve: at least one --allow-login is required")
	}
	if len(pushHosts) == 0 {
		pushHosts = listFlag{push.DefaultPushHost}
	}
	tmux, err := tmuxctl.New(opts.server)
	if err != nil {
		return fmt.Errorf("tmux server: %w", err)
	}
	pushStore, sessions, err := openServeStores(getenv, pushHosts)
	if err != nil {
		return err
	}
	var engine enginev1connect.EngineClient
	if daemonSocket, sockErr := protocol.SocketPath(opts.daemonSocket, getenv); sockErr == nil {
		engine = enginev1connect.NewEngineClient(
			cli.SessionHTTPClient(protocol.SessionSocketPath(daemonSocket)), cli.SessionBaseURL)
	}
	shutdown := make(chan struct{})
	handler, err := server.New(server.Config{
		Logins:     logins,
		Tmux:       tmux,
		Push:       pushStore,
		HostStatus: hostStatus,
		Sessions:   sessions,
		Shutdown:   shutdown,
		Engine:     engine,
		Home:       getenv("HOME"),
		Picker:     servePickerConfig(tmux, getenv),
		ReportSeen: seenReporter(opts.daemonSocket, getenv),
		Web:        webui.FS(),
	})
	if err != nil {
		return fmt.Errorf("serve: %w", err)
	}

	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	ln, err := server.Listen(ctx, *listen)
	if err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	return serveUntilDone(ctx, ln, handler, shutdown)
}

// serveUntilDone serves handler on ln until ctx is done, then shuts down and returns nil. It stops the server only
// when Shutdown returns, which waits for open streams, so it closes shutdown first to end every open Watch stream.
func serveUntilDone(ctx context.Context, ln net.Listener, handler http.Handler, shutdown chan struct{}) error {
	httpServer := &http.Server{Handler: handler, ReadHeaderTimeout: serveHeaderTimeout}
	serving := make(chan error, 1)
	go func() { serving <- httpServer.Serve(ln) }()
	select {
	case serveErr := <-serving:
		return fmt.Errorf("serve: %w", serveErr)
	case <-ctx.Done():
	}
	close(shutdown)
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), serveShutdownTimeout)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("serve: shutdown: %w", err)
	}
	return nil
}

// seenReporter returns the reporter that tells the daemon socket protocol.SocketPath resolves about a pane's seen
// time. It returns nil when no socket resolves, so `seen` still marks the pane.
func seenReporter(flagSocket string, getenv func(string) string) cli.SeenReporter {
	socket, err := protocol.SocketPath(flagSocket, getenv)
	if err != nil {
		return nil
	}
	return func(pane string, seen int64) error {
		return hook.ReportSeen(socket, pane, seen)
	}
}

// runTmux runs a subcommand that works on the tmux server client talks to. report is the seen reporter for seen.
func runTmux(ctx context.Context, client *tmuxctl.Client, name string, args []string, stdin *os.File,
	out io.Writer, getenv func(string) string, report cli.SeenReporter) error {
	// The cli errors already name their subcommand, so errors.Join passes them through unchanged while
	// keeping errors.Is working for cli.ErrQuestionRefused.
	switch name {
	case "list":
		return errors.Join(cli.List(ctx, client, args, out))
	case "seen":
		return errors.Join(cli.Seen(ctx, client, report, args, out))
	case "send":
		if err := refuseSendFlagsAfterKeys(args); err != nil {
			return err
		}
		return errors.Join(cli.Send(ctx, client, args, out))
	case "shell":
		return shellOpen(ctx, client, args)
	case "tree":
		return tree(ctx, client, args, stdin, out, getenv, report)
	default:
		return runDebug(ctx, client, args, getenv)
	}
}

// shellOpen runs `orrery shell`, which opens the popup shell for one directory on the client at one tty.
func shellOpen(ctx context.Context, client *tmuxctl.Client, args []string) error {
	flags := flag.NewFlagSet("shell", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	dir := flags.String("dir", "", "directory the shell session starts in")
	clientTTY := flags.String("client", "", "tty of the client that shows the popup")
	if err := flags.Parse(args); err != nil {
		return usagef("shell: %v", err)
	}
	if flags.NArg() > 0 {
		return usagef("shell: unexpected argument %q", flags.Arg(0))
	}
	if *dir == "" || *clientTTY == "" {
		return usagef("shell: --dir and --client are required")
	}
	return errors.Join(shell.Open(ctx, client, *dir, *clientTTY))
}

// tree runs `orrery tree`, which shows the pane tree on the client at --client. N on a row runs the picker for that
// row's directory, and the picker's choice switches the same client.
func tree(ctx context.Context, client *tmuxctl.Client, args []string, stdin *os.File, stdout io.Writer,
	getenv func(string) string, report cli.SeenReporter) error {
	flags := flag.NewFlagSet("tree", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	clientTTY := flags.String("client", "", "tty of the client that shows the tree")
	if err := flags.Parse(args); err != nil {
		return usagef("tree: %v", err)
	}
	if flags.NArg() > 0 {
		return usagef("tree: unexpected argument %q", flags.Arg(0))
	}
	if *clientTTY == "" {
		return usagef("tree: --client is required")
	}
	archivePath, err := archive.Path(getenv)
	if err != nil {
		return fmt.Errorf("tree: %w", err)
	}
	host, err := os.Hostname()
	if err != nil {
		return fmt.Errorf("tree: hostname: %w", err)
	}
	panes := func(ctx context.Context) ([]protocol.Pane, error) {
		all, listErr := client.ListPanes(ctx)
		if listErr != nil {
			return nil, fmt.Errorf("list panes: %w", listErr)
		}
		for i := range all {
			all[i].Host = host
		}
		return all, nil
	}

	result, err := tui.Run(ctx, tui.Options{Client: client, ClientTTY: *clientTTY, ArchivePath: archivePath,
		ReportSeen: report, Panes: panes, Input: stdin, Output: stdout})
	if err != nil {
		return fmt.Errorf("tree: %w", err)
	}
	if result.Action != tui.ActionIntent {
		return nil
	}
	cfg, err := openConfig(client, getenv)
	if err != nil {
		return fmt.Errorf("tree: %w", err)
	}
	target, chosen, err := picker.Run(ctx, picker.Options{Config: cfg, Dir: result.Dir, Input: stdin, Output: stdout})
	if err != nil {
		return fmt.Errorf("tree: %w", err)
	}
	if !chosen {
		return nil
	}
	if err = switchClient(ctx, client, *clientTTY, target); err != nil {
		return fmt.Errorf("tree: %w", err)
	}
	return nil
}

// openCommand runs `orrery open`. It runs the plan for args, or the picker when args name neither a session nor a
// directory, or when --intent TEXT is set. The chosen target is then shown on the invoking client.
func openCommand(ctx context.Context, client *tmuxctl.Client, server tmuxctl.Server, args []string, stdin *os.File,
	stdout io.Writer, getenv func(string) string) error {
	flags := flag.NewFlagSet("open", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	remote := flags.Bool("remote", false, "the current directory is on another host")
	intent := flags.String("intent", "", "text the picker ranks actions for, in place of ARGS")
	if err := flags.Parse(args); err != nil {
		return usagef("open: %v", err)
	}
	intentSet := false
	flags.Visit(func(f *flag.Flag) {
		intentSet = intentSet || f.Name == "intent"
	})
	if intentSet && flags.NArg() > 0 {
		return usagef("open: --intent takes no ARGS")
	}
	cfg, err := openConfig(client, getenv)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("open: working directory: %w", err)
	}
	req := open.Request{Args: flags.Args(), Cwd: cwd, Remote: *remote}
	target, chosen, err := openChoice(ctx, cfg, req, *intent, intentSet, stdin, stdout)
	if err != nil {
		return err
	}
	if !chosen {
		return nil
	}
	return showTarget(ctx, client, server, stdin, getenv, target)
}

// openChoice returns the target for req and whether one was chosen. With --intent, and with a plan that is text,
// the picker runs for the text. A plan for an existing directory with no session shows its resume actions in the
// picker. Otherwise the plan runs and its target is chosen.
func openChoice(ctx context.Context, cfg open.Config, req open.Request, intent string, intentSet bool,
	stdin *os.File, stdout io.Writer) (open.Target, bool, error) {
	if intentSet {
		return pick(ctx, cfg, intent, stdin, stdout)
	}
	plan, err := open.Resolve(ctx, cfg, req)
	if err != nil {
		return open.Target{}, false, fmt.Errorf("plan: %w", err)
	}
	switch {
	case plan.Text != "":
		return pick(ctx, cfg, plan.Text, stdin, stdout)
	case plan.Dir != "" && !plan.MakeDir:
		return resume(ctx, cfg, plan.Dir, stdin, stdout)
	}
	target, err := open.Execute(ctx, cfg, plan)
	if err != nil {
		return open.Target{}, false, fmt.Errorf("plan: %w", err)
	}
	return target, true, nil
}

// resume shows the resume actions for dir in the picker and returns the target it chose, if any.
func resume(ctx context.Context, cfg open.Config, dir string, stdin *os.File,
	stdout io.Writer) (open.Target, bool, error) {
	actions, err := open.ResumeActions(cfg, dir)
	if err != nil {
		return open.Target{}, false, fmt.Errorf("plan: %w", err)
	}
	target, chosen, err := picker.Choose(ctx, picker.Options{Config: cfg, Input: stdin, Output: stdout}, actions)
	if err != nil {
		return open.Target{}, false, fmt.Errorf("open: %w", err)
	}
	return target, chosen, nil
}

// pick runs the picker for text and returns the target it chose, if any.
func pick(ctx context.Context, cfg open.Config, text string, stdin *os.File,
	stdout io.Writer) (open.Target, bool, error) {
	target, chosen, err := picker.Run(ctx, picker.Options{Config: cfg, Text: text, Input: stdin, Output: stdout})
	if err != nil {
		return open.Target{}, false, fmt.Errorf("open: %w", err)
	}
	return target, chosen, nil
}

// showTarget puts the invoking client on target. Inside tmux that client is the one whose tty tmux reports, and it
// is switched. Outside tmux the process's terminal is attached to target. When target sets Tree, the tree opens on
// that client.
func showTarget(ctx context.Context, client *tmuxctl.Client, server tmuxctl.Server, stdin *os.File,
	getenv func(string) string, target open.Target) error {
	if getenv("TMUX") == "" {
		return attachTarget(ctx, client, server, stdin, target)
	}
	tty, err := client.Run(ctx, "display-message", "-p", "#{client_tty}")
	if err != nil {
		return fmt.Errorf("open: client tty: %w", err)
	}
	clientTTY := strings.TrimSpace(tty)
	if clientTTY == "" {
		return errors.New("open: tmux reports no client tty")
	}
	if err = switchClient(ctx, client, clientTTY, target); err != nil {
		return fmt.Errorf("open: %w", err)
	}
	if target.Tree {
		return openTree(ctx, client, clientTTY)
	}
	return nil
}

// attachTarget attaches the process's terminal to target on server. tmux needs the terminal's own descriptors,
// so the standard streams are the process's. When target sets Tree, the tree opens on the attached client.
func attachTarget(ctx context.Context, client *tmuxctl.Client, server tmuxctl.Server, stdin *os.File,
	target open.Target) error {
	argv := append(serverFlags(server), "attach-session", "-t", targetRef(target))
	if target.Tree {
		return attachWithTree(ctx, client, argv, stdin)
	}
	if err := attachCommand(ctx, argv, stdin).Run(); err != nil {
		return fmt.Errorf("open: attach: %w", err)
	}
	return nil
}

// attachCommand returns the tmux command that runs argv on the process's terminal.
func attachCommand(ctx context.Context, argv []string, stdin *os.File) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "tmux", argv...)
	cmd.Stdin = stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd
}

// attachWithTree runs the tmux command argv on the process's terminal. tmux lists the attached client only once the
// attach runs, so the tree opens on that client while the attach is still running. The function returns when the
// attach ends.
func attachWithTree(ctx context.Context, client *tmuxctl.Client, argv []string, stdin *os.File) error {
	tty, err := terminalTTY(ctx, stdin)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	attachCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := attachCommand(attachCtx, argv, stdin)
	if err = cmd.Start(); err != nil {
		return fmt.Errorf("open: attach: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	ticker := time.NewTicker(attachPoll)
	defer ticker.Stop()
	for listed := false; !listed; {
		select {
		case err = <-done:
			if err == nil {
				return errors.New("open: attach ended before tmux listed the client")
			}
			return fmt.Errorf("open: attach: %w", err)
		case <-ticker.C:
			if listed, err = clientListed(attachCtx, client, tty); err != nil {
				return fmt.Errorf("open: %w", err)
			}
		}
	}
	if treeErr := openTree(attachCtx, client, tty); treeErr != nil {
		return treeErr
	}
	if attachErr := <-done; attachErr != nil {
		return fmt.Errorf("open: attach: %w", attachErr)
	}
	return nil
}

// attachPoll is how often attachWithTree asks tmux for the attached client.
const attachPoll = 20 * time.Millisecond

// terminalTTY returns the tty of the terminal stdin is.
func terminalTTY(ctx context.Context, stdin *os.File) (string, error) {
	cmd := exec.CommandContext(ctx, "tty")
	cmd.Stdin = stdin
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("terminal tty: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// clientListed reports whether tmux lists the client at tty.
func clientListed(ctx context.Context, client *tmuxctl.Client, tty string) (bool, error) {
	out, err := client.Run(ctx, "list-clients", "-F", "#{client_tty}")
	if err != nil {
		return false, fmt.Errorf("list clients: %w", err)
	}
	return slices.Contains(strings.Fields(out), tty), nil
}

// openTree shows the tree in a popup on the client at tty, as the plugin's Space binding does. run-shell -b returns
// at once, so the open command does not wait for the popup. The popup runs this binary. Its command is one shell word,
// quoted for the shell and then for tmux, so that no path or tty is split or expanded.
func openTree(ctx context.Context, client *tmuxctl.Client, tty string) error {
	orrery, err := os.Executable()
	if err != nil {
		return fmt.Errorf("open tree: locate orrery binary: %w", err)
	}
	popup := "display-popup -c " + quoteTmux(tty) + " -E " +
		quoteTmux(quoteShell(orrery)+" tree --client "+quoteShell(tty))
	if _, err = client.Run(ctx, "run-shell", "-b", "-C", popup); err != nil {
		return fmt.Errorf("open tree: %w", err)
	}
	return nil
}

// quoteShell returns value as one single-quoted shell word.
func quoteShell(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

// quoteTmux returns value as one single-quoted tmux word. Single quotes are literal in tmux, so a single quote in
// value is written as '"'"'. Every "#" is doubled, because run-shell -C format-expands its command.
func quoteTmux(value string) string {
	return "'" + strings.ReplaceAll(strings.ReplaceAll(value, "#", "##"), "'", `'"'"'`) + "'"
}

// serverFlags returns the tmux flags that select server.
func serverFlags(server tmuxctl.Server) []string {
	switch {
	case server.Name != "":
		return []string{"-L", server.Name}
	case server.Socket != "":
		return []string{"-S", server.Socket}
	default:
		return nil
	}
}

// servePickerConfig returns the Picker serve's server uses: each ask resolves the picker's configuration with openConfig
// for client and getenv, so a missing zoxide or claude fails that ask, not serve's start.
func servePickerConfig(client *tmuxctl.Client, getenv func(string) string) func() (open.Config, error) {
	return func() (open.Config, error) { return openConfig(client, getenv) }
}

// openConfig returns the picker's configuration: zoxide and claude as absolute paths found on orrery's PATH, $HOME/Personal
// as the default root of new projects, $HOME as the directory the intent lookup names relative to, the directory trees
// of the roots file, and the archive of closed Claude panes.
func openConfig(client *tmuxctl.Client, getenv func(string) string) (open.Config, error) {
	zoxide, err := absLookPath("zoxide")
	if err != nil {
		return open.Config{}, err
	}
	claude, err := absLookPath("claude")
	if err != nil {
		return open.Config{}, err
	}
	archivePath, err := archive.Path(getenv)
	if err != nil {
		return open.Config{}, fmt.Errorf("archive path: %w", err)
	}
	home := getenv("HOME")
	roots, err := open.ReadRoots(open.RootsPath(getenv), home)
	if err != nil {
		return open.Config{}, fmt.Errorf("roots: %w", err)
	}
	return open.Config{
		Tmux:    client,
		Zoxide:  zoxide,
		Claude:  claude,
		Root:    filepath.Join(home, "Personal"),
		Home:    home,
		Roots:   roots,
		Archive: archivePath,
	}, nil
}

// absLookPath returns the absolute path of the binary name on orrery's PATH.
func absLookPath(name string) (string, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("find %s: %w", name, err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("absolute path of %s: %w", name, err)
	}
	return abs, nil
}

// switchClient switches the client at tty to target.
func switchClient(ctx context.Context, client *tmuxctl.Client, tty string, target open.Target) error {
	if _, err := client.Run(ctx, "switch-client", "-c", tty, "-t", targetRef(target)); err != nil {
		return fmt.Errorf("switch client: %w", err)
	}
	return nil
}

// targetRef returns the tmux target that selects target: its pane when it names one, else its session's current pane.
func targetRef(target open.Target) string {
	if target.Pane != "" {
		return target.Pane
	}
	return "=" + target.Session + ":"
}

// tmuxConf runs `orrery tmux-conf`, which prints the orrery tmux plugin for this binary's own path. It takes no
// flags and needs no tmux server.
func tmuxConf(args []string, stdout io.Writer) error {
	if len(args) > 0 {
		return usagef("tmux-conf: unexpected argument %q", args[0])
	}
	orrery, err := os.Executable()
	if err != nil {
		return fmt.Errorf("tmux-conf: locate orrery binary: %w", err)
	}
	if err = tmuxconf.Print(stdout, orrery); err != nil {
		return fmt.Errorf("tmux-conf: %w", err)
	}
	return nil
}

// helpCommand runs `orrery help`, which writes the help card for the tmux server's prefix, or the HTML page with
// --html. The card is colored only when stdout is a terminal and NO_COLOR is unset or empty. It reads the server and
// never starts one, so an unstarted server gives an empty prefix.
func helpCommand(ctx context.Context, server tmuxctl.Server, args []string, stdout io.Writer,
	getenv func(string) string) error {
	flags := flag.NewFlagSet("help", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	html := flags.Bool("html", false, "write the HTML page instead of the card")
	if err := flags.Parse(args); err != nil {
		return usagef("help: %v", err)
	}
	if flags.NArg() > 0 {
		return usagef("help: unexpected argument %q", flags.Arg(0))
	}
	client, err := tmuxctl.New(server)
	if err != nil {
		return fmt.Errorf("tmux server: %w", err)
	}
	prefix := help.Prefix(ctx, client)
	if *html {
		if err = help.Page(stdout, prefix); err != nil {
			return fmt.Errorf("help: %w", err)
		}
		return nil
	}
	card := help.Card
	if getenv("NO_COLOR") == "" && isTerminal(stdout) {
		card = help.ColorCard
	}
	if err = card(stdout, prefix); err != nil {
		return fmt.Errorf("help: %w", err)
	}
	return nil
}

// isTerminal reports whether out is a character device, such as a terminal. Anything that is not an *os.File, or
// whose Stat fails, is not a terminal.
func isTerminal(out io.Writer) bool {
	file, ok := out.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
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
