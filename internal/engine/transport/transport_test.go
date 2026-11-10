package transport

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/engine/wire"
)

const (
	testTimeout  = 5 * time.Second
	stallWindow  = 200 * time.Millisecond
	backlogCount = 200
	// maxLineTimeout is the hang guard for the test that pushes a line of MaxLineBytes through the
	// transport under the race detector.
	maxLineTimeout = 60 * time.Second

	fixedUUIDOne = "00000000-0000-4000-8000-000000000001"
	fixedUUIDTwo = "00000000-0000-4000-8000-000000000002"
)

// fakeProcess holds the far end of a pair of pipes. The transport writes to stdinWriter and
// reads from stdoutReader; the test plays the process through stdinReader and stdoutWriter.
type fakeProcess struct {
	stdinReader  *os.File
	stdinWriter  *os.File
	stdoutReader *os.File
	stdoutWriter *os.File
}

func newFakeProcess(t *testing.T) *fakeProcess {
	t.Helper()
	stdinReader, stdinWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() for stdin: %v", err)
	}
	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() for stdout: %v", err)
	}
	fake := &fakeProcess{
		stdinReader:  stdinReader,
		stdinWriter:  stdinWriter,
		stdoutReader: stdoutReader,
		stdoutWriter: stdoutWriter,
	}
	t.Cleanup(func() {
		for _, pipe := range []*os.File{stdinReader, stdinWriter, stdoutReader, stdoutWriter} {
			_ = pipe.Close()
		}
	})
	return fake
}

// startTransport starts a transport over the fake's pipes and closes it when the test ends.
func startTransport(t *testing.T, fake *fakeProcess, options ...Option) *Transport {
	t.Helper()
	transport := New(fake.stdinWriter, fake.stdoutReader, options...)
	t.Cleanup(func() {
		_ = transport.Close()
	})
	return transport
}

// emit writes data to the fake's stdout from a goroutine. Once the transport has closed its end,
// the write fails, and that failure is expected, so it is ignored.
func emit(fake *fakeProcess, data string) {
	go func() {
		_, _ = io.WriteString(fake.stdoutWriter, data)
	}()
}

// stdinLines reads count newline-terminated lines that the transport writes to the fake's stdin.
// The returned channel closes early if a read fails.
func stdinLines(fake *fakeProcess, count int) <-chan []byte {
	lines := make(chan []byte, count)
	go func() {
		defer close(lines)
		reader := bufio.NewReader(fake.stdinReader)
		for range count {
			line, err := reader.ReadBytes('\n')
			if err != nil {
				return
			}
			lines <- line
		}
	}()
	return lines
}

// receive returns the next value from ch, failing the test if none arrives in time.
func receive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	return receiveWithin(t, ch, testTimeout)
}

// receiveWithin returns the next value from ch, failing the test if none arrives within timeout.
func receiveWithin[T any](t *testing.T, ch <-chan T, timeout time.Duration) T {
	t.Helper()
	var zero T
	select {
	case value, ok := <-ch:
		if !ok {
			t.Fatal("channel closed before a value arrived")
		}
		return value
	case <-time.After(timeout):
		t.Fatal("timed out waiting for a value")
	}
	return zero
}

// receiveAllUntilClosed returns the values left in ch and fails the test unless ch closes in time.
func receiveAllUntilClosed[T any](t *testing.T, ch <-chan T) []T {
	t.Helper()
	var values []T
	timeout := time.After(testTimeout)
	for {
		select {
		case value, ok := <-ch:
			if !ok {
				return values
			}
			values = append(values, value)
		case <-timeout:
			t.Fatal("channel did not close in time")
		}
	}
}

// requireNoTermination fails the test if done reports termination during the stall window.
func requireNoTermination(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		t.Fatalf("transport terminated while its consumer was stalled: %v", err)
	case <-time.After(stallWindow):
	}
}

// requireInOrder receives count values from ch and checks each one's raw JSON against want.
func requireInOrder[T any](t *testing.T, ch <-chan T, raw func(T) string, want func(int) string, count int) {
	t.Helper()
	for i := range count {
		if got := raw(receive(t, ch)); got != want(i) {
			t.Fatalf("value %d = %q, want %q", i, got, want(i))
		}
	}
}

func numberedLine(kind string, seq int) string {
	return fmt.Sprintf(`{"type":%q,"seq":%d}`, kind, seq)
}

// numberedLines returns backlogCount numbered lines of the given type, one per line.
func numberedLines(kind string) string {
	var builder strings.Builder
	for i := range backlogCount {
		builder.WriteString(numberedLine(kind, i))
		builder.WriteByte('\n')
	}
	return builder.String()
}

// paddedEvent returns one JSON object of exactly size bytes.
func paddedEvent(size int) string {
	prefix := `{"type":"assistant","pad":"`
	suffix := `"}`
	return prefix + strings.Repeat("x", size-len(prefix)-len(suffix)) + suffix
}

func userLine(text, uuid string) string {
	return `{"type":"user","message":{"role":"user","content":[{"type":"text","text":"` + text +
		`"}]},"uuid":"` + uuid + `"}` + "\n"
}

func eventRaw(message wire.SDKMessage) string {
	return string(message.RawJSON())
}

func controlRaw(message ControlMessage) string {
	return string(message.RawJSON())
}

func TestInboundLineOfMaxLineBytesArrivesWhole(t *testing.T) {
	t.Parallel()

	fake := newFakeProcess(t)
	transport := startTransport(t, fake)
	line := paddedEvent(MaxLineBytes)
	emit(fake, line+"\n")

	got := receiveWithin(t, transport.Events(), maxLineTimeout)
	if !bytes.Equal(got.RawJSON(), []byte(line)) {
		t.Fatalf("event has %d bytes, want %d intact bytes", len(got.RawJSON()), len(line))
	}
}

func TestInboundLineLongerThanMaxLineBytesEndsTransport(t *testing.T) {
	t.Parallel()

	fake := newFakeProcess(t)
	transport := startTransport(t, fake)
	emit(fake, paddedEvent(MaxLineBytes+1)+"\n")

	err := receive(t, transport.Done())
	if !errors.Is(err, ErrLineTooLong) {
		t.Fatalf("Done() error = %v, want ErrLineTooLong", err)
	}
	if events := receiveAllUntilClosed(t, transport.Events()); len(events) != 0 {
		t.Fatalf("Events() delivered %d messages after an oversized line, want none", len(events))
	}
}

func TestSendsWriteExactEnvelopes(t *testing.T) {
	t.Parallel()

	fake := newFakeProcess(t)
	transport := startTransport(t, fake, WithUUIDGenerator(func() (string, error) {
		return fixedUUIDOne, nil
	}))
	lines := stdinLines(fake, 4)

	uuid, err := transport.SendUserMessage("hello")
	if err != nil {
		t.Fatalf("SendUserMessage() error = %v", err)
	}
	if uuid != fixedUUIDOne {
		t.Fatalf("SendUserMessage() UUID = %q, want %q", uuid, fixedUUIDOne)
	}
	if err := transport.SendInterrupt(fixedUUIDTwo); err != nil {
		t.Fatalf("SendInterrupt() error = %v", err)
	}
	if err := transport.SendInitialize(fixedUUIDTwo, []string{"example_dialog"}); err != nil {
		t.Fatalf("SendInitialize() error = %v", err)
	}
	if err := transport.SendControlResponse(fixedUUIDTwo, json.RawMessage(`{"ok":true}`)); err != nil {
		t.Fatalf("SendControlResponse() error = %v", err)
	}

	want := []string{
		userLine("hello", fixedUUIDOne),
		`{"type":"control_request","request_id":"00000000-0000-4000-8000-000000000002",` +
			`"request":{"subtype":"interrupt"}}` + "\n",
		`{"type":"control_request","request_id":"00000000-0000-4000-8000-000000000002",` +
			`"request":{"subtype":"initialize","supportedDialogKinds":["example_dialog"],` +
			`"hooks":{},"systemPrompt":[]}}` + "\n",
		`{"type":"control_response","response":{"subtype":"success",` +
			`"request_id":"00000000-0000-4000-8000-000000000002","response":{"ok":true}}}` + "\n",
	}
	for i, expected := range want {
		if got := string(receive(t, lines)); got != expected {
			t.Fatalf("envelope %d = %q, want %q", i, got, expected)
		}
	}
}

func TestSendUserMessageNearMaxLineBytesReachesProcessWhole(t *testing.T) {
	t.Parallel()

	fake := newFakeProcess(t)
	transport := startTransport(t, fake, WithUUIDGenerator(func() (string, error) {
		return fixedUUIDOne, nil
	}))
	text := strings.Repeat("x", MaxLineBytes-1024)
	lines := stdinLines(fake, 1)

	if _, err := transport.SendUserMessage(text); err != nil {
		t.Fatalf("SendUserMessage() error = %v", err)
	}
	got := receive(t, lines)
	want := userLine(text, fixedUUIDOne)
	if !bytes.Equal(got, []byte(want)) {
		t.Fatalf("process received %d bytes, want %d intact bytes", len(got), len(want))
	}
}

func TestStalledEventConsumerLosesNothingAndErrorsNothing(t *testing.T) {
	t.Parallel()

	fake := newFakeProcess(t)
	transport := startTransport(t, fake, WithBufferSize(1))
	emit(fake, numberedLines("assistant"))

	requireNoTermination(t, transport.Done())
	requireInOrder(t, transport.Events(), eventRaw, func(i int) string {
		return numberedLine("assistant", i)
	}, backlogCount)
}

func TestStalledControlConsumerLosesNothingAndErrorsNothing(t *testing.T) {
	t.Parallel()

	fake := newFakeProcess(t)
	transport := startTransport(t, fake, WithBufferSize(1))
	emit(fake, numberedLines("control_response"))

	requireNoTermination(t, transport.Done())
	requireInOrder(t, transport.Controls(), controlRaw, func(i int) string {
		return numberedLine("control_response", i)
	}, backlogCount)
}

func TestStalledTransportDoesNotDelayAnotherTransport(t *testing.T) {
	t.Parallel()

	stalledFake := newFakeProcess(t)
	stalled := startTransport(t, stalledFake, WithBufferSize(1))
	emit(stalledFake, numberedLines("assistant"))
	requireNoTermination(t, stalled.Done())

	otherFake := newFakeProcess(t)
	other := startTransport(t, otherFake, WithBufferSize(1))
	emit(otherFake, numberedLine("assistant", 0)+"\n")
	if got := eventRaw(receive(t, other.Events())); got != numberedLine("assistant", 0) {
		t.Fatalf("other transport delivered %q while the first was stalled", got)
	}

	requireInOrder(t, stalled.Events(), eventRaw, func(i int) string {
		return numberedLine("assistant", i)
	}, backlogCount)
}

func TestCloseDuringStallReturnsAndClosesBothChannels(t *testing.T) {
	t.Parallel()

	fake := newFakeProcess(t)
	transport := startTransport(t, fake, WithBufferSize(1))
	emit(fake, numberedLines("assistant"))
	requireNoTermination(t, transport.Done())

	closed := make(chan error, 1)
	go func() {
		closed <- transport.Close()
	}()
	if err := receive(t, closed); err != nil {
		t.Fatalf("Close() error = %v, want nil", err)
	}

	receiveAllUntilClosed(t, transport.Events())
	receiveAllUntilClosed(t, transport.Controls())
	if err := receive(t, transport.Done()); !errors.Is(err, ErrTransportClosed) {
		t.Fatalf("Done() error = %v, want ErrTransportClosed", err)
	}
}

// TestStalledConsumerPausesReadingFromTheProcess writes far more than the pipe and the reader's
// buffer hold while nobody drains Events. The process's write cannot finish until the consumer
// resumes, which shows the transport stopped reading instead of queueing lines without bound.
func TestStalledConsumerPausesReadingFromTheProcess(t *testing.T) {
	t.Parallel()

	const lineCount = 1024
	pad := strings.Repeat("x", 4096)
	line := func(seq int) string {
		return fmt.Sprintf(`{"type":"assistant","seq":%d,"pad":%q}`, seq, pad)
	}
	var builder strings.Builder
	for i := range lineCount {
		builder.WriteString(line(i))
		builder.WriteByte('\n')
	}

	fake := newFakeProcess(t)
	transport := startTransport(t, fake, WithBufferSize(1))
	written := make(chan error, 1)
	go func() {
		_, err := io.WriteString(fake.stdoutWriter, builder.String())
		written <- err
	}()

	select {
	case err := <-written:
		t.Fatalf("process wrote all %d bytes while the consumer was stalled (error %v)", builder.Len(), err)
	case <-time.After(stallWindow):
	}
	requireInOrder(t, transport.Events(), eventRaw, line, lineCount)
	if err := receive(t, written); err != nil {
		t.Fatalf("process write after the consumer resumed: %v", err)
	}
}
