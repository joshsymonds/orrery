package proc

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	defaultStderrCapacity = 64 << 10
	defaultStopTimeout    = 2 * time.Second
)

// ErrPathNotAbsolute marks a configured binary path that is not absolute, so the binary would be
// resolved through PATH.
var ErrPathNotAbsolute = errors.New("proc: binary path must be absolute")

// Config describes the command that Start runs.
type Config struct {
	// Path is the absolute path of the binary.
	Path string
	// Args follows the binary's own path as its argument vector.
	Args []string
	// Dir is the working directory, or the current one when empty.
	Dir string
	// Env replaces the environment when non-nil, and is inherited when nil.
	Env []string
	// MergeOutput writes the child's stdout into the same tail as its stderr, in the order the child
	// writes them. Stdout then reads EOF at once.
	MergeOutput bool
	// StderrCapacity is the number of stderr bytes kept. Zero takes 64 KiB.
	StderrCapacity int
	// StopTimeout is how long Stop waits after the interrupt before the kill. Zero takes 2 s.
	StopTimeout time.Duration
}

// Process is a started command in its own process group.
type Process struct {
	cmd         *exec.Cmd
	stdin       *os.File
	stdout      io.ReadCloser
	stderr      *tail
	stderrRead  *os.File
	stderrDone  chan struct{}
	copyErr     error
	stopTimeout time.Duration
	done        chan struct{}
	waitErr     error
	stopOnce    sync.Once
	stopErr     error
}

// Start runs the configured binary in a new process group with piped stdin and stdout, and captures
// its stderr into a bounded tail. With MergeOutput the stdout is captured into that tail too. The
// process is stopped with Stop.
func Start(cfg Config) (*Process, error) {
	if !filepath.IsAbs(cfg.Path) {
		return nil, ErrPathNotAbsolute
	}
	if cfg.StderrCapacity <= 0 {
		cfg.StderrCapacity = defaultStderrCapacity
	}
	if cfg.StopTimeout <= 0 {
		cfg.StopTimeout = defaultStopTimeout
	}

	childStdin, parentStdin, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("proc: open stdin pipe: %w", err)
	}
	// Without MergeOutput the child's stdout has its own pipe. With it, the child's stdout is the
	// stderr pipe, so both streams share one write end and reach the tail in write order.
	var parentStdout, childStdout *os.File
	if !cfg.MergeOutput {
		parentStdout, childStdout, err = os.Pipe()
		if err != nil {
			return nil, errors.Join(
				fmt.Errorf("proc: open stdout pipe: %w", err),
				closeFiles(childStdin, parentStdin),
			)
		}
	}
	parentStderr, childStderr, err := os.Pipe()
	if err != nil {
		return nil, errors.Join(
			fmt.Errorf("proc: open stderr pipe: %w", err),
			closeFiles(childStdin, parentStdin, parentStdout, childStdout),
		)
	}
	childOut := childStderr
	if !cfg.MergeOutput {
		childOut = childStdout
	}

	cmd := &exec.Cmd{
		Path:        cfg.Path,
		Args:        append([]string{cfg.Path}, cfg.Args...),
		Dir:         cfg.Dir,
		Env:         cfg.Env,
		Stdin:       childStdin,
		Stdout:      childOut,
		Stderr:      childStderr,
		SysProcAttr: &syscall.SysProcAttr{Setpgid: true},
	}
	if err = cmd.Start(); err != nil {
		return nil, errors.Join(
			fmt.Errorf("proc: start %s: %w", cfg.Path, err),
			closeFiles(childStdin, parentStdin, parentStdout, childStdout, parentStderr, childStderr),
		)
	}
	if err = closeFiles(childStdin, childStdout, childStderr); err != nil {
		pgid := cmd.Process.Pid
		return nil, errors.Join(
			fmt.Errorf("proc: close child pipe ends: %w", err),
			signalGroup(pgid, syscall.SIGKILL),
			cmd.Wait(),
			closeFiles(parentStdin, parentStdout, parentStderr),
		)
	}

	// With MergeOutput the output is in the stderr tail, so the caller's stdout is an empty reader.
	stdout := io.NopCloser(strings.NewReader(""))
	if !cfg.MergeOutput {
		stdout = parentStdout
	}

	process := &Process{
		cmd:         cmd,
		stdin:       parentStdin,
		stdout:      stdout,
		stderr:      newTail(cfg.StderrCapacity),
		stderrRead:  parentStderr,
		stderrDone:  make(chan struct{}),
		stopTimeout: cfg.StopTimeout,
		done:        make(chan struct{}),
	}
	go process.copyStderr()
	go process.run()
	return process, nil
}

// Pid returns the process id, which is also the process group id.
func (p *Process) Pid() int {
	return p.cmd.Process.Pid
}

// Stdin returns the write end of the child's stdin. The caller closes it.
func (p *Process) Stdin() io.WriteCloser {
	return p.stdin
}

// Stdout returns the read end of the child's stdout. The caller closes it. Under MergeOutput it is
// already at EOF.
func (p *Process) Stdout() io.ReadCloser {
	return p.stdout
}

// Stderr returns the last bytes the child wrote to stderr, up to the configured capacity. Under
// MergeOutput it holds the child's stdout and stderr, in write order.
func (p *Process) Stderr() string {
	return p.stderr.String()
}

// Done returns a channel that closes once the process is reaped.
func (p *Process) Done() <-chan struct{} {
	return p.done
}

// Wait blocks until Done closes and returns the exit error of the process, if any.
func (p *Process) Wait() error {
	<-p.done
	return p.waitErr
}

// Stop interrupts the process group and kills it if it has not exited after StopTimeout. It is
// idempotent and returns nil after a clean interrupt or a kill.
func (p *Process) Stop() error {
	p.stopOnce.Do(func() { p.stopErr = p.stop() })
	return p.stopErr
}

func (p *Process) stop() error {
	select {
	case <-p.done:
		return nil
	default:
	}
	pgid := p.Pid()
	interruptErr := signalGroup(pgid, syscall.SIGINT)
	timer := time.NewTimer(p.stopTimeout)
	defer timer.Stop()
	select {
	case <-p.done:
		return interruptErr
	case <-timer.C:
	}
	killErr := signalGroup(pgid, syscall.SIGKILL)
	<-p.done
	return errors.Join(interruptErr, killErr)
}

// run reaps the child, kills whatever remains of its group, and closes Done once the output has drained.
func (p *Process) run() {
	waitErr := p.cmd.Wait()
	killErr := signalGroup(p.Pid(), syscall.SIGKILL)
	var unblockErr error
	timer := time.NewTimer(p.stopTimeout)
	defer timer.Stop()
	select {
	case <-p.stderrDone:
	case <-timer.C:
		// A process that left the group still holds the stderr pipe open. Closing the read end
		// unblocks the copy.
		unblockErr = p.stderrRead.Close()
		<-p.stderrDone
	}
	p.waitErr = errors.Join(waitErr, killErr, unblockErr, p.copyErr)
	close(p.done)
}

// copyStderr keeps the child's stderr in the bounded tail until every writer closes the pipe, then
// closes the read end. Its error is read by run only after stderrDone closes.
func (p *Process) copyStderr() {
	defer close(p.stderrDone)
	_, copyErr := io.Copy(p.stderr, p.stderrRead)
	p.copyErr = errors.Join(copyErr, p.stderrRead.Close())
}

// signalGroup sends sig to every process in the group pgid. A group that no longer exists is not an
// error.
func signalGroup(pgid int, sig syscall.Signal) error {
	if err := syscall.Kill(-pgid, sig); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("proc: signal process group %d: %w", pgid, err)
	}
	return nil
}

// closeFiles closes every file and joins the errors. A nil file was never opened, as the stdout pipe is
// not under MergeOutput, and is skipped.
func closeFiles(files ...*os.File) error {
	var closeErr error
	for _, file := range files {
		if file == nil {
			continue
		}
		if err := file.Close(); err != nil {
			closeErr = errors.Join(closeErr, fmt.Errorf("proc: close pipe: %w", err))
		}
	}
	return closeErr
}
