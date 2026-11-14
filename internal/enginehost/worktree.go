package enginehost

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrNotGit is returned when a directory is outside any git work tree.
var ErrNotGit = errors.New("not inside a git work tree")

// ErrMainWorktree is returned when a directory is inside a repository's main working tree.
var ErrMainWorktree = errors.New("inside the main working tree of a repository")

// checkWritingWorkspace returns nil when dir is inside a linked worktree, where a writing step may run. It returns
// ErrNotGit outside any work tree and ErrMainWorktree inside a repository's main working tree. A main tree has
// the same git dir and common dir; a linked worktree has its own git dir under the common one.
func checkWritingWorkspace(ctx context.Context, dir string) error {
	inside, err := stepGit(ctx, dir, "rev-parse", "--is-inside-work-tree")
	if err != nil {
		return fmt.Errorf("%w: %s: %w", ErrNotGit, dir, err)
	}
	if inside != "true" {
		return fmt.Errorf("%w: %s", ErrNotGit, dir)
	}
	dirs, err := stepGit(ctx, dir, "rev-parse", "--path-format=absolute", "--git-dir", "--git-common-dir")
	if err != nil {
		return fmt.Errorf("resolve git dirs of %s: %w", dir, err)
	}
	gitDir, commonDir, ok := strings.Cut(dirs, "\n")
	if !ok {
		return fmt.Errorf("resolve git dirs of %s: unexpected output %q", dir, dirs)
	}
	if gitDir == commonDir {
		return fmt.Errorf("%w: %s", ErrMainWorktree, dir)
	}
	return nil
}

// addStepWorktree adds a detached worktree at path, checked out at revision, to the repository containing repoDir.
// The revision is resolved to a commit first so a name that starts with a dash is never read as a git option.
func addStepWorktree(ctx context.Context, repoDir, path, revision string) error {
	commit, err := stepGitResolve(ctx, repoDir, revision)
	if err != nil {
		return fmt.Errorf("resolve revision %q: %w", revision, err)
	}
	if _, err = stepGit(ctx, repoDir, "worktree", "add", "--detach", path, commit); err != nil {
		return fmt.Errorf("add step worktree at %s: %w", path, err)
	}
	return nil
}

// removeStepWorktree removes the worktree at path even when it has local changes, and drops git's record of it.
// git refuses to remove the main working tree or a directory that is not a worktree, so only that path is touched.
// When git refuses and path is absent, the worktree is already gone and the call returns nil.
func removeStepWorktree(ctx context.Context, repoDir, path string) error {
	_, err := stepGit(ctx, repoDir, "worktree", "remove", "--force", path)
	if err == nil {
		return nil
	}
	absent, checkErr := stepPathAbsent(path)
	if absent {
		return nil
	}
	return fmt.Errorf("remove step worktree %s: %w", path, errors.Join(err, checkErr))
}

// stepPathAbsent reports whether nothing is at path, not even a symbolic link. The check reads only the last element of
// path, through an os.Root of its parent directory, so it stays inside that directory. An empty path is absent, as
// os.Lstat reports it. It returns an error only when the root fails to close.
func stepPathAbsent(path string) (bool, error) {
	if path == "" {
		return true, nil
	}
	path = filepath.Clean(path)
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return errors.Is(err, os.ErrNotExist), nil
	}
	_, err = root.Lstat(filepath.Base(path))
	if closeErr := root.Close(); closeErr != nil {
		return false, fmt.Errorf("close root of %s: %w", path, closeErr)
	}
	return errors.Is(err, os.ErrNotExist), nil
}
