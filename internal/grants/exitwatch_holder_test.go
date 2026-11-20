package grants

import (
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/joshsymonds/orrery/internal/holder"
)

// inotifyLink is the link /proc/self/fd reports for an inotify descriptor.
const inotifyLink = "anon_inode:inotify"

// heldLock takes the holder lock of a fresh directory, which the test closes when it ends. It returns the directory and
// the lock file.
func heldLock(t *testing.T) (string, *os.File) {
	t.Helper()
	dir := t.TempDir()
	lock, err := holder.Lock(filepath.Join(dir, holder.LockName))
	if err != nil {
		t.Fatalf("take holder lock: %v", err)
	}
	t.Cleanup(func() { lock.Close() })
	return dir, lock
}

// lockLinkOf returns the link /proc/self/fd reports for the lock file of dir.
func lockLinkOf(t *testing.T, dir string) string {
	t.Helper()
	path, err := filepath.EvalSymlinks(filepath.Join(dir, holder.LockName))
	if err != nil {
		t.Fatalf("resolve lock file: %v", err)
	}
	return path
}

// fdLinks counts this process's open descriptors whose /proc/self/fd links name target.
func fdLinks(t *testing.T, target string) int {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatalf("list /proc/self/fd: %v", err)
	}
	count := 0
	for _, entry := range entries {
		link, err := os.Readlink(filepath.Join("/proc/self/fd", entry.Name()))
		if errors.Is(err, fs.ErrNotExist) {
			continue // the descriptor closed since the listing
		}
		if err != nil {
			t.Fatalf("read link of descriptor %s: %v", entry.Name(), err)
		}
		if link == target {
			count++
		}
	}
	return count
}

// awaitArmed waits until the first attempt of w is waiting, and fails the test if w ends first.
func awaitArmed(t *testing.T, w *defaultWatch) {
	t.Helper()
	select {
	case <-w.armed:
	case <-w.done:
		t.Fatal("holder watch ended before it armed")
	}
}

// TestBrokerWakeHolderWatchStopEndsItsWaitWhileTheHolderLives checks that stop ends a holder watch that waits while its
// holder lives, without closing exited, and that the watch leaves no descriptor open.
func TestBrokerWakeHolderWatchStopEndsItsWaitWhileTheHolderLives(t *testing.T) {
	dir, _ := heldLock(t)
	lockLink := lockLinkOf(t, dir)
	lockBefore := fdLinks(t, lockLink)
	inotifyBefore := fdLinks(t, inotifyLink)
	w := newHolderWatch(slog.New(slog.DiscardHandler), dir)
	t.Cleanup(w.stop)
	awaitArmed(t, w)
	w.stop()
	<-w.done
	select {
	case <-w.exited:
		t.Fatal("exited closed while the holder still holds its lock")
	default:
	}
	if got := fdLinks(t, lockLink); got != lockBefore {
		t.Fatalf("%d descriptors of the lock file after stop, want %d", got, lockBefore)
	}
	if got := fdLinks(t, inotifyLink); got > inotifyBefore {
		t.Fatalf("%d inotify descriptors after stop, want at most %d", got, inotifyBefore)
	}
}

// TestBrokerWakeHolderWatchSignalsWhenTheHolderLetsGo checks that a holder watch closes exited once the holder lets
// go of its lock, and that a close of the lock file while the holder still holds it does not.
func TestBrokerWakeHolderWatchSignalsWhenTheHolderLetsGo(t *testing.T) {
	dir, lock := heldLock(t)
	w := newHolderWatch(slog.New(slog.DiscardHandler), dir)
	t.Cleanup(w.stop)
	awaitArmed(t, w)
	// Opening and closing the lock file raises a close event while the holder still holds the lock.
	probe, err := os.Open(filepath.Join(dir, holder.LockName))
	if err != nil {
		t.Fatalf("open lock file: %v", err)
	}
	if err = probe.Close(); err != nil {
		t.Fatalf("close lock file: %v", err)
	}
	select {
	case <-w.exited:
		t.Fatal("exited closed on a close event while the holder still holds its lock")
	default:
	}
	if err = lock.Close(); err != nil {
		t.Fatalf("release holder lock: %v", err)
	}
	<-w.exited
	<-w.done
}

// TestBrokerWakeHolderWatchSignalsAtOnceWithNoLockFile checks that a holder watch on a directory with no lock file
// closes exited at once.
func TestBrokerWakeHolderWatchSignalsAtOnceWithNoLockFile(t *testing.T) {
	w := newHolderWatch(slog.New(slog.DiscardHandler), t.TempDir())
	t.Cleanup(w.stop)
	<-w.exited
	<-w.done
}
