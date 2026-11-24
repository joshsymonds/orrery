package holder

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"slices"
	"time"
)

// relinkBackoff is how long a linked Process waits before each attempt to re-attach its holder.
const relinkBackoff = 200 * time.Millisecond

// errNoConnection is what a linked Process's stop finds when no connection is current to send stop on.
var errNoConnection = errors.New("holder: no connection to send stop on")

// Link is a route from a controller to one session's holder that does not go through the holder's socket on this
// machine: a holder on another host, reached through that host's agent. The holder's wire runs over the connections
// the link dials, unchanged.
type Link interface {
	// Dial returns a connection that reaches the holder. ReattachLink dials it once to attach, and again to re-attach
	// after the connection drops. The ctx bounds the dial alone: the connection it returns outlives ctx. When the holder
	// has ended, or cannot be attached, Dial's error wraps ErrGone, ErrUnattachable or ErrNoAnswer, as Reattach's errors do.
	Dial(ctx context.Context) (net.Conn, error)
	// Done closes once the holder has ended. It is how a Process learns the holder's end over a link, where a session
	// lock reports it on this machine.
	Done() <-chan struct{}
	// Exit returns the holder's exit record, and whether the holder wrote one. It is meaningful once Done has closed.
	Exit() (Exit, bool)
}

// ReattachLink takes over the holder that link reaches, as Reattach takes over the holder on a socket. It attaches at
// replayFrom, speaking version, over connections that link dials, and returns the Process, whose stdout starts at
// replayFrom, and the ids of the writes the holder has already applied to the child's stdin. The Process learns the
// holder's end from link's Done and Exit. Its errors are Reattach's.
//
// When a connection fails before the holder has ended, the Process dials link again and re-attaches at the offset its
// reader has consumed, so each stdout byte reaches the reader once, and it resends each stdin write the holder did not
// answer under the same id. While it re-attaches, its stdout reads block, and a stdin write made then fails with an error
// that wraps the dial's error. Detach ends the re-attaching, and so does the holder's end.
func ReattachLink(ctx context.Context, link Link, replayFrom int64, version int) (*Process, []string, error) {
	conn, attached, err := attachLink(ctx, link, replayFrom, version)
	if err != nil {
		return nil, nil, err
	}
	lifetime, cancel := context.WithCancel(context.WithoutCancel(ctx))
	process := newLinkedProcess(lifetime, cancel, link, version, conn, replayFrom, attached)
	return process, attached.Written, nil
}

// attachLink dials link and attaches the holder it reaches at replayFrom, speaking version. The dial and the exchange
// are bounded by AttachTimeout and ctx. The connection it returns outlives ctx.
func attachLink(ctx context.Context, link Link, replayFrom int64, version int) (*Conn, Frame, error) {
	due := time.Now().Add(AttachTimeout)
	dialCtx, cancel := context.WithDeadline(ctx, due)
	raw, err := link.Dial(dialCtx)
	cancel()
	if err != nil {
		return nil, Frame{}, attachFailure(fmt.Errorf("holder: dial link: %w", dialRanOut(ctx, err)))
	}
	conn, attached, err := exchangeBounded(ctx, raw, "link", replayFrom, version, due)
	if err != nil {
		return nil, Frame{}, attachFailure(err)
	}
	return conn, attached, nil
}

// attachFailure returns err as a failed attach over a link returns it. A holder that gave no answer within the bound
// wraps ErrNoAnswer. The failure wraps ErrUnattachable unless it already names ErrGone or ErrUnattachable, as Reattach's
// failures do.
func attachFailure(err error) error {
	if errors.Is(err, os.ErrDeadlineExceeded) && !errors.Is(err, ErrNoAnswer) {
		err = fmt.Errorf("%w: %w", ErrNoAnswer, err)
	}
	if errors.Is(err, ErrGone) || errors.Is(err, ErrUnattachable) {
		return err
	}
	return unattachable(err)
}

// stopLink stops the holder that link reaches, as Stop does over a socket: it dials link, greets the holder and sends
// stop, and waits for stopped, all within holderStopTimeout. A linked Process falls back to it when its own connection
// cannot send stop.
func stopLink(link Link) error {
	due := time.Now().Add(holderStopTimeout)
	ctx, cancel := context.WithDeadline(context.Background(), due)
	defer cancel()
	raw, err := link.Dial(ctx)
	if err != nil {
		return fmt.Errorf("holder: dial link: %w", err)
	}
	conn := &Conn{conn: raw, frames: NewFrameReader(raw), controllerVersion: WireVersion}
	if err = raw.SetDeadline(due); err != nil {
		return conn.closeAfter(fmt.Errorf("holder: set deadline on link: %w", err))
	}
	if err = conn.greet(); err != nil {
		return conn.closeAfter(err)
	}
	return conn.Stop()
}

// linkedHolder is the heldProcess of a holder that ReattachLink reaches over a link. The holder is not this controller's
// child, so its end is the link's: Done is the link's Done, and the exit record is the link's Exit.
type linkedHolder struct {
	link Link
}

// Done returns the link's channel that closes once the holder has ended.
func (h linkedHolder) Done() <-chan struct{} {
	return h.link.Done()
}

// Wait returns the holder's end as Gone.Wait reports it from the exit record, once the link reports the holder ended.
func (h linkedHolder) Wait() error {
	<-h.link.Done()
	exit, ok := h.link.Exit()
	if !ok {
		return fmt.Errorf("holder: no exit record %s over the link", ExitName)
	}
	return (&Gone{exit: &exit}).Wait()
}

// Stderr returns the exit record's stderr tail once the holder has ended, and "" before then.
func (h linkedHolder) Stderr() string {
	select {
	case <-h.link.Done():
	default:
		return ""
	}
	exit, ok := h.link.Exit()
	if !ok {
		return ""
	}
	return exit.Stderr
}

// Stop stops the holder over a connection of the link. Process uses it only when its own connection cannot send stop.
func (h linkedHolder) Stop() error {
	return stopLink(h.link)
}

// newLinkedProcess returns the Process for the holder that link reaches, whose stream runs on conn, attached at
// replayFrom at version. Its re-attaching runs under lifetime, which cancel ends; Detach and the end of the stream call
// cancel.
func newLinkedProcess(
	lifetime context.Context,
	cancel context.CancelFunc,
	link Link,
	version int,
	conn *Conn,
	replayFrom int64,
	attached Frame,
) *Process {
	process := newStreamProcess(conn, replayFrom, attached)
	process.held = linkedHolder{link: link}
	process.link = link
	process.relinkTo = func(from int64) (*Conn, Frame, error) {
		return attachLink(lifetime, link, from, version)
	}
	process.relinkDone = lifetime.Done()
	process.cancelRelink = cancel
	process.start(context.WithoutCancel(lifetime), conn)
	return process
}

// relink re-attaches a linked Process's holder after its connection failed before the holder's end was known. It makes
// one attempt after another, each after relinkBackoff, at the offset the reader has consumed. It returns the new
// connection, or false when the stream ends instead: the holder has ended, or the Process stopped re-attaching.
func (p *Process) relink() (*Conn, bool) {
	for p.waitRelink() {
		p.mu.Lock()
		p.relinkErr = nil
		from := p.returned
		p.mu.Unlock()
		conn, attached, err := p.relinkTo(from)
		switch {
		case errors.Is(err, ErrGone):
			<-p.link.Done()
			return nil, false
		case err != nil:
			p.failRelink(err)
		default:
			if !p.install(conn, attached) {
				return nil, false
			}
			return conn, true
		}
	}
	return nil, false
}

// waitRelink waits relinkBackoff before an attempt. It reports false, with no attempt to make, when the holder has ended
// or the Process stopped re-attaching first.
func (p *Process) waitRelink() bool {
	timer := time.NewTimer(relinkBackoff)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-p.link.Done():
		return false
	case <-p.relinkDone:
		return false
	}
}

// failRelink records that an attempt to re-attach failed with err. Each write that has not gone out fails with it, since
// the holder has nothing of it to resend. A write that went out before the connection failed stays pending, and the next
// attach resends it.
func (p *Process) failRelink(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.relinkErr = err
	for id, write := range p.pending {
		if !write.sent {
			delete(p.pending, id)
			write.answer <- fmt.Errorf("holder: link is down: %w", err)
		}
	}
	p.notifyLocked()
}

// install makes conn the connection the stream runs on, attached as attached says. It then resends the writes still
// waiting on the holder, in the order they were registered, and the stop the Process has sent, if any. It reports false,
// after discarding conn, when the Process was detached while it attached.
func (p *Process) install(conn *Conn, attached Frame) bool {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	p.mu.Lock()
	if p.detached {
		p.mu.Unlock()
		p.discard(conn)
		return false
	}
	p.conn = conn
	p.buf = nil
	p.next = p.returned
	p.attachedConsumed = attached.Consumed
	p.attachedReplayFrom = attached.ReplayFrom
	resend := make([]*pendingWrite, 0, len(p.pending))
	for _, write := range p.pending {
		write.sent = false
		resend = append(resend, write)
	}
	stopping := p.stopping
	p.notifyLocked()
	p.mu.Unlock()
	slices.SortFunc(resend, func(a, b *pendingWrite) int { return cmp.Compare(a.seq, b.seq) })
	for _, write := range resend {
		if p.sendWriteHeld(write) != nil {
			// The send lost the connection, and the next attach resends what is left.
			break
		}
	}
	if stopping {
		if err := conn.send(Frame{Type: TypeStop}); err != nil {
			p.lose(conn)
		}
	}
	return true
}

// lose records that conn failed. A linked Process then has no connection until its next attach: its Reads wait, and a
// write not yet sent waits on the attempt that follows. Losing a connection that is no longer current only closes it.
func (p *Process) lose(conn *Conn) {
	p.mu.Lock()
	if p.conn == conn {
		p.conn = nil
		p.relinkErr = nil
		p.notifyLocked()
	}
	p.mu.Unlock()
	p.discard(conn)
}

// discard closes conn, which is no longer wanted, and keeps the error of closing it for the stream's end.
func (p *Process) discard(conn *Conn) {
	if err := conn.Close(); err != nil {
		p.mu.Lock()
		p.lostErr = errors.Join(p.lostErr, err)
		p.mu.Unlock()
	}
}

// sendStop sends stop on the connection the stream runs on, and marks the Process as stopping, so a connection that
// attaches later sends stop too: the holder may not have had the first. It fails when no connection is current or the
// send fails, and a failed send loses the connection.
func (p *Process) sendStop() error {
	p.mu.Lock()
	p.stopping = true
	conn := p.conn
	p.mu.Unlock()
	if conn == nil {
		return errNoConnection
	}
	if err := conn.send(Frame{Type: TypeStop}); err != nil {
		p.lose(conn)
		return err
	}
	return nil
}
