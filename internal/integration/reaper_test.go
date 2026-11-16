package integration_test

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/joshsymonds/orrery/internal/childproc"
	"github.com/joshsymonds/orrery/internal/exectest"
	"github.com/joshsymonds/orrery/internal/holder"
	"github.com/joshsymonds/orrery/internal/waittest"
)

// Every process a test starts must die with the test binary, however it dies. A test binary that go test's timeout
// kills, or that anything SIGKILLs, runs no cleanup, so the orreryd, holders and fake claudes its tests started would
// live on and hold what they held, such as a gate box's lock. Two mechanisms end them. Each binary a test starts runs
// under childproc's Pdeathsig, so the kernel kills it with the test binary. The holders and their children are started
// by orreryd and outlive it by design, so the reaper ends them: a process the test binary starts before any test, which
// waits for its stdin to close, as it does when the test binary dies, and then kills every process that carries the
// run's mark.

// runMarkEnv names the variable that marks every process of one test binary's run. TestMain sets it in the test
// binary's environment, which every fixture's environment starts from, and orreryd passes it on to its holders, their
// children and its gates. Its name does not start with ORRERY_, so isolateFromHost and scrubbedEnv keep it.
const runMarkEnv = "INTEGRATION_RUN_MARK"

// reaperEnv names the variable that makes the test binary run as the reaper of the run whose mark it holds.
const reaperEnv = "INTEGRATION_REAPER_FOR"

// reaperPidEnv names the variable TestMain sets to the reaper's pid, which the reaper test's helper reports.
const reaperPidEnv = "INTEGRATION_REAPER_PID"

// startReaper starts the reaper for a new run mark, sets the mark and the reaper's pid in the test binary's
// environment, and returns the write end of the reaper's stdin. The caller keeps it open and never writes to it, so the reaper reads EOF only once
// the test binary has exited, which closes it. The reaper starts before the mark is set, so it carries none itself, and
// it runs in a session of its own, so a signal to the test binary's process group does not reach it.
func startReaper() (*os.File, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return nil, fmt.Errorf("make the run mark: %w", err)
	}
	mark := hex.EncodeToString(raw)
	read, write, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("make the reaper's stdin: %w", err)
	}
	cmd := exec.Command(os.Args[0], "-test.run", "^$")
	cmd.Env = append(os.Environ(), reaperEnv+"="+mark)
	cmd.Stdin = read
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err = cmd.Start(); err != nil {
		return nil, errors.Join(fmt.Errorf("start the reaper: %w", err), read.Close(), write.Close())
	}
	// The reaper holds its own copy of the read end, and the test binary keeps only the write end.
	if err = read.Close(); err != nil {
		return nil, fmt.Errorf("close the reaper's read end: %w", err)
	}
	pid := cmd.Process.Pid
	// The reaper is waited for by no one: it outlives the test binary, and init reaps it.
	if err = cmd.Process.Release(); err != nil {
		return nil, fmt.Errorf("release the reaper: %w", err)
	}
	for name, value := range map[string]string{runMarkEnv: mark, reaperPidEnv: strconv.Itoa(pid)} {
		if err = os.Setenv(name, value); err != nil {
			return nil, errors.Join(fmt.Errorf("set %s: %w", name, err), write.Close())
		}
	}
	return write, nil
}

// reap is the reaper's whole run, for the run marked mark: it waits until its stdin reaches EOF, then kills every
// process that carries the mark, scanning again until a scan finds none alive, so a process forked during a scan is
// killed by the next. It returns the exit code.
func reap(mark string) int {
	if _, err := io.Copy(io.Discard, os.Stdin); err != nil {
		fmt.Fprintf(os.Stderr, "reaper: read stdin: %v\n", err)
	}
	needle := []byte(runMarkEnv + "=" + mark + "\x00")
	for {
		found := 0
		for _, pid := range markedPids(needle) {
			found++
			// A process that ended since the scan is not an error.
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
		if found == 0 {
			return 0
		}
	}
}

// markedPids returns the pid of every live process, other than this one, whose environment holds needle. A zombie has
// ended, and a process whose environment cannot be read is not one of the run's, which all run as this user.
func markedPids(needle []byte) []int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	self := os.Getpid()
	var pids []int
	for _, entry := range entries {
		pid, convErr := strconv.Atoi(entry.Name())
		if convErr != nil || pid == self {
			continue
		}
		environ, readErr := os.ReadFile(filepath.Join("/proc", entry.Name(), "environ"))
		if readErr != nil || !bytes.Contains(append([]byte{0}, environ...), append([]byte{0}, needle...)) {
			continue
		}
		if processEnded(pid) {
			continue
		}
		pids = append(pids, pid)
	}
	return pids
}

// processEnded reports whether pid has no live process: none exists, or it is a zombie, which has ended and waits only
// for its parent to reap it.
func processEnded(pid int) bool {
	stat, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return true
	}
	// The state follows the command name, which is in parentheses and may itself hold spaces or parentheses.
	end := bytes.LastIndexByte(stat, ')')
	if end < 0 || end+2 >= len(stat) {
		return true
	}
	return strings.HasPrefix(string(stat[end+2:]), "Z")
}

// reaperHelperEnv names the file the reaper test's helper writes its pids to. Without it, the helper does nothing.
const reaperHelperEnv = "INTEGRATION_REAPER_HELPER_PIDS"

// TestReaperHelper is not a test. TestKilledTestBinaryEndsItsProcesses runs the test binary with reaperHelperEnv set,
// and this then starts an orreryd with a session that stalls in a turn under its holder, writes the pids of the
// orreryd, the holder, the fake claude and its own reaper to the file the variable names, and blocks until it is
// killed.
func TestReaperHelper(t *testing.T) {
	path := os.Getenv(reaperHelperEnv)
	if path == "" {
		return
	}
	f := newEngineFixture(t)
	daemon := f.startOrreryd(t, true)
	dir := f.sessionDir(t, "reaper")
	id := f.start(t, dir, stallPrompt)
	f.waitForUnfinishedText(t, id)
	pids, err := holder.ReadPids(filepath.Join(filepath.Dir(f.dbPath), "holders", id))
	if err != nil {
		t.Fatalf("read holder pids: %v", err)
	}
	line := fmt.Sprintf("%d %d %d %s\n", daemon.cmd.Process.Pid, pids.Holder, fakeClaudePID(t, dir),
		os.Getenv(reaperPidEnv))
	if err = exectest.Write(path, []byte(line), 0o600); err != nil {
		t.Fatalf("write pids: %v", err)
	}
	select {}
}

// TestKilledTestBinaryEndsItsProcesses proves that a test binary that dies by SIGKILL, as one go test's timeout ends
// does, leaves none of the processes its tests started. The helper run of the test binary starts an orreryd whose
// session runs under a holder with a fake claude stalled in its turn, and reports their pids and its reaper's. The
// helper is SIGKILLed once it has reported them, so it runs no cleanup. Its reaper's stdin then closes; the reaper kills
// every process of the helper's run and exits. Once the reaper has ended, the orreryd, the holder and the fake claude
// must all have ended too.
func TestKilledTestBinaryEndsItsProcesses(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "pids")
	// Every file and socket the helper makes lands under tmp, which the test removes once the helper's processes have
	// ended: the helper is killed before its own cleanup could remove them. tmp is short, as the fixture's socket
	// paths need.
	tmp, err := shortRoot()
	if err != nil {
		t.Fatalf("make the helper's temporary directory: %v", err)
	}
	t.Cleanup(func() {
		if rmErr := os.RemoveAll(tmp); rmErr != nil {
			t.Errorf("remove %s: %v", tmp, rmErr)
		}
	})
	helper := exec.Command(os.Args[0], "-test.run", "^TestReaperHelper$")
	helper.Env = append(os.Environ(), reaperHelperEnv+"="+path, "TMPDIR="+tmp, "TMUX_TMPDIR="+tmp)
	var stderr lockedBuffer
	helper.Stderr = &stderr
	waited, err := childproc.Start(helper)
	if err != nil {
		t.Fatalf("start the helper: %v", err)
	}
	ended, result := waittest.Closed(waited)
	var fields []string
	waittest.Until(t, ended, "the helper's pids", func() bool {
		raw, readErr := os.ReadFile(path)
		fields = strings.Fields(string(raw))
		return readErr == nil && len(fields) == 4
	})
	if err = helper.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatalf("kill the helper: %v", err)
	}
	<-result
	pids := make([]int, len(fields))
	for i, field := range fields {
		if pids[i], err = strconv.Atoi(field); err != nil {
			t.Fatalf("helper pid %q: %v; helper stderr %q", field, err, stderr.String())
		}
	}
	reaper := pids[3]
	waitEnded(t, reaper)
	for i, name := range []string{"orreryd", "holder", "fake claude"} {
		if !processEnded(pids[i]) {
			t.Errorf("%s %d outlived the killed test binary and its reaper", name, pids[i])
			_ = syscall.Kill(pids[i], syscall.SIGKILL)
		}
	}
}

// waitEnded waits until pid has ended (see processEnded). It has no deadline of its own: the process it waits on, the
// reaper, ends once its scan finds nothing left to kill.
func waitEnded(t *testing.T, pid int) {
	t.Helper()
	waittest.UntilIdle(t, nil, fmt.Sprintf("the end of process %d", pid), func() bool { return processEnded(pid) })
}
