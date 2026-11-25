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

	"github.com/joshsymonds/orrery/internal/keylines"
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
	// StdoutCapacity is the number of stdout bytes kept in a tail of their own, which StdoutTail returns. Zero leaves
	// stdout to the caller, who reads it from Stdout.
	StdoutCapacity int
	// StderrCapacity is the number of stderr bytes kept. Zero takes 64 KiB.
	StderrCapacity int
	// KeyLines collects the key lines of each stream kept in a tail, the stderr and, with StdoutCapacity, the stdout,
	// from the whole stream and not only its tail, as a keylines.Collector keeps them. KeyLines returns them.
	KeyLines bool
	// StopTimeout is how long Stop waits after the interrupt before the kill. Zero takes 2 s.
	StopTimeout time.Duration
}

// Process is a started command in its own process group.
type Process struct {
	cmd         *exec.Cmd
	stdin       *os.File
	stdout      io.ReadCloser
	stdoutTail  *tail // nil unless the stdout is kept in a tail
	stderr      *tail
	keyLines    *keylines.Collector // nil unless Config.KeyLines is set
	captures    []*capture
	stopTimeout time.Duration
	// stopTimer returns the channel that delivers once Stop has waited d after the interrupt. It is time.After; a test
	// in this package replaces it.
	stopTimer func(d time.Duration) <-chan time.Time
	done      chan struct{}
	waitErr   error
	stopOnce  sync.Once
	stopErr   error
}

// capture is one of the child's output pipes, copied into a tail until every writer closes the pipe. With lines set,
// the pipe's bytes are written to lines too, so its key lines are collected.
type capture struct {
	read  *os.File
	tail  *tail
	lines *keylines.Writer
	done  chan struct{}
	err   error // read by run only after done closes
}

// Start runs the configured binary in a new process group with piped stdin, and captures its stderr into a bounded
// tail. With StdoutCapacity its stdout is captured into a tail of its own too, and otherwise the caller reads it. With
// KeyLines the key lines of each captured stream are collected as well. The process is stopped with Stop.
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
	parentStdout, childStdout, err := os.Pipe()
	if err != nil {
		return nil, errors.Join(
			fmt.Errorf("proc: open stdout pipe: %w", err),
			closeFiles(childStdin, parentStdin),
		)
	}
	parentStderr, childStderr, err := os.Pipe()
	if err != nil {
		return nil, errors.Join(
			fmt.Errorf("proc: open stderr pipe: %w", err),
			closeFiles(childStdin, parentStdin, parentStdout, childStdout),
		)
	}

	cmd := &exec.Cmd{
		Path:        cfg.Path,
		Args:        append([]string{cfg.Path}, cfg.Args...),
		Dir:         cfg.Dir,
		Env:         cfg.Env,
		Stdin:       childStdin,
		Stdout:      childStdout,
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

	process := &Process{
		cmd:         cmd,
		stdin:       parentStdin,
		stdout:      parentStdout,
		stderr:      newTail(cfg.StderrCapacity),
		stopTimeout: cfg.StopTimeout,
		stopTimer:   time.After,
		done:        make(chan struct{}),
	}
	process.captures = []*capture{{read: parentStderr, tail: process.stderr, done: make(chan struct{})}}
	if cfg.StdoutCapacity > 0 {
		// The stdout is kept in its own tail, so the caller's stdout is an empty reader.
		process.stdoutTail = newTail(cfg.StdoutCapacity)
		process.stdout = io.NopCloser(strings.NewReader(""))
		process.captures = append(process.captures,
			&capture{read: parentStdout, tail: process.stdoutTail, done: make(chan struct{})})
	}
	if cfg.KeyLines {
		process.keyLines = &keylines.Collector{}
		for _, c := range process.captures {
			c.lines = process.keyLines.Writer()
		}
	}
	for _, c := range process.captures {
		go c.copy()
	}
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

// Stdout returns the read end of the child's stdout. The caller closes it. With StdoutCapacity it is already at EOF,
// as the output is kept in StdoutTail.
func (p *Process) Stdout() io.ReadCloser {
	return p.stdout
}

// StdoutTail returns the last bytes the child wrote to stdout, up to StdoutCapacity. It is empty without
// StdoutCapacity.
func (p *Process) StdoutTail() string {
	if p.stdoutTail == nil {
		return ""
	}
	return p.stdoutTail.String()
}

// Stderr returns the last bytes the child wrote to stderr, up to the configured capacity.
func (p *Process) Stderr() string {
	return p.stderr.String()
}

// KeyLines returns the key lines of the streams kept in tails, from the whole of each stream, in the order the child
// wrote them, as a keylines.Collector keeps them. Once Done has closed they hold every line the child wrote, its last
// line included when it ended without a newline. It is nil without Config.KeyLines, and when the child wrote no key
// line.
func (p *Process) KeyLines() []string {
	if p.keyLines == nil {
		return nil
	}
	return p.keyLines.Lines()
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
	select {
	case <-p.done:
		return interruptErr
	case <-p.stopTimer(p.stopTimeout):
	}
	killErr := signalGroup(pgid, syscall.SIGKILL)
	<-p.done
	return errors.Join(interruptErr, killErr)
}

// run reaps the child, kills whatever remains of its group, and closes Done once the output has drained.
func (p *Process) run() {
	waitErr := p.cmd.Wait()
	killErr := signalGroup(p.Pid(), syscall.SIGKILL)
	expired := make(chan struct{})
	timer := time.AfterFunc(p.stopTimeout, func() { close(expired) })
	defer timer.Stop()
	var unblockErr, copyErr error
	for _, pipe := range p.captures {
		select {
		case <-pipe.done:
		case <-expired:
			// A process that left the group still holds the pipe open. Closing the read end unblocks the copy.
			unblockErr = errors.Join(unblockErr, ignoreClosed(pipe.read.Close()))
			<-pipe.done
		}
		copyErr = errors.Join(copyErr, pipe.err)
	}
	p.waitErr = errors.Join(waitErr, killErr, unblockErr, copyErr)
	close(p.done)
}

// copy keeps the pipe's bytes in the tail, and writes them to lines when it is set, until every writer closes the pipe,
// then adds the line the pipe ended on without a newline to lines and closes the read end. Its error is read by run
// only after done closes.
func (c *capture) copy() {
	defer close(c.done)
	var into io.Writer = c.tail
	if c.lines != nil {
		into = io.MultiWriter(c.tail, c.lines)
	}
	_, copyErr := io.Copy(into, c.read)
	if c.lines != nil {
		c.lines.Flush()
	}
	c.err = errors.Join(ignoreClosed(copyErr), ignoreClosed(c.read.Close()))
}

// ignoreClosed drops the closed-file error that closing a read end causes. run and capture.copy both close
// that end, so the copy's read fails with it when run unblocks the copy, and whichever closes second gets it.
// It says nothing about how the child exited.
func ignoreClosed(err error) error {
	if errors.Is(err, os.ErrClosed) {
		return nil
	}
	return err
}

// signalGroup sends sig to every process in the group pgid. A group that no longer exists is not an
// error.
func signalGroup(pgid int, sig syscall.Signal) error {
	if err := syscall.Kill(-pgid, sig); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("proc: signal process group %d: %w", pgid, err)
	}
	return nil
}

// closeFiles closes every file and joins the errors.
func closeFiles(files ...*os.File) error {
	var closeErr error
	for _, file := range files {
		if err := file.Close(); err != nil {
			closeErr = errors.Join(closeErr, fmt.Errorf("proc: close pipe: %w", err))
		}
	}
	return closeErr
}
