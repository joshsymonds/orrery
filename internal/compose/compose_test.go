package compose

import (
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// testRef is a candidate pin under refs/orrery/, where Compose pins its candidate.
const testRef = "refs/orrery/test/candidate"

// testBranch is an epic branch, the only kind of branch Advance moves.
const testBranch = "refs/heads/epic/demo"

// TestMain points git at no global or system config for the whole package, so the owner's git config never applies to
// the repositories the tests make or to the git the code under test runs. It is set here once rather than with t.Setenv
// in each test, because t.Setenv keeps a test from running in parallel.
func TestMain(m *testing.M) {
	os.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	os.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	os.Exit(m.Run())
}

// composeGit runs git in dir with the test identity, env added to the environment and stdin, and returns its trimmed
// output. It fails the test on error.
func composeGit(t *testing.T, dir string, env []string, stdin string, args ...string) string {
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

// composeRepo makes an empty repository under t.TempDir(), with HEAD on main.
func composeRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	composeGit(t, dir, nil, "", "init", "-q", "-b", "main")
	return dir
}

// revisionIdentity is the environment of every revision the tests build. The author and committer differ, and the
// dates are not the composer's, so a composed commit that took its identity from anywhere else fails the comparison.
func revisionIdentity() []string {
	return []string{
		"GIT_AUTHOR_NAME=Ada",
		"GIT_AUTHOR_EMAIL=ada@example.invalid",
		"GIT_AUTHOR_DATE=1700000000 +0130",
		"GIT_COMMITTER_NAME=Grace",
		"GIT_COMMITTER_EMAIL=grace@example.invalid",
		"GIT_COMMITTER_DATE=1700000100 -0500",
	}
}

// composeRevision writes a commit on parent (a root commit when parent is empty) whose tree is parent's tree with the
// files in set written and the paths in remove deleted. It moves no ref and touches no index or working tree but a
// temporary index.
func composeRevision(t *testing.T, dir, parent, message string, set map[string]string, remove []string) string {
	t.Helper()
	env := []string{"GIT_INDEX_FILE=" + filepath.Join(t.TempDir(), "index")}
	if parent == "" {
		composeGit(t, dir, env, "", "read-tree", "--empty")
	} else {
		composeGit(t, dir, env, "", "read-tree", parent)
	}
	for path, content := range set {
		blob := composeGit(t, dir, nil, content, "hash-object", "-w", "--stdin")
		composeGit(t, dir, env, "", "update-index", "--add", "--cacheinfo", "100644,"+blob+","+path)
	}
	for _, path := range remove {
		composeGit(t, dir, env, "", "update-index", "--force-remove", "--", path)
	}
	tree := composeGit(t, dir, env, "", "write-tree")
	commitArgs := []string{"commit-tree", tree}
	if parent != "" {
		commitArgs = append(commitArgs, "-p", parent)
	}
	return composeGit(t, dir, revisionIdentity(), message, commitArgs...)
}

// composeBase makes the batch base of the tests, which holds files a and b, and points main at it.
func composeBase(t *testing.T, dir string) string {
	t.Helper()
	base := composeRevision(t, dir, "", "base\n", map[string]string{"a": "one", "b": "bee"}, nil)
	composeGit(t, dir, nil, "", "update-ref", "refs/heads/main", base)
	return base
}

// composeFiles returns the files of commit as a map from path to content.
func composeFiles(t *testing.T, dir, commit string) map[string]string {
	t.Helper()
	files := make(map[string]string)
	for path := range strings.FieldsSeq(composeGit(t, dir, nil, "", "ls-tree", "-r", "--name-only", commit)) {
		files[path] = composeGit(t, dir, nil, "", "cat-file", "blob", commit+":"+path)
	}
	return files
}

// composeParents returns the parents of commit.
func composeParents(t *testing.T, dir, commit string) []string {
	t.Helper()
	fields := strings.Fields(composeGit(t, dir, nil, "", "rev-list", "--parents", "-n", "1", commit))
	return fields[1:]
}

// composeMeta returns the author, committer and message of commit, with the dates raw, so two commits can be compared
// on identity and message.
func composeMeta(t *testing.T, dir, commit string) string {
	t.Helper()
	return composeGit(t, dir, nil, "", "log", "-1", "--date=raw",
		"--format=%an <%ae> %ad|%cn <%ce> %cd|%B", commit)
}

func TestComposeChainsOneCommitPerTask(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	dir := composeRepo(t)
	base := composeBase(t, dir)
	first := composeRevision(t, dir, base, "change a, add x, drop b\n",
		map[string]string{"a": "one changed", "x": "new"}, []string{"b"})
	second := composeRevision(t, dir, base, "add y\n", map[string]string{"y": "two"}, nil)
	tasks := []Task{{Seq: 1, Base: base, Revision: first}, {Seq: 2, Base: base, Revision: second}}

	result, err := Compose(ctx, dir, base, tasks, testRef)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if len(result.LeftOut) != 0 {
		t.Fatalf("LeftOut = %v, want none", result.LeftOut)
	}
	if len(result.Composed) != 2 {
		t.Fatalf("Composed = %v, want two commits", result.Composed)
	}
	firstCommit, secondCommit := result.Composed[0], result.Composed[1]
	if firstCommit.Seq != 1 || secondCommit.Seq != 2 {
		t.Fatalf("Composed seqs = %d, %d; want 1, 2", firstCommit.Seq, secondCommit.Seq)
	}
	if got := composeParents(t, dir, firstCommit.Commit); !slices.Equal(got, []string{base}) {
		t.Fatalf("first commit parents = %v, want the base %s", got, base)
	}
	if got := composeParents(t, dir, secondCommit.Commit); !slices.Equal(got, []string{firstCommit.Commit}) {
		t.Fatalf("second commit parents = %v, want the first commit %s", got, firstCommit.Commit)
	}
	if result.Base != base || result.Candidate != secondCommit.Commit {
		t.Fatalf("Base, Candidate = %s, %s; want %s, %s", result.Base, result.Candidate, base, secondCommit.Commit)
	}
	wantFirst := map[string]string{"a": "one changed", "x": "new"}
	if got := composeFiles(t, dir, firstCommit.Commit); !maps.Equal(got, wantFirst) {
		t.Fatalf("first commit files = %v, want %v", got, wantFirst)
	}
	wantCandidate := map[string]string{"a": "one changed", "x": "new", "y": "two"}
	if got := composeFiles(t, dir, result.Candidate); !maps.Equal(got, wantCandidate) {
		t.Fatalf("candidate files = %v, want %v", got, wantCandidate)
	}
	if got, want := composeMeta(t, dir, firstCommit.Commit), composeMeta(t, dir, first); got != want {
		t.Fatalf("first commit meta = %q, want the revision's %q", got, want)
	}
	if got, want := composeMeta(t, dir, secondCommit.Commit), composeMeta(t, dir, second); got != want {
		t.Fatalf("second commit meta = %q, want the revision's %q", got, want)
	}
	if got := composeGit(t, dir, nil, "", "rev-parse", testRef); got != result.Candidate {
		t.Fatalf("pin %s = %s, want the candidate %s", testRef, got, result.Candidate)
	}
}

func TestComposeRecomposeGivesSameCommits(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	dir := composeRepo(t)
	base := composeBase(t, dir)
	first := composeRevision(t, dir, base, "one\n", map[string]string{"x": "new"}, nil)
	second := composeRevision(t, dir, base, "two\n", map[string]string{"y": "two"}, nil)
	tasks := []Task{{Seq: 1, Base: base, Revision: first}, {Seq: 2, Base: base, Revision: second}}

	composed, err := Compose(ctx, dir, base, tasks, testRef)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if len(composed.Composed) != 2 {
		t.Fatalf("Composed = %v, want two commits to recompose", composed.Composed)
	}
	again, err := Compose(ctx, dir, base, tasks, testRef)
	if err != nil {
		t.Fatalf("recompose refused by the pin: %v", err)
	}
	if !reflect.DeepEqual(again, composed) {
		t.Fatalf("recompose = %+v, want the same result %+v", again, composed)
	}
}

func TestComposePinRefusesAnotherCommit(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	dir := composeRepo(t)
	base := composeBase(t, dir)
	first := composeRevision(t, dir, base, "one\n", map[string]string{"x": "new"}, nil)
	second := composeRevision(t, dir, base, "two\n", map[string]string{"y": "two"}, nil)
	pinned, err := Compose(ctx, dir, base, []Task{{Seq: 1, Base: base, Revision: first}}, testRef)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	other := []Task{{Seq: 1, Base: base, Revision: first}, {Seq: 2, Base: base, Revision: second}}
	if _, err = Compose(ctx, dir, base, other, testRef); err == nil {
		t.Fatal("Compose moved a pin that points at another commit")
	}
	if got := composeGit(t, dir, nil, "", "rev-parse", testRef); got != pinned.Candidate {
		t.Fatalf("pin = %s after a refused compose, want %s", got, pinned.Candidate)
	}
}

func TestComposePinRefusesRefOutsideOrrery(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	dir := composeRepo(t)
	base := composeBase(t, dir)
	first := composeRevision(t, dir, base, "one\n", map[string]string{"x": "new"}, nil)
	tasks := []Task{{Seq: 1, Base: base, Revision: first}}
	if _, err := Compose(ctx, dir, base, tasks, testBranch); err == nil {
		t.Fatalf("Compose pinned a candidate at %s, outside refs/orrery/", testBranch)
	}
	refs := composeGit(t, dir, nil, "", "for-each-ref", "--format=%(refname)")
	if slices.Contains(strings.Fields(refs), testBranch) {
		t.Fatalf("%s was created by a refused compose", testBranch)
	}
}

func TestComposeLeavesOutTasksAndComposesTheRest(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	dir := composeRepo(t)
	base := composeBase(t, dir)
	other := composeRevision(t, dir, base, "other base\n", map[string]string{"z": "zed"}, nil)
	first := composeRevision(t, dir, base, "first\n", map[string]string{"a": "first"}, nil)
	overlapping := composeRevision(t, dir, base, "overlapping\n", map[string]string{"a": "second"}, nil)
	elsewhere := composeRevision(t, dir, other, "elsewhere\n", map[string]string{"q": "q"}, nil)
	last := composeRevision(t, dir, base, "last\n", map[string]string{"y": "two"}, nil)
	tasks := []Task{
		{Seq: 1, Base: base, Revision: first},
		{Seq: 2, Base: base, Revision: overlapping},
		{Seq: 3, Base: other, Revision: elsewhere},
		{Seq: 4, Base: base, Revision: last},
	}

	result, err := Compose(ctx, dir, base, tasks, testRef)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	wantLeftOut := []LeftOut{
		{Seq: 2, Reason: "T2 overlaps T1 at a"},
		{Seq: 3, Reason: fmt.Sprintf("T3 was built on %s, not on the batch base %s", other, base)},
	}
	if !slices.Equal(result.LeftOut, wantLeftOut) {
		t.Fatalf("LeftOut = %v, want %v", result.LeftOut, wantLeftOut)
	}
	if len(result.Composed) != 2 || result.Composed[0].Seq != 1 || result.Composed[1].Seq != 4 {
		t.Fatalf("Composed = %v, want T1 and T4", result.Composed)
	}
	firstCommit := result.Composed[0].Commit
	if got := composeParents(t, dir, result.Composed[1].Commit); !slices.Equal(got, []string{firstCommit}) {
		t.Fatalf("T4 parents = %v, want T1's commit %s", got, firstCommit)
	}
	want := map[string]string{"a": "first", "b": "bee", "y": "two"}
	if got := composeFiles(t, dir, result.Candidate); !maps.Equal(got, want) {
		t.Fatalf("candidate files = %v, want %v", got, want)
	}
}

// composeAncestry makes an older base, which holds files a and b, and the batch base built on it, which changes a and
// adds d/e. It returns both.
func composeAncestry(t *testing.T, dir string) (older, batchBase string) {
	t.Helper()
	older = composeBase(t, dir)
	batchBase = composeRevision(t, dir, older, "integrated\n", map[string]string{"a": "integrated", "d/e": "deep"}, nil)
	return older, batchBase
}

// A task built on an ancestor of the batch base whose changed paths are not among those changed since composes: its
// paths are laid over the batch base, which keeps both the batch base's changes and the task's.
func TestComposeTaskOnAncestorWithDisjointChangesComposes(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	dir := composeRepo(t)
	older, batchBase := composeAncestry(t, dir)
	revision := composeRevision(
		t,
		dir,
		older,
		"disjoint\n",
		map[string]string{"c": "sea", "d/f": "beside"},
		[]string{"b"},
	)
	later := composeRevision(t, dir, batchBase, "later\n", map[string]string{"y": "two"}, nil)
	tasks := []Task{{Seq: 1, Base: older, Revision: revision}, {Seq: 2, Base: batchBase, Revision: later}}

	result, err := Compose(ctx, dir, batchBase, tasks, testRef)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if len(result.LeftOut) != 0 || len(result.Composed) != 2 {
		t.Fatalf("Composed = %v, LeftOut = %v; want both tasks composed", result.Composed, result.LeftOut)
	}
	first := result.Composed[0].Commit
	if got := composeParents(t, dir, first); !slices.Equal(got, []string{batchBase}) {
		t.Fatalf("T1 parents = %v, want the batch base %s", got, batchBase)
	}
	if got, want := composeMeta(t, dir, first), composeMeta(t, dir, revision); got != want {
		t.Fatalf("T1 commit meta = %q, want the revision's %q", got, want)
	}
	want := map[string]string{"a": "integrated", "c": "sea", "d/e": "deep", "d/f": "beside"}
	if got := composeFiles(t, dir, first); !maps.Equal(got, want) {
		t.Fatalf("T1 commit files = %v, want %v", got, want)
	}
	want["y"] = "two"
	if got := composeFiles(t, dir, result.Candidate); !maps.Equal(got, want) {
		t.Fatalf("candidate files = %v, want %v", got, want)
	}
}

// A task built on an ancestor of the batch base that changed a path changed since, or one that holds or lies under
// such a path, is left out as built on another base: composing it would undo the batch base's change.
func TestComposeTaskOnAncestorWithOverlappingChangesIsLeftOut(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	dir := composeRepo(t)
	older, batchBase := composeAncestry(t, dir)
	same := composeRevision(t, dir, older, "same\n", map[string]string{"a": "mine"}, nil)
	holds := composeRevision(t, dir, older, "holds\n", map[string]string{"d": "file"}, nil)
	tasks := []Task{{Seq: 1, Base: older, Revision: same}, {Seq: 2, Base: older, Revision: holds}}

	result, err := Compose(ctx, dir, batchBase, tasks, testRef)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	wantLeftOut := []LeftOut{
		{Seq: 1, Reason: fmt.Sprintf("T1 was built on %s, not on the batch base %s", older, batchBase)},
		{Seq: 2, Reason: fmt.Sprintf("T2 was built on %s, not on the batch base %s", older, batchBase)},
	}
	if len(result.Composed) != 0 || !slices.Equal(result.LeftOut, wantLeftOut) {
		t.Fatalf("Composed = %v, LeftOut = %v; want none composed and LeftOut %v",
			result.Composed, result.LeftOut, wantLeftOut)
	}
	if result.Candidate != batchBase {
		t.Fatalf("Candidate = %s, want the batch base %s", result.Candidate, batchBase)
	}
}

// A task built on a commit that is not an ancestor of the batch base is left out, however disjoint its changes, and so
// is one whose base names no commit.
func TestComposeTaskOnUnrelatedBaseIsLeftOut(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	dir := composeRepo(t)
	older, batchBase := composeAncestry(t, dir)
	sibling := composeRevision(t, dir, older, "sibling\n", map[string]string{"s": "side"}, nil)
	unrelated := composeRevision(t, dir, sibling, "unrelated\n", map[string]string{"q": "queue"}, nil)
	missing := strings.Repeat("0", len(batchBase))
	tasks := []Task{{Seq: 1, Base: sibling, Revision: unrelated}, {Seq: 2, Base: missing, Revision: unrelated}}

	result, err := Compose(ctx, dir, batchBase, tasks, testRef)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	wantLeftOut := []LeftOut{
		{Seq: 1, Reason: fmt.Sprintf("T1 was built on %s, not on the batch base %s", sibling, batchBase)},
		{Seq: 2, Reason: fmt.Sprintf("T2 was built on %s, not on the batch base %s", missing, batchBase)},
	}
	if len(result.Composed) != 0 || !slices.Equal(result.LeftOut, wantLeftOut) {
		t.Fatalf("Composed = %v, LeftOut = %v; want none composed and LeftOut %v",
			result.Composed, result.LeftOut, wantLeftOut)
	}
}

func TestComposeOverlapRules(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		first      map[string]string
		second     map[string]string
		wantReason string
	}{
		{
			name:       "second under first",
			first:      map[string]string{"d/f": "x"},
			second:     map[string]string{"d/f/g": "y"},
			wantReason: "T2 overlaps T1 at d/f/g",
		},
		{
			name:       "second holds first",
			first:      map[string]string{"d/f/g": "x"},
			second:     map[string]string{"d/f": "y"},
			wantReason: "T2 overlaps T1 at d/f",
		},
		{
			name:   "sibling sharing a name prefix",
			first:  map[string]string{"a": "x"},
			second: map[string]string{"ab": "y"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()
			dir := composeRepo(t)
			base := composeBase(t, dir)
			first := composeRevision(t, dir, base, "first\n", tc.first, nil)
			second := composeRevision(t, dir, base, "second\n", tc.second, nil)
			tasks := []Task{{Seq: 1, Base: base, Revision: first}, {Seq: 2, Base: base, Revision: second}}

			result, err := Compose(ctx, dir, base, tasks, testRef)
			if err != nil {
				t.Fatalf("Compose: %v", err)
			}
			if tc.wantReason == "" {
				if len(result.Composed) != 2 || len(result.LeftOut) != 0 {
					t.Fatalf("Composed = %v, LeftOut = %v; want both tasks composed", result.Composed, result.LeftOut)
				}
				return
			}
			wantLeftOut := []LeftOut{{Seq: 2, Reason: tc.wantReason}}
			if len(result.Composed) != 1 || !slices.Equal(result.LeftOut, wantLeftOut) {
				t.Fatalf("Composed = %v, LeftOut = %v; want T1 composed and LeftOut %v",
					result.Composed, result.LeftOut, wantLeftOut)
			}
		})
	}
}

// TestComposeKeepsPathsThatLookLikePathspecs composes a task that changes a file named ":foo", which git would read as
// pathspec magic, and one that adds "*": the candidate holds both files as the task wrote them.
func TestComposeKeepsPathsThatLookLikePathspecs(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	dir := composeRepo(t)
	base := composeBase(t, dir)
	revision := composeRevision(t, dir, base, "magic names\n", map[string]string{":foo": "colon", "*": "star"}, nil)

	result, err := Compose(ctx, dir, base, []Task{{Seq: 1, Base: base, Revision: revision}}, testRef)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	want := map[string]string{"a": "one", "b": "bee", ":foo": "colon", "*": "star"}
	if got := composeFiles(t, dir, result.Candidate); !maps.Equal(got, want) {
		t.Fatalf("candidate files = %v, want %v", got, want)
	}
}

func TestComposeLeavesWorkingTreeAndIndexUnchanged(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	dir := composeRepo(t)
	base := composeBase(t, dir)
	composeGit(t, dir, nil, "", "read-tree", "-u", "--reset", base)
	if err := os.WriteFile(filepath.Join(dir, "a"), []byte("unstaged edit"), 0o600); err != nil {
		t.Fatalf("write a: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b"), []byte("staged edit"), 0o600); err != nil {
		t.Fatalf("write b: %v", err)
	}
	composeGit(t, dir, nil, "", "add", "b")
	if err := os.WriteFile(filepath.Join(dir, "scratch"), []byte("untracked"), 0o600); err != nil {
		t.Fatalf("write scratch: %v", err)
	}
	first := composeRevision(t, dir, base, "drop b, add x\n", map[string]string{"x": "new"}, []string{"b"})
	tasks := []Task{{Seq: 1, Base: base, Revision: first}}

	headBefore := composeGit(t, dir, nil, "", "rev-parse", "HEAD")
	indexBefore := composeGit(t, dir, nil, "", "ls-files", "--stage")
	statusBefore := composeGit(t, dir, nil, "", "status", "--porcelain")

	result, err := Compose(ctx, dir, base, tasks, testRef)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if len(result.Composed) != 1 {
		t.Fatalf("Composed = %v, want the one task", result.Composed)
	}

	if got := composeGit(t, dir, nil, "", "rev-parse", "HEAD"); got != headBefore {
		t.Fatalf("HEAD = %s after Compose, want %s", got, headBefore)
	}
	if got := composeGit(t, dir, nil, "", "ls-files", "--stage"); got != indexBefore {
		t.Fatalf("index after Compose:\n%s\nwant:\n%s", got, indexBefore)
	}
	if got := composeGit(t, dir, nil, "", "status", "--porcelain"); got != statusBefore {
		t.Fatalf("status after Compose:\n%s\nwant:\n%s", got, statusBefore)
	}
	if got, readErr := os.ReadFile(filepath.Join(dir, "a")); readErr != nil || string(got) != "unstaged edit" {
		t.Fatalf("a = %q, %v after Compose; want the unstaged edit", got, readErr)
	}
}

func TestAdvanceMovesBranchOnlyFromExpectedCommit(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	dir := composeRepo(t)
	base := composeBase(t, dir)
	first := composeRevision(t, dir, base, "one\n", map[string]string{"a": "changed"}, nil)
	composeGit(t, dir, nil, "", "update-ref", testBranch, base)

	if err := Advance(ctx, dir, testBranch, first, base); err != nil {
		t.Fatalf("Advance from the branch's commit: %v", err)
	}
	if got := composeGit(t, dir, nil, "", "rev-parse", testBranch); got != first {
		t.Fatalf("branch = %s after Advance, want %s", got, first)
	}
	if err := Advance(ctx, dir, testBranch, base, base); err == nil {
		t.Fatal("Advance moved a branch that is not at from")
	}
	if got := composeGit(t, dir, nil, "", "rev-parse", testBranch); got != first {
		t.Fatalf("branch = %s after a refused Advance, want %s", got, first)
	}
}

func TestAdvanceRefusesBranchOutsideEpic(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	dir := composeRepo(t)
	base := composeBase(t, dir)
	first := composeRevision(t, dir, base, "one\n", map[string]string{"a": "changed"}, nil)

	if err := Advance(ctx, dir, "refs/heads/main", first, base); err == nil {
		t.Fatal("Advance moved refs/heads/main, which is outside refs/heads/epic/")
	}
	if got := composeGit(t, dir, nil, "", "rev-parse", "refs/heads/main"); got != base {
		t.Fatalf("main = %s after a refused Advance, want %s", got, base)
	}
}

func TestAdvanceRefusesAMoveThatIsNotAFastForward(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	dir := composeRepo(t)
	base := composeBase(t, dir)
	newer := composeRevision(t, dir, base, "newer\n", map[string]string{"a": "newer"}, nil)
	older := composeRevision(t, dir, base, "older\n", map[string]string{"b": "older"}, nil)
	composeGit(t, dir, nil, "", "update-ref", testBranch, newer)

	if err := Advance(ctx, dir, testBranch, older, newer); err == nil {
		t.Fatal("Advance moved the branch to a commit that does not build on it")
	}
	if got := composeGit(t, dir, nil, "", "rev-parse", testBranch); got != newer {
		t.Fatalf("branch = %s after a refused Advance, want %s", got, newer)
	}
}
