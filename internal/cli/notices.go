package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
)

// errNoticesEnded is the error when the notices stream ends while stdin is open, or the socket stops taking lines.
var errNoticesEnded = errors.New("notices: stream ended")

// stdoutError is the error a write to stdout returned. Notices reports it apart from the stream ending.
type stdoutError struct {
	err error
}

// Error names the failed write to stdout.
func (e stdoutError) Error() string {
	return fmt.Sprintf("notices: write stdout: %v", e.err)
}

// Unwrap returns the write error.
func (e stdoutError) Unwrap() error {
	return e.err
}

// stdoutWriter writes to stdout and wraps a failed write in stdoutError.
type stdoutWriter struct {
	w io.Writer
}

// Write writes p to stdout.
func (s stdoutWriter) Write(p []byte) (int, error) {
	n, err := s.w.Write(p)
	if err != nil {
		return n, stdoutError{err: err}
	}
	return n, nil
}

// Notices implements `orrery notices`. It dials the notices socket at socketPath and relays between it and the process:
// every byte the socket sends goes to stdout, and every line from stdin goes to the socket. It opens no listener. When
// stdin ends it closes the connection and returns nil. When the socket's stream ends first it returns errNoticesEnded.
func Notices(ctx context.Context, socketPath string, args []string, stdin io.Reader, stdout io.Writer) (err error) {
	if len(args) > 0 {
		return usageError{msg: fmt.Sprintf("notices takes no arguments, got %q", args[0])}
	}
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "unix", socketPath)
	if err != nil {
		return fmt.Errorf("notices: orreryd serves no notices on %s", socketPath)
	}
	defer func() { err = errors.Join(err, conn.Close()) }()

	copyDone := make(chan error, 1)
	go func() {
		_, copyErr := io.Copy(stdoutWriter{w: stdout}, conn)
		copyDone <- copyErr
	}()
	stdinDone := make(chan error, 1)
	go func() {
		stdinDone <- relayLines(conn, stdin)
	}()

	select {
	case copyErr := <-copyDone:
		var writeErr stdoutError
		if errors.As(copyErr, &writeErr) {
			return copyErr
		}
		return errNoticesEnded
	case stdinErr := <-stdinDone:
		return stdinErr
	}
}

// relayLines writes each line of stdin to conn, newline included, until stdin ends. A stdin read failure returns that
// failure. A failed write means the socket has stopped taking lines, and returns errNoticesEnded.
func relayLines(conn io.Writer, stdin io.Reader) error {
	reader := bufio.NewReader(stdin)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			if _, writeErr := conn.Write(line); writeErr != nil {
				return errNoticesEnded
			}
		}
		switch {
		case errors.Is(err, io.EOF):
			return nil
		case err != nil:
			return fmt.Errorf("notices: read stdin: %w", err)
		}
	}
}
