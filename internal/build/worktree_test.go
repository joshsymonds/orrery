package build

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/hostexec"
)

// testEpic names the epic the worktree tests build branches for; its branch is epic/worktree.
const testEpic = "demo/worktree"

// buildWorktreeRoot returns a fresh directory with its symlinks resolved, the form git records a worktree path in.
func buildWorktreeRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve temp dir: %v", err)
	}
	return root
}

// buildListsWorktree reports whether git worktree list --porcelain in repo names path as a worktree.
func buildListsWorktree(t *testing.T, repo, path string) bool {
	t.Helper()
	listing := buildRun(t, repo, "worktree", "list", "--porcelain")
	return slices.Contains(strings.Split(listing, "\n"), "worktree "+path)
}

func TestCreateBranchCreatesAtBase(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := buildRepo(t)
	base := buildHead(t, dir)
	if err := CreateBranch(ctx, hostexec.NewLocal(), dir, testEpic, base); err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
	if got := buildRun(t, dir, "rev-parse", BranchRef(testEpic)); got != base {
		t.Fatalf("branch at %s, want %s", got, base)
	}
}

func TestCreateBranchFailsAndKeepsExistingBranch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := buildRepo(t)
	base := buildHead(t, dir)
	tip := buildObject(t, dir, base)
	buildRun(t, dir, "update-ref", BranchRef(testEpic), base)
	if err := CreateBranch(ctx, hostexec.NewLocal(), dir, testEpic, tip); err == nil {
		t.Fatal("CreateBranch accepted a branch that already exists")
	}
	if got := buildRun(t, dir, "rev-parse", BranchRef(testEpic)); got != base {
		t.Fatalf("branch at %s after a refused create, want %s", got, base)
	}
}

func TestBranchTipMissingBranchIsNotFound(t *testing.T) {
	t.Parallel()
	dir := buildRepo(t)
	tip, found, err := BranchTip(context.Background(), hostexec.NewLocal(), dir, testEpic)
	if err != nil {
		t.Fatalf("BranchTip: %v", err)
	}
	if found || tip != "" {
		t.Fatalf("BranchTip of a missing branch = %q, %t; want \"\", false", tip, found)
	}
}

func TestBranchTipReadsCreatedBranch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := buildRepo(t)
	base := buildHead(t, dir)
	if err := CreateBranch(ctx, hostexec.NewLocal(), dir, testEpic, base); err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
	tip, found, err := BranchTip(ctx, hostexec.NewLocal(), dir, testEpic)
	if err != nil {
		t.Fatalf("BranchTip: %v", err)
	}
	if !found || tip != base {
		t.Fatalf("BranchTip = %q, %t; want %q, true", tip, found, base)
	}
}

func TestBranchCheckedOutFollowsLinkedWorktree(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := buildRepo(t)
	base := buildHead(t, dir)
	if err := CreateBranch(ctx, hostexec.NewLocal(), dir, testEpic, base); err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
	checked, err := BranchCheckedOut(ctx, hostexec.NewLocal(), dir, testEpic)
	if err != nil {
		t.Fatalf("BranchCheckedOut before checkout: %v", err)
	}
	if checked {
		t.Fatal("BranchCheckedOut reports a branch no worktree has checked out")
	}
	// The short branch name checks the branch out; a full refs/heads/ name would add a detached worktree.
	linked := filepath.Join(buildWorktreeRoot(t), "linked")
	buildRun(t, dir, "worktree", "add", "-q", linked, "epic/"+Slug(testEpic))
	checked, err = BranchCheckedOut(ctx, hostexec.NewLocal(), dir, testEpic)
	if err != nil {
		t.Fatalf("BranchCheckedOut after checkout: %v", err)
	}
	if !checked {
		t.Fatal("BranchCheckedOut misses a branch a linked worktree has checked out")
	}
}

func TestAddWorktreeAddsDetachedAtBase(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := buildRepo(t)
	base := buildHead(t, dir)
	path := filepath.Join(buildWorktreeRoot(t), "step")
	if err := AddWorktree(ctx, hostexec.NewLocal(), dir, path, base); err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}
	if got := buildRun(t, path, "rev-parse", "HEAD"); got != base {
		t.Fatalf("worktree HEAD at %s, want %s", got, base)
	}
	if got := buildRun(t, path, "rev-parse", "--symbolic-full-name", "HEAD"); got != "HEAD" {
		t.Fatalf("worktree HEAD is attached to %s, want detached", got)
	}
	if !buildListsWorktree(t, dir, path) {
		t.Fatalf("git worktree list does not name %s", path)
	}
}

func TestAddWorktreeSamePathIsNoOp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := buildRepo(t)
	base := buildHead(t, dir)
	tip := buildObject(t, dir, base)
	path := filepath.Join(buildWorktreeRoot(t), "step")
	if err := AddWorktree(ctx, hostexec.NewLocal(), dir, path, base); err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}
	if err := AddWorktree(ctx, hostexec.NewLocal(), dir, path, tip); err != nil {
		t.Fatalf("second AddWorktree on the same path: %v", err)
	}
	if got := buildRun(t, path, "rev-parse", "HEAD"); got != base {
		t.Fatalf("second AddWorktree moved the worktree to %s, want %s", got, base)
	}
}

func TestAddWorktreeRefusesRegistrationWithoutDirectory(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := buildRepo(t)
	base := buildHead(t, dir)
	path := filepath.Join(buildWorktreeRoot(t), "step")
	buildRun(t, dir, "worktree", "add", "-q", "--detach", path, base)
	if err := os.RemoveAll(path); err != nil {
		t.Fatalf("remove worktree directory: %v", err)
	}
	if err := AddWorktree(ctx, hostexec.NewLocal(), dir, path, base); err == nil {
		t.Fatal("AddWorktree reported a worktree whose directory is gone")
	}
}

func TestRemoveWorktreesRemovesEveryWorktreeAndDir(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := buildRepo(t)
	base := buildHead(t, dir)
	taskDir := filepath.Join(buildWorktreeRoot(t), "task")
	clean := filepath.Join(taskDir, "clean")
	dirty := filepath.Join(taskDir, "dirty")
	for _, path := range []string{clean, dirty} {
		if err := AddWorktree(ctx, hostexec.NewLocal(), dir, path, base); err != nil {
			t.Fatalf("AddWorktree %s: %v", path, err)
		}
	}
	// An untracked file makes git refuse a plain remove, so this worktree is removed only with --force.
	if err := os.WriteFile(filepath.Join(dirty, "scratch.txt"), []byte("left behind\n"), 0o600); err != nil {
		t.Fatalf("write scratch file: %v", err)
	}
	if err := RemoveWorktrees(ctx, hostexec.NewLocal(), dir, taskDir); err != nil {
		t.Fatalf("RemoveWorktrees: %v", err)
	}
	if _, err := os.Stat(taskDir); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("task directory after RemoveWorktrees: %v, want not exist", err)
	}
	for _, path := range []string{clean, dirty} {
		if buildListsWorktree(t, dir, path) {
			t.Fatalf("git worktree list still names %s", path)
		}
	}
}

func TestRemoveWorktreesMissingDirIsNil(t *testing.T) {
	t.Parallel()
	dir := buildRepo(t)
	missing := filepath.Join(buildWorktreeRoot(t), "absent")
	if err := RemoveWorktrees(context.Background(), hostexec.NewLocal(), dir, missing); err != nil {
		t.Fatalf("RemoveWorktrees of a missing directory: %v", err)
	}
}

func TestPinWritesOrreryRef(t *testing.T) {
	t.Parallel()
	dir := buildRepo(t)
	tip := buildObject(t, dir, buildHead(t, dir))
	if err := Pin(context.Background(), hostexec.NewLocal(), dir, "refs/orrery/demo/T1", tip); err != nil {
		t.Fatalf("Pin: %v", err)
	}
	if got := buildRun(t, dir, "rev-parse", "refs/orrery/demo/T1"); got != tip {
		t.Fatalf("refs/orrery/demo/T1 at %s, want %s", got, tip)
	}
}

func TestPinRefusesRefOutsideOrrery(t *testing.T) {
	t.Parallel()
	dir := buildRepo(t)
	base := buildHead(t, dir)
	if err := Pin(context.Background(), hostexec.NewLocal(), dir, "refs/heads/x", base); err == nil {
		t.Fatal("Pin accepted refs/heads/x")
	}
	if got := buildRun(t, dir, "for-each-ref", "refs/heads/x"); got != "" {
		t.Fatalf("refs/heads/x exists after a refused pin: %s", got)
	}
}
