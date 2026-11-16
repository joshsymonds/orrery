package open

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/exectest"
)

// intentHelperEnv, set to 1, makes TestRunClaudeHelperProcess run when the test binary re-runs as its helper.
const intentHelperEnv = "ORRERY_OPEN_TEST_INTENT_HELPER"

// TestRunClaudeHelperProcess is not a test. TestRunClaudeDiesWithItsCaller re-runs the test binary with intentHelperEnv
// set and the fake claude's directory first on PATH, and the helper runs runClaude on the claude PATH finds until it is
// killed. Without the variable it does nothing.
func TestRunClaudeHelperProcess(_ *testing.T) {
	if os.Getenv(intentHelperEnv) != "1" {
		return
	}
	_, _ = runClaude(context.Background(), "claude", "example stdin", time.Hour)
	syscall.Exit(0)
}

// TestRunClaudeDiesWithItsCaller SIGKILLs a process whose intent run is held, as a picker dies with its popup, and
// requires the run gone or dying once the caller is reaped: the kernel sends the run its parent-death signal while the
// caller exits. Without it, a held run outlives its picker and writes into its directory later.
func TestRunClaudeDiesWithItsCaller(t *testing.T) {
	claude := installClaude(t, "pane d1\n", 0)
	writeFile(t, filepath.Join(filepath.Dir(claude), "hold"), "")
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("find the test binary: %v", err)
	}
	helper := exec.Command(exe, "-test.run=^TestRunClaudeHelperProcess$")
	helper.Env = append(os.Environ(), intentHelperEnv+"=1",
		"PATH="+filepath.Dir(claude)+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err = helper.Start(); err != nil {
		t.Fatalf("start helper: %v", err)
	}
	t.Cleanup(func() { _ = helper.Process.Kill() })
	exited := make(chan error, 1)
	go func() { exited <- helper.Wait() }()

	run := heldRunOf(t, helper.Process.Pid, filepath.Dir(claude), exited)
	t.Cleanup(func() { _ = syscall.Kill(run, syscall.SIGKILL) })
	if err = helper.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatalf("SIGKILL helper: %v", err)
	}
	<-exited
	if !exectest.KilledOrGone(t, run) {
		t.Fatalf("intent run %d outlived its SIGKILLed caller", run)
	}
}

// heldRunOf waits until the fake claude in dir that pid's runClaude started has recorded its run, and returns that
// child's pid. A child is listed under pid as soon as it is forked, before it asks for its parent-death signal and
// execs, so the wait is for the run's args file, which only the exec'd fake writes. It has no deadline of its own, and
// fails at once when pid exits first. A child is listed under the thread that forked it, so every thread's list is read.
func heldRunOf(t *testing.T, pid int, dir string, exited <-chan error) int {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		recorded, err := filepath.Glob(filepath.Join(dir, "calls", "run.*", "args"))
		if err != nil {
			t.Fatalf("glob runs in %s: %v", dir, err)
		}
		if child, ok := childOf(t, pid); ok && len(recorded) > 0 {
			return child
		}
		select {
		case err := <-exited:
			t.Fatalf("helper %d exited before its intent run started: %v", pid, err)
		case <-ticker.C:
		}
	}
}

// childOf returns a child of process pid, if it has one.
func childOf(t *testing.T, pid int) (int, bool) {
	t.Helper()
	lists, err := filepath.Glob(fmt.Sprintf("/proc/%d/task/*/children", pid))
	if err != nil {
		t.Fatalf("glob children of %d: %v", pid, err)
	}
	for _, list := range lists {
		raw, readErr := os.ReadFile(list)
		if errors.Is(readErr, os.ErrNotExist) {
			continue
		}
		if readErr != nil {
			t.Fatalf("read %s: %v", list, readErr)
		}
		if fields := strings.Fields(string(raw)); len(fields) > 0 {
			child, convErr := strconv.Atoi(fields[0])
			if convErr != nil {
				t.Fatalf("child pid %q in %s: %v", fields[0], list, convErr)
			}
			return child, true
		}
	}
	return 0, false
}
