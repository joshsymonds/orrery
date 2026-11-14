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

// gateTailBytes is the newest stdout and stderr a gate child keeps in its exit record.
const gateTailBytes = 8 << 10

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
	// GateLimit makes the child a gate when positive: its stdin is closed at start, its stdout and stderr share one
	// newest-8-KiB tail carried in the exit record's Stderr, and nothing is spooled. Once GateLimit has passed on
	// Clock from the child's start, the child is stopped as Stop stops it and ends with ReasonLimit. Zero takes no
	// limit.
	GateLimit time.Duration
	// Clock times the grace and the gate limit. Nil takes SystemClock{}.
	Clock Clock
}

// Child is the holder's child: a process group whose stdout is spooled and whose stdin the controller writes.
type Child struct {
	proc  *proc.Process
	spool *Spool
	clock Clock
	grace time.Duration
	gate  bool
	done  chan struct{}

	stopOnce sync.Once

	mu      sync.Mutex
	fault   error // a failure of the holder's own pump or signals, reported in Exit
	limited bool  // the gate limit passed while the child ran, so Exit reports ReasonLimit
}

// StartChild starts the child in its own process group with its stdout spooled into spool. A positive GateLimit makes
// the child a gate, which spools nothing and keeps its output in the exit record's tail.
func StartChild(cfg ChildConfig, spool *Spool) (*Child, error) {
	if cfg.Grace == 0 {
		cfg.Grace = DefaultGrace
	}
	if cfg.Clock == nil {
		cfg.Clock = SystemClock{}
	}
	gate := cfg.GateLimit > 0
	procCfg := proc.Config{Path: cfg.Path, Args: cfg.Args, Dir: cfg.Dir}
	if gate {
		procCfg.MergeOutput = true
		procCfg.StderrCapacity = gateTailBytes
	}
	started, err := proc.Start(procCfg)
	if err != nil {
		return nil, fmt.Errorf("holder: start child: %w", err)
	}
	if gate {
		if closeErr := started.Stdin().Close(); closeErr != nil {
			return nil, errors.Join(fmt.Errorf("holder: close gate stdin: %w", closeErr), started.Stop())
		}
	}
	child := &Child{
		proc:  started,
		spool: spool,
		clock: cfg.Clock,
		grace: cfg.Grace,
		gate:  gate,
		done:  make(chan struct{}),
	}
	if gate {
		// The limit is armed here, before the child is observed to run, so it counts from the child's start.
		go child.limitAfter(cfg.Clock.After(cfg.GateLimit))
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
// child, with Signal the signal's name. A child that passed its gate limit while it ran has ReasonLimit whatever
// reason is given. A wait error with no exit status, or a fault of the holder, is the last line of Stderr.
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
	if c.limited {
		exit.Reason = ReasonLimit
	}
	c.mu.Unlock()
	exit.Stderr = stderrWith(c.proc.Stderr(), faults)
	return exit
}

// limitAfter stops the child once limit fires, unless the child is reaped first. A child that is stopped this way
// is marked limited before the stop, so Exit reports ReasonLimit.
func (c *Child) limitAfter(limit <-chan time.Time) {
	select {
	case <-limit:
	case <-c.proc.Done():
		return
	}
	c.mu.Lock()
	select {
	case <-c.proc.Done():
		c.mu.Unlock()
		return
	default:
	}
	c.limited = true
	c.mu.Unlock()
	c.Stop()
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
// gate's stdin was closed at start, so only its stdout is closed here. A failure stops the child and is reported in
// Exit.
func (c *Child) pump() {
	reaped := c.proc.Done()
	if err := c.copyStdout(reaped); err != nil {
		c.fail(err)
		c.Stop()
	}
	<-reaped
	closeErr := c.proc.Stdout().Close()
	if !c.gate {
		closeErr = errors.Join(closeErr, c.proc.Stdin().Close())
	}
	if closeErr != nil {
		c.fail(fmt.Errorf("holder: close child pipes: %w", closeErr))
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

// stderrWith returns the stderr tail as the child wrote it. With err, the tail is followed by a newline unless it is
// empty or already ends in one, and then by err as the last line.
func stderrWith(tail string, err error) string {
	if err == nil {
		return tail
	}
	if tail != "" && !strings.HasSuffix(tail, "\n") {
		tail += "\n"
	}
	return tail + err.Error()
}
