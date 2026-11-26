package node

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/joshsymonds/orrery/internal/exectest"
	"github.com/joshsymonds/orrery/internal/holder"
	"github.com/joshsymonds/orrery/internal/hostexec"
)

// conformanceEnv is the host that a case runs against, with the directories the case may use. root is a directory that
// the host may read and write, and that a child may run in. state is the state directory that the host's session holders
// live in.
type conformanceEnv struct {
	host  hostexec.Host
	root  string
	state string
	// launched holds the session directories that a case launched through host, so runCase clears them while host serves.
	launched *[]string
}

// conformanceCase is one case of the table. Each case runs once against hostexec.NewLocal and once against a RemoteHost, on
// a fresh env of its own, and must pass under both.
type conformanceCase struct {
	name string
	run  func(t *testing.T, env conformanceEnv)
}

// conformanceCases is the table that TestRemoteHostConformance runs.
var conformanceCases = []conformanceCase{
	{"git success", caseGitSuccess},
	{"git failure", caseGitFailure},
	{"git that cannot start", caseGitCannotStart},
	{"read file", caseReadFile},
	{"read rooted", caseReadRooted},
	{"stat and lstat", caseStatAndLstat},
	{"read dir", caseReadDir},
	{"write file", caseWriteFile},
	{"symlink", caseSymlink},
	{"mkdir temp and mkdir all", caseMkdir},
	{"remove and remove all", caseRemove},
	{"eval symlinks", caseEvalSymlinks},
	{"file errors match fs kinds", caseFileErrors},
	{"session dir", caseSessionDir},
	{"held launch", caseLaunch},
	{"held reattach", caseReattach},
	{"held stop", caseStop},
	{"held clear", caseClear},
	{"held exit record", caseExitRecord},
	{"held kills leftovers", caseKillLeftovers},
}

// localEnv returns the env of hostexec.NewLocal: a root and a state directory of its own, which the test removes.
func localEnv(t *testing.T) conformanceEnv {
	t.Helper()
	return conformanceEnv{
		host:     hostexec.NewLocal(),
		root:     t.TempDir(),
		state:    shortStateDir(t),
		launched: new([]string),
	}
}

// remoteEnv returns the env of a RemoteHost over an agent that serves on a loopback listener until the test ends. The
// agent's allowed directories are the env's root and state directory, and the Client reaches the agent through the
// fake WhoIs of the agent tests.
func remoteEnv(t *testing.T) conformanceEnv {
	t.Helper()
	addr, dirs := startV2Agent(t)
	client := NewClient(ClientConfig{
		Name:    "remote",
		Address: addr,
		Dial:    (&net.Dialer{}).DialContext,
		Bound:   testBound,
		Clock:   newManualClock(),
		Secret:  testSecret,
	})
	return conformanceEnv{host: NewRemoteHost(client), root: dirs.root, state: dirs.state, launched: new([]string)}
}

// TestRemoteHostConformance runs each case of the table against hostexec.NewLocal and against a RemoteHost, and each case
// must pass under both. The held cases wait only on the child's own steps, through FIFOs, and never on a clock.
func TestRemoteHostConformance(t *testing.T) {
	for _, c := range conformanceCases {
		t.Run(c.name, func(t *testing.T) {
			t.Run("Local", func(t *testing.T) { runCase(t, c, localEnv(t)) })
			t.Run("RemoteHost", func(t *testing.T) { runCase(t, c, remoteEnv(t)) })
		})
	}
}

// runCase runs c on env's host, and then clears each session that c launched through it. The clearing happens in the
// test's body, not in a cleanup: the context of the agent that serves a RemoteHost ends before any cleanup runs, so the
// agent would already refuse the clear.
func runCase(t *testing.T, c conformanceCase, env conformanceEnv) {
	defer clearLaunched(t, env)
	c.run(t, env)
}

// clearLaunched clears each session directory that env's host launched, so that no holder outlives its case.
func clearLaunched(t *testing.T, env conformanceEnv) {
	t.Helper()
	for _, dir := range *env.launched {
		if err := env.host.Clear(context.Background(), dir); err != nil {
			t.Errorf("clear %s: %v", dir, err)
		}
	}
}

// writeConformanceFile writes contents to path with mode 0600, as a case's fixture.
func writeConformanceFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// conformanceRepo makes an empty git repository named name under env's root, and returns its path.
func conformanceRepo(t *testing.T, env conformanceEnv, name string) string {
	t.Helper()
	dir := filepath.Join(env.root, name)
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	v2InitRepo(t, dir)
	return dir
}

// caseGitSuccess runs git through the host. The stdout of a run is untrimmed, and the env and the stdin of a run reach git.
func caseGitSuccess(t *testing.T, env conformanceEnv) {
	repo := conformanceRepo(t, env, "repo")
	ctx := t.Context()
	top, err := env.host.Git(ctx, hostexec.GitCall{Dir: repo, Args: []string{"rev-parse", "--show-toplevel"}})
	if err != nil {
		t.Fatalf("rev-parse in dir: %v", err)
	}
	want, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatalf("resolve %s: %v", repo, err)
	}
	if string(top) != want+"\n" {
		t.Fatalf("rev-parse --show-toplevel = %q, want %q", top, want+"\n")
	}
	ident, err := env.host.Git(ctx, hostexec.GitCall{
		Dir:  repo,
		Args: []string{"var", "GIT_AUTHOR_IDENT"},
		Env:  []string{"GIT_AUTHOR_NAME=orrery-test", "GIT_AUTHOR_EMAIL=orrery-test@example.invalid"},
	})
	if err != nil {
		t.Fatalf("git var with env: %v", err)
	}
	if !strings.HasPrefix(string(ident), "orrery-test <orrery-test@example.invalid> ") {
		t.Fatalf("git var GIT_AUTHOR_IDENT = %q, want the identity the env names", ident)
	}
	sum, err := env.host.Git(ctx, hostexec.GitCall{
		Dir:   repo,
		Args:  []string{"hash-object", "--stdin"},
		Stdin: []byte("hello\n"),
	})
	if err != nil {
		t.Fatalf("git hash-object with stdin: %v", err)
	}
	if string(sum) != v2HelloBlob {
		t.Fatalf("hash-object --stdin = %q, want %q", sum, v2HelloBlob)
	}
}

// caseGitFailure runs a git that exits 1. Its error is a *GitError that carries the exit code, the stdout and stderr git
// wrote, and the arguments it ran with, and the stdout that Git returns is the GitError's.
func caseGitFailure(t *testing.T, env conformanceEnv) {
	repo := conformanceRepo(t, env, "repo")
	before := filepath.Join(env.root, "before")
	after := filepath.Join(env.root, "after")
	writeConformanceFile(t, before, "a\n")
	writeConformanceFile(t, after, "b\n")
	call := hostexec.GitCall{
		Dir:  repo,
		Args: []string{"diff", "--no-index", "--exit-code", before, after},
		// GIT_TRACE makes git write its trace to stderr, so this failure has stderr as well as stdout.
		Env: v2GitEnv("GIT_TRACE=1"),
	}
	out, err := env.host.Git(t.Context(), call)
	var gitErr *hostexec.GitError
	if !errors.As(err, &gitErr) {
		t.Fatalf("Git error = %v, want a *GitError", err)
	}
	if gitErr.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1 for a differing diff", gitErr.ExitCode)
	}
	if !bytes.Contains(gitErr.Stdout, []byte("+b\n")) || !bytes.Equal(out, gitErr.Stdout) {
		t.Fatalf("Stdout = %q, returned %q: want git's diff on both", gitErr.Stdout, out)
	}
	if !bytes.Contains(gitErr.Stderr, []byte("trace:")) {
		t.Fatalf("Stderr = %q, want git's trace", gitErr.Stderr)
	}
	if !slices.Equal(gitErr.Args, call.Args) {
		t.Fatalf("Args = %q, want %q", gitErr.Args, call.Args)
	}
}

// caseGitCannotStart runs git with a ctx that has already ended, so git never starts. The error is not a *GitError, as
// Local's is not.
func caseGitCannotStart(t *testing.T, env conformanceEnv) {
	repo := conformanceRepo(t, env, "repo")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := env.host.Git(ctx, hostexec.GitCall{Dir: repo, Args: []string{"status"}})
	if err == nil {
		t.Fatal("Git with an ended ctx succeeded")
	}
	var gitErr *hostexec.GitError
	if errors.As(err, &gitErr) {
		t.Fatalf("Git error = %v, want an error that is not a *GitError", err)
	}
}

// caseReadFile reads a file, and reads a missing one, which fails matching fs.ErrNotExist.
func caseReadFile(t *testing.T, env conformanceEnv) {
	ctx := t.Context()
	path := filepath.Join(env.root, "file")
	writeConformanceFile(t, path, "contents")
	got, err := env.host.ReadFile(ctx, path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "contents" {
		t.Fatalf("ReadFile = %q, want %q", got, "contents")
	}
	_, err = env.host.ReadFile(ctx, path+".missing")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("ReadFile of a missing file = %v, want ErrNotExist", err)
	}
}

// caseReadRooted reads a regular file inside a root, and refuses a symlink and a directory with ErrNotRegular.
func caseReadRooted(t *testing.T, env conformanceEnv) {
	ctx := t.Context()
	root := filepath.Join(env.root, "rooted")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeConformanceFile(t, filepath.Join(root, "file"), "contents")
	if err := os.Symlink("file", filepath.Join(root, "link")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if err := os.Mkdir(filepath.Join(root, "dir"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	got, err := env.host.ReadRooted(ctx, root, "file")
	if err != nil {
		t.Fatalf("ReadRooted of a regular file: %v", err)
	}
	if string(got) != "contents" {
		t.Fatalf("ReadRooted = %q, want %q", got, "contents")
	}
	for _, name := range []string{"link", "dir"} {
		_, err = env.host.ReadRooted(ctx, root, name)
		if !errors.Is(err, hostexec.ErrNotRegular) {
			t.Fatalf("ReadRooted(%q) = %v, want ErrNotRegular", name, err)
		}
	}
}

// caseStatAndLstat describes a symlink through Stat, which follows it, and Lstat, which does not, and a missing path
// through Stat, which fails matching fs.ErrNotExist.
func caseStatAndLstat(t *testing.T, env conformanceEnv) {
	ctx := t.Context()
	dir := filepath.Join(env.root, "stat")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeConformanceFile(t, filepath.Join(dir, "file"), "contents")
	if err := os.Symlink("file", filepath.Join(dir, "link")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	info, err := env.host.Stat(ctx, filepath.Join(dir, "link"))
	if err != nil {
		t.Fatalf("Stat of a link: %v", err)
	}
	if !info.Mode().IsRegular() || info.Size() != int64(len("contents")) {
		t.Fatalf("Stat of a link = mode %v size %d, want the regular target", info.Mode(), info.Size())
	}
	info, err = env.host.Lstat(ctx, filepath.Join(dir, "link"))
	if err != nil {
		t.Fatalf("Lstat of a link: %v", err)
	}
	if info.Mode()&fs.ModeSymlink == 0 {
		t.Fatalf("Lstat of a link = mode %v, want a symlink", info.Mode())
	}
	_, err = env.host.Stat(ctx, filepath.Join(dir, "missing"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Stat of a missing path = %v, want ErrNotExist", err)
	}
}

// caseReadDir lists a directory in name order, and reports the directory among the entries as one.
func caseReadDir(t *testing.T, env conformanceEnv) {
	dir := filepath.Join(env.root, "list")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeConformanceFile(t, filepath.Join(dir, "b"), "")
	writeConformanceFile(t, filepath.Join(dir, "a"), "")
	if err := os.Mkdir(filepath.Join(dir, "c"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	entries, err := env.host.ReadDir(t.Context(), dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if !slices.Equal(names, []string{"a", "b", "c"}) {
		t.Fatalf("ReadDir names = %q, want [a b c]", names)
	}
	if !entries[2].IsDir() {
		t.Fatalf("entry c is not a directory")
	}
}

// caseWriteFile writes a file, then overwrites it with a shorter write, and the file keeps the second write alone with its
// mode.
func caseWriteFile(t *testing.T, env conformanceEnv) {
	ctx := t.Context()
	path := filepath.Join(env.root, "file")
	if err := env.host.WriteFile(ctx, path, []byte("a longer first write"), 0o600); err != nil {
		t.Fatalf("WriteFile to a missing file: %v", err)
	}
	if err := env.host.WriteFile(ctx, path, []byte("short"), 0o600); err != nil {
		t.Fatalf("WriteFile over a file: %v", err)
	}
	got, err := env.host.ReadFile(ctx, path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != "short" {
		t.Fatalf("file = %q, want the second write alone", got)
	}
	info, err := env.host.Stat(ctx, path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", info.Mode().Perm())
	}
}

// caseSymlink makes a symbolic link to a target that does not exist, which reads as a link.
func caseSymlink(t *testing.T, env conformanceEnv) {
	ctx := t.Context()
	link := filepath.Join(env.root, "link")
	if err := env.host.Symlink(ctx, "target", link); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	info, err := env.host.Lstat(ctx, link)
	if err != nil {
		t.Fatalf("Lstat of the new link: %v", err)
	}
	if info.Mode()&fs.ModeSymlink == 0 {
		t.Fatalf("new link mode = %v, want a symlink", info.Mode())
	}
}

// caseMkdir makes a temporary directory in a directory, and a path with missing parents, and makes the path again.
func caseMkdir(t *testing.T, env conformanceEnv) {
	ctx := t.Context()
	made, err := env.host.MkdirTemp(ctx, env.root, "orrery-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	if filepath.Dir(made) != env.root || !strings.HasPrefix(filepath.Base(made), "orrery-") {
		t.Fatalf("MkdirTemp made %s, want a new directory in %s named orrery-*", made, env.root)
	}
	info, err := env.host.Stat(ctx, made)
	if err != nil {
		t.Fatalf("stat of the temporary directory: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("MkdirTemp made %s, which is not a directory", made)
	}
	path := filepath.Join(env.root, "a", "b", "c")
	if err = env.host.MkdirAll(ctx, path, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err = env.host.MkdirAll(ctx, path, 0o700); err != nil {
		t.Fatalf("MkdirAll of an existing directory: %v", err)
	}
	info, err = env.host.Stat(ctx, path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("MkdirAll made %s, which is not a directory", path)
	}
}

// caseRemove removes a file and a tree, and a missing file fails matching fs.ErrNotExist, while a missing tree is not an
// error.
func caseRemove(t *testing.T, env conformanceEnv) {
	ctx := t.Context()
	file := filepath.Join(env.root, "file")
	writeConformanceFile(t, file, "")
	if err := env.host.Remove(ctx, file); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := env.host.Lstat(ctx, file); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Lstat after Remove = %v, want ErrNotExist", err)
	}
	if err := env.host.Remove(ctx, file); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Remove of a missing file = %v, want ErrNotExist", err)
	}
	tree := filepath.Join(env.root, "tree")
	if err := os.MkdirAll(filepath.Join(tree, "sub"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeConformanceFile(t, filepath.Join(tree, "sub", "file"), "")
	if err := env.host.RemoveAll(ctx, tree); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}
	if _, err := env.host.Lstat(ctx, tree); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Lstat after RemoveAll = %v, want ErrNotExist", err)
	}
	if err := env.host.RemoveAll(ctx, tree); err != nil {
		t.Fatalf("RemoveAll of a missing path: %v", err)
	}
}

// caseEvalSymlinks resolves a symlink to the path its target has.
func caseEvalSymlinks(t *testing.T, env conformanceEnv) {
	target := filepath.Join(env.root, "target")
	writeConformanceFile(t, target, "")
	link := filepath.Join(env.root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	want, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatalf("EvalSymlinks of the target: %v", err)
	}
	got, err := env.host.EvalSymlinks(t.Context(), link)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	if got != want {
		t.Fatalf("EvalSymlinks = %q, want %q", got, want)
	}
}

// caseFileErrors checks the fs kinds of the errors that a file op returns: a write into a missing directory matches
// fs.ErrNotExist, and a symlink over an existing path matches fs.ErrExist.
func caseFileErrors(t *testing.T, env conformanceEnv) {
	ctx := t.Context()
	missing := filepath.Join(env.root, "missing", "file")
	if err := env.host.WriteFile(ctx, missing, []byte("x"), 0o600); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("WriteFile into a missing directory = %v, want ErrNotExist", err)
	}
	existing := filepath.Join(env.root, "existing")
	writeConformanceFile(t, existing, "")
	if err := env.host.Symlink(ctx, "target", existing); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("Symlink over an existing path = %v, want ErrExist", err)
	}
}

// caseSessionDir computes a session's directory as holder.SessionDir does, and refuses a session name that is not a single
// safe path element.
func caseSessionDir(t *testing.T, env conformanceEnv) {
	holders := filepath.Join(env.state, holdersName)
	got, err := env.host.SessionDir(holders, "s1")
	if err != nil {
		t.Fatalf("SessionDir: %v", err)
	}
	want, err := holder.SessionDir(holders, "s1")
	if err != nil {
		t.Fatalf("holder.SessionDir: %v", err)
	}
	if got != want {
		t.Fatalf("SessionDir = %q, want %q", got, want)
	}
	if _, err = env.host.SessionDir(holders, "../escape"); err == nil {
		t.Fatal("SessionDir of a session name with a path in it succeeded")
	}
}

// conformanceLaunch returns the Launch that runs script as a gate under session in env's state directory, in env's root,
// with the FIFOs of g in its environment.
func conformanceLaunch(env conformanceEnv, g v2Gate, session, script string) hostexec.Launch {
	return hostexec.Launch{
		StateDir: env.state,
		Session:  session,
		Path:     "/bin/sh",
		Args:     []string{"-c", script},
		Dir:      env.root,
		Env: []string{
			"PATH=" + os.Getenv("PATH"),
			"STARTED=" + g.started,
			"RELEASE=" + g.release,
			"MEMBER=" + g.member,
		},
		Limit: v2GateLimit,
	}
}

// launchConformanceHeld launches the gate child that script runs as session through env's host. It returns the child's
// Held and its session directory, which runCase clears once the case ends.
func launchConformanceHeld(t *testing.T, env conformanceEnv, g v2Gate, session, script string) (hostexec.Held, string) {
	t.Helper()
	held, err := env.host.Launch(t.Context(), conformanceLaunch(env, g, session, script))
	if err != nil {
		t.Fatalf("Launch %s: %v", session, err)
	}
	dir, err := env.host.SessionDir(filepath.Join(env.state, holdersName), session)
	if err != nil {
		t.Fatalf("SessionDir %s: %v", session, err)
	}
	*env.launched = append(*env.launched, dir)
	return held, dir
}

// caseLaunch launches a held child, which runs and has no exit yet, and whose session directory exists.
func caseLaunch(t *testing.T, env conformanceEnv) {
	g := newV2Gate(t)
	held, dir := launchConformanceHeld(t, env, g, "launch", v2HeldScript)
	g.awaitStarted(t)
	if _, ended := held.Exit(); ended {
		t.Fatal("Exit reports the child ended while it still runs")
	}
	if _, err := env.host.Lstat(t.Context(), dir); err != nil {
		t.Fatalf("session dir %s: %v", dir, err)
	}
}

// caseReattach detaches a held child, reattaches to its directory, and waits for the reattached child to end with its exit
// record. A reattach to a session that has no holder fails matching ErrGone.
func caseReattach(t *testing.T, env conformanceEnv) {
	ctx := t.Context()
	g := newV2Gate(t)
	held, dir := launchConformanceHeld(t, env, g, "reattach", v2HeldScript)
	g.awaitStarted(t)
	if err := held.Detach(); err != nil {
		t.Fatalf("Detach: %v", err)
	}
	_, err := env.host.Reattach(ctx, filepath.Join(env.state, holdersName, "missing"), "")
	if !errors.Is(err, hostexec.ErrGone) {
		t.Fatalf("Reattach of a session with no holder = %v, want ErrGone", err)
	}
	reattached, err := env.host.Reattach(ctx, dir, "")
	if err != nil {
		t.Fatalf("Reattach: %v", err)
	}
	g.releaseChild(t)
	if err = reattached.Wait(); err != nil {
		t.Fatalf("Wait of the reattached child: %v", err)
	}
	exit, ok := env.host.ExitRecord(dir)
	if !ok || exit.Reason != hostexec.ReasonExited || exit.Code != 0 || exit.Stdout != "done\n" {
		t.Fatalf("exit record = %+v, %v: want the reattached child's exit 0 with its output", exit, ok)
	}
}

// caseStop stops a held child, which leaves its directory in place, and then the holder is gone, and the exit record is a
// stopped one.
func caseStop(t *testing.T, env conformanceEnv) {
	ctx := t.Context()
	g := newV2Gate(t)
	_, dir := launchConformanceHeld(t, env, g, "stop", v2HeldScript)
	g.awaitStarted(t)
	if err := env.host.Stop(ctx, dir); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if _, err := env.host.Lstat(ctx, dir); err != nil {
		t.Fatalf("Stop removed %s: %v", dir, err)
	}
	if _, err := env.host.Reattach(ctx, dir, ""); !errors.Is(err, hostexec.ErrGone) {
		t.Fatalf("Reattach after Stop = %v, want ErrGone", err)
	}
	exit, ok := env.host.ExitRecord(dir)
	if !ok || exit.Reason != hostexec.ReasonStopped {
		t.Fatalf("exit record = %+v, %v: want a stopped record", exit, ok)
	}
}

// caseClear clears a held child's directory, which is then gone, and clearing a directory that is gone is not an error.
func caseClear(t *testing.T, env conformanceEnv) {
	ctx := t.Context()
	g := newV2Gate(t)
	_, dir := launchConformanceHeld(t, env, g, "clear", v2HeldScript)
	g.awaitStarted(t)
	if err := env.host.Clear(ctx, dir); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if _, err := env.host.Lstat(ctx, dir); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Lstat after Clear = %v, want ErrNotExist", err)
	}
	if err := env.host.Clear(ctx, dir); err != nil {
		t.Fatalf("Clear of a missing dir: %v", err)
	}
}

// caseExitRecord reads a held child's exit record once the child has ended, and finds none while it runs or in a directory
// that has none.
func caseExitRecord(t *testing.T, env conformanceEnv) {
	g := newV2Gate(t)
	held, dir := launchConformanceHeld(t, env, g, "exit-record", v2HeldScript)
	g.awaitStarted(t)
	if _, ok := env.host.ExitRecord(dir); ok {
		t.Fatal("ExitRecord reports a record while the child runs")
	}
	g.releaseChild(t)
	if err := held.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	exit, ok := env.host.ExitRecord(dir)
	if !ok || exit.Reason != hostexec.ReasonExited || exit.Code != 0 || exit.Stdout != "done\n" {
		t.Fatalf("exit record = %+v, %v: want the child's exit 0 with its output", exit, ok)
	}
	if _, ok = env.host.ExitRecord(filepath.Join(env.root, "no-session")); ok {
		t.Fatal("ExitRecord reports a record in a dir with none")
	}
}

// caseKillLeftovers kills the process that a held child left running in its group, once the holder has died without
// signaling the group and the child has ended.
func caseKillLeftovers(t *testing.T, env conformanceEnv) {
	g := newV2Gate(t)
	_, dir := launchConformanceHeld(t, env, g, "leftovers", v2LeftoverScript)
	g.awaitStarted(t)
	member := v2ReadPID(t, g.member)
	t.Cleanup(func() {
		if err := syscall.Kill(member, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
			t.Errorf("kill member %d at cleanup: %v", member, err)
		}
	})
	pids, err := holder.ReadPids(dir)
	if err != nil {
		t.Fatalf("pids record of the session: %v", err)
	}
	// The holder dies without signaling its child's group, so the member is left in the group for KillLeftovers. A live
	// holder would kill the group itself once its child was reaped.
	if err = syscall.Kill(pids.Holder, syscall.SIGKILL); err != nil {
		t.Fatalf("kill holder %d: %v", pids.Holder, err)
	}
	v2WaitExit(t, pids.Holder)
	g.releaseChild(t)
	v2WaitExit(t, pids.Child)
	if exectest.KilledOrGone(t, member) {
		t.Fatalf("member %d is gone though its holder died without signaling the group", member)
	}
	if err = env.host.KillLeftovers(dir); err != nil {
		t.Fatalf("KillLeftovers: %v", err)
	}
	if !exectest.KilledOrGone(t, member) {
		t.Fatalf("KillLeftovers left member %d running", member)
	}
}
