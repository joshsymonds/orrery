package holder

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/engine/proc"
)

const (
	// launchGateLimit is the gate limit a gate launch test gives its holder. Each test ends its child well before it.
	launchGateLimit = time.Minute
	// launchReleaseFile is the file a gate launch test's child waits for in its working directory before it runs its
	// script. A gate's stdin is closed, so a line cannot release it.
	launchReleaseFile = "release"
)

// launchGateChild returns the child a gate launch test runs: a launchChild that waits for launchReleaseFile before it
// runs script.
func launchGateChild(t *testing.T, script string) proc.Config {
	t.Helper()
	return launchChild(t, "until [ -e "+launchReleaseFile+" ]; do sleep 0.01; done; "+script)
}

// releaseGateChild lets a gate launch test's child run, by creating launchReleaseFile in the child's working directory.
func releaseGateChild(t *testing.T, child proc.Config) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(child.Dir, launchReleaseFile), nil, 0o600); err != nil {
		t.Fatalf("release the child: %v", err)
	}
}

// gateHolderArgv returns the holder's argv after its program for a gate launch: the holder's argv with --gate-limit
// before the separator, as seconds.
func gateHolderArgv(stateDir string, child proc.Config, seconds string) []string {
	return append([]string{
		"holder", "--session", testSession, "--state-dir", stateDir, "--gate-limit", seconds, "--", child.Path,
	}, child.Args...)
}

// sessionDirOf returns the holder directory of testSession under the state directory root.
func sessionDirOf(t *testing.T, root string) string {
	t.Helper()
	dir, err := SessionDir(filepath.Join(root, holdersName), testSession)
	if err != nil {
		t.Fatalf("SessionDir: %v", err)
	}
	return dir
}

func TestHolderCommandGateLimitIsItsWholeSecondsBeforeTheSeparator(t *testing.T) {
	orrery := orreryPath(t)
	root := launchRoot(t)
	child := launchChild(t, "exec cat")
	cases := []struct {
		name    string
		limit   time.Duration
		seconds string
	}{
		{name: "whole seconds", limit: 90 * time.Second, seconds: "90"},
		{name: "rounded up", limit: 1500 * time.Millisecond, seconds: "2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := LaunchConfig{Session: testSession, Child: child, GateLimit: tc.limit}
			want := gateHolderArgv(root, child, tc.seconds)

			prog, argv := holderCommand(cfg, orrery, root)
			if prog != orrery || !slices.Equal(argv, want) {
				t.Fatalf("plain holder command = %q %q, want %q %q", prog, argv, orrery, want)
			}

			cfg.Scope = fakeSystemdRunPath(t)
			prog, argv = holderCommand(cfg, orrery, root)
			wantScoped := append(scopeArgs(), orrery)
			wantScoped = append(wantScoped, want...)
			if prog != cfg.Scope || !slices.Equal(argv, wantScoped) {
				t.Fatalf("scoped holder command = %q %q, want %q %q", prog, argv, cfg.Scope, wantScoped)
			}
		})
	}
}

func TestLaunchGateKeepsItsExitRecordAfterTheHolderEnds(t *testing.T) {
	root := launchRoot(t)
	child := launchGateChild(t, exitTailChild)
	p := mustLaunch(t, LaunchConfig{
		StateDir:  root,
		Session:   testSession,
		Child:     child,
		GateLimit: launchGateLimit,
	})
	releaseGateChild(t, child)

	err := awaitResult(t, "Wait", startAsync(p.Wait))
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != childDiedCode {
		t.Fatalf("Wait = %v, want an *exec.ExitError with code %d", err, childDiedCode)
	}
	exit, ok := p.Exit()
	if !ok {
		t.Fatal("Exit reports no exit record after Wait returned")
	}
	if exit.Reason != ReasonExited || exit.Code != childDiedCode {
		t.Fatalf("Exit = reason %q code %d, want reason %q code %d",
			exit.Reason, exit.Code, ReasonExited, childDiedCode)
	}
	if got, want := p.Stderr(), "out\nerr\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
	if _, statErr := os.Stat(filepath.Join(sessionDirOf(t, root), ExitName)); statErr != nil {
		t.Fatalf("exit record after the holder ended: %v, want it kept until the caller clears it", statErr)
	}
}

func TestLaunchGateExitIsAbsentUntilTheChildExits(t *testing.T) {
	root := launchRoot(t)
	child := launchGateChild(t, exitTailChild)
	p := mustLaunch(t, LaunchConfig{
		StateDir:  root,
		Session:   testSession,
		Child:     child,
		GateLimit: launchGateLimit,
	})
	if exit, ok := p.Exit(); ok {
		t.Fatalf("Exit = %+v before the child ran, want none", exit)
	}
	releaseGateChild(t, child)

	if err := awaitResult(t, "Wait", startAsync(p.Wait)); err == nil {
		t.Fatal("Wait = nil, want the holder's status for the child's exit 3")
	}
	if _, ok := p.Exit(); !ok {
		t.Fatal("Exit reports no exit record once the child has exited")
	}
}

func TestLaunchWithoutGateLimitRemovesItsDirectoryOnceWaitReturns(t *testing.T) {
	root := launchRoot(t)
	child := launchChild(t, "read line; exit 0")
	p := mustLaunch(t, LaunchConfig{StateDir: root, Session: testSession, Child: child})
	release(t, p)

	if err := awaitResult(t, "Wait", startAsync(p.Wait)); err != nil {
		t.Fatalf("Wait = %v, want nil after the child exits 0", err)
	}
	if _, statErr := os.Stat(sessionDirOf(t, root)); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("session directory after Wait: stat = %v, want it removed", statErr)
	}
}

func TestLaunchGateWithoutStateDirFailsAndMakesNoDirectory(t *testing.T) {
	root := launchRoot(t)
	child := launchGateChild(t, exitTailChild)
	t.Setenv("TMPDIR", root)
	_, err := Launch(t.Context(), LaunchConfig{Session: testSession, Child: child, GateLimit: launchGateLimit})
	if err == nil {
		t.Fatal("Launch of a gate with no state directory succeeded, want an error")
	}
	requireEmptyDir(t, root)
}
