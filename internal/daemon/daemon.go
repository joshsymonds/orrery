// Package daemon serves the hook socket: it reads one frame per connection and reports its seen, read and input
// reports or answers its sessions question. It applies the session registry's pane updates to the pane's tmux server
// through state.ApplyPane, and reports each state change to observers.
package daemon

import (
	"context"
	"encoding/json"
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
	"github.com/joshsymonds/orrery/internal/stamp"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

const (
	// readTimeout bounds how long a connection may take to deliver its frame.
	readTimeout = 2 * time.Second
	// probeTimeout bounds the dial that decides whether a socket file has a live listener.
	probeTimeout = time.Second
	// answerTimeout bounds the write of a sessions answer, so a sender that stops reading holds no connection.
	answerTimeout = time.Second
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
	// Mirror, when set, syncs each applied pane update's pane to the store after its tmux writes and observers, outside
	// the update mutex, and tidies the store against tmux at start (or when TidyStart closes) and every TidyInterval.
	// Nil skips both.
	Mirror *Mirror
	// TidyInterval is how often Serve tidies with Mirror; zero or negative means 10 s.
	TidyInterval time.Duration
	// TidyTicks, when non-nil, is where Serve takes the ticks of its later tidies in place of a ticker every
	// TidyInterval. It is a test seam: production leaves it nil.
	TidyTicks <-chan time.Time
	// TidyStart, when non-nil and Mirror is set, holds the first tidy until it is closed; Serve then tidies at once
	// and every TidyInterval after. Nil tidies at start. Frame syncs never wait for it. If ctx is done before it
	// closes, Serve tidies nothing.
	TidyStart <-chan struct{}
	// AfterTidy, when set and Mirror is set, runs on the tidy loop's goroutine after each tidy pass. It must return
	// quickly, since the next pass waits for it. orreryd retries the notice row writes the store refused with it.
	AfterTidy func(context.Context)
	// Updates, when non-nil, is where Serve takes the session registry's pane updates. Serve applies each one in order on
	// one goroutine, until ctx is done. Nil takes none.
	Updates <-chan PaneUpdate
	// Observers are called in order, synchronously, for each state change. They must return quickly.
	Observers []Observer
	// Seen receives each seen report whose pane is set, in the connection's goroutine. Nil drops seen reports.
	Seen func(SeenReport)
	// Read receives each read report whose pane is set, in the connection's goroutine. Nil drops read reports.
	Read func(ReadReport)
	// Input receives the time, in unix seconds, of each input report, in the connection's goroutine. Nil drops input
	// reports.
	Input func(input int64)
	// Viewing receives each input report's time and the viewing key it carries, in the connection's goroutine. Nil
	// drops them.
	Viewing func(ViewReport)
	// Logger receives read, resolve and apply errors. Nil discards them.
	Logger *slog.Logger
	// Home is the user's home directory. Each applied pane update's Cwd is searched for a context stamp below it. Empty
	// means no stamp is found.
	Home string
	// Sessions is whether this orreryd serves sessions.sock. A sessions question is answered with it.
	Sessions bool
}

// SeenReport is one seen report: the pane the sender marked seen and the seen time it wrote.
type SeenReport struct {
	PaneID string
	Seen   int64
}

// ReadReport is one read report: the pane the phone read and the read time serve wrote.
type ReadReport struct {
	PaneID string
	Read   int64
}

// ViewReport is one viewing report: the input time serve wrote and the key the web view views with it, which is the
// zero protocol.ViewKey when it views none.
type ViewReport struct {
	Input int64
	View  protocol.ViewKey
}

// Observer receives one state change.
type Observer func(Transition)

// Transition is one state change of a pane, with the facts of the pane update that caused it that its observers read.
type Transition struct {
	PaneID string
	Server tmuxctl.Server
	// Host is the orrery name of the pane's host, and is empty for a pane of the controller's own tmux.
	Host string
	From protocol.State
	To   protocol.State
	// Cwd and BaseURL are the update's working directory and API base URL.
	Cwd     string
	BaseURL string
	// Transcript is the session's transcript path, and LastMessage is the last message of its turn.
	Transcript  string
	LastMessage string
	// Failed is whether the turn ended in an API error, and APIError is that error when it did.
	Failed   bool
	APIError string
}

// Hook JSON keys and event that a stored legacy transition's facts read. Claude Code writes them in snake case.
const (
	keyHookEventName        = "hook_event_name"
	keyTranscriptPath       = "transcript_path"
	keyLastAssistantMessage = "last_assistant_message"
	keyAPIError             = "error"
	stopFailureEvent        = "StopFailure"
)

// SetHookFacts sets the facts of t that its hook carries: Transcript and LastMessage from transcript_path and
// last_assistant_message, and Failed and APIError from hook_event_name and error. A hook that is not a JSON object sets
// none of them. A field that does not decode leaves its fact empty, and the returned error reports every such field, so
// the other facts are still set.
func (t *Transition) SetHookFacts(hook json.RawMessage) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(hook, &fields); err != nil {
		return fmt.Errorf("decode hook: %w", err)
	}
	var errs []error
	text := func(key string) string {
		value, err := hookText(fields, key)
		errs = append(errs, err)
		return value
	}
	t.Transcript = text(keyTranscriptPath)
	t.LastMessage = text(keyLastAssistantMessage)
	t.Failed = text(keyHookEventName) == stopFailureEvent
	t.APIError = ""
	if t.Failed {
		t.APIError = text(keyAPIError)
	}
	return errors.Join(errs...)
}

// hookText returns the string value of key in fields, or "" when key is absent or null. A value that is not a string
// is an error.
func hookText(fields map[string]json.RawMessage, key string) (string, error) {
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
// also runs the tidy loop and returns only after that loop stops. With Updates, Serve applies the registry's pane
// updates on a goroutine of its own, and returns only after that goroutine stops.
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
	var updates sync.WaitGroup
	updateCtx, cancelUpdates := context.WithCancel(ctx)
	defer updates.Wait()
	defer cancelUpdates()
	if cfg.Updates != nil {
		updates.Add(1)
		go func() {
			defer updates.Done()
			frames.applyUpdates(updateCtx, cfg.Updates)
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

// frameHandler applies pane updates. Its mutex serializes state.ApplyPane and the observer calls for each update; the
// store sync runs after the mutex is released. serversMu guards servers, the tmux servers frames have named, and
// defaultSkipReason, the reason the default socket was last skipped, which the tidy loop reads and writes.
type frameHandler struct {
	cfg               Config
	logger            *slog.Logger
	mu                sync.Mutex
	serversMu         sync.Mutex
	servers           map[tmuxctl.Server]bool
	defaultSkipReason string
}

// serveConn reads one frame from conn, answers or reports it, and closes conn; the close tells the sender the frame
// was handled. A frame that carries none of seen, read, input or ask does not read, so it is dropped with a warning.
// The read deadline bounds the wait for the frame.
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
	if frame.Ask == protocol.SessionsAsk {
		h.answerSessions(ctx, conn)
		return
	}
	if frame.Input != 0 {
		h.reportInput(frame)
		return
	}
	if frame.Read != 0 {
		h.reportRead(ctx, frame)
		return
	}
	h.reportSeen(ctx, frame)
}

// answerSessions writes whether this orreryd serves sessions to conn. It never touches tmux, a pane, the observers,
// Config.Seen, Config.Read or Config.Input.
func (h *frameHandler) answerSessions(ctx context.Context, conn net.Conn) {
	if err := conn.SetWriteDeadline(time.Now().Add(answerTimeout)); err != nil {
		h.logger.WarnContext(ctx, "set write deadline", "error", err)
		return
	}
	if err := protocol.WriteSessionsAnswer(conn, h.cfg.Sessions); err != nil {
		h.logger.WarnContext(ctx, "write sessions answer", "error", err)
	}
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

// reportInput passes an input report's time to Config.Input and its time and viewing key to Config.Viewing. Each is
// dropped when it is nil. Neither touches tmux, the pane state, the observers, Config.Seen or Config.Read.
func (h *frameHandler) reportInput(frame protocol.HookFrame) {
	if h.cfg.Input != nil {
		h.cfg.Input(frame.Input)
	}
	if h.cfg.Viewing != nil {
		h.cfg.Viewing(ViewReport{Input: frame.Input, View: frame.View})
	}
}

// reportRead passes a read report to Config.Read. It drops the report when its pane is empty or Config.Read is nil,
// and never touches tmux, the pane state, the observers or Config.Seen.
func (h *frameHandler) reportRead(ctx context.Context, frame protocol.HookFrame) {
	if frame.TmuxPane == "" {
		h.logger.WarnContext(ctx, "drop read report", "reason", "empty tmuxPane")
		return
	}
	if h.cfg.Read == nil {
		return
	}
	h.cfg.Read(ReadReport{PaneID: frame.TmuxPane, Read: frame.Read})
}

// syncApplied syncs pane on server to the store after its apply, outside the mutex: from the output of the read that
// the apply ran in its own write when read and ok, releases the mirror lock when read and the apply failed, and
// otherwise syncs the pane from its own read when ok and there is a Mirror.
func (h *frameHandler) syncApplied(ctx context.Context, server tmuxctl.Server, pane, out string, read, ok bool) {
	switch {
	case read && ok:
		h.cfg.Mirror.SyncRead(ctx, server, pane, out)
	case read:
		h.cfg.Mirror.ReleaseRead()
	case ok && h.cfg.Mirror != nil:
		h.cfg.Mirror.SyncPane(ctx, server, pane)
	}
}

// contextWrite returns the write of a session's context to pane's @orrery_context. The context is that of the stamp in
// effect for cwd under h.cfg.Home, or else that of baseURL. When the stamp cannot be read, the option is unset, so the
// pane is left out of the intent call rather than given a context it may not hold.
func (h *frameHandler) contextWrite(ctx context.Context, pane, cwd, baseURL string) tmuxctl.OptionWrite {
	found, err := stamp.Find(h.cfg.Home, cwd)
	if err != nil {
		h.logger.ErrorContext(ctx, "find context stamp", "pane", pane, "cwd", cwd, "error", err)
		return tmuxctl.Unset(protocol.OptionContext)
	}
	contextName := protocol.SummaryContext(baseURL)
	if found.Folder != "" {
		contextName = protocol.SummaryContext(found.BaseURL())
	}
	return tmuxctl.Set(protocol.OptionContext, contextName)
}

// noteServer records server as one a frame has named, so the tidy loop tidies it.
func (h *frameHandler) noteServer(server tmuxctl.Server) {
	h.serversMu.Lock()
	defer h.serversMu.Unlock()
	h.servers[server] = true
}

// tidyLoop tidies the servers at start, or when Config.TidyStart closes, and then every TidyInterval, or at each
// value of Config.TidyTicks, until ctx is done, running Config.AfterTidy after each pass. Each server's tidy first
// ends the sessions whose Claude Code is gone (see endGoneSessions), so the store reads them ended. It returns without
// tidying when ctx is done before TidyStart closes.
func (h *frameHandler) tidyLoop(ctx context.Context) {
	if h.cfg.TidyStart != nil {
		select {
		case <-ctx.Done():
			return
		case <-h.cfg.TidyStart:
		}
	}
	ticks := h.cfg.TidyTicks
	if ticks == nil {
		interval := h.cfg.TidyInterval
		if interval <= 0 {
			interval = defaultTidyInterval
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		ticks = ticker.C
	}
	for {
		for _, server := range h.tidyServers() {
			h.endGoneSessions(ctx, server)
			h.cfg.Mirror.Tidy(ctx, server)
		}
		if h.cfg.AfterTidy != nil {
			h.cfg.AfterTidy(ctx)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticks:
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
		h.logger.Warn(defaultSocketSkipMsg, "server", h.cfg.DefaultServer.Column(), "reason", reason)
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

// resolveServer returns configured when it is set. Otherwise it returns the socket named by the frame's
// $TMUX, the part before its first comma, and the default server when $TMUX is empty.
func resolveServer(configured tmuxctl.Server, tmuxEnv string) tmuxctl.Server {
	if configured != (tmuxctl.Server{}) {
		return configured
	}
	socket, _, _ := strings.Cut(tmuxEnv, ",")
	return tmuxctl.Server{Socket: socket}
}
