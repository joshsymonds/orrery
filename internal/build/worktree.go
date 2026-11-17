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

// wipSubject is the message of the commit a move keeps a worktree's uncommitted work in while the move runs.
const wipSubject = "orreryd: uncommitted work kept across a move"

// MoveWorktree moves the implement worktree at path onto to, keeping its work, committed or not. A path that does not
// exist holds no work. With no move in flight, a worktree whose HEAD to already reaches was moved before and changes
// nothing. Otherwise the move runs as steps that each leave the worktree in a state the next call recognizes, so a move
// cut at any point is finished, never started again, by the next call:
//
//  1. snapshot: the worktree's whole state, untracked files included, is committed on HEAD through a scratch index
//     (wipSubject), and wip, a ref of the task's own, is created at that commit. The worktree is not touched.
//  2. enter: HEAD is set to the snapshot and the index to its tree. The files do not change, since they are its tree.
//  3. replay: the commits from the newest commit that both HEAD and to reach, up to the snapshot, are replayed onto to
//     with `git rebase --onto`, the snapshot last. A replay git refuses is aborted, which returns the worktree to the
//     snapshot, and is the error.
//  4. unwrap: HEAD moves back off the replayed snapshot, keeping its files as uncommitted work.
//  5. finish: the index is read from HEAD and wip is deleted.
//
// The base the commits are replayed from is read from the worktree, not from the build, so a move after an earlier
// move that was never stored replays only the worktree's own commits.
func MoveWorktree(ctx context.Context, path, wip, to string) error {
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	snapshot, err := refCommit(ctx, path, wip)
	if err != nil {
		return err
	}
	if snapshot == "" {
		head, headErr := runGit(ctx, path, "rev-parse", "HEAD")
		if headErr != nil {
			return fmt.Errorf("read worktree head: %w", headErr)
		}
		moved, ancestorErr := isAncestor(ctx, path, to, head)
		if ancestorErr != nil || moved {
			return ancestorErr
		}
		if snapshot, err = snapshotWorktree(ctx, path, wip); err != nil {
			return err
		}
	}
	return resumeMove(ctx, path, wip, snapshot, to)
}

// resumeMove finishes the move of the worktree at path onto to, whose snapshot is the commit wip names, from whichever
// step of MoveWorktree the worktree is at. A move that ends finished an earlier move to another commit than to starts
// again from step 1.
func resumeMove(ctx context.Context, path, wip, snapshot, to string) error {
	if err := abortRebase(ctx, path); err != nil {
		return err
	}
	head, err := runGit(ctx, path, "rev-parse", "HEAD")
	if err != nil {
		return fmt.Errorf("read worktree head: %w", err)
	}
	parent, err := runGit(ctx, path, "rev-parse", snapshot+"^")
	if err != nil {
		return fmt.Errorf("read snapshot parent: %w", err)
	}
	subject, err := runGit(ctx, path, "log", "-1", "--format=%s", "HEAD")
	if err != nil {
		return fmt.Errorf("read worktree head subject: %w", err)
	}
	switch {
	case head == parent:
		if enterErr := enterSnapshot(ctx, path, snapshot); enterErr != nil {
			return enterErr
		}
		fallthrough
	case head == snapshot:
		if replayErr := replaySnapshot(ctx, path, snapshot, to); replayErr != nil {
			return replayErr
		}
		fallthrough
	case subject == wipSubject:
		if _, resetErr := runGit(ctx, path, "reset", "-q", "HEAD^"); resetErr != nil {
			return fmt.Errorf("unwrap uncommitted work: %w", resetErr)
		}
	}
	if finishErr := finishMove(ctx, path, wip, snapshot); finishErr != nil {
		return finishErr
	}
	return MoveWorktree(ctx, path, wip, to)
}

// refCommit returns the commit ref names in the repository of the worktree at path, and "" when there is no such ref.
func refCommit(ctx context.Context, path, ref string) (string, error) {
	commit, err := runGit(ctx, path, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == revParseMissing {
			return "", nil
		}
		return "", fmt.Errorf("read %s: %w", ref, err)
	}
	return commit, nil
}

// snapshotWorktree commits the whole state of the worktree at path, untracked files included and ignored files left
// out, on its HEAD through a scratch index, and creates wip at that commit. Neither the worktree nor its index changes.
func snapshotWorktree(ctx context.Context, path, wip string) (snapshot string, err error) {
	scratch, err := os.MkdirTemp("", "orrery-move-")
	if err != nil {
		return "", fmt.Errorf("create scratch index directory: %w", err)
	}
	defer func() {
		if removeErr := os.RemoveAll(scratch); removeErr != nil && err == nil {
			snapshot, err = "", fmt.Errorf("remove scratch index directory: %w", removeErr)
		}
	}()
	env := []string{"GIT_INDEX_FILE=" + filepath.Join(scratch, "index")}
	if _, err = runGitEnv(ctx, path, env, "read-tree", "HEAD"); err != nil {
		return "", fmt.Errorf("read head into scratch index: %w", err)
	}
	if _, err = runGitEnv(ctx, path, env, "add", "--all"); err != nil {
		return "", fmt.Errorf("stage uncommitted work: %w", err)
	}
	tree, err := runGitEnv(ctx, path, env, "write-tree")
	if err != nil {
		return "", fmt.Errorf("write uncommitted work: %w", err)
	}
	snapshot, err = runGit(ctx, path, "commit-tree", tree, "-p", "HEAD", "-m", wipSubject)
	if err != nil {
		return "", fmt.Errorf("commit uncommitted work: %w", err)
	}
	if _, err = runGit(ctx, path, "update-ref", wip, snapshot, ""); err != nil {
		return "", fmt.Errorf("keep uncommitted work at %s: %w", wip, err)
	}
	return snapshot, nil
}

// enterSnapshot points the worktree's HEAD at snapshot and reads its index from it. The files already are its tree.
func enterSnapshot(ctx context.Context, path, snapshot string) error {
	if _, err := runGit(ctx, path, "update-ref", "--no-deref", "HEAD", snapshot); err != nil {
		return fmt.Errorf("enter snapshot: %w", err)
	}
	if _, err := runGit(ctx, path, "reset", "-q"); err != nil {
		return fmt.Errorf("read snapshot index: %w", err)
	}
	return nil
}

// replaySnapshot replays the commits of the worktree at path, from the newest commit that both snapshot and to reach up
// to snapshot, onto to. Commits that start or end empty are kept, so the snapshot is always the last. A replay git
// refuses is aborted, which returns the worktree to snapshot, and is the error.
func replaySnapshot(ctx context.Context, path, snapshot, to string) error {
	if _, err := runGit(ctx, path, "reset", "-q"); err != nil {
		return fmt.Errorf("read snapshot index: %w", err)
	}
	base, err := runGit(ctx, path, "merge-base", snapshot, to)
	if err != nil {
		return fmt.Errorf("find the worktree's base: %w", err)
	}
	if _, err = runGit(ctx, path, "rebase", "--empty=keep", "--onto", to, base); err != nil {
		if abortErr := abortRebase(ctx, path); abortErr != nil {
			return fmt.Errorf("move commits: %w; %w", err, abortErr)
		}
		return fmt.Errorf("move commits: %w", err)
	}
	return nil
}

// abortRebase aborts a rebase the worktree at path holds in progress, and does nothing when it holds none.
func abortRebase(ctx context.Context, path string) error {
	for _, name := range []string{"rebase-merge", "rebase-apply"} {
		dir, err := runGit(ctx, path, "rev-parse", "--path-format=absolute", "--git-path", name)
		if err != nil {
			return fmt.Errorf("find rebase state: %w", err)
		}
		if _, err = os.Stat(dir); err == nil {
			if _, err = runGit(ctx, path, "rebase", "--abort"); err != nil {
				return fmt.Errorf("abort rebase: %w", err)
			}
			return nil
		}
	}
	return nil
}

// finishMove reads the index of the worktree at path from its HEAD and deletes wip while it names snapshot.
func finishMove(ctx context.Context, path, wip, snapshot string) error {
	if _, err := runGit(ctx, path, "reset", "-q"); err != nil {
		return fmt.Errorf("read moved index: %w", err)
	}
	if _, err := runGit(ctx, path, "update-ref", "-d", wip, snapshot); err != nil {
		return fmt.Errorf("delete %s: %w", wip, err)
	}
	return nil
}

// WipRef returns the ref a move of task seq of epic keeps the task's uncommitted work in while it runs.
func WipRef(epic string, seq int64) string {
	return refsOrreryPrefix + Slug(epic) + "/wip/" + taskLabel(seq)
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
