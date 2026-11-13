package session

import (
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"

	"github.com/joshsymonds/orrery/internal/engine/transport"
)

// Process is a started session process: its stdin and stdout pipes, the stderr it wrote, and the
// means to stop it and wait for its exit.
type Process interface {
	Stdin() io.WriteCloser
	Stdout() io.ReadCloser
	Stderr() string
	Stop() error
	Wait() error
}

// detacher is implemented by a process that can be left running when its session ends. Detach calls
// it in place of Stop.
type detacher interface {
	Detach() error
}

// Session is one headless Claude Code process. One goroutine reads its stdout lines in order,
// translates them and delivers each event on Events, so a consumer that stops reading pauses only
// this session.
type Session struct {
	process   Process
	transport *transport.Transport
	events    chan Event
	stop      chan struct{}
	finished  chan struct{}
	stopOnce  sync.Once
	stopErr   error
	requests  atomic.Uint64
	pending   *openRequests
}

// New starts a session on process: it writes the initialize request and starts delivering events.
// When the initialize request cannot be written, New stops the process and returns the error.
func New(process Process) (*Session, error) {
	stream := transport.New(process.Stdin(), process.Stdout(), transport.WithRawLines())
	session := &Session{
		process:   process,
		transport: stream,
		events:    make(chan Event),
		stop:      make(chan struct{}),
		finished:  make(chan struct{}),
		pending:   newOpenRequests(),
	}
	if err := stream.SendInitialize(session.nextRequestID(), nil); err != nil {
		return nil, errors.Join(
			fmt.Errorf("session: send initialize: %w", err),
			terminate(process, stream, false),
		)
	}
	go session.run()
	return session, nil
}

// Events returns the events of the session in the order its process wrote them. It is closed after
// Exited, or when the session is stopped or detached.
func (s *Session) Events() <-chan Event {
	return s.events
}

// Prompt writes text as one user message, under a fresh uuid.
func (s *Session) Prompt(text string) error {
	if _, err := s.transport.SendUserMessage(text); err != nil {
		return fmt.Errorf("session: send prompt: %w", err)
	}
	return nil
}

// Interrupt writes an interrupt request for the turn in progress.
func (s *Session) Interrupt() error {
	if err := s.transport.SendInterrupt(s.nextRequestID()); err != nil {
		return fmt.Errorf("session: send interrupt: %w", err)
	}
	return nil
}

// Stop ends the process, closes its stream and returns once the reader has finished. It does not
// wait for a consumer to read Events: once it begins the reader starts no new delivery, so a consumer
// reading at that moment receives at most the one event already offered. Stop is idempotent: later
// calls return the first call's error.
func (s *Session) Stop() error {
	return s.end(false)
}

// Detach ends the session as Stop does, except that a process with a Detach() error method is left
// running: its Detach is called in place of its Stop. Detach shares Stop's once, so whichever comes
// first holds, and later calls of either return its error.
func (s *Session) Detach() error {
	return s.end(true)
}

// end ends the session once: it closes stop, ends the process with terminate, and waits for the
// reader. Later calls return the first call's error.
func (s *Session) end(detach bool) error {
	s.stopOnce.Do(func() {
		close(s.stop)
		s.stopErr = terminate(s.process, s.transport, detach)
		<-s.finished
	})
	return s.stopErr
}

// nextRequestID returns the next control request id, orrery-1 for the first request written.
func (s *Session) nextRequestID() string {
	return fmt.Sprintf("orrery-%d", s.requests.Add(1))
}

// terminate ends the process and closes the transport, and returns the errors of both. When detach is
// set and the process can be left running, it detaches the process instead of stopping it.
func terminate(process Process, stream *transport.Transport, detach bool) error {
	if detachable, ok := process.(detacher); ok && detach {
		return errors.Join(detachable.Detach(), stream.Close())
	}
	return errors.Join(process.Stop(), stream.Close())
}

// run is the session's only reader. It delivers the events of each line in order, and when the
// stream ends it reports the process exit and closes Events.
func (s *Session) run() {
	defer close(s.finished)
	defer close(s.events)
	translator := newTranslator()
	for line := range s.transport.Lines() {
		for _, event := range translator.translate(line) {
			if !s.deliver(event) {
				return
			}
		}
	}
	// Once Stop or Detach has begun, the process is theirs: it has been stopped or left running, so the
	// reader must not stop it or wait on it.
	if s.stopping() {
		return
	}
	s.exit()
}

// exit stops the process and waits for it, then delivers Exited with the exit error, the stream
// error and the stderr of the process.
func (s *Session) exit() {
	exitErr := errors.Join(s.process.Stop(), s.process.Wait())
	s.send(Exited{
		ExitErr:   exitErr,
		StreamErr: <-s.transport.Done(),
		Stderr:    s.process.Stderr(),
	})
}

// send delivers one event and reports whether it did. Once Stop or Detach has begun, it delivers nothing.
func (s *Session) send(event Event) bool {
	if s.stopping() {
		return false
	}
	select {
	case s.events <- event:
		return true
	case <-s.stop:
		return false
	}
}

// stopping reports whether Stop or Detach has begun.
func (s *Session) stopping() bool {
	select {
	case <-s.stop:
		return true
	default:
		return false
	}
}
