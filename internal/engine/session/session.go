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
	}
	if err := stream.SendInitialize(session.nextRequestID(), nil); err != nil {
		return nil, errors.Join(fmt.Errorf("session: send initialize: %w", err), terminate(process, stream))
	}
	go session.run()
	return session, nil
}

// Events returns the events of the session in the order its process wrote them. It is closed after
// Exited, or when the session is stopped.
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
	s.stopOnce.Do(func() {
		close(s.stop)
		s.stopErr = terminate(s.process, s.transport)
		<-s.finished
	})
	return s.stopErr
}

// nextRequestID returns the next control request id, orrery-1 for the first request written.
func (s *Session) nextRequestID() string {
	return fmt.Sprintf("orrery-%d", s.requests.Add(1))
}

// terminate stops the process and closes the transport, and returns the errors of both.
func terminate(process Process, stream *transport.Transport) error {
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
			if !s.send(event) {
				return
			}
		}
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

// send delivers one event and reports whether it did. Once Stop has begun, it delivers nothing.
func (s *Session) send(event Event) bool {
	select {
	case <-s.stop:
		return false
	default:
	}
	select {
	case s.events <- event:
		return true
	case <-s.stop:
		return false
	}
}
