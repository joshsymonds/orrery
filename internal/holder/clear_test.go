package holder

import (
	"context"
	"errors"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// clearLockHeldFor is how long a test holds the session lock before it releases it, so Clear waits on the lock.
const clearLockHeldFor = 50 * time.Millisecond

// wedgedStopTimeout is the deadline the tests give Stop when the holder never answers. Stop gives the holder most of it
// before it kills the holder, so the kill and the wait for the lock fit inside it.
const wedgedStopTimeout = 3 * time.Second

// requireDirGone fails when dir still exists.
func requireDirGone(t *testing.T, dir string) {
	t.Helper()
	if _, err := os.Lstat(dir); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("dir %s: err %v, want it removed", dir, err)
	}
}

func TestClearMissingDirIsNil(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "missing")
	if err := Clear(t.Context(), dir); err != nil {
		t.Fatalf("Clear(missing dir) = %v, want nil", err)
	}
}

func TestClearRemovesLeftoverFilesWhenNoHolderServesDir(t *testing.T) {
	t.Parallel()

	cfg := newHolderConfig(t)
	if err := PrepareDir(cfg.Dir); err != nil {
		t.Fatalf("PrepareDir: %v", err)
	}
	exit := Exit{Reason: ReasonExited, Code: 3, StdoutEnd: int64(len("out\n")), Stderr: "err"}
	if err := WriteExit(filepath.Join(cfg.Dir, ExitName), exit); err != nil {
		t.Fatalf("WriteExit: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cfg.Dir, segmentName(0)), []byte("out\n"), filesTestMode); err != nil {
		t.Fatalf("WriteFile segment: %v", err)
	}
	// A socket file with no listener behind it is what a holder that died left behind.
	if err := os.WriteFile(socketOf(t, cfg.Dir), nil, socketTestMode); err != nil {
		t.Fatalf("WriteFile stale socket: %v", err)
	}

	if err := Clear(t.Context(), cfg.Dir); err != nil {
		t.Fatalf("Clear = %v, want nil", err)
	}
	requireDirGone(t, cfg.Dir)
}

func TestClearStopsRunningHolderThenRemovesDir(t *testing.T) {
	t.Parallel()

	cfg := newHolderConfig(t)
	run := startHolder(t.Context(), t, cfg)
	sock := socketOf(t, cfg.Dir)
	dialHolder(t, run, sock).hello(WireVersion)
	pid := awaitChildPid(t, cfg.Child.Dir)

	if err := Clear(t.Context(), cfg.Dir); err != nil {
		t.Fatalf("Clear = %v, want nil", err)
	}
	if err := run.wait(t); err != nil {
		t.Fatalf("holder Run = %v, want nil", err)
	}
	requireChildGroupGone(t, pid)
	requireDirGone(t, cfg.Dir)
}

func TestClearWaitsForHeldLockThenRemovesDir(t *testing.T) {
	t.Parallel()

	cfg := newHolderConfig(t)
	if err := PrepareDir(cfg.Dir); err != nil {
		t.Fatalf("PrepareDir: %v", err)
	}
	held, err := Lock(filepath.Join(cfg.Dir, LockName))
	if err != nil {
		t.Fatalf("Lock: %v", err)
	}
	release := time.AfterFunc(clearLockHeldFor, func() {
		if cerr := held.Close(); cerr != nil {
			t.Errorf("close held lock: %v", cerr)
		}
	})
	defer release.Stop()

	if err := Clear(t.Context(), cfg.Dir); err != nil {
		t.Fatalf("Clear = %v, want nil once the lock is released", err)
	}
	requireDirGone(t, cfg.Dir)
}

func TestClearWithLockHeldAndCancelledContextKeepsDir(t *testing.T) {
	t.Parallel()

	cfg := newHolderConfig(t)
	if err := PrepareDir(cfg.Dir); err != nil {
		t.Fatalf("PrepareDir: %v", err)
	}
	lockForTest(t, filepath.Join(cfg.Dir, LockName))
	if err := WriteExit(filepath.Join(cfg.Dir, ExitName), Exit{Reason: ReasonExited, Code: 0}); err != nil {
		t.Fatalf("WriteExit: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := Clear(ctx, cfg.Dir)
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("Clear = %v, want an error matching ErrLocked", err)
	}
	assertDirNames(t, cfg.Dir, ExitName, LockName)
}

// startLockHolder starts a sleep that holds the session lock at path, as a holder does while it runs. The sleep inherits
// the locked file descriptor, so the lock stays held after the test closes its own copy. The sleep leads its own process
// group, and the test's cleanup kills it and reaps it. It returns the sleep's pid and start time.
func startLockHolder(t *testing.T, path string) (int, string) {
	t.Helper()
	file, err := Lock(path)
	if err != nil {
		t.Fatalf("Lock(%s): %v", path, err)
	}
	cmd := exec.Command(shPath(t), "-c", orphanChildScript)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.ExtraFiles = []*os.File{file}
	startErr := cmd.Start()
	closeErr := file.Close()
	if startErr != nil {
		t.Fatalf("start lock holder: %v", startErr)
	}
	if closeErr != nil {
		t.Fatalf("close the test's copy of the lock: %v", closeErr)
	}
	pid := cmd.Process.Pid
	start := procStartOf(t, pid)
	t.Cleanup(func() {
		killGroupAndAwait(t, pid, start)
		reap(t, cmd)
	})
	return pid, start
}

// listenWedged listens on the socket in dir and never accepts, so a dial connects and its hello is never answered. The
// listener closes when the test ends.
func listenWedged(t *testing.T, dir string) {
	t.Helper()
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socketOf(t, dir), Net: "unix"})
	if err != nil {
		t.Fatalf("listen on %s: %v", socketOf(t, dir), err)
	}
	t.Cleanup(func() {
		if cerr := listener.Close(); cerr != nil {
			t.Errorf("close listener: %v", cerr)
		}
	})
}

// startWedged makes the parts of a holder that never answers in a new session dir: a listener on the socket that never
// accepts, a sleep that holds the session lock as the holder does, and a sleep as the holder's child. It returns the dir
// and the pids record of the two processes, which the caller writes.
func startWedged(t *testing.T) (string, Pids) {
	t.Helper()
	dir := sessionDir(t)
	listenWedged(t, dir)
	holder, holderStart := startLockHolder(t, filepath.Join(dir, LockName))
	child, childStart := startSleeper(t)
	return dir, Pids{Holder: holder, HolderStart: holderStart, Child: child, ChildStart: childStart}
}

func TestStopMissingDirIsNil(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "missing")
	if err := Stop(t.Context(), dir); err != nil {
		t.Fatalf("Stop(missing dir) = %v, want nil", err)
	}
}

func TestStopStopsRunningHolderAndKeepsDir(t *testing.T) {
	t.Parallel()

	cfg := newHolderConfig(t)
	run := startHolder(t.Context(), t, cfg)
	dialHolder(t, run, socketOf(t, cfg.Dir)).hello(WireVersion)
	pid := awaitChildPid(t, cfg.Child.Dir)

	if err := Stop(t.Context(), cfg.Dir); err != nil {
		t.Fatalf("Stop = %v, want nil", err)
	}
	if err := run.wait(t); err != nil {
		t.Fatalf("holder Run = %v, want nil", err)
	}
	requireChildGroupGone(t, pid)
	if got := readExitFile(t, cfg.Dir).Reason; got != ReasonStopped {
		t.Errorf("exit reason = %q, want %q", got, ReasonStopped)
	}
	if _, err := os.Stat(cfg.Dir); err != nil {
		t.Fatalf("Stat(dir) = %v, want the dir kept", err)
	}
}

func TestStopOnWedgedHolderKillsItAndItsChildWithinDeadline(t *testing.T) {
	t.Parallel()

	dir, pids := startWedged(t)
	writeRecord(t, dir, pids)
	ctx, cancel := context.WithTimeout(t.Context(), wedgedStopTimeout)
	defer cancel()

	stopped := startAsync(func() error { return Stop(ctx, dir) })
	if err := awaitResult(t, "Stop", stopped); err != nil {
		t.Fatalf("Stop = %v, want nil", err)
	}
	if ctx.Err() != nil {
		t.Fatalf("Stop returned after its context ended: %v", ctx.Err())
	}
	if procRunning(t, pids.Holder, pids.HolderStart) {
		t.Errorf("holder %d still runs after Stop", pids.Holder)
	}
	if procRunning(t, pids.Child, pids.ChildStart) {
		t.Errorf("child %d still runs after Stop", pids.Child)
	}
	awaitLockFree(t, dir)
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("Stat(dir) = %v, want the dir kept", err)
	}
}

func TestStopDoesNotSignalHolderWhoseRecordedStartDiffers(t *testing.T) {
	t.Parallel()

	dir, pids := startWedged(t)
	holderStart := pids.HolderStart
	pids.HolderStart += "1"
	writeRecord(t, dir, pids)
	ctx, cancel := context.WithTimeout(t.Context(), wedgedStopTimeout)
	defer cancel()

	if err := Stop(ctx, dir); !errors.Is(err, ErrLocked) {
		t.Fatalf("Stop = %v, want an error matching ErrLocked", err)
	}
	if ctx.Err() == nil {
		t.Errorf("Stop returned before its context ended")
	}
	if !procRunning(t, pids.Holder, holderStart) {
		t.Errorf("holder %d was signaled though its recorded start %s does not match", pids.Holder, pids.HolderStart)
	}
	if !procRunning(t, pids.Child, pids.ChildStart) {
		t.Errorf("child %d was stopped though its holder was not", pids.Child)
	}
}

func TestClearStopsWedgedHolderThenRemovesDir(t *testing.T) {
	t.Parallel()

	dir, pids := startWedged(t)
	writeRecord(t, dir, pids)
	ctx, cancel := context.WithTimeout(t.Context(), wedgedStopTimeout)
	defer cancel()

	cleared := startAsync(func() error { return Clear(ctx, dir) })
	if err := awaitResult(t, "Clear", cleared); err != nil {
		t.Fatalf("Clear = %v, want nil", err)
	}
	requireDirGone(t, dir)
	if procRunning(t, pids.Holder, pids.HolderStart) {
		t.Errorf("holder %d still runs after Clear", pids.Holder)
	}
	if procRunning(t, pids.Child, pids.ChildStart) {
		t.Errorf("child %d still runs after Clear", pids.Child)
	}
}
