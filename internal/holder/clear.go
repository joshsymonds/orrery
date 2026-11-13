package holder

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// clearLockRetry is how often Clear retries the session lock while a stopped holder still holds it.
const clearLockRetry = 10 * time.Millisecond

// Clear empties the session holder directory dir so a new holder can start there. A holder serving dir's socket is
// stopped first. Once the session lock is free, Clear removes dir with everything in it. A dir that does not exist is
// already empty, so Clear returns nil. When ctx ends while the lock is still held, Clear returns an error that wraps
// ErrLocked and leaves dir in place.
func Clear(ctx context.Context, dir string) error {
	if _, err := os.Lstat(dir); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("holder: inspect %s: %w", dir, err)
	}
	sock, err := SocketPath(dir)
	if err != nil {
		return err
	}
	// A dial that fails means no holder serves sock, so there is nothing to stop.
	if conn, dialErr := Dial(ctx, sock); dialErr == nil {
		if err = conn.Stop(); err != nil {
			return fmt.Errorf("holder: clear %s: stop holder: %w", dir, err)
		}
	}
	lock, err := awaitLock(ctx, filepath.Join(dir, LockName))
	if err != nil {
		return fmt.Errorf("holder: clear %s: %w", dir, err)
	}
	removeErr := os.RemoveAll(dir)
	if removeErr != nil {
		removeErr = fmt.Errorf("holder: remove %s: %w", dir, removeErr)
	}
	return errors.Join(removeErr, lock.Close())
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
