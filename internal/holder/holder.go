package holder

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
	"sync"
	"time"
)

// ErrUndrained means the session dir holds an exit file from an earlier holder, so no new holder may start on the
// session until that exit is drained.
var ErrUndrained = errors.New("holder: session has an undrained exit file")

// socketMode is the mode of the holder's socket: only the holder's user may connect.
const socketMode os.FileMode = 0o600

// maxWriteIDBytes is the longest write id the holder accepts, in bytes. A write id is 1 to maxWriteIDBytes bytes.
const maxWriteIDBytes = 256

// DefaultIdleTimeout is how long a holder may run with no controller attached before it stops its child.
const DefaultIdleTimeout = 10 * time.Minute

// Config describes one holder: the session it serves and the child it runs.
type Config struct {
	// Dir is the session's holder directory, from SessionDir.
	Dir string
	// Session names the session. The holder's hello answer carries it.
	Session string
	// Child describes the child. Its Clock, when nil, takes Clock.
	Child ChildConfig
	// Spool sizes the child's stdout spool.
	Spool SpoolConfig
	// Clock supplies the holder's timers. Nil takes SystemClock{}.
	Clock Clock
	// IdleTimeout is how long the holder runs with no controller attached before it stops the child. Zero takes
	// DefaultIdleTimeout.
	IdleTimeout time.Duration
	// Logger receives the holder's diagnostics. Nil discards them.
	Logger *slog.Logger

	// version is the WireVersion the holder answers with. Zero takes WireVersion.
	version int
	// peerUID returns the uid of a connection's peer. Nil takes PeerUID.
	peerUID func(*net.UnixConn) (int, error)
}

// withDefaults returns cfg with each zero field set to its default.
func (cfg Config) withDefaults() Config {
	if cfg.Clock == nil {
		cfg.Clock = SystemClock{}
	}
	if cfg.IdleTimeout == 0 {
		cfg.IdleTimeout = DefaultIdleTimeout
	}
	if cfg.Child.Clock == nil {
		cfg.Child.Clock = cfg.Clock
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	if cfg.version == 0 {
		cfg.version = WireVersion
	}
	if cfg.peerUID == nil {
		cfg.peerUID = PeerUID
	}
	return cfg
}

// Run serves the session in cfg.Dir until its child ends. It takes the session's lock, refuses to start while an exit
// file from an earlier holder is undrained, and serves the socket. A stop from a controller, the end of ctx, or the
// idle timeout stops the child: the idle timeout runs while no controller is attached. When the child ends, Run writes
// the exit file, ends the attached controller's stream with the rest of the output and the exit record, answers the
// stops and releases what it took.
func Run(ctx context.Context, cfg Config) error {
	cfg = cfg.withDefaults()
	if err := PrepareDir(cfg.Dir); err != nil {
		return fmt.Errorf("holder: %w", err)
	}
	lock, err := Lock(filepath.Join(cfg.Dir, LockName))
	if err != nil {
		return fmt.Errorf("session %s: %w", cfg.Session, err)
	}
	h, err := openHolder(cfg)
	if err != nil {
		return errors.Join(err, lock.Close())
	}
	return errors.Join(h.run(ctx), lock.Close())
}

// openHolder takes what the holder serves with: the socket, the spool and the child. A failure releases what it took
// first. A child that cannot start is recorded in the exit file, so the spool it leaves has one.
func openHolder(cfg Config) (*holder, error) {
	exitPath := filepath.Join(cfg.Dir, ExitName)
	_, statErr := os.Lstat(exitPath)
	if statErr == nil {
		return nil, fmt.Errorf("%w: %s", ErrUndrained, exitPath)
	}
	if !errors.Is(statErr, fs.ErrNotExist) {
		return nil, fmt.Errorf("holder: inspect exit file: %w", statErr)
	}
	sockPath, err := SocketPath(cfg.Dir)
	if err != nil {
		return nil, err
	}
	// The lock is held, so a socket file already here is left over from an earlier holder.
	if err = os.Remove(sockPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("holder: remove stale socket: %w", err)
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: sockPath, Net: "unix"})
	if err != nil {
		return nil, fmt.Errorf("holder: listen on %s: %w", sockPath, err)
	}
	if err = os.Chmod(sockPath, socketMode); err != nil {
		return nil, errors.Join(fmt.Errorf("holder: set socket mode: %w", err), listener.Close())
	}
	spool, err := NewSpool(cfg.Dir, cfg.Spool)
	if err != nil {
		return nil, errors.Join(err, listener.Close())
	}
	child, err := StartChild(cfg.Child, spool)
	if err != nil {
		exitErr := WriteExit(exitPath, Exit{Reason: ReasonExited, Code: -1, Stderr: err.Error()})
		return nil, errors.Join(err, exitErr, spool.Close(), listener.Close())
	}
	return &holder{
		cfg:          cfg,
		exitPath:     exitPath,
		listener:     listener,
		spool:        spool,
		child:        child,
		ended:        make(chan struct{}),
		conns:        make(map[*conn]struct{}),
		stoppers:     make(map[*conn]struct{}),
		writeIDs:     make(map[string]struct{}),
		queued:       make(chan struct{}, 1),
		writesClosed: make(chan struct{}),
	}, nil
}

// holder is one running holder: the socket it serves, the spool and child it runs, and its connections.
type holder struct {
	cfg      Config
	exitPath string
	listener *net.UnixListener
	spool    *Spool
	child    *Child

	// serving counts the accept loop and every connection goroutine.
	serving sync.WaitGroup

	mu sync.Mutex
	// conns holds every open connection.
	conns map[*conn]struct{}
	// stoppers holds the connections that sent stop. Each is answered with stopped once the exit file is written.
	stoppers map[*conn]struct{}
	// exited is set once the exit file is written. A stop that arrives after that is answered at once.
	exited bool
	// endReason is why the child is stopped: ReasonStopped for a stop from a controller or the end of the context,
	// ReasonIdle for the idle timeout. The first one set holds. It is empty while the child is not stopped by the holder.
	endReason string
	// idleCancel cancels the armed idle timeout. It is nil while none is armed, which is while a controller is attached.
	idleCancel chan struct{}
	// ctl is the attached controller, or nil when none is attached.
	ctl *controller
	// ended closes once the child has ended and exit holds its record. Each stream then sends the rest of the output and
	// the exit frame.
	ended chan struct{}
	exit  Exit
	// streaming counts the streams to attached controllers, so the end path waits for each to finish.
	streaming sync.WaitGroup

	// written holds the ids of the writes recorded, in the order received, and writeIDs holds the same ids for lookup.
	// They live as long as the holder.
	written  []string
	writeIDs map[string]struct{}
	// queue holds the writes recorded and not yet written to the child, in the order received. queued signals the
	// writer that queue grew. writesClosed closes once no connection can record another write.
	queue        []stdinWrite
	queued       chan struct{}
	writesClosed chan struct{}
}

// controller is the connection attached to the child's stdout stream. quit closes when the controller is replaced or
// drops, which ends its stream.
type controller struct {
	conn *conn
	quit chan struct{}
}

// stdinWrite is one write recorded for the child's stdin. Its answer goes to peer.
type stdinWrite struct {
	id   string
	data []byte
	peer *conn
}

// run serves connections until the child ends. Then it writes the exit file, ends each stream with the rest of the
// output and the exit record, answers the stops, closes the connections, drains the writes and closes the spool, and
// returns what those steps report.
func (h *holder) run(ctx context.Context) error {
	h.mu.Lock()
	h.armIdleLocked()
	h.mu.Unlock()
	accepting := make(chan struct{})
	go func() {
		defer close(accepting)
		h.accept()
	}()
	writing := make(chan struct{})
	go func() {
		defer close(writing)
		h.writeStdin()
	}()

	select {
	case <-h.child.Done():
	case <-ctx.Done():
		h.requestStop()
	}
	<-h.child.Done()

	exit := h.child.Exit(h.exitReason())
	writeErr := WriteExit(h.exitPath, exit)
	// Close stops the accept loop and removes the socket file.
	closeErr := h.listener.Close()
	<-accepting
	h.endStreams(exit)
	if writeErr == nil {
		h.answerStops()
	}
	connErr := h.closeConns()
	h.serving.Wait()
	close(h.writesClosed)
	<-writing
	return errors.Join(writeErr, closeErr, connErr, h.spool.Close())
}

// accept serves each connection the listener takes, until the listener closes.
func (h *holder) accept() {
	for {
		unixConn, err := h.listener.AcceptUnix()
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				h.cfg.Logger.Error("holder: accept", "err", err)
			}
			return
		}
		peer := &conn{unix: unixConn}
		h.mu.Lock()
		h.conns[peer] = struct{}{}
		h.mu.Unlock()
		h.serving.Add(1)
		go h.serve(peer)
	}
}

// serve runs one connection. A peer of another user is closed with no frame. The first frame must be hello, which is
// answered; any other first frame closes the connection. After hello, stop stops the child, attach makes the
// connection the controller, ack trims the spool for the controller, and write records a stdin write from the
// controller. A refused attach, ack or write, and any other frame, gets an error frame and closes the connection.
func (h *holder) serve(peer *conn) {
	defer h.serving.Done()
	defer h.drop(peer)
	if !h.admit(peer) {
		return
	}
	frames := NewFrameReader(peer.unix)
	if !h.greet(peer, frames) {
		return
	}
	for {
		frame, err := frames.Read()
		if err != nil {
			return
		}
		switch frame.Type {
		case TypeStop:
			h.stop(peer)
		case TypeAttach:
			if !h.attach(peer, frame.ReplayFrom) {
				return
			}
		case TypeAck:
			if !h.ack(peer, frame) {
				return
			}
		case TypeWrite:
			if !h.write(peer, frame) {
				return
			}
		default:
			h.reject(peer, frame.Type)
			return
		}
	}
}

// admit reports whether the peer is this holder's own user. A peer whose uid cannot be read is refused too.
func (h *holder) admit(peer *conn) bool {
	uid, err := h.cfg.peerUID(peer.unix)
	if err != nil {
		h.cfg.Logger.Warn("holder: read peer uid", "err", err)
		return false
	}
	if uid != os.Getuid() {
		h.cfg.Logger.Warn("holder: refused a peer of another user", "uid", uid)
		return false
	}
	return true
}

// greet reads the first frame, which must be hello, and answers it. It reports whether the connection goes on.
func (h *holder) greet(peer *conn, frames *FrameReader) bool {
	first, err := frames.Read()
	if err != nil || first.Type != TypeHello {
		return false
	}
	if err = peer.write(Frame{Type: TypeHello, Version: h.cfg.version, Session: h.cfg.Session}); err != nil {
		h.cfg.Logger.Warn("holder: answer hello", "err", err)
		return false
	}
	return true
}

// reject answers a frame the holder does not serve with an error frame. The caller then closes the connection.
func (h *holder) reject(peer *conn, frameType string) {
	h.refuse(peer, fmt.Sprintf("holder: unexpected %q frame", frameType))
}

// refuse answers peer with an error frame carrying message. The caller then closes the connection.
func (h *holder) refuse(peer *conn, message string) {
	if err := peer.write(Frame{Type: TypeError, Message: message}); err != nil {
		h.cfg.Logger.Warn("holder: answer with error", "err", err)
	}
}

// stop handles a stop frame from peer. Before the exit file is written, peer is recorded to be answered with stopped,
// and the child is stopped. After it, peer is answered at once.
func (h *holder) stop(peer *conn) {
	h.mu.Lock()
	exited := h.exited
	if !exited {
		h.stoppers[peer] = struct{}{}
	}
	h.mu.Unlock()
	if exited {
		h.answerStopped(peer)
		return
	}
	h.requestStop()
}

// requestStop records a stop as the child's end reason, unless an earlier reason holds, and stops the child. Child.Stop
// is idempotent.
func (h *holder) requestStop() {
	h.mu.Lock()
	if h.endReason == "" {
		h.endReason = ReasonStopped
	}
	h.mu.Unlock()
	h.child.Stop()
}

// exitReason returns the reason the child ended: the end reason the holder set, or ReasonExited when it set none.
func (h *holder) exitReason() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.endReason == "" {
		return ReasonExited
	}
	return h.endReason
}

// armIdleLocked starts the idle timeout. Unless disarmIdleLocked cancels it first, it stops the child when it runs out.
// Nothing is armed once the child has ended. The caller holds h.mu.
func (h *holder) armIdleLocked() {
	select {
	case <-h.ended:
		return
	default:
	}
	cancel := make(chan struct{})
	h.idleCancel = cancel
	timer := h.cfg.Clock.After(h.cfg.IdleTimeout)
	go h.awaitIdle(timer, cancel)
}

// disarmIdleLocked cancels the armed idle timeout, if one is armed. The caller holds h.mu.
func (h *holder) disarmIdleLocked() {
	if h.idleCancel != nil {
		close(h.idleCancel)
		h.idleCancel = nil
	}
}

// awaitIdle waits until the timeout armed with cancel runs out, then calls idleExpired. Closing cancel ends the wait,
// and the timer is then dropped.
func (h *holder) awaitIdle(timer <-chan time.Time, cancel chan struct{}) {
	select {
	case <-timer:
		h.idleExpired(cancel)
	case <-cancel:
	}
}

// idleExpired stops the child for want of a controller. It does nothing when the timeout armed with cancel was
// disarmed or replaced first. The end reason is ReasonIdle unless a stop came first.
func (h *holder) idleExpired(cancel chan struct{}) {
	h.mu.Lock()
	if h.idleCancel != cancel {
		h.mu.Unlock()
		return
	}
	h.idleCancel = nil
	if h.endReason == "" {
		h.endReason = ReasonIdle
	}
	h.mu.Unlock()
	h.child.Stop()
}

// answerStops marks the exit file written and sends stopped to every connection that sent stop.
func (h *holder) answerStops() {
	h.mu.Lock()
	h.exited = true
	stoppers := slices.Collect(maps.Keys(h.stoppers))
	h.mu.Unlock()
	for _, peer := range stoppers {
		h.answerStopped(peer)
	}
}

// answerStopped sends stopped to peer. A failed write is logged, not returned, because the peer may already have gone.
func (h *holder) answerStopped(peer *conn) {
	if err := peer.write(Frame{Type: TypeStopped}); err != nil {
		h.cfg.Logger.Warn("holder: answer stop", "err", err)
	}
}

// attach makes peer the controller and starts its stream from replayFrom. The new stream replaces the attached
// controller, whose connection closes. The attached frame lists every write id recorded so far. An attach the spool
// cannot serve gets an error frame and closes the connection, and the current controller stays. It reports whether the
// connection goes on.
func (h *holder) attach(peer *conn, replayFrom int64) bool {
	h.mu.Lock()
	if h.ctl != nil && h.ctl.conn == peer {
		h.mu.Unlock()
		h.refuse(peer, "holder: this connection is already attached")
		return false
	}
	select {
	case <-h.ended:
		h.mu.Unlock()
		h.refuse(peer, "holder: the child has ended")
		return false
	default:
	}
	start, end := h.spool.Start(), h.spool.End()
	if replayFrom < start || replayFrom > end {
		h.mu.Unlock()
		h.refuse(peer, fmt.Sprintf("holder: attach at offset %d: retained output is [%d, %d)", replayFrom, start, end))
		return false
	}
	prev := h.ctl
	ctl := &controller{conn: peer, quit: make(chan struct{})}
	h.ctl = ctl
	h.disarmIdleLocked()
	if prev != nil {
		close(prev.quit)
	}
	h.streaming.Add(1)
	attached := Frame{Type: TypeAttached, Start: start, End: end, Written: slices.Clone(h.written)}
	h.mu.Unlock()
	if prev != nil {
		h.drop(prev.conn)
	}
	go h.stream(ctl, attached, replayFrom)
	return true
}

// write handles a write frame from peer. Only the attached controller may write, with an id of 1 to maxWriteIDBytes
// bytes; any other write gets an error frame and closes the connection. An id not yet recorded is recorded at once and
// its data queued for the child's stdin, so a repeat that arrives while it waits is a duplicate too. A recorded id is
// answered as a duplicate and nothing is written. It reports whether the connection goes on.
func (h *holder) write(peer *conn, frame Frame) bool {
	h.mu.Lock()
	if h.ctl == nil || h.ctl.conn != peer {
		h.mu.Unlock()
		h.refuse(peer, "holder: write from a connection that is not attached")
		return false
	}
	if n := len(frame.ID); n == 0 || n > maxWriteIDBytes {
		h.mu.Unlock()
		h.refuse(peer, fmt.Sprintf("holder: write id is %d bytes, want 1 to %d", n, maxWriteIDBytes))
		return false
	}
	_, seen := h.writeIDs[frame.ID]
	if !seen {
		h.writeIDs[frame.ID] = struct{}{}
		h.written = append(h.written, frame.ID)
		h.queue = append(h.queue, stdinWrite{id: frame.ID, data: frame.Data, peer: peer})
	}
	h.mu.Unlock()
	if seen {
		if err := peer.write(Frame{Type: TypeWritten, ID: frame.ID, Duplicate: true}); err != nil {
			h.cfg.Logger.Warn("holder: answer duplicate write", "err", err)
			return false
		}
		return true
	}
	select {
	case h.queued <- struct{}{}:
	default:
	}
	return true
}

// writeStdin writes the recorded writes to the child's stdin in the order they were recorded, and answers each. A
// write the pipe holds up holds up only the writes behind it, not the connections. Once writesClosed closes, it writes
// what is still queued and returns.
func (h *holder) writeStdin() {
	for {
		h.mu.Lock()
		if len(h.queue) > 0 {
			next := h.queue[0]
			h.queue = h.queue[1:]
			h.mu.Unlock()
			h.deliver(next)
			continue
		}
		h.mu.Unlock()
		select {
		case <-h.queued:
		case <-h.writesClosed:
			h.mu.Lock()
			drained := len(h.queue) == 0
			h.mu.Unlock()
			if drained {
				return
			}
		}
	}
}

// deliver writes one recorded write to the child's stdin and answers its peer: written once the data is written, or an
// error frame when the child's stdin refuses it. The connection stays open either way.
func (h *holder) deliver(w stdinWrite) {
	answer := Frame{Type: TypeWritten, ID: w.id}
	if err := h.child.Write(w.data); err != nil {
		answer = Frame{Type: TypeError, ID: w.id, Message: err.Error()}
	}
	if err := w.peer.write(answer); err != nil {
		h.cfg.Logger.Warn("holder: answer write", "err", err)
	}
}

// ack trims the spool to the attached controller's consumed and replay-from offsets. An ack from any other connection,
// or one the spool refuses, gets an error frame and closes the connection. It reports whether the connection goes on.
func (h *holder) ack(peer *conn, frame Frame) bool {
	h.mu.Lock()
	var err error
	if h.ctl == nil || h.ctl.conn != peer {
		err = errors.New("connection is not attached")
	} else {
		err = h.spool.Ack(frame.Consumed, frame.ReplayFrom)
	}
	h.mu.Unlock()
	if err != nil {
		h.refuse(peer, fmt.Sprintf("holder: ack: %v", err))
		return false
	}
	return true
}

// stream serves ctl's stream. A failure gets an error frame and detaches the controller, unless the controller was
// already replaced or dropped, which ends the stream on its own.
func (h *holder) stream(ctl *controller, attached Frame, replayFrom int64) {
	defer h.streaming.Done()
	err := h.streamTo(ctl, attached, replayFrom)
	if err == nil {
		return
	}
	select {
	case <-ctl.quit:
		return
	default:
	}
	h.cfg.Logger.Warn("holder: stream to controller", "err", err)
	h.refuse(ctl.conn, err.Error())
	h.drop(ctl.conn)
}

// streamTo writes attached, then the stdout from replayFrom: from the spool while it holds bytes past the last frame,
// then live as the child writes more. Once the child has ended it writes the rest and the exit frame. It returns nil
// when ctl is replaced or dropped.
func (h *holder) streamTo(ctl *controller, attached Frame, replayFrom int64) error {
	if err := ctl.conn.write(attached); err != nil {
		return err
	}
	buf := make([]byte, ChunkBytes)
	off := replayFrom
	for {
		// Changed is taken before End, so an append between the two is never missed.
		changed := h.spool.Changed()
		end := h.spool.End()
		if off < end {
			count, readErr := h.spool.ReadAt(buf[:min(end-off, ChunkBytes)], off)
			if readErr != nil {
				return readErr
			}
			out := Frame{Type: TypeOut, Offset: off, Data: buf[:count]}
			off += int64(count)
			if writeErr := ctl.conn.write(out); writeErr != nil {
				return writeErr
			}
			continue
		}
		select {
		case <-ctl.quit:
			return nil
		case <-changed:
		case <-h.ended:
			if off == h.spool.End() {
				exit := h.exit
				return ctl.conn.write(Frame{Type: TypeExit, Exit: &exit})
			}
		}
	}
}

// endStreams records the child's exit record and closes ended, so each stream sends the rest of the output and the
// exit frame. It disarms the idle timeout, which must not stop a child that has ended. It returns once every stream has
// finished.
func (h *holder) endStreams(exit Exit) {
	h.mu.Lock()
	h.exit = exit
	close(h.ended)
	h.disarmIdleLocked()
	h.mu.Unlock()
	h.streaming.Wait()
}

// drop forgets peer and closes it. A peer that is the attached controller detaches, which ends its stream and arms the
// idle timeout again.
func (h *holder) drop(peer *conn) {
	h.mu.Lock()
	delete(h.conns, peer)
	if h.ctl != nil && h.ctl.conn == peer {
		close(h.ctl.quit)
		h.ctl = nil
		h.armIdleLocked()
	}
	h.mu.Unlock()
	if err := peer.close(); err != nil {
		h.cfg.Logger.Warn("holder: close connection", "err", err)
	}
}

// closeConns closes every open connection, so each serve goroutine ends.
func (h *holder) closeConns() error {
	h.mu.Lock()
	open := slices.Collect(maps.Keys(h.conns))
	h.mu.Unlock()
	var err error
	for _, peer := range open {
		err = errors.Join(err, peer.close())
	}
	return err
}

// conn is one accepted connection. Writes to it come from more than one goroutine, so each write holds writeMu.
type conn struct {
	unix      *net.UnixConn
	writeMu   sync.Mutex
	closeOnce sync.Once
}

// write sends one frame to the peer.
func (c *conn) write(frame Frame) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return WriteFrame(c.unix, frame)
}

// close closes the connection. Only the first call closes it, so later calls return nil.
func (c *conn) close() error {
	var err error
	c.closeOnce.Do(func() {
		if closeErr := c.unix.Close(); closeErr != nil {
			err = fmt.Errorf("holder: close connection: %w", closeErr)
		}
	})
	return err
}
