package holder

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// uniqueOtherSession names a second session in the scope tests, whose scopes must stay apart from testSession's.
const uniqueOtherSession = "u"

// TestLaunchScopeUniqueTwoLaunchesOfOneSessionUseDifferentUnits launches the same session twice, each time under the
// fake systemd-run, and checks that the two launches name their scopes differently. Each unit name carries the session
// and a nonce that is new for every launch.
func TestLaunchScopeUniqueTwoLaunchesOfOneSessionUseDifferentUnits(t *testing.T) {
	root := launchRoot(t)
	runs := recordSystemdRun(t)
	scope := fakeSystemdRunPath(t)
	first := mustLaunch(t, LaunchConfig{Scope: scope, StateDir: root, Session: testSession,
		Child: launchChild(t, "exec cat")})
	stopProcess(t, first)
	mustLaunch(t, LaunchConfig{Scope: scope, StateDir: root, Session: testSession, Child: launchChild(t, "exec cat")})

	scopes := launchedScopes(t, runs)
	if len(scopes) != 2 {
		t.Fatalf("launched scopes %q, want two", scopes)
	}
	if scopes[0] == scopes[1] {
		t.Fatalf("both launches of session %s run in scope %s, want a unique name each", testSession, scopes[0])
	}
	for _, s := range scopes {
		if !scopeUnitPattern.MatchString(strings.TrimSuffix(s, scopeUnitSuffix)) {
			t.Fatalf("scope %s, want %s with a 16-digit hex nonce", s, scopeUnitPrefix+testSession)
		}
	}
}

// TestLaunchScopeUniqueRespawnStartsAndStopsTheEarlierScope leaves a scope of the session loaded, with a live member, as
// a holder that ended while no controller watched it would. The respawn names its own scope, so it starts, and before it
// starts it stops the earlier scope: the member of that scope is gone once the respawn is running.
func TestLaunchScopeUniqueRespawnStartsAndStopsTheEarlierScope(t *testing.T) {
	root := launchRoot(t)
	record := recordSystemctl(t)
	scope := fakeSystemdRunPath(t)
	earlier := startLoadedScope(t, scope, scopeUnit(testSession, "fedcba9876543210"))

	p := mustLaunch(t, LaunchConfig{Scope: scope, StateDir: root, Session: testSession,
		Child: launchChild(t, "exec cat")})
	if processLive(t, earlier) {
		t.Fatalf("the earlier scope's member %d still runs once the respawn started", earlier)
	}
	echoLine(t, p)
	stopProcess(t, p)

	if calls := systemctlCalls(t, record); len(calls) == 0 || calls[0] != scopeGlobStopLine {
		t.Fatalf("systemctl calls %q, want the launch to begin with %q", calls, scopeGlobStopLine)
	}
}

// TestReattachScopeUniqueFindsAndStopsItsOwnScope launches a scoped holder and a second session's, detaches the first,
// and re-adopts it. The re-adopted holder's scope is the one its launch named, read from its directory, so once the holder
// has ended its daemon is gone, and the stop names that scope alone: the second session's scope is left running.
func TestReattachScopeUniqueFindsAndStopsItsOwnScope(t *testing.T) {
	root := launchRoot(t)
	record := recordSystemctl(t)
	runs := recordSystemdRun(t)
	scope := fakeSystemdRunPath(t)
	child := launchChild(t, daemonChildScript)
	first, err := Launch(t.Context(), LaunchConfig{Scope: scope, StateDir: root, Session: testSession, Child: child})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	daemon := awaitDaemon(t, first, child.Dir)
	own := launchedScopes(t, runs)[0]
	mustLaunch(t, LaunchConfig{Scope: scope, StateDir: root, Session: uniqueOtherSession,
		Child: launchChild(t, "exec cat")})
	other := launchedScopes(t, runs)[1]
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
	echoLine(t, p)
	if err = p.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if processLive(t, daemon) {
		t.Fatalf("daemon %d in the re-adopted holder's scope %s still runs after the holder ended", daemon, own)
	}
	calls := systemctlCalls(t, record)
	stops := 0
	for _, call := range calls {
		if call == "--user stop "+own {
			stops++
		}
	}
	if stops != 1 {
		t.Fatalf("stops of the re-adopted holder's scope %s = %d in %q, want exactly one", own, stops, calls)
	}
	if slices.Contains(calls, "--user stop "+other) {
		t.Fatalf("systemctl calls %q stop the other session's scope %s", calls, other)
	}
}
