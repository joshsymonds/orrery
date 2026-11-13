package session

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/joshsymonds/orrery/internal/engine/transport"
)

// The request id and write id of the initialize request, and the longest write id a write may carry in bytes.
const (
	initializeRequestID = "orrery-initialize"
	initializeWriteID   = "initialize"
	maxWriteIDBytes     = 256
)

// idWriter is implemented by a process's stdin when it takes the write id of each write.
type idWriter interface {
	WriteWithID(id string, data []byte) (int, error)
}

// stdinWriter is the write end of a session's stdin. Each write goes under id, the write id the session set for the
// write in progress: through WriteWithID when the process's stdin has it, and through Write when it does not.
type stdinWriter struct {
	writer io.WriteCloser
	id     string
}

// Write writes data under the write id set for it.
func (w *stdinWriter) Write(data []byte) (int, error) {
	if withID, ok := w.writer.(idWriter); ok {
		n, err := withID.WriteWithID(w.id, data)
		if err != nil {
			return n, fmt.Errorf("session: write stdin under %s: %w", w.id, err)
		}
		return n, nil
	}
	n, err := w.writer.Write(data)
	if err != nil {
		return n, fmt.Errorf("session: write stdin: %w", err)
	}
	return n, nil
}

// Close closes the process's stdin.
func (w *stdinWriter) Close() error {
	if err := w.writer.Close(); err != nil {
		return fmt.Errorf("session: close stdin: %w", err)
	}
	return nil
}

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
// translates each one and delivers it as a Line on Lines, so a consumer that stops reading pauses only
// this session.
type Session struct {
	process   Process
	transport *transport.Transport
	// stdin is the write end the transport writes to. writeMu is held across each write the session makes, so the
	// write id set on stdin is the id of that write.
	stdin    *stdinWriter
	writeMu  sync.Mutex
	lines    chan Line
	stop     chan struct{}
	finished chan struct{}
	stopOnce sync.Once
	stopErr  error
	pending  *openRequests
}

// New starts a session on process: it writes the initialize request under its write id and starts delivering
// lines. When the initialize request cannot be written, New stops the process and returns the error.
func New(process Process) (*Session, error) {
	writer := &stdinWriter{writer: process.Stdin()}
	stream := transport.New(writer, process.Stdout(), transport.WithRawLines())
	session := &Session{
		process:   process,
		transport: stream,
		stdin:     writer,
		lines:     make(chan Line),
		stop:      make(chan struct{}),
		finished:  make(chan struct{}),
		pending:   newOpenRequests(),
	}
	err := session.write(initializeWriteID, func() error {
		return stream.SendInitialize(initializeRequestID, nil)
	})
	if err != nil {
		return nil, errors.Join(
			fmt.Errorf("session: send initialize: %w", err),
			terminate(process, stream, false),
		)
	}
	go session.run()
	return session, nil
}

// Lines returns the stdout lines of the session in the order its process wrote them, each with the
// events it carries and its offsets. The process exit arrives last, as a Line at the end of the stream
// whose only event is Exited. Lines is closed after that, or when the session is stopped or detached.
func (s *Session) Lines() <-chan Line {
	return s.lines
}

// Prompt writes text as one user message, under a fresh uuid and writeID as its write id.
func (s *Session) Prompt(writeID, text string) error {
	return s.write(writeID, func() error {
		if _, err := s.transport.SendUserMessage(text); err != nil {
			return fmt.Errorf("session: send prompt: %w", err)
		}
		return nil
	})
}

// Interrupt writes an interrupt request for the turn in progress, under a fresh random id.
func (s *Session) Interrupt() error {
	id, err := newRequestID()
	if err == nil {
		err = s.write(id, func() error { return s.transport.SendInterrupt(id) })
	}
	if err != nil {
		return fmt.Errorf("session: send interrupt: %w", err)
	}
	return nil
}

// Stop ends the process, closes its stream and returns once the reader has finished. It does not
// wait for a consumer to read Lines: once it begins the reader starts no new delivery, so a consumer
// reading at that moment receives at most the one line already offered. Stop is idempotent: later
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

// newRequestID returns a fresh control request id: orrery- and 32 random hex digits. An interrupt and a mode change
// use it as both their request id and their write id.
func newRequestID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("session: request id: %w", err)
	}
	return "orrery-" + hex.EncodeToString(raw[:]), nil
}

// write runs send, which writes one envelope through the transport, with every stdin write it makes carrying id.
// writeMu is held across send, so no other write sets its id in between.
func (s *Session) write(id string, send func() error) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.stdin.id = id
	return send()
}

// terminate ends the process and closes the transport, and returns the errors of both. When detach is
// set and the process can be left running, it detaches the process instead of stopping it.
func terminate(process Process, stream *transport.Transport, detach bool) error {
	if detachable, ok := process.(detacher); ok && detach {
		return errors.Join(detachable.Detach(), stream.Close())
	}
	return errors.Join(process.Stop(), stream.Close())
}

// run is the session's only reader. For each stdout line it translates the line, observes its events and
// sends it as a Line with the line's offsets. When the stream ends it reports the process exit as the last
// Line and closes Lines.
func (s *Session) run() {
	defer close(s.finished)
	defer close(s.lines)
	translator := newTranslator()
	var offset int64
	for raw := range s.transport.Lines() {
		// The transport delivers a line without its LF, so the line spans its bytes and the LF after them.
		end := offset + int64(len(raw)) + 1
		events := translator.translate(raw)
		s.observeLine(offset, events)
		if !s.send(Line{Start: offset, End: end, Events: events}) {
			return
		}
		offset = end
	}
	// Once Stop or Detach has begun, the process is theirs: it has been stopped or left running, so the
	// reader must not stop it or wait on it.
	if s.stopping() {
		return
	}
	s.exit(offset)
}

// exit stops the process and waits for it, then sends Exited with the exit error, the stream error and the
// stderr of the process, as the last Line at offset, where the stream ends.
func (s *Session) exit(offset int64) {
	exitErr := errors.Join(s.process.Stop(), s.process.Wait())
	exited := Exited{
		ExitErr:   exitErr,
		StreamErr: <-s.transport.Done(),
		Stderr:    s.process.Stderr(),
	}
	s.send(Line{Start: offset, End: offset, Events: []Event{exited}})
}

// send delivers one line and reports whether it did. Once Stop or Detach has begun, it delivers nothing.
func (s *Session) send(line Line) bool {
	if s.stopping() {
		return false
	}
	select {
	case s.lines <- line:
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
