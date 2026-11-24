package holder

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// heldProcess is the holder's own OS process as a controller sees it through the holder. Done closes once the process
// has ended. proc.Process satisfies it.
type heldProcess interface {
	Done() <-chan struct{}
	Wait() error
	Stderr() string
	Stop() error
}

// Process is a session's child as a controller sees it through its holder. Its stdout is read only from the holder's
// out frames, and its stdin is written only by write frames, each under a fresh id that the holder answers. A Process
// over a link re-attaches its holder when its connection fails before the holder has ended.
type Process struct {
	held   heldProcess
	socket string
	remove string
	// scope stops the holder's user scope once the holder has ended; the zero stopper stops nothing.
	scope scopeStopper
	// stopTimeout bounds a stop over the socket before Stop falls back to the holder's own process: holderStopTimeout,
	// which a test may shorten before it calls Stop.
	stopTimeout time.Duration

	// link is the link a Process reaches its holder over, nil for a Process on the holder's socket.
	link Link
	// relinkTo attaches the holder again at an offset over link when the connection fails. relinkDone closes once the
	// Process has stopped re-attaching, and cancelRelink ends the re-attaching. All three are nil for a socket Process.
	relinkTo     func(replayFrom int64) (*Conn, Frame, error)
	relinkDone   <-chan struct{}
	cancelRelink context.CancelFunc

	// readMu serializes Reads, so the acks they send reach the holder in the order of their consumed offsets.
	readMu sync.Mutex
	// writeMu serializes the write frames a Process sends, so the writes a re-attach resends reach the holder before any
	// later write does.
	writeMu sync.Mutex

	// streamDone closes once the stream has ended and its connection is closed.
	streamDone chan struct{}
	// closeErr is the error of closing the connection at the stream's end, with the errors of closing lost connections.
	// run sets it before streamDone closes.
	closeErr error
	// ended closes once the holder's process has ended and its directory is handled, and endErr is set before it closes.
	ended  chan struct{}
	endErr error

	stopOnce sync.Once
	stopErr  error

	mu sync.Mutex
	// changed closes, and is replaced, whenever a field below that a waiter checks changes.
	changed chan struct{}
	// conn is the connection the stream runs on. It is nil while a linked Process has none, between a loss and its next
	// attach.
	conn *Conn
	// holderVersion is the wire version the holder speaks, which is the version its hello carried.
	holderVersion int
	// buf holds stdout the holder has sent and Read has not returned. next is the offset the next out frame starts at.
	buf  []byte
	next int64
	// returned is the stdout offset just past the bytes Read has returned. It starts at the offset the stream starts at.
	returned int64
	// replayFrom is the offset SetReplayFrom last allowed, 0 until it is called.
	replayFrom int64
	// attachedConsumed and attachedReplayFrom are the consumed and replay-from offsets the holder's attached frame of the
	// current connection carried. The holder never moves them back, so an ack carries no lower offsets.
	attachedConsumed   int64
	attachedReplayFrom int64
	// streamEnd is the error that ended the stream, io.EOF for a clean end. It is nil while the stream runs.
	streamEnd error
	// exit is the exit record, once the exit frame has arrived.
	exit *Exit
	// pending holds, by write id, each write waiting on the holder's answer.
	pending map[string]*pendingWrite
	// nextSeq numbers the writes in the order they are registered.
	nextSeq uint64
	// relinkErr is the error the last attempt to re-attach failed with, and nil while none has failed since the loss.
	relinkErr error
	// lostErr holds the errors of closing lost connections, which the stream's end reports.
	lostErr      error
	stdinClosed  bool
	stdoutClosed bool
	detached     bool
	// stopping is set once Stop has sent stop, so a connection that attaches after it sends stop again.
	stopping bool
}

// pendingWrite is a stdin write waiting on the holder's answer. seq orders the writes a re-attach resends. sent reports
// whether the write has gone out on a connection since the last attach, and answer receives the outcome.
type pendingWrite struct {
	id     string
	data   []byte
	seq    uint64
	sent   bool
	answer chan error
}

// newProcess returns the Process for held, the holder's own process. conn is attached at offset 0 to the holder that
// socket names, on a holder that has not yet spooled or consumed anything. remove is the directory removed once the
// holder has ended, or empty to keep it.
func newProcess(held heldProcess, conn *Conn, socket, remove string) *Process {
	return newAttachedProcess(context.Background(), held, conn, socket, remove, 0, Frame{}, scopeStopper{})
}

// newAttachedProcess returns the Process for held, the holder's own process, whose conn is attached at replayFrom. The
// stream's out frames start at replayFrom. attached is the attached frame the holder answered: acks carry no consumed or
// replay-from below its offsets. remove is the directory removed once the holder has ended, or empty to keep it. scope
// stops the holder's user scope once the holder has ended, under ctx, which must outlast the holder.
func newAttachedProcess(
	ctx context.Context,
	held heldProcess,
	conn *Conn,
	socket, remove string,
	replayFrom int64,
	attached Frame,
	scope scopeStopper,
) *Process {
	process := newStreamProcess(conn, replayFrom, attached)
	process.held = held
	process.socket = socket
	process.remove = remove
	process.scope = scope
	process.start(ctx, conn)
	return process
}

// newStreamProcess returns a Process whose stream runs on conn, attached at replayFrom, with no holder set yet.
func newStreamProcess(conn *Conn, replayFrom int64, attached Frame) *Process {
	return &Process{
		stopTimeout:        holderStopTimeout,
		conn:               conn,
		holderVersion:      conn.Version(),
		returned:           replayFrom,
		next:               replayFrom,
		attachedConsumed:   attached.Consumed,
		attachedReplayFrom: attached.ReplayFrom,
		streamDone:         make(chan struct{}),
		ended:              make(chan struct{}),
		changed:            make(chan struct{}),
		pending:            make(map[string]*pendingWrite),
	}
}

// SetReplayFrom allows the holder to trim its spool below off. It never lowers the offset, so a call with a smaller off
// changes nothing. Each ack carries the offset no higher than the bytes consumed.
func (p *Process) SetReplayFrom(off int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.replayFrom = max(p.replayFrom, off)
}

// Stdin returns the write end of the child's stdin.
func (p *Process) Stdin() io.WriteCloser {
	return processStdin{p: p}
}

// Stdout returns the read end of the child's stdout.
func (p *Process) Stdout() io.ReadCloser {
	return processStdout{p: p}
}

// Version returns the wire version the holder speaks, which is the version its hello carried.
func (p *Process) Version() int {
	return p.holderVersion
}

// Exit returns the exit record once the exit frame has arrived, and false before.
func (p *Process) Exit() (Exit, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.exit == nil {
		return Exit{}, false
	}
	return *p.exit, true
}

// Stderr returns the child's stderr tail from the exit record once the exit frame has arrived. Before that it returns
// the holder's own stderr.
func (p *Process) Stderr() string {
	p.mu.Lock()
	exit := p.exit
	p.mu.Unlock()
	if exit != nil {
		return exit.Stderr
	}
	return p.held.Stderr()
}

// Stop stops the child and returns once the holder has ended and its directory is removed. It is idempotent, so later
// calls return the first result. After Detach it returns nil at once, and after the exit frame it stops nothing and
// only waits, since the child has ended. It returns an error only when neither the holder's socket nor the holder's own
// process could be stopped.
func (p *Process) Stop() error {
	p.stopOnce.Do(func() {
		p.stopErr = p.stop()
	})
	return p.stopErr
}

// stop does the work of Stop.
func (p *Process) stop() error {
	if p.isDetached() {
		return nil
	}
	var stopErr error
	select {
	case <-p.held.Done():
	default:
		// After the exit frame the child has ended and the holder is ending on its own; a stop sent then races its
		// exit, and the fallback signal could end a holder that has already given up its signal handling.
		if !p.exitReceived() {
			stopErr = p.stopHolder()
		}
	}
	<-p.ended
	return stopErr
}

// exitReceived reports whether the holder's exit frame has arrived.
func (p *Process) exitReceived() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.exit != nil
}

// stopHolder asks the holder to stop its child: over its socket for a socket Process, and over the connection the stream
// runs on for a linked one. When that fails, it stops the holder's own process, which for a linked Process is the link's
// stop. It returns an error only when that fails too.
func (p *Process) stopHolder() error {
	var sendErr error
	if p.link == nil {
		sendErr = stopOverSocket(p.socket, p.stopTimeout)
	} else {
		sendErr = p.sendStop()
	}
	if sendErr == nil {
		return nil
	}
	if heldErr := p.held.Stop(); heldErr != nil {
		return errors.Join(sendErr, heldErr)
	}
	return nil
}

// stopOverSocket dials the holder on socket and sends it stop, which returns once the child has stopped. The dial, the
// hello and the wait for stopped share one deadline timeout away, so a holder that accepts its socket and never answers
// fails the stop, and stopHolder then falls back to the holder's own process.
func stopOverSocket(socket string, timeout time.Duration) error {
	return stopBounded(context.Background(), socket, time.Now().Add(timeout))
}

// Wait returns once the holder has ended and its directory is removed. It returns "signal: NAME" when a signal ended the
// child, as exec reports a signaled process, and the holder's own wait error otherwise. A removal error is joined to
// either.
func (p *Process) Wait() error {
	<-p.ended
	p.mu.Lock()
	exit := p.exit
	p.mu.Unlock()
	if exit != nil && exit.Signal != "" {
		return errors.Join(errors.New("signal: "+exit.Signal), p.endErr)
	}
	return errors.Join(p.held.Wait(), p.endErr)
}

// Detach closes the connection without stopping the holder. The holder, its child and its directory stay in place, and
// Reads end. A linked Process stops re-attaching too. Detach is idempotent.
func (p *Process) Detach() error {
	p.mu.Lock()
	if p.detached {
		p.mu.Unlock()
		return nil
	}
	p.detached = true
	conn := p.conn
	p.mu.Unlock()
	if p.cancelRelink != nil {
		p.cancelRelink()
	}
	if conn == nil {
		return nil
	}
	return conn.Close()
}

// start runs the stream on conn, the connection the Process was attached on, and waits for the holder's end, under ctx,
// which must outlast the holder. conn is passed in, since a send may lose the Process's current connection before the
// stream starts.
func (p *Process) start(ctx context.Context, conn *Conn) {
	go p.run(conn)
	go p.awaitHolder(ctx)
}

// run serves the stream on conn until it ends, then records the end, closes the connection and closes streamDone. Nothing
// is sent once the stream has ended, so the connection is not needed after it.
func (p *Process) run(conn *Conn) {
	p.finish(p.serve(conn))
	p.mu.Lock()
	lostErr := p.lostErr
	p.mu.Unlock()
	p.closeErr = errors.Join(lostErr, p.closeCurrent())
	if p.cancelRelink != nil {
		p.cancelRelink()
	}
	close(p.streamDone)
}

// serve receives the stream on conn and returns the error that ends it. A linked Process whose connection fails before
// the holder's end is known re-attaches over its link, and receives on the new connection. Any other failure ends the
// stream, as a socket Process's does.
func (p *Process) serve(conn *Conn) error {
	for {
		lost, err := p.receive(conn)
		if !lost || p.link == nil {
			return err
		}
		p.lose(conn)
		next, ok := p.relink()
		if !ok {
			return io.EOF
		}
		conn = next
	}
}

// awaitHolder waits for the holder's process to end and the stream to finish, so the exit record is read first. Unless
// the process is detached, it then stops the holder's scope, so nothing the child left running, such as a daemon it
// forked, outlives the holder, and removes the holder's directory unless the process keeps it. It closes ended last.
// endErr carries the connection's close error, the scope stop's and the removal's.
func (p *Process) awaitHolder(ctx context.Context) {
	<-p.held.Done()
	<-p.streamDone
	p.endErr = p.closeErr
	if !p.isDetached() {
		p.endErr = errors.Join(p.endErr, p.scope.stop(ctx))
	}
	if !p.isDetached() && p.remove != "" {
		if err := os.RemoveAll(p.remove); err != nil {
			p.endErr = errors.Join(p.endErr, fmt.Errorf("holder: remove %s: %w", p.remove, err))
		}
	}
	close(p.ended)
}

// receive reads conn's frames until the stream ends, and returns the error that ends it. lost reports whether conn itself
// failed, as opposed to the stream's own end or a frame the stream may not carry. A connection cut in the middle of a
// frame reads as a bad frame, so only a frame past the size limit, which a re-attach would send again, is not a loss. A
// clean end is io.EOF: an exit frame ends the stream, and so does the end of the connection.
func (p *Process) receive(conn *Conn) (bool, error) {
	for {
		frame, err := conn.Read()
		if err != nil {
			return !errors.Is(err, ErrFrameTooLong), err
		}
		switch frame.Type {
		case TypeOut:
			if appendErr := p.appendOut(frame); appendErr != nil {
				return false, appendErr
			}
		case TypeWritten:
			p.answer(frame.ID, nil)
		case TypeError:
			if frame.ID == "" {
				return false, errors.New(frame.Message)
			}
			p.answer(frame.ID, errors.New(frame.Message))
		case TypeExit:
			p.setExit(frame.Exit)
			return false, io.EOF
		default:
			return false, fmt.Errorf("holder: unexpected %q frame", frame.Type)
		}
	}
}

// appendOut adds an out frame's bytes to the buffer. The frame must start where the stream's last byte ended.
func (p *Process) appendOut(frame Frame) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if frame.Offset != p.next {
		return fmt.Errorf("holder: out frame at offset %d, want %d", frame.Offset, p.next)
	}
	p.buf = append(p.buf, frame.Data...)
	p.next += int64(len(frame.Data))
	p.notifyLocked()
	return nil
}

// setExit records the exit record that the exit frame carries.
func (p *Process) setExit(exit *Exit) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.exit = exit
}

// answer delivers err to the write waiting on id. An answer to no waiting write is dropped.
func (p *Process) answer(id string, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	write, ok := p.pending[id]
	if !ok {
		return
	}
	delete(p.pending, id)
	write.answer <- err
}

// finish ends the stream with err. A detached process ends cleanly, since its controller asked for the end. Each write
// still waiting is answered with the end, and each waiting Read is woken.
func (p *Process) finish(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.detached {
		err = io.EOF
	}
	p.streamEnd = err
	for id, write := range p.pending {
		write.answer <- err
		delete(p.pending, id)
	}
	p.notifyLocked()
}

// notifyLocked wakes every waiter on changed by closing it and starting the next one. The caller holds p.mu.
func (p *Process) notifyLocked() {
	close(p.changed)
	p.changed = make(chan struct{})
}

// isDetached reports whether Detach has been called.
func (p *Process) isDetached() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.detached
}

// currentConn returns the connection the stream runs on, or nil while a linked Process has none.
func (p *Process) currentConn() *Conn {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.conn
}

// closeCurrent closes the connection the stream runs on, when one is current.
func (p *Process) closeCurrent() error {
	conn := p.currentConn()
	if conn == nil {
		return nil
	}
	return conn.Close()
}

// relinking reports whether a linked Process has no connection and no end yet, so it is re-attaching. The caller holds
// p.mu.
func (p *Process) relinking() bool {
	return p.link != nil && p.conn == nil && p.streamEnd == nil
}

// readStdout copies buffered stdout into b, waiting while there is none. After a Read returns bytes while the stream has
// not ended, it acks the consumed offset and the replay-from, so the holder may trim its spool. Once the stream has
// ended, the connection is closed, so there is nothing to ack.
func (p *Process) readStdout(b []byte) (int, error) {
	p.readMu.Lock()
	defer p.readMu.Unlock()
	n, open, err := p.takeBytes(b)
	if err != nil {
		return 0, err
	}
	if open {
		p.ack()
	}
	return n, nil
}

// ack tells the holder what the reader has consumed, on the connection the stream runs on. The consumed offset is raised
// to the attached frame's. The replay-from is the one SetReplayFrom last allowed, raised to the attached frame's floor and
// lowered to consumed. A failed ack on a socket Process waits for the stream to end, so the next Read reports why. A
// failed ack on a linked Process loses that connection, so its stream re-attaches, and the next ack goes on the new one.
func (p *Process) ack() {
	p.mu.Lock()
	consumed := max(p.returned, p.attachedConsumed)
	replayFrom := max(min(p.replayFrom, consumed), p.attachedReplayFrom)
	conn := p.conn
	p.mu.Unlock()
	if conn == nil {
		return
	}
	err := conn.Ack(consumed, replayFrom)
	if err == nil {
		return
	}
	if p.link == nil {
		<-p.streamDone
		return
	}
	p.lose(conn)
}

// takeBytes copies buffered stdout into buf, waiting until there is some, the stream ends, or stdout is closed. A linked
// Process that is re-attaching has no stream to read, so the read waits for it too. Buffered bytes come before the
// stream's end. open reports whether the stream had not ended when the bytes were taken, which is decided under the same
// lock as the copy, so an exit frame or Detach that follows is never acked.
func (p *Process) takeBytes(buf []byte) (int, bool, error) {
	for {
		p.mu.Lock()
		switch {
		case p.stdoutClosed:
			p.mu.Unlock()
			return 0, false, os.ErrClosed
		case p.relinking():
			// Wait for the re-attach below, which wakes this read whichever way it ends.
		case len(p.buf) > 0:
			n := copy(buf, p.buf)
			p.buf = p.buf[n:]
			p.returned += int64(n)
			open := p.exit == nil && p.streamEnd == nil && !p.detached
			p.mu.Unlock()
			return n, open, nil
		case p.streamEnd != nil:
			streamErr := p.streamEnd
			p.mu.Unlock()
			return 0, false, streamErr
		}
		changed := p.changed
		p.mu.Unlock()
		<-changed
	}
}

// writeStdin sends data to the child's stdin as a write frame under id, and waits for the holder's answer. It returns
// len(data) once the holder has written it or answered id as a duplicate, and otherwise the error the holder or the end
// of the stream gives. An id the holder would refuse is not sent, since the holder closes its connection on one, and an id
// still waiting is refused too, since its answer would reach the new write and not the old one. A linked Process with no
// connection fails a write made after an attempt to re-attach failed with that attempt's error.
func (p *Process) writeStdin(id string, data []byte) (int, error) {
	if n := len(id); n == 0 || n > maxWriteIDBytes {
		return 0, fmt.Errorf("holder: write id is %d bytes, want 1 to %d", n, maxWriteIDBytes)
	}
	p.mu.Lock()
	switch {
	case p.stdinClosed:
		p.mu.Unlock()
		return 0, os.ErrClosed
	case p.streamEnd != nil:
		streamErr := p.streamEnd
		p.mu.Unlock()
		return 0, streamErr
	}
	if _, waiting := p.pending[id]; waiting {
		p.mu.Unlock()
		return 0, fmt.Errorf("holder: write id %s is already waiting", id)
	}
	if p.conn == nil && p.relinkErr != nil {
		relinkErr := p.relinkErr
		p.mu.Unlock()
		return 0, fmt.Errorf("holder: link is down: %w", relinkErr)
	}
	write := &pendingWrite{id: id, data: data, seq: p.nextSeq, answer: make(chan error, 1)}
	p.nextSeq++
	p.pending[id] = write
	p.mu.Unlock()
	p.writeMu.Lock()
	sendErr := p.sendWriteHeld(write)
	p.writeMu.Unlock()
	// A linked Process's failed send has already lost its connection, and the write stays pending for the next attach to
	// resend, so only a socket Process fails the write here.
	if sendErr != nil && p.link == nil {
		p.mu.Lock()
		delete(p.pending, id)
		p.mu.Unlock()
		return 0, sendErr
	}
	if writeErr := <-write.answer; writeErr != nil {
		return 0, writeErr
	}
	return len(data), nil
}

// sendWriteHeld sends write on the connection the stream runs on. It sends nothing when write has gone out since the last
// attach, when no connection is current, or when the write is no longer pending, as it is not once the stream has ended.
// It marks write sent before the send. A linked Process whose send fails loses that connection, so its stream re-attaches
// and resends write. The caller holds writeMu.
func (p *Process) sendWriteHeld(write *pendingWrite) error {
	p.mu.Lock()
	conn := p.conn
	if conn == nil || write.sent || p.pending[write.id] != write {
		p.mu.Unlock()
		return nil
	}
	write.sent = true
	p.mu.Unlock()
	err := conn.Write(write.id, write.data)
	if err != nil && p.link != nil {
		p.lose(conn)
	}
	return err
}

// newWriteID returns a fresh write id: 16 random bytes in hex.
func newWriteID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("holder: write id: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}

// processStdin is the write end of a Process's stdin.
type processStdin struct {
	p *Process
}

// Write sends b to the child's stdin under a fresh id and returns once the holder has written it.
func (s processStdin) Write(b []byte) (int, error) {
	id, err := newWriteID()
	if err != nil {
		return 0, err
	}
	return s.p.writeStdin(id, b)
}

// WriteWithID sends b to the child's stdin under id and returns once the holder has written it. The holder answers a
// repeat of a recorded id as a duplicate, which counts as written, so the child receives b once.
func (s processStdin) WriteWithID(id string, b []byte) (int, error) {
	return s.p.writeStdin(id, b)
}

// Close makes later writes fail with os.ErrClosed. It does not affect a write in flight.
func (s processStdin) Close() error {
	s.p.mu.Lock()
	defer s.p.mu.Unlock()
	s.p.stdinClosed = true
	return nil
}

// processStdout is the read end of a Process's stdout.
type processStdout struct {
	p *Process
}

// Read copies the child's stdout into b, waiting for output, the end of the stream, or Close.
func (s processStdout) Read(b []byte) (int, error) {
	return s.p.readStdout(b)
}

// Close wakes a Read blocked on stdout, and later Reads fail with os.ErrClosed. The stream keeps running, so the
// holder's output still arrives, but nothing reads it.
func (s processStdout) Close() error {
	s.p.mu.Lock()
	defer s.p.mu.Unlock()
	s.p.stdoutClosed = true
	s.p.notifyLocked()
	return nil
}
