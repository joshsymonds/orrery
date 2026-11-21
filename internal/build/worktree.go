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
	"strconv"
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

// TaskBranchRef returns the full name of the branch implementer attempt k of task seq of epic is committed to:
// refs/heads/orrery/<epic slug>/T<seq>.<k>.
func TaskBranchRef(epic string, seq, k int64) string {
	return taskBranchPrefix(epic, seq) + decimal(k)
}

// taskBranchPrefix is the full name every task branch of task seq of epic starts with, before its attempt k.
func taskBranchPrefix(epic string, seq int64) string {
	return taskBranchesPrefix + Slug(epic) + "/" + taskLabel(seq) + "."
}

// AddTaskWorktree adds a worktree of repo at path on a task branch of task seq of epic, and returns nil. A path that
// is already a worktree of repo is left as it is, so a later attempt, rerun or rung keeps the branch its first attempt
// made. Otherwise the branch is attempt k, one above the highest attempt of the task's existing task branches (1 when
// it has none). It is created create-only at base, so an existing branch is never moved, and the worktree is then
// checked out on it. Git's worktree add reads a full ref as a commit and detaches, so the worktree is added detached at
// the branch and its HEAD is made symbolic to the branch's full ref. A start cut short between the branch and the
// worktree leaves an unused branch, and one cut between the add and the symbolic ref leaves a detached worktree at the
// base.
func AddTaskWorktree(ctx context.Context, repo, epic string, seq int64, path, base string) error {
	abs, err := resolvePath(path)
	if err != nil {
		return err
	}
	exists, err := isWorktreeAt(ctx, repo, abs)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	ref, err := nextTaskBranch(ctx, repo, epic, seq)
	if err != nil {
		return err
	}
	if _, err = runGit(ctx, repo, "update-ref", ref, base, ""); err != nil {
		return fmt.Errorf("create task branch: %w", err)
	}
	if _, err = runGit(ctx, repo, "worktree", "add", "--detach", abs, ref); err != nil {
		return fmt.Errorf("add worktree: %w", err)
	}
	if _, err = runGit(ctx, abs, "symbolic-ref", "HEAD", ref); err != nil {
		return fmt.Errorf("check out task branch: %w", err)
	}
	return nil
}

// isWorktreeAt reports whether abs, a resolved path, is a worktree of repo whose directory exists. A registration whose
// directory is gone is not a worktree, so the add that follows refuses it and names the stale registration.
func isWorktreeAt(ctx context.Context, repo, abs string) (bool, error) {
	listing, err := runGit(ctx, repo, "worktree", "list", "--porcelain")
	if err != nil {
		return false, fmt.Errorf("list worktrees: %w", err)
	}
	if !slices.Contains(strings.Split(listing, "\n"), "worktree "+abs) {
		return false, nil
	}
	_, statErr := os.Stat(abs)
	return statErr == nil, nil
}

// nextTaskBranch returns the full name of the task branch the next attempt of task seq of epic is committed to: one
// above the highest attempt of the task's existing task branches, or attempt 1 when it has none. A ref under the epic's
// task branches whose name after the task's prefix is not a decimal attempt is not an attempt, and is skipped.
func nextTaskBranch(ctx context.Context, repo, epic string, seq int64) (string, error) {
	listing, err := runGit(ctx, repo, "for-each-ref", "--format=%(refname)", taskBranchesPrefix+Slug(epic)+"/")
	if err != nil {
		return "", fmt.Errorf("list task branches: %w", err)
	}
	prefix := taskBranchPrefix(epic, seq)
	var highest int64
	for name := range strings.SplitSeq(listing, "\n") {
		attempt, found := strings.CutPrefix(name, prefix)
		if !found || attempt == "" || strings.Trim(attempt, "0123456789") != "" {
			continue
		}
		k, parseErr := strconv.ParseInt(attempt, 10, 64)
		if parseErr != nil {
			return "", fmt.Errorf("read task branch %s: %w", name, parseErr)
		}
		highest = max(highest, k)
	}
	return TaskBranchRef(epic, seq, highest+1), nil
}

// wipSubject is the message of the commit that keeps a worktree's whole state while a move runs. The commit is the
// second parent of the merge a move makes, so the work it holds stays in the task's history.
const wipSubject = "orreryd: uncommitted work kept across a move"

// mergeConflict is the exit status merge-tree --write-tree gives a merge that conflicts.
const mergeConflict = 1

// MoveTaskWorktree moves the implement worktree at path onto to, keeping its work, committed or not, and gives that
// work a new task branch of task seq of epic that holds it merged into to. A path that does not exist holds no work,
// and nothing moves. A worktree whose HEAD already reaches to was moved before, and changes nothing. Otherwise the move
// runs as these steps, each of which git either finishes or refuses:
//
//  1. snapshot: a rebase left in progress is aborted (abortRebase), and wip is cleared. Then the worktree's whole state,
//     untracked files included, is committed on HEAD through a scratch index (snapshotWorktree), and wip, a ref of the
//     task's own, is set at that commit. The worktree is not touched.
//  2. merge: the snapshot is merged into to with merge-tree, without a checkout (mergeWork). A merge git refuses
//     returns the error, and moves no ref but wip.
//  3. cut: the task's next branch, one attempt above its highest existing one, is created create-only at to, then
//     advanced to the merge commit, whose first parent is to and whose second parent is the snapshot.
//  4. attach: HEAD is detached at the snapshot, the index and files are read from the merge, and HEAD is made symbolic
//     to the new branch by its full ref (attachBranch).
//  5. finish: wip is deleted while it names the snapshot.
//
// No task branch is rebased, reset or deleted: the branch the worktree was on keeps its commit.
func MoveTaskWorktree(ctx context.Context, path, wip, epic string, seq int64, to string) error {
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err := abortRebase(ctx, path); err != nil {
		return err
	}
	// The snapshot is taken afresh, so a wip left by an earlier move that did not finish is replaced, not reused.
	if _, err := runGit(ctx, path, "update-ref", "-d", wip); err != nil {
		return fmt.Errorf("clear %s: %w", wip, err)
	}
	reached, err := isAncestor(ctx, path, to, headRef)
	if err != nil || reached {
		return err
	}
	snapshot, err := snapshotWorktree(ctx, path, wip)
	if err != nil {
		return err
	}
	merge, err := mergeWork(ctx, path, to, snapshot, fmt.Sprintf("orreryd: %s moved onto %s", taskLabel(seq), to))
	if err != nil {
		return err
	}
	ref, err := nextTaskBranch(ctx, path, epic, seq)
	if err != nil {
		return err
	}
	if _, err = runGit(ctx, path, "update-ref", ref, to, ""); err != nil {
		return fmt.Errorf("create task branch: %w", err)
	}
	if _, err = runGit(ctx, path, "update-ref", ref, merge, to); err != nil {
		return fmt.Errorf("advance task branch: %w", err)
	}
	err = attachBranch(ctx, path, ref, merge, snapshot)
	if err != nil {
		return err
	}
	if _, err = runGit(ctx, path, "update-ref", "-d", wip, snapshot); err != nil {
		return fmt.Errorf("delete %s: %w", wip, err)
	}
	return nil
}

// mergeWork writes the merge of snapshot into to, with to as its first parent and snapshot as its second, and returns
// its commit. It writes the merge's tree without a checkout. A merge that conflicts is an error, and writes no ref.
func mergeWork(ctx context.Context, path, to, snapshot, message string) (string, error) {
	out, err := runGit(ctx, path, "merge-tree", "--write-tree", "--no-messages", "--name-only", to, snapshot)
	var exit *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exit) && exit.ExitCode() == mergeConflict:
		return "", fmt.Errorf("the work conflicts with %s", to)
	default:
		return "", fmt.Errorf("merge the work into %s: %w", to, err)
	}
	tree, _, _ := strings.Cut(out, "\n")
	merge, err := runGit(ctx, path, "commit-tree", tree, "-p", to, "-p", snapshot, "-m", message)
	if err != nil {
		return "", fmt.Errorf("commit the merge: %w", err)
	}
	return merge, nil
}

// attachBranch puts the worktree at path on ref, whose commit is merge, with its index and files read from merge. HEAD
// is detached at snapshot first, the commit the files are at, so the branch HEAD was on keeps its commit. When git
// refuses to update the files, HEAD goes back where it was, and the refusal is returned.
func attachBranch(ctx context.Context, path, ref, merge, snapshot string) error {
	branch, err := runGit(ctx, path, "rev-parse", "--symbolic-full-name", "HEAD")
	if err != nil {
		return fmt.Errorf("read worktree head branch: %w", err)
	}
	if branch == headRef {
		branch = ""
	}
	head, err := runGit(ctx, path, "rev-parse", "HEAD")
	if err != nil {
		return fmt.Errorf("read worktree head: %w", err)
	}
	if err = enterSnapshot(ctx, path, snapshot); err != nil {
		return errors.Join(err, restoreHead(ctx, path, branch, head))
	}
	if _, err = runGit(ctx, path, "reset", "-q", "--keep", merge); err != nil {
		return errors.Join(fmt.Errorf("update files to %s: %w", merge, err), restoreHead(ctx, path, branch, head))
	}
	if _, err = runGit(ctx, path, "symbolic-ref", "HEAD", ref); err != nil {
		return fmt.Errorf("check out %s: %w", ref, err)
	}
	return nil
}

// restoreHead sets HEAD of the worktree at path back to branch, or to head when branch is "" because HEAD was detached,
// and reads the index from it. The worktree's files stay as they are, so they are uncommitted work.
func restoreHead(ctx context.Context, path, branch, head string) error {
	var err error
	if branch != "" {
		_, err = runGit(ctx, path, "symbolic-ref", "HEAD", branch)
	} else {
		_, err = runGit(ctx, path, "update-ref", "--no-deref", "HEAD", head)
	}
	if err != nil {
		return fmt.Errorf("restore HEAD: %w", err)
	}
	if _, err = runGit(ctx, path, "reset", "-q"); err != nil {
		return fmt.Errorf("restore index: %w", err)
	}
	return nil
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

// WipRef returns the ref a move of task seq of epic keeps the task's uncommitted work in while it runs.
func WipRef(epic string, seq int64) string {
	return refsOrreryPrefix + Slug(epic) + "/wip/" + taskLabel(seq)
}

// CarryRef returns the ref a task's kept work is kept on when a climb to rung of task seq of epic cannot carry the
// work onto the epic's branch. The ref is named for the task and the rung the climb lands on, so each carry keeps its
// own work.
func CarryRef(epic string, seq int64, rung int64) string {
	return refsOrreryPrefix + Slug(epic) + "/carry/" + taskLabel(seq) + "-" + decimal(rung)
}

// KeepWork keeps the whole work of the implement worktree at path on ref, and returns the commit that holds it. A path
// that does not exist holds no work, and returns "" with no error. The work is the commit wip names when a move of the
// worktree was cut short, and otherwise a snapshot of the worktree taken as MoveTaskWorktree takes one
// (snapshotWorktree):
//
//  1. snapshot: the commit wip names is the snapshot, or one is taken (snapshotWorktree), which creates wip at it.
//  2. abort: a rebase left in progress is aborted (abortRebase), which returns the worktree to the snapshot.
//  3. keep: ref is set to the snapshot.
//  4. unwrap: when HEAD is the snapshot, HEAD moves back off it with reset -q HEAD^, keeping its files as uncommitted
//     work.
//  5. finish: wip is deleted while it names the snapshot.
//
// Afterwards the worktree is on the commit it was on before the move, with its work in place.
func KeepWork(ctx context.Context, path, wip, ref string) (string, error) {
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	snapshot, err := refCommit(ctx, path, wip)
	if err != nil {
		return "", err
	}
	if snapshot == "" {
		if snapshot, err = snapshotWorktree(ctx, path, wip); err != nil {
			return "", err
		}
	}
	if abortErr := abortRebase(ctx, path); abortErr != nil {
		return "", abortErr
	}
	if _, err = runGit(ctx, path, "update-ref", ref, snapshot); err != nil {
		return "", fmt.Errorf("keep work at %s: %w", ref, err)
	}
	head, err := runGit(ctx, path, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("read worktree head: %w", err)
	}
	if head == snapshot {
		if _, err = runGit(ctx, path, "reset", "-q", "HEAD^"); err != nil {
			return "", fmt.Errorf("unwrap kept work: %w", err)
		}
	}
	if _, err = runGit(ctx, path, "update-ref", "-d", wip, snapshot); err != nil {
		return "", fmt.Errorf("delete %s: %w", wip, err)
	}
	return snapshot, nil
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
