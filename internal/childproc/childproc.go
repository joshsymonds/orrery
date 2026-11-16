// Package childproc starts the child processes that must not outlive the process that starts them. A process that
// dies by SIGKILL runs no cleanup, so only the kernel can end its children: Start asks it to.
package childproc

import (
	"os/exec"
	"runtime"
	"syscall"
)

// Start starts cmd so that the kernel sends cmd SIGKILL when the process that started it dies, however it dies. It
// sets cmd.SysProcAttr.Pdeathsig and keeps cmd's other process attributes. Linux sends the signal when the thread that
// forked the child exits, not only the process, so cmd is started and waited on by a goroutine locked to its thread
// until cmd has exited. The returned channel receives cmd.Wait's error once cmd has exited. A cmd that does not start
// returns the error of cmd.Start, and no channel.
func Start(cmd *exec.Cmd) (<-chan error, error) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Pdeathsig = syscall.SIGKILL
	started := make(chan error, 1)
	waited := make(chan error, 1)
	go func() {
		// The goroutine ends locked, so its thread exits with it, and only once cmd has been waited for.
		runtime.LockOSThread()
		if err := cmd.Start(); err != nil {
			started <- err
			return
		}
		started <- nil
		waited <- cmd.Wait()
	}()
	if err := <-started; err != nil {
		return nil, err
	}
	return waited, nil
}

// Run is Start, then the wait for cmd: it returns the error of cmd.Start or of cmd.Wait.
func Run(cmd *exec.Cmd) error {
	waited, err := Start(cmd)
	if err != nil {
		return err
	}
	return <-waited
}
