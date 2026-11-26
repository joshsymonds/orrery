package node

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/joshsymonds/orrery/internal/holder"
	"github.com/joshsymonds/orrery/internal/holderhost"
)

// whoIsTimeout bounds each WhoIs that an agent asks of tailscaled, so that a hung tailscaled holds only the connection
// it was asked about.
const whoIsTimeout = 10 * time.Second

// holdersName is the subdirectory of the state directory that holds each session's holder directory.
const holdersName = "holders"

// panesHeaderTimeout bounds how long a pane connection waits for the controller's HTTP request headers, as the other
// servers of this repository bound theirs.
const panesHeaderTimeout = 5 * time.Second

// AgentConfig configures an Agent: the controller it admits, the WhoIs that names each peer, the holders of this machine
// that it serves, the state directory those holders live in, the scope they run in, and its logger.
type AgentConfig struct {
	// Controller is the controller's node name on the tailnet. Authorize admits only that peer.
	Controller string
	// WhoIs names each connection's peer from its remote address. An agent has one, since it serves no peer it cannot name.
	WhoIs WhoIs
	// Holders is this machine's holders, which the ops act on. In production it is a holderhost.Local over StateDir.
	Holders holderhost.Host
	// StateDir is the state directory that Holders keeps its sessions in, so each session's holder directory is
	// StateDir/holders/<id>. The attach, keeper and end ops reach a holder's socket and lock there, so they fail when it
	// is empty.
	StateDir string
	// Scope is the absolute path of systemd-run that runs each holder in its own user scope, as holderhost.LocalConfig.Scope
	// names it. Empty when the holders run plainly.
	Scope string
	// Panes is this machine's panes, which the pane ops serve. Nil means the agent serves no panes, and each pane op
	// answers CodeFailed.
	Panes HostPanes
	// Roots are the absolute directories whose folders the agent reports and checks. Each root's folders are reported
	// together, and a folder is admitted when it lies under any one of them. Empty serves none.
	Roots []string
	// Secret is the fleet secret that the agent checks each Request's Secret against, as SecretMatches compares them.
	Secret string
	// WakePane types text into pane of the tmux server column server when the pane is idle, and reports whether it did.
	// Nil wakes no pane.
	WakePane func(ctx context.Context, server, pane, text string) (bool, error)
	// Logger receives the agent's diagnostics. Nil discards them.
	Logger *slog.Logger
}

// Agent serves node.v1 for this machine's holders, to the one controller that its config admits.
type Agent struct {
	cfg AgentConfig
	// mu guards keepers and splices.
	mu sync.Mutex
	// keepers holds the keeper running on each session that has one.
	keepers map[string]*keeper
	// splices counts the splices open on each session. No keeper attaches to a session while a splice is open on it.
	splices map[string]int
	// keeping waits for every keeper goroutine.
	keeping sync.WaitGroup
}

// NewAgent returns the Agent that cfg describes. It fails when cfg names no controller, or has no WhoIs or no Holders,
// because an agent that cannot name its peer would serve any peer that connects.
func NewAgent(cfg AgentConfig) (*Agent, error) {
	switch {
	case cfg.Controller == "":
		return nil, errors.New("node: agent names no controller")
	case cfg.WhoIs == nil:
		return nil, errors.New("node: agent has no WhoIs")
	case cfg.Holders == nil:
		return nil, errors.New("node: agent has no holders")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	return &Agent{cfg: cfg, keepers: make(map[string]*keeper), splices: make(map[string]int)}, nil
}

// Serve accepts connections on listener until ctx ends, then closes listener and returns once every connection and every
// keeper has finished. Listener must be on the tailnet.
//
// Serve first asks WhoIs about listener's own address, and returns an error without accepting anything when that answer
// names no node, no user profile or no login, or names a tagged node. The login it reads is the owner's login, which
// Authorize admits.
//
// Each accepted connection is served on its own goroutine, and only after WhoIs names its peer and Authorize admits
// that peer. A connection that fails either check is closed before any frame is read, and the reason is logged. An
// admitted peer's one Request is dispatched as dispatch describes. Serve starts a keeper on each live holder under the
// state directory, and the keepers run until ctx ends.
func (a *Agent) Serve(ctx context.Context, listener net.Listener) error {
	login, err := a.ownLogin(ctx, listener)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	var conns sync.WaitGroup
	defer func() {
		cancel()
		conns.Wait()
		a.keeping.Wait()
	}()
	stop := context.AfterFunc(ctx, func() {
		if closeErr := listener.Close(); closeErr != nil {
			a.cfg.Logger.WarnContext(ctx, "node: close the listener", "err", closeErr)
		}
	})
	defer stop()
	a.keepHolders(ctx)
	for {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("node: accept: %w", acceptErr)
		}
		conns.Add(1)
		go func() {
			defer conns.Done()
			a.serveConn(ctx, conn, login)
		}()
	}
}

// ownLogin asks WhoIs about listener's own address, and returns the login that its node is signed in as. It fails when
// the answer names no node, no user profile, no login, or a tagged node.
func (a *Agent) ownLogin(ctx context.Context, listener net.Listener) (string, error) {
	addr := listener.Addr().String()
	wctx, cancel := context.WithTimeout(ctx, whoIsTimeout)
	defer cancel()
	resp, err := a.cfg.WhoIs.WhoIs(wctx, addr)
	if err != nil {
		return "", fmt.Errorf("node: WhoIs of the agent's own address %s: %w", addr, err)
	}
	switch {
	case resp == nil || resp.Node == nil:
		return "", fmt.Errorf("node: the agent's own address %s names no node", addr)
	case resp.UserProfile == nil:
		return "", fmt.Errorf("node: the agent's own address %s names no user profile", addr)
	case resp.Node.IsTagged():
		return "", fmt.Errorf("node: the agent's own address %s is a tagged node, not a device of its owner", addr)
	case resp.UserProfile.LoginName == "":
		return "", fmt.Errorf("node: the agent's own address %s names no login", addr)
	}
	return resp.UserProfile.LoginName, nil
}

// serveConn admits conn's peer, and once it is admitted, reads the one Request line that the peer sends and dispatches it.
func (a *Agent) serveConn(ctx context.Context, conn net.Conn, login string) {
	stop := context.AfterFunc(ctx, func() { a.closeConn(ctx, conn) })
	defer stop()
	defer a.closeConn(ctx, conn)
	if err := a.admit(ctx, conn, login); err != nil {
		a.cfg.Logger.WarnContext(ctx, "node: refused a connection", "remote", conn.RemoteAddr().String(), "reason", err)
		return
	}
	// The reader keeps whatever the controller sent after its Request line, since a splice carries those bytes on.
	reader := bufio.NewReader(conn)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		a.cfg.Logger.WarnContext(ctx, "node: read a request", "remote", conn.RemoteAddr().String(), "err", err)
		return
	}
	var req Request
	if err = json.Unmarshal(line, &req); err != nil {
		a.cfg.Logger.WarnContext(ctx, "node: read a request", "remote", conn.RemoteAddr().String(), "err", err)
		return
	}
	a.dispatch(ctx, conn, reader, req)
}

// admit asks WhoIs, within whoIsTimeout, about conn's remote address. It returns the reason the peer is refused, or nil
// when Authorize admits the peer.
func (a *Agent) admit(ctx context.Context, conn net.Conn, login string) error {
	remote := conn.RemoteAddr().String()
	wctx, cancel := context.WithTimeout(ctx, whoIsTimeout)
	defer cancel()
	resp, err := a.cfg.WhoIs.WhoIs(wctx, remote)
	if err != nil {
		return fmt.Errorf("node: WhoIs of %s: %w", remote, err)
	}
	return Authorize(resp, a.cfg.Controller, login)
}

// dispatch answers req, which an admitted peer sent on conn. Its version is checked first. Attach splices conn onto
// the session's holder, gone streams the holder's spool after its Response, and end answers once the holder has ended.
// Panes serves the agent's HostPanes over HTTP on conn, and watch-panes streams its events, as servePanes and
// watchPanes describe. Every other op is answered with one Response, as answer gives it.
func (a *Agent) dispatch(ctx context.Context, conn net.Conn, reader *bufio.Reader, req Request) {
	if req.Version != Version {
		a.reply(ctx, conn, Response{
			Code:    CodeFailed,
			Message: fmt.Sprintf("node: agent serves %s, not %s", Version, req.Version),
		})
		return
	}
	switch req.Op {
	case OpAttach:
		a.splice(ctx, conn, reader, req.Session)
	case OpGone:
		a.gone(ctx, conn, req)
	case OpEnd:
		a.end(ctx, conn, reader, req.Session)
	case OpPanes:
		a.servePanes(ctx, conn, reader)
	case OpWatchPanes:
		a.watchPanes(ctx, conn, reader)
	default:
		a.reply(ctx, conn, a.answer(ctx, req))
	}
}

// answer returns the one Response to req, for the ops that carry no stream. An op this agent does not serve gets a
// failure that says so.
func (a *Agent) answer(ctx context.Context, req Request) Response {
	switch req.Op {
	case OpPing:
		return Response{}
	case OpSessions:
		return a.sessions(ctx)
	case OpLaunch:
		return a.launch(ctx, req)
	case OpStop, OpKill, OpClear, OpStopOrphan:
		return a.act(ctx, req.Op, req.Session)
	case OpLoad:
		return a.load(ctx)
	case OpFolders:
		return a.folders()
	case OpFolder:
		return a.folder(req)
	case OpWakePane:
		return a.wakePane(ctx, req)
	default:
		return Response{Code: CodeFailed, Message: fmt.Sprintf("node: agent does not serve op %s", req.Op)}
	}
}

// noPanes returns the Response that a pane op gets from an agent with no HostPanes.
func noPanes() Response {
	return Response{Code: CodeFailed, Message: "agent serves no panes"}
}

// load answers with the host's Load, as the agent's HostPanes gives it.
func (a *Agent) load(ctx context.Context) Response {
	if a.cfg.Panes == nil {
		return noPanes()
	}
	load, err := a.cfg.Panes.Load(ctx)
	if err != nil {
		return failed(err)
	}
	return Response{Load: &load}
}

// noRoot returns the Response that a folders or folder op gets from an agent with no Roots.
func noRoot() Response {
	return Response{Code: CodeFailed, Message: "agent has no root"}
}

// folders answers with the directories that rootFolders lists under each of the agent's Roots, sorted by path.
func (a *Agent) folders() Response {
	if len(a.cfg.Roots) == 0 {
		return noRoot()
	}
	var folders []string
	for _, root := range a.cfg.Roots {
		folders = append(folders, rootFolders(root)...)
	}
	slices.Sort(folders)
	return Response{Folders: folders}
}

// folder answers with the resolved path of req.Dir, when checkFolder admits it under any of the agent's Roots. A dir
// that no root admits is answered with CodeFailed and the reason that the first root gave.
func (a *Agent) folder(req Request) Response {
	if len(a.cfg.Roots) == 0 {
		return noRoot()
	}
	var refusal error
	for _, root := range a.cfg.Roots {
		resolved, err := checkFolder(root, req.Dir)
		if err == nil {
			return Response{Folder: resolved}
		}
		if refusal == nil {
			refusal = err
		}
	}
	return Response{Code: CodeFailed, Message: refusal.Error()}
}

// wakePane types req.Text into pane req.Pane of the tmux server req.Server, through the agent's WakePane, and answers
// whether it typed the text. An agent with no WakePane answers CodeFailed, as does a WakePane that fails.
func (a *Agent) wakePane(ctx context.Context, req Request) Response {
	if a.cfg.WakePane == nil {
		return Response{Code: CodeFailed, Message: "agent wakes no panes"}
	}
	woke, err := a.cfg.WakePane(ctx, req.Server, req.Pane, req.Text)
	if err != nil {
		return Response{Code: CodeFailed, Message: err.Error()}
	}
	return Response{Woke: woke}
}

// servePanes answers an OpPanes Request with no Code, then serves conn as HTTP/1.1 over the agent's Panes handler until
// either side closes. The bytes after the Request line, which reader holds, are the start of the controller's HTTP.
func (a *Agent) servePanes(ctx context.Context, conn net.Conn, reader *bufio.Reader) {
	if a.cfg.Panes == nil {
		a.reply(ctx, conn, noPanes())
		return
	}
	if !a.reply(ctx, conn, Response{}) {
		return
	}
	ln := newOneConnListener(bufferedConn{Reader: reader, rawConn: rawConn{Conn: conn}})
	srv := &http.Server{
		Handler:           a.cfg.Panes.Handler(),
		ReadHeaderTimeout: panesHeaderTimeout,
		// The server closes the connection once the controller closes it or a response ends it, so the connection has
		// ended when the server reports it closed, and the listener then accepts nothing more and Serve returns.
		ConnState: func(_ net.Conn, state http.ConnState) {
			if state == http.StateClosed {
				ln.finish()
			}
		},
	}
	if err := srv.Serve(ln); !errors.Is(err, net.ErrClosed) {
		a.cfg.Logger.WarnContext(ctx, "node: serve panes", "remote", conn.RemoteAddr().String(), "err", err)
	}
}

// watchPanes answers an OpWatchPanes Request with no Code, then writes each event of the agent's HostPanes watch as one
// JSON line, until ctx ends or the controller disconnects. The controller sends nothing after its Request, so its
// disconnect ends what reader reads, and that cancels the watch.
func (a *Agent) watchPanes(ctx context.Context, conn net.Conn, reader *bufio.Reader) {
	if a.cfg.Panes == nil {
		a.reply(ctx, conn, noPanes())
		return
	}
	if !a.reply(ctx, conn, Response{}) {
		return
	}
	watchCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		defer cancel()
		if _, copyErr := io.Copy(io.Discard, reader); copyErr != nil {
			a.cfg.Logger.DebugContext(ctx, "node: watch-panes' controller",
				"remote", conn.RemoteAddr().String(), "err", copyErr)
		}
	}()
	enc := json.NewEncoder(conn)
	err := a.cfg.Panes.Watch(watchCtx, func(event PaneEvent) error {
		if encErr := enc.Encode(event); encErr != nil {
			return fmt.Errorf("write a pane event: %w", encErr)
		}
		return nil
	})
	if err != nil {
		a.cfg.Logger.DebugContext(ctx, "node: pane watch ended", "remote", conn.RemoteAddr().String(), "err", err)
	}
}

// bufferedConn is a connection whose reads return first the bytes that its reader holds, and then the connection's own.
// The reader is embedded above the connection, so its Read is the one bufferedConn has, and the connection's other
// methods are promoted from the connection beneath it. A read error is returned as the reader returns it, since
// net/http compares a read's io.EOF with ==.
type bufferedConn struct {
	*bufio.Reader
	rawConn
}

// rawConn is the connection that a bufferedConn reads after its reader's bytes.
type rawConn struct {
	net.Conn
}

// oneConnListener is a net.Listener that accepts its one connection, and then accepts nothing until it is finished. Its
// Serve therefore serves that connection alone.
type oneConnListener struct {
	conn net.Conn
	// taken reports whether Accept has returned conn. Only Serve calls Accept, so it needs no lock.
	taken bool
	// ended closes when the listener is finished, which ends each Accept that waits.
	ended    chan struct{}
	endsOnce sync.Once
}

// newOneConnListener returns a oneConnListener of conn.
func newOneConnListener(conn net.Conn) *oneConnListener {
	return &oneConnListener{conn: conn, ended: make(chan struct{})}
}

// Accept returns the listener's connection the first time, and net.ErrClosed once the listener is finished.
func (l *oneConnListener) Accept() (net.Conn, error) {
	if !l.taken {
		l.taken = true
		return l.conn, nil
	}
	<-l.ended
	return nil, net.ErrClosed
}

// Close finishes the listener, which Serve also does when it returns.
func (l *oneConnListener) Close() error {
	l.finish()
	return nil
}

// Addr returns the address of the listener's connection.
func (l *oneConnListener) Addr() net.Addr {
	return l.conn.LocalAddr()
}

// finish ends the listener, so that its waiting Accept returns net.ErrClosed. A second finish does nothing.
func (l *oneConnListener) finish() {
	l.endsOnce.Do(func() { close(l.ended) })
}

// sessions answers with the ids of the sessions that have a holder directory on this machine.
func (a *Agent) sessions(ctx context.Context) Response {
	ids, err := a.cfg.Holders.Sessions(ctx)
	if err != nil {
		return failed(err)
	}
	return Response{Sessions: ids}
}

// launch starts the session that req's Launch describes under a holder on this machine, and then detaches the launch's
// own process and keeps the holder attached. A step, or a launch that names claude's agent, agents or schema options, is
// refused, since steps stay on the controller. So is a session whose holder is live, since a launch clears the holder it
// finds.
func (a *Agent) launch(ctx context.Context, req Request) Response {
	spec := req.Launch
	switch {
	case spec == nil:
		return Response{Code: CodeFailed, Message: "node: launch names no spec"}
	case spec.Step || spec.Agent != "" || spec.Agents != "" || spec.Schema != "":
		return Response{Code: CodeFailed, Message: fmt.Sprintf(
			"node: launch of %s is a step or names claude options, which stay on the controller", req.Session)}
	}
	dir, err := a.holderPath(req.Session)
	if err != nil {
		return failed(err)
	}
	live, err := holder.Live(dir)
	if err != nil {
		return failed(err)
	}
	if live {
		return Response{
			Code:    CodeFailed,
			Message: fmt.Sprintf("node: session %s already has a live holder", req.Session),
		}
	}
	sess, err := a.cfg.Holders.Launch(ctx, holderhost.Spec{
		Session: req.Session,
		Dir:     spec.Dir,
		Resume:  spec.Resume,
		Model:   spec.Model,
	})
	if err != nil {
		return failed(err)
	}
	if err = sess.Detach(); err != nil {
		a.cfg.Logger.WarnContext(ctx, "node: detach a launched session", "session", req.Session, "err", err)
	}
	a.keep(ctx, req.Session)
	return Response{}
}

// act runs the holder op that stops, kills, clears or stops the orphan of session id, as Holders does it. The session must
// have a holder directory on this machine.
func (a *Agent) act(ctx context.Context, op Op, id string) Response {
	if _, err := a.holderOf(id); err != nil {
		return failed(err)
	}
	var err error
	switch op {
	case OpStop:
		err = a.cfg.Holders.Stop(ctx, id)
	case OpKill:
		err = a.cfg.Holders.Kill(ctx, id)
	case OpClear:
		err = a.cfg.Holders.Clear(ctx, id)
	case OpStopOrphan:
		err = a.cfg.Holders.StopOrphan(ctx, id)
	default:
		return Response{Code: CodeFailed, Message: fmt.Sprintf("node: op %s does not act on a session", op)}
	}
	if err != nil {
		return failed(err)
	}
	return Response{}
}

// gone answers with the exit record of session req.Session's holder, when it wrote one. It then streams the holder's spool
// from req.Offset to its end, and the connection closes after the last byte.
func (a *Agent) gone(ctx context.Context, conn net.Conn, req Request) {
	if _, err := a.holderOf(req.Session); err != nil {
		a.reply(ctx, conn, failed(err))
		return
	}
	gone, err := a.cfg.Holders.OpenGone(ctx, req.Session, req.Offset)
	if err != nil {
		a.reply(ctx, conn, failed(err))
		return
	}
	resp := Response{}
	if exit, found := gone.Exit(); found {
		resp.Exit = &exit
	}
	if !a.reply(ctx, conn, resp) {
		return
	}
	if _, err = io.Copy(conn, gone.Stdout()); err != nil {
		a.cfg.Logger.WarnContext(ctx, "node: stream a gone holder", "session", req.Session, "err", err)
	}
}

// end waits until session id's holder has ended, and then answers with its exit record. The wait ends early when the
// controller disconnects, since a controller sends nothing after its Request.
func (a *Agent) end(ctx context.Context, conn net.Conn, reader *bufio.Reader, id string) {
	dir, err := a.holderOf(id)
	if err != nil {
		a.reply(ctx, conn, failed(err))
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		defer cancel()
		if _, copyErr := io.Copy(io.Discard, reader); copyErr != nil {
			a.cfg.Logger.DebugContext(ctx, "node: end's controller", "session", id, "err", copyErr)
		}
	}()
	exit, found, err := holder.Ended(ctx, dir)
	if err != nil {
		a.reply(ctx, conn, failed(err))
		return
	}
	resp := Response{}
	if found {
		resp.Exit = &exit
	}
	a.reply(ctx, conn, resp)
}

// splice takes session id's holder for the controller on conn. It dials the holder's socket as this user, which the
// holder's peer check admits, answers the Request with no Code, and splices conn onto the socket byte for byte until
// either side ends. The splice is open for its whole life, so no keeper attaches meanwhile, and the holder is kept
// attached once it ends.
func (a *Agent) splice(ctx context.Context, conn net.Conn, reader *bufio.Reader, id string) {
	dir, err := a.holderOf(id)
	if err != nil {
		a.reply(ctx, conn, failed(err))
		return
	}
	a.beginSplice(id)
	defer a.endSplice(ctx, id)
	holderConn, err := a.dialHolder(ctx, dir)
	if err != nil {
		a.reply(ctx, conn, failed(err))
		return
	}
	defer a.closeConn(ctx, holderConn)
	if !a.reply(ctx, conn, Response{}) {
		return
	}
	a.pump(ctx, conn, reader, holderConn)
}

// dialHolder dials the socket of the holder in dir. A dial that fails is classed by the holder's lock, as
// holder.AttachError classes it.
func (a *Agent) dialHolder(ctx context.Context, dir string) (net.Conn, error) {
	sock, err := holder.SocketPath(dir)
	if err != nil {
		return nil, fmt.Errorf("node: socket of %s: %w", dir, err)
	}
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "unix", sock)
	if err != nil {
		return nil, fmt.Errorf("node: dial the holder of %s: %w", dir, holder.AttachError(dir, err))
	}
	return conn, nil
}

// pump copies bytes both ways between conn and holderConn until one side ends, and then closes both. reader holds the
// controller's bytes that came after its Request line, and they go to the holder first.
func (a *Agent) pump(ctx context.Context, conn net.Conn, reader io.Reader, holderConn net.Conn) {
	ended := make(chan error, 2)
	go func() {
		_, err := io.Copy(holderConn, reader)
		ended <- err
	}()
	go func() {
		_, err := io.Copy(conn, holderConn)
		ended <- err
	}()
	first := <-ended
	a.closeConn(ctx, conn)
	a.closeConn(ctx, holderConn)
	second := <-ended
	if err := errors.Join(first, second); err != nil {
		a.cfg.Logger.DebugContext(ctx, "node: splice ended", "remote", conn.RemoteAddr().String(), "err", err)
	}
}

// reply writes resp to conn as the one line that answers a Request, and reports whether it did.
func (a *Agent) reply(ctx context.Context, conn net.Conn, resp Response) bool {
	if err := json.NewEncoder(conn).Encode(resp); err != nil {
		a.cfg.Logger.WarnContext(ctx, "node: write a response", "remote", conn.RemoteAddr().String(), "err", err)
		return false
	}
	return true
}

// failed returns the Response that reports err. An error the holder package classes as ErrGone or ErrUnattachable keeps
// that class as its Code, and any other error is CodeFailed.
func failed(err error) Response {
	code := CodeFailed
	switch {
	case errors.Is(err, holder.ErrGone):
		code = CodeGone
	case errors.Is(err, holder.ErrUnattachable):
		code = CodeUnattachable
	}
	return Response{Code: code, Message: err.Error()}
}

// holderPath returns the holder directory of session id under the state directory. It fails when the agent names no state
// directory, or when id is not a session id.
func (a *Agent) holderPath(id string) (string, error) {
	if a.cfg.StateDir == "" {
		return "", errors.New("node: agent names no state directory")
	}
	dir, err := holder.SessionDir(filepath.Join(a.cfg.StateDir, holdersName), id)
	if err != nil {
		return "", fmt.Errorf("node: holder of session %s: %w", id, err)
	}
	return dir, nil
}

// holderOf returns the holder directory of session id, which must exist. A session with no holder directory is unknown to
// this agent, so the error for it wraps holder.ErrGone.
func (a *Agent) holderOf(id string) (string, error) {
	dir, err := a.holderPath(id)
	if err != nil {
		return "", err
	}
	if _, statErr := os.Lstat(dir); statErr != nil {
		if errors.Is(statErr, fs.ErrNotExist) {
			return "", fmt.Errorf("node: session %s is unknown: %w", id, holder.ErrGone)
		}
		return "", fmt.Errorf("node: inspect the holder of session %s: %w", id, statErr)
	}
	return dir, nil
}

// closeConn closes conn. A conn that is already closed is not an error.
func (a *Agent) closeConn(ctx context.Context, conn net.Conn) {
	if err := conn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		a.cfg.Logger.WarnContext(ctx, "node: close a connection", "remote", conn.RemoteAddr().String(), "err", err)
	}
}
