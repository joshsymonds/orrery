package exectest

import (
	"errors"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// execMark is an invented environment entry the processes of these tests run with.
const execMark = "EXECTEST_MARK=synthetic"

// recorder is a testing.TB whose Fatalf panics with the recorder instead of ending the test, so run can tell a failure.
type recorder struct {
	testing.TB
}

func (r *recorder) Helper() {}

func (r *recorder) Fatalf(string, ...any) {
	panic(r)
}

// run calls wait and returns whether it failed r.
func (r *recorder) run(wait func()) (failed bool) {
	defer func() {
		if got := recover(); got != nil {
			if got != r {
				panic(got)
			}
			failed = true
		}
	}()
	wait()
	return false
}

// start starts cmd with execMark as its environment. The process is killed and waited for when t ends.
func start(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	cmd.Env = []string{execMark}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start %s: %v", cmd.Path, err)
	}
	t.Cleanup(func() {
		if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			t.Errorf("kill %s: %v", cmd.Path, err)
		}
		// The process ends by the kill, or ended by itself, so only a failure to wait is reported.
		var exit *exec.ExitError
		if err := cmd.Wait(); err != nil && !errors.As(err, &exit) {
			t.Errorf("wait for %s: %v", cmd.Path, err)
		}
	})
}

func TestAwaitExecReturnsOnceTheEnvironmentReads(t *testing.T) {
	t.Parallel()
	cmd := exec.Command("cat")
	wait := AwaitExec(t, cmd)
	start(t, cmd)
	wait()
	data, err := os.ReadFile("/proc/" + strconv.Itoa(cmd.Process.Pid) + "/environ")
	if err != nil {
		t.Fatalf("read the environment of %d: %v", cmd.Process.Pid, err)
	}
	if !slices.Contains(strings.Split(string(data), "\x00"), execMark) {
		t.Fatalf("environment of %d is %q once the wait returned, want it to hold %q", cmd.Process.Pid, data, execMark)
	}
}

func TestAwaitExecFailsWhenTheProcessEndsFirst(t *testing.T) {
	t.Parallel()
	cmd := exec.Command("true")
	r := &recorder{TB: t}
	wait := AwaitExec(r, cmd)
	start(t, cmd)
	if !r.run(wait) {
		t.Fatal("the wait returned though the process ended without writing the line back")
	}
}
