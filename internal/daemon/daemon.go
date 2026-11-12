// Package daemon serves the hook socket: it reads one hook frame per connection, applies it to the
// pane's tmux server through state.Apply, and reports each state change to observers.
package daemon

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/state"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

const (
	// readTimeout bounds how long a connection may take to deliver its frame.
	readTimeout = 2 * time.Second
	// probeTimeout bounds the dial that decides whether a socket file has a live listener.
	probeTimeout = time.Second
	// defaultTidyInterval is the tidy period when Config.TidyInterval is not positive.
	defaultTidyInterval = 10 * time.Second
	// defaultSocketSkipMsg is the warning logged when the tidy skips the default socket.
	defaultSocketSkipMsg = "tmux default socket skipped"
)

// Config configures Serve.
type Config struct {
	// Server, when non-zero, selects the tmux server for every frame instead of the frame's $TMUX.
	Server tmuxctl.Server
	// DefaultServer, when non-zero and Server is zero, is tidied with the servers frames name, so the default server's
	// panes have rows from the first tidy. A frame whose $TMUX names the same socket names the same server.
	DefaultServer tmuxctl.Server
	// Mirror, when set, syncs each applied frame's pane to the store after its tmux writes and observers, outside the
	// frame mutex, and tidies the store against tmux at start and every TidyInterval. Nil skips both.
	Mirror *Mirror
	// TidyInterval is how often Serve tidies with Mirror; zero or negative means 10 s.
	TidyInterval time.Duration
	// Observers are called in order, synchronously, for each state change. They must return quickly.
	Observers []Observer
	// Seen receives each seen report whose pane is set, in the connection's goroutine. Nil drops seen reports.
	Seen func(SeenReport)
	// Logger receives read, resolve and apply errors. Nil discards them.
	Logger *slog.Logger
}

// SeenReport is one seen report: the pane the sender marked seen and the seen time it wrote.
type SeenReport struct {
	PaneID string
	Seen   int64
}

// Observer receives one state change.
type Observer func(Transition)

// Transition is one state change of a pane, with the frame that caused it.
type Transition struct {
	PaneID string
	Server tmuxctl.Server
	From   protocol.State
	To     protocol.State
	Frame  protocol.HookFrame
}

// Listen returns a listener on the unix socket socketPath. It creates the parent directory with mode
// 0700 when absent, replaces a stale socket file, and returns an error when a live listener holds the path.
// It makes the socket file mode 0600 whatever the umask, so only its owner can connect.
func Listen(socketPath string) (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o700); err != nil {
		return nil, fmt.Errorf("create socket directory: %w", err)
	}
	if err := removeStaleSocket(socketPath); err != nil {
		return nil, err
	}
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", socketPath, err)
	}
	if err = os.Chmod(socketPath, 0o600); err != nil {
		return nil, errors.Join(fmt.Errorf("restrict socket %s: %w", socketPath, err), ln.Close())
	}
	return ln, nil
}

// removeStaleSocket deletes path only when it is a socket that refuses connections. It returns an error
// when a listener accepts on path, when path is not a socket, or when the probe fails for another reason.
func removeStaleSocket(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat %s: %w", path, err)
	}
	if info.Mode().Type() != fs.ModeSocket {
		return fmt.Errorf("%s exists and is not a socket", path)
	}

	dialer := net.Dialer{Timeout: probeTimeout}
	conn, err := dialer.DialContext(context.Background(), "unix", path)
	if err == nil {
		if closeErr := conn.Close(); closeErr != nil {
			return fmt.Errorf("close probe connection: %w", closeErr)
		}
		return fmt.Errorf("socket %s has a live listener", path)
	}
	if !errors.Is(err, syscall.ECONNREFUSED) {
		return fmt.Errorf("probe socket %s: %w", path, err)
	}
	if err = os.Remove(path); err != nil {
		return fmt.Errorf("remove stale socket: %w", err)
	}
	return nil
}

// Serve accepts connections on ln until ctx is done, then closes ln, waits for in-flight connections,
// and returns nil. Any other accept failure is returned once in-flight connections finish. With a Mirror, Serve
// also runs the tidy loop and returns only after that loop stops.
func Serve(ctx context.Context, ln net.Listener, cfg Config) error {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	stop := context.AfterFunc(ctx, func() {
		if err := ln.Close(); err != nil {
			logger.ErrorContext(ctx, "close listener", "error", err)
		}
	})
	defer stop()

	frames := &frameHandler{cfg: cfg, logger: logger, servers: map[tmuxctl.Server]bool{}}
	var tidies sync.WaitGroup
	tidyCtx, cancelTidy := context.WithCancel(ctx)
	defer tidies.Wait()
	defer cancelTidy()
	if cfg.Mirror != nil {
		tidies.Add(1)
		go func() {
			defer tidies.Done()
			frames.tidyLoop(tidyCtx)
		}()
	}
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("accept: %w", err)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			frames.serveConn(ctx, conn)
		}()
	}
}

// frameHandler applies frames. Its mutex serializes state.Apply and the observer calls for each frame; the store
// sync runs after the mutex is released. serversMu guards servers, the tmux servers frames have named, and
// defaultSkipReason, the reason the default socket was last skipped, which the tidy loop reads and writes.
type frameHandler struct {
	cfg               Config
	logger            *slog.Logger
	mu                sync.Mutex
	serversMu         sync.Mutex
	servers           map[tmuxctl.Server]bool
	defaultSkipReason string
}

// serveConn reads one frame from conn, applies it, and closes conn; the close tells the sender the frame
// was handled. The frame is applied even when ctx is done, so an accepted event is never half-written. The
// read deadline bounds the wait for the frame.
func (h *frameHandler) serveConn(ctx context.Context, conn net.Conn) {
	defer func() {
		if err := conn.Close(); err != nil {
			h.logger.WarnContext(ctx, "close connection", "error", err)
		}
	}()
	if err := conn.SetReadDeadline(time.Now().Add(readTimeout)); err != nil {
		h.logger.WarnContext(ctx, "set read deadline", "error", err)
		return
	}
	frame, err := protocol.ReadFrame(conn)
	if err != nil {
		h.logger.WarnContext(ctx, "read hook frame", "error", err)
		return
	}
	if frame.Hook == nil {
		h.reportSeen(ctx, frame)
		return
	}
	if frame.TmuxPane == "" {
		h.logger.WarnContext(ctx, "drop hook frame", "reason", "empty tmuxPane")
		return
	}
	h.apply(context.WithoutCancel(ctx), frame)
}

// reportSeen passes a seen report to Config.Seen. It drops the report when its pane is empty or Config.Seen is nil,
// and never touches tmux or the pane state.
func (h *frameHandler) reportSeen(ctx context.Context, frame protocol.HookFrame) {
	if frame.TmuxPane == "" {
		h.logger.WarnContext(ctx, "drop seen report", "reason", "empty tmuxPane")
		return
	}
	if h.cfg.Seen == nil {
		return
	}
	h.cfg.Seen(SeenReport{PaneID: frame.TmuxPane, Seen: frame.Seen})
}

// apply runs one frame through state.Apply under the mutex and calls every observer when the state changes. With a
// Mirror, it then syncs the frame's pane to the store outside the mutex, so a slow or locked store delays only this
// frame's connection and never the frames queued behind it.
func (h *frameHandler) apply(ctx context.Context, frame protocol.HookFrame) {
	server := resolveServer(h.cfg.Server, frame.Tmux)
	h.noteServer(server)
	client, err := tmuxctl.New(server)
	if err != nil {
		h.logger.ErrorContext(ctx, "resolve tmux server", "error", err)
		return
	}
	if !h.applyLocked(ctx, client, server, frame) {
		return
	}
	if h.cfg.Mirror != nil {
		h.cfg.Mirror.SyncPane(ctx, server, frame.TmuxPane)
	}
}

// noteServer records server as one a frame has named, so the tidy loop tidies it.
func (h *frameHandler) noteServer(server tmuxctl.Server) {
	h.serversMu.Lock()
	defer h.serversMu.Unlock()
	h.servers[server] = true
}

// tidyLoop tidies the servers at start and then every TidyInterval until ctx is done.
func (h *frameHandler) tidyLoop(ctx context.Context) {
	interval := h.cfg.TidyInterval
	if interval <= 0 {
		interval = defaultTidyInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		for _, server := range h.tidyServers() {
			h.cfg.Mirror.Tidy(ctx, server)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// tidyServers returns the servers to tidy: Config.Server when it is set, else every server a frame has named and
// Config.DefaultServer when it is set, no frame has named it yet, and its directory passes checkDefaultSocket.
func (h *frameHandler) tidyServers() []tmuxctl.Server {
	if h.cfg.Server != (tmuxctl.Server{}) {
		return []tmuxctl.Server{h.cfg.Server}
	}
	h.serversMu.Lock()
	defer h.serversMu.Unlock()
	servers := slices.Collect(maps.Keys(h.servers))
	if h.cfg.DefaultServer != (tmuxctl.Server{}) && !h.servers[h.cfg.DefaultServer] && h.checkDefaultSocket() {
		servers = append(servers, h.cfg.DefaultServer)
	}
	return servers
}

// checkDefaultSocket reports whether the default socket's directory passes tmux's own check. It logs a skip when
// the reason differs from the one last logged, and forgets the reason once the check passes. The caller holds
// serversMu.
func (h *frameHandler) checkDefaultSocket() bool {
	reason := defaultSocketProblem(h.cfg.DefaultServer.Socket)
	if reason == h.defaultSkipReason {
		return reason == ""
	}
	h.defaultSkipReason = reason
	if reason != "" {
		h.logger.Warn(defaultSocketSkipMsg, "server", serverValue(h.cfg.DefaultServer), "reason", reason)
	}
	return reason == ""
}

// defaultSocketProblem returns why tmux would refuse the directory that holds socket, or "" when it would accept it.
// It applies the check of tmux's make_label: the directory must be a real directory (lstat, so a symlink fails), owned
// by the daemon's uid, with no permission bits for other users.
func defaultSocketProblem(socket string) string {
	info, err := os.Lstat(filepath.Dir(socket))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "directory missing"
	case err != nil:
		return "directory unreadable"
	case !info.IsDir():
		return "not a plain directory"
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "owner unknown"
	}
	if int(stat.Uid) != os.Getuid() {
		return "owner is not the daemon user"
	}
	if info.Mode().Perm()&0o007 != 0 {
		return "other users have access"
	}
	return ""
}

// applyLocked runs state.Apply, the context and session writes, and the observers under the mutex. It reports
// whether state.Apply succeeded.
func (h *frameHandler) applyLocked(
	ctx context.Context, client *tmuxctl.Client, server tmuxctl.Server, frame protocol.HookFrame,
) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	transition, changed, err := state.Apply(ctx, client, frame.TmuxPane, frame.Hook, time.Now())
	if err != nil {
		h.logger.ErrorContext(ctx, "apply hook frame", "pane", frame.TmuxPane, "error", err)
		return false
	}
	contextName := protocol.SummaryContext(frame.AnthropicBaseURL)
	err = client.SetPaneOption(ctx, frame.TmuxPane, protocol.OptionContext, contextName)
	if err != nil {
		h.logger.ErrorContext(ctx, "set pane context", "pane", frame.TmuxPane, "error", err)
	}
	if err = state.RecordSession(ctx, client, frame.TmuxPane, frame.Hook); err != nil {
		h.logger.ErrorContext(ctx, "record pane session", "pane", frame.TmuxPane, "error", err)
	}
	if !changed {
		return true
	}
	event := Transition{
		PaneID: frame.TmuxPane,
		Server: server,
		From:   transition.From,
		To:     transition.To,
		Frame:  frame,
	}
	for _, observe := range h.cfg.Observers {
		observe(event)
	}
	return true
}

// resolveServer returns configured when it is set. Otherwise it returns the socket named by the frame's
// $TMUX, the part before its first comma, and the default server when $TMUX is empty.
func resolveServer(configured tmuxctl.Server, tmuxEnv string) tmuxctl.Server {
	if configured != (tmuxctl.Server{}) {
		return configured
	}
	socket, _, _ := strings.Cut(tmuxEnv, ",")
	return tmuxctl.Server{Socket: socket}
}
