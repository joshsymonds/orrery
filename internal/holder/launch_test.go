package holder

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/joshsymonds/orrery/internal/engine/proc"
	"github.com/joshsymonds/orrery/internal/holdertest"
)

const (
	// launchLine is the line a launched child echoes back.
	launchLine = "hello from the controller\n"
	// launchChildPid is the file a launch test's child writes its pid to, in its working directory.
	launchChildPid = "child.pid"
	// launchReason is the reason the fake systemd-run gives when a test makes it fail.
	launchReason = "systemd-run: no user manager"
)

func TestMain(m *testing.M) {
	os.Exit(holdertest.Main(m))
}

// launchRoot returns a short state directory for one launch test. The directory is removed when the test ends.
func launchRoot(t *testing.T) string {
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

// launchChild returns the child a launch test runs: an sh that writes its pid to child.pid in its working directory,
// then runs script.
func launchChild(t *testing.T, script string) proc.Config {
	t.Helper()
	args := []string{"-c", "echo $$ > " + launchChildPid + "; " + script}
	return proc.Config{Path: shPath(t), Args: args, Dir: t.TempDir()}
}

// mustLaunch runs Launch for cfg and fails the test on error. The launched holder is stopped when the test ends.
func mustLaunch(t *testing.T, cfg LaunchConfig) *Process {
	t.Helper()
	p, err := Launch(t.Context(), cfg)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	t.Cleanup(func() {
		if stopErr := p.Stop(); stopErr != nil {
			t.Errorf("Stop at cleanup: %v", stopErr)
		}
	})
	return p
}

// orreryPath returns the absolute path of the orrery that the test build put first on PATH.
func orreryPath(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("orrery")
	if err != nil {
		t.Fatalf("find orrery: %v", err)
	}
	return path
}

// fakeSystemdRunPath returns the absolute path of the fake systemd-run in testdata.
func fakeSystemdRunPath(t *testing.T) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("testdata", "fakesystemdrun.sh"))
	if err != nil {
		t.Fatalf("resolve fakesystemdrun.sh: %v", err)
	}
	return path
}

// scopeArgs returns the arguments that put a holder of testSession in its own user scope, before the holder's argv.
func scopeArgs() []string {
	return []string{
		"--user", "--scope", "--collect", "-p", "OOMPolicy=continue", "--unit", "orrery-hold-" + testSession,
	}
}

// holderArgv returns the argv a launch runs its holder with: orrery holder, then the child's path and arguments after
// the separator.
func holderArgv(orrery, stateDir string, child proc.Config) []string {
	return append([]string{
		orrery, "holder", "--session", testSession, "--state-dir", stateDir, "--", child.Path,
	}, child.Args...)
}

// neverServingOrrery returns an orrery binary that writes its pid to the returned file and then sleeps, so the holder
// it stands for never serves its socket.
func neverServingOrrery(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "holder.pid")
	orrery := filepath.Join(dir, "orrery")
	script := "#!/bin/sh\necho $$ > " + pidFile + "\nexec sleep 3600\n"
	if err := os.WriteFile(orrery, []byte(script), 0o700); err != nil {
		t.Fatalf("write %s: %v", orrery, err)
	}
	return orrery, pidFile
}

// readPid returns the pid that the file at path holds.
func readPid(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("%s holds %q, want a pid: %v", path, data, err)
	}
	return pid
}

// parentPid returns the parent process id of pid, read from /proc.
func parentPid(t *testing.T, pid int) int {
	t.Helper()
	path := fmt.Sprintf("/proc/%d/stat", pid)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	// The command name sits in parentheses and may hold spaces, so the fields are read after its last parenthesis:
	// the state, then the parent's pid.
	stat := string(data)
	fields := strings.Fields(stat[strings.LastIndexByte(stat, ')')+1:])
	if len(fields) < 2 {
		t.Fatalf("%s has %d fields after the command name, want at least 2", path, len(fields))
	}
	ppid, err := strconv.Atoi(fields[1])
	if err != nil {
		t.Fatalf("%s parent pid %q: %v", path, fields[1], err)
	}
	return ppid
}

// cmdlineOf returns the argv of process pid, read from /proc.
func cmdlineOf(t *testing.T, pid int) []string {
	t.Helper()
	path := fmt.Sprintf("/proc/%d/cmdline", pid)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return strings.Split(strings.TrimSuffix(string(data), "\x00"), "\x00")
}

// requireProcessGone fails the test unless process pid has ended and been reaped.
func requireProcessGone(t *testing.T, pid int) {
	t.Helper()
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("process %d: kill(0) = %v, want ESRCH", pid, err)
	}
}

// requireEmptyDir fails the test when dir holds anything.
func requireEmptyDir(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir %s: %v", dir, err)
	}
	if len(entries) != 0 {
		t.Fatalf("%s holds %d entries, want none", dir, len(entries))
	}
}

// readStdoutBytes reads p's stdout until n bytes have arrived, and returns them.
func readStdoutBytes(t *testing.T, p *Process, n int) string {
	t.Helper()
	var got []byte
	for len(got) < n {
		res := readOnce(t, p.Stdout())
		if res.err != nil {
			t.Fatalf("read stdout after %q: %v", got, res.err)
		}
		got = append(got, res.data...)
	}
	return string(got)
}

func TestLaunchPlainStartRunsTheChildUnderItsHoldersArgv(t *testing.T) {
	root := launchRoot(t)
	child := launchChild(t, "exec cat")
	p := mustLaunch(t, LaunchConfig{StateDir: root, Session: testSession, Child: child})

	if got := awaitWrite(t, startWrite(p, []byte(launchLine))); got.err != nil {
		t.Fatalf("write to the child: %v", got.err)
	}
	if echoed := readStdoutBytes(t, p, len(launchLine)); echoed != launchLine {
		t.Fatalf("child echoed %q, want %q", echoed, launchLine)
	}
	childPid := readPid(t, filepath.Join(child.Dir, launchChildPid))
	want := holderArgv(orreryPath(t), root, child)
	if got := cmdlineOf(t, parentPid(t, childPid)); !slices.Equal(got, want) {
		t.Fatalf("holder argv %q, want %q", got, want)
	}
	stopProcess(t, p)
}

func TestLaunchScopeRunsTheHolderBehindTheScopeArgs(t *testing.T) {
	root := launchRoot(t)
	record := filepath.Join(t.TempDir(), "argv")
	t.Setenv("FAKE_SYSTEMD_RUN_ARGS", record)
	child := launchChild(t, "exec cat")
	p := mustLaunch(t, LaunchConfig{
		Scope:    fakeSystemdRunPath(t),
		StateDir: root,
		Session:  testSession,
		Child:    child,
	})
	stopProcess(t, p)

	data, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("read the fake systemd-run's record: %v", err)
	}
	got := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	want := append(scopeArgs(), holderArgv(orreryPath(t), root, child)...)
	if !slices.Equal(got, want) {
		t.Fatalf("systemd-run argv %q, want %q", got, want)
	}
}

func TestLaunchScopeFailureFailsWithItsReasonAndStartsNoChild(t *testing.T) {
	root := launchRoot(t)
	t.Setenv("FAKE_SYSTEMD_RUN_FAIL", launchReason)
	child := launchChild(t, "touch ran")
	p, err := Launch(t.Context(), LaunchConfig{
		Scope:    fakeSystemdRunPath(t),
		StateDir: root,
		Session:  testSession,
		Child:    child,
	})
	if err == nil {
		stopProcess(t, p)
		t.Fatal("Launch succeeded although systemd-run failed, want it to fail")
	}
	if !strings.Contains(err.Error(), launchReason) {
		t.Fatalf("Launch error %q does not carry systemd-run's reason %q", err, launchReason)
	}
	if _, statErr := os.Stat(filepath.Join(child.Dir, "ran")); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("the child ran after systemd-run failed: stat = %v", statErr)
	}
}

func TestLaunchFailureRemovesThePrivateStateDir(t *testing.T) {
	root := launchRoot(t)
	child := launchChild(t, "exec cat")
	t.Setenv("TMPDIR", root)
	t.Setenv("FAKE_SYSTEMD_RUN_FAIL", launchReason)
	_, err := Launch(t.Context(), LaunchConfig{
		Scope:   fakeSystemdRunPath(t),
		Session: testSession,
		Child:   child,
	})
	if err == nil || !strings.Contains(err.Error(), launchReason) {
		t.Fatalf("Launch error %v, want systemd-run's reason %q", err, launchReason)
	}
	requireEmptyDir(t, root)
}

func TestLaunchTimeoutFailsAndStopsAHolderThatNeverServes(t *testing.T) {
	root := launchRoot(t)
	orrery, pidFile := neverServingOrrery(t)
	clock := newFakeClock()
	child := launchChild(t, "exec cat")
	result := startAsync(func() error {
		_, err := Launch(t.Context(), LaunchConfig{
			Orrery:   orrery,
			StateDir: root,
			Session:  testSession,
			Child:    child,
			Clock:    clock,
		})
		return err
	})
	awaitFile(t, pidFile)
	clock.waitArmed(t, 2)
	clock.Advance(LaunchTimeout)
	if err := awaitResult(t, "Launch", result); err == nil || !strings.Contains(err.Error(), "did not serve") {
		t.Fatalf("Launch error %v, want the timeout", err)
	}
	requireProcessGone(t, readPid(t, pidFile))
}

func TestLaunchCancelStopsAHolderThatNeverServes(t *testing.T) {
	root := launchRoot(t)
	orrery, pidFile := neverServingOrrery(t)
	child := launchChild(t, "exec cat")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := startAsync(func() error {
		_, err := Launch(ctx, LaunchConfig{Orrery: orrery, StateDir: root, Session: testSession, Child: child})
		return err
	})
	awaitFile(t, pidFile)
	cancel()
	if err := awaitResult(t, "Launch", result); !errors.Is(err, context.Canceled) {
		t.Fatalf("Launch error %v, want context.Canceled", err)
	}
	requireProcessGone(t, readPid(t, pidFile))
}

func TestLaunchChildExitingWithAStatusGivesWaitItsExitErrorAndStderrTail(t *testing.T) {
	root := launchRoot(t)
	// The child waits for a line, so it cannot end before Launch attaches.
	child := launchChild(t, "read line; "+exitTailChild)
	p := mustLaunch(t, LaunchConfig{StateDir: root, Session: testSession, Child: child})
	release(t, p)

	err := awaitResult(t, "Wait", startAsync(p.Wait))
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != childDiedCode {
		t.Fatalf("Wait = %v, want an *exec.ExitError with code %d", err, childDiedCode)
	}
	if got := p.Stderr(); got != "err\n" {
		t.Fatalf("Stderr = %q, want %q", got, "err\n")
	}
}

func TestLaunchPrivateStateDirIsGoneOnceWaitReturns(t *testing.T) {
	root := launchRoot(t)
	child := launchChild(t, "read line; exit 0")
	t.Setenv("TMPDIR", root)
	p := mustLaunch(t, LaunchConfig{Session: testSession, Child: child})
	release(t, p)

	if err := awaitResult(t, "Wait", startAsync(p.Wait)); err != nil {
		t.Fatalf("Wait = %v, want nil after the child exits 0", err)
	}
	requireEmptyDir(t, root)
}
