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
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"connectrpc.com/connect"

	"github.com/joshsymonds/orrery/internal/cli"
	"github.com/joshsymonds/orrery/internal/daemon"
	"github.com/joshsymonds/orrery/internal/deskstream"
	"github.com/joshsymonds/orrery/internal/enginehost"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/holderhost"
	"github.com/joshsymonds/orrery/internal/hostexec"
	"github.com/joshsymonds/orrery/internal/hostload"
	"github.com/joshsymonds/orrery/internal/hostrelay"
	"github.com/joshsymonds/orrery/internal/node"
	"github.com/joshsymonds/orrery/internal/panehost"
	"github.com/joshsymonds/orrery/internal/paneregistry"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// nodeClaude is the claude binary that the agent's sessions and summaries run, as orreryd's --claude default names it.
const nodeClaude = "claude"

// seenTimeout bounds one seen report's call to the controller, which goes through sessions.sock.
const seenTimeout = 10 * time.Second

// nodeConfig is the command line of orrery node, as parseNode reads it, with the seams that tests set. Production
// leaves every seam zero.
type nodeConfig struct {
	server      tmuxctl.Server
	controller  string
	listen      string
	relay       string
	secretFile  string
	holderScope string
	roots       listFlag
	// daemonSocket is the global --socket, which names the daemon socket that the local sockets lie beside when
	// serveSessions is set.
	daemonSocket string
	// serveSessions is whether the node binds the daemon socket, the notices.sock and the sessions.sock beside it, and
	// forwards sessions.sock to relay. runNode sets it. A node without it, as the tests that start one directly are,
	// binds none of them.
	serveSessions bool
	seams         nodeSeams
}

// nodeSeams are the test seams of orrery node. Each zero field takes the production value.
type nodeSeams struct {
	// whoIs names each peer. Nil takes node.Tailnet().
	whoIs node.WhoIs
	// listen binds addr for the agent. Nil takes a TCP listener.
	listen func(addr string) (net.Listener, error)
	// stateDir is the state directory that holds the holders. Empty takes the directory of the store file.
	stateDir string
	// claude is the claude binary the holders run. Empty takes nodeClaude.
	claude string
}

// runNode runs `orrery node`, which serves node.v1 to the controller its flags name, until SIGINT or SIGTERM. The tmux
// server named by the global flags is the one its panes are of. It also serves the daemon socket that the global
// --socket names, the notices.sock beside it, and the sessions.sock beside it, which forwards to --relay.
func runNode(ctx context.Context, opts globals, args []string, stderr io.Writer, getenv func(string) string) error {
	cfg, err := parseNode(args)
	if err != nil {
		return err
	}
	cfg.server = opts.server
	cfg.daemonSocket = opts.daemonSocket
	cfg.serveSessions = true
	return serveNode(ctx, cfg, stderr, getenv)
}

// parseNode reads the flags of orrery node. Each refusal is a usage error that names the flag or argument it refuses.
// --relay is required, and its value is the address the sessions.sock forwarder dials.
func parseNode(args []string) (nodeConfig, error) {
	flags := flag.NewFlagSet("orrery node", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	controller := flags.String("controller", "", "node name of the controller whose calls the agent serves")
	relay := flags.String("relay", "", "HOST:PORT of the controller's relay")
	listen := flags.String("listen", "", "HOST:PORT the agent serves node.v1 on, on the tailnet")
	secretFile := flags.String("secret-file", "", "file that holds the fleet secret each call must carry")
	holderScope := flags.String("holder-scope", "",
		"absolute path of systemd-run, which runs each holder in its own user scope")
	var roots listFlag
	flags.Var(&roots, "root", "absolute directory whose folders the agent reports, repeatable")
	if err := flags.Parse(args); err != nil {
		return nodeConfig{}, usagef("node: %w", err)
	}
	if flags.NArg() > 0 {
		return nodeConfig{}, usagef("node: unexpected argument %q", flags.Arg(0))
	}
	switch {
	case *controller == "":
		return nodeConfig{}, usagef("node: --controller is required")
	case *relay == "":
		return nodeConfig{}, usagef("node: --relay is required")
	case *listen == "":
		return nodeConfig{}, usagef("node: --listen is required")
	case *secretFile == "":
		return nodeConfig{}, usagef("node: --secret-file is required")
	case *holderScope != "" && !filepath.IsAbs(*holderScope):
		return nodeConfig{}, usagef("node: --holder-scope needs an absolute path, got %q", *holderScope)
	}
	for _, root := range roots {
		if !filepath.IsAbs(root) {
			return nodeConfig{}, usagef("node: --root needs an absolute path, got %q", root)
		}
	}
	return nodeConfig{
		controller:  *controller,
		listen:      *listen,
		relay:       *relay,
		secretFile:  *secretFile,
		holderScope: *holderScope,
		roots:       roots,
	}, nil
}

// serveNode serves node.v1 as cfg describes. It reads the fleet secret first, so a secret file that is refused starts
// nothing. It builds the agent, binds the local sockets when cfg serves them, listens on cfg's address, and serves
// until SIGINT or SIGTERM. The local sockets are bound before the address is listened on, so a listener that reports
// its address has them beside it. Its panes run alongside the agent, and a failure of any of them ends all.
func serveNode(ctx context.Context, cfg nodeConfig, stderr io.Writer, getenv func(string) string) error {
	secret, err := node.ReadSecret(cfg.secretFile)
	if err != nil {
		return fmt.Errorf("--secret-file: %w", err)
	}
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	logger := slog.New(slog.NewTextHandler(stderr, nil))
	agent, host, err := newNodeAgent(ctx, cfg, secret, logger, getenv)
	if err != nil {
		return err
	}
	var local localSockets
	if cfg.serveSessions {
		if local, err = listenLocal(cfg, logger, getenv); err != nil {
			return err
		}
	}
	ln, err := listenNode(ctx, cfg)
	if err != nil {
		return errors.Join(err, local.close())
	}
	fwd := hostrelay.ForwardConfig{
		Address: cfg.relay,
		Dial:    (&net.Dialer{}).DialContext,
		Secret:  secret,
		Logger:  logger,
	}
	return serveAgent(ctx, agent, host, ln, local, fwd)
}

// localSockets are the sockets that serve this host's verbs: the daemon socket that protocol.SocketPath resolves, the
// notices.sock beside it, and the sessions.sock beside them, which forwards to the relay. The zero value binds none of
// them. The calls that the daemon socket and the notices socket make to the controller go through sessions.sock, as the
// CLI's calls do, so each reaches the relay as this host.
type localSockets struct {
	daemon   net.Listener
	notices  net.Listener
	sessions net.Listener
	// fleet is the controller's Fleet service, which each call reaches through sessions.sock.
	fleet  enginev1connect.FleetClient
	logger *slog.Logger
}

// listenLocal binds the local sockets beside the daemon socket that protocol.SocketPath resolves from cfg.daemonSocket
// and getenv. It binds the notices socket, then the sessions.sock, then the daemon socket, so a client that finds the
// daemon socket finds the others listening. Each socket is mode 0600, and a stale socket file is replaced only when
// nothing answers on it. A failure closes what was bound and names the socket that failed.
func listenLocal(cfg nodeConfig, logger *slog.Logger, getenv func(string) string) (localSockets, error) {
	socket, err := protocol.SocketPath(cfg.daemonSocket, getenv)
	if err != nil {
		return localSockets{}, fmt.Errorf("node: %w", err)
	}
	local := localSockets{logger: logger}
	if local.notices, err = daemon.Listen(deskstream.SocketPath(socket)); err != nil {
		return localSockets{}, fmt.Errorf("node: notices socket: %w", err)
	}
	if local.sessions, err = enginehost.Listen(protocol.SessionSocketPath(socket)); err != nil {
		return localSockets{}, errors.Join(fmt.Errorf("node: sessions socket: %w", err), local.close())
	}
	if local.daemon, err = daemon.Listen(socket); err != nil {
		return localSockets{}, errors.Join(fmt.Errorf("node: daemon socket: %w", err), local.close())
	}
	sessionsHTTP := cli.SessionHTTPClient(protocol.SessionSocketPath(socket))
	local.fleet = enginev1connect.NewFleetClient(sessionsHTTP, cli.SessionBaseURL)
	return local, nil
}

// close closes each local socket that is bound, and returns the errors of those that fail to close.
func (l localSockets) close() error {
	var errs []error
	for _, ln := range []net.Listener{l.daemon, l.notices, l.sessions} {
		if ln == nil {
			continue
		}
		if err := ln.Close(); err != nil {
			errs = append(errs, fmt.Errorf("node: close a local socket: %w", err))
		}
	}
	return errors.Join(errs...)
}

// listenNode binds cfg's listen address, through the listen seam when a test sets one. A failure names the address.
func listenNode(ctx context.Context, cfg nodeConfig) (net.Listener, error) {
	var (
		ln  net.Listener
		err error
	)
	if cfg.seams.listen != nil {
		ln, err = cfg.seams.listen(cfg.listen)
	} else {
		var lc net.ListenConfig
		ln, err = lc.Listen(ctx, "tcp", cfg.listen)
	}
	if err != nil {
		return nil, fmt.Errorf("node: listen on %s: %w", cfg.listen, err)
	}
	return ln, nil
}

// newNodeAgent returns the agent that cfg describes, and the panes of this machine that it serves. Its holders are a
// holderhost.Local over the state directory, with the environment, shell and scope that orreryd's local holders run
// with, so a restart finds the holders a previous run left. Its WhoIs is node.Tailnet() unless a test sets one.
func newNodeAgent(
	ctx context.Context, cfg nodeConfig, secret string, logger *slog.Logger, getenv func(string) string,
) (*node.Agent, *panehost.Host, error) {
	stateDir := cfg.seams.stateDir
	if stateDir == "" {
		dir, err := nodeStateDir(getenv)
		if err != nil {
			return nil, nil, err
		}
		stateDir = dir
	}
	claude := cfg.seams.claude
	if claude == "" {
		claude = nodeClaude
	}
	shell := getenv("SHELL")
	if !filepath.IsAbs(shell) {
		return nil, nil, fmt.Errorf("node: SHELL %q is not an absolute path", shell)
	}
	environ, err := enginehost.GradleEnviron(
		ctx, hostexec.NewLocal(), os.Environ(), stateDir, enginehost.UserGradleHome(getenv))
	if err != nil {
		return nil, nil, fmt.Errorf("node: session environment: %w", err)
	}
	holders := holderhost.NewLocal(holderhost.LocalConfig{
		Shell:    shell,
		Claude:   claude,
		Environ:  environ,
		StateDir: stateDir,
		Scope:    cfg.holderScope,
		CronHook: true,
	})
	panes, err := nodePanes(cfg, claude, logger, getenv)
	if err != nil {
		return nil, nil, err
	}
	configDir, claudeDir, err := nodeHostDirs(getenv)
	if err != nil {
		return nil, nil, err
	}
	whoIs := cfg.seams.whoIs
	if whoIs == nil {
		whoIs = node.Tailnet()
	}
	agent, err := node.NewAgent(node.AgentConfig{
		Controller: cfg.controller,
		WhoIs:      whoIs,
		Holders:    holders,
		StateDir:   stateDir,
		Scope:      cfg.holderScope,
		Panes:      panes,
		Roots:      cfg.roots,
		ConfigDir:  configDir,
		ClaudeDir:  claudeDir,
		Local:      hostexec.NewLocal(),
		Secret:     secret,
		WakePane:   cli.WakePane,
		Logger:     logger,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("node: %w", err)
	}
	return agent, panes, nil
}

// nodePanes returns the panes of this machine that the agent serves: a panehost.Host over the session registry under
// HOME, which is made if it is missing, and over the default tmux server cfg names. Its load is this machine's load,
// and its summaries run claude. Its watch runs once serveAgent runs it.
func nodePanes(cfg nodeConfig, claude string, logger *slog.Logger, getenv func(string) string) (*panehost.Host, error) {
	home := getenv("HOME")
	if home == "" {
		return nil, errors.New("node: HOME is not set, so there is no session registry")
	}
	dir := filepath.Join(home, ".claude", "sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("node: create session registry: %w", err)
	}
	name, err := os.Hostname()
	if err != nil {
		return nil, fmt.Errorf("node: read the machine's host name: %w", err)
	}
	host, err := panehost.New(panehost.Config{
		Name:          name,
		Dir:           dir,
		Home:          home,
		Proc:          paneregistry.SystemProc(),
		DefaultServer: cfg.server,
		Load:          hostload.Local,
		Claude:        claude,
		Logger:        logger,
	})
	if err != nil {
		return nil, fmt.Errorf("node: panes: %w", err)
	}
	return host, nil
}

// serveAgent runs host's panes, forwards sessions to the relay as fwd describes, serves the local sockets, and serves
// agent on ln until ctx ends. A failure of any of them ends the others too, and the error of each is returned. The
// listener is closed on return, unless the agent has already closed it. Forward closes sessions, and the local sockets
// close once ctx ends.
func serveAgent(
	ctx context.Context, agent *node.Agent, host *panehost.Host, ln net.Listener, local localSockets,
	fwd hostrelay.ForwardConfig,
) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	panesErr := goServe(cancel, func() error { return host.Run(ctx) })
	forwardErr := goServe(cancel, func() error { return forwardSessions(ctx, local.sessions, fwd) })
	daemonErr := goServe(cancel, func() error { return local.serveDaemon(ctx) })
	noticesErr := goServe(cancel, func() error { return local.serveDeskStream(ctx) })
	serveErr := agent.Serve(ctx, ln)
	cancel()
	if err := ln.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		serveErr = errors.Join(serveErr, fmt.Errorf("node: close the listener: %w", err))
	}
	return errors.Join(serveErr, <-panesErr, <-forwardErr, <-daemonErr, <-noticesErr)
}

// goServe runs run on a goroutine of its own and returns the channel that receives its error. A run that fails calls
// cancel, so that the others stop too.
func goServe(cancel context.CancelFunc, run func() error) <-chan error {
	errs := make(chan error, 1)
	go func() {
		err := run()
		if err != nil {
			cancel()
		}
		errs <- err
	}()
	return errs
}

// forwardSessions forwards sessions to the relay as fwd describes until ctx ends. A node that binds no sessions.sock,
// as a test that starts an agent directly does, has none to forward, so it returns nil at once.
func forwardSessions(ctx context.Context, sessions net.Listener, fwd hostrelay.ForwardConfig) error {
	if sessions == nil {
		return nil
	}
	if err := hostrelay.Forward(ctx, sessions, fwd); err != nil {
		return fmt.Errorf("node: forward the sessions socket: %w", err)
	}
	return nil
}

// serveDaemon serves the daemon socket until ctx ends, with the answers of a node. A sessions question is answered on,
// since a node runs sessions. Each seen report goes to the controller's Fleet.ReportSeen. Each read or input report is
// logged and dropped, since a host runs no engine, and serve sends those reports on every read and input. A nil daemon
// socket serves nothing.
func (l localSockets) serveDaemon(ctx context.Context) error {
	if l.daemon == nil {
		return nil
	}
	err := daemon.Serve(ctx, l.daemon, daemon.Config{
		Sessions: true,
		Seen:     func(report daemon.SeenReport) { l.reportSeen(ctx, report) },
		Read: func(report daemon.ReadReport) {
			l.logger.DebugContext(ctx, "drop read report", "pane", report.PaneID)
		},
		Viewing: func(report daemon.ViewReport) {
			l.logger.DebugContext(ctx, "drop input report", "input", report.Input)
		},
		Logger: l.logger,
	})
	if err != nil {
		return fmt.Errorf("node: serve the daemon socket: %w", err)
	}
	return nil
}

// reportSeen sends a seen report to the controller's Fleet.ReportSeen through sessions.sock. The hook that wrote the
// report has gone by then, so a call that fails is logged, not returned.
func (l localSockets) reportSeen(ctx context.Context, report daemon.SeenReport) {
	callCtx, cancel := context.WithTimeout(ctx, seenTimeout)
	defer cancel()
	_, err := l.fleet.ReportSeen(callCtx, connect.NewRequest(&v1.FleetReportSeenRequest{
		Pane: report.PaneID,
		Seen: report.Seen,
	}))
	if err != nil {
		l.logger.WarnContext(ctx, "report seen", "pane", report.PaneID, "error", err)
	}
}

// serveDeskStream serves the notices socket until ctx ends: each connection gets its own desk stream, and the listener
// closes once ctx ends. A nil notices socket serves nothing.
func (l localSockets) serveDeskStream(ctx context.Context) error {
	if l.notices == nil {
		return nil
	}
	var conns sync.WaitGroup
	defer conns.Wait()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	context.AfterFunc(ctx, func() {
		if err := l.notices.Close(); err != nil {
			l.logger.ErrorContext(ctx, "close the notices socket", "error", err)
		}
	})
	for {
		conn, err := l.notices.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("node: accept on the notices socket: %w", err)
		}
		conns.Add(1)
		go func() {
			defer conns.Done()
			l.streamDesk(ctx, conn)
		}()
	}
}

// streamDesk writes the controller's desk stream to conn until the stream or conn ends. Each message of the stream is
// written as the deskstream JSON line that a desk reads. The lines the desk sends back are read and discarded, as the
// deskstream hub does, and the desk's end of the connection ends the stream.
func (l localSockets) streamDesk(ctx context.Context, conn net.Conn) {
	defer func() {
		if err := conn.Close(); err != nil {
			l.logger.DebugContext(ctx, "close the notices stream", "error", err)
		}
	}()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		defer cancel()
		if _, err := io.Copy(io.Discard, conn); err != nil {
			l.logger.DebugContext(ctx, "read the notices stream", "error", err)
		}
	}()
	stream, err := l.fleet.WatchDesk(ctx, connect.NewRequest(&v1.FleetWatchDeskRequest{}))
	if err != nil {
		l.logger.WarnContext(ctx, "watch desk", "error", err)
		return
	}
	defer func() {
		if err := stream.Close(); err != nil {
			l.logger.DebugContext(ctx, "close the desk stream", "error", err)
		}
	}()
	for stream.Receive() {
		if err := writeDeskLine(conn, stream.Msg()); err != nil {
			l.logger.DebugContext(ctx, "write the notices stream", "error", err)
			return
		}
	}
	if err := stream.Err(); err != nil && ctx.Err() == nil {
		l.logger.WarnContext(ctx, "watch desk", "error", err)
	}
}

// writeDeskLine writes msg to out as the deskstream JSON line that a desk reads, newline included.
func writeDeskLine(out io.Writer, msg *v1.FleetWatchDeskResponse) error {
	line, err := json.Marshal(deskstream.Line{
		Kind:  msg.GetKind(),
		Host:  msg.GetHost(),
		State: msg.GetState(),
		Title: msg.GetTitle(),
		Body:  msg.GetBody(),
		Quiet: msg.GetQuiet(),
	})
	if err != nil {
		return fmt.Errorf("encode desk line: %w", err)
	}
	if _, err = out.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("write desk line: %w", err)
	}
	return nil
}

// nodeHostDirs returns the orrery config directory, $XDG_CONFIG_HOME/orrery or else $HOME/.config/orrery, and Claude's
// directory, $HOME/.claude. The agent's git, file, gate and leftover ops may name paths under them, beside its roots and
// its state directory. The Claude directory is empty when HOME is not set. It fails when neither XDG_CONFIG_HOME nor HOME
// is set, since then there is no config directory.
func nodeHostDirs(getenv func(string) string) (configDir, claudeDir string, err error) {
	home := getenv("HOME")
	configHome := getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		if home == "" {
			return "", "", errors.New("node: neither XDG_CONFIG_HOME nor HOME is set, so there is no config directory")
		}
		configHome = filepath.Join(home, ".config")
	}
	if home != "" {
		claudeDir = filepath.Join(home, ".claude")
	}
	return filepath.Join(configHome, "orrery"), claudeDir, nil
}

// nodeStateDir returns the absolute directory that the store file lies in, which is the state directory orreryd's
// sessions use.
func nodeStateDir(getenv func(string) string) (string, error) {
	storePath, err := store.Path(getenv)
	if err != nil {
		return "", fmt.Errorf("node: state directory: %w", err)
	}
	dir, err := filepath.Abs(filepath.Dir(storePath))
	if err != nil {
		return "", fmt.Errorf("node: state directory: %w", err)
	}
	return dir, nil
}
