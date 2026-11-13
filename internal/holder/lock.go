package holder

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// ErrLocked means another holder holds the session's lock.
var ErrLocked = errors.New("another holder holds the session's lock")

// lockFileMode is the mode of the session's lock file: only the holder's user may open it.
const lockFileMode os.FileMode = 0o600

// Lock opens or creates the session's lock file at path and takes an exclusive, non-blocking flock on it. The
// holder keeps the returned file open for its life, and closing it frees the lock. When another open file holds
// the lock, Lock closes its own file and returns ErrLocked.
func Lock(path string) (*os.File, error) {
	file, err := os.OpenFile(filepath.Clean(path), os.O_RDWR|os.O_CREATE, lockFileMode)
	if err != nil {
		return nil, fmt.Errorf("holder: open lock %s: %w", path, err)
	}
	if err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		closeErr := file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errors.Join(ErrLocked, closeErr)
		}
		return nil, errors.Join(fmt.Errorf("holder: lock %s: %w", path, err), closeErr)
	}
	return file, nil
}
