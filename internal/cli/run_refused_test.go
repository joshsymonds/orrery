package cli_test

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/joshsymonds/orrery/internal/cli"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
)

// TestRunRefusedReclaimStopsCommandWithGrace breaks the grant's stream while CMD ignores SIGTERM, then has orreryd refuse
// the reclaim. CMD must get the stop's grace before SIGKILL, and the whole group must be gone before the run exits 75.
func TestRunRefusedReclaimStopsCommandWithGrace(t *testing.T) {
	ready := make(chan struct{})
	pools := &stopPools{steps: []scriptedStep{
		{before: []*v1.AcquireResponse{grantedWith("grant-stubborn", poolMembers("builds", "m1"))}, after: ready},
		{fail: connect.NewError(connect.CodeNotFound, errors.New("orreryd refused the grant"))},
	}}
	socket := serveStopPools(t, pools)
	stdinR := stdinPipe(t)
	r, w := stdoutPipe(t)
	lines := outputLines(r)
	var stderr bytes.Buffer
	graces := make(chan time.Duration, 4)
	fire := make(chan time.Time)
	cfg := stopConfig(t, socket, w, &stderr, stdinR)
	cfg.After = func(d time.Duration) <-chan time.Time {
		graces <- d
		return fire
	}
	status := runAsync(context.Background(), cfg)
	pid := readyPID(t, nextLine(t, lines))
	// CMD ignores SIGTERM and would run forever if the test failed before SIGKILL, so the group is killed at cleanup.
	t.Cleanup(func() { _ = syscall.Kill(-pid, syscall.SIGKILL) })
	close(ready)

	select {
	case d := <-graces:
		if d != stopGrace {
			t.Fatalf("the grace = %v, want %v", d, stopGrace)
		}
	case <-time.After(runTimeout):
		t.Fatal("the grace never started after orreryd refused the reclaim")
	}
	// CMD ignores SIGTERM, so it runs on while the grace has not passed on the test's clock.
	time.Sleep(stopWatch)
	if processExited(t, pid) {
		t.Fatal("CMD exited before the grace passed")
	}
	close(fire)
	waitGone(t, pid)
	if got := awaitStatus(t, status); got != cli.ExitNoGrant {
		t.Errorf("RunWithGrant = %d, want ExitNoGrant %d", got, cli.ExitNoGrant)
	}
	if err := syscall.Kill(-pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Errorf("CMD's group is not empty after the run: kill(-%d, 0) = %v, want ESRCH", pid, err)
	}
	_ = w.Close()
	if got := remainingLines(lines); slices.ContainsFunc(got, isReadyLine) {
		t.Errorf("output = %q, want no rerun after the refused reclaim", got)
	}
	if !strings.Contains(stderr.String(), "refused") {
		t.Errorf("stderr = %q, want it to say orreryd refused the reclaim", stderr.String())
	}
}
