package holder

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
)

// endlessLine yields 'x' bytes with no line end until limit bytes have been read; read counts them.
type endlessLine struct {
	limit int
	read  int
}

func (s *endlessLine) Read(p []byte) (int, error) {
	if s.read >= s.limit {
		return 0, io.EOF
	}
	n := min(len(p), s.limit-s.read)
	for i := range p[:n] {
		p[i] = 'x'
	}
	s.read += n
	return n, nil
}

// recordingWriter keeps the payload of each Write call.
type recordingWriter struct {
	payloads [][]byte
}

func (w *recordingWriter) Write(p []byte) (int, error) {
	w.payloads = append(w.payloads, bytes.Clone(p))
	return len(p), nil
}

// messageFrame returns an error frame whose encoded line, LF included, is exactly size bytes.
func messageFrame(t *testing.T, size int) Frame {
	t.Helper()
	probe, err := json.Marshal(Frame{Type: TypeError, Message: "x"})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return Frame{Type: TypeError, Message: strings.Repeat("x", size-len(probe))}
}

func TestFrameRoundTripKeepsEveryField(t *testing.T) {
	t.Parallel()

	want := Frame{
		Type:       TypeOut,
		Version:    WireVersion,
		Session:    "session-1",
		ReplayFrom: 1 << 40,
		Consumed:   2 << 40,
		Start:      3,
		End:        7,
		Written:    []string{"write-1", "write-2"},
		Offset:     9,
		Data:       []byte{0x00, 0xff, 'a', '\n', 0x7f},
		ID:         "write-3",
		Duplicate:  true,
		Exit: &Exit{
			Reason:    ReasonExited,
			Code:      -1,
			Signal:    "SIGKILL",
			StdoutEnd: 11,
			Stderr:    "tail \"quoted\"\n",
		},
		Message: "message",
	}
	var buf bytes.Buffer
	if err := WriteFrame(&buf, want); err != nil {
		t.Fatalf("WriteFrame() error = %v", err)
	}
	got, err := NewFrameReader(&buf).Read()
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Read() = %+v, want %+v", got, want)
	}
}

func TestHelloAndStopKeepTheirFrozenShape(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		frame Frame
		want  string
	}{
		{
			name:  "hello",
			frame: Frame{Type: TypeHello, Version: WireVersion, Session: "s"},
			want:  fmt.Sprintf(`{"type":"hello","version":%d,"session":"s"}`, WireVersion),
		},
		{
			name:  "stop",
			frame: Frame{Type: TypeStop},
			want:  `{"type":"stop"}`,
		},
		{
			name:  "stopped",
			frame: Frame{Type: TypeStopped},
			want:  `{"type":"stopped"}`,
		},
	}
	for _, tc := range cases {
		var buf bytes.Buffer
		if err := WriteFrame(&buf, tc.frame); err != nil {
			t.Fatalf("%s: WriteFrame() error = %v", tc.name, err)
		}
		if got := buf.String(); got != tc.want+"\n" {
			t.Fatalf("%s: WriteFrame() wrote %q, want %q", tc.name, got, tc.want+"\n")
		}
	}
}

func TestWriteFrameIssuesOneWriteCarryingTheLine(t *testing.T) {
	t.Parallel()

	writer := &recordingWriter{}
	if err := WriteFrame(writer, Frame{Type: TypeAck, Consumed: 5}); err != nil {
		t.Fatalf("WriteFrame() error = %v", err)
	}
	want := `{"type":"ack","consumed":5}` + "\n"
	if len(writer.payloads) != 1 || string(writer.payloads[0]) != want {
		t.Fatalf("WriteFrame() issued %q, want one Write of %q", writer.payloads, want)
	}
}

func TestFrameReaderIgnoresUnknownFields(t *testing.T) {
	t.Parallel()

	line := `{"type":"ack","consumed":7,"future":{"nested":[1,2]},"unknownFlag":true}` + "\n"
	got, err := NewFrameReader(strings.NewReader(line)).Read()
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	want := Frame{Type: TypeAck, Consumed: 7}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Read() = %+v, want %+v", got, want)
	}
}

func TestFrameReaderRejectsBadFrames(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"empty type":      `{"type":""}` + "\n",
		"missing type":    `{"version":1}` + "\n",
		"non-string type": `{"type":5}` + "\n",
		"invalid JSON":    `{"type":` + "\n",
		"empty line":      "\n",
		"unterminated":    `{"type":"ack"}`,
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := NewFrameReader(strings.NewReader(input)).Read()
			if !errors.Is(err, ErrBadFrame) {
				t.Fatalf("Read() error = %v, want ErrBadFrame", err)
			}
		})
	}
}

func TestFrameReaderReturnsEOFAtCleanEnd(t *testing.T) {
	t.Parallel()

	if _, err := NewFrameReader(strings.NewReader("")).Read(); !errors.Is(err, io.EOF) {
		t.Fatalf("Read() of empty stream error = %v, want io.EOF", err)
	}

	reader := NewFrameReader(strings.NewReader(`{"type":"stop"}` + "\n"))
	if _, err := reader.Read(); err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if _, err := reader.Read(); !errors.Is(err, io.EOF) {
		t.Fatalf("Read() after last frame error = %v, want io.EOF", err)
	}
}

func TestWriteFrameRefusesEmptyType(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	err := WriteFrame(&buf, Frame{Message: "no type"})
	if !errors.Is(err, ErrBadFrame) {
		t.Fatalf("WriteFrame() error = %v, want ErrBadFrame", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("WriteFrame() wrote %d bytes for a refused frame", buf.Len())
	}
}

func TestFrameLimitIncludesLFOnBothSides(t *testing.T) {
	t.Parallel()

	atLimit := messageFrame(t, MaxFrameBytes)
	var buf bytes.Buffer
	if err := WriteFrame(&buf, atLimit); err != nil {
		t.Fatalf("WriteFrame(frame of MaxFrameBytes) error = %v", err)
	}
	if buf.Len() != MaxFrameBytes {
		t.Fatalf("WriteFrame(frame of MaxFrameBytes) wrote %d bytes, want %d", buf.Len(), MaxFrameBytes)
	}
	got, err := NewFrameReader(&buf).Read()
	if err != nil {
		t.Fatalf("Read(frame of MaxFrameBytes) error = %v", err)
	}
	if len(got.Message) != len(atLimit.Message) {
		t.Fatalf("Read() message length = %d, want %d", len(got.Message), len(atLimit.Message))
	}

	overLimit := messageFrame(t, MaxFrameBytes+1)
	if err := WriteFrame(&buf, overLimit); !errors.Is(err, ErrFrameTooLong) {
		t.Fatalf("WriteFrame(frame of MaxFrameBytes+1) error = %v, want ErrFrameTooLong", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("WriteFrame(frame of MaxFrameBytes+1) wrote %d bytes", buf.Len())
	}

	raw, err := json.Marshal(overLimit)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	_, err = NewFrameReader(bytes.NewReader(append(raw, '\n'))).Read()
	if !errors.Is(err, ErrFrameTooLong) {
		t.Fatalf("Read(frame of MaxFrameBytes+1) error = %v, want ErrFrameTooLong", err)
	}
}

func TestFrameReaderStopsAtLimitWithoutLineEnd(t *testing.T) {
	t.Parallel()

	source := &endlessLine{limit: 2 * MaxFrameBytes}
	_, err := NewFrameReader(source).Read()
	if !errors.Is(err, ErrFrameTooLong) {
		t.Fatalf("Read() error = %v, want ErrFrameTooLong", err)
	}
	if source.read > MaxFrameBytes+ChunkBytes {
		t.Fatalf("Read() consumed %d bytes of an endless line, want at most %d", source.read, MaxFrameBytes+ChunkBytes)
	}
}
