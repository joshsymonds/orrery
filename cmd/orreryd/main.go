// Command orreryd is the orrery daemon. It listens on a unix socket for hook frames, applies each one to the pane
// it names, starts summaries, and posts waiting and done notifications through ntfy and Web Push.
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
	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/notice"
	"github.com/joshsymonds/orrery/internal/ntfy"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/push"
	"github.com/joshsymonds/orrery/internal/store"
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
	// defaultSessionCap is the most live engine sessions at once when --session-cap is not given.
	defaultSessionCap = 4
	// notifyTimeout bounds each notification request.
	notifyTimeout = 10 * time.Second
	// waitingBody and doneBody stand in for an empty question or summary.
	waitingBody = "waiting for input"
	doneBody    = "done"
)

const usageText = `usage: orreryd [-L NAME | -S PATH] [--socket PATH] [--claude PATH] [--summary-model CONTEXT=MODEL]...
               [--push-host HOST]... [--sessions] [--session-cap N] [--session-shell PATH]

flags:
  -L NAME           tmux server by name (tmux -L)
  -S PATH           tmux server by socket path (tmux -S); not together with -L
  --socket PATH     daemon socket (default ORRERY_SOCKET, then $XDG_RUNTIME_DIR/orrery/orreryd.sock)
  --claude PATH     claude binary the summarizer runs (default claude)
  --summary-model CONTEXT=MODEL
                    summary model for a patchbay context; repeat for each context
  --push-host HOST  push relay host a Web Push subscription may name; repeat for each host
                    (default ntfy.sh)
  --sessions        serve the engine session service on sessions.sock beside the daemon socket
  --session-cap N   most live engine sessions at once (default 4)
  --session-shell PATH
                    absolute login shell that starts each session (default $SHELL)
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
	server        tmuxctl.Server
	defaultServer tmuxctl.Server // the default socket, set only when server is zero
	socket        string
	claude        string
	models        map[string]string
	pushHosts     []string
	sessions      bool
	sessionCap    int
	sessionShell  string
}

// hostFlag is the repeatable --push-host flag. It collects the push relay hosts in the order given.
type hostFlag []string

// String implements flag.Value.
func (h *hostFlag) String() string {
	return strings.Join(*h, ",")
}

// Set implements flag.Value.
func (h *hostFlag) Set(value string) error {
	*h = append(*h, value)
	return nil
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
	var pushHosts hostFlag
	flags.Var(&pushHosts, "push-host", "push relay host, repeatable")
	sessions := flags.Bool("sessions", false, "serve the engine session service")
	sessionCap := flags.Int("session-cap", defaultSessionCap, "most live engine sessions")
	sessionShell := flags.String("session-shell", getenv("SHELL"), "login shell that starts each session")
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
	if *sessionCap < 1 {
		return options{}, usagef("--session-cap must be at least 1, got %d", *sessionCap)
	}
	if *sessions && !filepath.IsAbs(*sessionShell) {
		return options{}, usagef("--sessions needs an absolute --session-shell, got %q", *sessionShell)
	}
	socket, err := protocol.SocketPath(*daemonSocket, getenv)
	if err != nil {
		return options{}, usagef("%v", err)
	}
	if len(pushHosts) == 0 {
		pushHosts = hostFlag{push.DefaultPushHost}
	}
	server := tmuxctl.Server{Name: *tmuxName, Socket: *tmuxSocket}
	var defaultServer tmuxctl.Server
	if server == (tmuxctl.Server{}) {
		defaultServer = tmuxctl.Server{Socket: defaultTmuxSocket(getenv)}
	}
	return options{
		server:        server,
		defaultServer: defaultServer,
		socket:        socket,
		claude:        *claude,
		models:        models,
		pushHosts:     pushHosts,
		sessions:      *sessions,
		sessionCap:    *sessionCap,
		sessionShell:  *sessionShell,
	}, nil
}

// defaultTmuxSocket returns the socket path tmux gives its default server. The directory is the symlink-resolved
// absolute TMUX_TMPDIR when it is set and resolves, else the resolved /tmp, else /tmp; the socket is tmux-<uid>/default
// in that directory.
func defaultTmuxSocket(getenv func(string) string) string {
	dir := resolveDir(getenv("TMUX_TMPDIR"))
	if dir == "" {
		dir = resolveDir("/tmp")
	}
	if dir == "" {
		dir = "/tmp"
	}
	return filepath.Join(dir, fmt.Sprintf("tmux-%d", os.Getuid()), "default")
}

// resolveDir returns the absolute, symlink-resolved path of dir, or "" when dir is empty or does not resolve.
func resolveDir(dir string) string {
	if dir == "" {
		return ""
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return ""
	}
	return resolved
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
	publisher, err := ntfy.New(getenv, host, &http.Client{Timeout: notifyTimeout})
	if err != nil && !errors.Is(err, ntfy.ErrNotConfigured) {
		return fmt.Errorf("ntfy: %w", err)
	}
	storePath, err := push.DefaultStorePath(getenv)
	if err != nil {
		return fmt.Errorf("push store: %w", err)
	}
	store, err := push.NewStore(storePath, opts.pushHosts)
	if err != nil {
		return fmt.Errorf("push store: %w", err)
	}
	pushes, err := push.NewPublisher(store, &http.Client{Timeout: notifyTimeout})
	if err != nil {
		return fmt.Errorf("push publisher: %w", err)
	}
	notices := notice.New(host, pushes, time.Now, logger)
	ln, err := daemon.Listen(opts.socket)
	if err != nil {
		return fmt.Errorf("daemon socket: %w", err)
	}

	st := openStore(ctx, getenv, logger)
	var mirror *daemon.Mirror
	if st != nil {
		mirror = daemon.NewMirror(st, time.Now, logger)
	}
	hub := &relay{
		claude:      opts.claude,
		models:      opts.models,
		logger:      logger,
		publisher:   publisher,
		notices:     notices,
		mirror:      mirror,
		summarizers: make(map[tmuxctl.Server]*summary.Summarizer),
	}
	observe := func(transition daemon.Transition) { hub.observe(ctx, transition) }
	var sessionNtfy enginehost.SessionPublisher
	if publisher != nil {
		sessionNtfy = publisher
	}
	notifier := enginehost.NewNotifier(enginehost.NotifierConfig{
		Host:   host,
		Ntfy:   sessionNtfy,
		Push:   pushes,
		Logger: logger,
	})
	var sessions *sessionHost
	if opts.sessions {
		sessions, err = startSessions(ctx, opts, st, notifier, logger)
		if err != nil {
			closeStore(ctx, st, logger)
			return errors.Join(err, ln.Close())
		}
	}
	serveErr := daemon.Serve(ctx, ln, daemon.Config{
		Server:        opts.server,
		DefaultServer: opts.defaultServer,
		Observers:     []daemon.Observer{observe, autoseen.New(time.Now, logger)},
		Seen: func(report daemon.SeenReport) {
			notices.Seen(ctx, notice.Key{Pane: report.PaneID}, report.Seen)
		},
		Logger: logger,
		Mirror: mirror,
	})
	hub.wait()
	notices.Wait()
	var sessionErr error
	if sessions != nil {
		sessionErr = sessions.stop()
	}
	closeStore(ctx, st, logger)
	if serveErr != nil {
		return errors.Join(fmt.Errorf("serve: %w", serveErr), sessionErr)
	}
	return sessionErr
}

// sessionHost is the engine session service of a --sessions orreryd. The manager runs the sessions, the writer stores
// their events, and the listener answers the Engine service on sessions.sock. Without a store the manager and writer
// are nil and every RPC is refused. The notifier receives the manager's notices and is waited on after the manager
// closes.
type sessionHost struct {
	manager    *enginehost.Manager
	writer     *enginehost.Writer
	notifier   *enginehost.Notifier
	stopWriter context.CancelFunc
	writerDone chan struct{}
	stopIdle   context.CancelFunc
	idleDone   chan struct{}
	stopServe  context.CancelFunc
	serveDone  chan error
}

// startSessions listens on sessions.sock beside the daemon socket and serves the Engine service on it until stop. With
// an open store it also starts the writer and a manager capped at opts.sessionCap that notifies through notifier. The
// manager reconciles the rows a previous run left before the service serves, and stops idle sessions until stop. A
// reconcile failure is logged and serving goes on.
func startSessions(
	ctx context.Context,
	opts options,
	st *store.Store,
	notifier *enginehost.Notifier,
	logger *slog.Logger,
) (*sessionHost, error) {
	host := &sessionHost{
		notifier:   notifier,
		writerDone: make(chan struct{}),
		idleDone:   make(chan struct{}),
		serveDone:  make(chan error, 1),
	}
	var commands enginehost.Commands
	if st != nil {
		host.writer = enginehost.NewWriter(enginehost.WriterConfig{Applier: st, Logger: logger})
		host.manager = enginehost.NewManager(enginehost.Config{
			Shell:    opts.sessionShell,
			Claude:   opts.claude,
			Environ:  os.Environ(),
			Cap:      opts.sessionCap,
			Writer:   host.writer,
			Notifier: notifier,
			Store:    st,
			Logger:   logger,
		})
		st.SetEngineLive(host.manager.Live)
		commands = host.manager
		var writerCtx context.Context
		writerCtx, host.stopWriter = context.WithCancel(context.WithoutCancel(ctx))
		go func() {
			host.writer.Run(writerCtx)
			close(host.writerDone)
		}()
		if reconcileErr := host.manager.Reconcile(ctx); reconcileErr != nil {
			logger.ErrorContext(ctx, "reconcile sessions, serving anyway", "error", reconcileErr)
		}
		var idleCtx context.Context
		idleCtx, host.stopIdle = context.WithCancel(ctx)
		go func() {
			host.manager.RunIdle(idleCtx)
			close(host.idleDone)
		}()
	}
	ln, err := enginehost.Listen(protocol.SessionSocketPath(opts.socket))
	if err != nil {
		host.shutdown()
		return nil, fmt.Errorf("session socket: %w", err)
	}
	var serveCtx context.Context
	serveCtx, host.stopServe = context.WithCancel(ctx)
	go func() { host.serveDone <- enginehost.Serve(serveCtx, ln, commands) }()
	return host, nil
}

// stop stops serving the session socket and then shuts the sessions down. It returns the error the session service ended
// with, if any.
func (h *sessionHost) stop() error {
	h.stopServe()
	serveErr := <-h.serveDone
	h.shutdown()
	if serveErr != nil {
		return fmt.Errorf("session service: %w", serveErr)
	}
	return nil
}

// shutdown stops the idle check, stops every live session and waits for the notices they started, and ends the writer
// after its last flush. Without a manager and writer it does nothing.
func (h *sessionHost) shutdown() {
	if h.manager != nil {
		h.stopIdle()
		<-h.idleDone
		h.manager.Close()
		h.notifier.Wait()
	}
	if h.writer != nil {
		h.stopWriter()
		<-h.writerDone
	}
}

// openStore opens the store at its resolved path. When the path does not resolve or the store cannot be opened, it
// logs the error and returns nil, so orreryd serves hooks and notifications without the store.
func openStore(ctx context.Context, getenv func(string) string, logger *slog.Logger) *store.Store {
	path, err := store.Path(getenv)
	if err != nil {
		logger.ErrorContext(ctx, "store unavailable, serving without it", "error", err)
		return nil
	}
	st, err := store.Open(ctx, path)
	if err != nil {
		logger.ErrorContext(ctx, "store unavailable, serving without it", "path", path, "error", err)
		return nil
	}
	return st
}

// closeStore closes st when it is open and logs a failed close.
func closeStore(ctx context.Context, st *store.Store, logger *slog.Logger) {
	if st == nil {
		return
	}
	if err := st.Close(); err != nil {
		logger.ErrorContext(ctx, "close store", "error", err)
	}
}

// relay turns each state change into summary requests and notifications. Its observe method runs under the
// daemon's lock, so it starts work and returns without waiting on HTTP. On SessionEnd it waits on tmux for the
// summarizer to forget the pane, so no run started before SessionEnd writes after the pane's next session begins.
type relay struct {
	claude      string
	models      map[string]string
	logger      *slog.Logger
	publisher   *ntfy.Publisher // nil when ntfy is off
	notices     *notice.Notices // the Web Push notices sent and cleared on this host
	mirror      *daemon.Mirror  // nil when the store is unavailable
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

// observe requests a summary for a pane that starts working or finishes, posts an ntfy notification when it waits for
// input or finishes, records a Web Push notice for the same transitions when push.Wanted allows, and forgets the
// pane's summary when its session ends.
func (r *relay) observe(ctx context.Context, transition daemon.Transition) {
	if transition.To == "" {
		r.forget(ctx, transition)
	}
	if transition.To == protocol.StateWorking || transition.To == protocol.StateDone {
		r.summarize(ctx, transition)
	}
	if transition.To == protocol.StateWaiting || transition.To == protocol.StateDone {
		if r.publisher != nil {
			r.notify(ctx, transition)
		}
		r.webPush(ctx, transition)
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
	var onWrite func(ctx context.Context, pane string)
	if r.mirror != nil {
		onWrite = func(ctx context.Context, pane string) { r.mirror.SyncPane(ctx, server, pane) }
	}
	summarizer, err := summary.New(summary.Config{
		Claude: r.claude, Models: r.models, Tmux: client, Logger: r.logger, OnWrite: onWrite,
	})
	if err != nil {
		return nil, fmt.Errorf("summarizer: %w", err)
	}
	r.summarizers[server] = summarizer
	return summarizer, nil
}

// notify posts the transition's ntfy notification from a goroutine that wait awaits. ntfy must be configured.
func (r *relay) notify(ctx context.Context, transition daemon.Transition) {
	// A notification accepted before a shutdown signal still goes out. The HTTP client's timeout bounds it.
	ctx = context.WithoutCancel(ctx)
	r.notifying.Add(1)
	go func() {
		defer r.notifying.Done()
		r.publish(ctx, transition)
	}()
}

// publish reads the pane's stored text from its tmux server, then posts the notification to ntfy. The caller starts
// it only when ntfy is configured. Failures are logged and never stop the daemon.
func (r *relay) publish(ctx context.Context, transition daemon.Transition) {
	client, err := tmuxctl.New(transition.Server)
	if err != nil {
		r.logger.ErrorContext(ctx, "resolve tmux server", "pane", transition.PaneID, "error", err)
		return
	}
	text, err := readPaneText(ctx, client, transition.PaneID)
	if err != nil {
		r.logger.ErrorContext(ctx, "read notification text", "pane", transition.PaneID, "error", err)
		return
	}
	r.publishNtfy(ctx, client, transition, filepath.Base(transition.Frame.Cwd), text)
}

// paneText is the text a notification carries, as stored on its pane.
type paneText struct {
	summary, label, question string
}

// readPaneText returns the summary, label and question options of pane.
func readPaneText(ctx context.Context, client *tmuxctl.Client, pane string) (paneText, error) {
	var text paneText
	for _, option := range []struct {
		name string
		dst  *string
	}{
		{protocol.OptionSummary, &text.summary},
		{protocol.OptionLabel, &text.label},
		{protocol.OptionQuestion, &text.question},
	} {
		value, _, err := client.PaneOption(ctx, pane, option.name)
		if err != nil {
			return paneText{}, fmt.Errorf("read %s: %w", option.name, err)
		}
		*option.dst = value
	}
	return text, nil
}

// publishNtfy posts the notification to ntfy. Its body is the question when the pane waits and the summary when
// it is done, and the session and window come from tmux.
func (r *relay) publishNtfy(
	ctx context.Context, client *tmuxctl.Client, transition daemon.Transition, project string, text paneText,
) {
	session, window, err := paneLocation(ctx, client, transition.PaneID)
	if err != nil {
		r.logger.ErrorContext(ctx, "read pane location", "pane", transition.PaneID, "error", err)
		return
	}
	body, fallback := text.question, waitingBody
	if transition.To == protocol.StateDone {
		body, fallback = text.summary, doneBody
	}
	if body == "" {
		body = fallback
	}
	err = r.publisher.Publish(ctx, project, session, window, transition.To, body)
	if err != nil {
		r.logger.ErrorContext(ctx, "post notification", "pane", transition.PaneID, "error", err)
	}
}

// webPush records a Web Push notice for the transition and sends it in the background when push.Wanted allows. A
// hook that does not decode still sends, and the decode error is logged.
func (r *relay) webPush(ctx context.Context, transition daemon.Transition) {
	wanted, err := push.Wanted(transition.To, transition.Frame.Hook)
	if err != nil {
		r.logger.WarnContext(ctx, "decide web push", "pane", transition.PaneID, "error", err)
	}
	if !wanted {
		return
	}
	r.notices.Notify(ctx, notice.Key{Pane: transition.PaneID}, func(ctx context.Context) (push.Notification, error) {
		return r.pushNotification(ctx, transition)
	})
}

// pushNotification reads the pane's stored text from its tmux server and returns the Web Push notification for the
// transition. Notices sets the host, pane, session and time.
func (r *relay) pushNotification(ctx context.Context, transition daemon.Transition) (push.Notification, error) {
	client, err := tmuxctl.New(transition.Server)
	if err != nil {
		return push.Notification{}, fmt.Errorf("resolve tmux server: %w", err)
	}
	text, err := readPaneText(ctx, client, transition.PaneID)
	if err != nil {
		return push.Notification{}, fmt.Errorf("read notification text: %w", err)
	}
	return push.Notification{
		Project:  filepath.Base(transition.Frame.Cwd),
		State:    transition.To,
		Summary:  text.summary,
		Label:    text.label,
		Question: text.question,
	}, nil
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
