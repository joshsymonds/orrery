package enginehost

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/joshsymonds/orrery/internal/engine/proc"
	"github.com/joshsymonds/orrery/internal/grants"
	"github.com/joshsymonds/orrery/internal/holder"
)

// gateHoldersName is the subdirectory of a gate's state directory that holds the holder directories, as holder.Launch
// lays them out.
const gateHoldersName = "holders"

// gateStateDirName is the directory under os.TempDir that holds a gate's holders when its run names no state directory.
const gateStateDirName = "orrery-gates"

// errGateNoCommand reports a gate that names no command to run.
var errGateNoCommand = errors.New("enginehost: gate names no command")

// gateRun describes one gate: the command, where it runs, how long it may run, and the complete environment it runs in.
// Name is the session the gate's holder serves, and a fresh session when empty. StateDir is the state directory the holder
// directory lives under, and a directory under os.TempDir when empty. Scope, when set, is the systemd-run scope the holder
// runs in. Started, when set, is called once the grant holds, with the grant's id and the holder directory, before the gate
// launches; its error releases the grant and launches nothing. Tail is the number of newest output bytes the gate
// keeps, and zero keeps the holder's default of 8 KiB.
type gateRun struct {
	Argv     []string
	Dir      string
	Limit    time.Duration
	Environ  []string
	Name     string
	StateDir string
	Scope    string
	Started  func(ctx context.Context, grantID, dir string) error
	Tail     int
}

// gateResult is how a gate ended: its exit status, whether Limit killed it, the last bytes it printed, the members of the
// grant the attempt that produced it held, and the gate's holder directory. The holder directory keeps the gate's exit
// record until its caller clears it.
type gateResult struct {
	Exit     int
	TimedOut bool
	Output   string
	Members  []grants.Member
	Holder   string
}

// errGateNoPools reports a granted gate with no pool commands to queue it with.
var errGateNoPools = errors.New("enginehost: gate has no pools")

// errGateUngranted reports a granted gate whose grant ended before the gate ran or while it ran.
var errGateUngranted = errors.New("enginehost: gate ended without its grant")

// errGateNoRecord reports a gate whose holder ended without leaving an exit record.
var errGateNoRecord = errors.New("enginehost: gate holder ended without an exit record")

// errGateDetach is the cause a caller cancels a gate's context with to leave the gate running under its holder.
var errGateDetach = errors.New("enginehost: detach from gate")

// errGateLost reports a gate whose outcome cannot be read: its holder left no record that names how the gate ended, its
// holder cannot be attached, or its grant cannot be reclaimed. The gate is ended and it does not run again.
var errGateLost = errors.New("enginehost: gate outcome lost")

// gateOutcome is how a gate's watch ended.
type gateOutcome int

const (
	// gateExited means the gate's holder ended on its own, and its exit record is kept in its directory.
	gateExited gateOutcome = iota
	// gateStopped means a stop ended the gate.
	gateStopped
	// gateUngranted means the grant's Acquire call ended while the gate ran.
	gateUngranted
	// gateCancelled means ctx ended the gate.
	gateCancelled
	// gateDetached means the gate runs on under its holder, which keeps the gate's directory. ctx ended with
	// errGateDetach, or a gate with a Scope had its grant's Acquire end with grants.ErrClosed.
	gateDetached
)

// runGrantedGate runs run.Argv as a gate that holds the members pools grants for req. Each attempt queues req, naming the
// gate's holder directory, and runs the gate once the grant holds its members. The gate runs under a holder in run.Dir,
// with the environment gateEnviron(run.Environ) gives. The Limit of an attempt counts from its grant, not from the queue.
// A stop from an owner hold ends the gate's holder, yields no result, and reclaims the stopped grant by its id, then the
// gate runs again. The grant ends only after the gate's processes have ended, except when the attempt detaches: ctx
// ends with errGateDetach, or a gate with a Scope has its grant's Acquire end with grants.ErrClosed. The holder then
// keeps running, and the gate's outcome is left to its exit record. It returns an error when pools is nil, when the
// grant ends without the gate ending, when the gate cannot start, or when ctx ends first, and an error matching
// errGateDetach when the attempt detaches. Argv[0] is a path, absolute or relative to run.Dir; it is not looked up on
// PATH.
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

// gatePlan is where an attempt's gate runs: its session, the holder directory that serves the session, the state directory
// that holds the holder directory, and the absolute path of the program.
type gatePlan struct {
	stateDir string
	session  string
	dir      string
	program  string
}

// planGate returns where the attempt for run runs. A run that names no session gets a fresh one.
func planGate(run gateRun) (gatePlan, error) {
	stateDir, err := gateStateDir(run.StateDir)
	if err != nil {
		return gatePlan{}, err
	}
	session := run.Name
	if session == "" {
		if session, err = newSessionID(); err != nil {
			return gatePlan{}, err
		}
	}
	dir, err := holder.SessionDir(filepath.Join(stateDir, gateHoldersName), session)
	if err != nil {
		return gatePlan{}, fmt.Errorf("enginehost: gate holder: %w", err)
	}
	program, err := gateProgram(run)
	if err != nil {
		return gatePlan{}, err
	}
	return gatePlan{stateDir: stateDir, session: session, dir: dir, program: program}, nil
}

// gateCall is the Acquire call an attempt queues with. The call runs on its own goroutine, and ended closes once it
// returns. granted receives the grant and stop receives a stop. err is read only after ended closes. scoped is set when
// the gate has a Scope, which makes the broker's close of the call a detach rather than an end.
type gateCall struct {
	cancel  context.CancelFunc
	granted chan grants.Grant
	stop    chan struct{}
	ended   chan struct{}
	scoped  bool
	err     error
}

// startGateCall queues req and returns its call, which is scoped when the gate has a Scope. The call's context is not
// ctx: while the request queues, await ends the call when ctx ends, and once a grant holds, only release ends it. So a
// detach leaves a held grant in place.
func startGateCall(ctx context.Context, pools PoolCommands, req grants.Request, scoped bool) *gateCall {
	acquireCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	call := &gateCall{
		cancel:  cancel,
		granted: make(chan grants.Grant, 1),
		stop:    make(chan struct{}, 1),
		ended:   make(chan struct{}),
		scoped:  scoped,
	}
	go func() {
		defer close(call.ended)
		call.err = pools.Acquire(acquireCtx, req, gateEvents(call.granted, call.stop))
	}()
	return call
}

// await waits until the call's grant holds, and returns it. The attempt fails when the call ends first, or when ctx ends
// while the request queues; the call is then ended. A scoped call the broker closes while the request queues fails the
// attempt with errGateDetach, since nothing has launched.
func (c *gateCall) await(ctx context.Context) (grants.Grant, error) {
	select {
	case held := <-c.granted:
		return held, nil
	case <-c.ended:
		c.cancel()
		if c.closedDetach() {
			return grants.Grant{}, fmt.Errorf("enginehost: gate left queued by the broker's close: %w", errGateDetach)
		}
		return grants.Grant{}, errors.Join(errGateUngranted, c.err)
	case <-ctx.Done():
		return grants.Grant{}, errors.Join(
			fmt.Errorf("enginehost: gate ended by its context: %w", ctx.Err()),
			c.release(),
		)
	}
}

// release ends the call and waits for it to return. Acquire returns the call's own cancellation once it is ended, which
// is not reported, and a scoped call's close by the broker, which detaches its gate rather than failing it. Any other
// error it returns is.
func (c *gateCall) release() error {
	c.cancel()
	<-c.ended
	if c.err != nil && !errors.Is(c.err, context.Canceled) && !c.closedDetach() {
		return fmt.Errorf("enginehost: end grant: %w", c.err)
	}
	return nil
}

// closedDetach reports whether the call ended because the broker closed, under a gate with a Scope. Such a gate is
// detached, not ended: its holder runs on in its scope, and its directory keeps its record for re-adoption. It reads
// err, so it is called only once ended has closed.
func (c *gateCall) closedDetach() bool {
	return c.scoped && errors.Is(c.err, grants.ErrClosed)
}

// gateAttempt queues req and runs the gate once its grant holds the members. It returns the gate's result, or, when a
// stop ends the grant first, the stopped grant and no result. Unless the attempt detaches, the gate's processes have
// ended, and the grant's Acquire call has returned, before gateAttempt does.
func gateAttempt(
	ctx context.Context,
	pools PoolCommands,
	req grants.Request,
	run gateRun,
) (gateResult, *grants.Grant, error) {
	plan, err := planGate(run)
	if err != nil {
		return gateResult{}, nil, err
	}
	req.Holder = plan.dir
	call := startGateCall(ctx, pools, req, run.Scope != "")
	held, err := call.await(ctx)
	if err != nil {
		return gateResult{}, nil, err
	}
	if err = gateGrantGone(ctx, call); err != nil {
		return gateResult{}, nil, errors.Join(err, call.release())
	}
	if run.Started != nil {
		if err = run.Started(ctx, held.ID, plan.dir); err != nil {
			return gateResult{}, nil, errors.Join(fmt.Errorf("enginehost: start gate: %w", err), call.release())
		}
	}
	return gateHeld(ctx, call, held, plan, run)
}

// gateHeld launches the gate under its holder for the grant held, and waits for the gate to end. It ends the grant as the
// gate's outcome requires. A detach returns with the gate left running under its holder.
func gateHeld(
	ctx context.Context,
	call *gateCall,
	held grants.Grant,
	plan gatePlan,
	run gateRun,
) (gateResult, *grants.Grant, error) {
	// A detach leaves the launch running, so the holder it starts is left running. Any other end of ctx ends the launch.
	launchCtx, cancelLaunch := context.WithCancelCause(context.WithoutCancel(ctx))
	defer cancelLaunch(nil)
	stopLaunch := context.AfterFunc(ctx, func() {
		if cause := context.Cause(ctx); !errors.Is(cause, errGateDetach) {
			cancelLaunch(cause)
		}
	})
	defer stopLaunch()
	process, err := holder.Launch(launchCtx, holder.LaunchConfig{
		Scope:     run.Scope,
		StateDir:  plan.stateDir,
		Session:   plan.session,
		Child:     proc.Config{Path: plan.program, Args: run.Argv[1:], Dir: run.Dir, Env: gateEnviron(run.Environ)},
		GateLimit: run.Limit,
		GateTail:  run.Tail,
	})
	if err != nil {
		exit, ended := gateLaunchExit(plan.dir)
		if !ended {
			return gateResult{}, nil, errors.Join(err, gateClear(ctx, plan.dir), call.release())
		}
		// The gate ended before Launch attached, so the holder ended and Launch failed with it. Its exit record is the
		// gate's outcome, and the holder directory keeps it for the caller.
		if endErr := errors.Join(gateKillLeftovers(plan.dir), call.release()); endErr != nil {
			return gateResult{}, nil, endErr
		}
		return gateExitResult(held, plan, exit), nil, nil
	}
	return gateWaitEnd(ctx, process, call, held, plan)
}

// gateWaitEnd waits for the gate that process serves, run under held, to end, and ends the grant as the gate's outcome
// requires. A detach returns with the gate left running under its holder. A stop returns the stopped grant and no
// result.
func gateWaitEnd(
	ctx context.Context,
	process *holder.Process,
	call *gateCall,
	held grants.Grant,
	plan gatePlan,
) (gateResult, *grants.Grant, error) {
	waited := make(chan error, 1)
	go func() {
		waited <- process.Wait()
	}()
	outcome, exit, err := gateWatch(ctx, process, plan.dir, waited, call)
	if outcome == gateDetached {
		return gateResult{}, nil, err
	}
	if err != nil {
		// A failed end clears the holder directory before the grant ends, whatever the failure.
		err = errors.Join(err, gateClear(ctx, plan.dir))
	}
	err = errors.Join(err, call.release())
	if outcome == gateUngranted {
		err = errors.Join(errGateUngranted, err)
	}
	switch {
	case err != nil:
		return gateResult{}, nil, err
	case outcome == gateStopped:
		return gateResult{}, &held, nil
	}
	return gateExitResult(held, plan, exit), nil, nil
}

// readoptGrantedGate resumes the granted gate that run names after the controller that started it ended. grantID is
// the grant the gate holds. When a holder still serves the gate, grantID is reclaimed for req, naming that holder, and
// the gate's end is waited for as an attempt waits for it. When the holder has ended, its exit record gives the outcome:
// an exit or a limit is the result, and runs nothing, once the grant the pools may still keep for the ended holder is
// ended, and a stop, which an owner hold makes, reruns the gate once on grantID. A holder that cannot be attached but
// has written its record is read the same way. Any other outcome, and a reclaim the pools refuse, is errGateLost. The
// gate's holder is then ended and cleared, so no later call reads a record that could rerun it. It returns
// errGateNoPools when pools is nil.
func readoptGrantedGate(
	ctx context.Context,
	pools PoolCommands,
	req grants.Request,
	run gateRun,
	grantID string,
) (gateResult, error) {
	if pools == nil {
		return gateResult{}, errGateNoPools
	}
	plan, err := planGate(run)
	if err != nil {
		return gateResult{}, err
	}
	process, err := holder.ReattachGate(ctx, plan.dir)
	switch {
	case err == nil:
		return readoptServed(ctx, pools, req, run, plan, process, grantID)
	case errors.Is(err, holder.ErrGone):
		return readoptEnded(ctx, pools, req, run, plan, grantID)
	}
	// A holder writes its exit record before it removes its socket, and frees its lock last, so a holder that is ending
	// can be unattachable with its record already written. That record is the gate's outcome.
	if _, recorded := gateExitRecord(plan.dir); recorded {
		return readoptEnded(ctx, pools, req, run, plan, grantID)
	}
	return gateResult{}, errors.Join(errGateLost, err, gateEnd(ctx, plan.dir))
}

// readoptServed reclaims grantID for req, naming the holder that process is attached to, and waits for the gate's end
// as an attempt does. A reclaim that is refused or ends leaves the gate running with no grant, so the holder is ended,
// except when the broker closes under a scoped gate's reclaim: that detaches from the holder, which runs on.
func readoptServed(
	ctx context.Context,
	pools PoolCommands,
	req grants.Request,
	run gateRun,
	plan gatePlan,
	process *holder.Process,
	grantID string,
) (gateResult, error) {
	req.GrantID = grantID
	req.Holder = plan.dir
	call := startGateCall(ctx, pools, req, run.Scope != "")
	held, err := call.await(ctx)
	switch {
	case errors.Is(err, errGateDetach):
		return gateResult{}, errors.Join(err, process.Detach())
	case err != nil:
		return gateResult{}, errors.Join(errGateLost, err, gateEnd(ctx, plan.dir))
	}
	result, stopped, err := gateWaitEnd(ctx, process, call, held, plan)
	switch {
	case errors.Is(err, errGateNoRecord):
		return gateResult{}, errors.Join(errGateLost, err)
	case err != nil || stopped == nil:
		return result, err
	}
	return gateRerun(ctx, pools, req, run, stopped.ID)
}

// readoptEnded takes the outcome of a gate whose holder has ended, from the exit record the holder left in plan's
// directory. An exit or a limit is the result, and runs nothing: the grant the gate held, which the broker may still keep
// for its holder, is reclaimed and ended at once, so its members are free when the result lands. A stopped gate reruns.
func readoptEnded(
	ctx context.Context,
	pools PoolCommands,
	req grants.Request,
	run gateRun,
	plan gatePlan,
	grantID string,
) (gateResult, error) {
	exit, ok := gateExitRecord(plan.dir)
	switch {
	case ok && exit.Reason == holder.ReasonStopped:
		// The stop left the holder's directory and its record for clearing. Clear them here, as a live stop does: the
		// rerun then launches a new holder, and no later call reads this record and reruns the gate again.
		if err := gateEnd(ctx, plan.dir); err != nil {
			return gateResult{}, errors.Join(errGateLost, err)
		}
		return gateRerun(ctx, pools, req, run, grantID)
	case ok && gateEndedOnItsOwn(exit):
		if err := gateKillLeftovers(plan.dir); err != nil {
			return gateResult{}, err
		}
		held, err := gateEndKept(ctx, pools, req, run, plan, grantID)
		if err != nil {
			return gateResult{}, err
		}
		return gateExitResult(held, plan, exit), nil
	}
	return gateResult{}, errors.Join(errGateLost, gateEnd(ctx, plan.dir))
}

// gateEndKept ends grantID, the grant a gate that has ended held, if the pools still keep it: it reclaims the grant for
// req, naming the gate's holder directory, and releases it at once. It returns the grant it ended, or no grant when the
// pools no longer hold it. A scoped gate whose reclaim the broker's close cuts returns an error matching errGateDetach.
func gateEndKept(
	ctx context.Context,
	pools PoolCommands,
	req grants.Request,
	run gateRun,
	plan gatePlan,
	grantID string,
) (grants.Grant, error) {
	req.GrantID = grantID
	req.Holder = plan.dir
	call := startGateCall(ctx, pools, req, run.Scope != "")
	held, err := call.await(ctx)
	switch {
	case errors.Is(err, grants.ErrNoGrant):
		return grants.Grant{}, nil
	case err != nil:
		return grants.Grant{}, err
	}
	if releaseErr := call.release(); releaseErr != nil {
		return grants.Grant{}, releaseErr
	}
	return held, nil
}

// gateRerun runs the gate again on grantID, the grant a stop ended, as runGrantedGate does after a stop. A grant the
// pools no longer hold cannot be reclaimed, so the gate is lost.
func gateRerun(
	ctx context.Context,
	pools PoolCommands,
	req grants.Request,
	run gateRun,
	grantID string,
) (gateResult, error) {
	req.GrantID = grantID
	result, err := runGrantedGate(ctx, pools, req, run)
	if errors.Is(err, grants.ErrNoGrant) {
		return gateResult{}, errors.Join(errGateLost, err)
	}
	return result, err
}

// gateLaunchExit returns the exit record the holder of dir left once the gate ended on its own, and whether there is
// one. Launch fails when the holder ends before it serves its socket, so a gate that ends at once is read here.
func gateLaunchExit(dir string) (holder.Exit, bool) {
	exit, ok := gateExitRecord(dir)
	return exit, ok && gateEndedOnItsOwn(exit)
}

// gateExitRecord returns the exit record the holder of dir left, and whether it left one. A directory that holds no
// readable record has none.
func gateExitRecord(dir string) (holder.Exit, bool) {
	gone, err := holder.OpenGone(dir, 0)
	if err != nil {
		return holder.Exit{}, false
	}
	return gone.Exit()
}

// gateEndedOnItsOwn reports whether an exit record is the gate's own end: its child reached an exit status, or was
// killed at its limit. A holder that a stop or the idle timeout ended is not an outcome, and neither is a child that
// reached no exit status, as a child that never started does: the holder records it with code -1 and no signal.
func gateEndedOnItsOwn(exit holder.Exit) bool {
	reached := exit.Code >= 0 || exit.Signal != ""
	return reached && (exit.Reason == holder.ReasonExited || exit.Reason == holder.ReasonLimit)
}

// gateExitResult returns the result of a gate that ended on its own with exit, under the grant held in plan's holder
// directory.
func gateExitResult(held grants.Grant, plan gatePlan, exit holder.Exit) gateResult {
	return gateResult{
		Exit:     exit.Code,
		TimedOut: exit.Reason == holder.ReasonLimit,
		Output:   exit.Stderr,
		Members:  held.Members,
		Holder:   plan.dir,
	}
}

// gateWatch waits until the gate's holder ends, or until a stop, the end of the grant or ctx ends the gate. A gate that a
// stop, the grant's end or any other end of ctx ends is stopped, its leftovers are killed and its holder directory is
// cleared. A holder that ends on its own keeps its directory and exit record for the caller. A ctx that ends with
// errGateDetach leaves the holder running, with its directory, and so does the broker's close of a scoped gate's grant.
// waited receives the holder's Wait error once the holder has ended.
func gateWatch(
	ctx context.Context,
	process *holder.Process,
	dir string,
	waited <-chan error,
	call *gateCall,
) (gateOutcome, holder.Exit, error) {
	select {
	case waitErr := <-waited:
		exit, ok := process.Exit()
		if !ok {
			return gateExited, holder.Exit{}, errors.Join(errGateNoRecord, waitErr, gateEnd(ctx, dir))
		}
		return gateExited, exit, gateKillLeftovers(dir)
	case <-call.stop:
		return gateStopped, holder.Exit{}, gateEnd(ctx, dir)
	case <-call.ended:
		if call.closedDetach() {
			return gateDetached, holder.Exit{}, gateLeftRunning(process)
		}
		return gateUngranted, holder.Exit{}, gateEnd(ctx, dir)
	case <-ctx.Done():
		if errors.Is(context.Cause(ctx), errGateDetach) {
			return gateDetached, holder.Exit{}, gateLeftRunning(process)
		}
		return gateCancelled, holder.Exit{}, errors.Join(
			fmt.Errorf("enginehost: gate ended by its context: %w", ctx.Err()),
			gateEnd(ctx, dir),
		)
	}
}

// gateLeftRunning detaches process from its holder, which keeps the gate running in its directory. It returns the
// error of the attempt that leaves the gate running: errGateDetach.
func gateLeftRunning(process *holder.Process) error {
	return errors.Join(fmt.Errorf("enginehost: gate left running: %w", errGateDetach), process.Detach())
}

// gateEnd ends a gate whose holder may still run: it stops the holder, kills what the gate's child left in its process
// group, and clears the holder directory. The grant ends only after it, so no process of the gate can use the members then.
func gateEnd(ctx context.Context, dir string) error {
	stopErr := gateStopHolder(ctx, dir)
	killErr := gateKillLeftovers(dir)
	return errors.Join(stopErr, killErr, gateClear(ctx, dir))
}

// gateStopHolder stops the holder serving dir, if one does. It runs whatever state ctx is in, and waits no longer than
// clearTimeout.
func gateStopHolder(ctx context.Context, dir string) error {
	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), clearTimeout)
	defer cancel()
	if err := holder.Stop(stopCtx, dir); err != nil {
		return fmt.Errorf("enginehost: stop gate holder: %w", err)
	}
	return nil
}

// gateClear clears the holder directory dir, stopping its holder first if one still serves it. An empty dir names no
// holder directory and is left alone. It runs whatever state ctx is in, since the gate it holds has ended or never
// launched, and waits no longer than clearTimeout.
func gateClear(ctx context.Context, dir string) error {
	if dir == "" {
		return nil
	}
	clearCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), clearTimeout)
	defer cancel()
	if err := holder.Clear(clearCtx, dir); err != nil {
		return fmt.Errorf("enginehost: clear gate holder: %w", err)
	}
	return nil
}

// gateKillLeftovers kills the processes the gate's child left running in its process group. A holder does not signal
// its child's group once the child has ended on its own, so the leftovers are killed here. A directory with no pids
// record has no child to kill. The group is killed only when its leader is the recorded child: a record with no
// ChildStart cannot be checked, so nothing is signaled, and nothing is signaled when another process holds the
// child's pid, since that process's group is not the gate's.
func gateKillLeftovers(dir string) error {
	pids, err := holder.ReadPids(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("enginehost: read gate pids: %w", err)
	}
	if pids.Child <= 0 {
		return fmt.Errorf("enginehost: gate pids in %s name no child", dir)
	}
	if pids.ChildStart == "" {
		return nil
	}
	held, err := gatePidHeldElsewhere(pids.Child, pids.ChildStart)
	if err != nil {
		return err
	}
	if held {
		return nil
	}
	return gateKillGroup(pids.Child)
}

// gateStartIndex is the index of field 22, the start time, among the stat fields after the command name, which begin
// at field 3.
const gateStartIndex = 22 - 3

// gatePidHeldElsewhere reports whether a process other than the one that started at start holds pid. A zombie holds
// its pid as a live process does, and a pid that /proc holds no stat for is held by no process.
func gatePidHeldElsewhere(pid int, start string) (bool, error) {
	raw, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	// A process reaped between the open and the read fails the read with ESRCH; it is gone all the same.
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ESRCH) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("enginehost: read stat of pid %d: %w", pid, err)
	}
	got, err := gateStartOf(string(raw))
	if err != nil {
		return false, fmt.Errorf("enginehost: stat of pid %d: %w", pid, err)
	}
	return got != start, nil
}

// gateStartOf returns field 22, the start time, of a /proc/PID/stat line. The command name is in parentheses and may
// hold spaces and parentheses, so the fields are counted after the last ')'.
func gateStartOf(stat string) (string, error) {
	end := strings.LastIndexByte(stat, ')')
	if end < 0 {
		return "", fmt.Errorf("enginehost: stat has no command name: %q", stat)
	}
	fields := strings.Fields(stat[end+1:])
	if len(fields) <= gateStartIndex {
		return "", fmt.Errorf("enginehost: stat has %d fields after its command name: %q", len(fields), stat)
	}
	return fields[gateStartIndex], nil
}

// gateStateDir returns the absolute state directory a gate's holder directory lives under: stateDir, or a directory under
// os.TempDir when stateDir is empty.
func gateStateDir(stateDir string) (string, error) {
	if stateDir == "" {
		stateDir = filepath.Join(os.TempDir(), gateStateDirName)
	}
	abs, err := filepath.Abs(stateDir)
	if err != nil {
		return "", fmt.Errorf("enginehost: gate state dir: %w", err)
	}
	return abs, nil
}

// gateProgram returns the absolute path run.Argv[0] names: a path absolute, or relative to run.Dir. It is not looked up on
// PATH.
func gateProgram(run gateRun) (string, error) {
	program := run.Argv[0]
	if !filepath.IsAbs(program) {
		program = filepath.Join(run.Dir, program)
	}
	abs, err := filepath.Abs(program)
	if err != nil {
		return "", fmt.Errorf("enginehost: gate program %s: %w", run.Argv[0], err)
	}
	return abs, nil
}

// gateEvents returns the send an attempt's Acquire call reports to: a grant goes to granted and a stop to stop. A grant event
// carries a grant. A queue position is counted from 1 and carries no grant. A stop carries neither. The broker sends each at
// most once per call, so a send to a full buffer is a repeat and is dropped.
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
// end closes call.ended, has already returned, or ctx has ended. A grant can end in the same instant it arrives, and a
// select on both picks either ready case, so a gate never starts on members its grant has already released. A scoped
// call the broker closed gives errGateDetach, since the gate does not start. It returns nil when the gate may start.
func gateGrantGone(ctx context.Context, call *gateCall) error {
	select {
	case <-call.ended:
		if call.closedDetach() {
			return fmt.Errorf("enginehost: gate left unstarted by the broker's close: %w", errGateDetach)
		}
		return errGateUngranted
	default:
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("enginehost: gate ended by its context: %w", err)
	}
	return nil
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
