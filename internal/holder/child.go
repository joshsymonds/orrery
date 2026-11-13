package holder

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/joshsymonds/orrery/internal/engine/proc"
)

// DefaultGrace is how long a stopped child may run after SIGTERM before SIGKILL.
const DefaultGrace = 5 * time.Second

// ChildConfig describes the child a holder runs.
type ChildConfig struct {
	// Path is the absolute path of the binary.
	Path string
	// Args follows the binary's own path as its argument vector.
	Args []string
	// Dir is the working directory, or the holder's when empty.
	Dir string
	// Grace is how long Stop waits after SIGTERM before SIGKILL. Zero takes DefaultGrace.
	Grace time.Duration
	// Clock times the grace. Nil takes SystemClock{}.
	Clock Clock
}

// Child is the holder's child: a process group whose stdout is spooled and whose stdin the controller writes.
type Child struct {
	proc  *proc.Process
	spool *Spool
	clock Clock
	grace time.Duration
	done  chan struct{}

	stopOnce sync.Once

	mu    sync.Mutex
	fault error // a failure of the holder's own pump or signals, reported in Exit
}

// StartChild starts the child in its own process group with its stdout spooled into spool.
func StartChild(cfg ChildConfig, spool *Spool) (*Child, error) {
	if cfg.Grace == 0 {
		cfg.Grace = DefaultGrace
	}
	if cfg.Clock == nil {
		cfg.Clock = SystemClock{}
	}
	started, err := proc.Start(proc.Config{Path: cfg.Path, Args: cfg.Args, Dir: cfg.Dir})
	if err != nil {
		return nil, fmt.Errorf("holder: start child: %w", err)
	}
	child := &Child{
		proc:  started,
		spool: spool,
		clock: cfg.Clock,
		grace: cfg.Grace,
		done:  make(chan struct{}),
	}
	go child.pump()
	return child, nil
}

// Write writes p to the child's stdin.
func (c *Child) Write(p []byte) error {
	if _, err := c.proc.Stdin().Write(p); err != nil {
		return fmt.Errorf("holder: write child stdin: %w", err)
	}
	return nil
}

// Pid returns the child's process id, which is also its process group id.
func (c *Child) Pid() int {
	return c.proc.Pid()
}

// Stop sends SIGTERM to the child's process group and returns at once. Unless the child is reaped first, SIGKILL
// follows once Grace has passed on the clock. Stop is idempotent, and it signals nothing once the child is reaped.
func (c *Child) Stop() {
	c.stopOnce.Do(func() {
		select {
		case <-c.proc.Done():
			return
		default:
		}
		c.signal(syscall.SIGTERM)
		go c.killAfterGrace()
	})
}

// Done returns a channel that closes once the child is reaped and every stdout byte it wrote is spooled.
func (c *Child) Done() <-chan struct{} {
	return c.done
}

// Exit returns the child's exit record. It waits for Done. Code is the exit status, or -1 when a signal ended the
// child, with Signal the signal's name. A wait error with no exit status, or a fault of the holder, is the last line
// of Stderr.
func (c *Child) Exit(reason string) Exit {
	<-c.done
	exit := Exit{Reason: reason, StdoutEnd: c.spool.End()}
	waitErr := c.proc.Wait()
	var exitErr *exec.ExitError
	switch {
	case errors.As(waitErr, &exitErr):
		exit.Code, exit.Signal = exitStatus(exitErr)
		waitErr = nil
	case waitErr != nil:
		exit.Code = -1
	}
	c.mu.Lock()
	faults := errors.Join(waitErr, c.fault)
	c.mu.Unlock()
	exit.Stderr = stderrWith(c.proc.Stderr(), faults)
	return exit
}

// killAfterGrace sends SIGKILL to the child's group once Grace has passed on the clock, unless the child is reaped
// first.
func (c *Child) killAfterGrace() {
	select {
	case <-c.clock.After(c.grace):
		c.signal(syscall.SIGKILL)
	case <-c.proc.Done():
	}
}

// pump copies the child's stdout into the spool, then closes the child's pipes once it is reaped and closes Done. A
// failure stops the child and is reported in Exit.
func (c *Child) pump() {
	reaped := c.proc.Done()
	if err := c.copyStdout(reaped); err != nil {
		c.fail(err)
		c.Stop()
	}
	<-reaped
	if err := errors.Join(c.proc.Stdout().Close(), c.proc.Stdin().Close()); err != nil {
		c.fail(fmt.Errorf("holder: close child pipes: %w", err))
	}
	close(c.done)
}

// copyStdout copies the child's stdout into the spool until EOF. While reaped is open it reads no further than the
// spool's room, so the spool never refuses an append. Once reaped closes it seals the spool and reads on, so the bytes
// the child left in its pipe are spooled.
func (c *Child) copyStdout(reaped <-chan struct{}) error {
	stdout := c.proc.Stdout()
	buf := make([]byte, ChunkBytes)
	sealed := false
	for {
		size := int64(ChunkBytes)
		if !sealed {
			// Changed is taken before Room, so an Ack between the two is never missed.
			changed := c.spool.Changed()
			size = min(c.spool.Room(), ChunkBytes)
			if size == 0 {
				select {
				case <-changed:
				case <-reaped:
					c.spool.Seal()
					sealed = true
				}
				continue
			}
		}
		n, err := stdout.Read(buf[:size])
		if n > 0 {
			if appendErr := c.spool.Append(buf[:n]); appendErr != nil {
				return fmt.Errorf("holder: spool child stdout: %w", appendErr)
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("holder: read child stdout: %w", err)
		}
	}
}

// signal sends sig to the child's process group. A group that is already gone is not an error.
func (c *Child) signal(sig syscall.Signal) {
	if err := syscall.Kill(-c.Pid(), sig); err != nil && !errors.Is(err, syscall.ESRCH) {
		c.fail(fmt.Errorf("holder: signal child group %d with %v: %w", c.Pid(), sig, err))
	}
}

// fail records err as a fault of the holder, reported in Exit.
func (c *Child) fail(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.fault = errors.Join(c.fault, err)
}

// exitStatus returns the exit code of an exited child, or -1 and the name of the signal that ended it.
func exitStatus(exitErr *exec.ExitError) (int, string) {
	if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return -1, status.Signal().String()
	}
	return exitErr.ExitCode(), ""
}

// stderrWith returns the stderr tail without its trailing newlines, with err as the last line when there is one.
func stderrWith(tail string, err error) string {
	tail = strings.TrimRight(tail, "\n")
	if err == nil {
		return tail
	}
	if tail == "" {
		return err.Error()
	}
	return tail + "\n" + err.Error()
}
