package enginehost

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/joshsymonds/orrery/internal/grants"
)

// gateWaitDelay is how long runGate waits for a gate's output to close after the gate has ended. A child
// that leaves the process group and keeps the output open would otherwise hold runGate past the limit.
const gateWaitDelay = time.Second

// gateOutputBytes is how much of a gate's combined stdout and stderr runGate keeps.
const gateOutputBytes = 8 * 1024

// errGateNoCommand reports a gate that names no command to run.
var errGateNoCommand = errors.New("enginehost: gate names no command")

// gateRun describes one gate: the command, where it runs, how long it may run, and the complete environment it runs in.
type gateRun struct {
	Argv    []string
	Dir     string
	Limit   time.Duration
	Environ []string
}

// gateResult is how a gate ended: its exit status, whether Limit killed it, the last bytes it printed, and the members
// of the grant the attempt that produced it held.
type gateResult struct {
	Exit     int
	TimedOut bool
	Output   string
	Members  []grants.Member
}

// errGateNoPools reports a granted gate with no pool commands to queue it with.
var errGateNoPools = errors.New("enginehost: gate has no pools")

// errGateUngranted reports a granted gate whose grant ended before the gate ran or while it ran.
var errGateUngranted = errors.New("enginehost: gate ended without its grant")

// gateStarted is a gate process running in its own process group. waited closes once the gate has exited and
// its output is collected, and waitErr is read only after that.
type gateStarted struct {
	pid     int
	output  *gateTail
	waitErr error
	waited  chan struct{}
}

// gateOutcome is how a started gate ended.
type gateOutcome int

const (
	// gateExited means the gate ended on its own.
	gateExited gateOutcome = iota
	// gateLimited means Limit killed the gate.
	gateLimited
	// gateStopped means a stop killed the gate.
	gateStopped
	// gateUngranted means the grant's Acquire call ended while the gate ran, and the gate was killed.
	gateUngranted
)

// runGrantedGate runs run.Argv as a gate that holds the members pools grants for req. Each attempt queues req
// and runs the gate once the grant holds its members, in run.Dir with the TMUX and TMUX_PANE markers removed
// from run.Environ. The Limit of an attempt counts from its grant, not from the queue. A stop from an owner hold
// kills the gate's group, yields no result, and reclaims the stopped grant by its id, then the gate runs again.
// The grant ends only after the gate's group has exited. It returns an error when pools is nil, when the grant ends
// without the gate ending, when the gate cannot start, or when ctx ends first. Argv[0] is a path, absolute or
// relative to run.Dir; it is not looked up on PATH.
func runGrantedGate(ctx context.Context, pools PoolCommands, req grants.Request, run gateRun) (gateResult, error) {
	if pools == nil {
		return gateResult{}, errGateNoPools
	}
	if len(run.Argv) == 0 {
		return gateResult{}, errGateNoCommand
	}
	for {
		result, stopped, err := gateAttempt(ctx, pools, req, run)
		if err != nil || stopped == nil {
			return result, err
		}
		req.GrantID = stopped.ID
	}
}

// gateAttempt queues req and runs the gate once its grant holds the members. It returns the gate's result, or,
// when a stop ends the grant first, the stopped grant and no result. The Acquire call of the grant has returned
// before gateAttempt does, and the gate's group has exited by then.
func gateAttempt(
	ctx context.Context,
	pools PoolCommands,
	req grants.Request,
	run gateRun,
) (gateResult, *grants.Grant, error) {
	acquireCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	granted := make(chan grants.Grant, 1)
	stop := make(chan struct{}, 1)
	ended := make(chan struct{})
	var acquireErr error
	go func() {
		defer close(ended)
		acquireErr = pools.Acquire(acquireCtx, req, gateEvents(granted, stop))
	}()

	// release ends the Acquire call, which returns ctx's error, and waits for it. Any other error it returns is kept.
	release := func() error {
		cancel()
		<-ended
		if acquireErr != nil && !errors.Is(acquireErr, acquireCtx.Err()) {
			return fmt.Errorf("enginehost: end grant: %w", acquireErr)
		}
		return nil
	}

	var held grants.Grant
	select {
	case held = <-granted:
	case <-ended:
		return gateResult{}, nil, errors.Join(errGateUngranted, acquireErr)
	}
	if err := gateGrantGone(ctx, ended); err != nil {
		return gateResult{}, nil, errors.Join(err, release())
	}
	started, err := gateStart(run)
	if err != nil {
		return gateResult{}, nil, errors.Join(err, release())
	}
	outcome, err := started.watch(ctx, run.Limit, stop, ended)
	// Kill what is left of the group whichever way the gate ended, so the grant ends only once no process of the
	// gate can still use its members.
	err = errors.Join(err, gateKillGroup(started.pid), release())
	if outcome == gateUngranted {
		err = errors.Join(errGateUngranted, err)
	}
	if err != nil {
		return gateResult{}, nil, err
	}
	if outcome == gateStopped {
		return gateResult{}, &held, nil
	}
	exit, err := gateExitCode(started.waitErr)
	if err != nil {
		return gateResult{}, nil, err
	}
	return gateResult{
		Exit:     exit,
		TimedOut: outcome == gateLimited,
		Output:   started.output.String(),
		Members:  held.Members,
	}, nil, nil
}

// gateEvents returns the send an attempt's Acquire call reports to: a grant goes to granted and a stop to stop. A grant
// event carries a grant. A queue position is counted from 1 and carries no grant. A stop carries neither. The broker
// sends each at most once per call, so a send to a full buffer is a repeat and is dropped.
func gateEvents(granted chan<- grants.Grant, stop chan<- struct{}) func(grants.Event) error {
	return func(event grants.Event) error {
		switch {
		case event.Grant != nil:
			select {
			case granted <- *event.Grant:
			default:
			}
		case event.Position == 0:
			select {
			case stop <- struct{}{}:
			default:
			}
		}
		return nil
	}
}

// gateGrantGone reports why a gate must not start on the grant it has just received: the grant's Acquire call, whose
// end closes ended, has already returned, or ctx has ended. A grant can end in the same instant it arrives, and a select
// on both picks either ready case, so a gate never starts on members its grant has already released. It returns nil
// when the gate may start.
func gateGrantGone(ctx context.Context, ended <-chan struct{}) error {
	select {
	case <-ended:
		return errGateUngranted
	default:
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("enginehost: gate ended by its context: %w", err)
	}
	return nil
}

// gateStart starts run.Argv in run.Dir in its own process group, with the TMUX and TMUX_PANE markers removed from
// run.Environ, and returns it running. A goroutine collects its exit into the returned gateStarted.
func gateStart(run gateRun) (*gateStarted, error) {
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
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("enginehost: start gate %s: %w", run.Argv[0], err)
	}
	started := &gateStarted{pid: cmd.Process.Pid, output: output, waited: make(chan struct{})}
	go func() {
		started.waitErr = cmd.Wait()
		close(started.waited)
	}()
	return started, nil
}

// watch waits for the gate to end and reports how it ended. It kills the gate's group when Limit passes, when
// stop closes, or when ungranted closes, and it returns once the gate has exited. When ctx ends first it kills
// the group and returns ctx's error. The outcome means something only when the error is nil. A nil channel never
// fires.
func (g *gateStarted) watch(
	ctx context.Context,
	limit time.Duration,
	stop, ungranted <-chan struct{},
) (gateOutcome, error) {
	limitTimer := time.NewTimer(limit)
	defer limitTimer.Stop()
	select {
	case <-g.waited:
		return gateExited, nil
	case <-limitTimer.C:
		return gateLimited, g.kill()
	case <-stop:
		return gateStopped, g.kill()
	case <-ungranted:
		return gateUngranted, g.kill()
	case <-ctx.Done():
		return gateExited, errors.Join(fmt.Errorf("enginehost: gate ended by its context: %w", ctx.Err()), g.kill())
	}
}

// kill kills the gate's group and waits for the gate to exit.
func (g *gateStarted) kill() error {
	err := gateKillGroup(g.pid)
	<-g.waited
	return err
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
