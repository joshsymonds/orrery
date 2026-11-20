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
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/waittest"
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

// running is one Run in flight. Its exit record and error are valid once done closes.
type running struct {
	done chan struct{}
	exit Exit
	err  error
}

// startHolder runs Run for cfg in a goroutine. ctx must derive from t.Context(), which ends the holder before the
// test's cleanups run, so the cleanup only waits for Run to return.
func startHolder(ctx context.Context, t *testing.T, cfg Config) *running {
	t.Helper()
	run := &running{done: make(chan struct{})}
	go func() {
		run.exit, run.err = Run(ctx, cfg)
		close(run.done)
	}()
	t.Cleanup(func() {
		<-run.done
	})
	return run
}

// wait returns Run's error once Run has returned. It has no deadline of its own.
func (run *running) wait(t *testing.T) error {
	t.Helper()
	<-run.done
	return run.err
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
	// The holder reads no real cgroup: the test may run in a holder scope, whose kills are not the test's.
	return Config{
		Dir:     dir,
		Session: testSession,
		Child:   ChildConfig{Path: fakeClaudePath(t), Dir: t.TempDir()},
		memory:  memoryCap{self: filepath.Join(t.TempDir(), "no-cgroup"), root: t.TempDir()},
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
			t.Fatalf("holder returned %v before its socket %s accepted: %v", run.err, sock, err)
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

// read returns the next frame the holder sends. It sets no deadline: the holder's pace is the machine's, and the test
// binary's timeout ends a read that never returns.
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

// setDeadline clears any read deadline, such as the one expectSilent sets, so the next read waits for the holder. A
// holder that never answers is the test binary's timeout to end.
func (c *client) setDeadline() {
	c.t.Helper()
	if err := c.conn.SetReadDeadline(time.Time{}); err != nil {
		c.t.Fatalf("set read deadline: %v", err)
	}
}

// awaitChildPid waits for the fake child to write its pid into childDir and returns it. The fake renames the file into
// place, so a read sees the whole pid. It fails at once if run, the holder that starts the child, ends first.
func awaitChildPid(t *testing.T, run *running, childDir string) int {
	t.Helper()
	name := filepath.Join(childDir, fakePidName)
	waittest.Until(t, run.done, "child pid in "+name, func() bool {
		_, err := os.Stat(name)
		return err == nil
	})
	return readChildPid(t, name)
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
	if !processGroupLive(t, pgid) {
		t.Fatalf("process group %d has no live process, want it alive", pgid)
	}
}

// flockBlocking takes an exclusive flock on fd, blocking until the kernel grants it. A signal that interrupts the wait
// restarts it.
func flockBlocking(fd int) error {
	for {
		err := syscall.Flock(fd, syscall.LOCK_EX)
		if !errors.Is(err, syscall.EINTR) {
			return err
		}
	}
}

// holdSessionLock opens the file at path with flag, waits in a blocking flock until the kernel grants it, runs use with
// the lock held, and closes the file, which releases the lock. The wait and use run with syscall.ForkLock held for
// reading, which Go's forks take for writing, so no fork can copy the descriptor while the lock is held and keep the
// lock busy after the release. A copy from a fork that began earlier lasts until its child execs, and the flock waits
// out that exec like any other holder, so the wait ends when the lock's last holder lets go. A holder that never does
// keeps the wait going until the test binary's timeout.
func holdSessionLock(path string, flag int, use func(file *os.File) error) error {
	file, err := os.OpenFile(path, flag, lockFileMode)
	if err != nil {
		return fmt.Errorf("open session lock: %w", err)
	}
	syscall.ForkLock.RLock()
	defer syscall.ForkLock.RUnlock()
	if err = flockBlocking(int(file.Fd())); err != nil {
		return errors.Join(fmt.Errorf("take session lock: %w", err), file.Close())
	}
	return errors.Join(use(file), file.Close())
}

// awaitLockFree waits until the session lock in dir is free to take, and leaves it free. A lock the holder never
// released keeps the wait going until the test binary's timeout.
func awaitLockFree(t *testing.T, dir string) {
	t.Helper()
	noUse := func(*os.File) error { return nil }
	if err := holdSessionLock(filepath.Clean(filepath.Join(dir, LockName)), os.O_RDWR|os.O_CREATE, noUse); err != nil {
		t.Fatalf("wait for the session lock to be free: %v", err)
	}
}

// lockIDOnFile returns the MAJOR:MINOR:INODE field of the lock process pid holds on the file at path, as the kernel names
// it, and false when pid holds no lock on path. The kernel writes a descriptor's lock list in its fdinfo, and that list
// holds only the descriptor's own locks, so one read sees all of them. /proc/locks is read a page at a time and can miss
// a line, which is why this reads the holder's descriptor instead. The device in the id need not be the one stat reports,
// as on btrfs, so the id is read from the kernel's line rather than computed.
func lockIDOnFile(pid int, path string) (string, bool, error) {
	proc := filepath.Join("/proc", strconv.Itoa(pid))
	fds, err := os.ReadDir(filepath.Join(proc, "fd"))
	if err != nil {
		return "", false, fmt.Errorf("list the descriptors of process %d: %w", pid, err)
	}
	for _, fd := range fds {
		target, err := os.Readlink(filepath.Join(proc, "fd", fd.Name()))
		if errors.Is(err, fs.ErrNotExist) {
			continue // the descriptor closed while it was read
		}
		if err != nil {
			return "", false, fmt.Errorf("read descriptor %s of process %d: %w", fd.Name(), pid, err)
		}
		if target != path {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(proc, "fdinfo", fd.Name()))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", false, fmt.Errorf("read the fdinfo of descriptor %s of process %d: %w", fd.Name(), pid, err)
		}
		for line := range strings.Lines(string(raw)) {
			rest, isLock := strings.CutPrefix(line, "lock:")
			if !isLock {
				continue
			}
			// The line reads: number, class, mode, access, pid, MAJOR:MINOR:INODE, start, end.
			fields := strings.Fields(rest)
			if len(fields) > 5 && fields[4] == strconv.Itoa(pid) {
				return fields[5], true, nil
			}
		}
	}
	return "", false, nil
}

// heldLockID returns how the kernel names the session lock file in dir, which the live holder that dir's pids record
// names must hold: the MAJOR:MINOR:INODE field of the holder's lock. The holder takes the lock before it writes the pids
// record, so the lock is already held when the record is read and no wait is needed. It fails if the holder holds no
// lock on the file, which means the holder has stopped and its lock is gone with it.
func heldLockID(t *testing.T, dir string) string {
	t.Helper()
	pids, err := ReadPids(dir)
	if err != nil {
		t.Fatalf("read the holder's pids: %v", err)
	}
	path, err := filepath.EvalSymlinks(filepath.Join(dir, LockName))
	if err != nil {
		t.Fatalf("resolve the session lock's path: %v", err)
	}
	id, ok, err := lockIDOnFile(pids.Holder, path)
	if err != nil {
		t.Fatalf("read the holder's lock: %v", err)
	}
	if !ok {
		t.Fatalf("holder %d holds no lock on %s: it stopped, or never took the lock", pids.Holder, path)
	}
	return id
}

// awaitLockUnheld waits until no open file description holds the session lock in dir, with no deadline of its own. It
// waits by taking the lock: the blocking flock returns once the last holder lets go, which Stop's own descriptor does
// when Run closes it, and a copy of that descriptor does at its child's exec. The lock is released again at once, and
// the lock the wait cleared must be the one heldLockID named, id.
func awaitLockUnheld(t *testing.T, dir, id string) {
	t.Helper()
	path, err := filepath.EvalSymlinks(filepath.Join(dir, LockName))
	if err != nil {
		t.Fatalf("resolve the session lock's path: %v", err)
	}
	var got string
	var held bool
	err = holdSessionLock(path, os.O_RDONLY, func(*os.File) error {
		var lookErr error
		got, held, lookErr = lockIDOnFile(os.Getpid(), path)
		return lookErr
	})
	if err != nil {
		t.Fatalf("wait for the session lock to be unheld: %v", err)
	}
	if !held || got != id {
		t.Fatalf("the session lock the wait cleared is %s (held %v), want %s", got, held, id)
	}
}

// childHasOpen reports whether a child of this process has the file at path open. A child that exits while it is read
// has nothing open.
func childHasOpen(t *testing.T, path string) bool {
	t.Helper()
	tasks, err := os.ReadDir("/proc/self/task")
	if err != nil {
		t.Fatalf("list this process's threads: %v", err)
	}
	for _, task := range tasks {
		raw, err := os.ReadFile(filepath.Join("/proc/self/task", task.Name(), "children"))
		if err != nil {
			continue // the thread has exited
		}
		for _, child := range strings.Fields(string(raw)) {
			fds, err := os.ReadDir(filepath.Join("/proc", child, "fd"))
			if err != nil {
				continue // the child has exited or exec'd into a process this user cannot read
			}
			for _, fd := range fds {
				target, err := os.Readlink(filepath.Join("/proc", child, "fd", fd.Name()))
				if err == nil && target == path {
					return true
				}
			}
		}
	}
	return false
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
	pid := awaitChildPid(t, run, cfg.Child.Dir)

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
	assertDirNames(t, cfg.Dir, ExitName, PidsName, LockName, segmentName(0))
}

func TestHolderChildThatExitsAloneWritesItsStatusAndTailAndReturnsThem(t *testing.T) {
	t.Parallel()

	cfg := newHolderConfig(t)
	cfg.Child = ChildConfig{Path: shPath(t), Args: []string{"-c", exitTailChild}, Dir: t.TempDir()}
	run := startHolder(t.Context(), t, cfg)
	if err := run.wait(t); err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}
	want := Exit{Reason: ReasonExited, Code: childDiedCode, StdoutEnd: int64(len("out\n")), Stderr: "err\n"}
	if got := readExitFile(t, cfg.Dir); got != want {
		t.Errorf("exit file = %+v, want %+v", got, want)
	}
	// The caller takes the status from the returned record, since the directory can be gone once the lock is free.
	if run.exit != want {
		t.Errorf("Run's exit = %+v, want the record it wrote, %+v", run.exit, want)
	}
	requireNoSocket(t, socketOf(t, cfg.Dir))
	assertDirNames(t, cfg.Dir, ExitName, PidsName, LockName, segmentName(0))
}

func TestHolderSecondHolderIsLockedOutAndFirstKeepsServing(t *testing.T) {
	t.Parallel()

	cfg := newHolderConfig(t)
	run := startHolder(t.Context(), t, cfg)
	sock := socketOf(t, cfg.Dir)
	dialHolder(t, run, sock).hello(WireVersion)
	pid := awaitChildPid(t, run, cfg.Child.Dir)

	if _, err := Run(t.Context(), cfg); !errors.Is(err, ErrLocked) {
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

	if _, err := Run(t.Context(), cfg); !errors.Is(err, ErrUndrained) {
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
	pid := awaitChildPid(t, run, cfg.Child.Dir)

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

	if _, err := Run(t.Context(), cfg); err == nil {
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

	if _, err := Run(t.Context(), cfg); !errors.Is(err, ErrSpoolExists) {
		t.Fatalf("Run = %v, want ErrSpoolExists", err)
	}
	requireNoSocket(t, socketOf(t, cfg.Dir))
	awaitLockFree(t, cfg.Dir)
}
