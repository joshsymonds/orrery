package transport

import (
	"bufio"
	"errors"
	"fmt"
	"io"
)

const (
	// MaxLineBytes is the largest JSON object accepted on one NDJSON line.
	MaxLineBytes = 16 << 20

	initialReadBufferSize = 64 << 10
)

var (
	// ErrLineTooLong marks an inbound NDJSON object larger than MaxLineBytes.
	ErrLineTooLong = errors.New("transport: NDJSON line exceeds limit")

	// ErrUnterminatedLine marks a final NDJSON object without a newline delimiter.
	ErrUnterminatedLine = errors.New("transport: unterminated NDJSON line")
)

// readJSONLine returns the next newline-delimited line without its newline.
// A line that fits in one buffer fill is returned as a slice of the reader's buffer,
// so the result is valid only until the next read from reader; a longer line is
// assembled in a fresh allocation.
func readJSONLine(reader *bufio.Reader) ([]byte, error) {
	var line []byte
	for len(line) < MaxLineBytes {
		fragment, err := reader.ReadSlice('\n')
		switch {
		case err == nil:
			return completeJSONLine(line, fragment)
		case errors.Is(err, bufio.ErrBufferFull):
			line, err = appendJSONLineFragment(line, fragment)
			if err != nil {
				return nil, err
			}
		default:
			return nil, classifyJSONLineReadError(err, len(line)+len(fragment))
		}
	}
	return readJSONLineDelimiter(reader, line)
}

func appendJSONLineFragment(line, fragment []byte) ([]byte, error) {
	if len(fragment) > MaxLineBytes-len(line) {
		return nil, ErrLineTooLong
	}
	return append(line, fragment...), nil
}

func completeJSONLine(line, fragment []byte) ([]byte, error) {
	fragment = fragment[:len(fragment)-1]
	if len(fragment) > MaxLineBytes-len(line) {
		return nil, ErrLineTooLong
	}
	if len(line) == 0 {
		return fragment, nil
	}
	return append(line, fragment...), nil
}

func classifyJSONLineReadError(err error, bytesRead int) error {
	if !errors.Is(err, io.EOF) {
		return fmt.Errorf("transport: read NDJSON line: %w", err)
	}
	if bytesRead == 0 {
		return io.EOF
	}
	if bytesRead > MaxLineBytes {
		return ErrLineTooLong
	}
	return ErrUnterminatedLine
}

func readJSONLineDelimiter(reader *bufio.Reader, line []byte) ([]byte, error) {
	delimiter, err := reader.ReadByte()
	if errors.Is(err, io.EOF) {
		return nil, ErrUnterminatedLine
	}
	if err != nil {
		return nil, fmt.Errorf("transport: read NDJSON line: %w", err)
	}
	if delimiter != '\n' {
		return nil, ErrLineTooLong
	}
	return line, nil
}
