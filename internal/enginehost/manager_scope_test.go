package enginehost

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// scopeDaemon returns the pid of the daemon the fake claude in dir started for a DAEMON prompt, once it is checked to be
// a running sleep. The daemon is killed at cleanup, should a failed test leave it running.
func scopeDaemon(t *testing.T, dir string) int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "fakeclaude.daemon"))
	if err != nil {
		t.Fatalf("read the fake claude's daemon pid: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("daemon pid %q: %v", data, err)
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	cmdline, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
	if err != nil || gateProcessGone(pid) || !strings.HasPrefix(string(cmdline), "sleep\x00") {
		t.Fatalf("daemon %d is not a running sleep: cmdline %q, err %v", pid, cmdline, err)
	}
	return pid
}

// TestSayResumesASessionWhoseChildLeftADaemonInItsScope plays the session that ran git over ssh: its claude starts a
// daemon, as ssh starts a ControlPersist master in the holder's scope, and then dies, so its holder ends while the daemon
// runs on. The ended holder's scope is stopped, which ends the daemon, so the next Say resumes the session under a new
// holder in a scope of the same name, and the resumed turn clears the failure's reason.
func TestSayResumesASessionWhoseChildLeftADaemonInItsScope(t *testing.T) {
	t.Parallel()
	root := holderRoot(t)
	m, path := newHolderManager(t, fakeSystemdRunPath(t), root)
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "DAEMON"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { closeAndClear(t, m, root, id) })
	waitHistory(t, engineOf(m), path, id, "the first reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1")
	})
	daemon := scopeDaemon(t, dir)

	if err = m.Say(t.Context(), SayRequest{ID: id, Text: "DIE"}); err != nil {
		t.Fatalf("Say DIE: %v", err)
	}
	failed := waitHistory(t, engineOf(m), path, id, "the failed state", func(h store.EngineHistory) bool {
		return h.State == "failed"
	})
	if !strings.HasSuffix(failed.Reason, "fake died") {
		t.Fatalf("failed reason = %q, want the death's", failed.Reason)
	}
	// The failure is recorded once the holder's Process has ended, which is after its scope was stopped.
	if !gateProcessGone(daemon) {
		t.Fatalf("daemon %d still runs after its holder ended", daemon)
	}

	if err = m.Say(t.Context(), SayRequest{ID: id, Text: "again"}); err != nil {
		t.Fatalf("Say after the death: %v", err)
	}
	resumed := waitHistory(t, engineOf(m), path, id, "the resumed reply", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 3") && h.State == "done"
	})
	if resumed.Reason != "" {
		t.Fatalf("reason after the resumed turn = %q, want none", resumed.Reason)
	}
}
