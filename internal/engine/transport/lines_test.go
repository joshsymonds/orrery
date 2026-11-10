package transport

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

const (
	rawAssistantLine   = `{"type":"assistant","text":"plain SDK message"}`
	rawControlLine     = `{"type":"control_request","request_id":"00000000-0000-4000-8000-000000000003","request":{"subtype":"can_use_tool"}}`
	rawNotJSONLine     = `not json at all`
	rawNumericTypeLine = `{"type":5}`
	rawClosingLine     = `{"type":"result","text":"second SDK message"}`
)

func TestRawLinesDeliversEveryLineInOrderWithoutDecoding(t *testing.T) {
	t.Parallel()

	fake := newFakeProcess(t)
	transport := startTransport(t, fake, WithRawLines())
	emit(fake, strings.Join([]string{
		rawAssistantLine,
		rawControlLine,
		rawNotJSONLine,
		rawNumericTypeLine,
		rawClosingLine,
	}, "\n")+"\n")

	want := []string{rawAssistantLine, rawControlLine, rawNotJSONLine, rawNumericTypeLine, rawClosingLine}
	for i, expected := range want {
		if got := string(receive(t, transport.Lines())); got != expected {
			t.Fatalf("line %d = %q, want %q", i, got, expected)
		}
	}
	requireNoTermination(t, transport.Done())
}

// TestRawLinesManyShortLinesSurviveBufferRefill writes about 180 KiB of short lines, nearly three
// read buffers, and only then lets the consumer read. Each short line is returned as a view of the
// reader's buffer, which later fills overwrite, so a delivered line that is not a copy is corrupted.
func TestRawLinesManyShortLinesSurviveBufferRefill(t *testing.T) {
	t.Parallel()

	const count = 3000
	fake := newFakeProcess(t)
	transport := startTransport(t, fake, WithRawLines(), WithBufferSize(count))
	padding := strings.Repeat("p", 24)
	want := make([]string, count)
	var stream strings.Builder
	for i := range want {
		want[i] = fmt.Sprintf(`{"type":"assistant","seq":%d,"pad":"%s"}`, i, padding)
		stream.WriteString(want[i])
		stream.WriteByte('\n')
	}
	emit(fake, stream.String())

	deadline := time.Now().Add(testTimeout)
	for len(transport.Lines()) < count {
		if time.Now().After(deadline) {
			t.Fatalf("only %d of %d lines queued before the consumer started", len(transport.Lines()), count)
		}
		time.Sleep(time.Millisecond)
	}
	for i, expected := range want {
		if got := string(receive(t, transport.Lines())); got != expected {
			t.Fatalf("line %d = %q, want %q", i, got, expected)
		}
	}
	requireNoTermination(t, transport.Done())
}

func TestRawLinesLongerThanReadBufferArriveIntact(t *testing.T) {
	t.Parallel()

	fake := newFakeProcess(t)
	transport := startTransport(t, fake, WithRawLines())
	large := paddedEvent(100 << 10)
	small := paddedEvent(70 << 10)
	emit(fake, large+"\n"+small+"\n"+rawAssistantLine+"\n")

	if got := string(receiveWithin(t, transport.Lines(), maxLineTimeout)); got != large {
		t.Fatalf("first line has %d bytes, want %d intact bytes", len(got), len(large))
	}
	if got := string(receive(t, transport.Lines())); got != small {
		t.Fatalf("second line has %d bytes, want %d intact bytes", len(got), len(small))
	}
	if got := string(receive(t, transport.Lines())); got != rawAssistantLine {
		t.Fatalf("third line = %q, want %q", got, rawAssistantLine)
	}
}

func TestRawLinesCloseDuringStallReturnsAndClosesEveryChannel(t *testing.T) {
	t.Parallel()

	fake := newFakeProcess(t)
	transport := startTransport(t, fake, WithRawLines(), WithBufferSize(1))
	emit(fake, numberedLines("assistant"))
	requireNoTermination(t, transport.Done())

	closed := make(chan error, 1)
	go func() {
		closed <- transport.Close()
	}()
	if err := receive(t, closed); err != nil {
		t.Fatalf("Close() error = %v, want nil", err)
	}

	receiveAllUntilClosed(t, transport.Lines())
	receiveAllUntilClosed(t, transport.Events())
	receiveAllUntilClosed(t, transport.Controls())
	if err := receive(t, transport.Done()); !errors.Is(err, ErrTransportClosed) {
		t.Fatalf("Done() error = %v, want ErrTransportClosed", err)
	}
}

func TestRawLinesEndOfStdoutClosesLinesAndReportsReadError(t *testing.T) {
	t.Parallel()

	fake := newFakeProcess(t)
	transport := startTransport(t, fake, WithRawLines())
	emit(fake, rawControlLine+"\n")
	if got := string(receive(t, transport.Lines())); got != rawControlLine {
		t.Fatalf("line = %q, want %q", got, rawControlLine)
	}
	if err := fake.stdoutWriter.Close(); err != nil {
		t.Fatalf("close fake stdout: %v", err)
	}

	if rest := receiveAllUntilClosed(t, transport.Lines()); len(rest) != 0 {
		t.Fatalf("Lines() delivered %d lines after end of stdout, want none", len(rest))
	}
	if events := receiveAllUntilClosed(t, transport.Events()); len(events) != 0 {
		t.Fatalf("Events() delivered %d messages in raw mode, want none", len(events))
	}
	if controls := receiveAllUntilClosed(t, transport.Controls()); len(controls) != 0 {
		t.Fatalf("Controls() delivered %d messages in raw mode, want none", len(controls))
	}
	err := receive(t, transport.Done())
	var transportErr *TransportError
	if !errors.As(err, &transportErr) || transportErr.Operation != "read" {
		t.Fatalf("Done() error = %v, want a read TransportError", err)
	}
	if !errors.Is(err, errUnexpectedEOF) {
		t.Fatalf("Done() error = %v, want unexpected EOF", err)
	}
}
