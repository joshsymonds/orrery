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
// out frames, and its stdin is written only by write frames, each under a fresh id that the holder answers.
type Process struct {
	held   heldProcess
	conn   *Conn
	socket string
	remove string

	// readMu serializes Reads, so the acks they send reach the holder in the order of their consumed offsets.
	readMu sync.Mutex
	// returned is the stdout offset just past the bytes Read has returned. It starts at the offset the stream starts at.
	// Only Read touches it, under readMu.
	returned int64
	// attachedConsumed and attachedReplayFrom are the consumed and replay-from offsets the holder's attached frame carried.
	// The holder never moves them back, so an ack carries no lower offsets. They are set before the stream starts.
	attachedConsumed   int64
	attachedReplayFrom int64

	stopOnce sync.Once
	stopErr  error

	// streamDone closes once the stream has ended and its exit record, if any, is set. ended closes once the holder's
	// process has ended and its directory is handled, and endErr is set before it closes.
	streamDone chan struct{}
	// closeErr is the error of closing the connection at the stream's end. run sets it before streamDone closes.
	closeErr error
	ended    chan struct{}
	endErr   error

	mu sync.Mutex
	// changed closes, and is replaced, whenever a field below that a waiter checks changes.
	changed chan struct{}
	// buf holds stdout the holder has sent and Read has not returned. next is the offset the next out frame starts at.
	buf  []byte
	next int64
	// replayFrom is the offset SetReplayFrom last allowed, 0 until it is called.
	replayFrom int64
	// streamEnd is the error that ended the stream, io.EOF for a clean end. It is nil while the stream runs.
	streamEnd error
	// exit is the exit record, once the exit frame has arrived.
	exit *Exit
	// pending holds, by write id, the channel that answers each write waiting on the holder.
	pending      map[string]chan error
	stdinClosed  bool
	stdoutClosed bool
	detached     bool
}

// newProcess returns the Process for held, the holder's own process. conn is attached at offset 0 to the holder that
// socket names, on a holder that has not yet spooled or consumed anything. remove is the directory removed once the
// holder has ended.
func newProcess(held heldProcess, conn *Conn, socket, remove string) *Process {
	return newAttachedProcess(held, conn, socket, remove, 0, Frame{})
}

// newAttachedProcess returns the Process for held, the holder's own process, whose conn is attached at replayFrom. The
// stream's out frames start at replayFrom. attached is the attached frame the holder answered: acks carry no consumed or
// replay-from below its offsets. remove is the directory removed once the holder has ended.
func newAttachedProcess(
	held heldProcess,
	conn *Conn,
	socket, remove string,
	replayFrom int64,
	attached Frame,
) *Process {
	process := &Process{
		held:               held,
		conn:               conn,
		socket:             socket,
		remove:             remove,
		returned:           replayFrom,
		next:               replayFrom,
		attachedConsumed:   attached.Consumed,
		attachedReplayFrom: attached.ReplayFrom,
		streamDone:         make(chan struct{}),
		ended:              make(chan struct{}),
		changed:            make(chan struct{}),
		pending:            make(map[string]chan error),
	}
	go process.run()
	go process.awaitHolder()
	return process
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

// stopHolder asks the holder to stop its child over its socket. When that fails, it stops the holder's own process, and
// it returns an error only when that fails too.
func (p *Process) stopHolder() error {
	socketErr := stopOverSocket(p.socket)
	if socketErr == nil {
		return nil
	}
	if heldErr := p.held.Stop(); heldErr != nil {
		return errors.Join(socketErr, heldErr)
	}
	return nil
}

// stopOverSocket dials the holder on socket and sends it stop, which returns once the child has stopped. The dial, the
// hello and the wait for stopped share one deadline holderStopTimeout away, so a holder that accepts its socket and never
// answers fails the stop, and stopHolder then falls back to the holder's own process.
func stopOverSocket(socket string) error {
	return stopBounded(context.Background(), socket, time.Now().Add(holderStopTimeout))
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
// Reads end. Detach is idempotent.
func (p *Process) Detach() error {
	p.mu.Lock()
	if p.detached {
		p.mu.Unlock()
		return nil
	}
	p.detached = true
	p.mu.Unlock()
	return p.conn.Close()
}

// run receives the stream until it ends, then records the end, closes the connection and closes streamDone. Nothing
// is sent once the stream has ended, so the connection is not needed after it.
func (p *Process) run() {
	p.finish(p.receive())
	p.closeErr = p.conn.Close()
	close(p.streamDone)
}

// awaitHolder waits for the holder's process to end and the stream to finish, so the exit record is read first. It then
// removes the holder's directory unless the process is detached, and closes ended. endErr carries the connection's close
// error and the removal's.
func (p *Process) awaitHolder() {
	<-p.held.Done()
	<-p.streamDone
	p.endErr = p.closeErr
	if !p.isDetached() {
		if err := os.RemoveAll(p.remove); err != nil {
			p.endErr = errors.Join(p.endErr, fmt.Errorf("holder: remove %s: %w", p.remove, err))
		}
	}
	close(p.ended)
}

// receive reads frames until the stream ends, and returns the error that ends it. A clean end is io.EOF: an exit frame
// ends the stream, and so does the end of the connection.
func (p *Process) receive() error {
	for {
		frame, err := p.conn.Read()
		if err != nil {
			return err
		}
		switch frame.Type {
		case TypeOut:
			if appendErr := p.appendOut(frame); appendErr != nil {
				return appendErr
			}
		case TypeWritten:
			p.answer(frame.ID, nil)
		case TypeError:
			if frame.ID == "" {
				return errors.New(frame.Message)
			}
			p.answer(frame.ID, errors.New(frame.Message))
		case TypeExit:
			p.setExit(frame.Exit)
			return io.EOF
		default:
			return fmt.Errorf("holder: unexpected %q frame", frame.Type)
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
	answer, ok := p.pending[id]
	if !ok {
		return
	}
	delete(p.pending, id)
	answer <- err
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
	for id, answer := range p.pending {
		answer <- err
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

// readStdout copies buffered stdout into b, waiting while there is none. After a Read returns bytes while the stream has
// not ended, it acks the consumed offset and the replay-from, so the holder may trim its spool. Once the stream has
// ended, the connection is closed, so there is nothing to ack. The stream can end while an ack is in flight (the holder
// closes its connection just after its exit frame, and the stream's end closes ours), so a failed ack waits for the
// stream's end and returns the bytes with no error: the next Read reports how the stream ended, which is also why the ack
// failed.
func (p *Process) readStdout(b []byte) (int, error) {
	p.readMu.Lock()
	defer p.readMu.Unlock()
	n, open, err := p.takeBytes(b)
	if err != nil {
		return 0, err
	}
	p.returned += int64(n)
	if !open {
		return n, nil
	}
	consumed := max(p.returned, p.attachedConsumed)
	if ackErr := p.conn.Ack(consumed, p.ackReplayFrom(consumed)); ackErr != nil {
		<-p.streamDone
	}
	return n, nil
}

// ackReplayFrom returns the replay-from an ack of consumed carries: the offset SetReplayFrom last allowed, but no higher
// than consumed, and no lower than the attached frame's replay-from, which the holder would refuse.
func (p *Process) ackReplayFrom(consumed int64) int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return max(min(p.replayFrom, consumed), p.attachedReplayFrom)
}

// takeBytes copies buffered stdout into buf, waiting until there is some, the stream ends, or stdout is closed. Buffered
// bytes come before the stream's end. open reports whether the stream had not ended when the bytes were taken, which is
// decided under the same lock as the copy, so an exit frame or Detach that follows is never acked.
func (p *Process) takeBytes(buf []byte) (int, bool, error) {
	for {
		p.mu.Lock()
		switch {
		case p.stdoutClosed:
			p.mu.Unlock()
			return 0, false, os.ErrClosed
		case len(p.buf) > 0:
			n := copy(buf, p.buf)
			p.buf = p.buf[n:]
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
// still waiting is refused too, since its answer would reach the new write and not the old one.
func (p *Process) writeStdin(id string, data []byte) (int, error) {
	if n := len(id); n == 0 || n > maxWriteIDBytes {
		return 0, fmt.Errorf("holder: write id is %d bytes, want 1 to %d", n, maxWriteIDBytes)
	}
	answer := make(chan error, 1)
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
	p.pending[id] = answer
	p.mu.Unlock()
	if sendErr := p.conn.Write(id, data); sendErr != nil {
		p.mu.Lock()
		delete(p.pending, id)
		p.mu.Unlock()
		return 0, sendErr
	}
	if writeErr := <-answer; writeErr != nil {
		return 0, writeErr
	}
	return len(data), nil
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
