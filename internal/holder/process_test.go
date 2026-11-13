package holder

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

const (
	// processSegmentBytes is the spool segment size the process tests run their holders with, so their output spans
	// several segment files.
	processSegmentBytes = 16 << 10
	// processReadSize is the buffer a test reads stdout through.
	processReadSize = 32 << 10
	// readOnceSize is how many bytes readOnce asks for, small enough that a test sees each frame's bytes in pieces.
	readOnceSize = 16
	// processUnread is how much stdout a child writes before a test writes to its stdin, without reading the stdout.
	processUnread = 5 << 20
)

var (
	// errHeldStop is what a fakeHeld's Stop returns when a test makes it fail.
	errHeldStop = errors.New("held process would not stop")
	// errHeldExit is what a fakeHeld's Wait returns when a test sets it.
	errHeldExit = errors.New("held process exited with status 3")
)

// processLike is the method set of session.Process. The holder package does not import session, so the test checks
// the Process against its own copy of that method set.
type processLike interface {
	Stdin() io.WriteCloser
	Stdout() io.ReadCloser
	Stderr() string
	Stop() error
	Wait() error
}

var _ processLike = (*Process)(nil)

// fakeHeld stands for the holder's own process. Its Done closes when the in-process Run returns, and its Stop cancels
// Run's context. Wait returns waitErr and Stderr returns stderr.
type fakeHeld struct {
	done    chan struct{}
	cancel  context.CancelFunc
	stderr  string
	stopErr error
	waitErr error
}

// newFakeHeld returns a fakeHeld whose Done stays open, for a test whose holder does not run in this process.
func newFakeHeld() *fakeHeld {
	return &fakeHeld{done: make(chan struct{})}
}

func (h *fakeHeld) Done() <-chan struct{} {
	return h.done
}

func (h *fakeHeld) Wait() error {
	<-h.done
	return h.waitErr
}

func (h *fakeHeld) Stderr() string {
	return h.stderr
}

// Stop cancels Run's context, which stops the holder's child, and returns stopErr.
func (h *fakeHeld) Stop() error {
	h.cancel()
	return h.stopErr
}

// startAsync runs fn in a goroutine and returns the channel its result arrives on.
func startAsync[T any](fn func() T) <-chan T {
	result := make(chan T, 1)
	go func() {
		result <- fn()
	}()
	return result
}

// awaitResult returns the next value on ch, failing the test when none arrives within waitArmedTimeout. what names
// the operation for the failure message.
func awaitResult[T any](t *testing.T, what string, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(waitArmedTimeout):
		t.Fatalf("%s did not return within %v", what, waitArmedTimeout)
		var zero T
		return zero
	}
}

// startHolderProcess runs a holder for cfg and attaches a controller to it at offset 0. The holder's own process is a
// fakeHeld, and the holder's run is returned with it along with the attached connection.
func startHolderProcess(t *testing.T, cfg Config) (*running, *fakeHeld, *Conn) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	run := startHolder(ctx, t, cfg)
	held := &fakeHeld{done: make(chan struct{}), cancel: cancel}
	go func() {
		<-run.done
		close(held.done)
	}()
	sock := socketOf(t, cfg.Dir)
	conn := awaitDial(t, run, func() (*Conn, error) { return Dial(t.Context(), sock) })
	if _, err := conn.Attach(0); err != nil {
		t.Fatalf("Attach(0): %v", err)
	}
	return run, held, conn
}

// newProcessConfig returns the holder config a process test starts from: the test's own holder dir and default child,
// with a spool whose segments are processSegmentBytes long.
func newProcessConfig(t *testing.T) Config {
	t.Helper()
	cfg := newHolderConfig(t)
	cfg.Spool = SpoolConfig{SegmentBytes: processSegmentBytes}
	return cfg
}

// shChild returns the child that runs script under sh in a new working directory.
func shChild(t *testing.T, script string) ChildConfig {
	t.Helper()
	return ChildConfig{Path: shPath(t), Args: []string{"-c", script}, Dir: t.TempDir()}
}

// gatedChild returns the child that waits for one line on its stdin, then runs script under sh. A test attaches to a
// gated child before it releases it, so the holder cannot end before the test is attached.
func gatedChild(t *testing.T, script string) ChildConfig {
	t.Helper()
	return shChild(t, "read line; "+script)
}

// release lets a gated child run, by writing the line it waits for to its stdin.
func release(t *testing.T, p *Process) {
	t.Helper()
	if got := awaitWrite(t, startWrite(p, []byte("go\n"))); got.err != nil {
		t.Fatalf("release the child: %v", got.err)
	}
}

// writeChildFile writes data to name in the child's working directory dir, for the child to copy to its stdout.
func writeChildFile(t *testing.T, dir, name string, data []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// awaitFile waits until the file at path exists, failing the test after waitArmedTimeout.
func awaitFile(t *testing.T, path string) {
	t.Helper()
	timeout := time.After(waitArmedTimeout)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		select {
		case <-timeout:
			t.Fatalf("%s did not appear within %v", path, waitArmedTimeout)
		case <-time.After(holderPoll):
		}
	}
}

// requireProcessGroupGone fails the test when any process of process group pgid is still alive.
func requireProcessGroupGone(t *testing.T, pgid int) {
	t.Helper()
	if err := syscall.Kill(-pgid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("process group %d: kill(0) = %v, want ESRCH", pgid, err)
	}
}

// requireHolderRemoved fails the test unless the holder dir dir is gone.
func requireHolderRemoved(t *testing.T, dir string) {
	t.Helper()
	if _, err := os.Lstat(dir); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("holder dir %s: err %v, want it removed", dir, err)
	}
}

// requireHexID fails the test unless id is the hex form of 16 bytes.
func requireHexID(t *testing.T, id string) {
	t.Helper()
	if raw, err := hex.DecodeString(id); err != nil || len(raw) != 16 {
		t.Fatalf("write id %q, want the hex form of 16 bytes", id)
	}
}

// segmentFiles returns the names of the spool's segment files in dir, in offset order.
func segmentFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir %s: %v", dir, err)
	}
	var names []string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), segmentPrefix) {
			names = append(names, entry.Name())
		}
	}
	return names
}

// awaitSegmentFiles waits until the spool's segment files in dir are exactly want, failing the test after
// waitArmedTimeout.
func awaitSegmentFiles(t *testing.T, dir string, want []string) {
	t.Helper()
	timeout := time.After(waitArmedTimeout)
	for {
		got := segmentFiles(t, dir)
		if slices.Equal(got, want) {
			return
		}
		select {
		case <-timeout:
			t.Fatalf("segment files %v, want %v within %v", got, want, waitArmedTimeout)
		case <-time.After(holderPoll):
		}
	}
}

// readResult is what a Read returned.
type readResult struct {
	data []byte
	err  error
}

// startRead makes one Read of up to size bytes from r in a goroutine, since a Read may block.
func startRead(r io.Reader, size int) <-chan readResult {
	return startAsync(func() readResult {
		buf := make([]byte, size)
		n, err := r.Read(buf)
		return readResult{data: buf[:n], err: err}
	})
}

// readOnce makes one Read of up to readOnceSize bytes from r.
func readOnce(t *testing.T, r io.Reader) readResult {
	t.Helper()
	return awaitResult(t, "Read", startRead(r, readOnceSize))
}

// readToEnd reads r until a Read returns an error, and returns everything read and that error.
func readToEnd(t *testing.T, r io.Reader) readResult {
	t.Helper()
	return awaitResult(t, "read to end", startAsync(func() readResult {
		var out []byte
		buf := make([]byte, processReadSize)
		for {
			n, err := r.Read(buf)
			out = append(out, buf[:n]...)
			if err != nil {
				return readResult{data: out, err: err}
			}
		}
	}))
}

// writeResult is what a Write returned.
type writeResult struct {
	n   int
	err error
}

// startWrite writes data to p's stdin in a goroutine, since a write waits for its answer.
func startWrite(p *Process, data []byte) <-chan writeResult {
	return startAsync(func() writeResult {
		n, err := p.Stdin().Write(data)
		return writeResult{n: n, err: err}
	})
}

// awaitWrite returns the result of a startWrite call, failing the test when none arrives within waitArmedTimeout.
func awaitWrite(t *testing.T, result <-chan writeResult) writeResult {
	t.Helper()
	return awaitResult(t, "Write", result)
}

// stopProcess stops p and fails the test unless Stop returns nil.
func stopProcess(t *testing.T, p *Process) {
	t.Helper()
	if err := awaitResult(t, "Stop", startAsync(p.Stop)); err != nil {
		t.Fatalf("Stop = %v, want nil", err)
	}
}

// rawProcess attaches a Process at offset 0 to a holder the test answers by hand. It returns the Process and the test's
// end of the connection.
func rawProcess(t *testing.T) (*Process, *client) {
	t.Helper()
	listener, sock := listenRaw(t)
	dialed := dialAsync(t, sock, WireVersion)
	server := acceptRaw(t, listener)
	if got := server.read(); got.Type != TypeHello {
		t.Fatalf("frame %+v, want the controller's hello", got)
	}
	server.send(Frame{Type: TypeHello, Version: WireVersion, Session: testSession})
	res := awaitDialResult(t, dialed)
	if res.err != nil {
		t.Fatalf("Dial: %v", res.err)
	}
	attached := startAsync(func() error {
		_, err := res.conn.Attach(0)
		return err
	})
	if got := server.read(); got.Type != TypeAttach {
		t.Fatalf("frame %+v, want an attach frame", got)
	}
	server.send(Frame{Type: TypeAttached})
	if err := awaitResult(t, "Attach", attached); err != nil {
		t.Fatalf("Attach(0): %v", err)
	}
	held := newFakeHeld()
	t.Cleanup(func() {
		close(held.done)
	})
	return newProcess(held, res.conn, sock, t.TempDir()), server
}

// requireAck fails the test unless the next frame the holder receives is an ack of consumed and replayFrom both at
// consumed.
func requireAck(t *testing.T, server *client, consumed int64) {
	t.Helper()
	got := server.read()
	if got.Type != TypeAck || got.Consumed != consumed || got.ReplayFrom != consumed {
		t.Fatalf("frame %+v, want an ack of %d", got, consumed)
	}
}

func TestProcessStdoutIsTheChildsOutputInOrderThenEOF(t *testing.T) {
	t.Parallel()

	cfg := newProcessConfig(t)
	want := spoolBytes(0, 200<<10)
	cfg.Child = gatedChild(t, "cat stdout.txt")
	writeChildFile(t, cfg.Child.Dir, "stdout.txt", want)
	_, held, conn := startHolderProcess(t, cfg)
	p := newProcess(held, conn, socketOf(t, cfg.Dir), cfg.Dir)
	release(t, p)

	got := readToEnd(t, p.Stdout())
	if !bytes.Equal(got.data, want) {
		t.Fatalf("stdout is %d bytes, ended by %v; want the child's %d bytes in order",
			len(got.data), got.err, len(want))
	}
	if !errors.Is(got.err, io.EOF) {
		t.Fatalf("read after the child's output = %v, want io.EOF", got.err)
	}
}

func TestProcessAckedOutputLeavesTheSpoolWhenItIsConsumed(t *testing.T) {
	t.Parallel()

	cfg := newProcessConfig(t)
	const size = 100 << 10
	// The child stays alive after its output, so the holder's directory is still in place when the segments are checked.
	cfg.Child = gatedChild(t, "cat stdout.txt; exec cat")
	writeChildFile(t, cfg.Child.Dir, "stdout.txt", spoolBytes(0, size))
	_, held, conn := startHolderProcess(t, cfg)
	p := newProcess(held, conn, socketOf(t, cfg.Dir), cfg.Dir)
	release(t, p)

	got := awaitResult(t, "read the output", startAsync(func() error {
		_, err := io.ReadFull(p.Stdout(), make([]byte, size))
		return err
	}))
	if got != nil {
		t.Fatalf("read the child's %d bytes = %v, want nil", size, got)
	}
	// Ack trims every segment that ends at or below the consumed offset, and never the segment still being written, so
	// the last segment is what remains.
	lastStart := int64(size) / processSegmentBytes * processSegmentBytes
	awaitSegmentFiles(t, cfg.Dir, []string{segmentName(lastStart)})
	stopProcess(t, p)
}

func TestProcessStdinReachesTheChildOnce(t *testing.T) {
	t.Parallel()

	cfg := newProcessConfig(t)
	cfg.Child = shChild(t, "exec cat")
	_, held, conn := startHolderProcess(t, cfg)
	p := newProcess(held, conn, socketOf(t, cfg.Dir), cfg.Dir)

	for _, line := range []string{"one\n", "two\n"} {
		if got := awaitWrite(t, startWrite(p, []byte(line))); got.err != nil || got.n != len(line) {
			t.Fatalf("Write(%q) = %d, %v; want %d, nil", line, got.n, got.err, len(line))
		}
	}
	echoed := make([]byte, len("one\ntwo\n"))
	readFull := awaitResult(t, "read the echo", startAsync(func() error {
		_, err := io.ReadFull(p.Stdout(), echoed)
		return err
	}))
	if readFull != nil || string(echoed) != "one\ntwo\n" {
		t.Fatalf("echo = %q, %v; want \"one\\ntwo\\n\", nil", echoed, readFull)
	}
	// Stopping the cat ends the stream, so anything it echoed beyond the eight bytes would show here.
	stopProcess(t, p)
	rest := readToEnd(t, p.Stdout())
	if len(rest.data) != 0 || !errors.Is(rest.err, io.EOF) {
		t.Fatalf("stdout after the echo = %q, %v; want nothing, then io.EOF", rest.data, rest.err)
	}
}

func TestProcessWriteToAChildWhoseStdinIsClosedReturnsAnError(t *testing.T) {
	t.Parallel()

	cfg := newProcessConfig(t)
	cfg.Child = shChild(t, "exec 0<&-; : > ready.flag; exec sleep 60")
	_, held, conn := startHolderProcess(t, cfg)
	p := newProcess(held, conn, socketOf(t, cfg.Dir), cfg.Dir)

	awaitFile(t, filepath.Join(cfg.Child.Dir, "ready.flag"))
	if got := awaitWrite(t, startWrite(p, []byte("x"))); got.err == nil {
		t.Fatalf("write to a child with a closed stdin = %d, nil; want an error", got.n)
	}
	stopProcess(t, p)
}

func TestProcessWriteCompletesWhileStdoutIsUnreadPastSeveralMiB(t *testing.T) {
	t.Parallel()

	cfg := newProcessConfig(t)
	cfg.Child = shChild(t, fmt.Sprintf("head -c %d /dev/zero; : > flushed.flag; exec cat", processUnread))
	_, held, conn := startHolderProcess(t, cfg)
	p := newProcess(held, conn, socketOf(t, cfg.Dir), cfg.Dir)

	awaitFile(t, filepath.Join(cfg.Child.Dir, "flushed.flag"))
	if got := awaitWrite(t, startWrite(p, []byte("ping\n"))); got.err != nil {
		t.Fatalf("write while %d bytes of stdout are unread = %v, want nil", processUnread, got.err)
	}
	stopProcess(t, p)
	got := readToEnd(t, p.Stdout())
	if len(got.data) < processUnread || !bytes.Equal(got.data[:processUnread], make([]byte, processUnread)) {
		t.Fatalf("stdout is %d bytes, ended by %v; want the %d zero bytes the child wrote first",
			len(got.data), got.err, processUnread)
	}
	if !errors.Is(got.err, io.EOF) {
		t.Fatalf("read after the stdout = %v, want io.EOF", got.err)
	}
}

func TestProcessStopEndsTheChildAndReturnsOnceTheHolderIsGone(t *testing.T) {
	t.Parallel()

	cfg := newProcessConfig(t)
	run, held, conn := startHolderProcess(t, cfg)
	p := newProcess(held, conn, socketOf(t, cfg.Dir), cfg.Dir)
	pid := awaitChildPid(t, cfg.Child.Dir)

	stopProcess(t, p)
	requireProcessGroupGone(t, pid)
	requireHolderRemoved(t, cfg.Dir)
	select {
	case <-run.done:
	default:
		t.Fatalf("holder still running after Stop returned")
	}
}

func TestProcessStopFallsBackToHeldStopWhenTheSocketIsGone(t *testing.T) {
	t.Parallel()

	cfg := newProcessConfig(t)
	run, held, conn := startHolderProcess(t, cfg)
	p := newProcess(held, conn, filepath.Join(cfg.Dir, "gone.sock"), cfg.Dir)
	pid := awaitChildPid(t, cfg.Child.Dir)

	stopProcess(t, p)
	requireProcessGroupGone(t, pid)
	requireHolderRemoved(t, cfg.Dir)
	select {
	case <-run.done:
	default:
		t.Fatalf("holder still running after Stop returned")
	}
}

func TestProcessStopReturnsAnErrorWhenNeitherStopReachesTheHolder(t *testing.T) {
	t.Parallel()

	cfg := newProcessConfig(t)
	_, held, conn := startHolderProcess(t, cfg)
	held.stopErr = errHeldStop
	p := newProcess(held, conn, filepath.Join(cfg.Dir, "gone.sock"), cfg.Dir)

	if err := awaitResult(t, "Stop", startAsync(p.Stop)); !errors.Is(err, errHeldStop) {
		t.Fatalf("Stop = %v, want an error carrying the held process's stop error", err)
	}
}

func TestProcessWaitReportsTheSignalThatEndedTheChild(t *testing.T) {
	t.Parallel()

	cfg := newProcessConfig(t)
	_, held, conn := startHolderProcess(t, cfg)
	p := newProcess(held, conn, socketOf(t, cfg.Dir), cfg.Dir)

	stopProcess(t, p)
	err := awaitResult(t, "Wait", startAsync(p.Wait))
	if err == nil || err.Error() != "signal: terminated" {
		t.Fatalf("Wait = %v, want signal: terminated", err)
	}
}

func TestProcessWaitReturnsTheHeldWaitErrorWhenTheChildExitsWithAStatus(t *testing.T) {
	t.Parallel()

	cfg := newProcessConfig(t)
	cfg.Child = gatedChild(t, "exit 3")
	_, held, conn := startHolderProcess(t, cfg)
	held.waitErr = errHeldExit
	p := newProcess(held, conn, socketOf(t, cfg.Dir), cfg.Dir)
	release(t, p)

	if err := awaitResult(t, "Wait", startAsync(p.Wait)); !errors.Is(err, errHeldExit) {
		t.Fatalf("Wait = %v, want the held process's wait error", err)
	}
}

func TestProcessStderrIsTheHoldersWhileTheChildRuns(t *testing.T) {
	t.Parallel()

	cfg := newProcessConfig(t)
	_, held, conn := startHolderProcess(t, cfg)
	held.stderr = "held says no"
	p := newProcess(held, conn, socketOf(t, cfg.Dir), cfg.Dir)

	if got := p.Stderr(); got != "held says no" {
		t.Fatalf("Stderr while the child runs = %q, want the holder's %q", got, "held says no")
	}
}

func TestProcessStderrIsTheExitTailOnceTheChildHasExited(t *testing.T) {
	t.Parallel()

	cfg := newProcessConfig(t)
	cfg.Child = gatedChild(t, exitTailChild)
	_, held, conn := startHolderProcess(t, cfg)
	p := newProcess(held, conn, socketOf(t, cfg.Dir), cfg.Dir)
	release(t, p)

	if got := readToEnd(t, p.Stdout()); string(got.data) != "out\n" || !errors.Is(got.err, io.EOF) {
		t.Fatalf("stdout = %q, %v; want \"out\\n\", then io.EOF", got.data, got.err)
	}
	if got := p.Stderr(); got != "err\n" {
		t.Fatalf("Stderr after the exit = %q, want the exit record's %q", got, "err\n")
	}
}

func TestProcessDetachLeavesTheHolderServingAndItsDirInPlace(t *testing.T) {
	t.Parallel()

	cfg := newProcessConfig(t)
	run, held, conn := startHolderProcess(t, cfg)
	sock := socketOf(t, cfg.Dir)
	p := newProcess(held, conn, sock, cfg.Dir)

	if err := p.Detach(); err != nil {
		t.Fatalf("Detach = %v, want nil", err)
	}
	if got := readOnce(t, p.Stdout()); len(got.data) != 0 || !errors.Is(got.err, io.EOF) {
		t.Fatalf("Read after Detach = %q, %v; want nothing, then io.EOF", got.data, got.err)
	}
	dialHolder(t, run, sock).hello(WireVersion)
	if _, err := os.Stat(cfg.Dir); err != nil {
		t.Fatalf("holder dir after Detach: %v, want it in place", err)
	}
	if err := p.Detach(); err != nil {
		t.Fatalf("second Detach = %v, want nil", err)
	}
	stopProcess(t, p)
	select {
	case <-run.done:
		t.Fatalf("holder ended after Detach and Stop, want it still serving")
	default:
	}
}

func TestProcessStdoutCloseWakesABlockedReadAndLaterReadsFail(t *testing.T) {
	t.Parallel()

	p, _ := rawProcess(t)
	blocked := startRead(p.Stdout(), 16)
	if err := p.Stdout().Close(); err != nil {
		t.Fatalf("Close = %v, want nil", err)
	}
	if got := awaitResult(t, "blocked Read", blocked); !errors.Is(got.err, os.ErrClosed) {
		t.Fatalf("blocked Read after Close = %v, want os.ErrClosed", got.err)
	}
	if got := readOnce(t, p.Stdout()); !errors.Is(got.err, os.ErrClosed) {
		t.Fatalf("later Read = %v, want os.ErrClosed", got.err)
	}
}

func TestProcessStdinCloseFailsLaterWrites(t *testing.T) {
	t.Parallel()

	p, _ := rawProcess(t)
	if err := p.Stdin().Close(); err != nil {
		t.Fatalf("Close = %v, want nil", err)
	}
	if got := awaitWrite(t, startWrite(p, []byte("x"))); !errors.Is(got.err, os.ErrClosed) {
		t.Fatalf("Write after Close = %d, %v; want os.ErrClosed", got.n, got.err)
	}
}

func TestProcessReadAcksWhatItConsumed(t *testing.T) {
	t.Parallel()

	p, server := rawProcess(t)
	server.send(Frame{Type: TypeOut, Offset: 0, Data: []byte("abc")})
	if got := readOnce(t, p.Stdout()); string(got.data) != "abc" || got.err != nil {
		t.Fatalf("first Read = %q, %v; want \"abc\", nil", got.data, got.err)
	}
	requireAck(t, server, 3)

	server.send(Frame{Type: TypeOut, Offset: 3, Data: []byte("de")})
	if got := readOnce(t, p.Stdout()); string(got.data) != "de" || got.err != nil {
		t.Fatalf("second Read = %q, %v; want \"de\", nil", got.data, got.err)
	}
	requireAck(t, server, 5)
}

func TestProcessOutOfOrderOutEndsTheStream(t *testing.T) {
	t.Parallel()

	p, server := rawProcess(t)
	server.send(Frame{Type: TypeOut, Offset: 0, Data: []byte("abc")})
	server.send(Frame{Type: TypeOut, Offset: 10, Data: []byte("xyz")})
	if got := readOnce(t, p.Stdout()); string(got.data) != "abc" || got.err != nil {
		t.Fatalf("Read before the gap = %q, %v; want \"abc\", nil", got.data, got.err)
	}
	if got := readOnce(t, p.Stdout()); got.err == nil || errors.Is(got.err, io.EOF) {
		t.Fatalf("Read after an out frame at offset 10 = %q, %v; want the stream's error", got.data, got.err)
	}
}

func TestProcessErrorWithoutIDEndsTheStreamWithItsMessage(t *testing.T) {
	t.Parallel()

	p, server := rawProcess(t)
	server.send(Frame{Type: TypeOut, Offset: 0, Data: []byte("abc")})
	server.send(Frame{Type: TypeError, Message: "holder: boom"})
	if got := readOnce(t, p.Stdout()); string(got.data) != "abc" || got.err != nil {
		t.Fatalf("Read before the error = %q, %v; want \"abc\", nil", got.data, got.err)
	}
	if got := readOnce(t, p.Stdout()); got.err == nil || got.err.Error() != "holder: boom" {
		t.Fatalf("Read after the error frame = %v, want the holder's message", got.err)
	}
}

func TestProcessWriteIsAnsweredByItsIDDuplicateCountsAsWrittenAndErrorFailsIt(t *testing.T) {
	t.Parallel()

	p, server := rawProcess(t)

	first := startWrite(p, []byte("one"))
	f1 := server.read()
	if f1.Type != TypeWrite || string(f1.Data) != "one" {
		t.Fatalf("frame %+v, want a write of one", f1)
	}
	requireHexID(t, f1.ID)
	server.send(Frame{Type: TypeWritten, ID: f1.ID})
	if got := awaitWrite(t, first); got.err != nil || got.n != len("one") {
		t.Fatalf("Write(one) = %d, %v; want 3, nil", got.n, got.err)
	}

	second := startWrite(p, []byte("two"))
	f2 := server.read()
	if f2.Type != TypeWrite || string(f2.Data) != "two" || f2.ID == f1.ID {
		t.Fatalf("frame %+v, want a write of two under an id other than %q", f2, f1.ID)
	}
	requireHexID(t, f2.ID)
	server.send(Frame{Type: TypeWritten, ID: f2.ID, Duplicate: true})
	if got := awaitWrite(t, second); got.err != nil || got.n != len("two") {
		t.Fatalf("Write(two) answered duplicate = %d, %v; want 3, nil", got.n, got.err)
	}

	third := startWrite(p, []byte("three"))
	f3 := server.read()
	if f3.Type != TypeWrite || string(f3.Data) != "three" {
		t.Fatalf("frame %+v, want a write of three", f3)
	}
	const refusal = "holder: write child stdin: broken pipe"
	server.send(Frame{Type: TypeError, ID: f3.ID, Message: refusal})
	if got := awaitWrite(t, third); got.err == nil || got.err.Error() != refusal {
		t.Fatalf("Write(three) refused = %d, %v; want the holder's %q", got.n, got.err, refusal)
	}
}
