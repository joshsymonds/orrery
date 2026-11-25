package holder

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestLockProbeLiveCreatesNoLock checks that Live reports a dir with no lock file as having no holder, and leaves no lock
// file behind for the probe to have created.
func TestLockProbeLiveCreatesNoLock(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, LockName)
	live, err := Live(dir)
	if err != nil {
		t.Fatalf("Live(%s) = %v, want no error", dir, err)
	}
	if live {
		t.Fatalf("Live(%s) = true, want false for a dir with no lock file", dir)
	}
	if _, err = os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("lock %s: err %v, want it not to exist after Live", path, err)
	}
}

// TestLockProbeEndedTreatsRemovedLockAsGone checks that Ended, already waiting on a held lock, returns ErrGone once the
// lock file is removed and released, and that it leaves the removed lock file absent. The fake clock arms Ended's retry
// timer only once Ended has opened the lock and found it held, so the removal lands inside the wait.
func TestLockProbeEndedTreatsRemovedLockAsGone(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, LockName)
	lock, err := Lock(path)
	if err != nil {
		t.Fatalf("Lock(%s): %v", path, err)
	}
	type endedResult struct {
		exit  Exit
		found bool
		err   error
	}
	clock := newFakeClock()
	done := make(chan endedResult, 1)
	go func() {
		exit, found, endErr := endedOn(t.Context(), dir, clock)
		done <- endedResult{exit: exit, found: found, err: endErr}
	}()
	waiting := make(chan struct{})
	go func() {
		clock.waitArmed(t, 1)
		close(waiting)
	}()
	select {
	case got := <-done:
		t.Fatalf("Ended returned before it waited on the held lock: exit %+v, found %v, err %v",
			got.exit, got.found, got.err)
	case <-waiting:
	}
	if err = os.Remove(path); err != nil {
		t.Fatalf("remove lock %s: %v", path, err)
	}
	if err = lock.Close(); err != nil {
		t.Fatalf("release lock %s: %v", path, err)
	}
	clock.Advance(clearLockRetry)
	got := <-done
	if !errors.Is(got.err, ErrGone) {
		t.Fatalf("Ended of a removed lock = %v, want ErrGone", got.err)
	}
	if got.found || !reflect.DeepEqual(got.exit, Exit{}) {
		t.Fatalf("Ended of a removed lock = %+v, found %v; want no exit record", got.exit, got.found)
	}
	if _, err = os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("lock %s: err %v, want it not to exist after Ended", path, err)
	}
}
