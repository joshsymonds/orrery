// Command orreryd is the orrery daemon. It listens on a unix socket for hook frames, applies each one to the pane
// it names, starts summaries, and routes waiting and done notifications to the desk stream and Web Push.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
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
	"github.com/joshsymonds/orrery/internal/desk"
	"github.com/joshsymonds/orrery/internal/deskstream"
	"github.com/joshsymonds/orrery/internal/donejudge"
	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/grants"
	"github.com/joshsymonds/orrery/internal/notice"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/push"
	"github.com/joshsymonds/orrery/internal/route"
	"github.com/joshsymonds/orrery/internal/stamp"
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

// envAnthropicBaseURL is the settings env key that sets claude's API base URL.
const envAnthropicBaseURL = "ANTHROPIC_BASE_URL"

const (
	defaultClaude = "claude"
	// defaultSessionCap is the most live engine sessions at once when --session-cap is not given.
	defaultSessionCap = 4
	// defaultStepCap is the most live step sessions at once when --step-cap is not given.
	defaultStepCap = 6
	// notifyTimeout bounds each notification request.
	notifyTimeout = 10 * time.Second
	// noticeLogName is the notice log's file name, beside the push store.
	noticeLogName = "notices.jsonl"
	// readWindow is how long after a read report its pane still counts as read, so a waiting notice for it is held.
	readWindow = 30 * time.Second
	// silentBackground is why a done notice goes nowhere when its Stop left a subagent or workflow running.
	silentBackground = "background work"
	// brokerTick is how often the grants broker ticks. Each tick ends the grants of gone clients and polls each member.
	brokerTick = 60 * time.Second
	// waitingBody and doneBody stand in for an empty question or summary.
	waitingBody = "waiting for input"
	doneBody    = "done"
)

const usageText = `usage: orreryd [-L NAME | -S PATH] [--socket PATH] [--claude PATH] [--summary-model CONTEXT=MODEL]...
               [--push-host HOST]... [--sessions] [--session-cap N] [--session-shell PATH]
               [--step-cap N]

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
  --step-cap N      most live step sessions at once, counted apart from --session-cap (default 6)
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
	stepCap       int
}

// repeatFlag is a repeatable string flag. It collects the values in the order given.
type repeatFlag []string

// String implements flag.Value.
func (h *repeatFlag) String() string {
	return strings.Join(*h, ",")
}

// Set implements flag.Value.
func (h *repeatFlag) Set(value string) error {
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
	var pushHosts repeatFlag
	flags.Var(&pushHosts, "push-host", "push relay host, repeatable")
	sessions := flags.Bool("sessions", false, "serve the engine session service")
	sessionCap := flags.Int("session-cap", defaultSessionCap, "most live engine sessions")
	sessionShell := flags.String("session-shell", getenv("SHELL"), "login shell that starts each session")
	stepCap := flags.Int("step-cap", defaultStepCap, "most live step sessions")
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
	if *stepCap < 1 {
		return options{}, usagef("--step-cap must be at least 1, got %d", *stepCap)
	}
	socket, err := protocol.SocketPath(*daemonSocket, getenv)
	if err != nil {
		return options{}, usagef("%v", err)
	}
	if len(pushHosts) == 0 {
		pushHosts = repeatFlag{push.DefaultPushHost}
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
		stepCap:       *stepCap,
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

// listenSockets listens on the notices (desk) socket beside the daemon socket first, then on the daemon socket. A client
// that sees the daemon socket listening therefore also finds the notices socket listening. When the second listen fails
// it closes the first.
func listenSockets(socket string) (net.Listener, net.Listener, error) {
	deskLn, err := daemon.Listen(deskstream.SocketPath(socket))
	if err != nil {
		return nil, nil, fmt.Errorf("desk socket: %w", err)
	}
	ln, err := daemon.Listen(socket)
	if err != nil {
		return nil, nil, errors.Join(fmt.Errorf("daemon socket: %w", err), deskLn.Close())
	}
	return ln, deskLn, nil
}

// startDesk returns a hub for host that serves the desk stream on deskLn in the background. A failure to serve is
// logged. The caller closes the hub.
func startDesk(ctx context.Context, host string, deskLn net.Listener, logger *slog.Logger) *deskstream.Hub {
	desk := deskstream.NewHub(host, time.Now, logger)
	go func() {
		if err := desk.Serve(deskLn); err != nil {
			logger.ErrorContext(ctx, "serve desk stream", "error", err)
		}
	}()
	return desk
}

// serve listens on the daemon and desk sockets and applies hook frames while it opens the store, so no frame waits for
// the store. The first tidy runs once the store is open and the sessions have started. A failed sessions start stops
// serving and returns its error. Once the store is open, serve returns only after the store is closed.
func serve(ctx context.Context, opts options, logger *slog.Logger, getenv func(string) string) error {
	host, err := os.Hostname()
	if err != nil {
		return fmt.Errorf("host name: %w", err)
	}
	if host == "" {
		return errors.New("host name is empty")
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
	noticeLog, err := route.OpenLog(filepath.Join(filepath.Dir(storePath), noticeLogName))
	if err != nil {
		return fmt.Errorf("notice log: %w", err)
	}
	// The log closes last, after the router and the notices that write to it have stopped.
	defer closeLog(ctx, noticeLog, logger)
	notices := notice.New(host, pushes, time.Now, logger)
	// A failed sessions start cancels ctx, so everything that ctx ends stops as it does on a signal.
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	ln, deskLn, err := listenSockets(opts.socket)
	if err != nil {
		return err
	}
	desk := startDesk(ctx, host, deskLn, logger)
	// The hub closes last, after the sessions and notices that post to it have stopped.
	defer desk.Close()

	// The mirror waits for the store that the open below attaches to it.
	mirror := daemon.NewPendingMirror(time.Now, logger)
	home := getenv("HOME")
	router := route.New(route.Config{
		Host:       host,
		Now:        time.Now,
		Desk:       desk,
		Phone:      notices,
		Subscribed: hasSubscription(store),
		LocalInput: localInput(opts),
		Log:        noticeLog,
		Logger:     logger,
	})
	hub := &relay{
		claude: opts.claude,
		models: opts.models,
		home:   home,
		logger: logger,
		router: router,
		judge: donejudge.New(
			enginehost.NewHaiku(enginehost.HaikuConfig{Claude: opts.claude, Environ: os.Environ()}),
			opts.models,
		),
		reads:       newReadTimes(),
		mirror:      mirror,
		summarizers: make(map[tmuxctl.Server]*summary.Summarizer),
	}
	observe := func(transition daemon.Transition) { hub.observe(ctx, transition) }
	notifier := enginehost.NewNotifier(enginehost.NotifierConfig{Route: router})
	// The first tidy waits for tidyStart, which closes once the store is open and the sessions have started.
	tidyStart := make(chan struct{})
	serveDone := serveInBackground(ctx, ln, daemon.Config{
		Server:        opts.server,
		DefaultServer: opts.defaultServer,
		Home:          home,
		Observers: []daemon.Observer{observe, autoseen.New(time.Now, logger, func(pane string, seen int64) {
			router.Seen(ctx, notice.Key{Pane: pane}, seen)
		})},
		Seen: func(report daemon.SeenReport) {
			router.Seen(ctx, notice.Key{Pane: report.PaneID}, report.Seen)
		},
		Read: func(report daemon.ReadReport) {
			hub.reads.note(report.PaneID, report.Read)
		},
		Logger:    logger,
		Mirror:    mirror,
		TidyStart: tidyStart,
	})

	st := openStore(ctx, getenv, logger)
	mirror.Attach(st)
	var sessions *sessionHost
	var startErr error
	if opts.sessions {
		sessions, startErr = startSessions(ctx, opts, host, st, notifier, logger, getenv)
	}
	if startErr != nil {
		stop()
	} else {
		close(tidyStart)
	}
	serveErr := <-serveDone
	sessionErr := shutdownServe(ctx, logger, st, hub, sessions, router, notices)
	if serveErr != nil {
		serveErr = fmt.Errorf("serve: %w", serveErr)
	}
	return errors.Join(startErr, serveErr, sessionErr)
}

// serveInBackground runs daemon.Serve on ln with cfg until ctx is done. The returned channel receives Serve's error
// once it has returned.
func serveInBackground(ctx context.Context, ln net.Listener, cfg daemon.Config) <-chan error {
	done := make(chan error, 1)
	go func() { done <- daemon.Serve(ctx, ln, cfg) }()
	return done
}

// shutdownServe runs once Serve has returned. It waits for the summaries, stops the sessions, then waits for the router
// and the notices, since the sessions can post to them, and closes the store. It returns the sessions' stop error.
func shutdownServe(
	ctx context.Context,
	logger *slog.Logger,
	st *store.Store,
	hub *relay,
	sessions *sessionHost,
	router *route.Router,
	notices *notice.Notices,
) error {
	hub.wait()
	var sessionErr error
	if sessions != nil {
		sessionErr = sessions.stop()
	}
	// Stopping the sessions can notify an interrupted turn, so the router and the notices are waited on after it.
	shutdownRouter(router)
	notices.Wait()
	closeStore(ctx, st, logger)
	return sessionErr
}

// sessionHost is the engine session service of a --sessions orreryd. The manager runs the sessions, the writer stores
// their events, and the listener answers the Engine, Steps and Pools services on sessions.sock. Without a store the
// manager, writer and broker are nil and every RPC is refused. The broker is closed before the listener stops, so a
// grant that a blocked Acquire holds stays stored for the next orreryd. pools is the broker when its restore succeeded,
// and nil otherwise, so no Pools are served. brokerRun is closed when the broker's tick loop returns, and is nil when no
// loop runs.
type sessionHost struct {
	manager    *enginehost.Manager
	steps      *enginehost.Steps
	writer     *enginehost.Writer
	broker     *grants.Broker
	pools      enginehost.PoolCommands
	brokerRun  chan struct{}
	stopWriter context.CancelFunc
	writerDone chan struct{}
	stopIdle   context.CancelFunc
	idleDone   chan struct{}
	stopServe  context.CancelFunc
	serveDone  chan error
}

// startSessions listens on sessions.sock beside the daemon socket and serves the Engine, Steps and Pools services on it
// until stop. With an open store it also starts the writer, a manager capped at opts.sessionCap that notifies through
// notifier, the grants broker for hostName, which restores the grants a previous run stored and then ticks every
// brokerTick until stop, and the step engine capped at opts.stepCap whose worktrees go under step-worktrees beside the
// store file. The manager reconciles the rows a previous run left before the service serves, then the steps do, and
// the manager stops idle sessions until stop. A reconcile or restore failure is logged and serving goes on; a failed
// restore serves no Pools, which refuse as unavailable, and the gates fail for want of pools.
func startSessions(
	ctx context.Context,
	opts options,
	hostName string,
	st *store.Store,
	notifier *enginehost.Notifier,
	logger *slog.Logger,
	getenv func(string) string,
) (*sessionHost, error) {
	host := &sessionHost{
		writerDone: make(chan struct{}),
		idleDone:   make(chan struct{}),
		serveDone:  make(chan error, 1),
	}
	var commands enginehost.Commands
	var stepCommands enginehost.StepCommands
	if st != nil {
		storePath, err := store.Path(getenv)
		if err != nil {
			return nil, fmt.Errorf("step worktrees: %w", err)
		}
		host.writer = enginehost.NewWriter(enginehost.WriterConfig{Applier: st, Logger: logger})
		host.manager = enginehost.NewManager(enginehost.Config{
			Shell:    opts.sessionShell,
			Claude:   opts.claude,
			Environ:  os.Environ(),
			Cap:      opts.sessionCap,
			StepCap:  opts.stepCap,
			Writer:   host.writer,
			Notifier: notifier,
			Store:    st,
			Logger:   logger,
		})
		host.startBroker(ctx, hostName, st, logger)
		// host.pools is nil when the broker's restore failed, which leaves the gates no pools to run under.
		steps, err := enginehost.NewSteps(enginehost.StepsConfig{
			Sessions:  host.manager,
			Store:     st,
			Writer:    host.writer,
			Pools:     host.pools,
			Worktrees: filepath.Join(filepath.Dir(storePath), "step-worktrees"),
			Environ:   os.Environ(),
			Logger:    logger,
			Haiku:     enginehost.NewHaiku(enginehost.HaikuConfig{Claude: opts.claude, Environ: os.Environ()}),
		})
		if err != nil {
			host.stopBroker()
			return nil, fmt.Errorf("step engine: %w", err)
		}
		host.steps = steps
		st.SetEngineLive(host.manager.Live)
		commands = host.manager
		stepCommands = host.steps
		var writerCtx context.Context
		writerCtx, host.stopWriter = context.WithCancel(context.WithoutCancel(ctx))
		go func() {
			host.writer.Run(writerCtx)
			close(host.writerDone)
		}()
		if reconcileErr := host.manager.Reconcile(ctx); reconcileErr != nil {
			logger.ErrorContext(ctx, "reconcile sessions, serving anyway", "error", reconcileErr)
		}
		if reconcileErr := host.steps.Reconcile(ctx); reconcileErr != nil {
			logger.ErrorContext(ctx, "reconcile steps, serving anyway", "error", reconcileErr)
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
	go func() { host.serveDone <- enginehost.Serve(serveCtx, ln, commands, stepCommands, host.pools) }()
	return host, nil
}

// startBroker builds the grants broker for hostName over st and restores the grants a previous run stored. A restore
// that succeeds makes the broker the host's pools and starts its tick loop. A restore that fails is logged and leaves
// the pools nil, so no Pools are served; the broker is still closed at shutdown, since its restore may have started
// member commands. The broker closes as soon as ctx ends: the signal that ends ctx also starts Serve's shutdown, whose
// Close after shutdownWait cancels each held Acquire, and an Acquire canceled while the broker is open ends its grant.
func (h *sessionHost) startBroker(ctx context.Context, hostName string, st *store.Store, logger *slog.Logger) {
	h.broker = grants.NewBroker(grants.Config{
		Host:    hostName,
		Store:   st,
		Environ: os.Environ(),
		Logger:  logger,
	})
	context.AfterFunc(ctx, h.broker.Close)
	if err := h.broker.Restore(ctx); err != nil {
		logger.ErrorContext(ctx, "restore grants, serving without pools", "error", err)
		return
	}
	h.pools = h.broker
	h.brokerRun = make(chan struct{})
	go h.runBroker(context.WithoutCancel(ctx))
}

// runBroker ticks the broker every brokerTick until the broker closes. Each tick ends the grants of clients that are
// gone and polls each member's status. It closes brokerRun when it returns.
func (h *sessionHost) runBroker(ctx context.Context) {
	defer close(h.brokerRun)
	ticker := time.NewTicker(brokerTick)
	defer ticker.Stop()
	h.broker.Run(ctx, ticker.C)
}

// stopBroker closes the broker and waits for its tick loop to return, and for its member commands to end. Each Acquire
// the broker is blocking returns without ending its grant, so the grant stays stored for the next orreryd. Without a
// broker it does nothing, and so does a second call.
func (h *sessionHost) stopBroker() {
	if h.broker == nil {
		return
	}
	h.broker.Close()
	if h.brokerRun != nil {
		<-h.brokerRun
	}
}

// stop stops the broker, then serving the session socket, and then shuts the sessions down. Serve's shutdown closes the
// connections of the Acquire calls still blocked, and an Acquire whose context ends would end its grant, so the broker
// closes first. It returns the error the session service ended with, if any.
func (h *sessionHost) stop() error {
	h.stopBroker()
	h.stopServe()
	serveErr := <-h.serveDone
	h.shutdown()
	if serveErr != nil {
		return fmt.Errorf("session service: %w", serveErr)
	}
	return nil
}

// shutdown stops the broker, stops the idle check, ends every step loop, stops every live session, and ends the writer
// after its last flush. The notices the sessions started are waited on in serve. Without a broker, manager and writer
// it does nothing.
func (h *sessionHost) shutdown() {
	h.stopBroker()
	if h.manager != nil {
		h.stopIdle()
		<-h.idleDone
		h.steps.Close()
		h.manager.Close()
	}
	if h.writer != nil {
		h.stopWriter()
		<-h.writerDone
	}
}

// openStore opens the store at its resolved path and logs "store open" once the store is open and migrated. When the
// path does not resolve or the store cannot be opened, it logs the error and returns nil, so orreryd serves hooks and
// notifications without the store.
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
	logger.InfoContext(ctx, "store open", "path", path)
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

// shutdownRouter waits for the router's running decisions, then closes it, so a notice received before shutdown routes
// instead of ending with a shutdown line.
func shutdownRouter(router *route.Router) {
	router.Wait()
	router.Close()
}

// closeLog closes noticeLog and logs a failed close.
func closeLog(ctx context.Context, noticeLog *route.Log, logger *slog.Logger) {
	if err := noticeLog.Close(); err != nil {
		logger.ErrorContext(ctx, "close notice log", "error", err)
	}
}

// tmuxClient returns the client for server. Its error names the server that did not resolve.
func tmuxClient(server tmuxctl.Server) (*tmuxctl.Client, error) {
	client, err := tmuxctl.New(server)
	if err != nil {
		return nil, fmt.Errorf("resolve tmux server: %w", err)
	}
	return client, nil
}

// localInput returns the router's LocalInput: the newest input made on the tmux server orreryd serves, or on the
// default server when it serves none by name.
func localInput(opts options) func(context.Context) (int64, bool, error) {
	server := opts.server
	if server == (tmuxctl.Server{}) {
		server = opts.defaultServer
	}
	return func(ctx context.Context) (int64, bool, error) {
		client, err := tmuxClient(server)
		if err != nil {
			return 0, false, err
		}
		input, ok, err := desk.NewestInput(ctx, client)
		if err != nil {
			return 0, false, fmt.Errorf("newest input: %w", err)
		}
		return input, ok, nil
	}
}

// hasSubscription returns the router's Subscribed: whether the push store lists at least one subscription.
func hasSubscription(store *push.Store) func() (bool, error) {
	return func() (bool, error) {
		subs, err := store.List()
		if err != nil {
			return false, fmt.Errorf("list push subscriptions: %w", err)
		}
		return len(subs) > 0, nil
	}
}

// relay turns each state change into summary requests and notifications. Its observe method runs under the
// daemon's lock, so it starts work and returns without waiting on HTTP. On SessionEnd it waits on tmux for the
// summarizer to forget the pane, so no run started before SessionEnd writes after the pane's next session begins.
type relay struct {
	claude      string
	models      map[string]string
	home        string
	logger      *slog.Logger
	router      *route.Router    // routes the waiting and done notices to the desk and the phone
	judge       *donejudge.Judge // decides whether a plain done needs the user before it routes
	reads       *readTimes       // each pane's newest read report
	mirror      *daemon.Mirror   // syncs the pane each summary is written to; nil skips the sync
	summarizers map[tmuxctl.Server]*summary.Summarizer
}

// readTimes keeps each pane's newest read time. Read reports arrive on connection goroutines and watching checks run
// on the router's, so it is safe for concurrent use.
type readTimes struct {
	mu    sync.Mutex
	times map[string]int64
}

// newReadTimes returns an empty readTimes.
func newReadTimes() *readTimes {
	return &readTimes{times: make(map[string]int64)}
}

// note keeps read as pane's read time when it is newer than the one kept.
func (rt *readTimes) note(pane string, read int64) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if read > rt.times[pane] {
		rt.times[pane] = read
	}
}

// recent reports whether pane was read within readWindow before now.
func (rt *readTimes) recent(pane string, now time.Time) bool {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	read, ok := rt.times[pane]
	return ok && now.Sub(time.Unix(read, 0)) <= readWindow
}

// forget drops pane's read time when its session ends.
func (rt *readTimes) forget(pane string) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	delete(rt.times, pane)
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

// observe requests a summary for a pane that starts working or finishes, hands the notice for a pane that waits for
// input or finishes to the router, and otherwise tells the router the pane changed. A session's end also forgets the
// pane's summary and read time.
func (r *relay) observe(ctx context.Context, transition daemon.Transition) {
	if transition.To == "" {
		r.forget(ctx, transition)
		r.reads.forget(transition.PaneID)
	}
	if transition.To == protocol.StateWorking || transition.To == protocol.StateDone {
		r.summarize(ctx, transition)
	}
	if transition.To == protocol.StateWaiting || transition.To == protocol.StateDone {
		r.notify(ctx, transition)
		return
	}
	r.router.Changed(notice.Key{Pane: transition.PaneID})
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
		Dir:            transition.Frame.Cwd,
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
		Claude: r.claude, Models: r.models, Home: r.home, Tmux: client, Logger: r.logger, OnWrite: onWrite,
	})
	if err != nil {
		return nil, fmt.Errorf("summarizer: %w", err)
	}
	r.summarizers[server] = summarizer
	return summarizer, nil
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

// notify hands the transition's notice to the router. A done notice whose Stop left a subagent or workflow running is
// silent. Any other done is judged before it routes. A hook that does not decode still routes, and its decode error is
// logged.
func (r *relay) notify(ctx context.Context, transition daemon.Transition) {
	wanted, err := push.Wanted(transition.To, transition.Frame.Hook)
	if err != nil {
		r.logger.WarnContext(ctx, "decide notice push", "pane", transition.PaneID, "error", err)
	}
	silent := ""
	if !wanted {
		silent = silentBackground
	}
	incoming := route.Notice{
		Key:      notice.Key{Pane: transition.PaneID},
		State:    string(transition.To),
		Silent:   silent,
		Watching: func(ctx context.Context) (bool, error) { return r.watching(ctx, transition) },
		Desk:     func(ctx context.Context) (deskstream.Notice, error) { return r.deskNotice(ctx, transition) },
		Push:     func(ctx context.Context) (push.Notification, error) { return r.pushNotification(ctx, transition) },
	}
	if transition.To == protocol.StateDone && silent == "" {
		incoming.Judge = r.judgeDone(ctx, transition)
	}
	r.router.Notify(ctx, incoming)
}

// judgeDone returns the Judge of a done transition: the verdict on the Stop hook's last assistant message. The call
// runs against the URL and env of the context stamp in effect for the frame's cwd under the relay's home, or else
// against the frame's base URL, as the summarizer does. A stamp that cannot be read fails the judge without a call. A
// last message that does not decode is logged and judged as empty, which fails the judge.
func (r *relay) judgeDone(
	ctx context.Context,
	transition daemon.Transition,
) func(context.Context) (route.Verdict, error) {
	message, err := hookString(transition.Frame.Hook, keyLastAssistantMessage)
	if err != nil {
		r.logger.WarnContext(ctx, "read last assistant message", "pane", transition.PaneID, "error", err)
	}
	return func(ctx context.Context) (route.Verdict, error) {
		found, err := stamp.Find(r.home, transition.Frame.Cwd)
		if err != nil {
			return route.Verdict{}, fmt.Errorf("done judge: find context stamp: %w", err)
		}
		baseURL, env := transition.Frame.AnthropicBaseURL, map[string]string(nil)
		if found.Folder != "" {
			baseURL, env = found.BaseURL(), found.Env
		} else if baseURL != "" {
			env = map[string]string{envAnthropicBaseURL: baseURL}
		}
		return r.judge.Done(ctx, baseURL, env, message)
	}
}

// watching reports whether the transition's pane is watched: a read report named it within readWindow, or a client of
// its tmux server shows it with recent input.
func (r *relay) watching(ctx context.Context, transition daemon.Transition) (bool, error) {
	now := time.Now()
	if r.reads.recent(transition.PaneID, now) {
		return true, nil
	}
	client, err := tmuxClient(transition.Server)
	if err != nil {
		return false, err
	}
	viewing, err := autoseen.Viewing(ctx, client, transition.PaneID, now)
	if err != nil {
		return false, fmt.Errorf("pane viewing: %w", err)
	}
	return viewing, nil
}

// deskNotice builds the desk notice for the transition. Its body is the question when the pane waits and the summary
// when it is done, and its title names the project, session and window.
func (r *relay) deskNotice(ctx context.Context, transition daemon.Transition) (deskstream.Notice, error) {
	client, err := tmuxClient(transition.Server)
	if err != nil {
		return deskstream.Notice{}, err
	}
	text, err := readPaneText(ctx, client, transition.PaneID)
	if err != nil {
		return deskstream.Notice{}, fmt.Errorf("read notification text: %w", err)
	}
	session, window, err := paneLocation(ctx, client, transition.PaneID)
	if err != nil {
		return deskstream.Notice{}, fmt.Errorf("read pane location: %w", err)
	}
	body, fallback := text.question, waitingBody
	if transition.To == protocol.StateDone {
		body, fallback = text.summary, doneBody
	}
	if body == "" {
		body = fallback
	}
	return deskstream.Notice{
		State: string(transition.To),
		Title: deskstream.PaneTitle(filepath.Base(transition.Frame.Cwd), session, window),
		Body:  body,
	}, nil
}

// pushNotification reads the pane's stored text from its tmux server and returns the Web Push notification for the
// transition. Notices sets the host, pane, session and time.
func (r *relay) pushNotification(ctx context.Context, transition daemon.Transition) (push.Notification, error) {
	client, err := tmuxClient(transition.Server)
	if err != nil {
		return push.Notification{}, err
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

// wait returns once every summary run has finished. It runs after Serve returns, so no observer can start more work.
func (r *relay) wait() {
	for _, summarizer := range r.summarizers {
		summarizer.Wait()
	}
}
