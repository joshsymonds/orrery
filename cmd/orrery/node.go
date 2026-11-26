package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/joshsymonds/orrery/internal/cli"
	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/holderhost"
	"github.com/joshsymonds/orrery/internal/hostexec"
	"github.com/joshsymonds/orrery/internal/hostload"
	"github.com/joshsymonds/orrery/internal/node"
	"github.com/joshsymonds/orrery/internal/panehost"
	"github.com/joshsymonds/orrery/internal/paneregistry"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// nodeClaude is the claude binary that the agent's sessions and summaries run, as orreryd's --claude default names it.
const nodeClaude = "claude"

// nodeConfig is the command line of orrery node, as parseNode reads it, with the seams that tests set. Production
// leaves every seam zero.
type nodeConfig struct {
	server      tmuxctl.Server
	controller  string
	listen      string
	secretFile  string
	holderScope string
	roots       listFlag
	seams       nodeSeams
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
// server named by the global flags is the one its panes are of.
func runNode(ctx context.Context, opts globals, args []string, stderr io.Writer, getenv func(string) string) error {
	cfg, err := parseNode(args)
	if err != nil {
		return err
	}
	cfg.server = opts.server
	return serveNode(ctx, cfg, stderr, getenv)
}

// parseNode reads the flags of orrery node. Each refusal is a usage error that names the flag or argument it refuses.
// --relay is required, and its value is only checked to be given, since nothing in this verb reaches the relay yet.
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
		secretFile:  *secretFile,
		holderScope: *holderScope,
		roots:       roots,
	}, nil
}

// serveNode serves node.v1 as cfg describes. It reads the fleet secret first, so a secret file that is refused starts
// nothing. It builds the agent, listens on cfg's address, and serves until SIGINT or SIGTERM. Its panes run alongside
// the agent, and a failure of either ends both.
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
	ln, err := listenNode(ctx, cfg)
	if err != nil {
		return err
	}
	return serveAgent(ctx, agent, host, ln)
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

// serveAgent runs host's panes and serves agent on ln until ctx ends. A failure of the panes ends the agent too, and
// the error of either is returned. The listener is closed on return, unless the agent has already closed it.
func serveAgent(ctx context.Context, agent *node.Agent, host *panehost.Host, ln net.Listener) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	panesErr := make(chan error, 1)
	go func() {
		err := host.Run(ctx)
		if err != nil {
			cancel()
		}
		panesErr <- err
	}()
	serveErr := agent.Serve(ctx, ln)
	cancel()
	if err := ln.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		serveErr = errors.Join(serveErr, fmt.Errorf("node: close the listener: %w", err))
	}
	return errors.Join(serveErr, <-panesErr)
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
