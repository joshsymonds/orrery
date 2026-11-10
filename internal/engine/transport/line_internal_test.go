package transport

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

var errReadFailed = errors.New("read failed")

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) {
	return 0, errReadFailed
}

func newLineReader(src io.Reader) *bufio.Reader {
	return bufio.NewReaderSize(src, initialReadBufferSize)
}

func TestReadJSONLineReturnsLinesInOrderWithoutNewlines(t *testing.T) {
	t.Parallel()

	reader := newLineReader(strings.NewReader("{\"a\":1}\n{\"b\":2}\n\n{\"c\":3}\n"))
	want := []string{`{"a":1}`, `{"b":2}`, ``, `{"c":3}`}
	for _, expected := range want {
		line, err := readJSONLine(reader)
		if err != nil {
			t.Fatalf("readJSONLine() error = %v, want line %q", err, expected)
		}
		if string(line) != expected {
			t.Fatalf("readJSONLine() = %q, want %q", line, expected)
		}
	}
	if _, err := readJSONLine(reader); !errors.Is(err, io.EOF) {
		t.Fatalf("readJSONLine() after last line error = %v, want io.EOF", err)
	}
}

func TestReadJSONLineAcceptsLineOfExactlyMaxLineBytes(t *testing.T) {
	t.Parallel()

	payload := bytes.Repeat([]byte("x"), MaxLineBytes)
	src := append(append([]byte{}, payload...), '\n')
	line, err := readJSONLine(newLineReader(bytes.NewReader(src)))
	if err != nil {
		t.Fatalf("readJSONLine() error = %v, want nil", err)
	}
	if !bytes.Equal(line, payload) {
		t.Fatalf("readJSONLine() returned %d bytes, want %d intact bytes", len(line), len(payload))
	}
}

func TestReadJSONLineRejectsLineOfMaxLineBytesPlusOne(t *testing.T) {
	t.Parallel()

	src := append(bytes.Repeat([]byte("x"), MaxLineBytes+1), '\n')
	_, err := readJSONLine(newLineReader(bytes.NewReader(src)))
	if !errors.Is(err, ErrLineTooLong) {
		t.Fatalf("readJSONLine() error = %v, want ErrLineTooLong", err)
	}
}

func TestReadJSONLineRejectsFinalLineWithoutNewline(t *testing.T) {
	t.Parallel()

	_, err := readJSONLine(newLineReader(strings.NewReader(`{"partial":true}`)))
	if !errors.Is(err, ErrUnterminatedLine) {
		t.Fatalf("readJSONLine() error = %v, want ErrUnterminatedLine", err)
	}
}

func TestReadJSONLineReturnsEOFOnEmptyStream(t *testing.T) {
	t.Parallel()

	_, err := readJSONLine(newLineReader(strings.NewReader("")))
	if !errors.Is(err, io.EOF) {
		t.Fatalf("readJSONLine() error = %v, want io.EOF", err)
	}
}

func TestReadJSONLineWrapsReadErrors(t *testing.T) {
	t.Parallel()

	_, err := readJSONLine(newLineReader(failingReader{}))
	if !errors.Is(err, errReadFailed) {
		t.Fatalf("readJSONLine() error = %v, want it to wrap the read failure", err)
	}
}
