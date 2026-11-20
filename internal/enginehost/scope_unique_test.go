package enginehost

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"syscall"
	"testing"

	"github.com/joshsymonds/orrery/internal/holder"
	"github.com/joshsymonds/orrery/internal/store"
)

// launchedScopeUnits returns the unit name each launch recorded in argv, the arguments the fake systemd-run recorded for
// the holders of session id, in launch order. It fails the test when a unit is not a launch's scope name of id, which is
// the session's name and a 16-digit hex nonce, with no .scope suffix.
func launchedScopeUnits(t *testing.T, argv []string, id string) []string {
	t.Helper()
	pattern := regexp.MustCompile("^orrery-hold-" + regexp.QuoteMeta(id) + "-[0-9a-f]{16}$")
	var units []string
	for i, arg := range argv {
		if arg != "--unit" || i+1 >= len(argv) {
			continue
		}
		if !pattern.MatchString(argv[i+1]) {
			t.Fatalf("scope unit %q, want orrery-hold-%s with a 16-digit hex nonce", argv[i+1], id)
		}
		units = append(units, argv[i+1])
	}
	return units
}

// startLoadedScope starts a process that stands in for a daemon left in the scope unit, as the fake systemd-run names
// it, so the scope is loaded with a live member. It returns the process's pid. The process is killed at cleanup, should a
// failed test leave it running.
func startLoadedScope(t *testing.T, scope, unit string) int {
	t.Helper()
	// The fake systemd-run names its scopes within its own directory, with its symlinks resolved.
	scopes, err := filepath.EvalSymlinks(filepath.Dir(scope))
	if err != nil {
		t.Fatalf("resolve the fake scope's directory: %v", err)
	}
	stale := exec.Command("sleep", "3600")
	stale.Env = append(os.Environ(), "FAKE_SCOPE_UNIT="+filepath.Join(scopes, unit+".scope"))
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
	return stale.Process.Pid
}

// TestStartScopeUniqueTwoLaunchesOfOneSessionUseDifferentUnits runs one session through a holder that ends, then resumes
// it, so the session's holder is launched twice. The two launches name their scopes differently.
func TestStartScopeUniqueTwoLaunchesOfOneSessionUseDifferentUnits(t *testing.T) {
	t.Parallel()
	root := holderRoot(t)
	record := filepath.Join(t.TempDir(), "argv")
	m, path := newHolderManager(t, fakeSystemdRunPath(t), root, "FAKE_SYSTEMD_RUN_ARGS="+record)
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "hi"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { closeAndClear(t, m, root, id) })
	waitHistory(t, engineOf(m), path, id, "the first reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1")
	})
	if err = m.Say(t.Context(), SayRequest{ID: id, Text: "DIE"}); err != nil {
		t.Fatalf("Say DIE: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the failed state", func(h store.EngineHistory) bool {
		return h.State == "failed"
	})
	if err = m.Say(t.Context(), SayRequest{ID: id, Text: "again"}); err != nil {
		t.Fatalf("Say after the death: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the resumed reply", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 3") && h.State == "done"
	})

	units := launchedScopeUnits(t, recordedArgs(t, record), id)
	if len(units) != 2 {
		t.Fatalf("launched scopes %q, want two launches", units)
	}
	if units[0] == units[1] {
		t.Fatalf("both launches of session %s run in scope %s, want a name unique to each launch", id, units[0])
	}
}

// TestSayScopeUniqueRespawnStartsAndStopsTheEarlierScope leaves a scope of the session loaded with a live member, as a
// holder that ended while no controller watched it would, and then resumes the session. The respawn names its own
// scope, so it starts, and the earlier scope is stopped before it does: the earlier scope's member is gone.
func TestSayScopeUniqueRespawnStartsAndStopsTheEarlierScope(t *testing.T) {
	t.Parallel()
	root := holderRoot(t)
	scope := fakeSystemdRunPath(t)
	m, path := newHolderManager(t, scope, root)
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "hi"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { closeAndClear(t, m, root, id) })
	waitHistory(t, engineOf(m), path, id, "the first reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1")
	})
	if err = m.Say(t.Context(), SayRequest{ID: id, Text: "DIE"}); err != nil {
		t.Fatalf("Say DIE: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the failed state", func(h store.EngineHistory) bool {
		return h.State == "failed"
	})
	earlier := startLoadedScope(t, scope, "orrery-hold-"+id+"-fedcba9876543210")

	if err = m.Say(t.Context(), SayRequest{ID: id, Text: "again"}); err != nil {
		t.Fatalf("Say after the earlier scope was left loaded: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the resumed reply", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 3") && h.State == "done"
	})
	if !gateProcessGone(earlier) {
		t.Fatalf("the earlier scope's member %d still runs after the respawn", earlier)
	}
}

// TestReconcileScopeUniqueReattachStopsItsOwnScope detaches a session whose child left a daemon in its scope, re-adopts
// its holder from a second manager, and ends that holder. The re-adopted holder's scope is the one its launch named, so
// its daemon is gone once the holder has ended.
func TestReconcileScopeUniqueReattachStopsItsOwnScope(t *testing.T) {
	t.Parallel()
	root := holderRoot(t)
	s, path := openStore(t)
	writer := NewWriter(WriterConfig{Applier: s})
	startRun(t, writer)
	scope := fakeSystemdRunPath(t)
	first := newStateManager(t, s, writer, &fakeSink{}, scope, root, nil)
	dir := t.TempDir()
	id, err := first.Start(t.Context(), StartRequest{Dir: dir, Prompt: "DAEMON"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { closeAndClear(t, first, root, id) })
	waitHistory(t, engineOf(first), path, id, "the first reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1")
	})
	daemon := scopeDaemon(t, dir)

	first.Close()
	if gateProcessGone(daemon) {
		t.Fatalf("daemon %d is gone after the controller detached", daemon)
	}
	second := newStateManager(t, s, writer, &fakeSink{}, scope, root, nil)
	t.Cleanup(func() { closeAndClear(t, second, root, id) })
	if err = second.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if !slices.Contains(second.Live(), id) {
		t.Fatalf("session %s is not live after Reconcile: live %v", id, second.Live())
	}

	if err = holder.Clear(t.Context(), holderDir(t, root, id)); err != nil {
		t.Fatalf("clear the re-adopted holder: %v", err)
	}
	waitUntil(t, engineOf(second), "the daemon in the re-adopted holder's scope to end", func() bool {
		return gateProcessGone(daemon)
	})
}
