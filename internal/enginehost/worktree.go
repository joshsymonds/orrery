package enginehost

import (
	"context"
	"errors"
	"fmt"
	"os"
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
	if _, statErr := os.Lstat(path); errors.Is(statErr, os.ErrNotExist) {
		return nil
	}
	return fmt.Errorf("remove step worktree %s: %w", path, err)
}
