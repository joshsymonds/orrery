package session

import (
	"context"
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
	// unanswered counts the prompts the session sent that no turn has answered.
	unanswered *unansweredPrompts
	// translator translates each stdout line in order. The replay and the reader share it, so each line is translated
	// with the state the lines before it left.
	translator *translator
	// written holds the write ids the holder has applied to the process's stdin before a re-adoption.
	written map[string]bool
	// replaying is set while Readopt replays lines: the session observes them and writes nothing for them.
	replaying bool
}

// New starts a session on process: it writes the initialize request under its write id and starts delivering
// lines. When the initialize request cannot be written, New stops the process and returns the error.
func New(process Process) (*Session, error) {
	session := newSession(process, nil)
	err := session.write(initializeWriteID, func() error {
		return session.transport.SendInitialize(initializeRequestID, nil)
	})
	if err != nil {
		return nil, errors.Join(
			fmt.Errorf("session: send initialize: %w", err),
			terminate(process, session.transport, false),
		)
	}
	go session.run(0)
	return session, nil
}

// newSession returns a session on process that has written nothing yet. written holds the write ids the holder has
// applied to the process's stdin.
func newSession(process Process, written []string) *Session {
	writer := &stdinWriter{writer: process.Stdin()}
	session := &Session{
		process:    process,
		transport:  transport.New(writer, process.Stdout(), transport.WithRawLines()),
		stdin:      writer,
		lines:      make(chan Line),
		stop:       make(chan struct{}),
		finished:   make(chan struct{}),
		pending:    newOpenRequests(),
		unanswered: newUnansweredPrompts(),
		translator: newTranslator(),
		written:    make(map[string]bool, len(written)),
	}
	for _, id := range written {
		session.written[id] = true
	}
	return session
}

// ReadoptOptions says where a re-adopted session resumes. From is the stdout offset the process's stdout begins at,
// Consumed the offset the controller has consumed, and Written the write ids the holder has applied to the process's
// stdin. Owed is how many prompts the previous controller wrote to the holder that no later stored turn end answers; it is
// never negative.
type ReadoptOptions struct {
	From, Consumed int64
	Written        []string
	Owed           int
}

// Readopt re-adopts a session whose process's stdout begins at opts.From. It reads and observes the lines from From up
// to opts.Consumed and returns them without writing anything for them: a request the host has not answered is open for
// its answer, and a refused request is not refused again. Later lines go out on Lines with writes on, as New's do. When
// a line ends past Consumed, or the stream or ctx ends first, Readopt fails after ending the process with Detach, or
// with Stop when it has no Detach, so that a holder keeps running.
func Readopt(ctx context.Context, process Process, opts ReadoptOptions) (*Session, []Line, error) {
	session := newSession(process, opts.Written)
	session.unanswered.owe(opts.Owed)
	lines, err := session.replay(ctx, opts)
	if err != nil {
		return nil, nil, errors.Join(err, terminate(process, session.transport, true))
	}
	go session.run(opts.Consumed)
	return session, lines, nil
}

// replay reads the stdout lines from opts.From up to opts.Consumed, observing each and writing nothing for it. The
// session is not running yet, so nothing else reads or writes it while it replays.
func (s *Session) replay(ctx context.Context, opts ReadoptOptions) ([]Line, error) {
	if opts.From > opts.Consumed {
		return nil, fmt.Errorf("session: readopt from %d past consumed %d", opts.From, opts.Consumed)
	}
	s.replaying = true
	defer func() { s.replaying = false }()
	var lines []Line
	offset := opts.From
	for offset < opts.Consumed {
		select {
		case raw, ok := <-s.transport.Lines():
			if !ok {
				return nil, fmt.Errorf(
					"session: readopt stream ended at %d before consumed %d: %w",
					offset, opts.Consumed, <-s.transport.Done(),
				)
			}
			// The transport delivers a line without its LF, so the line spans its bytes and the LF after them.
			end := offset + int64(len(raw)) + 1
			if end > opts.Consumed {
				return nil, fmt.Errorf(
					"session: readopt line [%d, %d) ends past consumed %d", offset, end, opts.Consumed,
				)
			}
			events := s.translator.translate(raw)
			s.observeLine(offset, events)
			lines = append(lines, Line{Start: offset, End: end, Events: events})
			offset = end
		case <-ctx.Done():
			return nil, fmt.Errorf("session: readopt: %w", ctx.Err())
		}
	}
	return lines, nil
}

// replayFromSetter is implemented by a process that takes the stdout offset its output resumes from.
type replayFromSetter interface {
	SetReplayFrom(off int64)
}

// SetReplayFrom tells the process the stdout offset its output resumes from, when the process takes one. A process
// without SetReplayFrom is left as it is.
func (s *Session) SetReplayFrom(off int64) {
	if setter, ok := s.process.(replayFromSetter); ok {
		setter.SetReplayFrom(off)
	}
}

// Lines returns the stdout lines of the session in the order its process wrote them, each with the
// events it carries and its offsets. The process exit arrives last, as a Line at the end of the stream
// whose only event is Exited. Lines is closed after that, or when the session is stopped or detached.
func (s *Session) Lines() <-chan Line {
	return s.lines
}

// Prompt writes text as one user message under writeID, which is both the message's uuid and its write id. The session
// counts the message as unanswered from before the write until a turn lists its uuid as answered, so a turn end read
// while the write is under way counts it, unless that turn end answers it. A message ExpectPrompt counted already is not
// counted again. A message that fails to send is not counted.
func (s *Session) Prompt(writeID, text string) error {
	s.unanswered.sending(writeID)
	err := s.write(writeID, func() error {
		if err := s.transport.SendUserMessageWithUUID(writeID, text); err != nil {
			return fmt.Errorf("session: send prompt: %w", err)
		}
		return nil
	})
	if err != nil {
		s.unanswered.drop(writeID)
		return err
	}
	s.unanswered.written(writeID)
	return nil
}

// ExpectPrompt counts the user message writeID as unanswered from now, before it is recorded or sent, until a turn lists
// it as answered: a turn end read while the message's record is being stored counts it, unless that turn end answers it.
// Prompt then sends it without counting it again. DropPrompt takes the count back for a message that is never sent.
func (s *Session) ExpectPrompt(writeID string) {
	s.unanswered.expect(writeID)
}

// DropPrompt takes back the count ExpectPrompt made of writeID, for a message that is never sent.
func (s *Session) DropPrompt(writeID string) {
	s.unanswered.drop(writeID)
}

// UnansweredPrompts returns how many prompts the session sent, or owes as a re-adopted session (see ReadoptOptions.Owed),
// that no turn has answered. It is safe to call from any goroutine.
func (s *Session) UnansweredPrompts() int {
	return s.unanswered.count()
}

// unansweredPrompts is the set of prompts the session sent that no turn has answered, by uuid, plus the owed prompts a
// re-adopted session was given: the prompts the previous controller wrote to the holder that no turn has answered, which
// the session did not send and so holds no uuid for. ExpectPrompt and Prompt record each send, before it is written, and
// the reader records each turn's end, so every access is guarded by mu. A send is counted before its write, so a turn
// that answers it is read after it is counted; the early set still holds an answer to a uuid not counted, so a send of
// that uuid recorded after it is not counted.
type unansweredPrompts struct {
	mu    sync.Mutex
	open  map[string]bool
	early map[string]bool
	// expected holds the messages counted before their send began (see expect), until the send has written them.
	expected map[string]bool
	owed     int
}

func newUnansweredPrompts() *unansweredPrompts {
	return &unansweredPrompts{
		open:     make(map[string]bool),
		early:    make(map[string]bool),
		expected: make(map[string]bool),
	}
}

// expect counts the user message uuid as sent before its send begins, as sent does, and marks it expected, so sending
// does not count it again, whether or not a turn has answered it meanwhile.
func (u *unansweredPrompts) expect(uuid string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.expected[uuid] = true
	u.sentLocked(uuid)
}

// sending counts the user message uuid as sent as its send begins, unless expect counted it already.
func (u *unansweredPrompts) sending(uuid string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if !u.expected[uuid] {
		u.sentLocked(uuid)
	}
}

// written forgets that the user message uuid was expected, once its send has written it.
func (u *unansweredPrompts) written(uuid string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	delete(u.expected, uuid)
}

// drop forgets the user message uuid, counted as sent and never sent.
func (u *unansweredPrompts) drop(uuid string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	delete(u.open, uuid)
	delete(u.expected, uuid)
}

// owe sets how many owed prompts the session holds, before its reader runs.
func (u *unansweredPrompts) owe(n int) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.owed = n
}

// sent records that the session sent the user message uuid, unless a turn already answered it.
func (u *unansweredPrompts) sent(uuid string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.sentLocked(uuid)
}

// sentLocked records the send of uuid, as sent does. The caller holds mu.
func (u *unansweredPrompts) sentLocked(uuid string) {
	if u.early[uuid] {
		delete(u.early, uuid)
		return
	}
	u.open[uuid] = true
}

// answer records that a turn answered the user messages uuids, and returns how many prompts are unanswered after it, as
// count does. A message that was not recorded as sent is one of the owed prompts while owed is above 0, which it
// lowers. Otherwise it is kept in early.
func (u *unansweredPrompts) answer(uuids []string) int {
	u.mu.Lock()
	defer u.mu.Unlock()
	for _, uuid := range uuids {
		switch {
		case u.open[uuid]:
			delete(u.open, uuid)
		case u.owed > 0:
			u.owed--
		default:
			u.early[uuid] = true
		}
	}
	return len(u.open) + u.owed
}

// count returns how many sent user messages no turn has answered, plus the owed prompts no turn has answered.
func (u *unansweredPrompts) count() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return len(u.open) + u.owed
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

// run is the session's only reader. From offset, the offset of the first line the transport delivers, it translates
// each stdout line, observes its events, records the prompts its turn ends answer and sends it as a Line with the
// line's offsets. When the stream ends it reports the process exit as the last Line and closes Lines.
func (s *Session) run(offset int64) {
	defer close(s.finished)
	defer close(s.lines)
	for raw := range s.transport.Lines() {
		// The transport delivers a line without its LF, so the line spans its bytes and the LF after them.
		end := offset + int64(len(raw)) + 1
		events := s.translator.translate(raw)
		s.observeLine(offset, events)
		s.answerPrompts(events)
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

// answerPrompts records the user messages each turn end in events lists as answered, and gives each turn end the count
// of prompts still unanswered after it (TurnEnded.Unanswered). Only run calls it: a replayed turn answers prompts the
// previous holder was sent, never one this session sent.
func (s *Session) answerPrompts(events []Event) {
	for i, event := range events {
		if ended, ok := event.(TurnEnded); ok {
			ended.Unanswered = s.unanswered.answer(ended.Answered)
			events[i] = ended
		}
	}
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
