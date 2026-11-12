package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
)

// ExitNoGrant is the exit status of `orrery run` when CMD did not run for want of a grant.
const ExitNoGrant = 75

const (
	// exitUsage is the status of a run with no command to run.
	exitUsage = 2
	// exitNotFound is the status of a command that is not on the path, as the shell reports it.
	exitNotFound = 127
	// exitCannotRun is the status of a command that is found but cannot start, as the shell reports it.
	exitCannotRun = 126
	// exitFailure is the status of a command whose exit could not be read.
	exitFailure = 1
	// signalExitBase is added to a signal number to make the status of a process that a signal ended.
	signalExitBase = 128
)

// defaultStopGrace is how long a stopped command gets after SIGTERM before SIGKILL when RunConfig sets no grace.
const defaultStopGrace = 30 * time.Second

// grantVariable names the variable that carries the grant id to the command.
const grantVariable = "ORRERY_GRANT"

// poolVariablePrefix starts the variable that names one granted pool's members.
const poolVariablePrefix = "ORRERY_POOL_"

// errStreamEnded is why a stream ended without an error.
var errStreamEnded = errors.New("the stream ended")

// RunConfig is one run: the grant to acquire and the command to run under it.
type RunConfig struct {
	Pools   enginev1connect.PoolsClient
	Request *v1.AcquireRequest
	Argv    []string
	Environ []string
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
	// Retry is how long a broken grant stream waits before each reclaim of the grant. Zero means one second.
	Retry time.Duration
	// StopGrace is how long a stopped command gets after SIGTERM before SIGKILL. Zero means 30 seconds.
	StopGrace time.Duration
	// After returns a channel that receives once d has passed; the stop grace runs on it. Nil means time.After.
	After func(d time.Duration) <-chan time.Time
}

// commandEnd is how a started command ended: its exit status, and an error from waiting for it besides the exit.
type commandEnd struct {
	status int
	err    error
}

// heldGrant is a grant a run holds: the grant, the stream that holds it, and end, which ends the grant's context and so
// its stream.
type heldGrant struct {
	granted *v1.AcquireGranted
	stream  *connect.ServerStreamForClient[v1.AcquireResponse]
	end     context.CancelFunc
}

// holdResult is how holdGrant ended: with a stop and the stream that carried it, or with the error that refused a
// reclaim.
type holdResult struct {
	stream *connect.ServerStreamForClient[v1.AcquireResponse]
	stop   *v1.AcquireStop
	err    error
}

// RunWithGrant acquires the grant Request asks for, then runs Argv under it and returns the command's exit status. The
// command runs without a shell, in its own process group, with Environ plus ORRERY_GRANT and one ORRERY_POOL_<POOL> per
// granted pool whose value is its members, comma-separated. The grant lasts while the command runs. A command that did
// not run for want of a grant returns ExitNoGrant. When ctx ends, the command gets SIGTERM and RunWithGrant returns its
// status once it exits. When the grant's stream breaks while the command runs, the grant is reclaimed by its id every
// Retry until orreryd grants it again, and the command keeps running. When a member of the grant is held, the command
// gets SIGTERM, then SIGKILL once StopGrace has passed, and the grant is reclaimed by its id to wait at its place in
// line; the command then runs again under the grant orreryd grants. When orreryd refuses a reclaim, the command gets
// SIGTERM and ExitNoGrant is returned.
func RunWithGrant(ctx context.Context, cfg RunConfig) int {
	if len(cfg.Argv) == 0 {
		fmt.Fprintln(cfg.Stderr, "orrery run: no command to run")
		return exitUsage
	}
	path, err := exec.LookPath(cfg.Argv[0])
	if err != nil {
		fmt.Fprintf(cfg.Stderr, "orrery run: %v\n", err)
		return exitNotFound
	}
	if cfg.Retry == 0 {
		cfg.Retry = time.Second
	}
	if cfg.StopGrace == 0 {
		cfg.StopGrace = defaultStopGrace
	}
	if cfg.After == nil {
		cfg.After = time.After
	}
	// The grant's stream is opened under a context that ctx does not cancel. While the request waits, ctx ending cancels
	// the stream through AfterFunc. Once granted, only runUnderGrant cancels it, after the command has exited, so the
	// grant is never released while the command still runs.
	streamCtx, endGrant := context.WithCancel(context.WithoutCancel(ctx))
	defer endGrant()
	stopWait := context.AfterFunc(ctx, endGrant)
	stream, err := cfg.Pools.Acquire(streamCtx, connect.NewRequest(cfg.Request))
	var granted *v1.AcquireGranted
	if err == nil {
		granted, err = awaitGrant(stream, cfg.Stderr)
	}
	// When ctx ended during the wait, endGrant has run and the grant is gone, so the command must not run.
	if !stopWait() {
		err = ctx.Err()
	}
	if err != nil {
		return noGrant(cfg.Stderr, err)
	}
	return runUnderGrant(ctx, streamCtx, cfg, path, heldGrant{granted: granted, stream: stream, end: endGrant})
}

// noGrant writes why the command did not run and returns ExitNoGrant.
func noGrant(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "orrery run: no grant: %v\n", err)
	return ExitNoGrant
}

// awaitGrant reads the acquire stream until it grants the request, writing each queued position to stderr. It returns
// an error when the stream fails or ends before a grant.
func awaitGrant(
	stream *connect.ServerStreamForClient[v1.AcquireResponse],
	stderr io.Writer,
) (*v1.AcquireGranted, error) {
	for stream.Receive() {
		switch event := stream.Msg().GetEvent().(type) {
		case *v1.AcquireResponse_Queued:
			fmt.Fprintf(stderr, "orrery run: queued at position %d\n", event.Queued.GetPosition())
		case *v1.AcquireResponse_Granted:
			return event.Granted, nil
		}
	}
	return nil, endError(stream.Err())
}

// endError returns why a stream ended: its error, or errStreamEnded when it ended without one.
func endError(err error) error {
	if err == nil {
		return errStreamEnded
	}
	return err
}

// holdGrant holds the grant while the command runs. It reads the grant's stream until a stop arrives, or until the
// stream ends. When the stream ends, it reclaims the grant by grantID and reads the reclaimed stream the same way. It
// returns the stop with the stream that carried it, or the error that refused a reclaim, or ctx's error once ctx ends.
func holdGrant(
	ctx context.Context,
	cfg RunConfig,
	grantID string,
	stream *connect.ServerStreamForClient[v1.AcquireResponse],
) holdResult {
	for {
		for stream.Receive() {
			if stop := stream.Msg().GetStop(); stop != nil {
				return holdResult{stream: stream, stop: stop}
			}
		}
		closeStream(stream, cfg.Stderr)
		_, reclaimed, err := reclaim(ctx, cfg, grantID)
		if err != nil {
			return holdResult{err: err}
		}
		stream = reclaimed
	}
}

// rejoin reclaims grantID after its command stopped, and waits at the grant's place in line until orreryd grants it
// again. Its wait ends with ctx: a grant that is granted after ctx ended is closed and not taken. It returns the
// reclaimed grant with the context its stream is open under.
func rejoin(ctx context.Context, cfg RunConfig, grantID string) (context.Context, heldGrant, error) {
	waitCtx, endWait := context.WithCancel(context.WithoutCancel(ctx))
	stopWait := context.AfterFunc(ctx, endWait)
	granted, stream, err := reclaim(waitCtx, cfg, grantID)
	if !stopWait() && err == nil {
		closeStream(stream, cfg.Stderr)
		err = fmt.Errorf("wait for the grant: %w", ctx.Err())
	}
	if err != nil {
		endWait()
		return nil, heldGrant{}, err
	}
	return waitCtx, heldGrant{granted: granted, stream: stream, end: endWait}, nil
}

// reclaim asks orreryd for the grant by grantID, every Retry, until it grants it again, and returns the grant and the
// stream that holds it. The reclaim is cfg.Request with its grant_id set. It returns the error that refused the
// reclaim, or ctx's error when ctx ends first. Any other failure is retried.
func reclaim(
	ctx context.Context,
	cfg RunConfig,
	grantID string,
) (*v1.AcquireGranted, *connect.ServerStreamForClient[v1.AcquireResponse], error) {
	req := proto.CloneOf(cfg.Request)
	req.GrantId = grantID
	for {
		select {
		case <-ctx.Done():
			return nil, nil, fmt.Errorf("reclaim the grant: %w", ctx.Err())
		case <-time.After(cfg.Retry):
		}
		granted, stream, err := reclaimOnce(ctx, cfg, req)
		if err == nil {
			return granted, stream, nil
		}
		if refusedReclaim(err) {
			return nil, nil, err
		}
	}
}

// reclaimOnce sends one reclaim and returns the grant and its stream once orreryd grants it. A reclaim that fails closes
// its stream.
func reclaimOnce(
	ctx context.Context,
	cfg RunConfig,
	req *v1.AcquireRequest,
) (*v1.AcquireGranted, *connect.ServerStreamForClient[v1.AcquireResponse], error) {
	stream, err := cfg.Pools.Acquire(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, nil, fmt.Errorf("reclaim the grant: %w", err)
	}
	granted, err := awaitGrant(stream, cfg.Stderr)
	if err != nil {
		closeStream(stream, cfg.Stderr)
		return nil, nil, err
	}
	return granted, stream, nil
}

// refusedReclaim reports whether err is orreryd refusing a reclaim: not_found for a grant it does not store, or
// failed_precondition for a grant whose pools it now refuses. Any other error is a failure to reach orreryd.
func refusedReclaim(err error) bool {
	switch connect.CodeOf(err) {
	case connect.CodeNotFound, connect.CodeFailedPrecondition:
		return true
	default:
		return false
	}
}

// closeStream closes a stream that has ended. A close error is written to stderr.
func closeStream(stream *connect.ServerStreamForClient[v1.AcquireResponse], stderr io.Writer) {
	if err := stream.Close(); err != nil {
		fmt.Fprintf(stderr, "orrery run: close the grant's stream: %v\n", err)
	}
}

// runUnderGrant runs path under held and returns its exit status. grantCtx is the context held's stream is open under.
// When the grant stops, CMD is stopped, the grant is reclaimed by its id to wait at its place in line, and CMD runs
// again under the grant orreryd grants then. The grant that is held when runUnderGrant returns is ended first, after
// its command has exited.
func runUnderGrant(ctx, grantCtx context.Context, cfg RunConfig, path string, held heldGrant) int {
	status, stopped := runOnce(ctx, grantCtx, cfg, path, held)
	if !stopped {
		return status
	}
	nextCtx, next, err := rejoin(ctx, cfg, held.granted.GetGrantId())
	if err != nil {
		if refusedReclaim(err) {
			fmt.Fprintf(cfg.Stderr, "orrery run: CMD stopped, and orreryd refused to reclaim its grant: %v\n", err)
		} else {
			fmt.Fprintf(cfg.Stderr, "orrery run: CMD stopped, and its grant was not reclaimed: %v\n", err)
		}
		return ExitNoGrant
	}
	return runUnderGrant(ctx, nextCtx, cfg, path, next)
}

// runOnce starts path under held and holds the grant while it runs. grantCtx is the context held's stream is open under.
// It returns CMD's exit status, and true when the grant stopped: then CMD has been stopped and has exited, and the grant
// is ended. When ctx ends, CMD gets SIGTERM and its status is returned. When orreryd refuses a reclaim, CMD gets SIGTERM
// and ExitNoGrant is returned.
func runOnce(ctx, grantCtx context.Context, cfg RunConfig, path string, held heldGrant) (int, bool) {
	pgid, exited, err := startCommand(&exec.Cmd{
		Path:        path,
		Args:        cfg.Argv,
		Env:         grantEnviron(cfg.Environ, held.granted),
		Stdin:       cfg.Stdin,
		Stdout:      cfg.Stdout,
		Stderr:      cfg.Stderr,
		SysProcAttr: &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL},
	})
	if err != nil {
		held.end()
		fmt.Fprintf(cfg.Stderr, "orrery run: start %s: %v\n", cfg.Argv[0], err)
		return exitCannotRun, false
	}
	result := make(chan holdResult, 1)
	go func() { result <- holdGrant(grantCtx, cfg, held.granted.GetGrantId(), held.stream) }()
	select {
	case end := <-exited:
		held.end()
		<-result
		return end.report(cfg.Stderr), false
	case <-ctx.Done():
		stopErr := signalGroup(pgid, syscall.SIGTERM)
		finished := <-exited
		held.end()
		// holdGrant may still be running, so it is waited for here and writes nothing after this returns.
		<-result
		if stopErr != nil {
			fmt.Fprintf(cfg.Stderr, "orrery run: %v\n", stopErr)
		}
		return finished.report(cfg.Stderr), false
	case res := <-result:
		if res.stop == nil {
			stopErr := signalGroup(pgid, syscall.SIGTERM)
			finished := <-exited
			held.end()
			if stopErr != nil {
				fmt.Fprintf(cfg.Stderr, "orrery run: %v\n", stopErr)
			}
			finished.report(cfg.Stderr)
			fmt.Fprintf(
				cfg.Stderr,
				"orrery run: the grant ended while CMD ran: orreryd refused to reclaim it: %v\n",
				res.err,
			)
			return ExitNoGrant, false
		}
		// The stop is written after CMD has exited, because CMD's output may still be copying to stderr until then.
		end, stopErr := stopCommand(pgid, exited, cfg.StopGrace, cfg.After)
		closeStream(res.stream, cfg.Stderr)
		held.end()
		if stopErr != nil {
			fmt.Fprintf(cfg.Stderr, "orrery run: %v\n", stopErr)
		}
		members := strings.Join(res.stop.GetMembers(), ", ")
		fmt.Fprintf(cfg.Stderr, "orrery run: members %s were held: stopped CMD\n", members)
		return end.report(cfg.Stderr), true
	}
}

// stopCommand sends SIGTERM to CMD's group, then waits for CMD to exit. The grace starts when SIGTERM is sent. When CMD
// has not exited once the grace has passed on after, its group gets SIGKILL and stopCommand waits for that exit. It
// returns CMD's end and the errors from signaling its group.
func stopCommand(
	pgid int,
	exited <-chan commandEnd,
	grace time.Duration,
	after func(d time.Duration) <-chan time.Time,
) (commandEnd, error) {
	termErr := signalGroup(pgid, syscall.SIGTERM)
	select {
	case end := <-exited:
		return end, termErr
	case <-after(grace):
	}
	killErr := signalGroup(pgid, syscall.SIGKILL)
	return <-exited, errors.Join(termErr, killErr)
}

// report writes the end's wait error to stderr, if it has one, and returns the exit status.
func (end commandEnd) report(stderr io.Writer) int {
	if end.err != nil {
		fmt.Fprintf(stderr, "orrery run: %v\n", end.err)
	}
	return end.status
}

// startCommand starts cmd on a thread that stays locked until cmd exits, because Pdeathsig fires when the thread that
// started a process exits. It returns cmd's process group, which is its process id, and the channel its end arrives on.
func startCommand(cmd *exec.Cmd) (int, <-chan commandEnd, error) {
	started := make(chan error, 1)
	exited := make(chan commandEnd, 1)
	go func() {
		runtime.LockOSThread()
		if err := cmd.Start(); err != nil {
			started <- err
			return
		}
		started <- nil
		status, err := waitCommand(cmd)
		exited <- commandEnd{status: status, err: err}
	}()
	if err := <-started; err != nil {
		return 0, nil, err
	}
	return cmd.Process.Pid, exited, nil
}

// waitCommand waits for the started cmd. It returns the exit status, and an error from waiting besides the exit itself,
// such as a copy of the command's output that failed.
func waitCommand(cmd *exec.Cmd) (int, error) {
	err := cmd.Wait()
	if cmd.ProcessState == nil {
		return exitFailure, fmt.Errorf("wait for CMD: %w", err)
	}
	var exitErr *exec.ExitError
	if err == nil || errors.As(err, &exitErr) {
		return exitStatus(cmd.ProcessState), nil
	}
	return exitStatus(cmd.ProcessState), fmt.Errorf("wait for CMD: %w", err)
}

// exitStatus returns the status of a process that ended: its exit code, or the signal's number added to 128 when a
// signal ended it.
func exitStatus(state *os.ProcessState) int {
	if status, ok := state.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return signalExitBase + int(status.Signal())
	}
	return state.ExitCode()
}

// signalGroup sends sig to every process in the group pgid. A group with no processes left is not an error.
func signalGroup(pgid int, sig syscall.Signal) error {
	if err := syscall.Kill(-pgid, sig); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("signal CMD: %w", err)
	}
	return nil
}

// grantEnviron returns environ with the grant's variables appended: the grant id, and each granted pool's members.
func grantEnviron(environ []string, granted *v1.AcquireGranted) []string {
	env := append(slices.Clip(environ), grantVariable+"="+granted.GetGrantId())
	for _, pool := range granted.GetPools() {
		name := poolVariablePrefix + strings.ToUpper(pool.GetPool())
		env = append(env, name+"="+strings.Join(pool.GetMembers(), ","))
	}
	return env
}
