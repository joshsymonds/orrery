package grants

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/sys/unix"

	"github.com/joshsymonds/orrery/internal/holder"
	"github.com/joshsymonds/orrery/internal/inotify"
)

// exitWatchRetry is how long a default exit watch waits before its next attempt, after an attempt failed while the
// watched process or holder may still live. Such a failure is logged and tried again, so a live client or holder is
// never left unwatched.
const exitWatchRetry = 5 * time.Second

// holderEvents are the inotify events a holder watch waits for: a close of the holder's lock file. The holder's close
// lets go of its lock, and any other open file's close may leave the lock free as well.
const holderEvents = unix.IN_CLOSE_WRITE | unix.IN_CLOSE_NOWRITE

// clientExitWatch is the default Config.WatchClient, and logs the failures of its watch to logger. It watches the
// process with pid that started at start through a pidfd, so exited closes once that process is gone: a pid with no
// process, a pid reused by a process that started later, or the process's exit. stop ends the watch without closing
// exited.
func clientExitWatch(logger *slog.Logger, pid int, start int64) (<-chan struct{}, func()) {
	w := startExitWatch(logger, fmt.Sprintf("client process %d", pid), nil, func(w *defaultWatch) (bool, error) {
		return w.waitClient(pid, start)
	})
	return w.exited, w.stop
}

// holderExitWatch is the default Config.WatchHolder, and logs the failures of its watch to logger. It watches the
// holder directory dir, so exited closes once the holder's lock is free, or at once when dir has no lock file. stop
// ends the watch without closing exited.
func holderExitWatch(logger *slog.Logger, dir string) (<-chan struct{}, func()) {
	w := newHolderWatch(logger, dir)
	return w.exited, w.stop
}

// defaultWatch is one default exit watch. Its goroutine makes attempts until one finds the watched process or holder
// gone, or stop ends the watch. An attempt keeps the file it waits on in the watch, so stop closes that file and ends a
// wait that blocks. done is closed when the goroutine returns, and armed, which only a holder watch has, is closed once
// the first attempt is waiting.
type defaultWatch struct {
	logger   *slog.Logger
	what     string
	exited   chan struct{}
	stopped  chan struct{}
	stopOnce sync.Once
	done     chan struct{}
	armed    chan struct{} // nil for a client watch
	armOnce  sync.Once
	mu       sync.Mutex
	file     io.Closer // the file the running attempt waits on, or nil
}

// startExitWatch starts an exit watch whose goroutine runs attempt until it reports the watched thing gone. armed is
// the channel the first attempt closes once it waits, or nil for none. It returns the started watch.
func startExitWatch(
	logger *slog.Logger, what string, armed chan struct{}, attempt func(*defaultWatch) (bool, error),
) *defaultWatch {
	w := &defaultWatch{
		logger: logger, what: what, exited: make(chan struct{}), stopped: make(chan struct{}),
		done: make(chan struct{}), armed: armed,
	}
	go w.run(attempt)
	return w
}

// newHolderWatch starts a watch on the holder directory dir, and returns it. Its exited closes once the holder's lock
// is free, or at once when dir has no lock file. Its stop ends the watch without closing exited.
func newHolderWatch(logger *slog.Logger, dir string) *defaultWatch {
	return startExitWatch(logger, fmt.Sprintf("holder directory %s", dir), make(chan struct{}),
		func(w *defaultWatch) (bool, error) {
			return w.waitHolder(dir)
		})
}

// run runs attempts until one reports the watched thing gone, or stop ends the watch. An attempt that fails while the
// watch runs is logged, and the next attempt waits exitWatchRetry.
func (w *defaultWatch) run(attempt func(*defaultWatch) (bool, error)) {
	defer close(w.done)
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

// arm closes the armed channel of a holder watch, once. A client watch has none, so arm does nothing for it.
func (w *defaultWatch) arm() {
	if w.armed == nil {
		return
	}
	w.armOnce.Do(func() { close(w.armed) })
}

// hold records file as the one the attempt waits on, so stop can close it. It reports false once the watch is stopped,
// and then the attempt gives up file.
func (w *defaultWatch) hold(file io.Closer) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.isStopped() {
		return false
	}
	w.file = file
	return true
}

// release gives up file, which the attempt is done with, and closes it.
func (w *defaultWatch) release(file io.Closer) {
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

// waitHolder makes one attempt to watch the holder directory dir. The holder lives while it holds its lock file. The
// attempt watches the inode of the lock file for close events, which a holder raises when it closes the file and so
// lets go of the lock. It arms the watch before its first probe of the lock, so a holder that lets go between that
// probe and the wait still raises an event. A directory with no lock file has no holder.
func (w *defaultWatch) waitHolder(dir string) (bool, error) {
	lock, err := os.Open(filepath.Clean(filepath.Join(dir, holder.LockName)))
	if errors.Is(err, fs.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("open holder lock: %w", err)
	}
	defer w.release(lock)
	watch, err := inotify.New()
	if err != nil {
		return false, fmt.Errorf("create holder watch: %w", err)
	}
	// The attempt waits on the inotify instance, so stop closes it, and that ends the wait.
	if !w.hold(watch) {
		w.release(watch)
		return false, nil
	}
	defer w.release(watch)
	// The watch names the inode of the lock file that was opened, whatever the holder directory's path names now.
	if _, err = watch.AddFile(lock, holderEvents); err != nil {
		return false, fmt.Errorf("watch holder lock: %w", err)
	}
	free, err := lockFree(lock)
	if err != nil {
		return false, err
	}
	if free {
		return true, nil
	}
	w.arm()
	return awaitFree(watch, lock)
}

// controlFD runs fn on the descriptor of file while the file stays open. fn must not keep the descriptor.
func controlFD(file *os.File, fn func(fd uintptr)) error {
	raw, err := file.SyscallConn()
	if err != nil {
		return fmt.Errorf("access descriptor: %w", err)
	}
	if err = raw.Control(fn); err != nil {
		return fmt.Errorf("control descriptor: %w", err)
	}
	return nil
}

// lockFree probes the holder's lock on file without waiting. It reports true when the lock is free, and then takes it
// and gives it up at once, and false while the holder still holds it.
func lockFree(file *os.File) (bool, error) {
	var free bool
	var probeErr error
	if err := controlFD(file, func(fd uintptr) {
		free, probeErr = probeFlock(fd)
	}); err != nil {
		return false, fmt.Errorf("probe holder lock: %w", err)
	}
	return free, probeErr
}

// probeFlock takes a shared, non-blocking flock on fd. It reports true when the lock was free, and releases it at once.
// A probe interrupted by a signal is made again.
func probeFlock(fd uintptr) (bool, error) {
	for {
		err := unix.Flock(int(fd), unix.LOCK_SH|unix.LOCK_NB)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if errors.Is(err, unix.EWOULDBLOCK) {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("probe holder lock: %w", err)
		}
		if unlockErr := unix.Flock(int(fd), unix.LOCK_UN); unlockErr != nil {
			return false, fmt.Errorf("release holder lock: %w", unlockErr)
		}
		return true, nil
	}
}

// awaitFree waits for events on watch, which watches the inode of lock, and probes the lock after each read of events.
// It reports true once the lock is free. It fails when a read fails, which stop causes, or when the kernel has dropped
// the watch, which it reports with IN_IGNORED.
func awaitFree(watch *inotify.Inotify, lock *os.File) (bool, error) {
	for {
		events, err := watch.Read()
		if err != nil {
			return false, fmt.Errorf("read holder watch: %w", err)
		}
		for _, event := range events {
			if event.Mask&unix.IN_IGNORED != 0 {
				return false, errors.New("the kernel dropped the holder watch")
			}
		}
		free, err := lockFree(lock)
		if err != nil {
			return false, err
		}
		if free {
			return true, nil
		}
	}
}
