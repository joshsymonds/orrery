package build

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// moveWip is the ref the move tests keep a worktree's uncommitted work in.
const moveWip = "refs/orrery/epic/wip/T1"

// moveWorld is a repository with a committer identity, a base holding thing.go, an implement worktree on task branch
// T1.1 at the base, and a new base on it that adds theirs.go.
type moveWorld struct {
	t                          *testing.T
	repo, epic, path, base, to string
}

// newMoveWorld returns a move world.
func newMoveWorld(t *testing.T) moveWorld {
	t.Helper()
	setup := newStartSetup(t)
	repo := setup.epic.Repository
	identify(t, repo)
	base := commitOff(t, repo, setup.base, "thing.go", "package thing\n")
	path := filepath.Join(t.TempDir(), "implement")
	if err := AddTaskWorktree(context.Background(), repo, setup.epic.ID, 1, path, base); err != nil {
		t.Fatal(err)
	}
	return moveWorld{
		t:    t,
		repo: repo,
		epic: setup.epic.ID,
		path: path,
		base: base,
		to:   commitOff(t, repo, base, "theirs.go", "package theirs\n"),
	}
}

// work leaves a commit of mine.go on the worktree's branch, then an uncommitted change to thing.go, a staged new file
// and an untracked one. It returns the commit of mine.go.
func (w moveWorld) work() string {
	w.t.Helper()
	writeFile(w.t, filepath.Join(w.path, "mine.go"), "package mine\n")
	buildRun(w.t, w.path, "add", "mine.go")
	buildRun(w.t, w.path, "commit", "-q", "-m", "mine")
	mine := buildHead(w.t, w.path)
	writeFile(w.t, filepath.Join(w.path, "thing.go"), "package thing // changed\n")
	writeFile(w.t, filepath.Join(w.path, "staged.go"), "package staged\n")
	buildRun(w.t, w.path, "add", "staged.go")
	writeFile(w.t, filepath.Join(w.path, "draft.go"), "package draft\n")
	return mine
}

// move moves the worktree onto to and fails the test on an error.
func (w moveWorld) move(to string) {
	w.t.Helper()
	if err := MoveTaskWorktree(context.Background(), w.path, moveWip, w.epic, 1, to); err != nil {
		w.t.Fatalf("MoveTaskWorktree: %v", err)
	}
}

// requireMerged fails the test unless the worktree is on task 1's second branch, whose commit merges the work into
// onto: its first parent is onto and its second parent holds the work, and the task's first branch still names earlier.
// The files hold the work as committed, with nothing uncommitted, the move's ref is gone and nothing is on the stash.
func (w moveWorld) requireMerged(earlier, onto string) {
	w.t.Helper()
	ctx := context.Background()
	branch := TaskBranchRef(w.epic, 1, 2)
	if got := buildRun(w.t, w.path, "rev-parse", "--symbolic-full-name", "HEAD"); got != branch {
		w.t.Errorf("worktree HEAD is attached to %s, want %s", got, branch)
	}
	if parent := buildRun(w.t, w.path, "rev-parse", "HEAD^1"); parent != onto {
		w.t.Errorf("HEAD^1 %s, want the new base %s", parent, onto)
	}
	if held, err := isAncestor(ctx, w.path, earlier, "HEAD^2"); err != nil || !held {
		w.t.Errorf("HEAD^2 does not hold %s: %v", earlier, err)
	}
	if got := buildRun(w.t, w.repo, "rev-parse", TaskBranchRef(w.epic, 1, 1)); got != earlier {
		w.t.Errorf("%s = %s, want it still at %s", TaskBranchRef(w.epic, 1, 1), got, earlier)
	}
	for name, want := range map[string]string{
		"thing.go": "package thing // changed\n", "staged.go": "package staged\n", "draft.go": "package draft\n",
		"mine.go": "package mine\n", "theirs.go": "package theirs\n",
	} {
		if data, err := os.ReadFile(filepath.Join(w.path, name)); err != nil || string(data) != want {
			w.t.Errorf("%s = %q, %v, want %q", name, data, err, want)
		}
	}
	if status := buildRun(w.t, w.path, "status", "--porcelain"); status != "" {
		w.t.Errorf("status %q, want the work committed", status)
	}
	if got := buildRun(w.t, w.repo, "for-each-ref", moveWip); got != "" {
		w.t.Errorf("%s = %q after the move, want it deleted", moveWip, got)
	}
	if got := buildRun(w.t, w.repo, "stash", "list"); got != "" {
		w.t.Errorf("stash list = %q, want the move to leave the stash alone", got)
	}
}

// TestMoveTaskWorktreeMergesWorkOntoTheNextBranch: a move of a worktree with a commit and uncommitted work cuts
// task 1's second branch at the new base, merged with the work. A second move onto the same base, whose HEAD already
// reaches it, changes nothing.
func TestMoveTaskWorktreeMergesWorkOntoTheNextBranch(t *testing.T) {
	t.Parallel()
	w := newMoveWorld(t)
	mine := w.work()
	w.move(w.to)
	w.requireMerged(mine, w.to)
	w.move(w.to)
	w.requireMerged(mine, w.to)
}

// TestMoveTaskWorktreeOfUncommittedWorkMergesOntoNewBase: a move of a worktree with only uncommitted work merges that
// work onto the new base on the next branch, and the first branch keeps its commit.
func TestMoveTaskWorktreeOfUncommittedWorkMergesOntoNewBase(t *testing.T) {
	t.Parallel()
	w := newMoveWorld(t)
	writeFile(t, filepath.Join(w.path, "thing.go"), "package thing // changed\n")
	writeFile(t, filepath.Join(w.path, "draft.go"), "package draft\n")
	w.move(w.to)
	if got := buildRun(t, w.path, "rev-parse", "--symbolic-full-name", "HEAD"); got != TaskBranchRef(w.epic, 1, 2) {
		t.Errorf("worktree HEAD is attached to %s, want %s", got, TaskBranchRef(w.epic, 1, 2))
	}
	if parent := buildRun(t, w.path, "rev-parse", "HEAD^1"); parent != w.to {
		t.Errorf("HEAD^1 %s, want the new base %s", parent, w.to)
	}
	if got := buildRun(t, w.repo, "rev-parse", TaskBranchRef(w.epic, 1, 1)); got != w.base {
		t.Errorf("%s = %s, want it still at the base %s", TaskBranchRef(w.epic, 1, 1), got, w.base)
	}
	if status := buildRun(t, w.path, "status", "--porcelain"); status != "" {
		t.Errorf("status %q, want the work committed", status)
	}
	ladderCarryFiles(t, w.path, map[string]string{
		"thing.go": "package thing // changed\n", "draft.go": "package draft\n", "theirs.go": "package theirs\n",
	})
}

// TestMoveTaskWorktreeAfterAnUnstoredMoveMergesOntoTheLaterBase: a move that cut task 1's second branch, whose build was
// never stored, then moves onto a later base cuts the third branch there, merged with the work the second branch holds,
// and the second branch keeps its commit.
func TestMoveTaskWorktreeAfterAnUnstoredMoveMergesOntoTheLaterBase(t *testing.T) {
	t.Parallel()
	w := newMoveWorld(t)
	w.work()
	w.move(w.to)
	second := buildHead(t, w.path)
	later := commitOff(t, w.repo, w.to, "later.go", "package later\n")
	w.move(later)
	if got := buildRun(t, w.path, "rev-parse", "--symbolic-full-name", "HEAD"); got != TaskBranchRef(w.epic, 1, 3) {
		t.Errorf("worktree HEAD is attached to %s, want %s", got, TaskBranchRef(w.epic, 1, 3))
	}
	if parent := buildRun(t, w.path, "rev-parse", "HEAD^1"); parent != later {
		t.Errorf("HEAD^1 %s, want the later base %s", parent, later)
	}
	if held, err := isAncestor(context.Background(), w.path, second, "HEAD^2"); err != nil || !held {
		t.Errorf("HEAD^2 does not hold %s: %v", second, err)
	}
	if got := buildRun(t, w.repo, "rev-parse", TaskBranchRef(w.epic, 1, 2)); got != second {
		t.Errorf("%s = %s, want it still at %s", TaskBranchRef(w.epic, 1, 2), got, second)
	}
	ladderCarryFiles(t, w.path, map[string]string{
		"thing.go": "package thing // changed\n", "draft.go": "package draft\n", "mine.go": "package mine\n",
		"theirs.go": "package theirs\n", "later.go": "package later\n",
	})
}

// TestMoveTaskWorktreeFinishesAMoveCutShort: a move cut after its snapshot, or after the worktree is detached at it, is
// finished by the next move, with the work as a whole move leaves it.
func TestMoveTaskWorktreeFinishesAMoveCutShort(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cuts := map[string]func(w moveWorld){
		"after the snapshot": func(w moveWorld) {
			if _, err := snapshotWorktree(ctx, w.path, moveWip); err != nil {
				t.Fatal(err)
			}
		},
		"after the worktree is detached at the snapshot": func(w moveWorld) {
			snapshot, err := snapshotWorktree(ctx, w.path, moveWip)
			if err != nil {
				t.Fatal(err)
			}
			if err = enterSnapshot(ctx, w.path, snapshot); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, cut := range cuts {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			w := newMoveWorld(t)
			w.t = t
			mine := w.work()
			cut(w)
			w.move(w.to)
			w.requireMerged(mine, w.to)
		})
	}
}

// TestMoveTaskWorktreeOfADetachedWorktreeCutsTheNextBranch: a worktree detached from its task branch is moved like any
// other: its commit and work are merged onto the new base on the next branch, and the branch it was detached from keeps
// its commit.
func TestMoveTaskWorktreeOfADetachedWorktreeCutsTheNextBranch(t *testing.T) {
	t.Parallel()
	w := newMoveWorld(t)
	buildRun(t, w.path, "checkout", "-q", "--detach")
	w.work()
	w.move(w.to)
	w.requireMerged(w.base, w.to)
}

// TestMoveTaskWorktreeRefusesAConflictingMergeAndKeepsTheWork: a merge that conflicts is refused with the worktree and
// its branch as they were. The move's ref keeps the work as its snapshot, as a refused move always did.
func TestMoveTaskWorktreeRefusesAConflictingMergeAndKeepsTheWork(t *testing.T) {
	t.Parallel()
	w := newMoveWorld(t)
	writeFile(t, filepath.Join(w.path, "theirs.go"), "package mine\n")
	buildRun(t, w.path, "add", "theirs.go")
	buildRun(t, w.path, "commit", "-q", "-m", "mine")
	mine := buildHead(t, w.path)
	writeFile(t, filepath.Join(w.path, "draft.go"), "package draft\n")
	before := buildRun(t, w.path, "status", "--porcelain")

	err := MoveTaskWorktree(context.Background(), w.path, moveWip, w.epic, 1, w.to)
	if err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("MoveTaskWorktree = %v, want the refused merge", err)
	}
	if got := buildRun(t, w.path, "rev-parse", "--symbolic-full-name", "HEAD"); got != TaskBranchRef(w.epic, 1, 1) {
		t.Errorf("worktree HEAD is attached to %s after a refused merge, want %s", got, TaskBranchRef(w.epic, 1, 1))
	}
	if got := buildRun(t, w.repo, "rev-parse", TaskBranchRef(w.epic, 1, 1)); got != mine {
		t.Errorf("%s = %s after a refused merge, want it still at %s", TaskBranchRef(w.epic, 1, 1), got, mine)
	}
	if got := buildRun(t, w.repo, "for-each-ref", TaskBranchRef(w.epic, 1, 2)); got != "" {
		t.Errorf("%s exists after a refused merge, want no next branch", TaskBranchRef(w.epic, 1, 2))
	}
	if data, readErr := os.ReadFile(filepath.Join(w.path, "theirs.go")); readErr != nil ||
		string(data) != "package mine\n" {
		t.Errorf("theirs.go = %q, %v, want the worktree's own version", data, readErr)
	}
	if status := buildRun(t, w.path, "status", "--porcelain"); status != before {
		t.Errorf("status %q after a refused merge, want %q as it was", status, before)
	}
	if got := buildRun(t, w.repo, "for-each-ref", "--format=%(objectname)", moveWip); got == "" {
		t.Error("the move's ref is gone after a refused merge, want it kept as the snapshot of the work")
	}
}

// TestMoveTaskWorktreeRefusedWriteLeavesHeadOnItsBranch: a move git cannot finish, because the worktree's index is
// locked, leaves HEAD on the branch it was on, and returns the refusal.
func TestMoveTaskWorktreeRefusedWriteLeavesHeadOnItsBranch(t *testing.T) {
	t.Parallel()
	w := newMoveWorld(t)
	mine := w.work()
	gitDir := buildRun(t, w.path, "rev-parse", "--path-format=absolute", "--git-dir")
	writeFile(t, filepath.Join(gitDir, "index.lock"), "")

	if err := MoveTaskWorktree(context.Background(), w.path, moveWip, w.epic, 1, w.to); err == nil {
		t.Fatal("MoveTaskWorktree = nil, want the refused write")
	}
	if got := buildRun(t, w.path, "rev-parse", "--symbolic-full-name", "HEAD"); got != TaskBranchRef(w.epic, 1, 1) {
		t.Errorf("worktree HEAD is attached to %s after a refused write, want %s", got, TaskBranchRef(w.epic, 1, 1))
	}
	if got := buildRun(t, w.repo, "rev-parse", TaskBranchRef(w.epic, 1, 1)); got != mine {
		t.Errorf("%s = %s after a refused write, want it still at %s", TaskBranchRef(w.epic, 1, 1), got, mine)
	}
	if data, err := os.ReadFile(filepath.Join(w.path, "draft.go")); err != nil || string(data) != "package draft\n" {
		t.Errorf("draft.go = %q, %v, want the work kept in the worktree", data, err)
	}
}

func TestMoveTaskWorktreeOfAMissingPathMovesNothing(t *testing.T) {
	t.Parallel()
	if err := MoveTaskWorktree(
		context.Background(), filepath.Join(t.TempDir(), "gone"), moveWip, testEpic, 1, "b",
	); err != nil {
		t.Errorf("MoveTaskWorktree of a missing path = %v, want nil", err)
	}
}
