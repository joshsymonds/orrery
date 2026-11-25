package enginehost

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/joshsymonds/orrery/internal/hostexec"
)

// ErrNotGit is returned when a directory is outside any git work tree.
var ErrNotGit = errors.New("not inside a git work tree")

// ErrMainWorktree is returned when a directory is inside a repository's main working tree.
var ErrMainWorktree = errors.New("inside the main working tree of a repository")

// checkWritingWorkspace returns nil when dir is inside a linked worktree on host, where a writing step may run. It
// returns ErrNotGit outside any work tree and ErrMainWorktree inside a repository's main working tree. A main tree has
// the same git dir and common dir; a linked worktree has its own git dir under the common one.
func checkWritingWorkspace(ctx context.Context, host hostexec.Host, dir string) error {
	inside, err := stepGit(ctx, host, dir, "rev-parse", "--is-inside-work-tree")
	if err != nil {
		return fmt.Errorf("%w: %s: %w", ErrNotGit, dir, err)
	}
	if inside != "true" {
		return fmt.Errorf("%w: %s", ErrNotGit, dir)
	}
	dirs, err := stepGit(ctx, host, dir, "rev-parse", "--path-format=absolute", "--git-dir", "--git-common-dir")
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

// addStepWorktree adds a detached worktree at path on host, checked out at revision, to the repository containing
// repoDir. The revision is resolved to a commit first so a name that starts with a dash is never read as a git option.
func addStepWorktree(ctx context.Context, host hostexec.Host, repoDir, path, revision string) error {
	commit, err := stepGitResolve(ctx, host, repoDir, revision)
	if err != nil {
		return fmt.Errorf("resolve revision %q: %w", revision, err)
	}
	if _, err = stepGit(ctx, host, repoDir, "worktree", "add", "--detach", path, commit); err != nil {
		return fmt.Errorf("add step worktree at %s: %w", path, err)
	}
	return nil
}

// removeStepWorktree removes the worktree at path on host even when it has local changes, and drops git's record of it.
// git refuses to remove the main working tree or a directory that is not a worktree, so only that path is touched.
// When git refuses and path is absent, the worktree is already gone and the call returns nil.
func removeStepWorktree(ctx context.Context, host hostexec.Host, repoDir, path string) error {
	_, err := stepGit(ctx, host, repoDir, "worktree", "remove", "--force", path)
	if err == nil {
		return nil
	}
	if stepPathAbsent(ctx, host, path) {
		return nil
	}
	return fmt.Errorf("remove step worktree %s: %w", path, err)
}

// clearUnlaunchedWorktree clears path on host, the worktree orrery names for one run or check under its own worktrees
// directory, before a restart starts that run again. A registered worktree, or nothing at path, is cleared as
// removeStepWorktree clears it. A start stopped inside git worktree add can leave a directory git does not list and
// that holds no .git entry, part of a checkout no git command can remove; only that start wrote it, so it is removed
// whole. Any other directory git does not list is refused, as removeStepWorktree refuses it.
func clearUnlaunchedWorktree(ctx context.Context, host hostexec.Host, repoDir, path string) error {
	err := removeStepWorktree(ctx, host, repoDir, path)
	if err == nil {
		return nil
	}
	half, checkErr := halfMadeWorktree(ctx, host, path)
	if checkErr != nil {
		return errors.Join(err, checkErr)
	}
	if !half {
		return err
	}
	if rmErr := host.RemoveAll(ctx, path); rmErr != nil {
		return fmt.Errorf("remove half-made worktree %s: %w", path, rmErr)
	}
	return nil
}

// halfMadeWorktree reports whether path on host is a directory, not a symbolic link, with no .git entry: what a git
// worktree add stopped part way through its checkout leaves.
func halfMadeWorktree(ctx context.Context, host hostexec.Host, path string) (bool, error) {
	info, err := host.Lstat(ctx, path)
	if err != nil {
		return false, fmt.Errorf("inspect %s: %w", path, err)
	}
	if !info.IsDir() {
		return false, nil
	}
	_, err = host.Lstat(ctx, filepath.Join(path, ".git"))
	if errors.Is(err, fs.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect %s: %w", filepath.Join(path, ".git"), err)
	}
	return false, nil
}

// stepPathAbsent reports whether nothing is at path on host, not even a symbolic link. It asks the host to Lstat path,
// so a final symbolic link is seen as present. An empty path is absent, as os.Lstat reports it. A path that cannot be
// described for any other reason is not absent.
func stepPathAbsent(ctx context.Context, host hostexec.Host, path string) bool {
	if path == "" {
		return true
	}
	_, err := host.Lstat(ctx, path)
	return errors.Is(err, fs.ErrNotExist)
}
