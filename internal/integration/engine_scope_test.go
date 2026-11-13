package integration_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/joshsymonds/orrery/internal/holder"
	"github.com/joshsymonds/orrery/internal/store"
)

// engineFakeSystemdRun is the fake systemd-run the holder tests share. It records its arguments to $FAKE_SYSTEMD_RUN_ARGS
// and execs the holder once it drops the seven scope arguments.
const engineFakeSystemdRun = "../holder/testdata/fakesystemdrun.sh"

// clearHolderTimeout bounds the holder.Clear a test's cleanup makes of its session's holder.
const clearHolderTimeout = waitLimit

// scopedFixture returns the fixture of a test whose sessions run their holders under the fake systemd-run, the absolute
// path of that fake for --holder-scope, and the file the fake records its arguments in.
func scopedFixture(t *testing.T) (*engineFixture, string, string) {
	t.Helper()
	f := newEngineFixture(t)
	scope, err := filepath.Abs(engineFakeSystemdRun)
	if err != nil {
		t.Fatalf("resolve fake systemd-run: %v", err)
	}
	argsFile := filepath.Join(f.root, "systemd-run.args")
	f.env = append(f.env, "FAKE_SYSTEMD_RUN_ARGS="+argsFile)
	return f, scope, argsFile
}

// TestEngineHolderScopeSurvivesStop proves the --holder-scope path end to end. A session an orreryd started with the flag
// runs its holder under the fake systemd-run, with the scope's argv and orreryd's state directory. Stopping that orreryd
// detaches the session and records nothing for it, so its fake claude keeps running and its row stays working with its
// unfinished text. A restarted orreryd re-adopts the session at its reconcile: the fake still runs, the row is working with
// the same text, and the session stops when it is asked to.
func TestEngineHolderScopeSurvivesStop(t *testing.T) {
	f, scope, argsFile := scopedFixture(t)

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
	requireProcessRunning(t, pid)
	if got := f.stateOf(t, id); got != "working" {
		t.Fatalf("state of %s after stop = %q, want working", id, got)
	}
	if got := sessionReason(t, f.dbPath, id); got != "" {
		t.Fatalf("reason of %s after stop = %q, want none", id, got)
	}
	if n := engineTextRows(t, f.dbPath, id); n != 1 {
		t.Fatalf("unfinished text rows of %s after stop = %d, want 1", id, n)
	}

	restarted := f.startOrreryd(t, true, "--holder-scope", scope)
	requireProcessRunning(t, pid)
	if got := f.stateOf(t, id); got != "working" {
		t.Fatalf("state of %s after restart = %q, want working", id, got)
	}
	if got := sessionReason(t, f.dbPath, id); got != "" {
		t.Fatalf("reason of %s after restart = %q, want none", id, got)
	}
	if n := engineTextRows(t, f.dbPath, id); n != 1 {
		t.Fatalf("unfinished text rows of %s after restart = %d, want 1", id, n)
	}
	f.stopSession(t, id, dir)
	wantExit(t, restarted.stop(t), 0)
}

// TestEngineHolderScopeSigkillReadoptsStalledTurn proves that a restart after orreryd is killed mid-turn re-adopts the
// session: its holder and fake claude keep running, and its row stays working with the stalled text.
func TestEngineHolderScopeSigkillReadoptsStalledTurn(t *testing.T) {
	f, scope, _ := scopedFixture(t)
	daemon := f.startOrreryd(t, true, "--holder-scope", scope)
	stateDir := filepath.Dir(f.dbPath)
	dir := f.sessionDir(t, "readopt-stall")
	id := f.start(t, dir, stallPrompt)
	t.Cleanup(func() { clearHolder(t, filepath.Join(stateDir, "holders", id)) })
	f.waitForUnfinishedText(t, id)
	pid := fakeClaudePID(t, dir)

	killOrreryd(t, daemon)
	restarted := f.startOrreryd(t, true, "--holder-scope", scope)

	requireProcessRunning(t, pid)
	if got := f.stateOf(t, id); got != "working" {
		t.Fatalf("state of %s after restart = %q, want working", id, got)
	}
	if got := sessionReason(t, f.dbPath, id); got != "" {
		t.Fatalf("reason of %s after restart = %q, want none", id, got)
	}
	if n := engineTextRows(t, f.dbPath, id); n != 1 {
		t.Fatalf("unfinished text rows of %s after restart = %d, want 1", id, n)
	}
	f.stopSession(t, id, dir)
	wantExit(t, restarted.stop(t), 0)
}

// TestEngineHolderScopeSigkillReadoptsAskedQuestion proves that a restart after orreryd is killed while a question waits
// re-adopts the session: the question is still open, the answer reaches the fake claude, and the turn finishes.
func TestEngineHolderScopeSigkillReadoptsAskedQuestion(t *testing.T) {
	f, scope, _ := scopedFixture(t)
	daemon := f.startOrreryd(t, true, "--holder-scope", scope)
	stateDir := filepath.Dir(f.dbPath)
	dir := f.sessionDir(t, "readopt-ask")
	id := f.start(t, dir, "ASK")
	t.Cleanup(func() { clearHolder(t, filepath.Join(stateDir, "holders", id)) })
	f.waitForEvent(t, id, store.EventEngineRequest, `"requestId":"ask-1"`)
	pid := fakeClaudePID(t, dir)

	killOrreryd(t, daemon)
	restarted := f.startOrreryd(t, true, "--holder-scope", scope)

	requireProcessRunning(t, pid)
	f.waitForHistory(t, id, func(h store.EngineHistory) bool {
		return h.State == "waiting" && h.Question == "Which color?"
	})
	wantExit(t, f.orrery(t, "session", "answer", id, "ask-1", "red"), 0)
	f.waitForBlock(t, id, "fake answered 1")
	f.stopSession(t, id, dir)
	wantExit(t, restarted.stop(t), 0)
}

// killOrreryd sends SIGKILL to the orreryd daemon the test started and waits for it to exit. Its holders keep running.
func killOrreryd(t *testing.T, daemon *orreryProcess) {
	t.Helper()
	if err := daemon.cmd.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatalf("kill orreryd: %v", err)
	}
	daemon.exitOutcome(t)
}

// requireProcessRunning fails unless /proc shows pid as a process that has not exited. A zombie has exited.
func requireProcessRunning(t *testing.T, pid int) {
	t.Helper()
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		t.Fatalf("fake claude process %d is not running: %v", pid, err)
	}
	_, after, found := strings.Cut(string(stat), ") ")
	if !found || strings.HasPrefix(after, "Z") || strings.HasPrefix(after, "X") {
		t.Fatalf("fake claude process %d is not running: stat %q", pid, stat)
	}
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
