package holder

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"syscall"

	"github.com/joshsymonds/orrery/internal/store"
)

const (
	// LockName is the file a holder locks for its lifetime.
	LockName = "holder.lock"
	// SocketName is the unix socket a holder listens on.
	SocketName = "holder.sock"
	// ExitName is the record a holder writes when its child exits.
	ExitName = "exit.json"

	holdersName = "holders"
	// maxSocketPath is the sun_path size in bytes, including its terminating NUL.
	maxSocketPath = 108
	holderDirMode = 0o700
)

var (
	// ErrBadSession means a session name is not a safe single path element.
	ErrBadSession = errors.New("holder: bad session name")
	// ErrSocketPathTooLong means the socket path does not fit in sun_path.
	ErrSocketPathTooLong = errors.New("holder: socket path too long")
	// ErrUnsafeDir means an existing holder directory is not a private directory of this user.
	ErrUnsafeDir = errors.New("holder: unsafe holder directory")
)

var sessionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// HoldersDir returns the holders directory: holders inside the directory that holds the store file. It returns
// the store path's error when neither XDG_STATE_HOME nor HOME is set.
func HoldersDir(getenv func(string) string) (string, error) {
	path, err := store.Path(getenv)
	if err != nil {
		return "", fmt.Errorf("holders dir: %w", err)
	}
	return filepath.Join(filepath.Dir(path), holdersName), nil
}

// SessionDir returns the directory of one session's holder files under holders. It returns ErrBadSession when
// the session name is not a single safe path element.
func SessionDir(holders, session string) (string, error) {
	if !sessionPattern.MatchString(session) {
		return "", fmt.Errorf("%w: %q", ErrBadSession, session)
	}
	return filepath.Join(holders, session), nil
}

// SocketPath returns the socket path inside dir. It returns ErrSocketPathTooLong when the path does not fit in
// sun_path.
func SocketPath(dir string) (string, error) {
	path := filepath.Join(dir, SocketName)
	if len(path) >= maxSocketPath {
		return "", fmt.Errorf("%w: %s is %d bytes, limit is %d", ErrSocketPathTooLong, path, len(path), maxSocketPath-1)
	}
	return path, nil
}

// PrepareDir creates dir with mode 0700, creating its parent with MkdirAll. An existing dir is accepted only when
// it is a directory and not a symlink, its permission bits are exactly 0700, and this user owns it. Otherwise it
// returns ErrUnsafeDir.
func PrepareDir(dir string) error {
	if err := os.MkdirAll(filepath.Dir(dir), holderDirMode); err != nil {
		return fmt.Errorf("create holders parent of %s: %w", dir, err)
	}
	mkErr := os.Mkdir(dir, holderDirMode)
	if mkErr == nil {
		// Chmod after Mkdir so the umask cannot widen or narrow the mode.
		if err := os.Chmod(dir, holderDirMode); err != nil {
			return fmt.Errorf("set mode of %s: %w", dir, err)
		}
		return nil
	}
	if !errors.Is(mkErr, fs.ErrExist) {
		return fmt.Errorf("create holder dir %s: %w", dir, mkErr)
	}
	return checkPrivateDir(dir)
}

// checkPrivateDir accepts dir only when it is a directory, not a symlink, with mode 0700 owned by this user.
func checkPrivateDir(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("inspect holder dir %s: %w", dir, err)
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("%w: %s is a symlink", ErrUnsafeDir, dir)
	}
	if !info.IsDir() {
		return fmt.Errorf("%w: %s is not a directory", ErrUnsafeDir, dir)
	}
	if perm := info.Mode().Perm(); perm != holderDirMode {
		return fmt.Errorf("%w: %s has mode %04o, want 0700", ErrUnsafeDir, dir, perm)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("%w: %s has no readable owner", ErrUnsafeDir, dir)
	}
	if int(st.Uid) != os.Getuid() {
		return fmt.Errorf("%w: %s is owned by uid %d, not %d", ErrUnsafeDir, dir, st.Uid, os.Getuid())
	}
	return nil
}
