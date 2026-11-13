package holder

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// orphanChildScript is the child an orphan test's holder runs. exec makes the child the sleep, so the child's pid is
// the sleep's.
const orphanChildScript = "exec sleep 600"

// procStatOf reads process pid's state letter and start time from /proc in one read. ok is false when /proc holds no
// stat for pid, which includes a process reaped between the open and the read (ESRCH). The test reads /proc itself, so
// its checks do not depend on the code they test.
func procStatOf(t *testing.T, pid int) (state, start string, ok bool) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ESRCH) {
		return "", "", false
	}
	if err != nil {
		t.Fatalf("read /proc/%d/stat: %v", pid, err)
	}
	fields := strings.Fields(string(raw[bytes.LastIndexByte(raw, ')')+1:]))
	if len(fields) <= pidsStartField {
		t.Fatalf("/proc/%d/stat has %d fields after its command name: %q", pid, len(fields), raw)
	}
	return fields[0], fields[pidsStartField], true
}

// procRunning reports whether /proc holds process pid with start start that is not a zombie.
func procRunning(t *testing.T, pid int, start string) bool {
	t.Helper()
	state, got, ok := procStatOf(t, pid)
	return ok && state != "Z" && got == start
}

// awaitStopped waits until process pid with start start no longer runs, failing the test after waitArmedTimeout.
func awaitStopped(t *testing.T, pid int, start string) {
	t.Helper()
	timeout := time.After(waitArmedTimeout)
	for procRunning(t, pid, start) {
		select {
		case <-timeout:
			t.Fatalf("process %d still runs %v after it should have stopped", pid, waitArmedTimeout)
		case <-time.After(holderPoll):
		}
	}
}

// awaitState waits until /proc holds process pid in state want, failing the test after waitArmedTimeout.
func awaitState(t *testing.T, pid int, want string) {
	t.Helper()
	timeout := time.After(waitArmedTimeout)
	for {
		if state, _, ok := procStatOf(t, pid); ok && state == want {
			return
		}
		select {
		case <-timeout:
			t.Fatalf("process %d did not reach state %q within %v", pid, want, waitArmedTimeout)
		case <-time.After(holderPoll):
		}
	}
}

// killGroupAndAwait SIGKILLs the process group led by pid when process pid with start still runs, and waits until it
// has stopped. A process that has already stopped is left alone.
func killGroupAndAwait(t *testing.T, pid int, start string) {
	t.Helper()
	if !procRunning(t, pid, start) {
		return
	}
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		t.Errorf("kill process group %d: %v", pid, err)
	}
	awaitStopped(t, pid, start)
}

// reap kills cmd's process unless the test already waited for it, then waits for it so it is not left a zombie.
func reap(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	if cmd.ProcessState != nil {
		return
	}
	if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		t.Errorf("kill process %d: %v", cmd.Process.Pid, err)
	}
	// Wait reports the signal that killed the process, which is the expected end.
	_ = cmd.Wait()
}

// startSleeper starts a sleep in its own process group, so a test can signal the group as StopOrphan does. The test's
// cleanup kills the group and reaps the sleep. It returns the sleep's pid and start time.
func startSleeper(t *testing.T) (int, string) {
	t.Helper()
	cmd := exec.Command(shPath(t), "-c", orphanChildScript)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleeper: %v", err)
	}
	pid := cmd.Process.Pid
	start := procStartOf(t, pid)
	t.Cleanup(func() {
		killGroupAndAwait(t, pid, start)
		reap(t, cmd)
	})
	return pid, start
}

// startOrphan runs a holder whose child sleeps, then SIGKILLs the holder once the holder's pids record is written. The
// child outlives its holder. It returns the session's holder dir and the record. The test's cleanup kills the child.
func startOrphan(t *testing.T) (string, Pids) {
	t.Helper()
	root := launchRoot(t)
	dir, err := SessionDir(filepath.Join(root, holdersName), testSession)
	if err != nil {
		t.Fatalf("SessionDir: %v", err)
	}
	holder := exec.Command(orreryPath(t), "holder", "--session", testSession, "--state-dir", root, "--",
		shPath(t), "-c", orphanChildScript)
	if err = holder.Start(); err != nil {
		t.Fatalf("start holder: %v", err)
	}
	t.Cleanup(func() { reap(t, holder) })
	recordPath := filepath.Join(dir, PidsName)
	awaitFile(t, recordPath)
	pids := readPidsRecord(t, recordPath)
	t.Cleanup(func() { killGroupAndAwait(t, pids.Child, pids.ChildStart) })

	if err = holder.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatalf("SIGKILL holder %d: %v", holder.Process.Pid, err)
	}
	// Wait reports the signal that killed the holder, which is the expected end.
	if waitErr := holder.Wait(); waitErr == nil {
		t.Fatalf("holder %d exited without error after SIGKILL", holder.Process.Pid)
	}
	if !procRunning(t, pids.Child, pids.ChildStart) {
		t.Fatalf("child %d ended with its holder, so the test has no orphan", pids.Child)
	}
	return dir, pids
}

// sessionDir returns an empty, private holder dir for one session under a short root that the test removes.
func sessionDir(t *testing.T) string {
	t.Helper()
	dir, err := SessionDir(filepath.Join(launchRoot(t), holdersName), testSession)
	if err != nil {
		t.Fatalf("SessionDir: %v", err)
	}
	if err = PrepareDir(dir); err != nil {
		t.Fatalf("PrepareDir: %v", err)
	}
	return dir
}

// writeRecord writes a pids record into dir that names child with start childStart and this test process as the
// holder.
func writeRecord(t *testing.T, dir string, pids Pids) {
	t.Helper()
	if err := WritePids(filepath.Join(dir, PidsName), pids); err != nil {
		t.Fatalf("WritePids: %v", err)
	}
}

func TestOrphanReadPidsReturnsTheRecordWritten(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	want := Pids{Holder: 11, HolderStart: "100", Child: 22, ChildStart: "200"}
	if err := WritePids(filepath.Join(dir, PidsName), want); err != nil {
		t.Fatalf("WritePids: %v", err)
	}
	got, err := ReadPids(dir)
	if err != nil {
		t.Fatalf("ReadPids: %v", err)
	}
	if got != want {
		t.Errorf("ReadPids = %+v, want %+v", got, want)
	}
}

func TestOrphanReadPidsWithoutARecordWrapsNotExist(t *testing.T) {
	t.Parallel()

	_, err := ReadPids(t.TempDir())
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("ReadPids error = %v, want one wrapping fs.ErrNotExist", err)
	}
}

func TestOrphanRunningReportsALiveProcessWithItsStart(t *testing.T) {
	pid, start := startSleeper(t)
	got, err := Running(pid, start)
	if err != nil {
		t.Fatalf("Running: %v", err)
	}
	if !got {
		t.Errorf("Running(%d, %s) = false, want true for a live process", pid, start)
	}
}

func TestOrphanRunningRefusesAStartThatDiffers(t *testing.T) {
	pid, start := startSleeper(t)
	got, err := Running(pid, start+"1")
	if err != nil {
		t.Fatalf("Running: %v", err)
	}
	if got {
		t.Errorf("Running(%d, %s) = true, want false for a start the process does not have", pid, start+"1")
	}
}

func TestOrphanRunningReportsAnAbsentPidAsNotRunning(t *testing.T) {
	t.Parallel()

	got, err := Running(absentPid, "1")
	if err != nil {
		t.Fatalf("Running: %v", err)
	}
	if got {
		t.Errorf("Running(%d) = true, want false for a pid no process holds", absentPid)
	}
}

func TestOrphanRunningReportsAZombieAsNotRunning(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	// The shell reads its stdin to the end and exits. Nothing reaps it, so it stays a zombie until the cleanup waits.
	cmd := exec.Command(shPath(t), "-c", "read line")
	cmd.Stdin = r
	if err = cmd.Start(); err != nil {
		t.Fatalf("start shell: %v", err)
	}
	t.Cleanup(func() { reap(t, cmd) })
	if err = r.Close(); err != nil {
		t.Fatalf("close read end: %v", err)
	}
	pid := cmd.Process.Pid
	start := procStartOf(t, pid)
	if err = w.Close(); err != nil {
		t.Fatalf("close write end: %v", err)
	}
	awaitState(t, pid, "Z")

	got, err := Running(pid, start)
	if err != nil {
		t.Fatalf("Running: %v", err)
	}
	if got {
		t.Errorf("Running(%d, %s) = true, want false for a zombie", pid, start)
	}
}

func TestOrphanStopOrphanKillsTheChildOfAKilledHolder(t *testing.T) {
	dir, pids := startOrphan(t)
	if err := StopOrphan(t.Context(), dir); err != nil {
		t.Fatalf("StopOrphan: %v", err)
	}
	if procRunning(t, pids.Child, pids.ChildStart) {
		t.Fatalf("child %d still runs after StopOrphan", pids.Child)
	}
}

func TestOrphanStopOrphanWithAnExitFileSignalsNothing(t *testing.T) {
	dir, pids := startOrphan(t)
	if err := WriteExit(filepath.Join(dir, ExitName), Exit{Reason: ReasonExited}); err != nil {
		t.Fatalf("WriteExit: %v", err)
	}
	if err := StopOrphan(t.Context(), dir); err != nil {
		t.Fatalf("StopOrphan: %v", err)
	}
	if !procRunning(t, pids.Child, pids.ChildStart) {
		t.Fatalf("child %d was stopped although its exit file is present", pids.Child)
	}
}

func TestOrphanStopOrphanWithoutAPidsRecordSignalsNothing(t *testing.T) {
	dir, pids := startOrphan(t)
	if err := os.Remove(filepath.Join(dir, PidsName)); err != nil {
		t.Fatalf("remove pids record: %v", err)
	}
	if err := StopOrphan(t.Context(), dir); err != nil {
		t.Fatalf("StopOrphan: %v", err)
	}
	if !procRunning(t, pids.Child, pids.ChildStart) {
		t.Fatalf("child %d was stopped although its pids record is absent", pids.Child)
	}
}

func TestOrphanStopOrphanSignalsNothingWhenTheChildStartNoLongerMatches(t *testing.T) {
	dir := sessionDir(t)
	pid, start := startSleeper(t)
	self := os.Getpid()
	writeRecord(t, dir, Pids{
		Holder:      self,
		HolderStart: procStartOf(t, self),
		Child:       pid,
		ChildStart:  start + "1",
	})
	if err := StopOrphan(t.Context(), dir); err != nil {
		t.Fatalf("StopOrphan: %v", err)
	}
	if !procRunning(t, pid, start) {
		t.Fatalf("process %d was signaled though its recorded start %s no longer matches", pid, start+"1")
	}
}

func TestOrphanStopOrphanWithAnEmptyStartReturnsAnErrorAndSignalsNothing(t *testing.T) {
	cases := []struct {
		name        string
		emptyHolder bool
	}{
		{name: "empty child start"},
		{name: "empty holder start", emptyHolder: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := sessionDir(t)
			pid, start := startSleeper(t)
			self := os.Getpid()
			pids := Pids{Holder: self, HolderStart: procStartOf(t, self), Child: pid, ChildStart: start}
			if tc.emptyHolder {
				pids.HolderStart = ""
			} else {
				pids.ChildStart = ""
			}
			writeRecord(t, dir, pids)
			if err := StopOrphan(t.Context(), dir); err == nil {
				t.Fatalf("StopOrphan = nil, want an error for a record with an empty start")
			}
			if !procRunning(t, pid, start) {
				t.Fatalf("process %d was signaled although its record has an empty start", pid)
			}
		})
	}
}

func TestOrphanStopOrphanFailsOnceItsContextIsDone(t *testing.T) {
	dir, pids := startOrphan(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := StopOrphan(ctx, dir); !errors.Is(err, context.Canceled) {
		t.Fatalf("StopOrphan = %v, want context.Canceled", err)
	}
	// The group was signaled before the wait gave up, so the child still stops.
	awaitStopped(t, pids.Child, pids.ChildStart)
}
