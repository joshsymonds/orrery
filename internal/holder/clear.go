package holder

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// clearLockRetry is how often Clear retries the session lock while a stopped holder still holds it.
const clearLockRetry = 10 * time.Millisecond

// stopKillReserve is the part of ctx's time that Stop keeps back from the wait for a holder's answer. Once that wait
// ends, Stop still needs time to kill the holder and its child and to see the session lock free.
const stopKillReserve = time.Second

// stopAnswerDivisor makes the answer's share of the time left at least 1/stopAnswerDivisor, so a short deadline still
// gives a holder that is stopping a chance to answer.
const stopAnswerDivisor = 2

// Stop ends the holder serving dir, a session's holder directory, and leaves dir in place. It sends stop over the holder's
// socket and waits for the holder to answer stopped, but not past ctx's deadline, so a holder that never answers does not
// hang it. When that fails while the session lock is still held, Stop SIGKILLs the holder that dir's pids record names
// and then the child it records, each only when /proc confirms the recorded start. Stop then waits until the session
// lock is free and releases it. When ctx ends first, Stop returns an error that wraps ErrLocked. A dir that does not
// exist has no holder, so Stop returns nil for it.
func Stop(ctx context.Context, dir string) error {
	present, err := dirPresent(dir)
	if err != nil || !present {
		return err
	}
	lock, err := stopHolder(ctx, dir)
	if err != nil {
		return fmt.Errorf("holder: stop %s: %w", dir, err)
	}
	if closeErr := lock.Close(); closeErr != nil {
		return fmt.Errorf("holder: release lock of %s: %w", dir, closeErr)
	}
	return nil
}

// Kill ends the holder serving dir without asking it, for a holder that has already failed to answer: it SIGKILLs the
// holder that dir's pids record names, and then the child it records, as Stop does once a holder gives no answer, and
// returns once the session lock is free. It signals nothing when the lock is free, when dir holds no pids record, or
// when /proc does not confirm the recorded start of the holder. A dir that does not exist has no holder, so Kill
// returns nil. When ctx has ended, Kill signals nothing and returns ctx's error; when ctx ends while the lock is still
// held, it returns an error that wraps ErrLocked.
func Kill(ctx context.Context, dir string) error {
	present, err := dirPresent(dir)
	if err != nil || !present {
		return err
	}
	if err = ctx.Err(); err != nil {
		return fmt.Errorf("holder: kill %s: %w", dir, err)
	}
	if err = killUnattachable(ctx, dir); err != nil {
		return fmt.Errorf("holder: kill %s: %w", dir, err)
	}
	lock, err := awaitLock(ctx, filepath.Join(dir, LockName))
	if err != nil {
		return fmt.Errorf("holder: kill %s: %w", dir, err)
	}
	if closeErr := lock.Close(); closeErr != nil {
		return fmt.Errorf("holder: release lock of %s: %w", dir, closeErr)
	}
	return nil
}

// Clear empties the session holder directory dir so a new holder can start there. A holder serving dir's socket is
// stopped first, as Stop stops it, and the session lock stays held until dir is removed. Once the session lock is free,
// Clear removes dir with everything in it. A dir that does not exist is already empty, so Clear returns nil. When ctx
// ends while the lock is still held, Clear returns an error that wraps ErrLocked and leaves dir in place.
func Clear(ctx context.Context, dir string) error {
	present, err := dirPresent(dir)
	if err != nil || !present {
		return err
	}
	lock, err := stopHolder(ctx, dir)
	if err != nil {
		// The controller of a holder that ended removes its directory, and can do so while this clear runs. A
		// directory gone by now is as cleared as this clear would leave it.
		if stillPresent, checkErr := dirPresent(dir); checkErr == nil && !stillPresent {
			return nil
		}
		return fmt.Errorf("holder: clear %s: %w", dir, err)
	}
	removeErr := os.RemoveAll(dir)
	if removeErr != nil {
		removeErr = fmt.Errorf("holder: remove %s: %w", dir, removeErr)
	}
	return errors.Join(removeErr, lock.Close())
}

// dirPresent reports whether dir exists. A dir that does not exist is reported as absent, not as an error.
func dirPresent(dir string) (bool, error) {
	_, err := os.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("holder: inspect %s: %w", dir, err)
	}
	return true, nil
}

// stopHolder stops the holder serving dir, as Stop does, and returns the session lock once it is free and taken. The
// caller holds the lock until it has left dir or removed it, so no holder starts there in between. The holder is killed
// only while ctx is live: a caller whose ctx has ended asked Stop to give up, not to kill.
func stopHolder(ctx context.Context, dir string) (*os.File, error) {
	sock, err := SocketPath(dir)
	if err != nil {
		return nil, err
	}
	due := answerDue(ctx)
	if stopErr := stopBounded(ctx, sock, due); stopErr != nil && ctx.Err() == nil {
		if err = killUnattachable(ctx, dir); err != nil {
			return nil, errors.Join(stopErr, err)
		}
	}
	return awaitLock(ctx, filepath.Join(dir, LockName))
}

// answerDue returns when a holder's answer to stop is due, which is the zero time when ctx has no deadline. The answer
// is due at the later of stopKillReserve before ctx's deadline and the share of the time left that stopAnswerDivisor
// names, so a holder that never answers still leaves time to be killed and for the lock to be seen free.
func answerDue(ctx context.Context) time.Time {
	deadline, ok := ctx.Deadline()
	if !ok {
		return time.Time{}
	}
	now := time.Now()
	left := deadline.Sub(now)
	return now.Add(max(left-stopKillReserve, left/stopAnswerDivisor))
}

// stopBounded dials the holder on sock, sends it stop and waits for it to answer stopped. due bounds each step: the dial,
// the hello the holder answers, and the wait for stopped. A zero due leaves them unbounded. Dial waits for the hello with
// no deadline, so the exchange runs here, where the connection's deadline is set.
func stopBounded(ctx context.Context, sock string, due time.Time) error {
	dialer := net.Dialer{Deadline: due}
	raw, err := dialer.DialContext(ctx, "unix", sock)
	if err != nil {
		return fmt.Errorf("holder: dial %s: %w", sock, err)
	}
	conn := &Conn{conn: raw, frames: NewFrameReader(raw), controllerVersion: WireVersion}
	if err = raw.SetDeadline(due); err != nil {
		return conn.closeAfter(fmt.Errorf("holder: set deadline on %s: %w", sock, err))
	}
	if err = conn.greet(); err != nil {
		return conn.closeAfter(err)
	}
	return conn.Stop()
}

// killUnattachable SIGKILLs the holder that dir's pids record names, and then the child it records, when the session lock
// is still held. It does nothing when the lock is free, when dir holds no pids record, or when /proc does not confirm the
// recorded start of the holder, so it signals no process that the record does not name.
func killUnattachable(ctx context.Context, dir string) error {
	held, err := lockHeld(filepath.Join(dir, LockName))
	if err != nil || !held {
		return err
	}
	pids, err := ReadPids(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	running, err := Running(pids.Holder, pids.HolderStart)
	if err != nil || !running {
		return err
	}
	if err = syscall.Kill(pids.Holder, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("holder: kill holder %d: %w", pids.Holder, err)
	}
	return StopOrphan(ctx, dir)
}

// lockHeld reports whether the session lock at path is held. A lock that is free is taken and released again. A lock
// file that does not exist is no holder: lockHeld reports it not held and never creates it.
func lockHeld(path string) (bool, error) {
	file, err := openLock(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("holder: open lock %s: %w", path, err)
	}
	lock, err := takeLock(file, path)
	if errors.Is(err, ErrLocked) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if closeErr := lock.Close(); closeErr != nil {
		return false, fmt.Errorf("holder: release lock %s: %w", path, closeErr)
	}
	return false, nil
}

// awaitLock takes the session lock at path. While another holder still holds it, awaitLock retries every
// clearLockRetry. When ctx ends first, it returns the last ErrLocked error.
func awaitLock(ctx context.Context, path string) (*os.File, error) {
	for {
		lock, err := Lock(path)
		if err == nil {
			return lock, nil
		}
		if !errors.Is(err, ErrLocked) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, err
		case <-SystemClock{}.After(clearLockRetry):
		}
	}
}

// awaitExistingLock takes the session lock at path without creating its file, so a wait on a holder's end never
// recreates a lock that is gone. A lock file that does not exist is ErrGone, and so is one removed while it waits.
// While another holder still holds the lock, awaitExistingLock retries every clearLockRetry. When ctx ends first, it
// returns the last ErrLocked error.
func awaitExistingLock(ctx context.Context, path string) (*os.File, error) {
	for {
		file, err := openLock(path)
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ErrGone
		}
		if err != nil {
			return nil, fmt.Errorf("holder: open lock %s: %w", path, err)
		}
		lock, err := takeLock(file, path)
		if errors.Is(err, ErrLocked) {
			select {
			case <-ctx.Done():
				return nil, err
			case <-SystemClock{}.After(clearLockRetry):
			}
			continue
		}
		if err != nil {
			return nil, err
		}
		named, err := namesLock(path, lock)
		if err != nil {
			return nil, errors.Join(err, lock.Close())
		}
		if named {
			return lock, nil
		}
		// The lock was removed, or a newer lock file took its place, while this wait ran. Open the path again: a
		// missing file is ErrGone, and a newer lock is waited on in turn.
		if closeErr := lock.Close(); closeErr != nil {
			return nil, fmt.Errorf("holder: release lock %s: %w", path, closeErr)
		}
	}
}

// namesLock reports whether path still names the file that lock has open. A path that does not exist names no file.
func namesLock(path string, lock *os.File) (bool, error) {
	held, err := lock.Stat()
	if err != nil {
		return false, fmt.Errorf("holder: inspect lock %s: %w", path, err)
	}
	current, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("holder: inspect lock %s: %w", path, err)
	}
	return os.SameFile(held, current), nil
}
