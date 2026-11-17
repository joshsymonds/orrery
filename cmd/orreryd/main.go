// Command orreryd is the orrery daemon. It listens on a unix socket for hook frames, applies each one to the pane
// it names, starts summaries, and routes waiting and done notifications to the desk stream and Web Push. With
// --sessions it also serves the engine session socket: the enginehost Manager, Steps, Writer, pools and evidence,
// and the planner and build runners. With --metrics-listen it serves /metrics and POST /alerts on that address.
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
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"connectrpc.com/connect"

	"github.com/joshsymonds/orrery/internal/alerts"
	"github.com/joshsymonds/orrery/internal/autoseen"
	"github.com/joshsymonds/orrery/internal/build"
	"github.com/joshsymonds/orrery/internal/cli"
	"github.com/joshsymonds/orrery/internal/daemon"
	"github.com/joshsymonds/orrery/internal/deskstream"
	"github.com/joshsymonds/orrery/internal/donejudge"
	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/gates"
	"github.com/joshsymonds/orrery/internal/grants"
	"github.com/joshsymonds/orrery/internal/holder"
	"github.com/joshsymonds/orrery/internal/inapp"
	"github.com/joshsymonds/orrery/internal/interpret"
	"github.com/joshsymonds/orrery/internal/notice"
	"github.com/joshsymonds/orrery/internal/planner"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/push"
	"github.com/joshsymonds/orrery/internal/roles"
	"github.com/joshsymonds/orrery/internal/route"
	"github.com/joshsymonds/orrery/internal/sdnotify"
	"github.com/joshsymonds/orrery/internal/server"
	"github.com/joshsymonds/orrery/internal/stamp"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/summary"
	"github.com/joshsymonds/orrery/internal/telemetry"
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

// envNotifySocket is the variable systemd sets to the socket that takes READY=1 for a Type=notify unit.
const envNotifySocket = "NOTIFY_SOCKET"

// managedDir is the managed settings root the session manager runs under and the planner's resolver checks agents in.
const managedDir = "/etc/claude-code"

const (
	defaultClaude = "claude"
	// defaultSessionCap is the most live engine sessions at once when --session-cap is not given.
	defaultSessionCap = 4
	// defaultStepCap is the most live step sessions at once when --step-cap is not given.
	defaultStepCap = 6
	// notifyTimeout bounds each notification request.
	notifyTimeout = 10 * time.Second
	// flightThreshold is how long a start to READY or a sessions reconcile may take before the flight recorder writes
	// a trace.
	flightThreshold = 10 * time.Second
	// flightDirName is the directory beside the store that holds the flight traces.
	flightDirName = "traces"
	// noticeLogName is the notice log's file name, beside the push store.
	noticeLogName = "notices.jsonl"
	// readWindow is how long after a read report its pane still counts as read, so a waiting notice for it is held.
	readWindow = 30 * time.Second
	// silentBackground is why a done notice goes nowhere when its Stop left a subagent or workflow running.
	silentBackground = "background work"
	// brokerTick is how often the grants broker ticks. Each tick ends the grants of gone clients and polls each member.
	brokerTick = 60 * time.Second
	// limitTick is how often the limit notices check the limited steps for a change, and how often the due pass, the
	// epic summaries, the session titles and the epic reports run.
	limitTick = 5 * time.Second
	// waitingBody and doneBody stand in for an empty question or summary.
	waitingBody = "waiting for input"
	doneBody    = "done"
)

const usageText = `usage: orreryd [-L NAME | -S PATH] [--socket PATH] [--claude PATH] [--summary-model CONTEXT=MODEL]...
               [--push-host HOST]... [--sessions] [--session-cap N] [--session-shell PATH]
               [--holder-scope PATH] [--step-cap N] [--metrics-listen ADDR]

flags:
  -L NAME           tmux server by name (tmux -L)
  -S PATH           tmux server by socket path (tmux -S); not together with -L
  --socket PATH     daemon socket (default ORRERY_SOCKET, then $XDG_RUNTIME_DIR/orrery/orreryd.sock)
  --claude PATH     claude binary the summarizer runs (default claude)
  --summary-model CONTEXT=MODEL
                    summary model for a proxy context; repeat for each context
  --push-host HOST  push relay host a Web Push subscription may name; repeat for each host
                    (default ntfy.sh)
  --sessions        serve the engine session service on sessions.sock beside the daemon socket
  --session-cap N   most live engine sessions at once (default 4)
  --session-shell PATH
                    absolute login shell that starts each session (default $SHELL)
  --holder-scope PATH
                    systemd-run binary that runs each session's holder in its own user scope
                    (default: none, the holder starts plainly)
  --step-cap N      most live step sessions at once, counted apart from --session-cap (default 6)
  --metrics-listen ADDR
                    serve /metrics on ADDR, a loopback IP literal with a port (default: none)
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
	server          tmuxctl.Server
	defaultServer   tmuxctl.Server // the default socket, set only when server is zero
	socket          string
	claude          string
	models          map[string]string
	pushHosts       []string
	sessions        bool
	sessionCap      int
	sessionShell    string
	holderScope     string
	stepCap         int
	judgeDelay      time.Duration           // the --judge-delay test seam's wait, route.JudgeDelay by default
	writeTick       time.Duration           // the --write-tick test seam's engine writer tick, 0 for the writer's own
	metricsListen   string                  // the --metrics-listen address, "" when unset
	metrics         *telemetry.Daemon       // set by startMetrics when metricsListen is set, nil otherwise
	metricsHandlers []connect.HandlerOption // set by startMetrics when metricsListen is set, nil otherwise
	webhook         *alerts.Webhook         // the POST /alerts intake, set by startMetrics when metricsListen is set
	metricsStop     func()                  // stops and waits for the metrics listener; set by startMetrics, nil otherwise
	notifySocket    string                  // the NOTIFY_SOCKET value orreryd started with, "" when unset
	// holdReconcile is a test seam: reconcile calls it before it starts, and production leaves it nil.
	holdReconcile func(ctx context.Context)
	// clock is a test seam: the clock the flight recorder times the start and reconcile phases on. Production leaves
	// it nil, which is time.Now.
	clock func() time.Time
	// attentionTicks is a test seam: when set, the attention loop passes at each value it delivers in place of every
	// five seconds, and production leaves it nil.
	attentionTicks <-chan time.Time
	// limitTicks is a test seam: when set, the limit notices check at each value it delivers in place of every
	// limitTick, and production leaves it nil.
	limitTicks <-chan time.Time
	// dueNow is a test seam: the clock the due pass reads to decide which limited steps are due. Production leaves it
	// nil, which is time.Now. A test whose steps carry fixed reset dates sets it, so the real date never makes them due.
	dueNow func() time.Time
	// host is a test seam: the host name the desk stream, the notices and the grants broker use in place of
	// os.Hostname. Production leaves it "".
	host string
	// flowTicks is a test seam: the path the flow appends a line to after each pass, and each SIGUSR1 starts a pass in
	// place of the flow's five-second ticks. Production leaves it "".
	flowTicks string
	flight    *telemetry.Flight // the flight recorder run starts, nil when it could not start
	started   time.Time         // when run began, the start of the start phase
	// stepEventTicks is a test seam: when set, the step caller-event pass passes at each value it delivers in place of
	// every five seconds, and production leaves it nil.
	stepEventTicks <-chan time.Time
	// epicEventTicks is a test seam: when set, the epic caller-event pass passes at each value it delivers in place of
	// every five seconds, and production leaves it nil.
	epicEventTicks <-chan time.Time
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

// modelFlag is the repeatable --summary-model flag. It maps each proxy context to its summary model.
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
	// start is when orreryd began, the start of the start phase that the flight recorder times to READY.
	start := time.Now()
	// The notify socket is read and removed before anything captures the environment, so no child inherits it.
	notifySocket := getenv(envNotifySocket)
	if err := os.Unsetenv(envNotifySocket); err != nil {
		return report(stderr, fmt.Errorf("unset %s: %w", envNotifySocket, err))
	}
	opts, err := parseFlags(args, getenv)
	if err != nil {
		return report(stderr, err)
	}
	opts.notifySocket = notifySocket
	opts.started = start
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	logger := slog.New(slog.NewTextHandler(stderr, nil))
	// The recorder starts whether or not --metrics-listen is set, and stops when run returns.
	opts.flight = startFlight(ctx, getenv, flightThreshold, logger)
	defer opts.flight.Stop()
	// The metrics address is bound before any socket exists, so a refused address fails before the daemon socket does.
	if err := startMetrics(ctx, &opts, start, logger); err != nil {
		return report(stderr, err)
	}
	return report(stderr, serve(ctx, opts, logger, getenv))
}

// startFlight starts the flight recorder over the traces directory beside the store, so a phase that takes longer than
// threshold writes a trace there. A recorder that cannot start is logged and nil is returned, so orreryd serves on.
func startFlight(
	ctx context.Context,
	getenv func(string) string,
	threshold time.Duration,
	logger *slog.Logger,
) *telemetry.Flight {
	storePath, err := store.Path(getenv)
	if err != nil {
		logger.ErrorContext(ctx, "flight recorder off, serving anyway", "error", err)
		return nil
	}
	dir := filepath.Join(filepath.Dir(storePath), flightDirName)
	flight, err := telemetry.StartFlight(dir, threshold)
	if err != nil {
		logger.ErrorContext(ctx, "start flight recorder, serving anyway", "dir", dir, "error", err)
		return nil
	}
	return flight
}

// observeFlight writes a flight trace named for phase when took is over the flight threshold. A write that fails is
// logged and serving goes on. A nil flight writes nothing.
func observeFlight(
	ctx context.Context,
	logger *slog.Logger,
	flight *telemetry.Flight,
	phase string,
	took time.Duration,
) {
	if err := flight.Observe(phase, took); err != nil {
		logger.ErrorContext(ctx, "write flight trace, serving anyway", "phase", phase, "error", err)
	}
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

// hostName is the --host test seam's name when it is set, and os.Hostname otherwise.
func hostName(seam string) (string, error) {
	if seam != "" {
		return seam, nil
	}
	host, err := os.Hostname()
	if err != nil {
		return "", fmt.Errorf("read the machine's host name: %w", err)
	}
	return host, nil
}

// dueClock is the due pass's clock for the --due-at test seam: nil, which is time.Now, for zero, and otherwise a clock
// stopped at unix second at.
func dueClock(at int64) func() time.Time {
	if at == 0 {
		return nil
	}
	return func() time.Time { return time.Unix(at, 0) }
}

// routeJudgeDelay is the route.Config.JudgeDelay of the --judge-delay wait d: d itself, or route.NoJudgeDelay for zero,
// which route.Config reads as its default.
func routeJudgeDelay(d time.Duration) time.Duration {
	if d == 0 {
		return route.NoJudgeDelay
	}
	return d
}

// checkLimits refuses a --session-cap or --step-cap below 1 and a negative --judge-delay or --write-tick, each as a
// usage error.
func checkLimits(sessionCap, stepCap int, judgeDelay, writeTick time.Duration) error {
	if sessionCap < 1 {
		return usagef("--session-cap must be at least 1, got %d", sessionCap)
	}
	if stepCap < 1 {
		return usagef("--step-cap must be at least 1, got %d", stepCap)
	}
	if judgeDelay < 0 {
		return usagef("--judge-delay must not be negative, got %v", judgeDelay)
	}
	if writeTick < 0 {
		return usagef("--write-tick must not be negative, got %v", writeTick)
	}
	return nil
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
	holderScope := flags.String("holder-scope", "", "systemd-run binary that runs each session's holder")
	stepCap := flags.Int("step-cap", defaultStepCap, "most live step sessions")
	metricsListen := flags.String("metrics-listen", "", "loopback address that serves /metrics")
	flowTicks := flags.String("flow-ticks", "", "test seam: file each flow pass appends a line to")
	dueAt := flags.Int64("due-at", 0, "test seam: unix seconds the due pass reads as now")
	hostName := flags.String("host", "", "test seam: host name in place of the machine's")
	judgeDelay := flags.Duration("judge-delay", route.JudgeDelay, "test seam: wait of a done before its judge")
	writeTick := flags.Duration("write-tick", 0, "test seam: how often the engine writer stores queued ops")
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
	if err := checkLimits(*sessionCap, *stepCap, *judgeDelay, *writeTick); err != nil {
		return options{}, err
	}
	if *dueAt < 0 {
		return options{}, usagef("--due-at must not be negative, got %d", *dueAt)
	}
	if *sessions && !filepath.IsAbs(*sessionShell) {
		return options{}, usagef("--sessions needs an absolute --session-shell, got %q", *sessionShell)
	}
	if *holderScope != "" && !filepath.IsAbs(*holderScope) {
		return options{}, usagef("--holder-scope needs an absolute path, got %q", *holderScope)
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
		holderScope:   *holderScope,
		stepCap:       *stepCap,
		judgeDelay:    *judgeDelay,
		writeTick:     *writeTick,
		metricsListen: *metricsListen,
		flowTicks:     *flowTicks,
		dueNow:        dueClock(*dueAt),
		host:          *hostName,
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
	desk := deskstream.NewHub(host, logger)
	go func() {
		if err := desk.Serve(deskLn); err != nil {
			logger.ErrorContext(ctx, "serve desk stream", "error", err)
		}
	}()
	return desk
}

// serve listens on the daemon and desk sockets and applies hook frames while it opens the store, so no frame waits for
// the store. The first tidy runs once the store is open and the sessions have started. A failed sessions start stops
// serving and returns its error. Once the store is open, serve returns only after the store is closed. serve sends
// systemd READY=1 once the sessions have started, or once the store has opened when there are no sessions. With
// --metrics-listen, the ready time is recorded just before READY=1. With a flight recorder, a start to READY over its
// threshold writes a start trace once READY=1 is sent.
func serve(ctx context.Context, opts options, logger *slog.Logger, getenv func(string) string) error {
	host, err := hostName(opts.host)
	if err != nil {
		return fmt.Errorf("host name: %w", err)
	}
	if host == "" {
		return errors.New("host name is empty")
	}
	storePath, store, pushes, err := openPushes(opts, getenv)
	if err != nil {
		return err
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
	// The watch logs the shutdown's start and writes the goroutine stacks of one that runs long, until serve returns.
	defer watchShutdown(ctx, logger, stacksDir(getenv), shutdownStacksAfter)()
	ln, deskLn, err := listenSockets(opts.socket)
	if err != nil {
		return err
	}
	desk := startDesk(ctx, host, deskLn, logger)
	// The hub closes last, after the sessions and notices that post to it have stopped.
	defer shutdownPhase(logger, "desk", desk.Close)
	// shutdownServe closes pages first, so its Watch streams end before the session socket stops.
	pages := inapp.NewHub()
	// The viewing tracker holds the notices of the key the web view views, and is fed by each input report.
	viewing := newViewTracker(time.Now)

	// The mirror waits for the store that the open below attaches to it.
	mirror := daemon.NewPendingMirror(time.Now, logger)
	home := getenv("HOME")
	hub := newRelay(routeConfig(serveRouteInputs{
		Host:       host,
		Now:        time.Now,
		JudgeDelay: routeJudgeDelay(opts.judgeDelay),
		Desk:       desk,
		Phone:      notices,
		Pages:      pages,
		Subscribed: hasSubscription(store),
		Log:        noticeLog,
		Logger:     logger,
	}, viewing), &relay{
		claude:      opts.claude,
		models:      opts.models,
		home:        home,
		logger:      logger,
		judge:       newDoneJudge(opts.claude, opts.models),
		reads:       newReadTimes(),
		mirror:      mirror,
		webhook:     opts.webhook,
		summarizers: make(map[tmuxctl.Server]*summary.Summarizer),
		views:       newDoneViews(),
	})
	router := hub.router
	pageNotices := enginehost.NewPageNotices(pages, router, time.Now)
	observe := func(transition daemon.Transition) { hub.observe(ctx, transition) }
	// The first tidy waits for tidyStart, which closes once the store is open and the sessions have started.
	tidyStart := make(chan struct{})
	serveDone := serveInBackground(ctx, ln, daemon.Config{
		Server:        opts.server,
		DefaultServer: opts.defaultServer,
		Home:          home,
		Sessions:      opts.sessions,
		Observers: []daemon.Observer{observe, autoseen.New(time.Now, logger, func(pane string, seen int64) {
			router.Seen(ctx, notice.Key{Pane: pane}, seen)
		}, hub.viewer)},
		Seen:      func(report daemon.SeenReport) { router.Seen(ctx, notice.Key{Pane: report.PaneID}, report.Seen) },
		Input:     desk.Input,
		Viewing:   func(report daemon.ViewReport) { viewing.note(report.Input, report.View) },
		Read:      func(report daemon.ReadReport) { hub.reads.note(report.PaneID, report.Read) },
		Logger:    logger,
		Mirror:    mirror,
		TidyStart: tidyStart,
		// Each tidy pass retries the notice row writes the store refused, so they are stored once it takes writes.
		AfterTidy: notices.Retry,
	})

	st := openStore(ctx, getenv, logger, opts.metrics)
	attachStore(ctx, st, mirror, notices, router)
	var sessions *sessionHost
	var startErr error
	if opts.sessions {
		sessions, startErr = startSessions(ctx, opts, host, st, router, pageNotices, logger, getenv)
	}
	if startErr != nil {
		stop()
	} else {
		close(tidyStart)
		notifyReady(ctx, logger, opts)
	}
	serveErr := awaitServe(ctx, logger, serveDone)
	shutdownPhase(logger, "metrics listener", opts.stopMetricsListener)
	sessionErr := shutdownServe(ctx, logger, pages, st, hub, sessions, router, notices)
	if serveErr != nil {
		serveErr = fmt.Errorf("serve: %w", serveErr)
	}
	return errors.Join(startErr, serveErr, sessionErr)
}

// openPushes opens the push store at its default path, for opts' push hosts, and the publisher that sends through it.
// It returns the store's path too, which the notice log lies beside.
func openPushes(opts options, getenv func(string) string) (string, *push.Store, *push.Publisher, error) {
	storePath, err := push.DefaultStorePath(getenv)
	if err != nil {
		return "", nil, nil, fmt.Errorf("push store: %w", err)
	}
	store, err := push.NewStore(storePath, opts.pushHosts)
	if err != nil {
		return "", nil, nil, fmt.Errorf("push store: %w", err)
	}
	pushes, err := push.NewPublisher(store, &http.Client{Timeout: notifyTimeout}, opts.metrics)
	if err != nil {
		return "", nil, nil, fmt.Errorf("push publisher: %w", err)
	}
	return storePath, store, pushes, nil
}

// awaitServe returns the error daemon.Serve returns on serveDone. Once ctx has ended, the wait for Serve's shutdown is
// logged as the hook socket phase.
func awaitServe(ctx context.Context, logger *slog.Logger, serveDone <-chan error) error {
	select {
	case err := <-serveDone:
		return err
	case <-ctx.Done():
	}
	var err error
	shutdownPhase(logger, "hook socket", func() { err = <-serveDone })
	return err
}

// stacksDir returns the flight traces directory beside the store, which a slow shutdown writes its goroutine stacks
// to. A store path that does not resolve gives "", and the write then fails and is logged.
func stacksDir(getenv func(string) string) string {
	storePath, err := store.Path(getenv)
	if err != nil {
		return ""
	}
	return filepath.Join(filepath.Dir(storePath), flightDirName)
}

// flightNow is the time on the clock the flight recorder times phases on: opts.clock, or time.Now when it is nil.
func (opts options) flightNow() time.Time {
	if opts.clock == nil {
		return time.Now()
	}
	return opts.clock()
}

// notifyReady records the ready time on metrics, then sends systemd READY=1 to opts.notifySocket. An empty socket means
// systemd did not start orreryd, so nothing is sent. A failed send is logged and serving goes on. A nil metrics records
// nothing. The start to READY time is then observed by the flight recorder, which writes a start trace when it is over
// the threshold.
func notifyReady(ctx context.Context, logger *slog.Logger, opts options) {
	took := opts.flightNow().Sub(opts.started)
	opts.metrics.Ready(ctx, time.Now())
	if opts.notifySocket != "" {
		if err := sdnotify.Ready(opts.notifySocket); err != nil {
			logger.ErrorContext(ctx, "notify systemd, serving anyway", "error", err)
		}
	}
	observeFlight(ctx, logger, opts.flight, "start", took)
}

// metricsListeningMessage is the message of the log line that names the address the metrics listener bound.
const metricsListeningMessage = "metrics listening"

// startMetrics serves /metrics and POST /alerts for a --metrics-listen orreryd until ctx ends or serve stops it. It
// binds the address and logs the address it bound, so a port 0 address can be found, records start as the start time,
// and sets opts.metrics and opts.metricsHandlers, which the push publisher, the writer, the manager and the session
// services take, and opts.webhook, which serve's relay attaches to the notice router. Without the flag it sets nothing.
func startMetrics(ctx context.Context, opts *options, start time.Time, logger *slog.Logger) error {
	if opts.metricsListen == "" {
		return nil
	}
	ln, err := server.Listen(ctx, opts.metricsListen)
	if err != nil {
		return fmt.Errorf("metrics listener: %w", err)
	}
	logger.InfoContext(ctx, metricsListeningMessage, "addr", ln.Addr().String())
	provider, err := telemetry.New()
	if err != nil {
		return errors.Join(fmt.Errorf("metrics provider: %w", err), ln.Close())
	}
	daemon, err := telemetry.NewDaemon(ctx, provider, start)
	if err != nil {
		return errors.Join(fmt.Errorf("metrics daemon: %w", err), ln.Close())
	}
	webhook := alerts.New()
	opts.metrics = daemon
	opts.metricsHandlers = provider.HandlerOptions()
	opts.webhook = webhook
	listenCtx, stopListen := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		if serveErr := provider.ServeWithAlerts(listenCtx, ln, webhook); serveErr != nil {
			logger.ErrorContext(ctx, "serve metrics", "error", serveErr)
		}
	}()
	opts.metricsStop = func() {
		stopListen()
		<-done
	}
	return nil
}

// stopMetricsListener stops the metrics listener startMetrics started and waits for it to end, so no alert is accepted
// once serve closes the router. It runs here rather than on the run context, which a failed sessions start leaves
// running. Without --metrics-listen it does nothing.
func (opts options) stopMetricsListener() {
	if opts.metricsStop != nil {
		opts.metricsStop()
	}
}

// serveInBackground runs daemon.Serve on ln with cfg until ctx is done. The returned channel receives Serve's error
// once it has returned.
func serveInBackground(ctx context.Context, ln net.Listener, cfg daemon.Config) <-chan error {
	done := make(chan error, 1)
	go func() { done <- daemon.Serve(ctx, ln, cfg) }()
	return done
}

// shutdownServe runs once Serve has returned. It closes pages first, so the Watch streams open on it end before the
// session socket stops serving. It waits for the summaries and then for the mirror's deferred syncs and tidies, stops
// the sessions, then waits for the router and the notices, since the sessions can post to them, and closes the store.
// It returns the sessions' stop error.
func shutdownServe(
	ctx context.Context,
	logger *slog.Logger,
	pages *inapp.Hub,
	st *store.Store,
	hub *relay,
	sessions *sessionHost,
	router *route.Router,
	notices *notice.Notices,
) error {
	shutdownPhase(logger, "pages", pages.Close)
	shutdownPhase(logger, "pane summaries", hub.wait)
	// Serve and the summaries have returned, so nothing defers more mirror work, and what was deferred lands before
	// the store closes.
	if hub.mirror != nil {
		shutdownPhase(logger, "mirror", hub.mirror.Wait)
	}
	var sessionErr error
	if sessions != nil {
		sessionErr = sessions.stop()
	}
	// Stopping the sessions can notify an interrupted turn, so the router and the notices are waited on after it. The
	// notices close before the store, so a row write the store refused gets its last try while the store is open.
	shutdownPhase(logger, "router", func() { shutdownRouter(router) })
	shutdownPhase(logger, "notices", func() { notices.Close(ctx) })
	shutdownPhase(logger, "store", func() { closeStore(ctx, st, logger) })
	return sessionErr
}

// sessionHost is the engine session service of a --sessions orreryd. The manager runs the sessions, the writer stores
// their events, and the listener answers the Engine, Steps, Pools and Evidence services on sessions.sock. Without a
// store the manager, writer and broker are nil and every RPC is refused. The broker is closed before the listener stops,
// so a grant that a blocked Acquire holds stays stored for the next orreryd. pools is the broker when its restore
// succeeded, and nil otherwise, so no Pools are served. evidence is the evidence runner over the store, the steps and
// the pools, and nil without a store. checks is the node checks runner over the store and the pools, and nil without a
// store. brokerRun is closed when the broker's tick loop returns, and is nil when no loop runs.
type sessionHost struct {
	manager    *enginehost.Manager
	steps      *enginehost.Steps
	writer     *enginehost.Writer
	broker     *grants.Broker
	pools      enginehost.PoolCommands
	evidence   *enginehost.Evidence
	checks     *enginehost.NodeChecks
	brokerRun  chan struct{}
	stopWriter context.CancelFunc
	writerDone chan struct{}
	stopIdle   context.CancelFunc
	idleDone   chan struct{}
	stopFlow   context.CancelFunc
	flowDone   chan struct{}
	// attentionDone is closed when the attention loop returns, and is nil when no loop runs.
	attentionDone chan struct{}
	stopLimits    context.CancelFunc
	limitsDone    chan struct{}
	stopDue       context.CancelFunc
	dueDone       chan struct{}
	stopSummaries context.CancelFunc
	summariesDone chan struct{}
	stopTitles    context.CancelFunc
	titlesDone    chan struct{}
	stopReports   context.CancelFunc
	reportsDone   chan struct{}
	stopServe     context.CancelFunc
	serveDone     chan error
	// stopCallerEvents cancels the caller-event passes, each of which closes its done channel when it returns.
	stopCallerEvents context.CancelFunc
	stepEventsDone   chan struct{}
	epicEventsDone   chan struct{}
	// logger logs each phase of the shutdown.
	logger *slog.Logger
}

// stateDirOf returns the absolute directory the store file lies in, which the store path getenv resolves names. The
// worktrees the node checks run in are git worktrees under it, so it must be absolute whatever XDG_STATE_HOME holds.
func stateDirOf(getenv func(string) string) (string, error) {
	storePath, err := store.Path(getenv)
	if err != nil {
		return "", fmt.Errorf("state directory: %w", err)
	}
	stateDir, err := filepath.Abs(filepath.Dir(storePath))
	if err != nil {
		return "", fmt.Errorf("state directory: %w", err)
	}
	return stateDir, nil
}

// startSessions listens on sessions.sock beside the daemon socket and serves the Engine, Steps, Pools and Evidence
// services on it until stop. With an open store it also starts the writer, a manager capped at opts.sessionCap that
// notifies through router, the grants broker for hostName, which restores the grants a previous run stored and then
// ticks every brokerTick until stop, and the step engine capped at opts.stepCap whose worktrees go under step-worktrees
// beside the store file. The steps re-adopt the open step rows a previous run left, the manager reconciles the session
// rows and the steps reconcile the rest before the service serves. The evidence runner is then built over the store,
// the steps and the pools, and takes up the open evidence runs, so no judge starts beside a step already re-adopted.
// The node checks are built over the store and the pools the same way, and take up the open checks before the flow
// starts. The flow then runs over the store until stop, with the planner and the build runner as its planner and
// builder, and its attention loop routes its epic notices through router when router is not nil. The limit notices tell
// router which steps a usage limit holds, every limitTick until stop; the due pass resumes each due row whose reset has
// passed, every limitTick until stop; the epic summaries write each stored epic's missing summary through haiku, every
// limitTick until stop; the session titles write each headless session's missing title through haiku, every limitTick
// until stop; the manager stops idle sessions, until stop; and the caller-event passes tell each step's caller when the
// step ends, and each epic's creator of its events, every five seconds until stop (startCallerEvents). A readopt,
// reconcile, restore or resume failure is logged and serving goes on; a failed restore serves no Pools, which refuse as
// unavailable, and the gates fail for want of pools. The Notices service on it answers from notices, and refuses as
// unavailable when notices is nil. With st open, each call of the Epics service on it that names an epic records its
// session in st.
func startSessions(
	ctx context.Context,
	opts options,
	hostName string,
	st *store.Store,
	router enginehost.Router,
	notices enginehost.NoticeSource,
	logger *slog.Logger,
	getenv func(string) string,
) (*sessionHost, error) {
	host := &sessionHost{
		logger:        logger,
		writerDone:    make(chan struct{}),
		idleDone:      make(chan struct{}),
		flowDone:      make(chan struct{}),
		limitsDone:    make(chan struct{}),
		dueDone:       make(chan struct{}),
		summariesDone: make(chan struct{}),
		reportsDone:   make(chan struct{}),
		serveDone:     make(chan error, 1),
	}
	var commands enginehost.Commands
	var stepCommands enginehost.StepCommands
	var epics enginehost.EpicCommands
	var evidence enginehost.EvidenceCommands
	if st != nil {
		stateDir, childEnviron, err := sessionState(getenv)
		if err != nil {
			return nil, err
		}
		host.writer = enginehost.NewWriter(enginehost.WriterConfig{
			Applier: st, Tick: opts.writeTick, Logger: logger, Metrics: opts.metrics,
		})
		notifier := enginehost.NewNotifier(enginehost.NotifierConfig{Route: router})
		host.manager = enginehost.NewManager(enginehost.Config{
			Shell:       opts.sessionShell,
			Claude:      opts.claude,
			Environ:     childEnviron,
			HolderScope: opts.holderScope,
			StateDir:    stateDir,
			ManagedDir:  managedDir,
			Cap:         opts.sessionCap,
			StepCap:     opts.stepCap,
			Writer:      host.writer,
			Notifier:    notifier,
			Store:       st,
			Logger:      logger,
			Metrics:     opts.metrics,
		})
		host.startBroker(ctx, hostName, st, logger)
		// host.pools is nil when the broker's restore failed, which leaves the gates no pools to run under.
		resolve := planner.NewResolver(os.Environ(), managedDir)
		haiku := enginehost.NewHaiku(enginehost.HaikuConfig{Claude: opts.claude, Environ: os.Environ()})
		steps, err := enginehost.NewSteps(enginehost.StepsConfig{
			Sessions:     host.manager,
			Store:        st,
			Writer:       host.writer,
			Pools:        host.pools,
			Worktrees:    filepath.Join(stateDir, "step-worktrees"),
			Environ:      childEnviron,
			Logger:       logger,
			Haiku:        haiku,
			GateStateDir: filepath.Join(stateDir, "gates"),
			GateScope:    opts.holderScope,
			ReadoptCheck: readoptCheck(st, resolve),
			ReadoptGate:  readoptGate(st),
		})
		if err != nil {
			host.stopBroker()
			return nil, fmt.Errorf("step engine: %w", err)
		}
		host.steps = steps
		st.SetEngineLive(host.manager.Live)
		commands, stepCommands = host.manager, host.steps
		epics = st
		host.startWriter(ctx)
		host.reconcile(ctx, logger, opts.holdReconcile, opts.flight, opts.flightNow)
		host.startCallerEvents(ctx, st, opts, logger)
		host.startEvidence(ctx, st, stateDir, opts.holderScope, childEnviron, logger)
		evidence = host.evidence
		host.startNodeChecks(ctx, st, stateDir, opts.holderScope, childEnviron, logger)
		host.startFlow(ctx, st, router, opts.attentionTicks, opts.flowTicks, getenv, resolve, stateDir, logger)
		host.startLimits(ctx, st, router, opts.limitTicks, logger)
		host.startSummaries(ctx, st, haiku, logger)
		host.startTitles(ctx, st, haiku, logger)
		host.startReports(ctx, st, haiku, logger)
		host.startDue(ctx, st, opts.dueNow, logger)
		host.startIdle(ctx)
	}
	ln, err := enginehost.Listen(protocol.SessionSocketPath(opts.socket))
	if err != nil {
		host.shutdown()
		return nil, fmt.Errorf("session socket: %w", err)
	}
	var serveCtx context.Context
	serveCtx, host.stopServe = context.WithCancel(ctx)
	go func() {
		host.serveDone <- enginehost.Serve(
			serveCtx, ln, commands, stepCommands, epics, host.pools, evidence, notices, serveOpts(opts, st, logger)...,
		)
	}()
	return host, nil
}

// serveOpts returns the handler options the session socket serves with: opts.metricsHandlers, and with st open, the
// interceptor that records the session of each epic call that names an epic.
func serveOpts(opts options, st *store.Store, logger *slog.Logger) []connect.HandlerOption {
	if st == nil {
		return opts.metricsHandlers
	}
	interceptor := enginehost.EpicCallInterceptor(st, time.Now, logger)
	return append(slices.Clip(opts.metricsHandlers), connect.WithInterceptors(interceptor))
}

// readoptCheck is the ReadoptCheck of the steps. A re-adopted row gets the planner's return check when it is the step of a
// running planner turn, and when that check is nil, the release integrator's check when it is a release integrator
// step, and then the build's verifier or orchestrator check. An error from any of them is returned.
func readoptCheck(
	st *store.Store,
	resolve planner.Resolver,
) func(ctx context.Context, row store.StepRow) (func(value json.RawMessage) []string, error) {
	plan := planner.ReadoptCheck(st, resolve)
	integrate := interpret.ReadoptCheck(st)
	verify := build.ReadoptCheck(st)
	return func(ctx context.Context, row store.StepRow) (func(value json.RawMessage) []string, error) {
		check, err := plan(ctx, row)
		if err != nil || check != nil {
			return check, err
		}
		check, err = integrate(ctx, row)
		if err != nil || check != nil {
			return check, err
		}
		return verify(ctx, row)
	}
}

// readoptGate is the ReadoptGate of the steps. A re-adopted row gets the judge gate of an evidence run when it is a judge
// step, and when that gate is nil, the review gate of a task's review step. An error from either is returned.
func readoptGate(st *store.Store) func(ctx context.Context, row store.StepRow) (*gates.Gate, error) {
	judge := enginehost.EvidenceReadoptGate(st)
	review := build.ReadoptGate(st)
	return func(ctx context.Context, row store.StepRow) (*gates.Gate, error) {
		gate, err := judge(ctx, row)
		if err != nil || gate != nil {
			return gate, err
		}
		return review(ctx, row)
	}
}

// reconcile re-adopts the open step rows a previous run left, then reconciles the session rows and the step rows that no
// re-adoption holds. A failure is logged and serving goes on. hold, when not nil, is called first and returns when the
// reconcile may start. The flight recorder flight, when not nil, writes a reconcile trace when the whole call, hold
// included, takes longer than its threshold on the clock now.
func (h *sessionHost) reconcile(
	ctx context.Context,
	logger *slog.Logger,
	hold func(context.Context),
	flight *telemetry.Flight,
	now func() time.Time,
) {
	start := now()
	defer func() { observeFlight(ctx, logger, flight, "reconcile", now().Sub(start)) }()
	if hold != nil {
		hold(ctx)
	}
	if err := h.steps.Readopt(ctx, h.manager); err != nil {
		logger.ErrorContext(ctx, "readopt steps, serving anyway", "error", err)
	}
	if err := h.manager.Reconcile(ctx); err != nil {
		logger.ErrorContext(ctx, "reconcile sessions, serving anyway", "error", err)
	}
	if err := h.steps.Reconcile(ctx); err != nil {
		logger.ErrorContext(ctx, "reconcile steps, serving anyway", "error", err)
	}
}

// startBroker builds the grants broker for hostName over st and restores the grants a previous run stored. A restore
// that succeeds makes the broker the host's pools and starts its tick loop. A restore that fails is logged and leaves
// the pools nil, so no Pools are served; the broker is still closed at shutdown, since its restore may have started
// member commands. The broker closes as soon as ctx ends: the signal that ends ctx also starts Serve's shutdown, whose
// Close after shutdownWait cancels each held Acquire, and an Acquire canceled while the broker is open ends its grant.
func (h *sessionHost) startBroker(ctx context.Context, hostName string, st *store.Store, logger *slog.Logger) {
	h.broker = grants.NewBroker(grants.Config{
		Host:        hostName,
		Store:       st,
		Environ:     os.Environ(),
		Logger:      logger,
		HolderAlive: holderLives,
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

// holderLives reports whether the holder that serves the holder directory dir is running. A gate's grant names the
// orreryd that started it as its client, which ends with a SIGKILL, so the gate's holder is what keeps the grant a
// restarted broker restores until the gate exits. A holder holds its session lock for its whole life, from before it
// starts its child: its pids record is written only after the child starts, so a holder whose child has already run
// may have none yet. The test is a non-blocking flock on the lock file, which the holder's flock refuses. The file is
// opened, never created, so a directory with no lock file has no live holder, and a free lock means the holder has
// ended: the kernel frees a flock when its process dies.
func holderLives(dir string) bool {
	lock, err := os.OpenFile(filepath.Join(dir, holder.LockName), os.O_RDONLY, 0)
	if err != nil {
		return false
	}
	defer lock.Close()
	err = syscall.Flock(int(lock.Fd()), syscall.LOCK_SH|syscall.LOCK_NB)
	return errors.Is(err, syscall.EWOULDBLOCK)
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
	h.phase("broker", h.stopBroker)
	var serveErr error
	h.phase("session socket", func() {
		h.stopServe()
		serveErr = <-h.serveDone
	})
	h.shutdown()
	if serveErr != nil {
		return fmt.Errorf("session service: %w", serveErr)
	}
	return nil
}

// sessionState returns the state directory getenv names and the environment of every child a holder runs: sessions,
// steps and gates alike build with Gradle daemonless (enginehost.GradleEnviron), since a holder's scope is stopped when
// the holder ends, which would end a daemon another step's build runs in.
func sessionState(getenv func(string) string) (string, []string, error) {
	stateDir, err := stateDirOf(getenv)
	if err != nil {
		return "", nil, fmt.Errorf("step worktrees: %w", err)
	}
	environ, err := enginehost.GradleEnviron(os.Environ(), stateDir, enginehost.UserGradleHome(os.Getenv))
	if err != nil {
		return "", nil, fmt.Errorf("session environment: %w", err)
	}
	return stateDir, environ, nil
}

// startEvidence builds the evidence runner over st, the steps and the pools, with its blobs and capture worktrees under
// stateDir and its gates under stateDir/gates, and takes up the open evidence runs a previous run left. A failure to take
// them up is logged and serving goes on. Its gates run in environ.
func (h *sessionHost) startEvidence(
	ctx context.Context,
	st *store.Store,
	stateDir, holderScope string,
	environ []string,
	logger *slog.Logger,
) {
	h.evidence = enginehost.NewEvidence(enginehost.EvidenceConfig{
		Store:        st,
		Steps:        h.steps,
		Pools:        h.pools,
		Environ:      environ,
		StateDir:     stateDir,
		GateStateDir: filepath.Join(stateDir, "gates"),
		GateScope:    holderScope,
		Logger:       logger,
	})
	if err := h.evidence.Resume(ctx); err != nil {
		logger.ErrorContext(ctx, "resume evidence runs, serving anyway", "error", err)
	}
}

// startNodeChecks builds the node checks runner over st and the pools, with its check worktrees under stateDir and its
// gates under stateDir/gates, and takes up the open checks a previous run left. A failure to take them up is logged and
// serving goes on. Its gates run in environ.
func (h *sessionHost) startNodeChecks(
	ctx context.Context,
	st *store.Store,
	stateDir, holderScope string,
	environ []string,
	logger *slog.Logger,
) {
	h.checks = enginehost.NewNodeChecks(enginehost.NodeChecksConfig{
		Store:        st,
		Pools:        h.pools,
		Environ:      environ,
		StateDir:     stateDir,
		GateStateDir: filepath.Join(stateDir, "gates"),
		GateScope:    holderScope,
		Logger:       logger,
	})
	if err := h.checks.Resume(ctx); err != nil {
		logger.ErrorContext(ctx, "resume node checks, serving anyway", "error", err)
	}
}

// startFlow runs the interpreter over st until stop, with the planner and the build runner it builds over st and the
// steps as its planner and builder. The planner reads the agent from the role file on each pass; without one it parks
// each epic with pending triggers through a turn that starts no step. Each planned task's gate and judge are resolved
// by resolve. The build runner moves the builds the flow gives it through the steps and the evidence runner. The check,
// evidence and agent nodes of the flow run on the node checks, the evidence runner and the steps, and read the
// environment through getenv. With a
// router, the attention loop runs under the same context and routes the epics that need their owner through it,
// passing at each value of attentionTicks when it is not nil. With flowTicks set, the flow passes at start and once per
// SIGUSR1, and logs each pass to the file flowTicks names (flowSeam).
func (h *sessionHost) startFlow(
	ctx context.Context,
	st *store.Store,
	router planner.Router,
	attentionTicks <-chan time.Time,
	flowTicks string,
	getenv func(string) string,
	resolve planner.Resolver,
	stateDir string,
	logger *slog.Logger,
) {
	plannerRun := planner.New(planner.Config{
		Store:   st,
		Steps:   h.steps,
		Agent:   func() (string, error) { return roles.Agent(getenv, "planner") },
		Logger:  logger,
		Resolve: resolve,
	})
	buildRun := build.New(build.Config{
		Store:    st,
		Steps:    h.steps,
		Evidence: h.evidence,
		Checks:   h.checks,
		Getenv:   getenv,
		StateDir: stateDir,
		Logger:   logger,
	})
	var flowCtx context.Context
	flowCtx, h.stopFlow = context.WithCancel(ctx)
	ticks, passed := flowSeam(flowCtx, flowTicks, logger)
	flow := interpret.New(interpret.Config{
		Store:     st,
		Planner:   plannerRun,
		Builder:   buildRun,
		Checks:    h.checks,
		Evidence:  h.evidence,
		Steps:     h.steps,
		Reviewers: interpret.NewEpicReviewers(),
		Ending:    st,
		StateDir:  stateDir,
		Getenv:    getenv,
		Logger:    logger,
		Ticks:     ticks,
		Passed:    passed,
	})
	go func() {
		flow.Run(flowCtx)
		close(h.flowDone)
	}()
	if router == nil {
		return
	}
	attention := planner.NewAttention(planner.AttentionConfig{
		Store: st, Router: router, Ticks: attentionTicks, Logger: logger,
	})
	h.attentionDone = make(chan struct{})
	go func() {
		attention.Run(flowCtx)
		close(h.attentionDone)
	}()
}

// flowSeam returns the test seam of the flow when path is set: the channel the flow passes at, which gets one value per
// SIGUSR1, and the function the flow calls after each pass, which appends a line to path. The SIGUSR1 handler is
// installed here, before the flow starts, and it is stopped when ctx ends. Without a path it returns nil and nil, and
// SIGUSR1 keeps its default action.
func flowSeam(ctx context.Context, path string, logger *slog.Logger) (<-chan time.Time, func()) {
	if path == "" {
		return nil, nil
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGUSR1)
	ticks := make(chan time.Time)
	go forwardSignals(ctx, signals, ticks)
	return ticks, func() { appendPassLine(ctx, logger, path) }
}

// forwardSignals sends one value on ticks for each signal it receives, until ctx ends. It stops the signals when it
// returns.
func forwardSignals(ctx context.Context, signals chan os.Signal, ticks chan<- time.Time) {
	defer signal.Stop(signals)
	for {
		select {
		case <-ctx.Done():
			return
		case <-signals:
		}
		select {
		case <-ctx.Done():
			return
		case ticks <- time.Now():
		}
	}
}

// appendPassLine appends one line to the file at path, which logs each pass of the flow. A failed write is logged, and
// serving goes on.
func appendPassLine(ctx context.Context, logger *slog.Logger, path string) {
	file, err := os.OpenFile(filepath.Clean(path), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err == nil {
		_, err = file.WriteString("pass\n")
		err = errors.Join(err, file.Close())
	}
	if err != nil {
		logger.ErrorContext(ctx, "write flow pass line, serving anyway", "path", path, "error", err)
	}
}

// startLimits tells router which epics a usage limit holds, checking st every limitTick until stop, or at each value
// ticks delivers when ticks is not nil. The marks st holds say which limits a previous orreryd told, and those are not
// told again.
func (h *sessionHost) startLimits(
	ctx context.Context,
	st *store.Store,
	router enginehost.Router,
	ticks <-chan time.Time,
	logger *slog.Logger,
) {
	stopTicker := func() {}
	if ticks == nil {
		ticker := time.NewTicker(limitTick)
		ticks, stopTicker = ticker.C, ticker.Stop
	}
	limits := enginehost.NewLimitNotices(enginehost.LimitNoticesConfig{
		Steps:  st,
		Epics:  st,
		Told:   st,
		Route:  router,
		Now:    time.Now,
		Tick:   ticks,
		Logger: logger,
	})
	var limitsCtx context.Context
	limitsCtx, h.stopLimits = context.WithCancel(ctx)
	go func() {
		defer close(h.limitsDone)
		defer stopTicker()
		limits.Run(limitsCtx)
	}()
}

// startCallerEvents runs the step caller-event pass and the epic caller-event pass over st until shutdown, each every
// five seconds, or at each value of opts.stepEventTicks and opts.epicEventTicks when those are not nil. Both send
// through one CallerSender: a headless caller gets its event as a prompt through the manager, a pane caller gets it
// through cli.WakePane, and a step caller is only marked. Neither depends on a router, since a caller event goes to its
// caller alone.
func (h *sessionHost) startCallerEvents(ctx context.Context, st *store.Store, opts options, logger *slog.Logger) {
	sender := enginehost.NewCallerSender(enginehost.CallerSenderConfig{
		Deliver:  h.manager.Deliver,
		WakePane: cli.WakePane,
		Store:    st,
		Now:      time.Now,
		Logger:   logger,
	})
	steps := enginehost.NewStepCallerEvents(enginehost.StepCallerEventsConfig{
		Store: st, Sender: sender, Ticks: opts.stepEventTicks, Logger: logger,
	})
	epics := planner.NewEpicCallerEvents(planner.EpicCallerEventsConfig{
		Store: st, Sender: sender, Ticks: opts.epicEventTicks, Logger: logger,
	})
	var eventsCtx context.Context
	eventsCtx, h.stopCallerEvents = context.WithCancel(ctx)
	h.stepEventsDone = make(chan struct{})
	h.epicEventsDone = make(chan struct{})
	go func() {
		defer close(h.stepEventsDone)
		steps.Run(eventsCtx)
	}()
	go func() {
		defer close(h.epicEventsDone)
		epics.Run(eventsCtx)
	}()
}

// startIdle runs the manager's stop of idle sessions until stop.
func (h *sessionHost) startIdle(ctx context.Context) {
	var idleCtx context.Context
	idleCtx, h.stopIdle = context.WithCancel(ctx)
	go func() {
		h.manager.RunIdle(idleCtx)
		close(h.idleDone)
	}()
}

// startWriter runs the host's writer until stopWriter, on a context that outlives ctx's cancel so a shutdown can
// still flush, and closes writerDone when the writer returns.
func (h *sessionHost) startWriter(ctx context.Context) {
	var writerCtx context.Context
	writerCtx, h.stopWriter = context.WithCancel(context.WithoutCancel(ctx))
	go func() {
		h.writer.Run(writerCtx)
		close(h.writerDone)
	}()
}

// startDue resumes the due rows over st every limitTick until stop, and once when it starts, by the clock now, or
// time.Now when now is nil. Its steps are the host's steps, which startSessions has re-adopted before it starts the
// pass.
func (h *sessionHost) startDue(ctx context.Context, st *store.Store, now func() time.Time, logger *slog.Logger) {
	if now == nil {
		now = time.Now
	}
	ticker := time.NewTicker(limitTick)
	due := enginehost.NewDuePass(enginehost.DuePassConfig{
		Store:  st,
		Steps:  h.steps,
		Now:    now,
		Tick:   ticker.C,
		Logger: logger,
	})
	var dueCtx context.Context
	dueCtx, h.stopDue = context.WithCancel(ctx)
	go func() {
		defer close(h.dueDone)
		defer ticker.Stop()
		due.Run(dueCtx)
	}()
}

// startSummaries writes the summary of each stored epic that has none, through haiku, checking st every limitTick until
// stop. The first check runs at start.
func (h *sessionHost) startSummaries(
	ctx context.Context,
	st *store.Store,
	haiku *enginehost.Haiku,
	logger *slog.Logger,
) {
	ticker := time.NewTicker(limitTick)
	summaries := enginehost.NewEpicSummaries(enginehost.EpicSummariesConfig{
		Store:  st,
		Haiku:  haiku,
		Tick:   ticker.C,
		Logger: logger,
	})
	var summariesCtx context.Context
	summariesCtx, h.stopSummaries = context.WithCancel(ctx)
	go func() {
		defer close(h.summariesDone)
		defer ticker.Stop()
		summaries.Run(summariesCtx)
	}()
}

// startTitles writes the title of each headless session that has none, from its first prompt through haiku, checking st
// every limitTick until stop. The first check runs at start.
func (h *sessionHost) startTitles(
	ctx context.Context,
	st *store.Store,
	haiku *enginehost.Haiku,
	logger *slog.Logger,
) {
	ticker := time.NewTicker(limitTick)
	titles := enginehost.NewSessionTitles(enginehost.SessionTitlesConfig{
		Store:  st,
		Haiku:  haiku,
		Tick:   ticker.C,
		Logger: logger,
	})
	var titlesCtx context.Context
	titlesCtx, h.stopTitles = context.WithCancel(ctx)
	h.titlesDone = make(chan struct{})
	go func() {
		defer close(h.titlesDone)
		defer ticker.Stop()
		titles.Run(titlesCtx)
	}()
}

// startReports writes the report of each ended epic that has none, through haiku, checking st every limitTick until
// stop. The first check runs at start.
func (h *sessionHost) startReports(
	ctx context.Context,
	st *store.Store,
	haiku *enginehost.Haiku,
	logger *slog.Logger,
) {
	ticker := time.NewTicker(limitTick)
	reports := enginehost.NewEpicReports(enginehost.EpicReportsConfig{
		Store:  st,
		Haiku:  haiku,
		Tick:   ticker.C,
		Logger: logger,
	})
	var reportsCtx context.Context
	reportsCtx, h.stopReports = context.WithCancel(ctx)
	go func() {
		defer close(h.reportsDone)
		defer ticker.Stop()
		reports.Run(reportsCtx)
	}()
}

// shutdown stops the broker, stops the idle check, stops the flow and waits for it to return, which waits for the
// planner's turns, stops the limit notices and waits for the attention loop, the limit loop, the due pass, the epic
// summaries, the session titles and the epic reports to return, stops the caller-event passes and waits for each to
// return, closes the node checks and the evidence runner and waits for their drives, ends every step loop, stops every
// live session, and ends the writer after its last flush. The notices the sessions started are waited on in serve.
// Without a broker, manager and writer it does nothing.
func (h *sessionHost) shutdown() {
	h.stopBroker()
	if h.manager != nil {
		h.phase("idle check", func() {
			h.stopIdle()
			<-h.idleDone
		})
		h.phase("flow", func() {
			h.stopFlow()
			<-h.flowDone
		})
		h.phase("node checks", h.checks.Close)
		h.phase("evidence", h.evidence.Close)
		if h.attentionDone != nil {
			h.phase("attention", func() { <-h.attentionDone })
		}
		h.phase("limit notices", func() {
			h.stopLimits()
			<-h.limitsDone
		})
		h.phase("due pass", func() {
			h.stopDue()
			<-h.dueDone
		})
		h.phase("epic summaries", func() {
			h.stopSummaries()
			<-h.summariesDone
		})
		h.phase("session titles", func() {
			h.stopTitles()
			<-h.titlesDone
		})
		h.phase("epic reports", func() {
			h.stopReports()
			<-h.reportsDone
		})
		h.phase("step caller events", func() {
			h.stopCallerEvents()
			<-h.stepEventsDone
		})
		h.phase("epic caller events", func() { <-h.epicEventsDone })
		h.phase("steps", h.steps.Close)
		h.phase("sessions", h.manager.Close)
	}
	if h.writer != nil {
		h.phase("writer", func() {
			h.stopWriter()
			<-h.writerDone
		})
	}
}

// phase runs the shutdown phase name through shutdownPhase on the host's logger, or on a discarding one when the host
// has none.
func (h *sessionHost) phase(name string, phase func()) {
	logger := h.logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	shutdownPhase(logger, name, phase)
}

// openStore opens the store at its resolved path and logs "store open" once the store is open and migrated. When the
// path does not resolve or the store cannot be opened, it logs the error and returns nil, so orreryd serves hooks and
// notifications without the store. The store warns on logger of each call over 100 ms and counts it on metrics.
func openStore(
	ctx context.Context,
	getenv func(string) string,
	logger *slog.Logger,
	metrics *telemetry.Daemon,
) *store.Store {
	path, err := store.Path(getenv)
	if err != nil {
		logger.ErrorContext(ctx, "store unavailable, serving without it", "error", err)
		return nil
	}
	st, err := store.Open(ctx, path, store.WithLogger(logger), store.WithSlowCallCounter(metrics.StoreSlowCall))
	if err != nil {
		logger.ErrorContext(ctx, "store unavailable, serving without it", "path", path, "error", err)
		return nil
	}
	logger.InfoContext(ctx, "store open", "path", path)
	return st
}

// attachStore attaches st to the mirror and to notices, which keep each key's last push in it. When the store opened,
// it also attaches st to the router, which stores the notices already pending and restores the ones a previous
// orreryd left. The router comes second, so a push it resends at its Attach has its time stored. With no store,
// notices gets an untyped nil, so it keeps no rows. serve calls it before the sessions start, so a reconciled
// session's notice is newer than any it restores.
func attachStore(
	ctx context.Context,
	st *store.Store,
	mirror *daemon.Mirror,
	notices *notice.Notices,
	router *route.Router,
) {
	mirror.Attach(st)
	if st == nil {
		notices.Attach(ctx, nil)
		return
	}
	notices.Attach(ctx, st)
	router.Attach(ctx, st)
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
	webhook     *alerts.Webhook  // the POST /alerts intake that routes to router; nil without --metrics-listen
	summarizers map[tmuxctl.Server]*summary.Summarizer
	views       *doneViews // each done's viewing check, handed from observe to the autoseen observer
}

// viewTimeout bounds the viewing check of one done transition, which runs on the hook path.
const viewTimeout = 2 * time.Second

// doneView is the one viewing check a done transition makes: whether a client of the pane's tmux server shows the pane
// with recent input, or the error of the check. The notice's first watching check and the autoseen observer both take
// it, so a done reads its pane's window and clients once.
type doneView struct {
	viewing bool
	err     error
}

// doneViews hands each pane's done view from the relay's observer to the autoseen observer, which the daemon calls
// next for the same transition: it calls a transition's observers in order, one transition at a time. A nil doneViews
// keeps nothing.
type doneViews struct {
	mu    sync.Mutex
	views map[string]doneView
}

// newDoneViews returns an empty doneViews.
func newDoneViews() *doneViews {
	return &doneViews{views: make(map[string]doneView)}
}

// put keeps view as pane's done view until it is taken.
func (v *doneViews) put(pane string, view doneView) {
	if v == nil {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.views[pane] = view
}

// take returns and forgets pane's done view, and reports whether one was kept.
func (v *doneViews) take(pane string) (doneView, bool) {
	if v == nil {
		return doneView{}, false
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	view, ok := v.views[pane]
	delete(v.views, pane)
	return view, ok
}

// viewer is the autoseen observer's Viewer: the done view the relay's observer kept for pane, or a fresh check when it
// kept none.
func (r *relay) viewer(ctx context.Context, client *tmuxctl.Client, pane string, now time.Time) (bool, error) {
	if view, ok := r.views.take(pane); ok {
		return view.viewing, view.err
	}
	viewing, err := autoseen.Viewing(ctx, client, pane, now)
	if err != nil {
		return false, fmt.Errorf("pane viewing: %w", err)
	}
	return viewing, nil
}

// viewDone checks whether a client of the transition's tmux server shows its pane with recent input, under
// viewTimeout.
func (r *relay) viewDone(ctx context.Context, transition daemon.Transition) doneView {
	client, err := tmuxClient(transition.Server)
	if err != nil {
		return doneView{err: err}
	}
	ctx, cancel := context.WithTimeout(ctx, viewTimeout)
	defer cancel()
	viewing, err := autoseen.Viewing(ctx, client, transition.PaneID, time.Now())
	if err != nil {
		return doneView{err: fmt.Errorf("pane viewing: %w", err)}
	}
	return doneView{viewing: viewing}
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
	var summarized <-chan struct{}
	if transition.To == protocol.StateWorking || transition.To == protocol.StateDone {
		summarized = r.summarize(ctx, transition)
	}
	var view *doneView
	if transition.To == protocol.StateDone {
		checked := r.viewDone(ctx, transition)
		r.views.put(transition.PaneID, checked)
		view = &checked
	}
	if transition.To == protocol.StateWaiting || transition.To == protocol.StateDone {
		r.notify(ctx, transition, summarized, view)
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
// request is the Stop run and carries the last assistant message. It returns the channel that closes once the request
// has settled (see summary.Summarizer.Request), or nil when no request was made.
func (r *relay) summarize(ctx context.Context, transition daemon.Transition) <-chan struct{} {
	transcript, err := hookString(transition.Frame.Hook, keyTranscriptPath)
	if err != nil {
		r.logger.WarnContext(ctx, "read transcript path", "pane", transition.PaneID, "error", err)
		return nil
	}
	summarizer, err := r.summarizerFor(transition.Server)
	if err != nil {
		r.logger.ErrorContext(ctx, "create summarizer", "error", err)
		return nil
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
			return nil
		}
	}
	return summarizer.Request(ctx, req)
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

// paneText is the text a notification carries, as stored on its pane, and the pane's session and window index.
type paneText struct {
	summary, label, question string
	session                  string
	window                   int
}

// readPaneText returns the summary, label and question options of pane, and its session name and window index, in one
// tmux invocation.
func readPaneText(ctx context.Context, client *tmuxctl.Client, pane string) (paneText, error) {
	values, err := client.PaneValues(ctx, pane,
		protocol.OptionSummary, protocol.OptionLabel, protocol.OptionQuestion, "session_name", "window_index")
	if err != nil {
		return paneText{}, fmt.Errorf("read pane %s: %w", pane, err)
	}
	window, err := strconv.Atoi(values[4])
	if err != nil {
		return paneText{}, fmt.Errorf("window index %q: %w", values[4], err)
	}
	return paneText{summary: values[0], label: values[1], question: values[2], session: values[3], window: window}, nil
}

// notify hands the transition's notice to the router, with view as its first watching check when it is not nil. A done notice whose Stop left a subagent or workflow running is
// silent. Any other done is judged before it routes. A hook that does not decode still routes, and its decode error is
// logged. A notice that routes keeps its transition as its source, so a restart can resume it; a silent one never
// pends, so it needs none.
func (r *relay) notify(ctx context.Context, transition daemon.Transition, summarized <-chan struct{}, view *doneView) {
	incoming := r.paneNotice(ctx, transition, summarized, view)
	if incoming.Silent == "" {
		if data, err := encodePaneSource(transition); err != nil {
			r.logger.ErrorContext(ctx, "encode pane notice", "pane", transition.PaneID, "error", err)
		} else {
			incoming.Source = route.Source{Kind: paneSourceKind, Data: data}
		}
	}
	r.router.Notify(ctx, incoming)
}

// newDoneJudge returns the judge of done notices, which asks claude for each verdict under the per-context models.
func newDoneJudge(claude string, models map[string]string) *donejudge.Judge {
	return donejudge.New(enginehost.NewHaiku(enginehost.HaikuConfig{Claude: claude, Environ: os.Environ()}), models)
}

// serveRouteInputs is what serve gives routeConfig besides the viewing tracker.
type serveRouteInputs struct {
	Host       string
	JudgeDelay time.Duration
	Now        func() time.Time
	After      func(d time.Duration, f func()) (stop func() bool)
	Desk       route.DeskSink
	Phone      route.PhoneSink
	Pages      route.PageSink
	Subscribed func() (bool, error)
	Log        route.Recorder
	Logger     *slog.Logger
}

// routeConfig returns the route.Config that serve builds its router from. Its Viewing check is viewing.viewed, so a
// notice whose key the page views is held. serve and the viewing tests both build the router through it.
func routeConfig(in serveRouteInputs, viewing *viewTracker) route.Config {
	return route.Config{
		Host:       in.Host,
		Now:        in.Now,
		After:      in.After,
		JudgeDelay: in.JudgeDelay,
		Desk:       in.Desk,
		Phone:      in.Phone,
		Pages:      in.Pages,
		Viewing:    viewing.viewed,
		Subscribed: in.Subscribed,
		Log:        in.Log,
		Logger:     in.Logger,
	}
}

// newRelay returns hub with the router cfg builds, which hub routes its notices through. The router restores each
// stored notice through the rebuild of its source kind: a session notice through enginehost.RestoreNotice, a limit
// notice through enginehost.RestoreLimitNotice, an epic notice through planner.RestoreNotice, a pane notice through
// hub's rebuildPane. So hub is built first, and cfg's Rebuild is set here. A set webhook attaches to the router.
func newRelay(cfg route.Config, hub *relay) *relay {
	cfg.Rebuild = map[string]route.Rebuild{
		enginehost.SessionSource: enginehost.RestoreNotice,
		enginehost.LimitSource:   enginehost.RestoreLimitNotice,
		planner.EpicSource:       planner.RestoreNotice,
		paneSourceKind:           hub.rebuildPane,
	}
	hub.router = route.New(cfg)
	if hub.webhook != nil {
		hub.webhook.Attach(hub.router)
	}
	return hub
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
	body, fallback := text.question, waitingBody
	if transition.To == protocol.StateDone {
		body, fallback = text.summary, doneBody
	}
	if body == "" {
		body = fallback
	}
	return deskstream.Notice{
		State: string(transition.To),
		Title: deskstream.PaneTitle(filepath.Base(transition.Frame.Cwd), text.session, text.window),
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

// wait returns once every summary run has finished. It runs after Serve returns, so no observer can start more work.
func (r *relay) wait() {
	for _, summarizer := range r.summarizers {
		summarizer.Wait()
	}
}
