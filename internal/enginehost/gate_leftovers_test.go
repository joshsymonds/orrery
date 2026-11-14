package enginehost

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/holder"
)

// gateTestStart returns the start time of pid as a pids record holds it: field 22 of /proc/PID/stat.
func gateTestStart(t *testing.T, pid int) string {
	t.Helper()
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		t.Fatalf("read stat of pid %d: %v", pid, err)
	}
	stat := string(raw)
	end := strings.LastIndexByte(stat, ')')
	if end < 0 {
		t.Fatalf("stat of pid %d has no command name: %q", pid, stat)
	}
	fields := strings.Fields(stat[end+1:])
	const startIndex = 22 - 3
	if len(fields) <= startIndex {
		t.Fatalf("stat of pid %d has %d fields after its command name: %q", pid, len(fields), stat)
	}
	return fields[startIndex]
}

// gateStartLeader starts a sleep that leads its own process group, as a gate's child does, and returns its pid. The
// cleanup kills its group and waits for it, so the process never outlives the test.
func gateStartLeader(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("sleep", "30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start group leader: %v", err)
	}
	pid := cmd.Process.Pid
	waited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(waited)
	}()
	t.Cleanup(func() {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		<-waited
	})
	return pid
}

// gateWriteLeftoverPids writes a pids record in dir that names child, started at childStart.
func gateWriteLeftoverPids(t *testing.T, dir string, child int, childStart string) {
	t.Helper()
	self := os.Getpid()
	pids := holder.Pids{Holder: self, HolderStart: gateTestStart(t, self), Child: child, ChildStart: childStart}
	if err := holder.WritePids(filepath.Join(dir, holder.PidsName), pids); err != nil {
		t.Fatalf("write pids record: %v", err)
	}
}

// gateAssertStillRunning fails the test when pid has ended. A group that gateKillLeftovers signals dies well within the
// settling time, so a process still alive after it was spared.
func gateAssertStillRunning(t *testing.T, pid int, why string) {
	t.Helper()
	time.Sleep(500 * time.Millisecond)
	if gateProcessGone(pid) {
		t.Fatalf("process %d was killed, though %s", pid, why)
	}
}

func TestGateLeftoversSpareGroupOfPidHeldElsewhere(t *testing.T) {
	t.Parallel()
	leader := gateStartLeader(t)
	start, err := strconv.ParseUint(gateTestStart(t, leader), 10, 64)
	if err != nil {
		t.Fatalf("start of pid %d: %v", leader, err)
	}
	dir := t.TempDir()
	gateWriteLeftoverPids(t, dir, leader, strconv.FormatUint(start+1, 10))
	if err = gateKillLeftovers(dir); err != nil {
		t.Fatalf("gateKillLeftovers: %v", err)
	}
	gateAssertStillRunning(t, leader, "its pids record names another start time for the pid")
}

func TestGateLeftoversSignalNothingWithoutChildStart(t *testing.T) {
	t.Parallel()
	leader := gateStartLeader(t)
	dir := t.TempDir()
	gateWriteLeftoverPids(t, dir, leader, "")
	if err := gateKillLeftovers(dir); err != nil {
		t.Fatalf("gateKillLeftovers: %v", err)
	}
	gateAssertStillRunning(t, leader, "its pids record has no ChildStart to check it against")
}

func TestGateLeftoversKillsGroupOfRecordedChild(t *testing.T) {
	t.Parallel()
	child := gateStartLeader(t)
	dir := t.TempDir()
	gateWriteLeftoverPids(t, dir, child, gateTestStart(t, child))
	if err := gateKillLeftovers(dir); err != nil {
		t.Fatalf("gateKillLeftovers: %v", err)
	}
	gateWaitGone(t, child)
}

func TestGateLeftoversKillsMemberLeftByExitedChild(t *testing.T) {
	t.Parallel()
	stateDir := gateShortStateDir(t)
	holderDir, err := holder.SessionDir(filepath.Join(stateDir, gateHoldersName), "leftovers")
	if err != nil {
		t.Fatalf("holder dir: %v", err)
	}
	member := filepath.Join(t.TempDir(), "member")
	result, err := runGrantedGate(t.Context(), &fakeGatePools{}, gateGrantRequest(), gateRun{
		// The child leaves a background sleep in its group, then exits once its holder has recorded the child.
		Argv: []string{"/bin/sh", "-c",
			`sleep 30 >/dev/null 2>&1 & echo "$!" > "$MEMBER"; ` +
				`while [ ! -e "$RECORD" ]; do sleep 0.05; done; exit 0`},
		Dir:      t.TempDir(),
		Limit:    time.Minute,
		Name:     "leftovers",
		StateDir: stateDir,
		Environ:  gateEnv("MEMBER="+member, "RECORD="+filepath.Join(holderDir, holder.PidsName)),
	})
	gateKillDetachedAtCleanup(t, member)
	if err != nil {
		t.Fatalf("runGrantedGate: %v", err)
	}
	if result.Exit != 0 {
		t.Fatalf("Exit = %d, want 0", result.Exit)
	}
	pid, err := strconv.Atoi(gateWaitFile(t, member))
	if err != nil {
		t.Fatalf("member pid in %s: %v", member, err)
	}
	gateWaitGone(t, pid)
}
