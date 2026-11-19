package guard

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const (
	testFile   = "a_test.go"
	renamedTo  = "b_test.go"
	captureSh  = "scripts/gen.sh"
	rootScript = "root.sh"
	newScript  = "missing.sh"
)

// TestMain points git at no global or system config for the whole package, so the owner's git config never applies to
// the repositories the tests make or to the git the code under test runs. It is set here once rather than with t.Setenv
// in each test, because t.Setenv keeps a test from running in parallel.
func TestMain(m *testing.M) {
	os.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	os.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	os.Exit(m.Run())
}

// gitRun runs git in dir with a fixed identity, env added to the environment and stdin fed to it. It returns the
// trimmed output and fails the test on error.
func gitRun(t *testing.T, dir string, env []string, stdin string, args ...string) string {
	t.Helper()
	full := append([]string{
		"-c", "user.name=Test",
		"-c", "user.email=test@example.invalid",
		"-c", "commit.gpgsign=false",
	}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// newRepo makes an empty repository under t.TempDir().
func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitRun(t, dir, nil, "", "init", "-q")
	return dir
}

// commitFiles makes a root commit in dir whose tree holds exactly files, a map from path to content, and returns its
// id. It writes objects and a temporary index only, so the work tree and every ref stay as they were.
func commitFiles(t *testing.T, dir string, files map[string]string) string {
	t.Helper()
	env := []string{"GIT_INDEX_FILE=" + filepath.Join(t.TempDir(), "index")}
	gitRun(t, dir, env, "", "read-tree", "--empty")
	for path, content := range files {
		blob := gitRun(t, dir, nil, content, "hash-object", "-w", "--stdin")
		gitRun(t, dir, env, "", "update-index", "--add", "--cacheinfo", "100644,"+blob+","+path)
	}
	tree := gitRun(t, dir, env, "", "write-tree")
	return gitRun(t, dir, nil, "", "commit-tree", tree, "-m", "commit")
}

func TestCheck(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		base, head map[string]string
		spec       Spec
		want       []Refusal
	}{
		{
			name: "lowered assertion names file and test",
			base: map[string]string{testFile: goFile(testA2)},
			head: map[string]string{testFile: goFile(testA1)},
			spec: Spec{Guarded: []string{testFile}},
			want: []Refusal{{File: testFile, Test: "TestA", Rule: "assertions lowered from 2 to 1", Fact: true}},
		},
		{
			name: "renamed guarded file is deleted",
			base: map[string]string{testFile: goFile(testA1)},
			head: map[string]string{renamedTo: goFile(testA1)},
			spec: Spec{Guarded: []string{testFile}},
			want: []Refusal{{File: testFile, Rule: "guarded file deleted", Fact: true}},
		},
		{
			name: "edited capture file is refused",
			base: map[string]string{captureSh: "old"},
			head: map[string]string{captureSh: "new"},
			spec: Spec{CaptureArgs: []string{"sh", captureSh, "--fast"}},
			want: []Refusal{{File: captureSh, Rule: "capture file changed"}},
		},
		{
			name: "dot-prefixed capture names its file",
			base: map[string]string{captureSh: "old"},
			head: map[string]string{captureSh: "new"},
			spec: Spec{CaptureArgs: []string{"./" + captureSh}},
			want: []Refusal{{File: captureSh, Rule: "capture file changed"}},
		},
		{
			name: "argv naming no file at base guards nothing",
			base: map[string]string{captureSh: "old", rootScript: "old"},
			head: map[string]string{captureSh: "new", rootScript: "new", newScript: "new"},
			spec: Spec{CaptureArgs: []string{"scripts", "scripts/*.sh", ".", "", "/" + captureSh, newScript}},
		},
		{
			name: "unchanged commit gives no refusal",
			base: map[string]string{testFile: goFile(testA2), captureSh: "old"},
			head: map[string]string{testFile: goFile(testA2), captureSh: "old"},
			spec: Spec{Guarded: []string{testFile}, CaptureArgs: []string{captureSh}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			repo := newRepo(t)
			base := commitFiles(t, repo, tc.base)
			head := commitFiles(t, repo, tc.head)
			got, err := Check(t.Context(), repo, base, head, tc.spec)
			if err != nil {
				t.Fatalf("Check: %v", err)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("refusals = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCheckUnknownBaseIsAnError(t *testing.T) {
	t.Parallel()
	repo := newRepo(t)
	head := commitFiles(t, repo, map[string]string{testFile: goFile(testA1)})
	_, err := Check(t.Context(), repo, "no-such-base", head, Spec{Guarded: []string{testFile}})
	if err == nil {
		t.Error("Check with an unknown base: err = nil, want an error")
	}
}
