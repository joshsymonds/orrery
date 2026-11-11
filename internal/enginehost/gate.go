package enginehost

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// gateRetry is how long runGate waits before it tries the locks again.
const gateRetry = time.Second

// gateWaitDelay is how long runGate waits for a gate's output to close after the gate has ended. A child
// that leaves the process group and keeps the output open would otherwise hold runGate past the limit.
const gateWaitDelay = time.Second

// gateOutputBytes is how much of a gate's combined stdout and stderr runGate keeps.
const gateOutputBytes = 8 * 1024

// errGateNoLock reports a gate that names no lock file to hold.
var errGateNoLock = errors.New("enginehost: gate names no lock")

// errGateNoCommand reports a gate that names no command to run.
var errGateNoCommand = errors.New("enginehost: gate names no command")

// gateRun describes one gate: the command, where it runs, how long it may run, the lock files it may
// hold, and the complete environment it runs in.
type gateRun struct {
	Argv    []string
	Dir     string
	Limit   time.Duration
	Locks   []string
	Environ []string
}

// gateResult is how a gate ended: its exit status, whether Limit killed it, the last bytes it printed,
// and the lock file it held.
type gateResult struct {
	Exit     int
	TimedOut bool
	Output   string
	Lock     string
}

// runGate holds the first free lock in run.Locks, runs run.Argv in run.Dir in its own process group
// with the TMUX and TMUX_PANE markers removed from run.Environ, and kills the whole group when run.Limit
// passes or ctx ends. Argv[0] is a path, absolute or relative to run.Dir; it is not looked up on PATH.
// The lock is released when the gate ends. It returns an error only when the gate cannot start or ctx
// ends first.
func runGate(ctx context.Context, run gateRun) (gateResult, error) {
	if len(run.Argv) == 0 {
		return gateResult{}, errGateNoCommand
	}
	lock, lockPath, err := gateAcquire(ctx, run.Locks)
	if err != nil {
		return gateResult{}, err
	}
	defer lock.Close()

	output := &gateTail{capacity: gateOutputBytes}
	cmd := &exec.Cmd{
		Path:        run.Argv[0],
		Args:        run.Argv,
		Dir:         run.Dir,
		Env:         gateEnviron(run.Environ),
		Stdout:      output,
		Stderr:      output,
		WaitDelay:   gateWaitDelay,
		SysProcAttr: &syscall.SysProcAttr{Setpgid: true},
	}
	if err = cmd.Start(); err != nil {
		return gateResult{}, fmt.Errorf("enginehost: start gate %s: %w", run.Argv[0], err)
	}
	pid := cmd.Process.Pid
	var waitErr error
	waited := make(chan struct{})
	go func() {
		waitErr = cmd.Wait()
		close(waited)
	}()

	limit := time.NewTimer(run.Limit)
	defer limit.Stop()
	timedOut := false
	var killErr error
	select {
	case <-waited:
	case <-limit.C:
		timedOut = true
		killErr = gateKillGroup(pid)
		<-waited
	case <-ctx.Done():
		killErr = gateKillGroup(pid)
		<-waited
		return gateResult{}, errors.Join(fmt.Errorf("enginehost: gate ended by its context: %w", ctx.Err()), killErr)
	}
	if killErr != nil {
		return gateResult{}, killErr
	}
	exit, err := gateExitCode(waitErr)
	if err != nil {
		return gateResult{}, err
	}
	return gateResult{Exit: exit, TimedOut: timedOut, Output: output.String(), Lock: lockPath}, nil
}

// gateAcquire returns the open, locked file of the first lock in paths that is free, and its path. It
// tries every lock in order, then waits gateRetry before trying again, until one frees or ctx ends.
func gateAcquire(ctx context.Context, paths []string) (*os.File, string, error) {
	if len(paths) == 0 {
		return nil, "", errGateNoLock
	}
	for {
		for _, path := range paths {
			file, held, err := gateTryLock(path)
			if err != nil {
				return nil, "", err
			}
			if held {
				return file, path, nil
			}
		}
		select {
		case <-ctx.Done():
			return nil, "", fmt.Errorf("enginehost: gate waited for a lock: %w", ctx.Err())
		case <-time.After(gateRetry):
		}
	}
}

// gateTryLock takes a non-blocking exclusive flock on path. held is false when another holder has it.
func gateTryLock(path string) (*os.File, bool, error) {
	file, err := os.OpenFile(filepath.Clean(path), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, false, fmt.Errorf("enginehost: open gate lock %s: %w", path, err)
	}
	if err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("enginehost: lock gate lock %s: %w", path, err)
	}
	return file, true, nil
}

// gateEnviron returns environ without the TMUX and TMUX_PANE markers.
func gateEnviron(environ []string) []string {
	kept := make([]string, 0, len(environ))
	for _, entry := range environ {
		name, _, _ := strings.Cut(entry, "=")
		if name == "TMUX" || name == "TMUX_PANE" {
			continue
		}
		kept = append(kept, entry)
	}
	return kept
}

// gateKillGroup kills every process in the group pgid. A group that already exited is not an error.
func gateKillGroup(pgid int) error {
	if err := syscall.Kill(-pgid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("enginehost: kill gate group %d: %w", pgid, err)
	}
	return nil
}

// gateExitCode returns the exit status of a finished gate. A gate killed by a signal reports -1.
func gateExitCode(waitErr error) (int, error) {
	if waitErr == nil {
		return 0, nil
	}
	if errors.Is(waitErr, exec.ErrWaitDelay) {
		// The gate exited with status 0 and a detached child kept its output open past gateWaitDelay.
		return 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(waitErr, &exitErr) {
		return exitErr.ExitCode(), nil
	}
	return 0, fmt.Errorf("enginehost: wait for gate: %w", waitErr)
}

// gateTail keeps the newest capacity bytes written to it. exec.Cmd calls Write from one goroutine at a
// time when stdout and stderr are the same writer, and String is read only after the gate has ended.
type gateTail struct {
	capacity int
	data     []byte
}

// Write appends data and drops the oldest bytes beyond capacity. It always reports the full length.
func (t *gateTail) Write(data []byte) (int, error) {
	t.data = append(t.data, data...)
	if over := len(t.data) - t.capacity; over > 0 {
		t.data = t.data[:copy(t.data, t.data[over:])]
	}
	return len(data), nil
}

// String returns the kept bytes in the order they were written.
func (t *gateTail) String() string {
	return string(t.data)
}
