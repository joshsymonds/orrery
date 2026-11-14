package enginehost

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// stepGitTestRepo makes a throwaway repository under t.TempDir() with one commit on main. The owner's git config
// never applies: TestMain points GIT_CONFIG_GLOBAL at /dev/null and the commands below pass identity and signing flags.
func stepGitTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	stepGitTestRun(t, dir, "init", "-q", "-b", "main")
	stepGitTestWrite(t, dir, "README", "base\n")
	stepGitTestRun(t, dir, "add", "README")
	stepGitTestRun(t, dir, "commit", "-q", "-m", "base")
	return dir
}

// stepGitTestRun runs git in dir with the test identity and fails the test on error.
func stepGitTestRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{
		"-c", "user.name=Test",
		"-c", "user.email=test@example.invalid",
		"-c", "commit.gpgsign=false",
	}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// stepGitTestWrite writes content to a repo-relative path, creating parent directories.
func stepGitTestWrite(t *testing.T, dir, rel, content string) {
	t.Helper()
	path := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// stepGitTestCommit stages every given repo-relative path with the given content and commits, returning the new HEAD.
func stepGitTestCommit(t *testing.T, dir string, files map[string]string) string {
	t.Helper()
	for rel, content := range files {
		stepGitTestWrite(t, dir, rel, content)
		stepGitTestRun(t, dir, "add", rel)
	}
	stepGitTestRun(t, dir, "commit", "-q", "-m", "change")
	return stepGitTestRun(t, dir, "rev-parse", "HEAD")
}

// stepGitTestCheck returns the named check from checks and fails the test if it is absent.
func stepGitTestCheck(t *testing.T, checks []stepCheck, name string) stepCheck {
	t.Helper()
	for _, c := range checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no check named %q in %+v", name, checks)
	return stepCheck{}
}

// stepGitTestOrder asserts the checks come back as head, ancestry, clean, owned.
func stepGitTestOrder(t *testing.T, checks []stepCheck) {
	t.Helper()
	want := []string{"head", "ancestry", "clean", "owned"}
	if len(checks) != len(want) {
		t.Fatalf("got %d checks, want %d: %+v", len(checks), len(want), checks)
	}
	for i, name := range want {
		if checks[i].Name != name {
			t.Fatalf("check %d is %q, want %q", i, checks[i].Name, name)
		}
	}
}

func TestStepGitCommitChecksAllPassOnOwnedCommit(t *testing.T) {
	t.Parallel()
	dir := stepGitTestRepo(t)
	start := stepGitTestRun(t, dir, "rev-parse", "HEAD")
	commit := stepGitTestCommit(t, dir, map[string]string{
		"owned/a.go": "package owned\n",
		"owned/b.go": "package owned\n",
	})

	checks := commitChecks(context.Background(), dir, start, commit, []string{"owned/a.go", "owned/b.go"})
	stepGitTestOrder(t, checks)
	for _, c := range checks {
		if !c.OK {
			t.Errorf("check %s failed on a clean owned commit: %s", c.Name, c.Detail)
		}
	}
}

func TestStepGitHeadFailsWhenHeadDiffersFromCommit(t *testing.T) {
	t.Parallel()
	dir := stepGitTestRepo(t)
	start := stepGitTestRun(t, dir, "rev-parse", "HEAD")
	first := stepGitTestCommit(t, dir, map[string]string{"owned/a.go": "one\n"})
	second := stepGitTestCommit(t, dir, map[string]string{"owned/a.go": "two\n"})

	checks := commitChecks(context.Background(), dir, start, first, []string{"owned/a.go"})
	head := stepGitTestCheck(t, checks, "head")
	if head.OK {
		t.Fatalf("head passed though HEAD is %s and commit is %s", second, first)
	}
	if !strings.Contains(head.Detail, second) {
		t.Errorf("head detail %q does not name the HEAD found, %s", head.Detail, second)
	}
}

func TestStepGitHeadFailsWhenCommitDoesNotResolve(t *testing.T) {
	t.Parallel()
	dir := stepGitTestRepo(t)
	start := stepGitTestRun(t, dir, "rev-parse", "HEAD")

	checks := commitChecks(context.Background(), dir, start, "0123456789abcdef0123456789abcdef01234567", nil)
	head := stepGitTestCheck(t, checks, "head")
	if head.OK {
		t.Fatalf("head passed for a commit that does not exist: %s", head.Detail)
	}
	if head.Detail == "" {
		t.Error("head failure has an empty detail")
	}
}

func TestStepGitAncestryFailsOffBranch(t *testing.T) {
	t.Parallel()
	dir := stepGitTestRepo(t)
	stepGitTestRun(t, dir, "checkout", "-q", "-b", "side")
	side := stepGitTestCommit(t, dir, map[string]string{"side.txt": "side\n"})
	stepGitTestRun(t, dir, "checkout", "-q", "main")
	commit := stepGitTestCommit(t, dir, map[string]string{"owned/a.go": "main\n"})

	checks := commitChecks(context.Background(), dir, side, commit, []string{"owned/a.go"})
	ancestry := stepGitTestCheck(t, checks, "ancestry")
	if ancestry.OK {
		t.Fatalf("ancestry passed though start %s is not an ancestor of HEAD %s", side, commit)
	}
	if !strings.Contains(ancestry.Detail, side) {
		t.Errorf("ancestry detail %q does not name start %s", ancestry.Detail, side)
	}
	if head := stepGitTestCheck(t, checks, "head"); !head.OK {
		t.Errorf("head failed on the checked-out commit: %s", head.Detail)
	}
}

func TestStepGitCleanFailsAndIgnoresIgnored(t *testing.T) {
	cases := []struct {
		name    string
		dirty   func(t *testing.T, dir string)
		wantOK  bool
		mention string
	}{
		{
			name: "modified tracked file",
			dirty: func(t *testing.T, dir string) {
				t.Helper()
				stepGitTestWrite(t, dir, "README", "changed\n")
			},
			mention: "README",
		},
		{
			name: "staged change",
			dirty: func(t *testing.T, dir string) {
				t.Helper()
				stepGitTestWrite(t, dir, "staged.txt", "staged\n")
				stepGitTestRun(t, dir, "add", "staged.txt")
			},
			mention: "staged.txt",
		},
		{
			name: "untracked file",
			dirty: func(t *testing.T, dir string) {
				t.Helper()
				stepGitTestWrite(t, dir, "scratch.txt", "scratch\n")
			},
			mention: "scratch.txt",
		},
		{
			name: "untracked file in a new directory",
			dirty: func(t *testing.T, dir string) {
				t.Helper()
				stepGitTestWrite(t, dir, "newdir/inner.txt", "inner\n")
			},
			mention: "newdir/inner.txt",
		},
		{
			name: "ignored file",
			dirty: func(t *testing.T, dir string) {
				t.Helper()
				stepGitTestWrite(t, dir, ".gitignore", "*.log\n")
				stepGitTestRun(t, dir, "add", ".gitignore")
				stepGitTestRun(t, dir, "commit", "-q", "-m", "ignore logs")
				stepGitTestWrite(t, dir, "build.log", "noise\n")
			},
			wantOK: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := stepGitTestRepo(t)
			start := stepGitTestRun(t, dir, "rev-parse", "HEAD")
			tc.dirty(t, dir)
			commit := stepGitTestRun(t, dir, "rev-parse", "HEAD")

			clean := stepGitTestCheck(t, commitChecks(context.Background(), dir, start, commit, nil), "clean")
			if clean.OK != tc.wantOK {
				t.Fatalf("clean OK = %v, want %v; detail %q", clean.OK, tc.wantOK, clean.Detail)
			}
			if !tc.wantOK && !strings.Contains(clean.Detail, tc.mention) {
				t.Errorf("clean detail %q does not name %s", clean.Detail, tc.mention)
			}
		})
	}
}

func TestStepGitOwnedFailsNamingPathOutsideOwned(t *testing.T) {
	t.Parallel()
	dir := stepGitTestRepo(t)
	start := stepGitTestRun(t, dir, "rev-parse", "HEAD")
	commit := stepGitTestCommit(t, dir, map[string]string{
		"owned/a.go":   "owned\n",
		"outside/x.go": "outside\n",
	})

	checks := commitChecks(context.Background(), dir, start, commit, []string{"owned/a.go"})
	owned := stepGitTestCheck(t, checks, "owned")
	if owned.OK {
		t.Fatalf("owned passed though outside/x.go is not owned: %s", owned.Detail)
	}
	if !strings.Contains(owned.Detail, "outside/x.go") {
		t.Errorf("owned detail %q does not name outside/x.go", owned.Detail)
	}
	if strings.Contains(owned.Detail, "owned/a.go") {
		t.Errorf("owned detail %q names owned/a.go, which is owned", owned.Detail)
	}
}

func TestStepGitOwnedMatchesExactPaths(t *testing.T) {
	t.Parallel()
	dir := stepGitTestRepo(t)
	start := stepGitTestRun(t, dir, "rev-parse", "HEAD")
	commit := stepGitTestCommit(t, dir, map[string]string{"owned/a.go.bak": "x\n"})

	// The owned entry "owned" is a directory name, not a path prefix; only exact paths are owned.
	checks := commitChecks(context.Background(), dir, start, commit, []string{"owned/a.go"})
	owned := stepGitTestCheck(t, checks, "owned")
	if owned.OK {
		t.Fatalf("owned passed for owned/a.go.bak against owned/a.go: %s", owned.Detail)
	}
	prefix := stepGitTestCheck(t, commitChecks(context.Background(), dir, start, commit, []string{"owned"}), "owned")
	if prefix.OK {
		t.Fatalf("owned passed for owned/a.go.bak against the directory entry owned: %s", prefix.Detail)
	}
}

func TestStepGitOwnedCountsBothSidesOfRename(t *testing.T) {
	t.Parallel()
	dir := stepGitTestRepo(t)
	body := "a fairly long body so the rename is detected cleanly\nline two\nline three\n"
	stepGitTestCommit(t, dir, map[string]string{"outside/x.go": body})
	start := stepGitTestRun(t, dir, "rev-parse", "HEAD")
	if err := os.MkdirAll(filepath.Join(dir, "owned"), 0o755); err != nil {
		t.Fatal(err)
	}
	stepGitTestRun(t, dir, "mv", "outside/x.go", "owned/x.go")
	stepGitTestRun(t, dir, "commit", "-q", "-m", "move into owned")
	commit := stepGitTestRun(t, dir, "rev-parse", "HEAD")

	// The new path is owned, but the old path is not: the rename must fail on the old side.
	checks := commitChecks(context.Background(), dir, start, commit, []string{"owned/x.go"})
	owned := stepGitTestCheck(t, checks, "owned")
	if owned.OK {
		t.Fatalf("owned passed though a rename removed outside/x.go: %s", owned.Detail)
	}
	if !strings.Contains(owned.Detail, "outside/x.go") {
		t.Errorf("owned detail %q does not name the old side outside/x.go", owned.Detail)
	}
}

func TestStepGitOwnedCountsNewSideOfRename(t *testing.T) {
	t.Parallel()
	dir := stepGitTestRepo(t)
	stepGitTestCommit(
		t,
		dir,
		map[string]string{"owned/x.go": "a fairly long body so the rename is detected cleanly\nline two\nline three\n"},
	)
	start := stepGitTestRun(t, dir, "rev-parse", "HEAD")
	if err := os.MkdirAll(filepath.Join(dir, "outside"), 0o755); err != nil {
		t.Fatal(err)
	}
	stepGitTestRun(t, dir, "mv", "owned/x.go", "outside/x.go")
	stepGitTestRun(t, dir, "commit", "-q", "-m", "move out of owned")
	commit := stepGitTestRun(t, dir, "rev-parse", "HEAD")

	// The old path is owned, but the new path is not: the rename must fail on the new side.
	owned := stepGitTestCheck(
		t,
		commitChecks(context.Background(), dir, start, commit, []string{"owned/x.go"}),
		"owned",
	)
	if owned.OK {
		t.Fatalf("owned passed though a rename created outside/x.go: %s", owned.Detail)
	}
	if !strings.Contains(owned.Detail, "outside/x.go") {
		t.Errorf("owned detail %q does not name the new side outside/x.go", owned.Detail)
	}
}

func TestStepGitOwnedDetailCapsPathsAtTwenty(t *testing.T) {
	t.Parallel()
	dir := stepGitTestRepo(t)
	start := stepGitTestRun(t, dir, "rev-parse", "HEAD")
	files := map[string]string{}
	for i := range 25 {
		files[fmt.Sprintf("outside/f%02d.txt", i)] = "x\n"
	}
	commit := stepGitTestCommit(t, dir, files)

	owned := stepGitTestCheck(t, commitChecks(context.Background(), dir, start, commit, nil), "owned")
	if owned.OK {
		t.Fatal("owned passed with 25 unowned paths")
	}
	named := strings.Count(owned.Detail, "outside/f")
	if named != stepGitPathLimit {
		t.Errorf("owned detail names %d paths, want %d: %q", named, stepGitPathLimit, owned.Detail)
	}
	if !strings.Contains(owned.Detail, "5 more") {
		t.Errorf("owned detail %q does not say 5 more paths were left out", owned.Detail)
	}
}

func TestStepGitReturnsTrimmedStdout(t *testing.T) {
	t.Parallel()
	dir := stepGitTestRepo(t)
	want := stepGitTestRun(t, dir, "rev-parse", "HEAD")

	got, err := stepGit(context.Background(), dir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("stepGit rev-parse: %v", err)
	}
	if got != want {
		t.Errorf("stepGit returned %q, want trimmed %q", got, want)
	}
}

func TestStepGitErrorCarriesStderr(t *testing.T) {
	t.Parallel()
	dir := stepGitTestRepo(t)

	_, err := stepGit(context.Background(), dir, "rev-parse", "--verify", "--end-of-options", "nope^{commit}")
	if err == nil {
		t.Fatal("stepGit returned no error for an unknown revision")
	}
	if !strings.Contains(err.Error(), "fatal:") {
		t.Errorf("error %q does not carry git's stderr", err.Error())
	}
}
