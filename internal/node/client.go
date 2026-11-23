package node

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"syscall"
	"time"

	"github.com/joshsymonds/orrery/internal/engine/session"
	"github.com/joshsymonds/orrery/internal/holder"
	"github.com/joshsymonds/orrery/internal/holderhost"
)

// DefaultBound is how long a Client waits for an agent's answer to one op, when its ClientConfig sets no Bound.
const DefaultBound = 15 * time.Second

// retriesPerBound is how many dials Bound holds: a refused dial is retried each Bound/retriesPerBound until Bound passes.
const retriesPerBound = 5

// pingsPerBound is how many pings Bound holds: Reach pings each Bound/pingsPerBound on the clock.
const pingsPerBound = 3

// ClientConfig configures a Client: the remote host it reaches, the dialer that reaches its agent over the tailnet, how
// long an op waits for the agent, the clock its waits run on, and its logger.
type ClientConfig struct {
	// Name is the host's name, as hosts.toml gives it.
	Name string
	// Address is the host's agent, as host:port, as hosts.toml gives it.
	Address string
	// Dial connects to the agent's address over the tailnet, as net.Dialer.DialContext does.
	Dial func(ctx context.Context, network, address string) (net.Conn, error)
	// Bound is how long an op waits for the agent's answer. Zero takes DefaultBound.
	Bound time.Duration
	// Clock times the waits of the ops. Nil takes holder.SystemClock{}.
	Clock holder.Clock
	// Logger receives the client's diagnostics. Nil discards them.
	Logger *slog.Logger
}

// Client is the holderhost.Host of one remote machine: the holders on the machine whose agent it dials. A Client never
// relaunches a session on another host.
type Client struct {
	cfg ClientConfig
	// pinging starts the pings that Reach reads, on the first call of Reach.
	pinging sync.Once
	// mu guards unreachable and changed.
	mu sync.Mutex
	// unreachable reports whether no ping has been answered within Bound.
	unreachable bool
	// changed closes at each change of unreachable, and is replaced then.
	changed chan struct{}
}

// NewClient returns the Client that cfg describes. A zero Bound becomes DefaultBound, a nil Clock becomes
// holder.SystemClock{}, and a nil Logger discards the diagnostics.
func NewClient(cfg ClientConfig) *Client {
	if cfg.Bound == 0 {
		cfg.Bound = DefaultBound
	}
	if cfg.Clock == nil {
		cfg.Clock = holder.SystemClock{}
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	return &Client{cfg: cfg, changed: make(chan struct{})}
}

var _ holderhost.Host = (*Client)(nil)

// Launch starts the session on the remote machine. The agent starts its holder, and the Client attaches to the holder at
// offset 0, through a link, and returns the session that reads it.
func (c *Client) Launch(ctx context.Context, spec holderhost.Spec) (*session.Session, error) {
	req := Request{Version: Version, Op: OpLaunch, Session: spec.Session, Launch: &LaunchSpec{
		Dir:    spec.Dir,
		Resume: spec.Resume,
		Model:  spec.Model,
		Agent:  spec.Agent,
		Agents: spec.Agents,
		Schema: spec.Schema,
		Step:   spec.Step,
	}}
	if _, err := c.call(ctx, req); err != nil {
		return nil, err
	}
	process, _, err := holder.ReattachLink(ctx, c.link(spec.Session), 0, holder.WireVersion)
	if err != nil {
		return nil, fmt.Errorf("node: attach %s on %s: %w", spec.Session, c.cfg.Name, err)
	}
	sess, err := session.New(process)
	if err != nil {
		return nil, fmt.Errorf("node: start %s on %s: %w", spec.Session, c.cfg.Name, err)
	}
	return sess, nil
}

// Reattach takes over the holder of session id on the remote machine, at replayFrom, through a link to the agent.
func (c *Client) Reattach(
	ctx context.Context, id string, replayFrom int64, version int,
) (*holder.Process, []string, error) {
	process, written, err := holder.ReattachLink(ctx, c.link(id), replayFrom, version)
	if err != nil {
		return nil, nil, fmt.Errorf("node: reattach %s on %s: %w", id, c.cfg.Name, err)
	}
	return process, written, nil
}

// Stop ends the holder of session id on the remote machine, and leaves its directory in place.
func (c *Client) Stop(ctx context.Context, id string) error {
	return c.act(ctx, OpStop, id)
}

// Kill ends the holder of session id on the remote machine without asking it.
func (c *Client) Kill(ctx context.Context, id string) error {
	return c.act(ctx, OpKill, id)
}

// Clear empties the holder directory of session id on the remote machine.
func (c *Client) Clear(ctx context.Context, id string) error {
	return c.act(ctx, OpClear, id)
}

// StopOrphan stops the child that the gone holder of session id recorded on the remote machine.
func (c *Client) StopOrphan(ctx context.Context, id string) error {
	return c.act(ctx, OpStopOrphan, id)
}

// OpenGone reads the child of session id as its gone holder left it, from offset from. The spool is read whole, so the
// returned Gone holds it in memory.
func (c *Client) OpenGone(parent context.Context, id string, from int64) (holderhost.Gone, error) {
	req := Request{Version: Version, Op: OpGone, Session: id, Offset: from}
	ctx, cancel := c.bound(parent)
	defer cancel()
	conn, err := c.dial(ctx)
	if err != nil {
		return nil, c.fail(parent, req, err)
	}
	resp, err := c.exchange(ctx, conn, req)
	if err != nil {
		return nil, c.fail(parent, req, err)
	}
	defer c.closeConn(conn)
	if err = respErr(resp); err != nil {
		return nil, c.opErr(req, err)
	}
	stop := context.AfterFunc(ctx, func() { c.closeConn(conn) })
	spool, err := io.ReadAll(conn)
	stopped := stop()
	if err == nil && !stopped {
		err = fmt.Errorf("the bound ended while the spool streamed: %w", ctx.Err())
	}
	if err != nil {
		return nil, c.fail(parent, req, err)
	}
	return &nodeGone{host: c.cfg.Name, spool: bytes.NewReader(spool), exit: resp.Exit}, nil
}

// Sessions returns the ids that have a holder directory on the remote machine.
func (c *Client) Sessions(ctx context.Context) ([]string, error) {
	resp, err := c.call(ctx, Request{Version: Version, Op: OpSessions})
	if err != nil {
		return nil, err
	}
	return resp.Sessions, nil
}

// Reach reports whether the agent has answered a ping within Bound, and returns a channel that closes at the next change
// of that. The first call starts the pings, which run until the process ends.
func (c *Client) Reach() (bool, <-chan struct{}) {
	c.pinging.Do(func() { go c.pingLoop() })
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.unreachable, c.changed
}

// act sends the op that acts on session id, which answers with no value.
func (c *Client) act(ctx context.Context, op Op, id string) error {
	_, err := c.call(ctx, Request{Version: Version, Op: op, Session: id})
	return err
}

// call sends req, and returns the Response unless the agent's answer carries an error code.
func (c *Client) call(parent context.Context, req Request) (Response, error) {
	resp, err := c.request(parent, req)
	if err != nil {
		return Response{}, err
	}
	if err = respErr(resp); err != nil {
		return Response{}, c.opErr(req, err)
	}
	return resp, nil
}

// request sends req on a new connection, and returns the Response that answers it. Dial, answer and the connection's
// close are all bounded by Bound on the clock.
func (c *Client) request(parent context.Context, req Request) (Response, error) {
	ctx, cancel := c.bound(parent)
	defer cancel()
	conn, err := c.dial(ctx)
	if err != nil {
		return Response{}, c.fail(parent, req, err)
	}
	resp, err := c.exchange(ctx, conn, req)
	if err != nil {
		return Response{}, c.fail(parent, req, err)
	}
	c.closeConn(conn)
	return resp, nil
}

// bound returns a context that ends once Bound has passed on the clock, or once parent ends, and the func that releases
// it.
func (c *Client) bound(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	timer := c.cfg.Clock.After(c.cfg.Bound)
	go func() {
		select {
		case <-timer:
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}

// dial connects to the agent. A refused dial is retried each Bound/retriesPerBound until ctx ends, and any other failure
// ends the dial at once.
func (c *Client) dial(ctx context.Context) (net.Conn, error) {
	for {
		conn, err := c.cfg.Dial(ctx, "tcp", c.cfg.Address)
		if !errors.Is(err, syscall.ECONNREFUSED) {
			return conn, err
		}
		select {
		case <-ctx.Done():
			return nil, err
		case <-c.cfg.Clock.After(c.cfg.Bound / retriesPerBound):
		}
	}
}

// exchange sends req on conn, and reads the Response line that answers it. A conn still unanswered when ctx ends is
// closed, which ends the read. On success conn stays open for the caller, which may read what follows the Response line.
// On failure exchange closes conn.
func (c *Client) exchange(ctx context.Context, conn net.Conn, req Request) (Response, error) {
	stop := context.AfterFunc(ctx, func() { c.closeConn(conn) })
	resp, err := roundTrip(conn, req)
	if stopped := stop(); err != nil || !stopped {
		c.closeConn(conn)
		if err == nil {
			err = fmt.Errorf("the bound ended before the answer: %w", ctx.Err())
		}
		return Response{}, err
	}
	return resp, nil
}

// fail returns the error of a request whose transport failed with cause. A caller that cancelled its ctx gets that
// cancellation back. Any other failure, including an answer that did not come before a deadline, such as Bound or the
// holder's attach deadline on the ctx of a link's dial, matches holderhost.ErrUnreachable.
func (c *Client) fail(parent context.Context, req Request, cause error) error {
	if errors.Is(parent.Err(), context.Canceled) {
		return c.opErr(req, parent.Err())
	}
	return c.opErr(req, fmt.Errorf("%w: %w", holderhost.ErrUnreachable, cause))
}

// opErr names the op and the host in err.
func (c *Client) opErr(req Request, err error) error {
	return fmt.Errorf("node: %s %q on %s: %w", req.Op, req.Session, c.cfg.Name, err)
}

// closeConn closes conn. A conn that is already closed is not an error, and any other failure is logged, since the
// exchange on conn is over.
func (c *Client) closeConn(conn net.Conn) {
	if err := conn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		c.cfg.Logger.Debug("node: close a connection", "host", c.cfg.Name, "err", err)
	}
}

// link returns the holder.Link of session id on the agent.
func (c *Client) link(id string) *nodeLink {
	return &nodeLink{client: c, id: id, done: make(chan struct{})}
}

// roundTrip writes req as the one line a controller sends, and reads the one line that answers it.
func roundTrip(conn net.Conn, req Request) (Response, error) {
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return Response{}, fmt.Errorf("send %s: %w", req.Op, err)
	}
	line, err := readLine(conn)
	if err != nil {
		return Response{}, fmt.Errorf("read the %s response: %w", req.Op, err)
	}
	var resp Response
	if err = json.Unmarshal(line, &resp); err != nil {
		return Response{}, fmt.Errorf("decode the %s response: %w", req.Op, err)
	}
	return resp, nil
}

// readLine reads conn up to and including the LF that ends its line. It reads one byte at a time, so that it consumes
// nothing past the LF: a holder sends no frame until its controller speaks, so after an attach's Response line the
// splice's bytes are the holder's frames, which the caller reads from conn itself.
func readLine(conn net.Conn) ([]byte, error) {
	var line []byte
	var oneByte [1]byte
	for {
		if _, err := io.ReadFull(conn, oneByte[:]); err != nil {
			return nil, fmt.Errorf("read a line: %w", err)
		}
		line = append(line, oneByte[0])
		if oneByte[0] == '\n' {
			return line, nil
		}
	}
}

// pingLoop pings the agent each Bound/pingsPerBound on the clock. It reads the host unreachable once Bound has passed on
// the clock with no ping answered, and reachable again at the next answer.
func (c *Client) pingLoop() {
	answers := make(chan struct{})
	tick := c.cfg.Clock.After(c.cfg.Bound / pingsPerBound)
	expiry := c.cfg.Clock.After(c.cfg.Bound)
	for {
		select {
		case <-tick:
			tick = c.cfg.Clock.After(c.cfg.Bound / pingsPerBound)
			go c.ping(answers)
		case <-answers:
			expiry = c.cfg.Clock.After(c.cfg.Bound)
			c.setUnreachable(false)
		case <-expiry:
			expiry = nil
			c.setUnreachable(true)
		}
	}
}

// ping asks the agent for a ping, and reports the answer on answers. A ping that is not answered reports nothing.
func (c *Client) ping(answers chan<- struct{}) {
	if _, err := c.call(context.Background(), Request{Version: Version, Op: OpPing}); err != nil {
		c.cfg.Logger.Debug("node: ping", "host", c.cfg.Name, "err", err)
		return
	}
	answers <- struct{}{}
}

// setUnreachable records whether the host reads unreachable, and closes the change channel when that changes.
func (c *Client) setUnreachable(unreachable bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.unreachable == unreachable {
		return
	}
	c.unreachable = unreachable
	close(c.changed)
	c.changed = make(chan struct{})
	c.cfg.Logger.Info("node: host reachability changed", "host", c.cfg.Name, "unreachable", unreachable)
}

// nodeLink is the holder.Link of one session on the agent. Dial attaches the session's holder over a new connection, and
// the end of the holder is the agent's answer to an end op.
type nodeLink struct {
	client *Client
	id     string
	// watch starts the end watch, on the first call of Done.
	watch sync.Once
	// done closes once the holder has ended, and exit and found are set before it closes.
	done  chan struct{}
	exit  holder.Exit
	found bool
}

var _ holder.Link = (*nodeLink)(nil)

// Dial opens a connection to the session's holder, and returns it once the agent has spliced it onto the holder. The
// returned connection stays open after ctx ends.
func (l *nodeLink) Dial(parent context.Context) (net.Conn, error) {
	client := l.client
	req := Request{Version: Version, Op: OpAttach, Session: l.id}
	ctx, cancel := client.bound(parent)
	defer cancel()
	conn, err := client.dial(ctx)
	if err != nil {
		return nil, client.fail(parent, req, err)
	}
	resp, err := client.exchange(ctx, conn, req)
	if err != nil {
		return nil, client.fail(parent, req, err)
	}
	if err = respErr(resp); err != nil {
		client.closeConn(conn)
		return nil, client.opErr(req, err)
	}
	return conn, nil
}

// Done returns the channel that closes once the holder has ended. The first call starts the end watch.
func (l *nodeLink) Done() <-chan struct{} {
	l.watch.Do(func() { go l.watchEnd() })
	return l.done
}

// Exit returns the holder's exit record, and whether the holder wrote one. It is meaningful once Done has closed.
func (l *nodeLink) Exit() (holder.Exit, bool) {
	return l.exit, l.found
}

// watchEnd asks the agent's end op until the holder's end is known, and then closes done. The end op answers only once
// the holder has ended, so a request that outlasts Bound is asked again, and so is one the agent could not answer.
func (l *nodeLink) watchEnd() {
	for {
		resp, err := l.client.call(context.Background(), Request{Version: Version, Op: OpEnd, Session: l.id})
		switch {
		case err == nil:
			if resp.Exit != nil {
				l.exit, l.found = *resp.Exit, true
			}
			close(l.done)
			return
		case errors.Is(err, holder.ErrGone):
			close(l.done)
			return
		}
		l.client.cfg.Logger.Debug("node: end watch", "host", l.client.cfg.Name, "session", l.id, "err", err)
		<-l.client.cfg.Clock.After(l.client.cfg.Bound / retriesPerBound)
	}
}

// nodeGone is the holderhost.Gone of a session whose holder has ended on the agent's machine. Its stdout is the spool
// the agent streamed, and its exit record is the one the agent answered with.
type nodeGone struct {
	host  string
	spool *bytes.Reader
	exit  *holder.Exit
}

var _ holderhost.Gone = (*nodeGone)(nil)

// Stdin returns a stdin that takes no writes, since the child has ended.
func (g *nodeGone) Stdin() io.WriteCloser {
	return goneStdin{}
}

// Stdout returns the spool. Every Stdout of one nodeGone shares its read position.
func (g *nodeGone) Stdout() io.ReadCloser {
	return io.NopCloser(g.spool)
}

// Stderr returns the exit record's stderr tail, or "" when the holder wrote no exit record.
func (g *nodeGone) Stderr() string {
	if g.exit == nil {
		return ""
	}
	return g.exit.Stderr
}

// Stop does nothing, since the child has already ended.
func (g *nodeGone) Stop() error {
	return nil
}

// Wait returns nil when the child exited with status 0, "exit status N" when it exited with status N, and "signal: NAME"
// when a signal ended it, as holder.Gone.Wait reports them. Without an exit record it returns an error.
func (g *nodeGone) Wait() error {
	switch {
	case g.exit == nil:
		return fmt.Errorf("node: no exit record for the session on %s", g.host)
	case g.exit.Signal != "":
		return errors.New("signal: " + g.exit.Signal)
	case g.exit.Code != 0:
		return fmt.Errorf("exit status %d", g.exit.Code)
	}
	return nil
}

// Exit returns the exit record, and whether the holder wrote one.
func (g *nodeGone) Exit() (holder.Exit, bool) {
	if g.exit == nil {
		return holder.Exit{}, false
	}
	return *g.exit, true
}

// goneStdin is the stdin of a nodeGone: it takes no writes, since no child reads them.
type goneStdin struct{}

// Write fails, since the session's holder has ended, so no child reads the write.
func (goneStdin) Write([]byte) (int, error) {
	return 0, fmt.Errorf("node: session has ended: %w", holder.ErrGone)
}

// Close does nothing.
func (goneStdin) Close() error {
	return nil
}

// respErr returns the error that resp's code reports, or nil when the op succeeded. Each code wraps the holder package's
// error for it, so a Client returns the errors Local returns.
func respErr(resp Response) error {
	switch resp.Code {
	case "":
		return nil
	case CodeGone:
		return fmt.Errorf("%s: %w", resp.Message, holder.ErrGone)
	case CodeUnattachable:
		return fmt.Errorf("%s: %w", resp.Message, holder.ErrUnattachable)
	case CodeNoAnswer:
		return fmt.Errorf("%s: %w: %w", resp.Message, holder.ErrUnattachable, holder.ErrNoAnswer)
	case CodeFailed:
		return errors.New(resp.Message)
	default:
		return fmt.Errorf("unknown code %q: %s", resp.Code, resp.Message)
	}
}
