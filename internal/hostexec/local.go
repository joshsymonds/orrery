package hostexec

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
)

// Local is the Host for this machine. Its methods are stubs until Local is implemented: each returns an error matching
// errors.ErrUnsupported.
type Local struct{}

// Compile-time check that *Local is a Host.
var _ Host = (*Local)(nil)

// NewLocal returns the Host for this machine.
func NewLocal() *Local {
	return &Local{}
}

// unsupported returns the error a Local method returns for op until Local implements it.
func unsupported(op string) error {
	return fmt.Errorf("hostexec: local %s: %w", op, errors.ErrUnsupported)
}

// Git returns an error matching errors.ErrUnsupported until Local runs git.
func (*Local) Git(_ context.Context, _ GitCall) ([]byte, error) {
	return nil, unsupported("Git")
}

// ReadFile returns an error matching errors.ErrUnsupported until Local reads files.
func (*Local) ReadFile(_ context.Context, _ string) ([]byte, error) {
	return nil, unsupported("ReadFile")
}

// ReadRooted returns an error matching errors.ErrUnsupported until Local reads rooted files.
func (*Local) ReadRooted(_ context.Context, _, _ string) ([]byte, error) {
	return nil, unsupported("ReadRooted")
}

// Stat returns an error matching errors.ErrUnsupported until Local stats files.
func (*Local) Stat(_ context.Context, _ string) (fs.FileInfo, error) {
	return nil, unsupported("Stat")
}

// Lstat returns an error matching errors.ErrUnsupported until Local stats files without following symlinks.
func (*Local) Lstat(_ context.Context, _ string) (fs.FileInfo, error) {
	return nil, unsupported("Lstat")
}

// ReadDir returns an error matching errors.ErrUnsupported until Local lists directories.
func (*Local) ReadDir(_ context.Context, _ string) ([]fs.DirEntry, error) {
	return nil, unsupported("ReadDir")
}

// WriteFile returns an error matching errors.ErrUnsupported until Local writes files.
func (*Local) WriteFile(_ context.Context, _ string, _ []byte, _ fs.FileMode) error {
	return unsupported("WriteFile")
}

// Symlink returns an error matching errors.ErrUnsupported until Local makes symbolic links.
func (*Local) Symlink(_ context.Context, _, _ string) error {
	return unsupported("Symlink")
}

// MkdirTemp returns an error matching errors.ErrUnsupported until Local makes temporary directories.
func (*Local) MkdirTemp(_ context.Context, _, _ string) (string, error) {
	return "", unsupported("MkdirTemp")
}

// MkdirAll returns an error matching errors.ErrUnsupported until Local makes directories.
func (*Local) MkdirAll(_ context.Context, _ string, _ fs.FileMode) error {
	return unsupported("MkdirAll")
}

// Remove returns an error matching errors.ErrUnsupported until Local removes files.
func (*Local) Remove(_ context.Context, _ string) error {
	return unsupported("Remove")
}

// RemoveAll returns an error matching errors.ErrUnsupported until Local removes trees.
func (*Local) RemoveAll(_ context.Context, _ string) error {
	return unsupported("RemoveAll")
}

// EvalSymlinks returns an error matching errors.ErrUnsupported until Local resolves symlinks.
func (*Local) EvalSymlinks(_ context.Context, _ string) (string, error) {
	return "", unsupported("EvalSymlinks")
}

// SessionDir returns an error matching errors.ErrUnsupported until Local names session directories.
func (*Local) SessionDir(_, _ string) (string, error) {
	return "", unsupported("SessionDir")
}

// Launch returns an error matching errors.ErrUnsupported until Local launches holders.
func (*Local) Launch(_ context.Context, _ Launch) (Held, error) {
	return nil, unsupported("Launch")
}

// Reattach returns an error matching errors.ErrUnsupported until Local reattaches holders.
func (*Local) Reattach(_ context.Context, _, _ string) (Held, error) {
	return nil, unsupported("Reattach")
}

// Stop returns an error matching errors.ErrUnsupported until Local stops holders.
func (*Local) Stop(_ context.Context, _ string) error {
	return unsupported("Stop")
}

// Clear returns an error matching errors.ErrUnsupported until Local clears session directories.
func (*Local) Clear(_ context.Context, _ string) error {
	return unsupported("Clear")
}

// ExitRecord reports no record until Local reads exit records. It returns no error to report unsupported with, so it
// reports the same absence a directory without a record does.
func (*Local) ExitRecord(_ string) (Exit, bool) {
	return Exit{}, false
}

// KillLeftovers returns an error matching errors.ErrUnsupported until Local kills leftover processes.
func (*Local) KillLeftovers(_ string) error {
	return unsupported("KillLeftovers")
}
