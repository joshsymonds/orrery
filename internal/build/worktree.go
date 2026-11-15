package build

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// revParseMissing is the exit status of `git rev-parse --verify --quiet` for a name that does not resolve.
const revParseMissing = 1

// CreateBranch creates epic's branch at base. The empty old value makes git refuse when the branch already exists,
// so an existing branch is never moved.
func CreateBranch(ctx context.Context, repo, epic, base string) error {
	if _, err := runGit(ctx, repo, "update-ref", BranchRef(epic), base, ""); err != nil {
		return fmt.Errorf("create branch: %w", err)
	}
	return nil
}

// BranchTip returns the commit epic's branch names, and false with no error when the branch does not exist.
func BranchTip(ctx context.Context, repo, epic string) (string, bool, error) {
	tip, err := runGit(ctx, repo, "rev-parse", "--verify", "--quiet", BranchRef(epic)+"^{commit}")
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == revParseMissing {
			return "", false, nil
		}
		return "", false, fmt.Errorf("read branch tip: %w", err)
	}
	return tip, true, nil
}

// BranchCheckedOut reports whether any worktree of repo has epic's branch checked out.
func BranchCheckedOut(ctx context.Context, repo, epic string) (bool, error) {
	listing, err := runGit(ctx, repo, "worktree", "list", "--porcelain")
	if err != nil {
		return false, fmt.Errorf("list worktrees: %w", err)
	}
	return slices.Contains(strings.Split(listing, "\n"), "branch "+BranchRef(epic)), nil
}

// AddWorktree adds a detached worktree of repo at path, checked out at base. A path that is already a worktree of
// repo is left as it is and returns nil. A registration whose directory is gone is not a worktree, so git refuses it
// and names the stale registration.
func AddWorktree(ctx context.Context, repo, path, base string) error {
	abs, err := resolvePath(path)
	if err != nil {
		return err
	}
	listing, err := runGit(ctx, repo, "worktree", "list", "--porcelain")
	if err != nil {
		return fmt.Errorf("list worktrees: %w", err)
	}
	if slices.Contains(strings.Split(listing, "\n"), "worktree "+abs) {
		if _, err = os.Stat(abs); err == nil {
			return nil
		}
	}
	if _, err = runGit(ctx, repo, "worktree", "add", "--detach", abs, base); err != nil {
		return fmt.Errorf("add worktree: %w", err)
	}
	return nil
}

// RemoveWorktrees removes every worktree whose path is an entry of dir, then removes dir. A missing dir is nil. An
// entry that is not a worktree makes git refuse it, so nothing that is not a worktree is deleted.
func RemoveWorktrees(ctx context.Context, repo, dir string) error {
	root, err := resolvePath(dir)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read worktree directory: %w", err)
	}
	for _, entry := range entries {
		if _, err = runGit(ctx, repo, "worktree", "remove", "--force", filepath.Join(root, entry.Name())); err != nil {
			return fmt.Errorf("remove worktree: %w", err)
		}
	}
	// Each worktree's directory went with it, so dir is empty; os.Remove refuses anything else.
	if err = os.Remove(root); err != nil {
		return fmt.Errorf("remove worktree directory: %w", err)
	}
	return nil
}

// Pin points ref at commit. It refuses a ref outside refs/orrery/, the only refs orreryd pins.
func Pin(ctx context.Context, repo, ref, commit string) error {
	if !strings.HasPrefix(ref, refsOrreryPrefix) {
		return fmt.Errorf("pin %s: not under %s", ref, refsOrreryPrefix)
	}
	if _, err := runGit(ctx, repo, "update-ref", ref, commit); err != nil {
		return fmt.Errorf("pin %s: %w", ref, err)
	}
	return nil
}

// resolvePath returns path as an absolute path with its symlinks resolved, the form git records a worktree under. A
// path that does not exist yet keeps its absolute form.
func resolvePath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("absolute path of %s: %w", path, err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if errors.Is(err, fs.ErrNotExist) {
		return abs, nil
	}
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", abs, err)
	}
	return resolved, nil
}
