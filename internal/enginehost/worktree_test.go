package enginehost

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stepWorktreeTestWorktrees returns the output of git worktree list --porcelain in dir.
func stepWorktreeTestWorktrees(t *testing.T, dir string) string {
	t.Helper()
	return stepGitTestRun(t, dir, "worktree", "list", "--porcelain")
}

// stepWorktreeTestAddLinked adds a detached linked worktree of repo at a fresh path under t.TempDir() and returns
// the path with symlinks resolved, the form git lists it under.
func stepWorktreeTestAddLinked(t *testing.T, repo string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "linked")
	stepGitTestRun(t, repo, "worktree", "add", "--detach", path, "HEAD")
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func TestCheckWritingWorkspaceRefusesNonGitDirectory(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	err := checkWritingWorkspace(context.Background(), dir)
	if !errors.Is(err, ErrNotGit) {
		t.Fatalf("checkWritingWorkspace(non-git %s) = %v, want ErrNotGit", dir, err)
	}
}

func TestCheckWritingWorkspaceRefusesMainWorkingTree(t *testing.T) {
	t.Parallel()
	repo := stepGitTestRepo(t)
	sub := filepath.Join(repo, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, dir := range []string{repo, sub} {
		err := checkWritingWorkspace(context.Background(), dir)
		if !errors.Is(err, ErrMainWorktree) {
			t.Fatalf("checkWritingWorkspace(main tree dir %s) = %v, want ErrMainWorktree", dir, err)
		}
	}
}

func TestCheckWritingWorkspaceAcceptsLinkedWorktree(t *testing.T) {
	t.Parallel()
	repo := stepGitTestRepo(t)
	linked := stepWorktreeTestAddLinked(t, repo)
	sub := filepath.Join(linked, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, dir := range []string{linked, sub} {
		if err := checkWritingWorkspace(context.Background(), dir); err != nil {
			t.Fatalf("checkWritingWorkspace(linked worktree dir %s) = %v, want nil", dir, err)
		}
	}
}

func TestAddStepWorktreeDetachesAtRevisionAndLeavesMainBranch(t *testing.T) {
	t.Parallel()
	repo := stepGitTestRepo(t)
	revision := stepGitTestRun(t, repo, "rev-parse", "HEAD")
	stepGitTestCommit(t, repo, map[string]string{"next.txt": "later\n"})
	mainHead := stepGitTestRun(t, repo, "rev-parse", "HEAD")
	path := filepath.Join(t.TempDir(), "step")

	if err := addStepWorktree(context.Background(), repo, path, revision); err != nil {
		t.Fatalf("addStepWorktree: %v", err)
	}

	if got := stepGitTestRun(t, path, "rev-parse", "HEAD"); got != revision {
		t.Fatalf("worktree HEAD = %s, want revision %s", got, revision)
	}
	if got := stepGitTestRun(t, path, "rev-parse", "--abbrev-ref", "HEAD"); got != "HEAD" {
		t.Fatalf("worktree --abbrev-ref HEAD = %q, want detached HEAD", got)
	}
	if got := stepGitTestRun(t, repo, "symbolic-ref", "--short", "HEAD"); got != "main" {
		t.Fatalf("main tree branch = %q, want main", got)
	}
	if got := stepGitTestRun(t, repo, "rev-parse", "HEAD"); got != mainHead {
		t.Fatalf("main tree HEAD = %s, want unchanged %s", got, mainHead)
	}
}

func TestRemoveStepWorktreeRemovesDirtyWorktreeAndIsIdempotent(t *testing.T) {
	t.Parallel()
	repo := stepGitTestRepo(t)
	revision := stepGitTestRun(t, repo, "rev-parse", "HEAD")
	path := filepath.Join(t.TempDir(), "step")
	if err := addStepWorktree(context.Background(), repo, path, revision); err != nil {
		t.Fatalf("addStepWorktree: %v", err)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	stepGitTestWrite(t, path, "uncommitted.txt", "dirty\n")

	if err := removeStepWorktree(context.Background(), repo, path); err != nil {
		t.Fatalf("removeStepWorktree: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("worktree directory still present after remove: %v", err)
	}
	if list := stepWorktreeTestWorktrees(t, repo); strings.Contains(list, resolved) {
		t.Fatalf("git worktree list still names %s:\n%s", resolved, list)
	}
	if err := removeStepWorktree(context.Background(), repo, path); err != nil {
		t.Fatalf("second removeStepWorktree = %v, want nil", err)
	}
}

func TestRemoveStepWorktreeLeavesMainTreeAndUnregisteredDirectory(t *testing.T) {
	t.Parallel()
	repo := stepGitTestRepo(t)
	unregistered := t.TempDir()
	stepGitTestWrite(t, unregistered, "keep.txt", "keep\n")

	if err := removeStepWorktree(context.Background(), repo, repo); err == nil {
		t.Fatal("removeStepWorktree(main tree) = nil, want error")
	}
	if _, err := os.Stat(filepath.Join(repo, "README")); err != nil {
		t.Fatalf("main tree was touched: %v", err)
	}

	if err := removeStepWorktree(context.Background(), repo, unregistered); err == nil {
		t.Fatal("removeStepWorktree(unregistered dir) = nil, want error")
	}
	if _, err := os.Stat(filepath.Join(unregistered, "keep.txt")); err != nil {
		t.Fatalf("unregistered directory was touched: %v", err)
	}
}
