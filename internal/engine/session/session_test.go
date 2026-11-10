package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/engine/transport"
)

// testGuard bounds every wait on a session. It only turns a hang into a failure: no test expects
// anything to happen within it.
const testGuard = 10 * time.Second

// stream is an in-memory byte stream. Writes never block. Reads block until bytes arrive, the stream
// ends after its written bytes drain, or it is closed, which ends reads at once.
type stream struct {
	mu     sync.Mutex
	cond   *sync.Cond
	data   []byte
	ended  bool
	closed bool
}

func newStream() *stream {
	s := &stream{}
	s.cond = sync.NewCond(&s.mu)
	return s
}

func (s *stream) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.ended {
		return 0, io.ErrClosedPipe
	}
	s.data = append(s.data, p...)
	s.cond.Broadcast()
	return len(p), nil
}

func (s *stream) Read(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for len(s.data) == 0 && !s.ended && !s.closed {
		s.cond.Wait()
	}
	if s.closed || len(s.data) == 0 {
		return 0, io.EOF
	}
	n := copy(p, s.data)
	s.data = s.data[n:]
	return n, nil
}

func (s *stream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	s.cond.Broadcast()
	return nil
}

// end ends the stream after its written bytes drain, as a process exit does.
func (s *stream) end() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ended = true
	s.cond.Broadcast()
}

// contents returns a copy of every byte written and not yet read.
func (s *stream) contents() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return bytes.Clone(s.data)
}

// fakeProcess is an in-memory session process. Its stdout is written by the test; Stop and exit end
// it the way a real exit does.
type fakeProcess struct {
	stdin    *stream
	stdout   *stream
	once     sync.Once
	done     chan struct{}
	reaped   chan struct{}
	reapOnce sync.Once
	exitErr  error
	stderr   string
}

func newFakeProcess() *fakeProcess {
	return &fakeProcess{
		stdin:  newStream(),
		stdout: newStream(),
		done:   make(chan struct{}),
		reaped: make(chan struct{}),
	}
}

func (p *fakeProcess) Stdin() io.WriteCloser { return p.stdin }

func (p *fakeProcess) Stdout() io.ReadCloser { return p.stdout }

// Stderr returns what the process wrote to stderr only once Wait has returned, as a real process's
// stderr is complete only after it is reaped. Before that it returns "".
func (p *fakeProcess) Stderr() string {
	select {
	case <-p.reaped:
		return p.stderr
	default:
		return ""
	}
}

func (p *fakeProcess) Stop() error {
	p.exit(nil, "")
	return nil
}

// Wait returns once the process has exited, and from then on Stderr reports what it wrote.
func (p *fakeProcess) Wait() error {
	<-p.done
	p.reapOnce.Do(func() { close(p.reaped) })
	return p.exitErr
}

// exit ends the process once: its stdout ends after the bytes written to it, and Wait returns err.
func (p *fakeProcess) exit(err error, stderr string) {
	p.once.Do(func() {
		p.exitErr = err
		p.stderr = stderr
		p.stdout.end()
		close(p.done)
	})
}

// exited reports whether the process has ended, without waiting.
func (p *fakeProcess) exited() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}

// startSession starts a session on a fake process and stops it when the test ends.
func startSession(t *testing.T) (*Session, *fakeProcess) {
	t.Helper()
	process := newFakeProcess()
	session, err := New(process)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		if stopErr := stopWithin(t, session); stopErr != nil {
			t.Errorf("Stop in cleanup: %v", stopErr)
		}
	})
	return session, process
}

// emit writes each line and its newline to the process's stdout.
func emit(t *testing.T, process *fakeProcess, lines ...[]byte) {
	t.Helper()
	for _, line := range lines {
		if _, err := process.stdout.Write(append(bytes.Clone(line), '\n')); err != nil {
			t.Fatalf("write stdout line: %v", err)
		}
	}
}

// stdinLines returns the lines written to the process's stdin so far.
func stdinLines(process *fakeProcess) []string {
	text := strings.TrimSuffix(string(process.stdin.contents()), "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

// nextEvent returns the next event, failing the test if none arrives within testGuard.
func nextEvent(t *testing.T, session *Session) Event {
	t.Helper()
	select {
	case event, ok := <-session.Events():
		if !ok {
			t.Fatal("Events closed before the next event")
		}
		return event
	case <-time.After(testGuard):
		t.Fatalf("no event within %v", testGuard)
	}
	return nil
}

// readEvents returns the next n events in order.
func readEvents(t *testing.T, session *Session, n int) []Event {
	t.Helper()
	events := make([]Event, 0, n)
	for range n {
		events = append(events, nextEvent(t, session))
	}
	return events
}

// drainEvents returns every event up to the close of Events, failing the test if Events does not
// close within testGuard.
func drainEvents(t *testing.T, session *Session) []Event {
	t.Helper()
	var events []Event
	for {
		select {
		case event, ok := <-session.Events():
			if !ok {
				return events
			}
			events = append(events, event)
		case <-time.After(testGuard):
			t.Fatalf("Events did not close within %v after %d events", testGuard, len(events))
		}
	}
}

// requireClosed fails the test unless Events is closed within testGuard.
func requireClosed(t *testing.T, session *Session) {
	t.Helper()
	select {
	case event, ok := <-session.Events():
		if ok {
			t.Fatalf("Events delivered %#v, want it closed", event)
		}
	case <-time.After(testGuard):
		t.Fatalf("Events not closed within %v", testGuard)
	}
}

// stopWithin calls Stop and fails the test unless it returns within testGuard.
func stopWithin(t *testing.T, session *Session) error {
	t.Helper()
	result := make(chan error, 1)
	go func() { result <- session.Stop() }()
	select {
	case err := <-result:
		return err
	case <-time.After(testGuard):
		t.Fatalf("Stop did not return within %v", testGuard)
	}
	return nil
}

// stackDumpSize bounds the goroutine dump that awaitSenderParked reads.
const stackDumpSize = 1 << 20

// awaitSenderParked returns once a session reader is blocked in the select of send, the only point where
// the reader blocks while its consumer does not read. It fails the test if that does not happen within
// testGuard. It waits for a state, not for a duration.
func awaitSenderParked(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(testGuard)
	for !senderParked() {
		if time.Now().After(deadline) {
			t.Fatalf("no session reader blocked in send within %v", testGuard)
		}
		runtime.Gosched()
	}
}

// senderParked reports whether a goroutine dump shows a goroutine blocked on a channel operation inside
// (*Session).send. The non-blocking check at the top of send never shows as blocked. The blocking
// operation is a select while send also watches stop, and a plain channel send when it does not.
func senderParked() bool {
	buf := make([]byte, stackDumpSize)
	dump := string(buf[:runtime.Stack(buf, true)])
	for _, goroutine := range strings.Split(dump, "\n\n") {
		header, _, _ := strings.Cut(goroutine, "\n")
		blocked := strings.Contains(header, "[select") || strings.Contains(header, "[chan send")
		if blocked && strings.Contains(goroutine, "(*Session).send(") {
			return true
		}
	}
	return false
}

// sentEnvelope is the part of a user envelope the tests read.
type sentEnvelope struct {
	Type    string `json:"type"`
	UUID    string `json:"uuid"`
	Message struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	} `json:"message"`
}

// requireUserEnvelope fails the test unless line is a user envelope carrying text, and returns its uuid.
func requireUserEnvelope(t *testing.T, line, text string) string {
	t.Helper()
	var envelope sentEnvelope
	if err := json.Unmarshal([]byte(line), &envelope); err != nil {
		t.Fatalf("decode envelope %q: %v", line, err)
	}
	if envelope.Type != "user" || len(envelope.Message.Content) != 1 || envelope.Message.Content[0].Text != text {
		t.Fatalf("envelope = %q, want a user message with text %q", line, text)
	}
	if envelope.UUID == "" {
		t.Fatalf("envelope %q carries no uuid", line)
	}
	return envelope.UUID
}

const wantInitialize = `{"type":"control_request","request_id":"orrery-1",` +
	`"request":{"subtype":"initialize","supportedDialogKinds":null,"hooks":{},"systemPrompt":[]}}`

const wantInterrupt = `{"type":"control_request","request_id":"orrery-2","request":{"subtype":"interrupt"}}`

// replayFixtures returns the capture fixtures a session replays, one per recorded shape.
func replayFixtures() []string {
	return []string{"question", "permission-allow", "permission-deny", "interrupt", "mode-change", "two-turns"}
}

func TestNewWritesInitializeFirst(t *testing.T) {
	t.Parallel()
	_, process := startSession(t)
	lines := stdinLines(process)
	if len(lines) != 1 || lines[0] != wantInitialize {
		t.Errorf("stdin lines = %q, want exactly the initialize request %q", lines, wantInitialize)
	}
}

func TestTwoPromptsWriteTwoEnvelopesWithDistinctUUIDs(t *testing.T) {
	t.Parallel()
	session, process := startSession(t)
	if err := session.Prompt("first prompt"); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if err := session.Prompt("second prompt"); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	lines := stdinLines(process)
	if len(lines) != 3 {
		t.Fatalf("stdin has %d lines, want initialize and two prompts: %q", len(lines), lines)
	}
	first := requireUserEnvelope(t, lines[1], "first prompt")
	second := requireUserEnvelope(t, lines[2], "second prompt")
	if first == second {
		t.Errorf("both prompts carry uuid %q, want distinct uuids", first)
	}
}

func TestInterruptThenPromptReachTheirTurns(t *testing.T) {
	t.Parallel()
	session, process := startSession(t)
	if err := session.Interrupt(); err != nil {
		t.Fatalf("Interrupt: %v", err)
	}
	lines := stdinLines(process)
	if len(lines) != 2 || lines[1] != wantInterrupt {
		t.Fatalf("stdin lines = %q, want the interrupt request %q second", lines, wantInterrupt)
	}

	interrupted := translateFixture(t, "interrupt")
	emit(t, process, fixtureLines(t, "interrupt")...)
	requireEvents(t, readEvents(t, session, len(interrupted)), interrupted)

	if err := session.Prompt("after the interrupt"); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	lines = stdinLines(process)
	if len(lines) != 3 {
		t.Fatalf("stdin has %d lines after the prompt, want 3: %q", len(lines), lines)
	}
	requireUserEnvelope(t, lines[2], "after the interrupt")

	next := translateFixture(t, "two-turns")
	emit(t, process, fixtureLines(t, "two-turns")...)
	requireEvents(t, readEvents(t, session, len(next)), next)
}

func TestFixtureReplayDeliversTranslatorEventsThenExited(t *testing.T) {
	t.Parallel()
	for _, name := range replayFixtures() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			session, process := startSession(t)
			want := translateFixture(t, name)
			emit(t, process, fixtureLines(t, name)...)
			process.exit(nil, "")

			got := drainEvents(t, session)
			if len(got) != len(want)+1 {
				t.Fatalf("got %d events, want %d translated events then Exited", len(got), len(want))
			}
			requireEvents(t, got[:len(want)], want)
			exited, ok := got[len(got)-1].(Exited)
			if !ok {
				t.Fatalf("last event = %#v, want Exited", got[len(got)-1])
			}
			if exited.ExitErr != nil || exited.Stderr != "" {
				t.Errorf("Exited = %#v, want no exit error and no stderr", exited)
			}
			requireStreamEnded(t, exited)
		})
	}
}

// requireStreamEnded fails the test unless the stream error is the transport's read error.
func requireStreamEnded(t *testing.T, exited Exited) {
	t.Helper()
	var transportErr *transport.TransportError
	if !errors.As(exited.StreamErr, &transportErr) {
		t.Errorf("StreamErr = %v, want a transport error", exited.StreamErr)
	}
}

func TestProcessExitDeliversExitErrorAndStderr(t *testing.T) {
	t.Parallel()
	session, process := startSession(t)
	errExit := errors.New("exit status 3")
	process.exit(errExit, "boom: bad flag")

	got := drainEvents(t, session)
	if len(got) != 1 {
		t.Fatalf("got events %#v, want only Exited", got)
	}
	exited, ok := got[0].(Exited)
	if !ok {
		t.Fatalf("event = %#v, want Exited", got[0])
	}
	if !errors.Is(exited.ExitErr, errExit) {
		t.Errorf("ExitErr = %v, want %v", exited.ExitErr, errExit)
	}
	if exited.Stderr != "boom: bad flag" {
		t.Errorf("Stderr = %q, want %q", exited.Stderr, "boom: bad flag")
	}
	requireStreamEnded(t, exited)
}

// TestStopWithUnreadConsumerReturnsAndClosesEvents is not parallel: awaitSenderParked reads the
// goroutine dump, which must not show the readers of other sessions.
func TestStopWithUnreadConsumerReturnsAndClosesEvents(t *testing.T) {
	session, process := startSession(t)
	want := translateFixture(t, "two-turns")
	if len(want) < 2 {
		t.Fatalf("fixture two-turns yields %d events, want at least 2", len(want))
	}
	emit(t, process, fixtureLines(t, "two-turns")...)
	requireEvents(t, readEvents(t, session, 1), want[:1])
	awaitSenderParked(t)

	if err := stopWithin(t, session); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	requireClosed(t, session)
	if !process.exited() {
		t.Errorf("Stop left the process running")
	}
}

func TestStopIsIdempotentAndStopsTheProcess(t *testing.T) {
	t.Parallel()
	session, process := startSession(t)
	if err := stopWithin(t, session); err != nil {
		t.Fatalf("first Stop: %v", err)
	}
	if err := stopWithin(t, session); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
	if !process.exited() {
		t.Errorf("Stop left the process running")
	}
	requireClosed(t, session)
}

// TestUnreadSessionDoesNotDelayAnother is not parallel: it waits until the unread session's reader is
// parked in send, which awaitSenderParked sees in a dump of every goroutine.
func TestUnreadSessionDoesNotDelayAnother(t *testing.T) {
	unread, unreadProcess := startSession(t)
	reading, readingProcess := startSession(t)
	emit(t, unreadProcess, []byte(`{"type":"control_cancel_request","request_id":"req_unread_1"}`))
	awaitSenderParked(t)
	emit(t, readingProcess, []byte(`{"type":"control_cancel_request","request_id":"req_reading_1"}`))

	want := RequestCancelled{RequestID: "req_reading_1"}
	if got := nextEvent(t, reading); got != want {
		t.Errorf("reading session event = %#v, want %#v", got, want)
	}
	want = RequestCancelled{RequestID: "req_unread_1"}
	if got := nextEvent(t, unread); got != want {
		t.Errorf("unread session event = %#v, want %#v once its consumer reads", got, want)
	}
}
