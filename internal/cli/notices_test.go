package cli_test

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/cli"
	"github.com/joshsymonds/orrery/internal/deskstream"
)

// noticesWait bounds every wait on the fake socket, on stdout, or on the command, so a stalled command fails its test
// instead of hanging the run.
const noticesWait = 10 * time.Second

// noticesOutput collects what the command writes to stdout. Each write signals wrote without blocking, so a test waits
// for output without sleeping.
type noticesOutput struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	wrote chan struct{}
}

// newNoticesOutput returns an empty output.
func newNoticesOutput() *noticesOutput {
	return &noticesOutput{wrote: make(chan struct{}, 1)}
}

// Write appends p to the output and signals wrote.
func (o *noticesOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	n, err := o.buf.Write(p)
	o.mu.Unlock()
	select {
	case o.wrote <- struct{}{}:
	default:
	}
	return n, err
}

// String returns everything written so far.
func (o *noticesOutput) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.buf.String()
}

// waitFor returns once the output equals want, and fails the test when that does not happen within noticesWait.
func (o *noticesOutput) waitFor(t *testing.T, want string) {
	t.Helper()
	deadline := time.After(noticesWait)
	for {
		if got := o.String(); got == want {
			return
		}
		select {
		case <-o.wrote:
		case <-deadline:
			t.Fatalf("stdout = %q, want %q", o.String(), want)
		}
	}
}

// noticesCall is one Notices call running on its own goroutine.
type noticesCall struct {
	done chan error
}

// startNotices runs cli.Notices on its own goroutine with the given stdin and stdout.
func startNotices(t *testing.T, socketPath string, stdin io.Reader, stdout io.Writer) noticesCall {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		done <- cli.Notices(t.Context(), socketPath, nil, stdin, stdout)
	}()
	return noticesCall{done: done}
}

// wait returns the error Notices returned, and fails the test when it does not return within noticesWait.
func (c noticesCall) wait(t *testing.T) error {
	t.Helper()
	select {
	case err := <-c.done:
		return err
	case <-time.After(noticesWait):
		t.Fatal("notices did not return")
		return nil
	}
}

// listenNotices listens on the notices socket that a daemon socket in a new temporary directory names. It returns the
// notices path and the listener, which is closed at cleanup.
func listenNotices(t *testing.T) (string, net.Listener) {
	t.Helper()
	path := deskstream.SocketPath(filepath.Join(t.TempDir(), "orreryd.sock"))
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen on %s: %v", path, err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return path, ln
}

// TestNoticesWritesSocketBytesToStdout checks that the socket's bytes reach stdout unchanged and in order, and that
// the socket closing first returns the stream-ended error. Stdin stays open, so the socket is the side that ends.
func TestNoticesWritesSocketBytesToStdout(t *testing.T) {
	path, ln := listenNotices(t)
	const stream = "{\"kind\":\"hello\",\"host\":\"gnomon\"}\n{\"kind\":\"notice\",\"title\":\"a · b:1\"}\n"

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		_, _ = io.WriteString(conn, stream)
		_ = conn.Close()
	}()

	stdin, pw := io.Pipe()
	t.Cleanup(func() { _ = pw.Close() })
	stdout := newNoticesOutput()
	err := startNotices(t, path, stdin, stdout).wait(t)

	if err == nil || err.Error() != "notices: stream ended" {
		t.Fatalf("err = %v, want notices: stream ended", err)
	}
	if got := stdout.String(); got != stream {
		t.Fatalf("stdout = %q, want %q", got, stream)
	}
}

// TestNoticesRelaysBothWays checks that a stdin line reaches the socket while the stream is open, that the socket's
// later bytes reach stdout, and that stdin's end closes the connection and returns nil.
func TestNoticesRelaysBothWays(t *testing.T) {
	path, ln := listenNotices(t)
	const activity = "{\"input\":1700000000}\n"
	const hello = "{\"kind\":\"hello\",\"host\":\"gnomon\"}\n"
	const notice = "{\"kind\":\"notice\",\"title\":\"a · b:1\"}\n"

	received := make(chan string, 1)
	closed := make(chan string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		_, _ = io.WriteString(conn, hello)
		line, err := bufio.NewReader(conn).ReadString('\n')
		if err != nil {
			received <- ""
			return
		}
		received <- line
		_, _ = io.WriteString(conn, notice)
		rest, _ := io.ReadAll(conn)
		closed <- string(rest)
	}()

	stdin, pw := io.Pipe()
	t.Cleanup(func() { _ = pw.Close() })
	stdout := newNoticesOutput()
	call := startNotices(t, path, stdin, stdout)

	written := make(chan error, 1)
	go func() {
		_, err := io.WriteString(pw, activity)
		written <- err
	}()
	select {
	case err := <-written:
		if err != nil {
			t.Fatalf("write stdin: %v", err)
		}
	case <-time.After(noticesWait):
		t.Fatal("stdin line was not read")
	}
	select {
	case got := <-received:
		if got != activity {
			t.Fatalf("socket received %q, want %q", got, activity)
		}
	case <-time.After(noticesWait):
		t.Fatal("socket received no stdin line")
	}
	stdout.waitFor(t, hello+notice)

	if err := pw.Close(); err != nil {
		t.Fatalf("close stdin: %v", err)
	}
	if err := call.wait(t); err != nil {
		t.Fatalf("err = %v, want nil after stdin ends", err)
	}
	select {
	case rest := <-closed:
		if rest != "" {
			t.Fatalf("socket read %q after stdin ended, want EOF", rest)
		}
	case <-time.After(noticesWait):
		t.Fatal("socket was not closed after stdin ended")
	}
}

// TestNoticesStdinLinesReachSocket checks that every stdin line is written to the socket, in order, and that stdin
// ending closes the connection, which the socket sees as EOF.
func TestNoticesStdinLinesReachSocket(t *testing.T) {
	path, ln := listenNotices(t)
	const lines = "{\"input\":1}\n{\"input\":2}\n"

	got := make(chan string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		data, _ := io.ReadAll(conn)
		got <- string(data)
	}()

	err := startNotices(t, path, strings.NewReader(lines), newNoticesOutput()).wait(t)
	if err != nil {
		t.Fatalf("err = %v, want nil after stdin ends", err)
	}
	select {
	case data := <-got:
		if data != lines {
			t.Fatalf("socket received %q, want %q", data, lines)
		}
	case <-time.After(noticesWait):
		t.Fatal("socket received no data")
	}
}

// TestNoticesDialFailureNamesSocket checks that a notices socket nothing listens on fails with the dial message
// naming that path.
func TestNoticesDialFailureNamesSocket(t *testing.T) {
	path := deskstream.SocketPath(filepath.Join(t.TempDir(), "orreryd.sock"))
	err := startNotices(t, path, strings.NewReader(""), newNoticesOutput()).wait(t)
	want := "notices: orreryd serves no notices on " + path
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %q", err, want)
	}
}

// TestNoticesRejectsArguments checks that an argument is a usage error, refused before the socket is dialed.
func TestNoticesRejectsArguments(t *testing.T) {
	err := cli.Notices(t.Context(), "", []string{"extra"}, strings.NewReader(""), newNoticesOutput())
	if !errors.Is(err, cli.ErrUsage) {
		t.Fatalf("err = %v, want a usage error", err)
	}
}
