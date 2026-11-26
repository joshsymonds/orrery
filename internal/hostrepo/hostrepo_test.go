package hostrepo

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/fleettest"
	"github.com/joshsymonds/orrery/internal/hostexec"
)

// fixedRoots is a RootLister that lists the roots it holds.
type fixedRoots []string

// Roots returns the roots that fixedRoots holds.
func (r fixedRoots) Roots(context.Context) ([]string, error) {
	return r, nil
}

// gitIn runs git with args in dir on this machine, and returns its stdout with the surrounding space trimmed. It
// fails the test when git fails.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := hostexec.NewLocal().Git(t.Context(), hostexec.GitCall{Dir: dir, Args: args})
	if err != nil {
		t.Fatalf("git %s in %s: %v", strings.Join(args, " "), dir, err)
	}
	return strings.TrimSpace(string(out))
}

// cloneLocal clones origin on this machine into a directory named name under root, and returns the clone's path.
func cloneLocal(t *testing.T, origin, root, name string) string {
	t.Helper()
	gitIn(t, root, "clone", "-q", origin, name)
	return filepath.Join(root, name)
}

// checkoutState returns what the checkout at dir holds that a fetch must leave alone: its HEAD, its local branches, its
// index and its status, as git reports them.
func checkoutState(t *testing.T, dir string) string {
	t.Helper()
	return strings.Join([]string{
		gitIn(t, dir, "rev-parse", "HEAD"),
		gitIn(t, dir, "for-each-ref", "--format=%(refname) %(objectname)", "refs/heads"),
		gitIn(t, dir, "ls-files", "--stage"),
		gitIn(t, dir, "status", "--porcelain"),
	}, "\n")
}

// wantHostError checks that err names host, and that err carries a *hostexec.GitError.
func wantHostError(t *testing.T, err error, host string) *hostexec.GitError {
	t.Helper()
	if err == nil {
		t.Fatal("Ensure succeeded, want an error")
	}
	if !strings.HasPrefix(err.Error(), "hostrepo: "+host+": ") {
		t.Errorf("error %q does not name host %s", err, host)
	}
	var gitErr *hostexec.GitError
	if !errors.As(err, &gitErr) {
		t.Fatalf("error %q carries no git error", err)
	}
	return gitErr
}

// wantGitStderr checks that err carries git's stderr in its message, as the failed run wrote it.
func wantGitStderr(t *testing.T, err error) {
	t.Helper()
	gitErr := wantHostError(t, err, "b")
	stderr := strings.TrimSpace(string(gitErr.Stderr))
	if stderr == "" {
		t.Fatalf("git's error %v has no stderr to carry", err)
	}
	if !strings.Contains(err.Error(), stderr) {
		t.Errorf("error %q does not carry git's stderr %q", err, stderr)
	}
}

func TestRepoOnHostPrefersRepositoryPath(t *testing.T) {
	f := fleettest.Start(t, "b")
	root := f.Roots["b"]
	// a-other sorts before pref by path, so only the preference picks pref.
	cloneLocal(t, f.Origin, root, "a-other")
	preferred := cloneLocal(t, f.Origin, root, "pref")

	req := Request{Host: "b", Origin: f.Origin, Preferred: preferred}
	got, err := Ensure(t.Context(), f.Hosts["b"], f.Clients["b"], req)
	if err != nil {
		t.Fatal(err)
	}
	if got != preferred {
		t.Fatalf("Ensure = %s, want the preferred checkout %s", got, preferred)
	}
}

func TestRepoOnHostFirstByPath(t *testing.T) {
	f := fleettest.Start(t, "b")
	root := f.Roots["b"]
	// Both are checkouts of origin. a/inner is at level 2 and b is at level 1, but a/inner sorts first by path.
	if err := os.MkdirAll(filepath.Join(root, "a"), 0o750); err != nil {
		t.Fatal(err)
	}
	inner := cloneLocal(t, f.Origin, filepath.Join(root, "a"), "inner")
	cloneLocal(t, f.Origin, root, "b")

	got, err := Ensure(t.Context(), f.Hosts["b"], f.Clients["b"], Request{Host: "b", Origin: f.Origin})
	if err != nil {
		t.Fatal(err)
	}
	if got != inner {
		t.Fatalf("Ensure = %s, want the first checkout by path, %s", got, inner)
	}
}

func TestRepoOnHostMatchesOriginSpelling(t *testing.T) {
	f := fleettest.Start(t, "b")
	checkout := cloneLocal(t, f.Origin, f.Roots["b"], "repo")
	for _, tc := range []struct {
		name, origin string
	}{
		{name: "trailing slash", origin: f.Origin + "/"},
		{name: "without .git", origin: strings.TrimSuffix(f.Origin, ".git")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Ensure(t.Context(), f.Hosts["b"], f.Clients["b"], Request{Host: "b", Origin: tc.origin})
			if err != nil {
				t.Fatal(err)
			}
			if got != checkout {
				t.Fatalf("Ensure = %s, want the checkout %s", got, checkout)
			}
		})
	}
}

func TestRepoOnHostClonesUnderFirstRoot(t *testing.T) {
	f := fleettest.Start(t, "b")
	// The second root lies outside the agent's roots, so the agent refuses to list it. A clone under it would fail too.
	roots := fixedRoots{f.Roots["b"], t.TempDir()}
	want := filepath.Join(f.Roots["b"], "work")

	got, err := Ensure(t.Context(), f.Hosts["b"], roots, Request{Host: "b", Origin: f.Origin, Preferred: want})
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("Ensure = %s, want the clone under the first root, %s", got, want)
	}
	if origin := gitIn(t, got, "remote", "get-url", "origin"); origin != f.Origin {
		t.Fatalf("origin of the clone = %s, want %s", origin, f.Origin)
	}
}

func TestRepoOnHostFetchesBase(t *testing.T) {
	f := fleettest.Start(t, "b")
	checkout := cloneLocal(t, f.Origin, f.Roots["b"], "repo")
	// The base is a commit pushed after the clone, so the checkout holds it only once the fetch brings it.
	base := f.PushCommit(t, "second commit")

	req := Request{Host: "b", Origin: f.Origin, Preferred: checkout, Base: base}
	got, err := Ensure(t.Context(), f.Hosts["b"], f.Clients["b"], req)
	if err != nil {
		t.Fatal(err)
	}
	if got != checkout {
		t.Fatalf("Ensure = %s, want the checkout %s", got, checkout)
	}
	if fetched := gitIn(t, checkout, "rev-parse", "refs/remotes/origin/main"); fetched != base {
		t.Fatalf("origin/main of the checkout is %s, want the base %s", fetched, base)
	}
}

func TestRepoOnHostLeavesCheckoutUntouched(t *testing.T) {
	f := fleettest.Start(t, "b")
	checkout := cloneLocal(t, f.Origin, f.Roots["b"], "repo")
	gitIn(t, checkout, "branch", "topic")
	if err := os.WriteFile(filepath.Join(checkout, "dirty.txt"), []byte("dirty\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(checkout, "staged.txt"), []byte("staged\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitIn(t, checkout, "add", "staged.txt")
	before := checkoutState(t, checkout)
	// The origin moves on, so the fetch has a commit to bring in.
	pushed := f.PushCommit(t, "second commit")

	req := Request{Host: "b", Origin: f.Origin, Preferred: checkout}
	got, err := Ensure(t.Context(), f.Hosts["b"], f.Clients["b"], req)
	if err != nil {
		t.Fatal(err)
	}
	if got != checkout {
		t.Fatalf("Ensure = %s, want the checkout %s", got, checkout)
	}
	if fetched := gitIn(t, checkout, "rev-parse", "refs/remotes/origin/main"); fetched != pushed {
		t.Fatalf("origin/main of the checkout is %s, want the fetched %s", fetched, pushed)
	}
	if after := checkoutState(t, checkout); after != before {
		t.Fatalf("the fetch changed the checkout:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestRepoOnHostCloneFailureNamesHost(t *testing.T) {
	f := fleettest.Start(t, "b")
	missing := filepath.Join(t.TempDir(), "missing.git")

	_, err := Ensure(t.Context(), f.Hosts["b"], f.Clients["b"],
		Request{Host: "b", Origin: missing, Preferred: filepath.Join(f.Roots["b"], "repo")})
	wantGitStderr(t, err)
}

func TestRepoOnHostFetchFailureNamesHost(t *testing.T) {
	f := fleettest.Start(t, "b")
	checkout := cloneLocal(t, f.Origin, f.Roots["b"], "repo")
	// The origin goes away, so the checkout still names it but the fetch has nothing to fetch from.
	if err := os.Rename(f.Origin, f.Origin+".gone"); err != nil {
		t.Fatal(err)
	}

	req := Request{Host: "b", Origin: f.Origin, Preferred: checkout}
	_, err := Ensure(t.Context(), f.Hosts["b"], f.Clients["b"], req)
	wantGitStderr(t, err)
}

func TestRepoOnHostMissingBaseNamesHost(t *testing.T) {
	f := fleettest.Start(t, "b")
	checkout := cloneLocal(t, f.Origin, f.Roots["b"], "repo")
	// No commit has this id, so the base is missing after the fetch.
	missing := strings.Repeat("1", 40)

	_, err := Ensure(t.Context(), f.Hosts["b"], f.Clients["b"],
		Request{Host: "b", Origin: f.Origin, Preferred: checkout, Base: missing})
	wantHostError(t, err, "b")
}
