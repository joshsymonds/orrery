package proc

import (
	"errors"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// leaverStart starts a sleep in its own session, with stdout redirected by redirect, so the sleep
// inherits whatever the child's stderr is. It records the sleep's pid in the file named by $1 and waits
// for that file before the child exits. The child must not exit before the leaver has called setsid, or
// run's group kill takes the leaver down with the child.
func leaverStart(redirect string) string {
	return `setsid sh -c 'echo $$ > "$1"; exec sleep 60' sh "$1"` + redirect + " &\n" +
		"while [ ! -s \"$1\" ]; do sleep 0.01; done\n"
}

// A group leaver that holds the output pipe makes run close the read end after StopTimeout. The
// copy then fails on that close, and Wait must still report the child's own exit.
func TestWaitReportsChildExitWhenGroupLeaverHoldsOutput(t *testing.T) {
	const stopTimeout = 300 * time.Millisecond
	tests := []struct {
		name        string
		cfg         Config
		body        string
		wantStatus  int
		wantSuccess bool
	}{
		{
			name:        "merged output exit 0",
			cfg:         Config{MergeOutput: true, StopTimeout: stopTimeout},
			body:        leaverStart("") + "echo ready\nexit 0\n",
			wantSuccess: true,
		},
		{
			name:       "merged output exit 5",
			cfg:        Config{MergeOutput: true, StopTimeout: stopTimeout},
			body:       leaverStart("") + "echo ready\nexit 5\n",
			wantStatus: 5,
		},
		{
			name:        "stderr held exit 0",
			cfg:         Config{StopTimeout: stopTimeout},
			body:        leaverStart(" >/dev/null") + "echo ready >&2\nexit 0\n",
			wantSuccess: true,
		},
		{
			name:       "stderr held exit 5",
			cfg:        Config{StopTimeout: stopTimeout},
			body:       leaverStart(" >/dev/null") + "echo ready >&2\nexit 5\n",
			wantStatus: 5,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pidFile := filepath.Join(t.TempDir(), "holder.pid")
			p := startWith(t, tt.cfg, tt.body, pidFile)
			select {
			case <-p.Done():
			case <-time.After(stopTimeout + goneTimeout):
				t.Fatalf("Done not closed within %v while a group leaver holds the output", stopTimeout+goneTimeout)
			}
			holder := readPID(t, pidFile)
			t.Cleanup(func() {
				if err := syscall.Kill(holder, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
					t.Errorf("kill holder %d: %v", holder, err)
				}
			})
			if err := syscall.Kill(holder, 0); err != nil {
				t.Fatalf("holder %d is gone after Done, so the test never held the output open: %v", holder, err)
			}
			err := p.Wait()
			if tt.wantSuccess {
				if err != nil {
					t.Errorf("Wait = %v, want nil for a child that exited 0", err)
				}
				return
			}
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() != tt.wantStatus {
				t.Errorf("Wait = %v, want exit status %d", err, tt.wantStatus)
			}
		})
	}
}
