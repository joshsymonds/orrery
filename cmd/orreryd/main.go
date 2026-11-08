// Command orreryd is the orrery daemon. It listens on a unix socket for hook frames, applies each one to the pane
// it names, starts summaries, and posts waiting and done notifications through ntfy.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/joshsymonds/orrery/internal/autoseen"
	"github.com/joshsymonds/orrery/internal/daemon"
	"github.com/joshsymonds/orrery/internal/ntfy"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/summary"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// Exit codes.
const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2
)

// Hook JSON keys the summarizer reads. Claude Code writes them in snake case.
const (
	keyTranscriptPath       = "transcript_path"
	keyLastAssistantMessage = "last_assistant_message"
)

const (
	defaultClaude = "claude"
	// ntfyTimeout bounds each notification request.
	ntfyTimeout = 10 * time.Second
	// waitingBody and doneBody stand in for an empty question or summary.
	waitingBody = "waiting for input"
	doneBody    = "done"
)

const usageText = `usage: orreryd [-L NAME | -S PATH] [--socket PATH] [--claude PATH] [--summary-model CONTEXT=MODEL]...

flags:
  -L NAME           tmux server by name (tmux -L)
  -S PATH           tmux server by socket path (tmux -S); not together with -L
  --socket PATH     daemon socket (default ORRERY_SOCKET, then $XDG_RUNTIME_DIR/orrery/orreryd.sock)
  --claude PATH     claude binary the summarizer runs (default claude)
  --summary-model CONTEXT=MODEL
                    summary model for a patchbay context; repeat for each context
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

// options holds the parsed command line.
type options struct {
	server tmuxctl.Server
	socket string
	claude string
	models map[string]string
}

// modelFlag is the repeatable --summary-model flag. It maps each patchbay context to its summary model.
type modelFlag map[string]string

// String implements flag.Value.
func (m modelFlag) String() string {
	return ""
}

// Set implements flag.Value. It splits value at its first "=" and refuses an empty side or a repeated context.
func (m modelFlag) Set(value string) error {
	name, model, found := strings.Cut(value, "=")
	if !found || name == "" || model == "" {
		return fmt.Errorf("want CONTEXT=MODEL, got %q", value)
	}
	if _, repeated := m[name]; repeated {
		return fmt.Errorf("context %q given twice", name)
	}
	m[name] = model
	return nil
}

func main() {
	os.Exit(run(os.Args[1:], os.Stderr, os.Getenv))
}

// run starts orreryd with the command line args and returns its exit code. It exits 0 after SIGINT or SIGTERM.
func run(args []string, stderr io.Writer, getenv func(string) string) int {
	opts, err := parseFlags(args, getenv)
	if err != nil {
		return report(stderr, err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	logger := slog.New(slog.NewTextHandler(stderr, nil))
	return report(stderr, serve(ctx, opts, logger, getenv))
}

// report prints err to stderr and returns the exit code it maps to. A nil err exits 0 silently.
func report(stderr io.Writer, err error) int {
	var usage usageError
	switch {
	case err == nil:
		return exitOK
	case errors.As(err, &usage):
		fmt.Fprintf(stderr, "orreryd: %s\n%s", usage.msg, usageText)
		return exitUsage
	default:
		fmt.Fprintf(stderr, "orreryd: %v\n", err)
		return exitFailure
	}
}

// parseFlags reads the command line and resolves the daemon socket. Every error it returns is a usage error.
func parseFlags(args []string, getenv func(string) string) (options, error) {
	flags := flag.NewFlagSet("orreryd", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	tmuxName := flags.String("L", "", "tmux server name")
	tmuxSocket := flags.String("S", "", "tmux server socket path")
	daemonSocket := flags.String("socket", "", "daemon socket path")
	claude := flags.String("claude", defaultClaude, "claude binary to run")
	models := modelFlag{}
	flags.Var(models, "summary-model", "summary model as CONTEXT=MODEL")
	if err := flags.Parse(args); err != nil {
		return options{}, usagef("%v", err)
	}
	if flags.NArg() > 0 {
		return options{}, usagef("unexpected argument %q", flags.Arg(0))
	}
	if *tmuxName != "" && *tmuxSocket != "" {
		return options{}, usagef("-L and -S are mutually exclusive")
	}
	if *claude == "" {
		return options{}, usagef("--claude is empty")
	}
	socket, err := protocol.SocketPath(*daemonSocket, getenv)
	if err != nil {
		return options{}, usagef("%v", err)
	}
	return options{
		server: tmuxctl.Server{Name: *tmuxName, Socket: *tmuxSocket},
		socket: socket,
		claude: *claude,
		models: models,
	}, nil
}

// serve listens on the daemon socket until ctx is done, then waits for the summaries and notifications it started.
func serve(ctx context.Context, opts options, logger *slog.Logger, getenv func(string) string) error {
	host, err := os.Hostname()
	if err != nil {
		return fmt.Errorf("host name: %w", err)
	}
	if host == "" {
		return errors.New("host name is empty")
	}
	publisher, err := ntfy.New(getenv, host, &http.Client{Timeout: ntfyTimeout})
	if err != nil && !errors.Is(err, ntfy.ErrNotConfigured) {
		return fmt.Errorf("ntfy: %w", err)
	}
	ln, err := daemon.Listen(opts.socket)
	if err != nil {
		return fmt.Errorf("daemon socket: %w", err)
	}

	hub := &relay{
		claude:      opts.claude,
		models:      opts.models,
		logger:      logger,
		publisher:   publisher,
		summarizers: make(map[tmuxctl.Server]*summary.Summarizer),
	}
	observe := func(transition daemon.Transition) { hub.observe(ctx, transition) }
	serveErr := daemon.Serve(ctx, ln, daemon.Config{
		Server:    opts.server,
		Observers: []daemon.Observer{observe, autoseen.New(time.Now, logger)},
		Logger:    logger,
	})
	hub.wait()
	if serveErr != nil {
		return fmt.Errorf("serve: %w", serveErr)
	}
	return nil
}

// relay turns each state change into summary requests and notifications. Its observe method runs under the
// daemon's lock, so it starts work and returns without waiting on HTTP. On SessionEnd it waits on tmux for the
// summarizer to forget the pane, so no run started before SessionEnd writes after the pane's next session begins.
type relay struct {
	claude      string
	models      map[string]string
	logger      *slog.Logger
	publisher   *ntfy.Publisher // nil when ntfy is off
	summarizers map[tmuxctl.Server]*summary.Summarizer
	notifying   sync.WaitGroup // one count per notification in flight
}

// hookString returns the string value of key in the hook JSON, or "" when key is absent or null.
func hookString(hook json.RawMessage, key string) (string, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(hook, &fields); err != nil {
		return "", fmt.Errorf("decode hook: %w", err)
	}
	raw, ok := fields[key]
	if !ok {
		return "", nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", fmt.Errorf("hook field %s: %w", key, err)
	}
	return value, nil
}

// observe requests a summary for a pane that starts working or finishes, posts a notification when it waits for
// input or finishes, and forgets the pane's summary when its session ends.
func (r *relay) observe(ctx context.Context, transition daemon.Transition) {
	if transition.To == "" {
		r.forget(ctx, transition)
	}
	if transition.To == protocol.StateWorking || transition.To == protocol.StateDone {
		r.summarize(ctx, transition)
	}
	if r.publisher != nil && (transition.To == protocol.StateWaiting || transition.To == protocol.StateDone) {
		r.notify(ctx, transition)
	}
}

// forget ends the pane's session on the summarizer for its tmux server. The summarizer drops the pane's waiting
// request and unsets its summary and label, and a run already in flight writes nothing once it finishes.
func (r *relay) forget(ctx context.Context, transition daemon.Transition) {
	summarizer, err := r.summarizerFor(transition.Server)
	if err != nil {
		r.logger.ErrorContext(ctx, "create summarizer", "pane", transition.PaneID, "error", err)
		return
	}
	summarizer.Forget(transition.PaneID)
}

// summarize requests the summary for the transition's pane. A working request is a periodic refresh; a done
// request is the Stop run and carries the last assistant message.
func (r *relay) summarize(ctx context.Context, transition daemon.Transition) {
	transcript, err := hookString(transition.Frame.Hook, keyTranscriptPath)
	if err != nil {
		r.logger.WarnContext(ctx, "read transcript path", "pane", transition.PaneID, "error", err)
		return
	}
	summarizer, err := r.summarizerFor(transition.Server)
	if err != nil {
		r.logger.ErrorContext(ctx, "create summarizer", "error", err)
		return
	}
	req := summary.Request{
		PaneID:         transition.PaneID,
		BaseURL:        transition.Frame.AnthropicBaseURL,
		TranscriptPath: transcript,
	}
	if transition.To == protocol.StateDone {
		req.Stop = true
		req.LastAssistantMessage, err = hookString(transition.Frame.Hook, keyLastAssistantMessage)
		if err != nil {
			r.logger.WarnContext(ctx, "read last assistant message", "pane", transition.PaneID, "error", err)
			return
		}
	}
	summarizer.Request(ctx, req)
}

// summarizerFor returns the summarizer for the tmux server, creating it on first use.
func (r *relay) summarizerFor(server tmuxctl.Server) (*summary.Summarizer, error) {
	if s, ok := r.summarizers[server]; ok {
		return s, nil
	}
	client, err := tmuxctl.New(server)
	if err != nil {
		return nil, fmt.Errorf("tmux server: %w", err)
	}
	s, err := summary.New(summary.Config{Claude: r.claude, Models: r.models, Tmux: client, Logger: r.logger})
	if err != nil {
		return nil, fmt.Errorf("summarizer: %w", err)
	}
	r.summarizers[server] = s
	return s, nil
}

// notify posts the transition's notification from a goroutine that wait awaits.
func (r *relay) notify(ctx context.Context, transition daemon.Transition) {
	// A notification accepted before a shutdown signal still goes out. The HTTP client's timeout bounds it.
	ctx = context.WithoutCancel(ctx)
	r.notifying.Add(1)
	go func() {
		defer r.notifying.Done()
		r.publish(ctx, transition)
	}()
}

// publish reads the pane's session, window and stored text from its tmux server, then posts the notification.
// Failures are logged and never stop the daemon.
func (r *relay) publish(ctx context.Context, transition daemon.Transition) {
	client, err := tmuxctl.New(transition.Server)
	if err != nil {
		r.logger.ErrorContext(ctx, "resolve tmux server", "pane", transition.PaneID, "error", err)
		return
	}
	session, window, err := paneLocation(ctx, client, transition.PaneID)
	if err != nil {
		r.logger.ErrorContext(ctx, "read pane location", "pane", transition.PaneID, "error", err)
		return
	}
	option, fallback := protocol.OptionQuestion, waitingBody
	if transition.To == protocol.StateDone {
		option, fallback = protocol.OptionSummary, doneBody
	}
	body, _, err := client.PaneOption(ctx, transition.PaneID, option)
	if err != nil {
		r.logger.ErrorContext(ctx, "read notification body", "pane", transition.PaneID, "error", err)
		return
	}
	if body == "" {
		body = fallback
	}
	project := filepath.Base(transition.Frame.Cwd)
	err = r.publisher.Publish(ctx, project, session, window, transition.To, body)
	if err != nil {
		r.logger.ErrorContext(ctx, "post notification", "pane", transition.PaneID, "error", err)
	}
}

// paneLocation returns the session name and window index of pane.
func paneLocation(ctx context.Context, client *tmuxctl.Client, pane string) (string, int, error) {
	out, err := client.Run(ctx, "display-message", "-p", "-t", pane, "#{session_name}\n#{window_index}")
	if err != nil {
		return "", 0, fmt.Errorf("display-message: %w", err)
	}
	session, index, found := strings.Cut(strings.TrimSuffix(out, "\n"), "\n")
	if !found {
		return "", 0, fmt.Errorf("display-message output %q has no window index", out)
	}
	window, err := strconv.Atoi(index)
	if err != nil {
		return "", 0, fmt.Errorf("window index %q: %w", index, err)
	}
	return session, window, nil
}

// wait returns once every summary run and notification has finished. It runs after Serve returns, so no observer
// can start more work.
func (r *relay) wait() {
	for _, summarizer := range r.summarizers {
		summarizer.Wait()
	}
	r.notifying.Wait()
}
