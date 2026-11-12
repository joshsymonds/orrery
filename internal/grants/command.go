package grants

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"time"
)

// Outcome is how a command ended.
type Outcome int

// Outcomes of RunCommand. Succeeded is exit status 0. Exited is any other exit, including death by a signal.
// Failed is a command that did not start, or that RunCommand killed at its limit or when its context ended.
const (
	Succeeded Outcome = iota
	Exited
	Failed
)

// errCommandNoArgv reports a command that names no argv to run.
var errCommandNoArgv = errors.New("grants: command names no argv")

// RunCommand runs argv without a shell: argv[0] is found on this process's PATH and the rest are its arguments.
// The command runs in its own process group, with env as its complete environment, no stdin, and its output
// discarded. It returns Succeeded for exit status 0 and Exited for any other exit. It returns Failed when the
// command does not start, or when it passes limit or ctx ends first; then it kills the whole group, grandchildren
// included, before it returns. The error is nil exactly when the outcome is Succeeded, and otherwise names the cause.
func RunCommand(ctx context.Context, argv []string, limit time.Duration, env []string) (Outcome, error) {
	if len(argv) == 0 {
		return Failed, errCommandNoArgv
	}
	path, err := exec.LookPath(argv[0])
	if err != nil {
		return Failed, fmt.Errorf("grants: find %s: %w", argv[0], err)
	}
	cmd := &exec.Cmd{
		Path: path,
		Args: argv,
		// A nil Env would give the command this process's environment, so copy env even when it is empty.
		Env:         append([]string{}, env...),
		SysProcAttr: &syscall.SysProcAttr{Setpgid: true},
	}
	if err = cmd.Start(); err != nil {
		return Failed, fmt.Errorf("grants: start %s: %w", argv[0], err)
	}
	pid := cmd.Process.Pid
	var waitErr error
	waited := make(chan struct{})
	go func() {
		waitErr = cmd.Wait()
		close(waited)
	}()

	timer := time.NewTimer(limit)
	defer timer.Stop()
	select {
	case <-waited:
	case <-timer.C:
		killErr := commandKillGroup(pid)
		<-waited
		return Failed, errors.Join(fmt.Errorf("grants: command %s passed its limit %s", argv[0], limit), killErr)
	case <-ctx.Done():
		killErr := commandKillGroup(pid)
		<-waited
		return Failed, errors.Join(
			fmt.Errorf("grants: command %s ended by its context: %w", argv[0], ctx.Err()), killErr)
	}
	if waitErr == nil {
		return Succeeded, nil
	}
	var exitErr *exec.ExitError
	if errors.As(waitErr, &exitErr) {
		return Exited, fmt.Errorf("grants: command %s: %w", argv[0], exitErr)
	}
	return Failed, fmt.Errorf("grants: wait for command %s: %w", argv[0], waitErr)
}

// commandKillGroup kills every process in the group pgid. A group that already exited is not an error.
func commandKillGroup(pgid int) error {
	if err := syscall.Kill(-pgid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("grants: kill command group %d: %w", pgid, err)
	}
	return nil
}
