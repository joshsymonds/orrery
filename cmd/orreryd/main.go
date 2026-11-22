// Command orreryd is the orrery daemon. It listens on a unix socket for seen, read and input reports, applies each
// session registry update to the pane it names, starts summaries, and routes waiting and done notifications to the
// desk stream and Web Push. With --sessions it also serves the engine session socket: the enginehost Manager, Steps,
// Writer, pools and evidence, and the planner and build runners. With --metrics-listen it serves /metrics and POST
// /alerts on that address.
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
	"sync/atomic"
	"syscall"
	"time"

	"connectrpc.com/connect"

	"github.com/joshsymonds/orrery/internal/alerts"
	"github.com/joshsymonds/orrery/internal/autoseen"
	"github.com/joshsymonds/orrery/internal/build"
	"github.com/joshsymonds/orrery/internal/cli"
	"github.com/joshsymonds/orrery/internal/closurejudge"
	"github.com/joshsymonds/orrery/internal/daemon"
	"github.com/joshsymonds/orrery/internal/deskstream"
	"github.com/joshsymonds/orrery/internal/donejudge"
	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/gates"
	"github.com/joshsymonds/orrery/internal/grants"
	"github.com/joshsymonds/orrery/internal/holder"
	"github.com/joshsymonds/orrery/internal/hostexec"
	"github.com/joshsymonds/orrery/internal/inapp"
	"github.com/joshsymonds/orrery/internal/interpret"
	"github.com/joshsymonds/orrery/internal/notice"
	"github.com/joshsymonds/orrery/internal/paneregistry"
	"github.com/joshsymonds/orrery/internal/panewatch"
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

// envAnthropicBaseURL is the settings env key that sets claude's API base URL.
const envAnthropicBaseURL = "ANTHROPIC_BASE_URL"

// envNotifySocket is the variable systemd sets to the socket that takes READY=1 for a Type=notify unit.
const envNotifySocket = "NOTIFY_SOCKET"

// managedDir is the managed settings root the session manager runs under and the planner's resolver checks agents in.
const managedDir = "/etc/claude-code"

const (
	defaultClaude = "claude"
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
	// silentNotWanted is why a notice goes nowhere when push.Wanted does not want its state.
	silentNotWanted = "state not wanted"
	// storeOpenRetry is how long openStore waits before it tries a busy store open again.
	storeOpenRetry = 2 * time.Second
	// waitingBody and doneBody stand in for an empty question or summary.
	waitingBody = "waiting for input"
	doneBody    = "done"
)

const usageText = `usage: orreryd [-L NAME | -S PATH] [--socket PATH] [--claude PATH] [--summary-model CONTEXT=MODEL]...
               [--push-host HOST]... [--sessions] [--session-shell PATH]
               [--holder-scope PATH] [--metrics-listen ADDR]

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
  --session-shell PATH
                    absolute login shell that starts each session (default $SHELL)
  --holder-scope PATH
                    systemd-run binary that runs each session's holder in its own user scope
                    (default: none, the holder starts plainly)
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
	sessionShell    string
	holderScope     string
	judgeDelay      time.Duration           // the --judge-delay test seam's wait, route.JudgeDelay by default
	storeBusy       time.Duration           // the --store-busy-timeout test seam's wait, store.BusyTimeout by default
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
	// epicWrites is a test seam: when set, the store's epics-changed hook calls it with the epics a write moved, after
	// the flow's wake and the due pass's wake. Production leaves it nil.
	epicWrites func(epics []string)
	// storeRetryAfter is a test seam: openStore waits on the channel it returns for a busy store open to be tried again,
	// in place of time.After. Production leaves it nil.
	storeRetryAfter func(time.Duration) <-chan time.Time
	// dueNow is a test seam: the clock the due pass reads to decide which limited steps are due. Production leaves it
	// nil, which is time.Now. A test whose steps carry fixed reset dates sets it, so the real date never makes them due.
	dueNow func() time.Time
	// relayNow is a test seam: the clock the notice relay's watching and viewing checks read as now (relay.now).
	// Production leaves it nil, which is time.Now. A test that reports a read sets it to the read's second, so a loaded
	// machine never ages the read out of readWindow before the test ends.
	relayNow func() time.Time
	// cronNow is a test seam: the clock the cron answers and the cron scheduler read as now. Production leaves it nil,
	// which is time.Now.
	cronNow func() time.Time
	// cronTimers is a test seam: the timers the cron scheduler arms in place of time.NewTimer. Production leaves it nil.
	cronTimers func(time.Duration) enginehost.CronTimer
	// cronDeliver is a test seam: the delivery the cron scheduler calls in place of Manager.DeliverCron. Production
	// leaves it nil.
	cronDeliver func(context.Context, enginehost.DeliverRequest) error
	// releaseSlotNow is a test seam: the clock the release slots read as now. Production leaves it nil, which is time.Now.
	releaseSlotNow func() time.Time
	// releaseSlotTimers is a test seam: the timers the release slots arm in place of time.NewTimer. Production leaves it nil.
	releaseSlotTimers func(time.Duration) enginehost.CronTimer
	// host is a test seam: the host name the desk stream, the notices and the grants broker use in place of
	// os.Hostname. Production leaves it "".
	host string
	// flowTicks is a test seam: the path the flow appends a line to after each pass, and each SIGUSR1 starts a pass in
	// place of the flow's five-second ticks. Production leaves it "".
	flowTicks string
	flight    *telemetry.Flight // the flight recorder run starts, nil when it could not start
	started   time.Time         // when run began, the start of the start phase
	// callerEventPassed is a test seam: the function the caller-event delivery calls after each of its passes returns, so
	// a test counts the passes. Production leaves it nil.
	callerEventPassed func()
	// callerEventTimer is a test seam: the timer the caller-event delivery arms for its retry in place of time.NewTimer.
	// Production leaves it nil.
	callerEventTimer func(time.Duration) (<-chan time.Time, func() bool)
	// callerWakePane is a test seam: the function the caller-event delivery calls in place of cli.WakePane to send text
	// to a pane caller. Production leaves it nil.
	callerWakePane func(ctx context.Context, server, pane, text string) (bool, error)
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

// checkClockSeams refuses a negative --due-at or --relay-at as a usage error.
func checkClockSeams(dueAt, relayAt int64) error {
	if dueAt < 0 {
		return usagef("--due-at must not be negative, got %d", dueAt)
	}
	if relayAt < 0 {
		return usagef("--relay-at must not be negative, got %d", relayAt)
	}
	return nil
}

// unixClock is the clock of a test seam that names unix seconds, --due-at's or --relay-at's: nil, which is time.Now, for
// zero, and otherwise a clock stopped at unix second at.
func unixClock(at int64) func() time.Time {
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

// checkLimits refuses a negative --judge-delay or --store-busy-timeout, each as a usage error.
func checkLimits(judgeDelay, storeBusy time.Duration) error {
	if judgeDelay < 0 {
		return usagef("--judge-delay must not be negative, got %v", judgeDelay)
	}
	if storeBusy < 0 {
		return usagef("--store-busy-timeout must not be negative, got %v", storeBusy)
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
	sessionShell := flags.String("session-shell", getenv("SHELL"), "login shell that starts each session")
	holderScope := flags.String("holder-scope", "", "systemd-run binary that runs each session's holder")
	metricsListen := flags.String("metrics-listen", "", "loopback address that serves /metrics")
	flowTicks := flags.String("flow-ticks", "", "test seam: file each flow pass appends a line to")
	dueAt := flags.Int64("due-at", 0, "test seam: unix seconds the due pass reads as now")
	relayAt := flags.Int64("relay-at", 0, "test seam: unix seconds the notice relay's watching checks read as now")
	hostName := flags.String("host", "", "test seam: host name in place of the machine's")
	judgeDelay := flags.Duration("judge-delay", route.JudgeDelay, "test seam: wait of a done before its judge")
	storeBusy := flags.Duration("store-busy-timeout", store.BusyTimeout,
		"test seam: how long a store write waits for the write lock")
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
	if err := checkLimits(*judgeDelay, *storeBusy); err != nil {
		return options{}, err
	}
	if err := checkClockSeams(*dueAt, *relayAt); err != nil {
		return options{}, err
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
		sessionShell:  *sessionShell,
		holderScope:   *holderScope,
		judgeDelay:    *judgeDelay,
		storeBusy:     *storeBusy,
		metricsListen: *metricsListen,
		flowTicks:     *flowTicks,
		dueNow:        unixClock(*dueAt),
		relayNow:      unixClock(*relayAt),
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

// serveRelay returns the relay serve observes panes through: its done and closure judges run opts' claude and
// models, under home, and it mirrors through mirror.
func serveRelay(opts options, home string, logger *slog.Logger, mirror *daemon.Mirror) *relay {
	return &relay{
		claude:      opts.claude,
		models:      opts.models,
		home:        home,
		logger:      logger,
		judge:       newDoneJudge(opts.claude, opts.models),
		closures:    newClosureJudge(opts.claude, opts.models),
		reads:       newReadTimes(),
		mirror:      mirror,
		webhook:     opts.webhook,
		summarizers: make(map[tmuxctl.Server]*summary.Summarizer),
		views:       newDoneViews(),
		now:         opts.relayNow,
	}
}

// serve listens on the daemon and desk sockets and serves frames while it opens the store, so no frame waits for the
// store. The first tidy runs once the store is open and the sessions have started. A failed sessions start stops
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
	}, viewing), serveRelay(opts, home, logger, mirror))
	router := hub.router
	pageNotices := enginehost.NewPageNotices(pages, router, router, time.Now)
	observe := func(transition daemon.Transition) { hub.observe(ctx, transition) }
	// callerWake holds the caller-event wake once startSessions has returned the host, so the pane observer does nothing
	// until then.
	var callerWake atomic.Pointer[func()]
	// The first tidy waits for tidyStart, which closes once the store is open and the sessions have started.
	tidyStart := make(chan struct{})
	watch := startPaneWatch(ctx, logger, home, router)
	serveDone := serveInBackground(ctx, ln, daemon.Config{
		Server:        opts.server,
		DefaultServer: opts.defaultServer,
		Home:          home,
		Sessions:      opts.sessions,
		Observers: []daemon.Observer{observe, autoseen.New(time.Now, logger, func(pane string, seen int64) {
			router.Seen(ctx, notice.Key{Pane: pane}, seen)
		}, hub.viewer), callerWakeOnDone(&callerWake)},
		Seen:      func(report daemon.SeenReport) { router.Seen(ctx, notice.Key{Pane: report.PaneID}, report.Seen) },
		Input:     desk.Input,
		Viewing:   func(report daemon.ViewReport) { viewing.note(report.Input, report.View) },
		Read:      func(report daemon.ReadReport) { hub.reads.note(report.PaneID, report.Read) },
		Logger:    logger,
		Mirror:    mirror,
		TidyStart: tidyStart,
		Updates:   watch.updates,
		// Each tidy pass retries the notice row writes the store refused, so they are stored once it takes writes.
		AfterTidy: notices.Retry,
	})

	st := openStore(ctx, getenv, logger, opts.metrics, opts.storeBusy, opts.storeRetryAfter)
	attachStore(ctx, st, mirror, notices, router)
	hub.attachVerdicts(st)
	var sessions *sessionHost
	var startErr error
	if opts.sessions {
		sessions, startErr = startSessions(ctx, opts, host, st, router, pageNotices, logger, getenv)
	}
	publishCallerWake(&callerWake, sessions)
	if startErr != nil {
		stop()
	} else {
		close(tidyStart)
		notifyReady(ctx, logger, opts)
	}
	serveErr := awaitServe(ctx, logger, serveDone, watch)
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
// logged as the hook socket phase. The pane watch is stopped on return, before serve closes the router its unreadable
// entries notify.
func awaitServe(ctx context.Context, logger *slog.Logger, serveDone <-chan error, watch *paneWatch) error {
	defer watch.stop(logger)
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

// paneWatch is the pane watch of one serve: the channel its pane updates go to, which Serve takes them from, and the
// means to stop the watch and wait for it to return.
type paneWatch struct {
	updates chan daemon.PaneUpdate
	cancel  context.CancelFunc
	done    chan struct{} // closed once the watch has returned, or at once when it never started
}

// startPaneWatch makes the session registry under home, the directory Claude Code writes its entries to, and runs the
// pane watch of it in a goroutine. Run sends each pane update on the watch's updates, and each registry entry or
// transcript it cannot read goes to router as a notice. A directory that cannot be made is logged, and no watch runs.
// An error Run returns is logged too, except the context's error, which is how every run ends. Without a home there is
// no registry, so no directory is made and no watch runs.
func startPaneWatch(ctx context.Context, logger *slog.Logger, home string, router *route.Router) *paneWatch {
	watchCtx, cancel := context.WithCancel(ctx)
	watch := &paneWatch{updates: make(chan daemon.PaneUpdate), cancel: cancel, done: make(chan struct{})}
	if home == "" {
		close(watch.done)
		return watch
	}
	dir := filepath.Join(home, ".claude", "sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		logger.ErrorContext(ctx, "create session registry", "dir", dir, "error", err)
		close(watch.done)
		return watch
	}
	go func() {
		defer close(watch.done)
		err := panewatch.Run(watchCtx, panewatch.Config{
			Dir:     dir,
			Home:    home,
			Proc:    paneregistry.SystemProc(),
			Updates: watch.updates,
			Unreadable: func(path string, err error) {
				router.Notify(watchCtx, registryNotice(path, err))
			},
			Logger: logger,
		})
		if err != nil && !errors.Is(err, context.Canceled) {
			logger.ErrorContext(ctx, "watch panes", "error", err)
		}
	}()
	return watch
}

// stop ends the pane watch and waits until it has returned, so that no unreadable entry notifies the router once serve
// closes it. The wait is logged as the pane watch phase.
func (w *paneWatch) stop(logger *slog.Logger) {
	w.cancel()
	shutdownPhase(logger, "pane watch", func() { <-w.done })
}

// shutdownServe runs once Serve has returned. It closes pages first, so the Watch streams open on it end before the
// session socket stops serving. It waits for the summaries and then for the mirror's deferred syncs and tidies, stops
// the sessions, then waits for the router and the notices, since the sessions can post to them, clears the store hooks,
// and closes the store. It returns the sessions' stop error.
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
	clearStoreHooks(st)
	shutdownPhase(logger, "store", func() { closeStore(ctx, st, logger) })
	return sessionErr
}

// sessionHost is the engine session service of a --sessions orreryd. The manager runs the sessions, the writer stores
// their events, and the listener answers the Engine, Steps, Pools, Evidence and Contracts services on sessions.sock.
// Without a store the manager, writer and broker are nil and every RPC is refused. The broker is closed before the
// listener stops, so a grant that a blocked Acquire holds stays stored for the next orreryd. pools is the broker when
// its restore succeeded, and nil otherwise, so no Pools are served. evidence is the evidence runner over the store, the
// steps and the pools, and nil without a store. checks is the node checks runner over the store and the pools, and nil
// without a store.
type sessionHost struct {
	manager    *enginehost.Manager
	steps      *enginehost.Steps
	epics      enginehost.EpicCommands
	steering   enginehost.SteeringSessions
	contracts  *enginehost.Contracts
	writer     *enginehost.Writer
	broker     *grants.Broker
	pools      enginehost.PoolCommands
	evidence   *enginehost.Evidence
	checks     *enginehost.NodeChecks
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
	// cron fires the cron jobs of the sessions. stopCron cancels its Run, which closes cronDone when it returns.
	cron          *enginehost.CronScheduler
	stopCron      context.CancelFunc
	cronDone      chan struct{}
	stopSummaries context.CancelFunc
	summariesDone chan struct{}
	stopTitles    context.CancelFunc
	titlesDone    chan struct{}
	stopHeadlines context.CancelFunc
	headlinesDone chan struct{}
	stopClosures  context.CancelFunc
	closuresDone  chan struct{}
	stopReports   context.CancelFunc
	reportsDone   chan struct{}
	stopServe     context.CancelFunc
	serveDone     chan error
	// callerEvents is the caller-event delivery. Its Wake asks for a round. stopCallerEvents cancels its Run, which closes
	// callerEventsDone when it returns.
	callerEvents     *enginehost.CallerEvents
	stopCallerEvents context.CancelFunc
	callerEventsDone chan struct{}
	// flowWake is the relay the steps' EpicWake hands epics to. startFlow sets it to the interpreter's Wake.
	flowWake flowWakeRelay
	// dueWake is the relay the steps' Stored and the epic writes wake the due pass through. startDue sets it to the due
	// pass's Wake.
	dueWake dueWakeRelay
	// outsideWake is the relay the release slots' frees wake the epics that waited on an outside holder through.
	// startFlow sets it to the interpreter's WakeOutsideWaiters.
	outsideWake outsideWakeRelay
	// changes is the change feed the Changes service serves, nil without a store. wireStore sets it.
	changes *enginehost.ChangeFeed
	// epicWrites is the test seam options.epicWrites, which subscribeFlowWake's hook calls. Production leaves it nil.
	epicWrites func(epics []string)
	// limits tells the usage limits. Its Wake is part of the Steps engine's stored wake (see storedWake). stopLimits
	// cancels its Run, which closes limitsDone when it returns.
	limits *enginehost.LimitNotices
	// releaseSlots holds the repositories' release slots for outside releasers. stopReleaseSlots cancels its Run, which
	// closes releaseSlotsDone when it returns.
	releaseSlots     *enginehost.ReleaseSlots
	stopReleaseSlots context.CancelFunc
	releaseSlotsDone chan struct{}
	// paneSends records the texts sent to panes, nil without a store. The PaneSends service on the session socket
	// serves it.
	paneSends enginehost.PaneSendRecorder
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

// newSessionHost returns the sessionHost that startSessions fills, with the done channels of the loops and the writer
// already made, and logger logging each shutdown phase.
func newSessionHost(logger *slog.Logger) *sessionHost {
	return &sessionHost{
		logger:        logger,
		writerDone:    make(chan struct{}),
		idleDone:      make(chan struct{}),
		flowDone:      make(chan struct{}),
		limitsDone:    make(chan struct{}),
		dueDone:       make(chan struct{}),
		cronDone:      make(chan struct{}),
		summariesDone: make(chan struct{}),
		reportsDone:   make(chan struct{}),
		serveDone:     make(chan error, 1),

		releaseSlotsDone: make(chan struct{}),
	}
}

// startSessions listens on sessions.sock beside the daemon socket and serves the Engine, Steps, Pools and Evidence
// services on it until stop. With an open store it also starts the writer, a manager that notifies through router, the
// grants broker for hostName, which restores the grants a previous run stored and then runs no loop until stop, and the
// step engine whose worktrees go under step-worktrees
// beside the store file. The steps re-adopt the open step rows a previous run left, the manager reconciles the session
// rows and the steps reconcile the rest before the service serves. The evidence runner is then built over the store,
// the steps and the pools, and takes up the open evidence runs, so no judge starts beside a step already re-adopted.
// The node checks are built over the store and the pools the same way, and take up the open checks before the flow
// starts. The flow then runs over the store until stop, with the planner and the build runner as its planner and
// builder, and its attention loop routes its epic notices through router when router is not nil. The limit notices tell
// router which steps a usage limit holds, at start and on each stored hold or resume until stop; the due pass resumes
// each due row whose reset has passed, at its start, on each wake and at each row's moment, until stop (startDue); the
// session titles write each headless session's missing title through haiku, at start and on each first prompt the store
// stores, until stop; the session headlines refresh each headless session's headline once its turns have ended, through
// haiku, at start and on each turn end the store stores, until stop; the manager stops idle sessions, until stop; and
// the caller-event delivery tells each
// step's caller when the step ends, and each epic's steerer of its events, on its start, on each wake, and once more
// after a failed round, until stop (startCallerEvents);
// and the cron scheduler fires each cron job of a headless session when it is due, through the manager's DeliverCron,
// from one timer for the next due fire until stop (startCron); and the release slots free each outside hold at its
// deadline from one timer, wake their waiting holds on each epic write, and wake the flow's epics that waited on a hold
// when it ends, until stop (startReleaseSlots). A readopt, reconcile, restore or resume failure is
// logged and serving goes on; a failed restore serves no Pools, which refuse as unavailable, and the gates fail for
// want of pools. The Notices service on it answers from notices, and refuses as unavailable when notices is nil. With
// st open, each call of the Epics service on it that names an epic records its session in st. The Changes service on it
// relays the sessions each store write appends events to, from the change feed st publishes to.
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
	host := newSessionHost(logger)
	host.epicWrites = opts.epicWrites
	var commands enginehost.Commands
	var stepCommands enginehost.StepCommands
	var evidence enginehost.EvidenceCommands
	var crons enginehost.CronCommands
	if st != nil {
		stateDir, childEnviron, err := adoptSessionState(ctx, st, hostName, getenv)
		if err != nil {
			return nil, err
		}
		host.newWriter(opts, st, logger)
		host.newCron(opts, st, logger)
		host.newWakes(st, opts, router, logger)
		host.manager = enginehost.NewManager(enginehost.Config{
			Shell:       opts.sessionShell,
			Claude:      opts.claude,
			Environ:     childEnviron,
			HolderScope: opts.holderScope,
			StateDir:    stateDir,
			ManagedDir:  managedDir,
			Writer:      host.writer,
			Notifier:    enginehost.NewNotifier(enginehost.NotifierConfig{Route: router}),
			Store:       st,
			Logger:      logger,
			Metrics:     opts.metrics,
			CronWake:    host.cron.Wake, PutOffs: st, Panes: newPaneQuestions(questionServer(opts), router),
			EventWake: host.callerEvents.Wake,
		})
		host.startBroker(ctx, hostName, st, logger)
		// host.pools is nil when the broker's restore failed, which leaves the gates no pools to run under.
		resolve, hosts := planner.NewResolver(os.Environ(), managedDir), hostexec.Map{hostName: hostexec.NewLocal()}
		haiku := enginehost.NewHaiku(enginehost.HaikuConfig{Claude: opts.claude, Environ: os.Environ()})
		steps := enginehost.NewSteps(enginehost.StepsConfig{
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
			ReadoptCheck: readoptCheck(st, resolve, hosts),
			ReadoptGate:  readoptGate(st),
			Stored:       host.storedWake, EpicWake: host.stepEpicWake,
			Host:  hostName,
			Hosts: hosts,
		})
		host.steps = steps
		host.wireStore(st)
		commands, stepCommands = host.manager, host.steps
		host.epics, host.steering, host.contracts = st, epicSteering(host.manager, st), contractsOf(st)
		crons = enginehost.NewCronAnswers(enginehost.CronAnswersConfig{
			Store: st, Now: opts.cronNow, Changed: host.cron.Changed})
		host.newReleaseSlots(opts, st, logger)
		host.startWriter(ctx)
		host.reconcile(ctx, logger, opts.holdReconcile, opts.flight, opts.flightNow)
		host.startCallerEvents(ctx)
		host.startCron(ctx)
		host.startEvidence(ctx, st, stateDir, opts, childEnviron, logger)
		evidence = host.evidence
		host.startNodeChecks(ctx, st, stateDir, opts, childEnviron, logger)
		host.startFlow(ctx, st, router, opts.attentionTicks, opts.flowTicks, getenv, resolve, stateDir, hosts, logger)
		host.startReleaseSlots(ctx)
		host.startLimits(ctx)
		host.startSummaries(ctx, st, haiku, logger)
		host.startTitles(ctx, st, haiku, logger)
		host.startTurnEnded(ctx, st, haiku, opts.models, logger)
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
	// The session socket also serves the release slots, through serveConfig's ReleaseSlots.
	cfg := host.serveConfig(
		commands, stepCommands, evidence, notices, crons, hostName, serveOpts(opts, st, logger))
	go func() {
		host.serveDone <- enginehost.ServeWith(serveCtx, ln, cfg)
	}()
	return host, nil
}

// adoptSessionState returns sessionState's state directory and child environ, after st adopts hostName for the rows
// stored before hosts, so the sessions start with each epic and step naming its host. It returns the error either gives.
func adoptSessionState(
	ctx context.Context,
	st *store.Store,
	hostName string,
	getenv func(string) string,
) (string, []string, error) {
	stateDir, childEnviron, err := sessionState(ctx, getenv)
	if err != nil {
		return "", nil, err
	}
	if err = st.AdoptHost(ctx, hostName); err != nil {
		return "", nil, fmt.Errorf("adopt host: %w", err)
	}
	return stateDir, childEnviron, nil
}

// serveConfig returns what the session socket serves: the services the host holds, and the given commands, evidence,
// notices and crons, with handlerOpts on each handler and hostName as the host each epic is stamped with. ReleaseSlots
// is set only when the host has release slots, since a nil *ReleaseSlots in the interface would not refuse as
// unavailable, so it stays unset without a store.
func (h *sessionHost) serveConfig(
	commands enginehost.Commands,
	steps enginehost.StepCommands,
	evidence enginehost.EvidenceCommands,
	notices enginehost.NoticeSource,
	crons enginehost.CronCommands,
	hostName string,
	handlerOpts []connect.HandlerOption,
) enginehost.ServeConfig {
	cfg := enginehost.ServeConfig{
		Commands:  commands,
		Steps:     steps,
		Epics:     h.epics,
		Steering:  h.steering,
		Pools:     h.pools,
		Evidence:  evidence,
		Notices:   notices,
		Crons:     crons,
		Changes:   h.changes,
		Contracts: h.contracts,
		PaneSends: h.paneSends,
		Host:      hostName,
		Options:   handlerOpts,
	}
	if h.releaseSlots != nil {
		cfg.ReleaseSlots = h.releaseSlots
	}
	return cfg
}

// epicSteering returns the steering sessions of the epics st serves, which manager starts.
func epicSteering(manager *enginehost.Manager, st *store.Store) *enginehost.EpicSteering {
	return enginehost.NewEpicSteering(enginehost.EpicSteeringConfig{
		Manager: manager, Sessions: st, Render: planner.SteeringLines, PaneDir: paneDir,
	})
}

// contractsOf returns the contracts st serves, stamped with the process clock.
func contractsOf(st *store.Store) *enginehost.Contracts {
	return &enginehost.Contracts{Store: st, Now: time.Now}
}

// paneDir returns the current directory of the tmux pane on the server whose store column is server. Its error names
// the pane that did not read.
func paneDir(ctx context.Context, server, pane string) (string, error) {
	column, err := tmuxctl.ServerOfColumn(server)
	if err != nil {
		return "", fmt.Errorf("pane %s: %w", pane, err)
	}
	client, err := tmuxClient(column)
	if err != nil {
		return "", err
	}
	values, err := client.PaneValues(ctx, pane, "pane_current_path")
	if err != nil {
		return "", fmt.Errorf("read directory of pane %s: %w", pane, err)
	}
	return values[0], nil
}

// serveOpts returns the handler options the session socket serves with: opts.metricsHandlers, and with st open, the
// interceptor that records the session of each epic call that names an epic.
func serveOpts(opts options, st *store.Store, logger *slog.Logger) []connect.HandlerOption {
	if st == nil {
		return opts.metricsHandlers
	}
	interceptor := enginehost.EpicCallInterceptor(st, time.Now, nil, logger)
	return append(slices.Clip(opts.metricsHandlers), connect.WithInterceptors(interceptor))
}

// readoptCheck is the ReadoptCheck of the steps. A re-adopted row gets the planner's return check when it is the step of a
// running planner turn, and when that check is nil, the release integrator's check when it is a release integrator
// step, and then the build's verifier or orchestrator check, which reads the epic's tasks on the host that hosts
// resolves the epic to. An error from any of them is returned.
func readoptCheck(
	st *store.Store,
	resolve planner.Resolver,
	hosts hostexec.Hosts,
) func(ctx context.Context, row store.StepRow) (func(value json.RawMessage) []string, error) {
	plan := planner.ReadoptCheck(st, resolve)
	integrate := interpret.ReadoptCheck(st)
	verify := build.ReadoptCheck(st, hosts)
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
// that succeeds makes the broker the host's pools. A restore that fails is logged and leaves the pools nil, so no Pools
// are served; the broker is still closed at shutdown, since its restore may have started member commands. The broker
// closes as soon as ctx ends: the signal that ends ctx also starts Serve's shutdown, whose Close after shutdownWait
// cancels each held Acquire, and an Acquire canceled while the broker is open ends its grant.
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

// stopBroker closes the broker, which waits for its member commands to end. Each Acquire the broker is blocking returns
// without ending its grant, so the grant stays stored for the next orreryd. Without a broker it does nothing, and so
// does a second call.
func (h *sessionHost) stopBroker() {
	if h.broker == nil {
		return
	}
	h.broker.Close()
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
func sessionState(ctx context.Context, getenv func(string) string) (string, []string, error) {
	stateDir, err := stateDirOf(getenv)
	if err != nil {
		return "", nil, fmt.Errorf("step worktrees: %w", err)
	}
	environ, err := enginehost.GradleEnviron(
		ctx, hostexec.NewLocal(), os.Environ(), stateDir, enginehost.UserGradleHome(os.Getenv))
	if err != nil {
		return "", nil, fmt.Errorf("session environment: %w", err)
	}
	return stateDir, environ, nil
}

// startEvidence builds the evidence runner over st, the steps and the pools, with its blobs and capture worktrees under
// stateDir and its gates under stateDir/gates, and takes up the open evidence runs a previous run left. A failure to take
// them up is logged and serving goes on. Its gates run in environ, under the holder scope and its memory cap that opts
// name.
func (h *sessionHost) startEvidence(
	ctx context.Context,
	st *store.Store,
	stateDir string,
	opts options,
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
		GateScope:    opts.holderScope,
		Logger:       logger,
	})
	if err := h.evidence.Resume(ctx); err != nil {
		logger.ErrorContext(ctx, "resume evidence runs, serving anyway", "error", err)
	}
}

// startNodeChecks builds the node checks runner over st and the pools, with its check worktrees under stateDir and its
// gates under stateDir/gates, and takes up the open checks a previous run left. A failure to take them up is logged and
// serving goes on. Its gates run in environ, under the holder scope and its memory cap that opts name.
func (h *sessionHost) startNodeChecks(
	ctx context.Context,
	st *store.Store,
	stateDir string,
	opts options,
	environ []string,
	logger *slog.Logger,
) {
	h.checks = enginehost.NewNodeChecks(enginehost.NodeChecksConfig{
		Store:        st,
		Pools:        h.pools,
		Environ:      environ,
		StateDir:     stateDir,
		GateStateDir: filepath.Join(stateDir, "gates"),
		GateScope:    opts.holderScope,
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
// SIGUSR1, and logs each pass to the file flowTicks names (flowSeam). Without flowTicks, each wake passes the flow too:
// a store write that moves an epic, a step end or hold of an epic, and a freed step slot that refused an epic
// (subscribeFlowWake).
func (h *sessionHost) startFlow(
	ctx context.Context,
	st *store.Store,
	router planner.Router,
	attentionTicks <-chan time.Time,
	flowTicks string,
	getenv func(string) string,
	resolve planner.Resolver,
	stateDir string,
	hosts hostexec.Hosts,
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
		Hosts:    hosts,
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
		Hosts:     hosts,
		Getenv:    getenv,
		Logger:    logger,
		Ticks:     ticks,
		Passed:    passed,
	})
	h.subscribeFlowWake(st, flow.Wake)
	h.outsideWake.set(flow.WakeOutsideWaiters)
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

// flowWakeRelay hands the epics a wake names to the flow's interpreter wake, which subscribeFlowWake sets. Until then a
// wake does nothing: the interpreter's first pass, which covers every epic, makes up for it. It is safe from any
// goroutine, never blocks, and takes only its own lock.
type flowWakeRelay struct {
	mu   sync.Mutex
	wake func(epics []string)
}

// set makes wake the interpreter's wake that Wake hands epics to.
func (r *flowWakeRelay) set(wake func(epics []string)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.wake = wake
}

// Wake hands epics to the interpreter's wake, or does nothing while none is set. The wake is called outside the lock.
func (r *flowWakeRelay) Wake(epics []string) {
	r.mu.Lock()
	wake := r.wake
	r.mu.Unlock()
	if wake != nil {
		wake(epics)
	}
}

// outsideWakeRelay wakes the epics that waited on an outside hold, through the function startFlow sets it to. Until then a
// wake does nothing: the flow's first pass, which covers every epic, makes up for it. It is safe from any goroutine, never
// blocks, and takes only its own lock.
type outsideWakeRelay struct {
	mu   sync.Mutex
	wake func()
}

// set makes wake the function that Wake calls.
func (r *outsideWakeRelay) set(wake func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.wake = wake
}

// Wake calls the function set makes, or does nothing while none is set. The function is called outside the lock.
func (r *outsideWakeRelay) Wake() {
	r.mu.Lock()
	wake := r.wake
	r.mu.Unlock()
	if wake != nil {
		wake()
	}
}

// dueWakeRelay wakes the due pass that startDue sets it to. Until then a wake does nothing: the due pass's first pass,
// which covers every row, makes up for it. It is safe from any goroutine, never blocks, and takes only its own lock.
type dueWakeRelay struct {
	mu   sync.Mutex
	wake func()
}

// set makes wake the due pass's wake that Wake calls.
func (r *dueWakeRelay) set(wake func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.wake = wake
}

// Wake calls the due pass's wake, or does nothing while none is set. The wake is called outside the lock.
func (r *dueWakeRelay) Wake() {
	r.mu.Lock()
	wake := r.wake
	r.mu.Unlock()
	if wake != nil {
		wake()
	}
}

// wireStore points st's engine liveness at the manager, and makes h.changes the change feed that st's session-events
// hook publishes each event write to, for the Changes service to relay.
func (h *sessionHost) wireStore(st *store.Store) {
	st.SetEngineLive(h.manager.Live)
	h.changes = enginehost.NewChangeFeed()
	st.OnSessionEvents(h.changes.Publish)
	h.paneSends = st
}

// subscribeFlowWake makes wake the flow's wake: the relay the steps and the manager hand epics to, and the store's
// epics-changed hook, which calls it on each write that moves an epic and then wakes the due pass. The due pass wakes on
// an epic write too, since a limit's due row for an epic is stored with the epic's pause, which moves its revision. The
// hook also wakes the release slots' HoldWaits, since an epic write may be the one that frees a slot they wait on.
func (h *sessionHost) subscribeFlowWake(st *store.Store, wake func(epics []string)) {
	h.flowWake.set(wake)
	st.OnEpicsChanged(func(epics []string) {
		wake(epics)
		h.dueWake.Wake()
		if h.releaseSlots != nil {
			h.releaseSlots.Changed()
		}
		if h.epicWrites != nil {
			h.epicWrites(epics)
		}
	})
}

// stepEpicWake is the steps' EpicWake: it hands the epic of a step end or hold to the flow.
func (h *sessionHost) stepEpicWake(epic string) {
	h.flowWake.Wake([]string{epic})
}

// stepStored is the part of the steps' Stored (see storedWake) that wakes the caller-event delivery, and then the due
// pass, since a step limit publishes its due row once the write that stores it has committed. It does not block.
func (h *sessionHost) stepStored() {
	h.callerEvents.Wake()
	h.dueWake.Wake()
}

// clearStoreHooks clears the epics-changed and session-events hooks of st, so no write after it calls them. A nil st
// has no hooks.
func clearStoreHooks(st *store.Store) {
	if st == nil {
		return
	}
	st.OnEpicsChanged(nil)
	st.OnSessionEvents(nil)
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

// newWakes builds the caller-event delivery and the limit notices over st, the two loops the Steps engine's stored wake
// calls (see storedWake). They are built before the Steps engine, so the wake reaches both from the first stored hold.
func (h *sessionHost) newWakes(st *store.Store, opts options, router enginehost.Router, logger *slog.Logger) {
	h.newCallerEvents(st, opts, logger)
	h.newLimitNotices(st, router, logger)
}

// newLimitNotices builds the limit notices over st as h.limits, routing their notices through router. The marks st
// holds say which limits a previous orreryd told, and those are not told again.
func (h *sessionHost) newLimitNotices(st *store.Store, router enginehost.Router, logger *slog.Logger) {
	h.limits = enginehost.NewLimitNotices(enginehost.LimitNoticesConfig{
		Steps:  st,
		Epics:  st,
		Told:   st,
		Route:  router,
		Now:    time.Now,
		Logger: logger,
	})
}

// storedWake is the Steps engine's stored wake. A hold or resume that the Steps engine stores calls it, so the caller
// events, the due pass and the limit notices each check at once (see stepStored). Each Wake call returns without
// blocking.
func (h *sessionHost) storedWake() {
	h.stepStored()
	h.limits.Wake()
}

// startLimits runs h.limits until shutdown, on one goroutine. Its Run checks at start, then once on each wake (see
// storedWake) and once when a retry fires (see enginehost.LimitNotices).
func (h *sessionHost) startLimits(ctx context.Context) {
	var limitsCtx context.Context
	limitsCtx, h.stopLimits = context.WithCancel(ctx)
	go func() {
		defer close(h.limitsDone)
		h.limits.Run(limitsCtx)
	}()
}

// newCallerEvents builds the caller-event delivery over st as h.callerEvents. Its wake is the trigger of every caller
// event: the manager's EventWake, the steps' Stored, the store's OnCallerEvent, and the pane observer. Both passes send
// through one CallerSender: a headless caller gets its event as a prompt through the manager, a pane caller gets it
// through cli.WakePane, and a step caller gets the end of its step as a prompt while that step is open (see
// Steps.DeliverChildEnd), or is only marked when the step has ended. Neither depends on a router, since a caller event
// goes to its caller alone. The sender's Deliver and DeliverStep read h.manager and h.steps when they send, which is after
// startSessions has set them. When opts.callerEventPassed is not nil, it is called after each pass returns, and
// opts.callerEventTimer and opts.callerWakePane replace the retry timer and cli.WakePane.
func (h *sessionHost) newCallerEvents(st *store.Store, opts options, logger *slog.Logger) {
	wakePane := opts.callerWakePane
	if wakePane == nil {
		wakePane = cli.WakePane
	}
	sender := enginehost.NewCallerSender(enginehost.CallerSenderConfig{
		Deliver: func(ctx context.Context, req enginehost.DeliverRequest) error {
			return h.manager.Deliver(ctx, req)
		},
		DeliverStep: func(ctx context.Context, req enginehost.DeliverRequest) error {
			return h.steps.DeliverChildEnd(ctx, req)
		},
		WakePane: wakePane,
		Store:    st,
		Now:      time.Now,
		Logger:   logger,
	})
	passes := []enginehost.CallerPass{
		enginehost.NewStepCallerEvents(enginehost.StepCallerEventsConfig{Store: st, Sender: sender}),
		planner.NewEpicCallerEvents(planner.EpicCallerEventsConfig{Store: st, Sender: sender}),
	}
	if opts.callerEventPassed != nil {
		for i, pass := range passes {
			passes[i] = passedPass{pass: pass, passed: opts.callerEventPassed}
		}
	}
	h.callerEvents = enginehost.NewCallerEvents(enginehost.CallerEventsConfig{
		Passes: passes, NewTimer: opts.callerEventTimer, Logger: logger,
	})
	st.OnCallerEvent(h.callerEvents.Wake)
}

// startCallerEvents runs h.callerEvents until shutdown, on one goroutine. Its Run makes one round at its start, then
// one on each wake and each retry of a failed round (see enginehost.CallerEvents). It is started after the reconcile,
// so no round runs before the rows are reconciled.
func (h *sessionHost) startCallerEvents(ctx context.Context) {
	var eventsCtx context.Context
	eventsCtx, h.stopCallerEvents = context.WithCancel(ctx)
	h.callerEventsDone = make(chan struct{})
	go func() {
		defer close(h.callerEventsDone)
		h.callerEvents.Run(eventsCtx)
	}()
}

// callerWakeOnDone returns the daemon observer that calls the caller-event wake held in wake when a transition is to
// done: a pane caller's applied state becoming done may let the event its pane held go. It does nothing while wake
// holds no function. The wake does not block, so the observer returns at once.
func callerWakeOnDone(wake *atomic.Pointer[func()]) daemon.Observer {
	return func(transition daemon.Transition) {
		if transition.To != protocol.StateDone {
			return
		}
		if fn := wake.Load(); fn != nil {
			(*fn)()
		}
	}
}

// publishCallerWake stores the caller-event wake of host in holder, which callerWakeOnDone calls. A nil host, or one
// that runs no caller-event delivery, stores nothing.
func publishCallerWake(holder *atomic.Pointer[func()], host *sessionHost) {
	if host == nil || host.callerEvents == nil {
		return
	}
	wake := host.callerEvents.Wake
	holder.Store(&wake)
}

// passedPass is a caller-event pass that calls passed after each run of pass returns.
type passedPass struct {
	pass   enginehost.CallerPass
	passed func()
}

// Pass runs the pass, then calls passed, and returns the pass's error.
func (p passedPass) Pass(ctx context.Context) error {
	err := p.pass.Pass(ctx)
	p.passed()
	if err != nil {
		return fmt.Errorf("caller event pass: %w", err)
	}
	return nil
}

// newWriter builds the host's writer over st, which stores the events of the sessions and steps, as h.writer.
func (h *sessionHost) newWriter(opts options, st *store.Store, logger *slog.Logger) {
	h.writer = enginehost.NewWriter(enginehost.WriterConfig{
		Applier: st, Logger: logger, Metrics: opts.metrics,
	})
}

// newCron builds the host's cron scheduler over st, and stores it as h.cron. Each due fire goes through the manager's
// DeliverCron, or through opts.cronDeliver when that is set. The delivery reads h.manager when a fire is due, which is
// after startSessions has set it.
func (h *sessionHost) newCron(opts options, st *store.Store, logger *slog.Logger) {
	deliver := func(ctx context.Context, req enginehost.DeliverRequest) error {
		if opts.cronDeliver != nil {
			return opts.cronDeliver(ctx, req)
		}
		return h.manager.DeliverCron(ctx, req)
	}
	h.cron = enginehost.NewCronScheduler(enginehost.CronSchedulerConfig{
		Store: st, Deliver: deliver, Now: opts.cronNow, NewTimer: opts.cronTimers, Logger: logger,
	})
}

// startCron runs the cron scheduler until stop, on its own cancel, and closes cronDone when its Run returns.
func (h *sessionHost) startCron(ctx context.Context) {
	var cronCtx context.Context
	cronCtx, h.stopCron = context.WithCancel(ctx)
	go func() {
		defer close(h.cronDone)
		h.cron.Run(cronCtx)
	}()
}

// newReleaseSlots builds the release slots over st, which free each outside hold at its deadline and wake the flow's
// outside waiters when a hold ends, with the test seams of opts for the clock and the timers.
func (h *sessionHost) newReleaseSlots(opts options, st *store.Store, logger *slog.Logger) {
	h.releaseSlots = enginehost.NewReleaseSlots(enginehost.ReleaseSlotsConfig{
		Store: st, Now: opts.releaseSlotNow, NewTimer: opts.releaseSlotTimers, Freed: h.outsideWake.Wake,
		Logger: logger,
	})
}

// startReleaseSlots runs the release slots until stop, on its own cancel, and closes releaseSlotsDone when its Run
// returns.
func (h *sessionHost) startReleaseSlots(ctx context.Context) {
	var slotsCtx context.Context
	slotsCtx, h.stopReleaseSlots = context.WithCancel(ctx)
	go func() {
		defer close(h.releaseSlotsDone)
		h.releaseSlots.Run(slotsCtx)
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

// startDue resumes the due rows over st until stop, by the clock now, or time.Now when now is nil. It passes once when it
// starts, then on each wake of the due relay (see dueWakeRelay) and at each due row's moment. Its steps are the host's
// steps, which startSessions has re-adopted before it starts the pass.
func (h *sessionHost) startDue(ctx context.Context, st *store.Store, now func() time.Time, logger *slog.Logger) {
	if now == nil {
		now = time.Now
	}
	due := enginehost.NewDuePass(enginehost.DuePassConfig{
		Store:  st,
		Steps:  h.steps,
		Now:    now,
		Logger: logger,
	})
	h.dueWake.set(due.Wake)
	var dueCtx context.Context
	dueCtx, h.stopDue = context.WithCancel(ctx)
	go func() {
		defer close(h.dueDone)
		due.Run(dueCtx)
	}()
}

// startSummaries writes the summary of each stored epic that has none, through haiku. It checks every epic at start,
// then each epic the store stores, on the store's OnEpicStored hook, until stop.
func (h *sessionHost) startSummaries(
	ctx context.Context,
	st *store.Store,
	haiku *enginehost.Haiku,
	logger *slog.Logger,
) {
	summaries := enginehost.NewEpicSummaries(enginehost.EpicSummariesConfig{
		Store:  st,
		Haiku:  haiku,
		Logger: logger,
	})
	st.OnEpicStored(summaries.Wake)
	var summariesCtx context.Context
	summariesCtx, h.stopSummaries = context.WithCancel(ctx)
	go func() {
		defer close(h.summariesDone)
		summaries.Run(summariesCtx)
	}()
}

// startTitles writes the title of each headless session that has none, from its first prompt through haiku. It checks
// every untitled session at start, then each session whose first prompt the store stores, on the store's OnFirstPrompt
// hook, until stop.
func (h *sessionHost) startTitles(
	ctx context.Context,
	st *store.Store,
	haiku *enginehost.Haiku,
	logger *slog.Logger,
) {
	titles := enginehost.NewSessionTitles(enginehost.SessionTitlesConfig{
		Store:  st,
		Haiku:  haiku,
		Logger: logger,
	})
	st.OnFirstPrompt(titles.Wake)
	var titlesCtx context.Context
	titlesCtx, h.stopTitles = context.WithCancel(ctx)
	h.titlesDone = make(chan struct{})
	go func() {
		defer close(h.titlesDone)
		titles.Run(titlesCtx)
	}()
}

// startHeadlines refreshes the headline of each headless session whose turns have ended since its last headline, from
// its first prompt and recent turns through haiku. It checks every due session at start, then each session the wake it
// returns names, until stop. The store's OnTurnEnded hook calls that wake for each session whose turn end the store
// stores.
func (h *sessionHost) startHeadlines(
	ctx context.Context,
	st *store.Store,
	haiku *enginehost.Haiku,
	logger *slog.Logger,
) func(session string) {
	headlines := enginehost.NewSessionHeadlines(enginehost.SessionHeadlinesConfig{
		Store:  st,
		Haiku:  haiku,
		Logger: logger,
	})
	var headlinesCtx context.Context
	headlinesCtx, h.stopHeadlines = context.WithCancel(ctx)
	h.headlinesDone = make(chan struct{})
	go func() {
		defer close(h.headlinesDone)
		headlines.Run(headlinesCtx)
	}()
	return headlines.Wake
}

// startTurnEnded starts the session headlines and the session closures, and sets the store's single OnTurnEnded hook to
// wake both for each session whose turn end the store stores.
func (h *sessionHost) startTurnEnded(
	ctx context.Context,
	st *store.Store,
	haiku *enginehost.Haiku,
	models map[string]string,
	logger *slog.Logger,
) {
	headlinesWake := h.startHeadlines(ctx, st, haiku, logger)
	closuresWake := h.startClosures(ctx, st, haiku, models, logger)
	st.OnTurnEnded(func(session string) {
		headlinesWake(session)
		closuresWake(session)
	})
}

// startClosures stores whether each headless session whose turn ended done is closed, from its recent turns through the
// closure judge, which calls haiku under models. It judges each session the wake it returns names, until stop. The
// store's OnTurnEnded hook calls that wake for each session whose turn end the store stores.
func (h *sessionHost) startClosures(
	ctx context.Context,
	st *store.Store,
	haiku *enginehost.Haiku,
	models map[string]string,
	logger *slog.Logger,
) func(session string) {
	closures := enginehost.NewSessionClosures(enginehost.SessionClosuresConfig{
		Store:  st,
		Judge:  closurejudge.New(haiku, models),
		Logger: logger,
	})
	var closuresCtx context.Context
	closuresCtx, h.stopClosures = context.WithCancel(ctx)
	h.closuresDone = make(chan struct{})
	go func() {
		defer close(h.closuresDone)
		closures.Run(closuresCtx)
	}()
	return closures.Wake
}

// startReports writes the report of each ended epic that has none, through haiku. It checks every ended epic at start,
// then each epic the store ends, on the store's OnEpicEnded hook, until stop.
func (h *sessionHost) startReports(
	ctx context.Context,
	st *store.Store,
	haiku *enginehost.Haiku,
	logger *slog.Logger,
) {
	reports := enginehost.NewEpicReports(enginehost.EpicReportsConfig{
		Store:  st,
		Haiku:  haiku,
		Logger: logger,
	})
	st.OnEpicEnded(reports.Wake)
	var reportsCtx context.Context
	reportsCtx, h.stopReports = context.WithCancel(ctx)
	go func() {
		defer close(h.reportsDone)
		reports.Run(reportsCtx)
	}()
}

// shutdown stops the broker, stops the idle check, stops the flow and waits for it to return, which waits for the
// planner's turns, stops the limit notices and waits for the attention loop, the limit loop, the due pass, the epic
// summaries, the session titles, the session headlines, the session closures and the epic reports to return, stops
// the caller-event delivery and waits for it to return, closes the node checks and the evidence runner and waits for
// their drives, ends every
// step loop, stops every live session, and ends the writer after its last flush. The notices the sessions started are
// waited on in serve. Without a broker, manager and writer it does nothing.
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
		h.phase("cron fires", func() {
			h.stopCron()
			<-h.cronDone
		})
		h.phase("release slots", func() {
			h.stopReleaseSlots()
			<-h.releaseSlotsDone
		})
		h.phase("epic summaries", func() {
			h.stopSummaries()
			<-h.summariesDone
		})
		h.phase("session titles", func() {
			h.stopTitles()
			<-h.titlesDone
		})
		h.phase("session headlines", func() {
			h.stopHeadlines()
			<-h.headlinesDone
		})
		h.phase("session closures", func() {
			h.stopClosures()
			<-h.closuresDone
		})
		h.phase("epic reports", func() {
			h.stopReports()
			<-h.reportsDone
		})
		h.phase("caller events", func() {
			h.stopCallerEvents()
			<-h.callerEventsDone
		})
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

// openStore opens the store at its resolved path and logs "store open" once the store is open and migrated. A store
// that is busy when it opens, as Litestream holds it during a checkpoint, is tried again each storeOpenRetry, waiting on
// retryAfter (time.After when nil), until it opens or ctx ends; the wait is logged as "store busy, retrying open". When
// the path does not resolve or the store cannot be opened for another reason, it logs the error and returns nil, so
// orreryd serves hooks and notifications without the store. The store warns on logger of each call over 100 ms and
// counts it on metrics, and each of its writes waits at most busy for the write lock.
func openStore(
	ctx context.Context,
	getenv func(string) string,
	logger *slog.Logger,
	metrics *telemetry.Daemon,
	busy time.Duration,
	retryAfter func(time.Duration) <-chan time.Time,
) *store.Store {
	path, err := store.Path(getenv)
	if err != nil {
		logger.ErrorContext(ctx, "store unavailable, serving without it", "error", err)
		return nil
	}
	if retryAfter == nil {
		retryAfter = time.After
	}
	for {
		st, err := store.Open(ctx, path, store.WithLogger(logger), store.WithSlowCallCounter(metrics.StoreSlowCall),
			store.WithBusyTimeout(busy))
		if err == nil {
			logger.InfoContext(ctx, "store open", "path", path)
			return st
		}
		if !store.Busy(err) {
			logger.ErrorContext(ctx, "store unavailable, serving without it", "path", path, "error", err)
			return nil
		}
		logger.WarnContext(ctx, "store busy, retrying open", "path", path, "error", err)
		select {
		case <-retryAfter(storeOpenRetry):
		case <-ctx.Done():
			return nil
		}
	}
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
	views       *doneViews       // each done's viewing check, handed from observe to the autoseen observer
	now         func() time.Time // the clock the viewing checks read; nil reads time.Now

	closures *closurejudge.Judge          // decides whether a done pane's conversation is closed
	paneKeys paneKeyReader                // reads a pane's id and change time for its closure check; nil reads tmux
	verdicts atomic.Pointer[verdictStore] // stores the closure verdicts, once the store opens
	closing  sync.WaitGroup               // the closure checks, which wait returns after
}

// clock returns the time the relay's viewing checks compare the tmux clients' input with: r.now's, or time.Now's when
// r.now is nil.
func (r *relay) clock() time.Time {
	if r.now == nil {
		return time.Now()
	}
	return r.now()
}

// viewTimeout bounds the viewing check of one done transition, which runs on the daemon's observer path.
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
	viewing, err := autoseen.Viewing(ctx, client, transition.PaneID, r.clock())
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

// observe requests a summary for a pane that starts working or finishes, hands the notice for a pane that waits for
// input or finishes to the router, and otherwise tells the router the pane changed. A session's end also forgets the
// pane's summary and read time. A transition to done starts the pane's closure check.
func (r *relay) observe(ctx context.Context, transition daemon.Transition) {
	if transition.To == "" {
		r.forget(ctx, transition)
		r.reads.forget(transition.PaneID)
	}
	r.checkClosure(ctx, transition)
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
	summarizer, err := r.summarizerFor(transition.Server)
	if err != nil {
		r.logger.ErrorContext(ctx, "create summarizer", "error", err)
		return nil
	}
	req := summary.Request{
		PaneID:         transition.PaneID,
		BaseURL:        transition.BaseURL,
		Dir:            transition.Cwd,
		TranscriptPath: transition.Transcript,
	}
	if transition.To == protocol.StateDone {
		req.Stop = true
		req.LastAssistantMessage = transition.LastMessage
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

// notify hands the transition's notice to the router, with view as its first watching check when it is not nil. A
// notice whose state push.Wanted does not want is silent. Any other done is judged before it routes. A hook whose facts
// do not decode still routes, and the daemon logs its decode error. A notice that routes keeps its transition as its
// source, so a restart can resume it; a silent one never pends, so it needs none.
func (r *relay) notify(ctx context.Context, transition daemon.Transition, summarized <-chan struct{}, view *doneView) {
	incoming := r.paneNotice(transition, summarized, view)
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
// runs against the URL and env of the context stamp in effect for the transition's cwd under the relay's home, or else
// against the transition's base URL, as the summarizer does. A stamp that cannot be read fails the judge without a call.
func (r *relay) judgeDone(transition daemon.Transition) func(context.Context) (route.Verdict, error) {
	message := transition.LastMessage
	return func(ctx context.Context) (route.Verdict, error) {
		found, err := stamp.Find(r.home, transition.Cwd)
		if err != nil {
			return route.Verdict{}, fmt.Errorf("done judge: find context stamp: %w", err)
		}
		baseURL, env := transition.BaseURL, map[string]string(nil)
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
	now := r.clock()
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
		Title: deskstream.PaneTitle(filepath.Base(transition.Cwd), text.session, text.window),
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
	return paneNotification(transition, text), nil
}

// wait returns once every summary run and closure check has finished. It runs after Serve returns, so no observer can
// start more work.
func (r *relay) wait() {
	for _, summarizer := range r.summarizers {
		summarizer.Wait()
	}
	r.closing.Wait()
}
