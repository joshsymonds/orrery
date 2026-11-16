package childproc_test

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/joshsymonds/orrery/internal/childproc"
	"github.com/joshsymonds/orrery/internal/exectest"
)

// helperEnv picks how TestHelperProcess starts its child when the test binary re-runs as the parent.
const helperEnv = "ORRERY_CHILDPROC_TEST_HELPER"

// TestHelperProcess is not a test. The tests re-run the test binary with helperEnv set, as the parent of a sleep that
// it starts through childproc.Start, or through a plain exec.Cmd.Start when helperEnv is "plain". The parent prints the
// sleep's pid and then reads its stdin, which the test holds open, until it is killed. Without the variable it does
// nothing.
func TestHelperProcess(_ *testing.T) {
	mode := os.Getenv(helperEnv)
	if mode == "" {
		return
	}
	sleep := exec.Command("sleep", "1000")
	var err error
	if mode == "plain" {
		err = sleep.Start()
	} else {
		_, err = childproc.Start(sleep)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stdout, sleep.Process.Pid)
	_, _ = io.Copy(io.Discard, os.Stdin)
	os.Exit(0)
}

// TestStartKillsChildWhenParentIsKilled SIGKILLs a parent that started a sleep, then reaps the parent. The kernel
// sends a child its parent-death signal while the parent exits, before the parent can be reaped, so once the reap
// returns, a child started through childproc.Start is gone, a zombie, or holds a pending SIGKILL. A child of a plain
// exec.Cmd.Start is none of those: it runs on.
func TestStartKillsChildWhenParentIsKilled(t *testing.T) {
	t.Parallel()
	tests := []struct {
		mode     string
		wantDies bool
	}{
		{mode: "childproc", wantDies: true},
		{mode: "plain", wantDies: false},
	}
	for _, tc := range tests {
		t.Run(tc.mode, func(t *testing.T) {
			t.Parallel()
			child := startParentAndKill(t, tc.mode)
			if dies := exectest.KilledOrGone(t, child); dies != tc.wantDies {
				t.Fatalf("after its parent was SIGKILLed, the %s child is killed or gone = %t, want %t",
					tc.mode, dies, tc.wantDies)
			}
		})
	}
}

// startParentAndKill runs the test binary as the parent of a sleep started in mode, SIGKILLs the parent once it has
// printed the sleep's pid, reaps it, and returns that pid. The sleep is killed at cleanup whatever the test found.
func startParentAndKill(t *testing.T, mode string) int {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("find the test binary: %v", err)
	}
	parent := exec.Command(exe, "-test.run=^TestHelperProcess$")
	parent.Env = append(os.Environ(), helperEnv+"="+mode)
	parent.Stderr = os.Stderr
	stdin, err := parent.StdinPipe()
	if err != nil {
		t.Fatalf("parent stdin: %v", err)
	}
	t.Cleanup(func() { _ = stdin.Close() })
	out, err := parent.StdoutPipe()
	if err != nil {
		t.Fatalf("parent stdout: %v", err)
	}
	if err = parent.Start(); err != nil {
		t.Fatalf("start parent: %v", err)
	}
	t.Cleanup(func() { _ = parent.Process.Kill() })
	// The read ends with the pid line, or at EOF when the parent exits first, which fails the parse.
	line, _ := bufio.NewReader(out).ReadString('\n')
	child, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatalf("parent printed %q, want the sleep's pid: %v", line, err)
	}
	t.Cleanup(func() { _ = syscall.Kill(child, syscall.SIGKILL) })
	if err = parent.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatalf("SIGKILL parent: %v", err)
	}
	var exitErr *exec.ExitError
	if err = parent.Wait(); !errors.As(err, &exitErr) {
		t.Fatalf("reap parent: %v, want its SIGKILL exit", err)
	}
	return child
}
