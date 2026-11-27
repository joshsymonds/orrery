package node

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
)

// maxSymlinks bounds how many symlinks resolvePath follows in one path, so that a symlink loop is an error and not a hang.
const maxSymlinks = 40

// errOutside is the error of a path that lies outside the agent's allowed directories, or is not absolute. An op that
// names such a path is refused with CodeRefused, and nothing runs.
var errOutside = errors.New("node: path lies outside the agent's roots")

// claudeFolderName is the folder Claude Code reads a directory's settings and agents from.
const claudeFolderName = ".claude"

// refusal returns the Response of an op whose check failed with err: CodeRefused when err names a path outside the
// agent's allowed directories, and CodeFailed for any other error.
func refusal(err error) Response {
	if errors.Is(err, errOutside) {
		return Response{Code: CodeRefused, Message: err.Error()}
	}
	return failed(err)
}

// allowedDirs returns the directories an op may name: the agent's Roots, StateDir, ConfigDir and ClaudeDir, each resolved
// by resolvePath. An empty directory allows nothing, so it is left out.
func (a *Agent) allowedDirs() ([]string, error) {
	named := append([]string{a.cfg.StateDir, a.cfg.ConfigDir, a.cfg.ClaudeDir}, a.cfg.Roots...)
	var dirs []string
	for _, dir := range named {
		if dir == "" {
			continue
		}
		resolved, err := resolvePath(dir)
		if err != nil {
			return nil, fmt.Errorf("node: resolve the agent's directory %s: %w", dir, err)
		}
		dirs = append(dirs, resolved)
	}
	return dirs, nil
}

// checkPaths returns nil when every path lies under one of the agent's allowed directories once its symlinks resolve. A
// path that is not absolute, or that lies outside all of them, is refused with an error that wraps errOutside.
func (a *Agent) checkPaths(paths ...string) error {
	allowed, err := a.allowedDirs()
	if err != nil {
		return err
	}
	for _, path := range paths {
		if checkErr := checkResolved(
			path,
			func(resolved string) bool { return underAny(allowed, resolved) },
		); checkErr != nil {
			return checkErr
		}
	}
	return nil
}

// checkRead returns nil when the read-only op may name path. It may name what checkPaths allows, and also the places
// Claude Code reads its settings and agents from for a process in an allowed directory: each folder above an allowed
// directory, which the op may stat, lstat or resolve but not read or list; anything under such a folder's .claude; and
// anything under the agent's ManagedDir. A controller resolving a step's agents and stamp reads these, and none of them
// is written. Any other path is refused with an error that wraps errOutside.
func (a *Agent) checkRead(op FileOp, path string) error {
	allowed, err := a.allowedDirs()
	if err != nil {
		return err
	}
	var managed string
	if a.cfg.ManagedDir != "" {
		if managed, err = resolvePath(a.cfg.ManagedDir); err != nil {
			return fmt.Errorf("node: resolve the agent's directory %s: %w", a.cfg.ManagedDir, err)
		}
	}
	return checkResolved(path, func(resolved string) bool {
		return underAny(allowed, resolved) || (managed != "" && within(managed, resolved)) ||
			claudeAbove(allowed, op, resolved)
	})
}

// claudeAbove reports whether the read-only op may name resolved as a place Claude Code reads above one of the allowed
// dirs: one of the folders above it, when op only stats or resolves, or anything under such a folder's .claude.
func claudeAbove(allowed []string, op FileOp, resolved string) bool {
	metadata := op == FileStat || op == FileLstat || op == FileEvalSymlinks
	for _, dir := range allowed {
		for above := filepath.Dir(dir); ; above = filepath.Dir(above) {
			if (metadata && resolved == above) || within(filepath.Join(above, claudeFolderName), resolved) {
				return true
			}
			if above == filepath.Dir(above) {
				break
			}
		}
	}
	return false
}

// checkResolved returns nil when path is absolute and allow admits it once its symlinks resolve. Any other path is
// refused with an error that wraps errOutside, and a path that cannot be resolved is that error.
func checkResolved(path string, allow func(resolved string) bool) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("%w: %q is not absolute", errOutside, path)
	}
	resolved, err := resolvePath(path)
	if err != nil {
		return err
	}
	if !allow(resolved) {
		return fmt.Errorf("%w: %s resolves to %s", errOutside, path, resolved)
	}
	return nil
}

// underAny reports whether path lies under one of dirs.
func underAny(dirs []string, path string) bool {
	return slices.ContainsFunc(dirs, func(dir string) bool { return within(dir, path) })
}

// resolvePath returns the absolute path that path names once every symlink in it is followed. It resolves one component
// at a time, so a path whose last parts do not exist yet resolves as far as they exist, and the rest stays as written. A
// symlink is followed to its target even when that target does not exist.
func resolvePath(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("node: path %q is not absolute", path)
	}
	pending := strings.Split(path, "/")
	resolved := "/"
	links := 0
	for len(pending) > 0 {
		name := pending[0]
		pending = pending[1:]
		switch name {
		case "", ".":
			continue
		case "..":
			resolved = filepath.Dir(resolved)
			continue
		}
		next := filepath.Join(resolved, name)
		info, err := os.Lstat(next)
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
			return filepath.Clean(strings.Join(append([]string{next}, pending...), "/")), nil
		}
		if err != nil {
			return "", fmt.Errorf("node: inspect %s: %w", next, err)
		}
		if info.Mode()&fs.ModeSymlink == 0 {
			resolved = next
			continue
		}
		links++
		if links > maxSymlinks {
			return "", fmt.Errorf("node: too many symbolic links in %s", path)
		}
		target, err := os.Readlink(next)
		if err != nil {
			return "", fmt.Errorf("node: read the symbolic link %s: %w", next, err)
		}
		if filepath.IsAbs(target) {
			resolved = "/"
		}
		pending = append(strings.Split(target, "/"), pending...)
	}
	return resolved, nil
}
