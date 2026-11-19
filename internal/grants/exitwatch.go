package grants

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/sys/unix"

	"github.com/joshsymonds/orrery/internal/holder"
)

// exitWatchRetry is how long a default exit watch waits before its next attempt, after an attempt failed while the
// watched process or holder may still live. Such a failure is logged and tried again, so a live client or holder is
// never left unwatched.
const exitWatchRetry = 5 * time.Second

// clientExitWatch is the default Config.WatchClient, and logs the failures of its watch to logger. It watches the
// process with pid that started at start through a pidfd, so exited closes once that process is gone: a pid with no
// process, a pid reused by a process that started later, or the process's exit. stop ends the watch without closing
// exited.
func clientExitWatch(logger *slog.Logger, pid int, start int64) (<-chan struct{}, func()) {
	return startExitWatch(logger, fmt.Sprintf("client process %d", pid), func(w *defaultWatch) (bool, error) {
		return w.waitClient(pid, start)
	})
}

// holderExitWatch is the default Config.WatchHolder, and logs the failures of its watch to logger. It watches the
// holder directory dir, so exited closes once the holder's lock is free, or at once when dir has no lock file. stop
// ends the watch without closing exited.
func holderExitWatch(logger *slog.Logger, dir string) (<-chan struct{}, func()) {
	return startExitWatch(logger, fmt.Sprintf("holder directory %s", dir), func(w *defaultWatch) (bool, error) {
		return w.waitHolder(dir)
	})
}

// defaultWatch is one default exit watch. Its goroutine makes attempts until one finds the watched process or holder
// gone, or stop ends the watch. An attempt keeps the file it waits on in the watch, so stop closes that file and ends a
// wait that blocks.
type defaultWatch struct {
	logger   *slog.Logger
	what     string
	exited   chan struct{}
	stopped  chan struct{}
	stopOnce sync.Once
	mu       sync.Mutex
	file     *os.File // the file the running attempt waits on, or nil
}

// startExitWatch starts an exit watch whose goroutine runs attempt until it reports the watched thing gone. It returns the
// watch's exited channel and its stop.
func startExitWatch(
	logger *slog.Logger, what string, attempt func(*defaultWatch) (bool, error),
) (<-chan struct{}, func()) {
	w := &defaultWatch{logger: logger, what: what, exited: make(chan struct{}), stopped: make(chan struct{})}
	go w.run(attempt)
	return w.exited, w.stop
}

// run runs attempts until one reports the watched thing gone, or stop ends the watch. An attempt that fails while the
// watch runs is logged, and the next attempt waits exitWatchRetry.
func (w *defaultWatch) run(attempt func(*defaultWatch) (bool, error)) {
	for {
		gone, err := attempt(w)
		if w.isStopped() {
			return
		}
		if gone {
			close(w.exited)
			return
		}
		w.logger.Warn("exit watch failed; trying again", "watch", w.what, "err", err)
		timer := time.NewTimer(exitWatchRetry)
		select {
		case <-w.stopped:
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// stop ends the watch. It closes the file the running attempt waits on, which ends a wait that blocks, and it may be
// called more than once.
func (w *defaultWatch) stop() {
	w.stopOnce.Do(func() {
		close(w.stopped)
		w.mu.Lock()
		file := w.file
		w.file = nil
		w.mu.Unlock()
		if file != nil {
			_ = file.Close() // the watch has no reader left to report a close error to
		}
	})
}

// isStopped reports whether stop has run.
func (w *defaultWatch) isStopped() bool {
	select {
	case <-w.stopped:
		return true
	default:
		return false
	}
}

// hold records file as the one the attempt waits on, so stop can close it. It reports false once the watch is stopped,
// and then the attempt gives up file.
func (w *defaultWatch) hold(file *os.File) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.isStopped() {
		return false
	}
	w.file = file
	return true
}

// release gives up file, which the attempt is done with, and closes it.
func (w *defaultWatch) release(file *os.File) {
	w.mu.Lock()
	if w.file == file {
		w.file = nil
	}
	w.mu.Unlock()
	_ = file.Close() // the watch has no reader left to report a close error to
}

// waitClient makes one attempt to watch the process with pid that started at start. It reports the process gone, or an
// error when the attempt failed while the process may still live.
func (w *defaultWatch) waitClient(pid int, start int64) (bool, error) {
	fd, err := unix.PidfdOpen(pid, unix.PIDFD_NONBLOCK)
	if errors.Is(err, unix.ESRCH) || errors.Is(err, unix.EINVAL) {
		// ESRCH: no process has pid. EINVAL: pid cannot name a process, as 0 or a negative pid cannot.
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("open pidfd: %w", err)
	}
	// From here the file owns fd, and every path that gives up the attempt closes it.
	file := os.NewFile(uintptr(fd), "client pidfd")
	if !w.hold(file) {
		w.release(file)
		return false, nil
	}
	defer w.release(file)
	// The pidfd is open before the start time is read, so a process that exits in between is still seen to end.
	now, err := readStart(pid)
	if errors.Is(err, fs.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if now != start {
		return true, nil
	}
	raw, err := file.SyscallConn()
	if err != nil {
		return false, fmt.Errorf("watch pidfd: %w", err)
	}
	// Read calls the function once the wait starts and again each time the pidfd is reported readable. The function asks
	// the kernel whether the process has exited, rather than trusting a report that came before the wait began.
	var pollErr error
	err = raw.Read(func(fd uintptr) bool {
		var exited bool
		exited, pollErr = pidfdExited(fd)
		return exited || pollErr != nil
	})
	if err != nil {
		return false, fmt.Errorf("wait for exit: %w", err)
	}
	if pollErr != nil {
		return false, fmt.Errorf("poll pidfd: %w", pollErr)
	}
	return true, nil
}

// pidfdExited reports whether the process of the pidfd fd has exited, which makes the pidfd readable. It polls the pidfd
// without waiting.
func pidfdExited(fd uintptr) (bool, error) {
	if fd > math.MaxInt32 {
		return false, fmt.Errorf("pidfd %d is out of range", fd)
	}
	fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
	for {
		n, err := unix.Poll(fds, 0)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return false, fmt.Errorf("poll: %w", err)
		}
		return n > 0, nil
	}
}

// waitHolder makes one attempt to watch the holder directory dir. The holder lives while it holds its lock file, so the
// attempt takes a blocking shared flock on the lock file, which waits for the holder's exclusive one, and releases it at
// once. A directory with no lock file has no holder.
func (w *defaultWatch) waitHolder(dir string) (bool, error) {
	file, err := os.Open(filepath.Clean(filepath.Join(dir, holder.LockName)))
	if errors.Is(err, fs.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("open holder lock: %w", err)
	}
	if !w.hold(file) {
		w.release(file)
		return false, nil
	}
	defer w.release(file)
	raw, err := file.SyscallConn()
	if err != nil {
		return false, fmt.Errorf("watch holder lock: %w", err)
	}
	// Control keeps the file open while the flock blocks, so the descriptor is not reused under the flock.
	var waitErr error
	err = raw.Control(func(fd uintptr) {
		waitErr = waitFree(int(fd))
	})
	if err != nil {
		return false, fmt.Errorf("watch holder lock: %w", err)
	}
	if waitErr != nil {
		return false, waitErr
	}
	return true, nil
}

// waitFree takes a blocking shared flock on fd, which waits for the holder's exclusive one to go, and releases it at
// once. A flock interrupted by a signal is taken again.
func waitFree(fd int) error {
	for {
		err := unix.Flock(fd, unix.LOCK_SH)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return fmt.Errorf("take holder lock: %w", err)
		}
		if unlockErr := unix.Flock(fd, unix.LOCK_UN); unlockErr != nil {
			return fmt.Errorf("release holder lock: %w", unlockErr)
		}
		return nil
	}
}
