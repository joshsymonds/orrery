package holder

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/joshsymonds/orrery/internal/waittest"
)

// daemonChildScript is a launch test child's script that starts a daemon, a setsid sleep with its stdio closed that it
// leaves running, as ssh leaves a ControlPersist master, and writes the daemon's pid to daemon.pid. It then echoes one
// line of its stdin and ends, leaving the daemon running. The subshell that starts the daemon exits at once, so the
// daemon is no child of the child.
const daemonChildScript = "(setsid sleep 3600 </dev/null >/dev/null 2>&1 & " +
	"echo $! > daemon.pid.tmp && mv daemon.pid.tmp daemon.pid); IFS= read -r line; printf '%s\\n' \"$line\""

// scopeStopLine is the line the fake systemctl records for a stop of testSession's scope.
const scopeStopLine = "--user stop orrery-hold-" + testSession + ".scope"

// recordSystemctl points the fake systemctl's record at a fresh file and returns its path.
func recordSystemctl(t *testing.T) string {
	t.Helper()
	record := filepath.Join(t.TempDir(), "systemctl")
	t.Setenv("FAKE_SYSTEMCTL_ARGS", record)
	return record
}

// systemctlCalls returns the calls the fake systemctl recorded in record, one per line, or none when it ran no call.
func systemctlCalls(t *testing.T, record string) []string {
	t.Helper()
	data, err := os.ReadFile(record)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("read the fake systemctl's record: %v", err)
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

// awaitDaemon waits until the child of p, whose working directory is dir, has written its daemon's pid, and returns the
// pid once the daemon is checked to be a running sleep. It fails at once when p has ended first. The daemon is killed
// at cleanup, should a failed test leave it running.
func awaitDaemon(t *testing.T, p *Process, dir string) int {
	t.Helper()
	path := filepath.Join(dir, "daemon.pid")
	waittest.Until(t, p.ended, "the child's daemon", func() bool {
		_, err := os.Stat(path)
		return err == nil
	})
	pid := readPid(t, path)
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	if !processLive(t, pid) || filepath.Base(cmdlineOf(t, pid)[0]) != "sleep" {
		t.Fatalf("daemon %d is not a running sleep", pid)
	}
	return pid
}

// echoLine writes launchLine to p's child and reads it back. A daemonChildScript child ends once it has echoed it.
func echoLine(t *testing.T, p *Process) {
	t.Helper()
	if got := awaitWrite(t, startWrite(p, []byte(launchLine))); got.err != nil {
		t.Fatalf("write to the child: %v", got.err)
	}
	if echoed := readStdoutBytes(t, p, len(launchLine)); echoed != launchLine {
		t.Fatalf("child echoed %q, want %q", echoed, launchLine)
	}
}

// TestLaunchScopeStopsTheScopeOnceTheHolderEnds holds a daemon past its holder: the child starts a daemon in the scope
// and then ends, and the daemon runs on. Once the holder has ended, the scope is stopped, so the daemon is gone by the
// time Wait returns and nothing keeps the session's scope name loaded.
func TestLaunchScopeStopsTheScopeOnceTheHolderEnds(t *testing.T) {
	root := launchRoot(t)
	record := recordSystemctl(t)
	child := launchChild(t, daemonChildScript)
	p, err := Launch(t.Context(), LaunchConfig{Scope: fakeSystemdRunPath(t), StateDir: root, Session: testSession,
		Child: child})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	daemon := awaitDaemon(t, p, child.Dir)

	echoLine(t, p)
	if err = p.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if processLive(t, daemon) {
		t.Fatalf("daemon %d the child left still runs after its holder ended", daemon)
	}
	// The launch stops the scope before it starts the holder, and the Process stops it once the holder has ended.
	if got, want := systemctlCalls(t, record), []string{scopeStopLine, scopeStopLine}; !slices.Equal(got, want) {
		t.Fatalf("systemctl calls %q, want %q", got, want)
	}
}

// TestLaunchScopeStopsAScopeAnEndedHolderLeft starts a session whose scope is still loaded: a process in it stands in
// for the daemon of a holder that ended while no controller watched it, so no stop ran. systemd-run refuses a loaded
// scope name, so the launch stops that scope first, and then the new holder serves.
func TestLaunchScopeStopsAScopeAnEndedHolderLeft(t *testing.T) {
	root := launchRoot(t)
	scope := fakeSystemdRunPath(t)
	// The fake systemd-run names its scopes within its own directory, with its symlinks resolved.
	scopes, err := filepath.EvalSymlinks(filepath.Dir(scope))
	if err != nil {
		t.Fatalf("resolve the fake scope's directory: %v", err)
	}
	stale := exec.Command("sleep", "3600")
	stale.Env = append(os.Environ(), "FAKE_SCOPE_UNIT="+filepath.Join(scopes, "orrery-hold-"+testSession+".scope"))
	stale.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err = stale.Start(); err != nil {
		t.Fatalf("start the stale scope's daemon: %v", err)
	}
	waited := make(chan error, 1)
	go func() { waited <- stale.Wait() }()
	t.Cleanup(func() {
		_ = stale.Process.Kill()
		<-waited
	})

	p := mustLaunch(t, LaunchConfig{Scope: scope, StateDir: root, Session: testSession,
		Child: launchChild(t, "exec cat")})
	if processLive(t, stale.Process.Pid) {
		t.Fatalf("the stale scope's daemon %d still runs after the launch", stale.Process.Pid)
	}
	echoLine(t, p)
	stopProcess(t, p)
}

// TestReattachWithScopeKeepsALiveHolderAndStopsItsScopeOnceItEnds detaches a scoped holder, as a controller's shutdown
// does, and re-adopts it under the same scope. The re-adoption stops nothing: the daemon in the scope still runs and the
// holder still serves. Once the re-adopted holder has ended, its scope is stopped as a launched holder's is.
func TestReattachWithScopeKeepsALiveHolderAndStopsItsScopeOnceItEnds(t *testing.T) {
	root := launchRoot(t)
	record := recordSystemctl(t)
	scope := fakeSystemdRunPath(t)
	child := launchChild(t, daemonChildScript)
	first, err := Launch(t.Context(), LaunchConfig{Scope: scope, StateDir: root, Session: testSession, Child: child})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	daemon := awaitDaemon(t, first, child.Dir)
	if err = first.Detach(); err != nil {
		t.Fatalf("Detach: %v", err)
	}

	dir, err := SessionDir(filepath.Join(root, holdersName), testSession)
	if err != nil {
		t.Fatalf("SessionDir: %v", err)
	}
	p, _, err := Reattach(t.Context(), dir, 0, WireVersion, WithScope(scope))
	if err != nil {
		t.Fatalf("Reattach: %v", err)
	}
	if got, want := systemctlCalls(t, record), []string{scopeStopLine}; !slices.Equal(got, want) {
		t.Fatalf("systemctl calls after the re-adoption %q, want only the launch's %q", got, want)
	}
	if !processLive(t, daemon) {
		t.Fatalf("daemon %d of the live holder's scope ended at the re-adoption", daemon)
	}
	echoLine(t, p)
	if err = p.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if processLive(t, daemon) {
		t.Fatalf("daemon %d still runs after the re-adopted holder ended", daemon)
	}
	if got, want := systemctlCalls(t, record), []string{scopeStopLine, scopeStopLine}; !slices.Equal(got, want) {
		t.Fatalf("systemctl calls %q, want %q", got, want)
	}
}
