package node

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/joshsymonds/orrery/internal/exectest"
	"github.com/joshsymonds/orrery/internal/holder"
)

// v1Version is the protocol version an older controller names, which the agent refuses.
const v1Version = "node.v1"

// v2GateLimit is the limit of each gate the v2 tests launch. A gate with a limit keeps its session directory once its
// child ends, so the tests need one. The limit is the holder's own timer, which no test waits on. It is far longer than any
// test runs, so the timer never ends a child early.
const v2GateLimit = 24 * time.Hour

// v2HelloBlob is the object name git gives the blob of "hello\n".
const v2HelloBlob = "ce013625030ba8dba906f756967f9e9ca394464a\n"

// v2HeldScript is the child of the gate tests that must keep running: it writes ready to $STARTED once it runs, blocks
// until a line arrives on $RELEASE, prints done and exits 0.
const v2HeldScript = `echo ready > "$STARTED"; read -r _ < "$RELEASE"; echo done; exit 0`

// v2LeftoverScript is the child of the leftovers test: it leaves a sleep running in its process group, records the
// sleep's pid in $MEMBER, and then waits as v2HeldScript does.
const v2LeftoverScript = `sleep 600 >/dev/null 2>&1 & echo "$!" > "$MEMBER"; ` +
	`echo ready > "$STARTED"; read -r _ < "$RELEASE"; exit 0`

// v2CarryScript is the child of the launch test: it records its argv, its $MARK and its directory in $RECORD, writes a
// 37-byte line to stdout, and then waits as v2HeldScript does before it prints done.
const v2CarryScript = `printf '%s\n' "$0" "$@" > "$RECORD"; printf '%s\n' "$MARK" "$(pwd -P)" >> "$RECORD"; ` +
	`printf '0123456789abcdefghijklmnopqrstuvwxyz\n'; echo ready > "$STARTED"; ` +
	`read -r _ < "$RELEASE"; echo done; exit 0`

// v2Dirs are the directories of a v2 test agent: its roots, its state directory, and its config and Claude directories.
type v2Dirs struct {
	root   string
	state  string
	config string
	claude string
}

// v2Named is one of the directories an agent allows, with the name that a subtest gives it.
type v2Named struct {
	name string
	dir  string
}

// named returns each directory an agent allows, with its name.
func (d v2Dirs) named() []v2Named {
	return []v2Named{{"roots", d.root}, {"state dir", d.state}, {"config dir", d.config}, {"claude dir", d.claude}}
}

// startV2Agent serves an Agent over a loopback listener until the test ends. Its roots, state directory, config directory
// and Claude directory are the directories it returns, and its holders are nodeLocal's, which run the fake claude.
func startV2Agent(t *testing.T) (string, v2Dirs) {
	t.Helper()
	local, stateDir := nodeLocal(t)
	dirs := v2Dirs{root: t.TempDir(), state: stateDir, config: t.TempDir(), claude: t.TempDir()}
	ln := listenLoopback(t)
	agent, err := NewAgent(AgentConfig{
		Controller: testController,
		WhoIs:      peerWhoIs{own: ln.Addr().String()},
		Holders:    local,
		StateDir:   stateDir,
		Roots:      []string{dirs.root},
		ConfigDir:  dirs.config,
		ClaudeDir:  dirs.claude,
		Secret:     testSecret,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	served := make(chan error, 1)
	go func() { served <- agent.Serve(ctx, ln) }()
	t.Cleanup(func() {
		cancel()
		if serveErr := <-served; serveErr != nil {
			t.Error(serveErr)
		}
	})
	return ln.Addr().String(), dirs
}

// v2Sibling makes the directory beside dir, which lies outside dir though it shares dir's name, so that a check on name
// prefixes alone would admit it. The directory is removed when the test ends.
func v2Sibling(t *testing.T, dir string) string {
	t.Helper()
	sibling := dir + "-outside"
	if err := os.Mkdir(sibling, 0o700); err != nil {
		t.Fatalf("make %s: %v", sibling, err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(sibling); err != nil {
			t.Errorf("remove %s: %v", sibling, err)
		}
	})
	return sibling
}

// v2Call sends req from the controller to the agent at addr, with the fleet secret and the agent's version, and returns
// the Response.
func v2Call(t *testing.T, addr string, req Request) Response {
	t.Helper()
	req.Version = Version
	req.Secret = testSecret
	return ask(t, addr, req)
}

// v2File calls the file op spec at the agent at addr.
func v2File(t *testing.T, addr string, spec FileSpec) Response {
	t.Helper()
	return v2Call(t, addr, Request{Op: OpFile, File: &spec})
}

// v2GitEnv is the environment of each git run the v2 tests make, with the global and system config of git left out, so
// that the output the tests compare does not depend on the owner's config. The extra entries follow.
func v2GitEnv(extra ...string) []string {
	return append([]string{"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1"}, extra...)
}

// v2Git calls the git op spec at the agent at addr, in the environment that v2GitEnv gives with spec's own Env after it.
func v2Git(t *testing.T, addr string, spec GitSpec) Response {
	t.Helper()
	spec.Env = v2GitEnv(spec.Env...)
	return v2Call(t, addr, Request{Op: OpGit, Git: &spec})
}

// v2InitRepo makes an empty git repository in dir, which must exist.
func v2InitRepo(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", "init", "-q", dir)
	cmd.Env = v2GitEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init %s: %v\n%s", dir, err, out)
	}
}

// v2Refused fails the test unless resp refuses its op with CodeRefused.
func v2Refused(t *testing.T, resp Response) {
	t.Helper()
	if resp.Code != CodeRefused {
		t.Fatalf("op answered %+v, want code %q", resp, CodeRefused)
	}
}

// v2Absent fails the test unless nothing lies at path, so that a refused op left no file or directory behind.
func v2Absent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("%s exists or cannot be inspected (%v), though the op naming it was refused", path, err)
	}
}

// v2HolderDir returns the holder directory of session under stateDir.
func v2HolderDir(stateDir, session string) string {
	return filepath.Join(stateDir, holdersName, session)
}

// v2GateSpec returns the GateSpec that runs script under session in stateDir, in dir, with the FIFOs of g in its environment.
func v2GateSpec(dir, stateDir string, g v2Gate, session, script string) GateSpec {
	return GateSpec{
		StateDir: stateDir,
		Session:  session,
		Path:     "/bin/sh",
		Args:     []string{"-c", script},
		Dir:      dir,
		Env: []string{
			"PATH=" + os.Getenv("PATH"),
			"STARTED=" + g.started,
			"RELEASE=" + g.release,
			"MEMBER=" + g.member,
		},
		Limit: v2GateLimit,
	}
}

// v2Launch launches the gate that spec describes through the agent at addr, and fails the test when the launch is refused.
func v2Launch(t *testing.T, addr string, spec GateSpec) {
	t.Helper()
	if resp := v2Call(t, addr, Request{Op: OpGateLaunch, Gate: &spec}); resp.Code != "" {
		t.Fatalf("gate launch %s: %+v", spec.Session, resp)
	}
}

// v2Gate is a gate child that a test follows through FIFOs, so that the test waits on the child's steps and never on a
// clock. The child writes ready to started once it runs, and it reads a line from release before it goes on. It writes
// its pid to member, when its script does.
type v2Gate struct {
	started string
	release string
	member  string
}

// newV2Gate returns a v2Gate whose FIFOs and member file lie in a new directory of the test's.
func newV2Gate(t *testing.T) v2Gate {
	t.Helper()
	dir := t.TempDir()
	g := v2Gate{
		started: filepath.Join(dir, "started"),
		release: filepath.Join(dir, "release"),
		member:  filepath.Join(dir, "member"),
	}
	for _, fifo := range []string{g.started, g.release} {
		if err := syscall.Mkfifo(fifo, 0o600); err != nil {
			t.Fatalf("mkfifo %s: %v", fifo, err)
		}
	}
	return g
}

// awaitStarted blocks until the child writes ready to its started FIFO. The open waits for the child's writer.
func (g v2Gate) awaitStarted(t *testing.T) {
	t.Helper()
	fifo, err := os.Open(g.started)
	if err != nil {
		t.Fatalf("open %s: %v", g.started, err)
	}
	data, readErr := io.ReadAll(fifo)
	if err = errors.Join(readErr, fifo.Close()); err != nil {
		t.Fatalf("read %s: %v", g.started, err)
	}
	if string(data) != "ready\n" {
		t.Fatalf("child wrote %q to its started FIFO, want ready", data)
	}
}

// releaseChild lets the child go on. It writes a line to the release FIFO, which the child reads once it blocks on it.
func (g v2Gate) releaseChild(t *testing.T) {
	t.Helper()
	fifo, err := os.OpenFile(g.release, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open %s: %v", g.release, err)
	}
	_, writeErr := fifo.WriteString("go\n")
	if err = errors.Join(writeErr, fifo.Close()); err != nil {
		t.Fatalf("release the child through %s: %v", g.release, err)
	}
}

// v2FakeScope returns a systemd-run that records its argv in a file and then runs the program that follows its --unit
// argument, and a systemctl beside it that stops nothing. It returns the systemd-run's path and the record's path.
func v2FakeScope(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	record := filepath.Join(dir, "argv")
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$@\" > '" + record + "'\n" +
		"while [ $# -gt 0 ] && [ \"$1\" != \"--unit\" ]; do shift; done\n" +
		"shift 2\n" +
		"exec \"$@\"\n"
	run := filepath.Join(dir, "systemd-run")
	exectest.WriteFile(t, run, []byte(script), 0o700)
	exectest.WriteFile(t, filepath.Join(dir, "systemctl"), []byte("#!/bin/sh\nexit 0\n"), 0o700)
	return run, record
}

// v2ReadLines returns the lines of the file at path.
func v2ReadLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

// v2ReadPID returns the pid that the child wrote to the member file at path.
func v2ReadPID(t *testing.T, path string) int {
	t.Helper()
	lines := v2ReadLines(t, path)
	var pid int
	if _, err := fmt.Sscan(lines[0], &pid); err != nil {
		t.Fatalf("pid in %s: %v", path, err)
	}
	return pid
}

// v2Argv returns the argv of the process with pid, as /proc reports it.
func v2Argv(t *testing.T, pid int) []string {
	t.Helper()
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		t.Fatalf("read the argv of %d: %v", pid, err)
	}
	return strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00")
}

// v2FlagValue returns the argument that follows flag in argv, and whether argv holds flag with one.
func v2FlagValue(argv []string, flag string) (string, bool) {
	i := slices.Index(argv, flag)
	if i < 0 || i+1 >= len(argv) {
		return "", false
	}
	return argv[i+1], true
}

// v2WaitExit blocks until process pid has exited, or has been reaped. The kernel reports an exit through the process's
// pidfd, so the wait does not poll.
func v2WaitExit(t *testing.T, pid int) {
	t.Helper()
	fd, err := unix.PidfdOpen(pid, 0)
	if errors.Is(err, unix.ESRCH) {
		return
	}
	if err != nil {
		t.Fatalf("pidfd_open %d: %v", pid, err)
	}
	fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
	for {
		_, err = unix.Poll(fds, -1)
		if !errors.Is(err, unix.EINTR) {
			break
		}
	}
	if err = errors.Join(err, unix.Close(fd)); err != nil {
		t.Fatalf("wait for process %d to exit: %v", pid, err)
	}
}

// TestNodeV2OpsGitAnswersExitCodeStdoutAndStderr runs git through the agent. A run that succeeds answers with its stdout
// untrimmed and exit code 0. A run that fails answers with its exit code, stdout and stderr, as git wrote them.
func TestNodeV2OpsGitAnswersExitCodeStdoutAndStderr(t *testing.T) {
	addr, dirs := startV2Agent(t)
	repo := filepath.Join(dirs.root, "repo")
	if err := os.Mkdir(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	v2InitRepo(t, repo)
	t.Run("success", func(t *testing.T) {
		resp := v2Git(t, addr, GitSpec{Dir: repo, Args: []string{"hash-object", "--stdin"}, Stdin: []byte("hello\n")})
		if resp.Code != "" || resp.Git == nil || !resp.Git.Started {
			t.Fatalf("git answered %+v, want a started run", resp)
		}
		if resp.Git.ExitCode != 0 || string(resp.Git.Stdout) != v2HelloBlob {
			t.Fatalf("git exit %d stdout %q, want exit 0 and stdout %q",
				resp.Git.ExitCode, resp.Git.Stdout, v2HelloBlob)
		}
	})
	t.Run("failure with stdout and stderr", func(t *testing.T) {
		before := filepath.Join(dirs.root, "before")
		after := filepath.Join(dirs.root, "after")
		for path, text := range map[string]string{before: "a\n", after: "b\n"} {
			if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		// GIT_TRACE makes git write its trace to stderr, so this failure has stderr as well as stdout.
		resp := v2Git(t, addr, GitSpec{
			Dir:  repo,
			Args: []string{"diff", "--no-index", "--exit-code", before, after},
			Env:  []string{"GIT_TRACE=1"},
		})
		got := resp.Git
		if resp.Code != "" || got == nil || !got.Started || got.ExitCode != 1 {
			t.Fatalf("git answered %+v, want a started run that exited 1", resp)
		}
		if !strings.Contains(string(got.Stdout), "\n-a\n+b\n") {
			t.Fatalf("git stdout %q, want the diff of the two files", got.Stdout)
		}
		if !strings.Contains(string(got.Stderr), "trace:") {
			t.Fatalf("git stderr %q, want git's trace", got.Stderr)
		}
	})
	t.Run("exit code 128", func(t *testing.T) {
		resp := v2Git(t, addr, GitSpec{Dir: repo, Args: []string{"rev-parse", "--verify", "no-such-ref"}})
		if resp.Git == nil || resp.Git.ExitCode != 128 || len(resp.Git.Stdout) != 0 {
			t.Fatalf("git answered %+v, want exit 128 and no stdout", resp)
		}
		if !strings.Contains(string(resp.Git.Stderr), "fatal:") {
			t.Fatalf("git stderr %q, want git's fatal message", resp.Git.Stderr)
		}
	})
}

// TestNodeV2OpsFileReadsAnswerEachMethod runs the file ops that read a file or describe it, and checks what each answers.
// A missing path answers its fs error as an errno, and so does a rooted read of a symlink, which is not regular.
func TestNodeV2OpsFileReadsAnswerEachMethod(t *testing.T) {
	addr, dirs := startV2Agent(t)
	file := filepath.Join(dirs.root, "f")
	link := filepath.Join(dirs.root, "link")
	if err := os.WriteFile(file, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("f", link); err != nil {
		t.Fatal(err)
	}
	t.Run("read", func(t *testing.T) {
		resp := v2File(t, addr, FileSpec{Op: FileRead, Path: file})
		if resp.Code != "" || resp.File == nil || string(resp.File.Data) != "hello" {
			t.Fatalf("read answered %+v, want the file's data", resp)
		}
	})
	t.Run("read rooted", func(t *testing.T) {
		resp := v2File(t, addr, FileSpec{Op: FileReadRooted, Root: dirs.root, Name: "f"})
		if resp.File == nil || string(resp.File.Data) != "hello" {
			t.Fatalf("read rooted answered %+v, want the file's data", resp)
		}
		resp = v2File(t, addr, FileSpec{Op: FileReadRooted, Root: dirs.root, Name: "link"})
		if resp.Code != "" || resp.File == nil || resp.File.Errno != ErrnoNotRegular {
			t.Fatalf("read rooted of a symlink answered %+v, want errno %q", resp, ErrnoNotRegular)
		}
	})
	t.Run("stat", func(t *testing.T) {
		resp := v2File(t, addr, FileSpec{Op: FileStat, Path: file})
		if resp.File == nil || resp.File.Info == nil || resp.File.Info.Name != "f" || resp.File.Info.Size != 5 {
			t.Fatalf("stat answered %+v, want the file's info", resp)
		}
		resp = v2File(t, addr, FileSpec{Op: FileStat, Path: filepath.Join(dirs.root, "missing")})
		if resp.Code != "" || resp.File == nil || resp.File.Errno != ErrnoNotExist {
			t.Fatalf("stat of a missing path answered %+v, want errno %q", resp, ErrnoNotExist)
		}
	})
	t.Run("lstat", func(t *testing.T) {
		resp := v2File(t, addr, FileSpec{Op: FileLstat, Path: link})
		if resp.File == nil || resp.File.Info == nil || resp.File.Info.Mode&fs.ModeSymlink == 0 {
			t.Fatalf("lstat answered %+v, want the link itself", resp)
		}
	})
	t.Run("read dir and eval symlinks", func(t *testing.T) {
		resp := v2File(t, addr, FileSpec{Op: FileReadDir, Path: dirs.root})
		var names []string
		if resp.File != nil {
			for _, entry := range resp.File.Entries {
				names = append(names, entry.Name)
			}
		}
		if !slices.Equal(names, []string{"f", "link"}) {
			t.Fatalf("read dir listed %v, want [f link]", names)
		}
		resolved := v2File(t, addr, FileSpec{Op: FileEvalSymlinks, Path: link})
		want, err := filepath.EvalSymlinks(file)
		if err != nil {
			t.Fatal(err)
		}
		if resolved.File == nil || resolved.File.Path != want {
			t.Fatalf("eval symlinks answered %+v, want %s", resolved, want)
		}
	})
}

// TestNodeV2OpsFileWritesApplyEachMethod runs the file ops that write or remove a path, and checks what each leaves on
// disk. A write into a missing directory answers ErrnoNotExist, and a symlink over an existing path answers ErrnoExist.
func TestNodeV2OpsFileWritesApplyEachMethod(t *testing.T) {
	addr, dirs := startV2Agent(t)
	w := filepath.Join(dirs.root, "w")
	if resp := v2File(t, addr, FileSpec{Op: FileWrite, Path: w, Data: []byte("data"), Perm: 0o600}); resp.Code != "" {
		t.Fatalf("write: %+v", resp)
	}
	if got, err := os.ReadFile(w); err != nil || string(got) != "data" {
		t.Fatalf("written file = %q, %v; want data", got, err)
	}
	resp := v2File(t, addr, FileSpec{Op: FileWrite, Path: filepath.Join(dirs.root, "gone", "x"), Data: []byte("x")})
	if resp.Code != "" || resp.File == nil || resp.File.Errno != ErrnoNotExist {
		t.Fatalf("write into a missing directory answered %+v, want errno %q", resp, ErrnoNotExist)
	}
	made := v2File(t, addr, FileSpec{Op: FileSymlink, Target: "w", Path: filepath.Join(dirs.root, "made")})
	if made.Code != "" || made.File == nil {
		t.Fatalf("symlink: %+v", made)
	}
	if target, err := os.Readlink(filepath.Join(dirs.root, "made")); err != nil || target != "w" {
		t.Fatalf("link points to %q, %v; want w", target, err)
	}
	again := v2File(t, addr, FileSpec{Op: FileSymlink, Target: "w", Path: filepath.Join(dirs.root, "made")})
	if again.File == nil || again.File.Errno != ErrnoExist {
		t.Fatalf("symlink over an existing link answered %+v, want errno %q", again, ErrnoExist)
	}
	v2FileMakesAndRemoves(t, addr, dirs, w)
}

// v2FileMakesAndRemoves runs the mkdir-temp, mkdir-all, remove and remove-all ops under dirs.root, and checks each. w is
// a file that the test made, and that remove takes.
func v2FileMakesAndRemoves(t *testing.T, addr string, dirs v2Dirs, w string) {
	t.Helper()
	temp := v2File(t, addr, FileSpec{Op: FileMkdirTemp, Path: dirs.root, Pattern: "t-*"})
	if temp.Code != "" || temp.File == nil || filepath.Dir(temp.File.Path) != dirs.root {
		t.Fatalf("mkdir-temp answered %+v, want a directory in %s", temp, dirs.root)
	}
	if info, err := os.Stat(temp.File.Path); err != nil || !info.IsDir() {
		t.Fatalf("mkdir-temp made no directory at %s: %v", temp.File.Path, err)
	}
	nested := filepath.Join(dirs.root, "a", "b")
	if resp := v2File(t, addr, FileSpec{Op: FileMkdirAll, Path: nested, Perm: 0o700}); resp.Code != "" {
		t.Fatalf("mkdir-all: %+v", resp)
	}
	if info, err := os.Stat(nested); err != nil || !info.IsDir() {
		t.Fatalf("mkdir-all made no directory at %s: %v", nested, err)
	}
	if resp := v2File(t, addr, FileSpec{Op: FileRemove, Path: w}); resp.Code != "" {
		t.Fatalf("remove: %+v", resp)
	}
	v2Absent(t, w)
	again := v2File(t, addr, FileSpec{Op: FileRemove, Path: w})
	if again.File == nil || again.File.Errno != ErrnoNotExist {
		t.Fatalf("remove of a missing file answered %+v, want errno %q", again, ErrnoNotExist)
	}
	if resp := v2File(t, addr, FileSpec{Op: FileRemoveAll, Path: filepath.Join(dirs.root, "a")}); resp.Code != "" {
		t.Fatalf("remove-all: %+v", resp)
	}
	v2Absent(t, filepath.Join(dirs.root, "a"))
	if resp := v2File(t, addr, FileSpec{Op: FileRemoveAll, Path: filepath.Join(dirs.root, "a")}); resp.Code != "" {
		t.Fatalf("remove-all of a missing path answered %+v, want no code", resp)
	}
}

// TestNodeV2OpsFileOpsAllowedInEachDir writes a file with the file op into each directory the agent allows, which shows
// that none of them is refused.
func TestNodeV2OpsFileOpsAllowedInEachDir(t *testing.T) {
	addr, dirs := startV2Agent(t)
	for _, named := range dirs.named() {
		t.Run(named.name, func(t *testing.T) {
			path := filepath.Join(named.dir, "allowed")
			spec := FileSpec{Op: FileWrite, Path: path, Data: []byte("ok"), Perm: 0o600}
			if resp := v2File(t, addr, spec); resp.Code != "" {
				t.Fatalf("write in %s answered %+v, want no code", named.dir, resp)
			}
			if got, err := os.ReadFile(path); err != nil || string(got) != "ok" {
				t.Fatalf("file in %s = %q, %v; want ok", named.dir, got, err)
			}
		})
	}
}

// TestNodeV2OpsRefusesPathsOutsideEachAllowedDir names a path beside each directory the agent allows, with a file op, a
// git op and a gate launch. Each is refused with CodeRefused, and nothing it names is made.
func TestNodeV2OpsRefusesPathsOutsideEachAllowedDir(t *testing.T) {
	addr, dirs := startV2Agent(t)
	for _, named := range dirs.named() {
		t.Run(named.name, func(t *testing.T) {
			outside := v2Sibling(t, named.dir)
			t.Run("file op", func(t *testing.T) {
				resp := v2File(t, addr, FileSpec{Op: FileWrite, Path: filepath.Join(outside, "f"), Data: []byte("x")})
				v2Refused(t, resp)
				v2Absent(t, filepath.Join(outside, "f"))
			})
			t.Run("git op", func(t *testing.T) {
				v2Refused(t, v2Git(t, addr, GitSpec{Dir: outside, Args: []string{"init", "-q"}}))
				v2Absent(t, filepath.Join(outside, ".git"))
			})
			t.Run("gate launch", func(t *testing.T) {
				spec := v2GateSpec(outside, dirs.state, newV2Gate(t), "refused", "exit 0")
				v2Refused(t, v2Call(t, addr, Request{Op: OpGateLaunch, Gate: &spec}))
				v2Absent(t, v2HolderDir(dirs.state, "refused"))
			})
		})
	}
}

// TestNodeV2OpsRefusesSymlinksOutOfRoots shows that a symlink which leads out of the agent's root is refused, as is a new
// symlink whose target lies outside, whether the target is absolute or climbs out with "..". A symlink that stays inside
// the root is allowed.
func TestNodeV2OpsRefusesSymlinksOutOfRoots(t *testing.T) {
	addr, dirs := startV2Agent(t)
	outside := v2Sibling(t, dirs.root)
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("s"), 0o600); err != nil {
		t.Fatal(err)
	}
	escape := filepath.Join(dirs.root, "escape")
	if err := os.Symlink(outside, escape); err != nil {
		t.Fatal(err)
	}
	t.Run("read through a link out", func(t *testing.T) {
		v2Refused(t, v2File(t, addr, FileSpec{Op: FileRead, Path: filepath.Join(escape, "secret")}))
	})
	t.Run("absolute target out", func(t *testing.T) {
		link := filepath.Join(dirs.root, "abs")
		v2Refused(t, v2File(t, addr, FileSpec{Op: FileSymlink, Target: filepath.Join(outside, "secret"), Path: link}))
		v2Absent(t, link)
	})
	t.Run("relative target climbing out", func(t *testing.T) {
		link := filepath.Join(dirs.root, "climb")
		target := "../" + filepath.Base(outside) + "/secret"
		v2Refused(t, v2File(t, addr, FileSpec{Op: FileSymlink, Target: target, Path: link}))
		v2Absent(t, link)
	})
	t.Run("target inside the root", func(t *testing.T) {
		resp := v2File(t, addr, FileSpec{Op: FileSymlink, Target: "x", Path: filepath.Join(dirs.root, "inner")})
		if resp.Code != "" {
			t.Fatalf("symlink inside the root answered %+v, want no code", resp)
		}
	})
}

// TestNodeV2OpsRefusesGateStateDirOutside launches a gate whose state directory lies outside the agent's allowed
// directories, and it is refused, so the holder directory the launch would have made does not exist.
func TestNodeV2OpsRefusesGateStateDirOutside(t *testing.T) {
	addr, dirs := startV2Agent(t)
	outside := v2Sibling(t, dirs.state)
	spec := v2GateSpec(dirs.root, outside, newV2Gate(t), "lost", "exit 0")
	v2Refused(t, v2Call(t, addr, Request{Op: OpGateLaunch, Gate: &spec}))
	v2Absent(t, v2HolderDir(outside, "lost"))
}

// TestNodeV2OpsGateLaunchCarriesLaunchAndAnswersExitRecord launches a gate with argv, env, a directory, a tail and a scope.
// It checks each in what the child and the holder show, refuses a second launch of the live session, and then reads the
// exit record that the child's end left.
func TestNodeV2OpsGateLaunchCarriesLaunchAndAnswersExitRecord(t *testing.T) {
	addr, dirs := startV2Agent(t)
	g := newV2Gate(t)
	scope, scopeRecord := v2FakeScope(t)
	work := filepath.Join(dirs.root, "work")
	if err := os.Mkdir(work, 0o700); err != nil {
		t.Fatal(err)
	}
	record := filepath.Join(dirs.root, "record")
	const session = "carried"
	spec := v2GateSpec(work, dirs.state, g, session, v2CarryScript)
	spec.Args = []string{"-c", v2CarryScript, "argv0", "one two", "three"}
	spec.Env = append(spec.Env, "RECORD="+record, "MARK=carried")
	spec.Tail = 8
	spec.Scope = scope
	v2Launch(t, addr, spec)
	dir := v2HolderDir(dirs.state, session)
	g.awaitStarted(t)
	v2CheckCarried(t, record, work, session, scopeRecord, dir)
	v2RefuseLiveLaunch(t, addr, spec, dir)
	g.releaseChild(t)
	v2WaitExit(t, v2HolderPID(t, dir))
	v2CheckExited(t, addr, dir)
}

// v2Pids returns the pids record of the holder directory dir.
func v2Pids(t *testing.T, dir string) holder.Pids {
	t.Helper()
	pids, err := holder.ReadPids(dir)
	if err != nil {
		t.Fatalf("pids record of %s: %v", dir, err)
	}
	return pids
}

// v2HolderPID returns the pid of the holder of the directory dir.
func v2HolderPID(t *testing.T, dir string) int {
	t.Helper()
	return v2Pids(t, dir).Holder
}

// v2CheckCarried checks what a gate launch carried, as the child and the holder show it: the child's argv, its env and its
// directory, the holder's gate limit and tail, and the unit of the scope the holder ran in.
func v2CheckCarried(t *testing.T, record, work, session, scopeRecord, dir string) {
	t.Helper()
	workDir, err := filepath.EvalSymlinks(work)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"argv0", "one two", "three", "carried", workDir}
	if got := v2ReadLines(t, record); !slices.Equal(got, want) {
		t.Fatalf("child recorded %q, want its argv, env and directory %q", got, want)
	}
	argv := v2Argv(t, v2HolderPID(t, dir))
	if limit, ok := v2FlagValue(argv, "--gate-limit"); !ok || limit != "86400" {
		t.Fatalf("holder argv %q has gate limit %q, want 86400", argv, limit)
	}
	if tail, ok := v2FlagValue(argv, "--gate-tail"); !ok || tail != "8" {
		t.Fatalf("holder argv %q has gate tail %q, want 8", argv, tail)
	}
	unit, ok := v2FlagValue(v2ReadLines(t, scopeRecord), "--unit")
	if !ok || !strings.HasPrefix(unit, "orrery-hold-"+session+"-") {
		t.Fatalf("scope was given unit %q, want a unit of session %s", unit, session)
	}
}

// v2RefuseLiveLaunch launches spec again while the holder of its session lives, and checks that the launch is refused and
// that the live holder keeps running.
func v2RefuseLiveLaunch(t *testing.T, addr string, spec GateSpec, dir string) {
	t.Helper()
	resp := v2Call(t, addr, Request{Op: OpGateLaunch, Gate: &spec})
	if resp.Code != CodeFailed {
		t.Fatalf("second launch of a live session answered %+v, want code %q", resp, CodeFailed)
	}
	if live, err := holder.Live(dir); err != nil || !live {
		t.Fatalf("holder live = %v, %v after the refused launch; want the holder still live", live, err)
	}
}

// v2CheckExited checks the exit record that the child's end left in dir: the child exited by itself with status 0, and the
// record keeps the last 8 bytes of its stdout, as its gate tail asks.
func v2CheckExited(t *testing.T, addr, dir string) {
	t.Helper()
	resp := v2Call(t, addr, Request{Op: OpExitRecord, HolderDir: dir})
	if resp.Code != "" || resp.Exit == nil {
		t.Fatalf("exit-record answered %+v, want the record", resp)
	}
	if resp.Exit.Reason != holder.ReasonExited || resp.Exit.Code != 0 || resp.Exit.Stdout != "yz\ndone\n" {
		t.Fatalf("exit record = %+v, want exited with status 0 and the 8-byte stdout tail", resp.Exit)
	}
}

// TestNodeV2OpsKillsLeftoversOfGateChild leaves a process running in a gate child's process group. It kills the gate's
// holder first, so the holder does not kill the group when its child ends. The kill-leftovers op then kills the process.
func TestNodeV2OpsKillsLeftoversOfGateChild(t *testing.T) {
	addr, dirs := startV2Agent(t)
	g := newV2Gate(t)
	const session = "leftovers"
	v2Launch(t, addr, v2GateSpec(dirs.root, dirs.state, g, session, v2LeftoverScript))
	dir := v2HolderDir(dirs.state, session)
	g.awaitStarted(t)
	member := v2ReadPID(t, g.member)
	t.Cleanup(func() {
		if err := syscall.Kill(member, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
			t.Errorf("kill member %d at cleanup: %v", member, err)
		}
	})
	pids := v2Pids(t, dir)
	if err := syscall.Kill(pids.Holder, syscall.SIGKILL); err != nil {
		t.Fatalf("kill holder %d: %v", pids.Holder, err)
	}
	v2WaitExit(t, pids.Holder)
	g.releaseChild(t)
	v2WaitExit(t, pids.Child)
	if exectest.KilledOrGone(t, member) {
		t.Fatalf("member %d is gone though its holder died without signaling the group", member)
	}
	if resp := v2Call(t, addr, Request{Op: OpKillLeftovers, HolderDir: dir}); resp.Code != "" {
		t.Fatalf("kill-leftovers: %+v", resp)
	}
	// The kill is queued once kill-leftovers answers, so the member reads as killed at once, without a wait that a missing
	// kill would never end.
	if !exectest.KilledOrGone(t, member) {
		t.Fatalf("kill-leftovers left member %d running", member)
	}
}

// TestNodeV2OpsGateStopLeavesDirAndClearRemovesIt stops a running gate, which leaves its directory with the exit record of
// the stop. Clear then removes the directory, and the record with it.
func TestNodeV2OpsGateStopLeavesDirAndClearRemovesIt(t *testing.T) {
	addr, dirs := startV2Agent(t)
	g := newV2Gate(t)
	const session = "stopped"
	v2Launch(t, addr, v2GateSpec(dirs.root, dirs.state, g, session, v2HeldScript))
	dir := v2HolderDir(dirs.state, session)
	g.awaitStarted(t)
	if resp := v2Call(t, addr, Request{Op: OpGateStop, HolderDir: dir}); resp.Code != "" {
		t.Fatalf("gate-stop: %+v", resp)
	}
	if _, err := os.Lstat(dir); err != nil {
		t.Fatalf("gate-stop removed %s: %v", dir, err)
	}
	stopped := v2Call(t, addr, Request{Op: OpExitRecord, HolderDir: dir})
	if stopped.Exit == nil || stopped.Exit.Reason != holder.ReasonStopped {
		t.Fatalf("exit record after gate-stop = %+v, want a stopped record", stopped)
	}
	if resp := v2Call(t, addr, Request{Op: OpGateClear, HolderDir: dir}); resp.Code != "" {
		t.Fatalf("gate-clear: %+v", resp)
	}
	v2Absent(t, dir)
	if resp := v2Call(t, addr, Request{Op: OpExitRecord, HolderDir: dir}); resp.Exit != nil {
		t.Fatalf("exit record of a cleared directory = %+v, want none", resp.Exit)
	}
}

// TestNodeV2OpsGateAttachSplicesOntoHolder attaches to a running gate through the agent. The splice carries the holder's
// wire, so the holder answers the attach as it answers a session's.
func TestNodeV2OpsGateAttachSplicesOntoHolder(t *testing.T) {
	addr, dirs := startV2Agent(t)
	g := newV2Gate(t)
	const session = "attached"
	v2Launch(t, addr, v2GateSpec(dirs.root, dirs.state, g, session, v2HeldScript))
	g.awaitStarted(t)
	resp, reader, conn := openStream(t, addr, Request{
		Version:   Version,
		Secret:    testSecret,
		Op:        OpGateAttach,
		HolderDir: v2HolderDir(dirs.state, session),
	})
	if resp.Code != "" {
		t.Fatalf("gate-attach: %+v", resp)
	}
	spliceAttach(t, conn, reader, 0)
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestNodeV2OpsHolderAliveFollowsHolder answers whether the holder of a gate lives. It does while the gate runs, and does
// not once the gate is stopped.
func TestNodeV2OpsHolderAliveFollowsHolder(t *testing.T) {
	addr, dirs := startV2Agent(t)
	g := newV2Gate(t)
	const session = "alive"
	v2Launch(t, addr, v2GateSpec(dirs.root, dirs.state, g, session, v2HeldScript))
	dir := v2HolderDir(dirs.state, session)
	g.awaitStarted(t)
	if resp := v2Call(t, addr, Request{Op: OpHolderAlive, HolderDir: dir}); resp.Code != "" || !resp.Alive {
		t.Fatalf("holder-alive of a running gate = %+v, want alive", resp)
	}
	if resp := v2Call(t, addr, Request{Op: OpGateStop, HolderDir: dir}); resp.Code != "" {
		t.Fatalf("gate-stop: %+v", resp)
	}
	if resp := v2Call(t, addr, Request{Op: OpHolderAlive, HolderDir: dir}); resp.Code != "" || resp.Alive {
		t.Fatalf("holder-alive after gate-stop = %+v, want not alive", resp)
	}
}

// TestNodeV2OpsProcessAliveMatchesPidAndStart checks processes against their pid and start time on this machine. The test's
// own process is alive under its own start time and not under another, and a process that has exited is not alive.
func TestNodeV2OpsProcessAliveMatchesPidAndStart(t *testing.T) {
	addr, _ := startV2Agent(t)
	alive := func(p int, s int64) bool {
		return v2Call(t, addr, Request{Op: OpProcessAlive, PID: p, Start: s}).Alive
	}
	self := os.Getpid()
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", self))
	if err != nil {
		t.Fatal(err)
	}
	start, err := procStart(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	if !alive(self, start) {
		t.Fatalf("process %d is not alive under its own start time %d", self, start)
	}
	if alive(self, start+1) {
		t.Fatalf("process %d is alive under start time %d, which is not its own", self, start+1)
	}
	done := exec.CommandContext(t.Context(), "true")
	if err = done.Run(); err != nil {
		t.Fatal(err)
	}
	if alive(done.Process.Pid, 1) {
		t.Fatalf("exited process %d is alive", done.Process.Pid)
	}
}

// TestNodeV2OpsRefusesVersionOtherThanV2 answers a Request that names node.v1 with CodeVersion, and a message that names both
// versions.
func TestNodeV2OpsRefusesVersionOtherThanV2(t *testing.T) {
	addr, _ := startV2Agent(t)
	resp := ask(t, addr, Request{Version: v1Version, Secret: testSecret, Op: OpPing})
	if resp.Code != CodeVersion {
		t.Fatalf("a node.v1 request got %+v, want code %q", resp, CodeVersion)
	}
	for _, name := range []string{Version, v1Version} {
		if !strings.Contains(resp.Message, name) {
			t.Fatalf("message %q does not name %s", resp.Message, name)
		}
	}
}
