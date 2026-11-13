package integration_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/joshsymonds/orrery/internal/holder"
)

// engineFakeSystemdRun is the fake systemd-run the holder tests share. It records its arguments to $FAKE_SYSTEMD_RUN_ARGS
// and execs the holder once it drops the seven scope arguments.
const engineFakeSystemdRun = "../holder/testdata/fakesystemdrun.sh"

// clearHolderTimeout bounds the holder.Clear a test's cleanup makes of its session's holder.
const clearHolderTimeout = waitLimit

// TestEngineHolderScopeSurvivesStop proves the --holder-scope path end to end. A session an orreryd started with the flag
// runs its holder under the fake systemd-run, with the scope's argv and orreryd's state directory. Stopping that orreryd
// detaches the session, so its fake claude keeps running. A restarted orreryd stops the holder when it reconciles, and the
// row keeps the reason the stop recorded.
func TestEngineHolderScopeSurvivesStop(t *testing.T) {
	f := newEngineFixture(t)
	scope, err := filepath.Abs(engineFakeSystemdRun)
	if err != nil {
		t.Fatalf("resolve fake systemd-run: %v", err)
	}
	argsFile := filepath.Join(f.root, "systemd-run.args")
	f.env = append(f.env, "FAKE_SYSTEMD_RUN_ARGS="+argsFile)

	daemon := f.startOrreryd(t, true, "--holder-scope", scope)
	stateDir := filepath.Dir(f.dbPath)
	dir := f.sessionDir(t, "scope")
	id := f.start(t, dir, stallPrompt)
	// The cleanup runs before the daemon's terminate, so a test that fails before the restart leaves no fake behind.
	t.Cleanup(func() { clearHolder(t, filepath.Join(stateDir, "holders", id)) })
	f.waitForUnfinishedText(t, id)
	pid := fakeClaudePID(t, dir)

	orreryBin := filepath.Join(os.Getenv(binDirEnv), "orrery")
	wantPrefix := []string{
		"--user", "--scope", "--collect", "-p", "OOMPolicy=continue", "--unit", "orrery-hold-" + id,
		orreryBin, "holder", "--session", id, "--state-dir", stateDir, "--",
	}
	argv := recordedArgv(t, argsFile)
	if len(argv) <= len(wantPrefix) || !slices.Equal(argv[:len(wantPrefix)], wantPrefix) {
		t.Fatalf("systemd-run argv %q does not start with %q", argv, wantPrefix)
	}
	if got := argv[len(wantPrefix)]; got != f.shell {
		t.Fatalf("holder child %q, want the session shell %q", got, f.shell)
	}

	wantExit(t, daemon.stop(t), 0)
	if err = syscall.Kill(pid, 0); err != nil {
		t.Fatalf("fake claude %d after orreryd stopped: %v", pid, err)
	}
	if got := f.stateOf(t, id); got != "interrupted" {
		t.Fatalf("state of %s after stop = %q, want interrupted", id, got)
	}
	if got := sessionReason(t, f.dbPath, id); got != stoppedDuringTurnBody {
		t.Fatalf("reason of %s after stop = %q, want %q", id, got, stoppedDuringTurnBody)
	}

	restarted := f.startOrreryd(t, true, "--holder-scope", scope)
	waitGone(t, pid)
	if got := f.stateOf(t, id); got != "interrupted" {
		t.Fatalf("state of %s after restart = %q, want interrupted", id, got)
	}
	if got := sessionReason(t, f.dbPath, id); got != stoppedDuringTurnBody {
		t.Fatalf("reason of %s after restart = %q, want %q", id, got, stoppedDuringTurnBody)
	}
	wantExit(t, restarted.stop(t), 0)
}

// recordedArgv returns the arguments the fake systemd-run recorded, one per line in argsFile.
func recordedArgv(t *testing.T, argsFile string) []string {
	t.Helper()
	raw, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read systemd-run arguments: %v", err)
	}
	return strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
}

// clearHolder empties the holder directory dir and fails the test when the clear fails. A directory that is already gone
// is not an error.
func clearHolder(t *testing.T, dir string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), clearHolderTimeout)
	defer cancel()
	if err := holder.Clear(ctx, dir); err != nil {
		t.Errorf("clear holder %s: %v", dir, err)
	}
}
