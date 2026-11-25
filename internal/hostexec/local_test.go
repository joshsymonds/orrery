package hostexec

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/joshsymonds/orrery/internal/exectest"
	"github.com/joshsymonds/orrery/internal/holder"
)

// The held tests run a gate child, a shell script that does its steps in order and waits on the test between them. The
// child writes to a FIFO once it runs, and reads a line from another FIFO before it goes on, so the test waits on the
// child's own steps with no clock and no poll.

// heldScript is the child the held tests run. It writes ready to $STARTED once it runs, blocks until a line arrives on
// $RELEASE, prints done and exits 0.
const heldScript = `echo ready > "$STARTED"; read -r _ < "$RELEASE"; echo done; exit 0`

// leftoverScript is the child the leftovers test runs. It leaves a sleep running in its process group, records the
// sleep's pid in $MEMBER, writes ready to $STARTED, blocks until a line arrives on $RELEASE, and exits 0. Its output
// goes nowhere, so the child does not die of a broken pipe once its holder is gone.
const leftoverScript = `sleep 600 >/dev/null 2>&1 & echo "$!" > "$MEMBER"; ` +
	`echo ready > "$STARTED"; read -r _ < "$RELEASE"; exit 0`

// heldGateLimit is the Limit that makes a held child a gate. Its LimitSignal FIFO, not this duration, reaches the limit,
// and no test opens that FIFO, so no clock runs.
const heldGateLimit = time.Minute

// fifoMode is the mode the test FIFOs are made with.
const fifoMode = 0o600

// fakeSSH stands in for ssh as git's transport runs it. It opens the FIFO named by $READY for writing as descriptor 3,
// starts a sleep that inherits descriptor 3 with its output to /dev/null, reports ready on descriptor 3, and waits. The
// sleep is a grandchild of git, so only a kill of git's process group ends it, and that leaves the FIFO with no writer.
const fakeSSH = "#!/bin/sh\n" +
	`exec 3>"$READY"` + "\n" +
	`sleep 600 >/dev/null 2>&1 &` + "\n" +
	`echo ready >&3` + "\n" +
	`wait` + "\n"

// helloBlob is the object name git gives the blob of "hello\n".
const helloBlob = "ce013625030ba8dba906f756967f9e9ca394464a\n"

func TestHostexecLocalGit(t *testing.T) {
	t.Parallel()
	local := NewLocal()
	t.Run("runs in dir with env and stdin", func(t *testing.T) {
		t.Parallel()
		repo := gitInit(t)
		ctx := t.Context()
		top, err := local.Git(ctx, GitCall{Dir: repo, Args: []string{"rev-parse", "--show-toplevel"}})
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
		ident, err := local.Git(ctx, GitCall{
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
		sum, err := local.Git(ctx, GitCall{
			Dir:   repo,
			Args:  []string{"hash-object", "--stdin"},
			Stdin: []byte("hello\n"),
		})
		if err != nil {
			t.Fatalf("git hash-object with stdin: %v", err)
		}
		if string(sum) != helloBlob {
			t.Fatalf("hash-object --stdin = %q, want %q", sum, helloBlob)
		}
	})
	t.Run("failure carries exit code stdout and stderr", func(t *testing.T) {
		t.Parallel()
		repo := gitInit(t)
		dir := t.TempDir()
		before := filepath.Join(dir, "before")
		after := filepath.Join(dir, "after")
		writeFile(t, before, "a\n")
		writeFile(t, after, "b\n")
		call := GitCall{
			Dir:  repo,
			Args: []string{"diff", "--no-index", "--exit-code", before, after},
			// GIT_TRACE makes git write its trace to stderr, so this failure has stderr as well as stdout.
			Env: []string{"GIT_TRACE=1"},
		}
		out, err := local.Git(t.Context(), call)
		var gitErr *GitError
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
	})
	t.Run("remote call is killed with its group", func(t *testing.T) {
		t.Parallel()
		repo := gitInit(t)
		dir := t.TempDir()
		ready := filepath.Join(dir, "ready")
		makeFIFO(t, ready)
		ssh := filepath.Join(dir, "fake-ssh")
		exectest.WriteFile(t, ssh, []byte(fakeSSH), 0o700)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		type outcome struct {
			out []byte
			err error
		}
		done := make(chan outcome, 1)
		go func() {
			out, err := local.Git(ctx, GitCall{
				Dir:    repo,
				Args:   []string{"ls-remote", "ssh://fake.invalid/repo"},
				Env:    []string{"GIT_SSH_COMMAND=" + ssh, "READY=" + ready},
				Remote: true,
			})
			done <- outcome{out, err}
		}()
		// The read returns once fake-ssh has opened the FIFO and reported ready, so the transport is running.
		signal, err := os.Open(ready)
		if err != nil {
			t.Fatalf("open %s: %v", ready, err)
		}
		reader := bufio.NewReader(signal)
		line, err := reader.ReadString('\n')
		if err != nil || line != "ready\n" {
			t.Fatalf("fake ssh reported %q, %v, want ready", line, err)
		}
		cancel()
		got := <-done
		// The writers left on the FIFO are fake-ssh's descriptor 3 and the sleep that inherited it. Once the kill of git's
		// group has ended both, the read ends with no data.
		rest, err := io.ReadAll(reader)
		if err = errors.Join(err, signal.Close()); err != nil {
			t.Fatalf("drain %s: %v", ready, err)
		}
		if len(rest) != 0 {
			t.Fatalf("fake ssh wrote %q after ready", rest)
		}
		var gitErr *GitError
		if !errors.As(got.err, &gitErr) {
			t.Fatalf("Git error = %v, want a *GitError for the killed run", got.err)
		}
		if gitErr.ExitCode != -1 {
			t.Fatalf("ExitCode = %d, want -1 for a run a signal ended", gitErr.ExitCode)
		}
	})
}

func TestHostexecLocalFiles(t *testing.T) {
	t.Parallel()
	local := NewLocal()
	t.Run("read", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "file")
		writeFile(t, path, "contents")
		got, err := local.ReadFile(t.Context(), path)
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		if string(got) != "contents" {
			t.Fatalf("ReadFile = %q, want %q", got, "contents")
		}
		_, err = local.ReadFile(t.Context(), path+".missing")
		if !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("ReadFile of a missing file = %v, want ErrNotExist", err)
		}
	})
	t.Run("rooted read refuses a symlink", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		writeFile(t, filepath.Join(root, "file"), "contents")
		if err := os.Symlink("file", filepath.Join(root, "link")); err != nil {
			t.Fatalf("symlink: %v", err)
		}
		if err := os.Mkdir(filepath.Join(root, "dir"), 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		got, err := local.ReadRooted(t.Context(), root, "file")
		if err != nil {
			t.Fatalf("ReadRooted of a regular file: %v", err)
		}
		if string(got) != "contents" {
			t.Fatalf("ReadRooted = %q, want %q", got, "contents")
		}
		for _, name := range []string{"link", "dir"} {
			_, err = local.ReadRooted(t.Context(), root, name)
			if !errors.Is(err, ErrNotRegular) {
				t.Fatalf("ReadRooted(%q) = %v, want ErrNotRegular", name, err)
			}
		}
	})
	t.Run("stat", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "file"), "contents")
		if err := os.Symlink("file", filepath.Join(dir, "link")); err != nil {
			t.Fatalf("symlink: %v", err)
		}
		info, err := local.Stat(t.Context(), filepath.Join(dir, "link"))
		if err != nil {
			t.Fatalf("Stat of a link: %v", err)
		}
		if !info.Mode().IsRegular() || info.Size() != int64(len("contents")) {
			t.Fatalf("Stat of a link = mode %v size %d, want the regular target", info.Mode(), info.Size())
		}
		_, err = local.Stat(t.Context(), filepath.Join(dir, "missing"))
		if !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("Stat of a missing path = %v, want ErrNotExist", err)
		}
	})
	t.Run("lstat", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "file"), "contents")
		if err := os.Symlink("file", filepath.Join(dir, "link")); err != nil {
			t.Fatalf("symlink: %v", err)
		}
		info, err := local.Lstat(t.Context(), filepath.Join(dir, "link"))
		if err != nil {
			t.Fatalf("Lstat of a link: %v", err)
		}
		if info.Mode()&fs.ModeSymlink == 0 {
			t.Fatalf("Lstat of a link = mode %v, want a symlink", info.Mode())
		}
	})
	t.Run("list", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "b"), "")
		writeFile(t, filepath.Join(dir, "a"), "")
		if err := os.Mkdir(filepath.Join(dir, "c"), 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		entries, err := local.ReadDir(t.Context(), dir)
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
	})
	t.Run("write", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "file")
		if err := local.WriteFile(t.Context(), path, []byte("a longer first write"), 0o600); err != nil {
			t.Fatalf("WriteFile to a missing file: %v", err)
		}
		if err := local.WriteFile(t.Context(), path, []byte("short"), 0o600); err != nil {
			t.Fatalf("WriteFile over a file: %v", err)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read back: %v", err)
		}
		if string(got) != "short" {
			t.Fatalf("file = %q, want the second write alone", got)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat: %v", err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("mode = %v, want 0600", info.Mode().Perm())
		}
	})
	t.Run("symlink", func(t *testing.T) {
		t.Parallel()
		link := filepath.Join(t.TempDir(), "link")
		if err := local.Symlink(t.Context(), "target", link); err != nil {
			t.Fatalf("Symlink: %v", err)
		}
		info, err := os.Lstat(link)
		if err != nil {
			t.Fatalf("Lstat of the new link: %v", err)
		}
		if info.Mode()&fs.ModeSymlink == 0 {
			t.Fatalf("new link mode = %v, want a symlink", info.Mode())
		}
		target, err := os.Readlink(link)
		if err != nil {
			t.Fatalf("Readlink: %v", err)
		}
		if target != "target" {
			t.Fatalf("link points to %q, want %q", target, "target")
		}
	})
	t.Run("temp dir", func(t *testing.T) {
		t.Parallel()
		base := t.TempDir()
		made, err := local.MkdirTemp(t.Context(), base, "orrery-*")
		if err != nil {
			t.Fatalf("MkdirTemp: %v", err)
		}
		if filepath.Dir(made) != base || !strings.HasPrefix(filepath.Base(made), "orrery-") {
			t.Fatalf("MkdirTemp made %s, want a new directory in %s named orrery-*", made, base)
		}
		info, err := os.Stat(made)
		if err != nil {
			t.Fatalf("stat: %v", err)
		}
		if !info.IsDir() {
			t.Fatalf("MkdirTemp made %s, which is not a directory", made)
		}
	})
	t.Run("mkdir", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "a", "b", "c")
		if err := local.MkdirAll(t.Context(), path, 0o700); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat: %v", err)
		}
		if !info.IsDir() {
			t.Fatalf("MkdirAll made %s, which is not a directory", path)
		}
		if err = local.MkdirAll(t.Context(), path, 0o700); err != nil {
			t.Fatalf("MkdirAll of an existing directory: %v", err)
		}
	})
	t.Run("remove", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		dir := t.TempDir()
		file := filepath.Join(dir, "file")
		writeFile(t, file, "")
		if err := local.Remove(ctx, file); err != nil {
			t.Fatalf("Remove: %v", err)
		}
		if _, err := os.Lstat(file); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("Lstat after Remove = %v, want ErrNotExist", err)
		}
		if err := local.Remove(ctx, file); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("Remove of a missing file = %v, want ErrNotExist", err)
		}
		tree := filepath.Join(dir, "tree")
		if err := os.MkdirAll(filepath.Join(tree, "sub"), 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		writeFile(t, filepath.Join(tree, "sub", "file"), "")
		if err := local.RemoveAll(ctx, tree); err != nil {
			t.Fatalf("RemoveAll: %v", err)
		}
		if _, err := os.Lstat(tree); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("Lstat after RemoveAll = %v, want ErrNotExist", err)
		}
		if err := local.RemoveAll(ctx, tree); err != nil {
			t.Fatalf("RemoveAll of a missing path: %v", err)
		}
	})
	t.Run("eval symlinks", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		target := filepath.Join(dir, "target")
		writeFile(t, target, "")
		link := filepath.Join(dir, "link")
		if err := os.Symlink(target, link); err != nil {
			t.Fatalf("symlink: %v", err)
		}
		want, err := filepath.EvalSymlinks(target)
		if err != nil {
			t.Fatalf("EvalSymlinks of the target: %v", err)
		}
		got, err := local.EvalSymlinks(t.Context(), link)
		if err != nil {
			t.Fatalf("EvalSymlinks: %v", err)
		}
		if got != want {
			t.Fatalf("EvalSymlinks = %q, want %q", got, want)
		}
	})
}

func TestHostexecLocalHeld(t *testing.T) {
	t.Parallel()
	local := NewLocal()
	t.Run("launch", func(t *testing.T) {
		t.Parallel()
		stateDir := shortStateDir(t)
		child := newHeldChild(t, heldScript)
		held, dir := child.launch(t, local, stateDir, "launch")
		child.awaitStarted(t)
		if _, ended := held.Exit(); ended {
			t.Fatal("Exit reports the child ended while it still runs")
		}
		if _, err := os.Lstat(dir); err != nil {
			t.Fatalf("session dir %s: %v", dir, err)
		}
	})
	t.Run("reattach", func(t *testing.T) {
		t.Parallel()
		stateDir := shortStateDir(t)
		child := newHeldChild(t, heldScript)
		held, dir := child.launch(t, local, stateDir, "reattach")
		child.awaitStarted(t)
		if err := held.Detach(); err != nil {
			t.Fatalf("Detach: %v", err)
		}
		reattached, err := local.Reattach(t.Context(), dir, "")
		if err != nil {
			t.Fatalf("Reattach: %v", err)
		}
		child.release(t)
		if err = reattached.Wait(); err != nil {
			t.Fatalf("Wait of the reattached child: %v", err)
		}
		exit, ok := local.ExitRecord(dir)
		if !ok || exit.Reason != ReasonExited || exit.Code != 0 || exit.Stdout != "done\n" {
			t.Fatalf("exit record = %+v, %v: want the reattached child's exit 0 with its output", exit, ok)
		}
	})
	t.Run("stop", func(t *testing.T) {
		t.Parallel()
		stateDir := shortStateDir(t)
		child := newHeldChild(t, heldScript)
		_, dir := child.launch(t, local, stateDir, "stop")
		child.awaitStarted(t)
		if err := local.Stop(t.Context(), dir); err != nil {
			t.Fatalf("Stop: %v", err)
		}
		if _, err := os.Lstat(dir); err != nil {
			t.Fatalf("Stop removed %s: %v", dir, err)
		}
		if _, err := local.Reattach(t.Context(), dir, ""); !errors.Is(err, ErrGone) {
			t.Fatalf("Reattach after Stop = %v, want ErrGone", err)
		}
		exit, ok := local.ExitRecord(dir)
		if !ok || exit.Reason != ReasonStopped {
			t.Fatalf("exit record = %+v, %v: want a stopped record", exit, ok)
		}
	})
	t.Run("clear", func(t *testing.T) {
		t.Parallel()
		stateDir := shortStateDir(t)
		child := newHeldChild(t, heldScript)
		_, dir := child.launch(t, local, stateDir, "clear")
		child.awaitStarted(t)
		if err := local.Clear(t.Context(), dir); err != nil {
			t.Fatalf("Clear: %v", err)
		}
		if _, err := os.Lstat(dir); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("Lstat after Clear = %v, want ErrNotExist", err)
		}
		if err := local.Clear(t.Context(), dir); err != nil {
			t.Fatalf("Clear of a missing dir: %v", err)
		}
	})
	t.Run("exit record", func(t *testing.T) {
		t.Parallel()
		stateDir := shortStateDir(t)
		child := newHeldChild(t, heldScript)
		held, dir := child.launch(t, local, stateDir, "exit-record")
		child.awaitStarted(t)
		if _, ok := local.ExitRecord(dir); ok {
			t.Fatal("ExitRecord reports a record while the child runs")
		}
		child.release(t)
		if err := held.Wait(); err != nil {
			t.Fatalf("Wait: %v", err)
		}
		exit, ok := local.ExitRecord(dir)
		if !ok || exit.Reason != ReasonExited || exit.Code != 0 || exit.Stdout != "done\n" {
			t.Fatalf("exit record = %+v, %v: want the child's exit 0 with its output", exit, ok)
		}
		if _, ok = local.ExitRecord(t.TempDir()); ok {
			t.Fatal("ExitRecord reports a record in a dir with none")
		}
	})
	t.Run("kills leftovers", func(t *testing.T) {
		t.Parallel()
		stateDir := shortStateDir(t)
		child := newHeldChild(t, leftoverScript)
		_, dir := child.launch(t, local, stateDir, "leftovers")
		child.awaitStarted(t)
		member := readPID(t, child.memberFile)
		t.Cleanup(func() {
			if err := syscall.Kill(member, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
				t.Errorf("kill member %d at cleanup: %v", member, err)
			}
		})
		pids, err := holder.ReadPids(dir)
		if err != nil {
			t.Fatalf("pids record of the session: %v", err)
		}
		// The holder dies without signaling its child's group, so the member is left in the group for KillLeftovers. A
		// live holder would kill the group itself once its child was reaped.
		if err = syscall.Kill(pids.Holder, syscall.SIGKILL); err != nil {
			t.Fatalf("kill holder %d: %v", pids.Holder, err)
		}
		waitPidExit(t, pids.Holder)
		child.release(t)
		waitPidExit(t, pids.Child)
		if exectest.KilledOrGone(t, member) {
			t.Fatalf("member %d is gone though its holder died without signaling the group", member)
		}
		if err = local.KillLeftovers(dir); err != nil {
			t.Fatalf("KillLeftovers: %v", err)
		}
		if !exectest.KilledOrGone(t, member) {
			t.Fatalf("KillLeftovers left member %d running", member)
		}
	})
}

// waitPidExit blocks until process pid has exited, or has already been reaped. The kernel reports an exit through the
// process's pidfd, so the wait does not poll.
func waitPidExit(t *testing.T, pid int) {
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
		if errors.Is(err, unix.EINTR) {
			continue
		}
		break
	}
	if err = errors.Join(err, unix.Close(fd)); err != nil {
		t.Fatalf("wait for process %d to exit: %v", pid, err)
	}
}

// gitInit makes an empty repository in a new directory and returns its path.
func gitInit(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	out, err := exec.CommandContext(t.Context(), "git", "init", "-q", dir).CombinedOutput()
	if err != nil {
		t.Fatalf("git init %s: %v\n%s", dir, err, out)
	}
	return dir
}

// writeFile writes contents to path with mode 0600.
func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// makeFIFO makes a named pipe at path.
func makeFIFO(t *testing.T, path string) {
	t.Helper()
	if err := syscall.Mkfifo(path, fifoMode); err != nil {
		t.Fatalf("mkfifo %s: %v", path, err)
	}
}

// shortRoot returns a fresh state directory short enough that a holder's socket path fits in sun_path, which
// t.TempDir's paths can overflow. The caller removes it.
func shortRoot() (string, error) {
	root, err := os.MkdirTemp("", "hostexec")
	if err != nil {
		return "", fmt.Errorf("make state dir: %w", err)
	}
	return root, nil
}

// shortStateDir returns a shortRoot directory that is removed when the test ends.
func shortStateDir(t *testing.T) string {
	t.Helper()
	root, err := shortRoot()
	if err != nil {
		t.Fatalf("%v", err)
	}
	t.Cleanup(func() {
		if rmErr := os.RemoveAll(root); rmErr != nil {
			t.Errorf("remove %s: %v", root, rmErr)
		}
	})
	return root
}

// heldChild is a gate child a held test runs. The FIFOs and the member file are how the test follows the child's steps.
type heldChild struct {
	script      string
	startedFIFO string
	releaseFIFO string
	memberFile  string
	limitFIFO   string
}

// newHeldChild returns a heldChild that runs script, with its FIFOs made in a new directory.
func newHeldChild(t *testing.T, script string) heldChild {
	t.Helper()
	dir := t.TempDir()
	child := heldChild{
		script:      script,
		startedFIFO: filepath.Join(dir, "started"),
		releaseFIFO: filepath.Join(dir, "release"),
		memberFile:  filepath.Join(dir, "member"),
		limitFIFO:   filepath.Join(dir, "limit"),
	}
	makeFIFO(t, child.startedFIFO)
	makeFIFO(t, child.releaseFIFO)
	makeFIFO(t, child.limitFIFO)
	return child
}

// launch launches the child as a gate under session in stateDir. It returns the child's Held and its session directory.
// The session is cleared when the test ends.
func (c heldChild) launch(t *testing.T, local *Local, stateDir, session string) (Held, string) {
	t.Helper()
	held, err := local.Launch(t.Context(), Launch{
		StateDir: stateDir,
		Session:  session,
		Path:     "/bin/sh",
		Args:     []string{"-c", c.script},
		Env: []string{
			"PATH=" + os.Getenv("PATH"),
			"STARTED=" + c.startedFIFO,
			"RELEASE=" + c.releaseFIFO,
			"MEMBER=" + c.memberFile,
		},
		Limit:       heldGateLimit,
		LimitSignal: c.limitFIFO,
	})
	if err != nil {
		t.Fatalf("Launch %s: %v", session, err)
	}
	dir, err := local.SessionDir(filepath.Join(stateDir, "holders"), session)
	if err != nil {
		t.Fatalf("SessionDir %s: %v", session, err)
	}
	t.Cleanup(func() {
		if clearErr := local.Clear(context.WithoutCancel(t.Context()), dir); clearErr != nil {
			t.Errorf("clear %s: %v", dir, clearErr)
		}
	})
	return held, dir
}

// awaitStarted blocks until the child writes ready to its started FIFO, which it does once it runs.
func (c heldChild) awaitStarted(t *testing.T) {
	t.Helper()
	if got := readFIFO(t, c.startedFIFO); got != "ready\n" {
		t.Fatalf("child wrote %q to its started FIFO, want ready", got)
	}
}

// release lets the child run on. It writes a line to the release FIFO, which the child reads once it blocks on it.
func (c heldChild) release(t *testing.T) {
	t.Helper()
	fifo, err := os.OpenFile(c.releaseFIFO, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open %s: %v", c.releaseFIFO, err)
	}
	_, writeErr := fifo.WriteString("go\n")
	if err = errors.Join(writeErr, fifo.Close()); err != nil {
		t.Fatalf("release the child through %s: %v", c.releaseFIFO, err)
	}
}

// readFIFO returns what the writers of the FIFO at path write before they all close it. The open waits for a writer.
func readFIFO(t *testing.T, path string) string {
	t.Helper()
	fifo, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	data, err := io.ReadAll(fifo)
	if err = errors.Join(err, fifo.Close()); err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// readPID returns the pid the child wrote to the member file.
func readPID(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("pid in %s: %v", path, err)
	}
	return pid
}
