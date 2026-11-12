package pools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// isolate points HOME at a fresh directory and keeps git from reading any configuration but a repository's own, so
// a test reads nothing from the machine it runs on. It returns the home directory.
func isolate(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, "gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	return home
}

// fakeEnv returns a getenv that reads vars and reports every other name as unset.
func fakeEnv(vars map[string]string) func(string) string {
	return func(name string) string { return vars[name] }
}

// resolvedDir returns a fresh temporary directory with its symlinks resolved, so its path matches what git reports.
func resolvedDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// writeFile creates path with text, along with any missing parent directories.
func writeFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), os.ModePerm); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

// runGit runs git -C dir with args and fails the test on error.
func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// newRepo makes a git repository with one commit and returns its root.
func newRepo(t *testing.T) string {
	t.Helper()
	root := resolvedDir(t)
	runGit(t, root, "init", "-q")
	runGit(t, root, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-q", "--allow-empty",
		"-m", "init")
	return root
}

// mustDiscover returns Discover's result for dir and fails the test on error.
func mustDiscover(t *testing.T, dir string, env func(string) string) Files {
	t.Helper()
	files, err := Discover(context.Background(), dir, env)
	if err != nil {
		t.Fatalf("Discover(%s): %v", dir, err)
	}
	return files
}

// mustLoad returns Load's result for dir and fails the test on error.
func mustLoad(t *testing.T, dir string, env func(string) string) (string, []Pool) {
	t.Helper()
	project, pools, err := Load(context.Background(), dir, env)
	if err != nil {
		t.Fatalf("Load(%s): %v", dir, err)
	}
	return project, pools
}

// poolText renders a valid pool file that defines one pool named name on host, with the given members.
func poolText(name, host string, members ...string) string {
	quoted := make([]string, 0, len(members))
	for _, member := range members {
		quoted = append(quoted, strconv.Quote(member))
	}
	return fmt.Sprintf(`[pools.%s]
host = %q
cap = 1
members = [%s]
wake = ["wake"]
wake_limit = 1
status = ["status"]
status_limit = 1
admit = ["admit"]
admit_limit = 1
`, name, host, strings.Join(quoted, ", "))
}

// poolNames returns the names of pools in order.
func poolNames(pools []Pool) []string {
	names := make([]string, 0, len(pools))
	for _, pool := range pools {
		names = append(names, pool.Name)
	}
	return names
}

func TestDiscoverNamesGitRepositoryAsProject(t *testing.T) {
	home := isolate(t)
	env := fakeEnv(map[string]string{"HOME": home})
	root := newRepo(t)
	dir := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(dir, os.ModePerm); err != nil {
		t.Fatal(err)
	}
	got := mustDiscover(t, dir, env)
	if want := (Files{Project: root}); got != want {
		t.Fatalf("Discover(%s) = %+v, want %+v", dir, got, want)
	}
}

func TestDiscoverFindsProjectFileAtWorkTreeTop(t *testing.T) {
	home := isolate(t)
	env := fakeEnv(map[string]string{"HOME": home})
	root := newRepo(t)
	dir := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(dir, os.ModePerm); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "a", ".orrery", "project.toml"), "")

	got := mustDiscover(t, dir, env)
	if got.ProjectFile != "" {
		t.Fatalf("a project file below the work tree top was used: %+v", got)
	}

	top := filepath.Join(root, ".orrery", "project.toml")
	writeFile(t, top, "")
	got = mustDiscover(t, dir, env)
	if want := (Files{Project: root, ProjectFile: top}); got != want {
		t.Fatalf("Discover(%s) = %+v, want %+v", dir, got, want)
	}
}

func TestDiscoverLinkedWorktreeNamesMainCheckout(t *testing.T) {
	home := isolate(t)
	env := fakeEnv(map[string]string{"HOME": home})
	root := newRepo(t)
	writeFile(t, filepath.Join(root, ".orrery", "project.toml"), "")
	wt := filepath.Join(resolvedDir(t), "wt")
	runGit(t, root, "worktree", "add", "-q", "--detach", wt)
	wtFile := filepath.Join(wt, ".orrery", "project.toml")
	writeFile(t, wtFile, "")

	dir := filepath.Join(wt, "sub")
	if err := os.MkdirAll(dir, os.ModePerm); err != nil {
		t.Fatal(err)
	}
	got := mustDiscover(t, dir, env)
	if want := (Files{Project: root, ProjectFile: wtFile}); got != want {
		t.Fatalf("Discover(%s) = %+v, want %+v", dir, got, want)
	}
}

func TestDiscoverOutsideGitUsesNearestProjectFile(t *testing.T) {
	home := isolate(t)
	env := fakeEnv(map[string]string{"HOME": home})
	root := resolvedDir(t)
	outer := filepath.Join(root, ".orrery", "project.toml")
	inner := filepath.Join(root, "inner", ".orrery", "project.toml")
	writeFile(t, outer, "")
	writeFile(t, inner, "")

	deep := filepath.Join(root, "inner", "deep")
	if err := os.MkdirAll(deep, os.ModePerm); err != nil {
		t.Fatal(err)
	}
	got := mustDiscover(t, deep, env)
	if want := (Files{Project: filepath.Join(root, "inner"), ProjectFile: inner}); got != want {
		t.Fatalf("Discover(%s) = %+v, want %+v", deep, got, want)
	}

	other := filepath.Join(root, "other")
	if err := os.MkdirAll(other, os.ModePerm); err != nil {
		t.Fatal(err)
	}
	got = mustDiscover(t, other, env)
	if want := (Files{Project: root, ProjectFile: outer}); got != want {
		t.Fatalf("Discover(%s) = %+v, want %+v", other, got, want)
	}
}

func TestDiscoverOutsideGitWithoutProjectFileNamesDirectory(t *testing.T) {
	home := isolate(t)
	env := fakeEnv(map[string]string{"HOME": home})
	dir := resolvedDir(t)
	got := mustDiscover(t, dir, env)
	if want := (Files{Project: dir}); got != want {
		t.Fatalf("Discover(%s) = %+v, want %+v", dir, got, want)
	}
}

func TestDiscoverResolvesRelativeDirectory(t *testing.T) {
	home := isolate(t)
	env := fakeEnv(map[string]string{"HOME": home})
	root := resolvedDir(t)
	sub := filepath.Join(root, "sub")
	if err := os.MkdirAll(sub, os.ModePerm); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	got := mustDiscover(t, "sub", env)
	if want := (Files{Project: sub}); got != want {
		t.Fatalf("Discover(sub) = %+v, want %+v", got, want)
	}
}

func TestDiscoverRefusesProjectPathThatIsNotADirectory(t *testing.T) {
	home := isolate(t)
	env := fakeEnv(map[string]string{"HOME": home})
	dir := resolvedDir(t)
	writeFile(t, filepath.Join(dir, ".orrery"), "not a directory")
	if _, err := Discover(context.Background(), dir, env); err == nil {
		t.Fatal("Discover read through a .orrery that is a file, want an error")
	}
}

func TestDiscoverRefusesMissingDirectory(t *testing.T) {
	home := isolate(t)
	env := fakeEnv(map[string]string{"HOME": home})
	root := newRepo(t)
	if _, err := Discover(context.Background(), filepath.Join(root, "absent"), env); err == nil {
		t.Fatal("Discover of a directory that does not exist returned no error")
	}
}

func TestDiscoverRefusesCanceledContext(t *testing.T) {
	home := isolate(t)
	env := fakeEnv(map[string]string{"HOME": home})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Discover(ctx, newRepo(t), env); err == nil {
		t.Fatal("Discover with a canceled context returned no error")
	}
}

func TestDiscoverRefusesMissingGit(t *testing.T) {
	isolate(t)
	t.Setenv("PATH", t.TempDir())
	env := fakeEnv(map[string]string{"HOME": t.TempDir()})
	if _, err := Discover(context.Background(), resolvedDir(t), env); err == nil {
		t.Fatal("Discover without a git on PATH returned no error")
	}
}

func TestDiscoverMachineFileFollowsXDGConfigHome(t *testing.T) {
	isolate(t)
	home := t.TempDir()
	xdg := t.TempDir()
	writeFile(t, filepath.Join(home, ".config", "orrery", "pools.toml"), "")
	xdgFile := filepath.Join(xdg, "orrery", "pools.toml")
	writeFile(t, xdgFile, "")
	env := fakeEnv(map[string]string{"HOME": home, "XDG_CONFIG_HOME": xdg})
	got := mustDiscover(t, resolvedDir(t), env)
	if got.MachineFile != xdgFile {
		t.Fatalf("MachineFile = %q, want %q", got.MachineFile, xdgFile)
	}
}

func TestDiscoverMachineFileFallsBackToHome(t *testing.T) {
	isolate(t)
	home := t.TempDir()
	homeFile := filepath.Join(home, ".config", "orrery", "pools.toml")
	writeFile(t, homeFile, "")
	envs := []map[string]string{
		{"HOME": home, "XDG_CONFIG_HOME": ""},
		{"HOME": home},
	}
	for _, vars := range envs {
		got := mustDiscover(t, resolvedDir(t), fakeEnv(vars))
		if got.MachineFile != homeFile {
			t.Fatalf("%v: MachineFile = %q, want %q", vars, got.MachineFile, homeFile)
		}
	}
}

func TestDiscoverMachineFileEmptyWhenMissing(t *testing.T) {
	home := isolate(t)
	env := fakeEnv(map[string]string{"HOME": home})
	got := mustDiscover(t, resolvedDir(t), env)
	if got.MachineFile != "" {
		t.Fatalf("MachineFile = %q for a missing file, want empty", got.MachineFile)
	}
}

func TestDiscoverRefusesMissingHomeAndXDG(t *testing.T) {
	isolate(t)
	if _, err := Discover(context.Background(), resolvedDir(t), fakeEnv(nil)); err == nil {
		t.Fatal("Discover with neither HOME nor XDG_CONFIG_HOME returned no error")
	}
}

func TestLoadMergesProjectAndMachinePools(t *testing.T) {
	home := isolate(t)
	root := newRepo(t)
	writeFile(t, filepath.Join(root, ".orrery", "project.toml"), poolText("gpu", "box-a", "g1", "g2"))
	writeFile(t, filepath.Join(home, ".config", "orrery", "pools.toml"), poolText("cpu", "box-b", "c1"))
	env := fakeEnv(map[string]string{"HOME": home})
	sub := filepath.Join(root, "sub")
	if err := os.MkdirAll(sub, os.ModePerm); err != nil {
		t.Fatal(err)
	}

	project, pools := mustLoad(t, sub, env)
	if project != root {
		t.Fatalf("project = %q, want %q", project, root)
	}
	if want := []string{"cpu", "gpu"}; !slices.Equal(poolNames(pools), want) {
		t.Fatalf("pool names = %v, want %v", poolNames(pools), want)
	}
	if want := []string{"g1", "g2"}; !slices.Equal(pools[1].Members, want) {
		t.Fatalf("gpu members = %v, want %v", pools[1].Members, want)
	}
}

func TestLoadKeepsIdenticalPoolOnce(t *testing.T) {
	home := isolate(t)
	root := newRepo(t)
	text := poolText("gpu", "box-a", "g1")
	writeFile(t, filepath.Join(root, ".orrery", "project.toml"), text)
	writeFile(t, filepath.Join(home, ".config", "orrery", "pools.toml"), text)
	env := fakeEnv(map[string]string{"HOME": home})

	_, pools := mustLoad(t, root, env)
	if want := []string{"gpu"}; !slices.Equal(poolNames(pools), want) {
		t.Fatalf("pool names = %v, want %v", poolNames(pools), want)
	}
}

func TestLoadRefusesPoolDefinedTwice(t *testing.T) {
	home := isolate(t)
	root := newRepo(t)
	writeFile(t, filepath.Join(root, ".orrery", "project.toml"), poolText("gpu", "box-a", "g1"))
	writeFile(t, filepath.Join(home, ".config", "orrery", "pools.toml"), poolText("gpu", "box-b", "g1"))
	env := fakeEnv(map[string]string{"HOME": home})

	_, _, err := Load(context.Background(), root, env)
	if !errors.Is(err, ErrDefinedTwice) {
		t.Fatalf("Load error = %v, want ErrDefinedTwice", err)
	}
}

func TestLoadRefusesMemberInTwoPools(t *testing.T) {
	home := isolate(t)
	root := newRepo(t)
	writeFile(t, filepath.Join(root, ".orrery", "project.toml"), poolText("gpu", "box-a", "shared"))
	writeFile(t, filepath.Join(home, ".config", "orrery", "pools.toml"), poolText("cpu", "box-a", "shared"))
	env := fakeEnv(map[string]string{"HOME": home})

	_, _, err := Load(context.Background(), root, env)
	if !errors.Is(err, ErrDefinedTwice) {
		t.Fatalf("Load error = %v, want ErrDefinedTwice", err)
	}
}

func TestLoadRefusesFileThatDoesNotParse(t *testing.T) {
	home := isolate(t)
	root := newRepo(t)
	projectFile := filepath.Join(root, ".orrery", "project.toml")
	writeFile(t, projectFile, "[pools.gpu\n")
	env := fakeEnv(map[string]string{"HOME": home})

	_, _, err := Load(context.Background(), root, env)
	if err == nil || !strings.Contains(err.Error(), projectFile) {
		t.Fatalf("Load error = %v, want an error naming %s", err, projectFile)
	}
}

func TestLoadRefusesUnreadableFile(t *testing.T) {
	home := isolate(t)
	root := newRepo(t)
	projectFile := filepath.Join(root, ".orrery", "project.toml")
	if err := os.MkdirAll(projectFile, os.ModePerm); err != nil {
		t.Fatal(err)
	}
	env := fakeEnv(map[string]string{"HOME": home})

	_, _, err := Load(context.Background(), root, env)
	if err == nil || !strings.Contains(err.Error(), projectFile) {
		t.Fatalf("Load error = %v, want an error naming %s", err, projectFile)
	}
}

func TestLoadWithoutFilesGivesNoPools(t *testing.T) {
	home := isolate(t)
	dir := resolvedDir(t)
	env := fakeEnv(map[string]string{"HOME": home})

	project, pools, err := Load(context.Background(), dir, env)
	if err != nil {
		t.Fatalf("Load(%s): %v", dir, err)
	}
	if project != dir || len(pools) != 0 {
		t.Fatalf("Load(%s) = %q, %v; want %q and no pools", dir, project, pools, dir)
	}
}
