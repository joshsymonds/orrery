package holder

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

const (
	// testSession is the session every holder test serves.
	testSession = "s"
	// fakePidName is the file the fake child writes its pid to in its working directory.
	fakePidName = "fakeclaude.pid"
	// unknownFrameType is a frame type no holder serves.
	unknownFrameType = "bogus"
	// holderPoll is how often a test checks for something a holder does on its own.
	holderPoll = 10 * time.Millisecond
	// socketTestMode and dirTestMode are the modes the contract requires of the socket and its directory.
	socketTestMode os.FileMode = 0o600
	dirTestMode    os.FileMode = 0o700
	// exitTailChild is the child that writes one stdout line and one stderr line, then exits with status 3.
	exitTailChild = "echo out; echo err >&2; exit 3"
)

// running is one Run in flight. Its result is valid once done closes.
type running struct {
	done chan struct{}
	err  error
}

// startHolder runs Run for cfg in a goroutine. ctx must derive from t.Context(), which ends the holder before the
// test's cleanups run, so the cleanup only waits for Run to return.
func startHolder(ctx context.Context, t *testing.T, cfg Config) *running {
	t.Helper()
	run := &running{done: make(chan struct{})}
	go func() {
		run.err = Run(ctx, cfg)
		close(run.done)
	}()
	t.Cleanup(func() {
		select {
		case <-run.done:
		case <-time.After(waitArmedTimeout):
			t.Errorf("holder still running %v after its test ended", waitArmedTimeout)
		}
	})
	return run
}

// wait returns Run's error once Run has returned, failing the test after waitArmedTimeout.
func (run *running) wait(t *testing.T) error {
	t.Helper()
	select {
	case <-run.done:
		return run.err
	case <-time.After(waitArmedTimeout):
		t.Fatalf("holder did not return within %v", waitArmedTimeout)
		return nil
	}
}

// shortRoot creates a temporary directory whose path is short enough for a socket path. A t.TempDir path carries the
// test name and the TMPDIR prefix, and can exceed sun_path's limit, so holder dirs go under this root instead. The
// caller removes the root.
func shortRoot() (string, error) {
	root, err := os.MkdirTemp("", "hold")
	if err != nil {
		return "", fmt.Errorf("create short root: %w", err)
	}
	return root, nil
}

// newHolderConfig returns a Config for one session whose holder dir sits under a short root. The root is removed when
// the test ends, after its holder has returned.
func newHolderConfig(t *testing.T) Config {
	t.Helper()
	root, err := shortRoot()
	if err != nil {
		t.Fatalf("%v", err)
	}
	t.Cleanup(func() {
		if rmErr := os.RemoveAll(root); rmErr != nil {
			t.Errorf("remove %s: %v", root, rmErr)
		}
	})
	dir, err := SessionDir(root, testSession)
	if err != nil {
		t.Fatalf("SessionDir: %v", err)
	}
	return Config{
		Dir:     dir,
		Session: testSession,
		Child:   ChildConfig{Path: fakeClaudePath(t), Dir: t.TempDir()},
	}
}

// socketOf returns the socket path inside the holder dir dir.
func socketOf(t *testing.T, dir string) string {
	t.Helper()
	sock, err := SocketPath(dir)
	if err != nil {
		t.Fatalf("SocketPath: %v", err)
	}
	return sock
}

// shPath returns the absolute path of sh.
func shPath(t *testing.T) string {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Fatalf("find sh: %v", err)
	}
	return sh
}

// client is the test's end of one connection to a holder.
type client struct {
	t      *testing.T
	conn   *net.UnixConn
	frames *FrameReader
}

// dialHolder connects to the holder's socket at sock. The holder binds the socket a moment after Run starts, so dialing
// retries until the socket accepts. It fails at once if Run returns first.
func dialHolder(t *testing.T, run *running, sock string) *client {
	t.Helper()
	timeout := time.After(waitArmedTimeout)
	for {
		conn, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: sock, Net: "unix"})
		if err == nil {
			t.Cleanup(func() {
				if cerr := conn.Close(); cerr != nil {
					t.Errorf("close client connection: %v", cerr)
				}
			})
			return &client{t: t, conn: conn, frames: NewFrameReader(conn)}
		}
		select {
		case <-run.done:
			t.Fatalf("holder returned %v before its socket accepted", run.err)
		case <-timeout:
			t.Fatalf("socket %s did not accept within %v: %v", sock, waitArmedTimeout, err)
		case <-time.After(holderPoll):
		}
	}
}

// send writes frame to the holder.
func (c *client) send(frame Frame) {
	c.t.Helper()
	if err := WriteFrame(c.conn, frame); err != nil {
		c.t.Fatalf("send %s frame: %v", frame.Type, err)
	}
}

// read returns the next frame the holder sends, failing the test when none arrives within waitArmedTimeout.
func (c *client) read() Frame {
	c.t.Helper()
	c.setDeadline()
	frame, err := c.frames.Read()
	if err != nil {
		c.t.Fatalf("read frame: %v", err)
	}
	return frame
}

// expectClosed fails unless the holder closes the connection with no further frame.
func (c *client) expectClosed() {
	c.t.Helper()
	c.setDeadline()
	frame, err := c.frames.Read()
	if !errors.Is(err, io.EOF) {
		c.t.Fatalf("read after the connection: frame %+v, err %v; want a close with no frame", frame, err)
	}
}

// hello sends hello from a controller at version and returns the holder's answer, which must be a hello.
func (c *client) hello(version int) Frame {
	c.t.Helper()
	c.send(Frame{Type: TypeHello, Version: version})
	answer := c.read()
	if answer.Type != TypeHello {
		c.t.Fatalf("answer to hello = %+v, want a hello", answer)
	}
	return answer
}

// setDeadline bounds the next read so a holder that never answers fails the test instead of hanging it.
func (c *client) setDeadline() {
	c.t.Helper()
	if err := c.conn.SetReadDeadline(time.Now().Add(waitArmedTimeout)); err != nil {
		c.t.Fatalf("set read deadline: %v", err)
	}
}

// awaitChildPid waits for the fake child to write its pid into childDir and returns it. The fake renames the file into
// place, so a read sees the whole pid.
func awaitChildPid(t *testing.T, childDir string) int {
	t.Helper()
	name := filepath.Join(childDir, fakePidName)
	timeout := time.After(waitArmedTimeout)
	for {
		if _, err := os.Stat(name); err == nil {
			return readChildPid(t, name)
		}
		select {
		case <-timeout:
			t.Fatalf("fake child wrote no pid to %s within %v", name, waitArmedTimeout)
		case <-time.After(holderPoll):
		}
	}
}

// readExitFile returns the exit record in the holder dir dir.
func readExitFile(t *testing.T, dir string) Exit {
	t.Helper()
	return readExitRecord(t, filepath.Join(dir, ExitName))
}

// requireNoSocket fails when the socket file sock exists.
func requireNoSocket(t *testing.T, sock string) {
	t.Helper()
	if _, err := os.Lstat(sock); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("socket %s: err %v, want it removed", sock, err)
	}
}

// requireChildGroupAlive fails unless process group pgid still has a process.
func requireChildGroupAlive(t *testing.T, pgid int) {
	t.Helper()
	if err := syscall.Kill(-pgid, 0); err != nil {
		t.Fatalf("process group %d: kill(0) = %v, want it alive", pgid, err)
	}
}

// awaitLockFree fails unless the session lock in dir becomes free to take within waitArmedTimeout. A process forked
// while the lock is open holds a copy of its descriptor until that process execs, so a parallel test's fork can keep
// the lock busy for a moment after the holder has closed it. A lock the holder never released stays busy past the
// timeout.
func awaitLockFree(t *testing.T, dir string) {
	t.Helper()
	path := filepath.Join(dir, LockName)
	timeout := time.After(waitArmedTimeout)
	for {
		lock, err := Lock(path)
		if err == nil {
			if cerr := lock.Close(); cerr != nil {
				t.Fatalf("close session lock: %v", cerr)
			}
			return
		}
		select {
		case <-timeout:
			t.Fatalf("session lock still held after %v: %v", waitArmedTimeout, err)
		case <-time.After(holderPoll):
		}
	}
}

func TestHolderHelloAnswersWireVersionAndSessionToAnyController(t *testing.T) {
	t.Parallel()

	cfg := newHolderConfig(t)
	run := startHolder(t.Context(), t, cfg)
	sock := socketOf(t, cfg.Dir)
	for _, controllerVersion := range []int{0, WireVersion + 1} {
		conn := dialHolder(t, run, sock)
		answer := conn.hello(controllerVersion)
		if answer.Version != WireVersion || answer.Session != testSession {
			t.Errorf("hello from a controller at version %d answered version %d session %q, want version %d session %q",
				controllerVersion, answer.Version, answer.Session, WireVersion, testSession)
		}
	}
}

func TestHolderStopEndsChildGroupThenWritesExitAndAnswersStopped(t *testing.T) {
	t.Parallel()

	cfg := newHolderConfig(t)
	run := startHolder(t.Context(), t, cfg)
	sock := socketOf(t, cfg.Dir)
	conn := dialHolder(t, run, sock)
	conn.hello(WireVersion + 1)
	pid := awaitChildPid(t, cfg.Child.Dir)

	conn.send(Frame{Type: TypeStop})
	if got := conn.read(); got.Type != TypeStopped {
		t.Fatalf("answer to stop = %+v, want a stopped frame", got)
	}
	// stopped is sent only once the exit file is written, so it is readable before Run returns.
	exit := readExitFile(t, cfg.Dir)
	if err := run.wait(t); err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}
	if exit.Reason != ReasonStopped || exit.Code != -1 || exit.Signal != childSignalTerm {
		t.Errorf("exit = %+v, want reason %q, code -1, signal %q", exit, ReasonStopped, childSignalTerm)
	}
	requireChildGroupGone(t, pid)
	requireNoSocket(t, sock)
	assertDirNames(t, cfg.Dir, ExitName, LockName, segmentName(0))
}

func TestHolderChildThatExitsAloneWritesItsStatusAndTailThenReturnsNil(t *testing.T) {
	t.Parallel()

	cfg := newHolderConfig(t)
	cfg.Child = ChildConfig{Path: shPath(t), Args: []string{"-c", exitTailChild}, Dir: t.TempDir()}
	run := startHolder(t.Context(), t, cfg)
	if err := run.wait(t); err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}
	want := Exit{Reason: ReasonExited, Code: childDiedCode, StdoutEnd: int64(len("out\n")), Stderr: "err"}
	if got := readExitFile(t, cfg.Dir); got != want {
		t.Errorf("exit = %+v, want %+v", got, want)
	}
	requireNoSocket(t, socketOf(t, cfg.Dir))
	assertDirNames(t, cfg.Dir, ExitName, LockName, segmentName(0))
}

func TestHolderSecondHolderIsLockedOutAndFirstKeepsServing(t *testing.T) {
	t.Parallel()

	cfg := newHolderConfig(t)
	run := startHolder(t.Context(), t, cfg)
	sock := socketOf(t, cfg.Dir)
	dialHolder(t, run, sock).hello(WireVersion)
	pid := awaitChildPid(t, cfg.Child.Dir)

	if err := Run(t.Context(), cfg); !errors.Is(err, ErrLocked) {
		t.Fatalf("second Run = %v, want ErrLocked", err)
	}
	requireChildGroupAlive(t, pid)
	answer := dialHolder(t, run, sock).hello(WireVersion)
	if answer.Session != testSession {
		t.Errorf("first holder answered session %q after the second Run, want %q", answer.Session, testSession)
	}
}

func TestHolderLeftoverExitFileIsUndrainedAndStartsNothing(t *testing.T) {
	t.Parallel()

	cfg := newHolderConfig(t)
	if err := PrepareDir(cfg.Dir); err != nil {
		t.Fatalf("PrepareDir: %v", err)
	}
	if err := WriteExit(filepath.Join(cfg.Dir, ExitName), Exit{Reason: ReasonExited, Code: 0}); err != nil {
		t.Fatalf("WriteExit: %v", err)
	}

	if err := Run(t.Context(), cfg); !errors.Is(err, ErrUndrained) {
		t.Fatalf("Run = %v, want ErrUndrained", err)
	}
	requireNoSocket(t, socketOf(t, cfg.Dir))
	assertDirNames(t, cfg.Child.Dir)
	awaitLockFree(t, cfg.Dir)
}

func TestHolderSocketIs0600InA0700Dir(t *testing.T) {
	t.Parallel()

	cfg := newHolderConfig(t)
	run := startHolder(t.Context(), t, cfg)
	sock := socketOf(t, cfg.Dir)
	// The socket is bound before it is chmodded, so the mode is checked only once a hello shows Run has finished opening.
	dialHolder(t, run, sock).hello(WireVersion)

	assertMode(t, sock, socketTestMode)
	assertMode(t, cfg.Dir, dirTestMode)
}

func TestHolderRefusesAPeerOfAnotherUserWithNoFrame(t *testing.T) {
	t.Parallel()

	cfg := newHolderConfig(t)
	cfg.peerUID = func(*net.UnixConn) (int, error) { return os.Getuid() + 1, nil }
	run := startHolder(t.Context(), t, cfg)
	// The peer sends nothing. A holder that served it would wait for a hello, so the read would time out, not see a close.
	dialHolder(t, run, socketOf(t, cfg.Dir)).expectClosed()
}

func TestHolderContextEndStopsChildWithReasonStopped(t *testing.T) {
	t.Parallel()

	cfg := newHolderConfig(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	run := startHolder(ctx, t, cfg)
	sock := socketOf(t, cfg.Dir)
	dialHolder(t, run, sock).hello(WireVersion)
	pid := awaitChildPid(t, cfg.Child.Dir)

	cancel()
	if err := run.wait(t); err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}
	exit := readExitFile(t, cfg.Dir)
	if exit.Reason != ReasonStopped || exit.Signal != childSignalTerm {
		t.Errorf("exit = %+v, want reason %q, signal %q", exit, ReasonStopped, childSignalTerm)
	}
	requireChildGroupGone(t, pid)
	requireNoSocket(t, sock)
}

func TestHolderAnswersAnUnknownFrameWithErrorThenCloses(t *testing.T) {
	t.Parallel()

	cfg := newHolderConfig(t)
	run := startHolder(t.Context(), t, cfg)
	conn := dialHolder(t, run, socketOf(t, cfg.Dir))
	conn.hello(WireVersion)

	conn.send(Frame{Type: unknownFrameType})
	if got := conn.read(); got.Type != TypeError || got.Message == "" {
		t.Fatalf("answer to an unknown frame = %+v, want an error frame with a message", got)
	}
	conn.expectClosed()
}

func TestHolderClosesAConnectionWhoseFirstFrameIsNotHello(t *testing.T) {
	t.Parallel()

	cfg := newHolderConfig(t)
	run := startHolder(t.Context(), t, cfg)
	conn := dialHolder(t, run, socketOf(t, cfg.Dir))

	// A stop before hello must close the connection with no answer, so the holder never acts on it.
	conn.send(Frame{Type: TypeStop})
	conn.expectClosed()
}

func TestHolderReplacesAStaleSocketFile(t *testing.T) {
	t.Parallel()

	cfg := newHolderConfig(t)
	if err := PrepareDir(cfg.Dir); err != nil {
		t.Fatalf("PrepareDir: %v", err)
	}
	sock := socketOf(t, cfg.Dir)
	if err := os.WriteFile(sock, nil, socketTestMode); err != nil {
		t.Fatalf("WriteFile stale socket: %v", err)
	}

	run := startHolder(t.Context(), t, cfg)
	dialHolder(t, run, sock).hello(WireVersion)
}

func TestHolderChildThatCannotStartWritesExitAndReleasesEverything(t *testing.T) {
	t.Parallel()

	cfg := newHolderConfig(t)
	missing := filepath.Join(cfg.Child.Dir, "missing")
	cfg.Child.Path = missing

	if err := Run(t.Context(), cfg); err == nil {
		t.Fatal("Run = nil, want the start failure")
	}
	exit := readExitFile(t, cfg.Dir)
	if exit.Reason != ReasonExited || exit.Code != -1 || !strings.Contains(exit.Stderr, missing) {
		t.Errorf("exit = %+v, want reason %q, code -1, stderr naming %s", exit, ReasonExited, missing)
	}
	requireNoSocket(t, socketOf(t, cfg.Dir))
	awaitLockFree(t, cfg.Dir)
	assertDirNames(t, cfg.Dir, ExitName, LockName, segmentName(0))
}

func TestHolderRefusesLeftoverSpoolSegmentsAndReleasesEverything(t *testing.T) {
	t.Parallel()

	cfg := newHolderConfig(t)
	if err := PrepareDir(cfg.Dir); err != nil {
		t.Fatalf("PrepareDir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cfg.Dir, segmentName(0)), nil, filesTestMode); err != nil {
		t.Fatalf("WriteFile segment: %v", err)
	}

	if err := Run(t.Context(), cfg); !errors.Is(err, ErrSpoolExists) {
		t.Fatalf("Run = %v, want ErrSpoolExists", err)
	}
	requireNoSocket(t, socketOf(t, cfg.Dir))
	awaitLockFree(t, cfg.Dir)
}
