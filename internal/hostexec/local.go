package hostexec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// Local is the Host for this machine: git, the file system and the session holders run directly on it.
type Local struct{}

// Compile-time check that *Local is a Host.
var _ Host = (*Local)(nil)

// NewLocal returns the Host for this machine.
func NewLocal() *Local {
	return &Local{}
}

// Git runs git -C call.Dir call.Args... with argv and no shell, in the host's environment plus call.Env. It feeds
// call.Stdin to git and captures git's stdout and stderr. A Remote call disables git's prompt, runs in a process group
// of its own, and has that group killed when ctx ends. A non-zero exit or a kill returns a *GitError; a git that could
// not start returns a wrapped error that is not one.
func (*Local) Git(ctx context.Context, call GitCall) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git")
	cmd.Args = append(cmd.Args, "-C", call.Dir)
	cmd.Args = append(cmd.Args, call.Args...)
	cmd.Env = append(os.Environ(), call.Env...)
	cmd.Stdin = bytes.NewReader(call.Stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.WaitDelay = call.WaitDelay
	if call.Remote {
		cmd.Env = append(cmd.Env, "GIT_TERMINAL_PROMPT=0")
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	}
	err := cmd.Run()
	out := stdout.Bytes()
	if err == nil {
		return out, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return out, &GitError{
			Args:     call.Args,
			ExitCode: exitErr.ExitCode(),
			Stdout:   out,
			Stderr:   stderr.Bytes(),
			Err:      err,
		}
	}
	return out, fmt.Errorf("hostexec: git %s: %w", strings.Join(call.Args, " "), err)
}

// ReadFile reads the file at path.
func (*Local) ReadFile(_ context.Context, path string) ([]byte, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("hostexec: read file: %w", err)
	}
	return data, nil
}

// ReadRooted reads the regular file name inside root through an os.Root. A symlink, and any file that is not regular,
// fails with an error matching ErrNotRegular.
func (*Local) ReadRooted(_ context.Context, root, name string) ([]byte, error) {
	dir, err := os.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("hostexec: open root: %w", err)
	}
	data, err := readRegularIn(dir, name)
	if closeErr := dir.Close(); closeErr != nil {
		return nil, errors.Join(err, fmt.Errorf("hostexec: close root: %w", closeErr))
	}
	return data, err
}

// readRegularIn reads the regular file name in root. It checks the name with Lstat, so a symlink is refused, then opens
// it and checks that the open file is the one it checked, so a name swapped for a symlink in between is refused too.
func readRegularIn(root *os.Root, name string) ([]byte, error) {
	before, err := root.Lstat(name)
	if err != nil {
		return nil, fmt.Errorf("hostexec: stat %s: %w", name, err)
	}
	if !before.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: %s", ErrNotRegular, name)
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, fmt.Errorf("hostexec: open %s: %w", name, err)
	}
	data, err := readOpened(file, before)
	if closeErr := file.Close(); closeErr != nil {
		return nil, errors.Join(err, fmt.Errorf("hostexec: close %s: %w", name, closeErr))
	}
	return data, err
}

// readOpened reads the open file that must be the file before describes, and is regular.
func readOpened(file *os.File, before fs.FileInfo) ([]byte, error) {
	after, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("hostexec: stat open file: %w", err)
	}
	if !os.SameFile(before, after) || !after.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: %s changed as it was opened", ErrNotRegular, file.Name())
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("hostexec: read %s: %w", file.Name(), err)
	}
	return data, nil
}

// Stat describes the file at path, following symlinks.
func (*Local) Stat(_ context.Context, path string) (fs.FileInfo, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("hostexec: stat: %w", err)
	}
	return info, nil
}

// Lstat describes the file at path without following a final symlink.
func (*Local) Lstat(_ context.Context, path string) (fs.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("hostexec: lstat: %w", err)
	}
	return info, nil
}

// ReadDir lists the directory at path.
func (*Local) ReadDir(_ context.Context, path string) ([]fs.DirEntry, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, fmt.Errorf("hostexec: read dir: %w", err)
	}
	return entries, nil
}

// WriteFile writes data to the file at path, creating it with perm when it is missing.
func (*Local) WriteFile(_ context.Context, path string, data []byte, perm fs.FileMode) error {
	if err := os.WriteFile(path, data, perm); err != nil {
		return fmt.Errorf("hostexec: write file: %w", err)
	}
	return nil
}

// Symlink makes link a symbolic link to target.
func (*Local) Symlink(_ context.Context, target, link string) error {
	if err := os.Symlink(target, link); err != nil {
		return fmt.Errorf("hostexec: symlink: %w", err)
	}
	return nil
}

// MkdirTemp makes a new directory in dir, named from pattern as os.MkdirTemp names it, and returns its path.
func (*Local) MkdirTemp(_ context.Context, dir, pattern string) (string, error) {
	made, err := os.MkdirTemp(dir, pattern)
	if err != nil {
		return "", fmt.Errorf("hostexec: mkdir temp: %w", err)
	}
	return made, nil
}

// MkdirAll makes path and any missing parents of it.
func (*Local) MkdirAll(_ context.Context, path string, perm fs.FileMode) error {
	if err := os.MkdirAll(path, perm); err != nil {
		return fmt.Errorf("hostexec: mkdir: %w", err)
	}
	return nil
}

// Remove removes the file or empty directory at path.
func (*Local) Remove(_ context.Context, path string) error {
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("hostexec: remove: %w", err)
	}
	return nil
}

// RemoveAll removes path and everything under it. A path that does not exist is not an error.
func (*Local) RemoveAll(_ context.Context, path string) error {
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("hostexec: remove all: %w", err)
	}
	return nil
}

// EvalSymlinks returns path with every symlink in it resolved.
func (*Local) EvalSymlinks(_ context.Context, path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("hostexec: eval symlinks: %w", err)
	}
	return resolved, nil
}
