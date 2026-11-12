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
}

// commandEnd is how a started command ended: its exit status, and an error from waiting for it besides the exit.
type commandEnd struct {
	status int
	err    error
}

// RunWithGrant acquires the grant Request asks for, then runs Argv under it and returns the command's exit status. The
// command runs without a shell, in its own process group, with Environ plus ORRERY_GRANT and one ORRERY_POOL_<POOL> per
// granted pool whose value is its members, comma-separated. The grant lasts while the command runs. A command that did
// not run for want of a grant returns ExitNoGrant. When ctx ends, the command gets SIGTERM and RunWithGrant returns its
// status once it exits. When the grant's stream breaks while the command runs, the grant is reclaimed by its id every
// Retry until orreryd grants it again, and the command keeps running. When orreryd refuses the reclaim, the command
// gets SIGTERM and ExitNoGrant is returned.
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
	return runUnderGrant(streamCtx, cfg, path, granted, stream, endGrant, ctx.Done())
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

// holdGrant holds the grant while the command runs. It reads the grant's stream until the stream ends, then reclaims
// the grant by grantID and holds the reclaimed grant the same way, again and again. It returns only when a reclaim
// fails: with the refusal, or with ctx's error once ctx ends.
func holdGrant(
	ctx context.Context,
	cfg RunConfig,
	grantID string,
	stream *connect.ServerStreamForClient[v1.AcquireResponse],
) error {
	for {
		for stream.Receive() {
			// Nothing after the grant changes the run.
		}
		closeStream(stream, cfg.Stderr)
		reclaimed, err := reclaim(ctx, cfg, grantID)
		if err != nil {
			return err
		}
		stream = reclaimed
	}
}

// reclaim asks orreryd for the grant by grantID, every Retry, until it grants it again, and returns the stream that
// holds the reclaimed grant. The reclaim is cfg.Request with its grant_id set. It returns the error that refused the
// reclaim, or ctx's error when ctx ends first. Any other failure is retried.
func reclaim(
	ctx context.Context,
	cfg RunConfig,
	grantID string,
) (*connect.ServerStreamForClient[v1.AcquireResponse], error) {
	req := proto.CloneOf(cfg.Request)
	req.GrantId = grantID
	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("reclaim the grant: %w", ctx.Err())
		case <-time.After(cfg.Retry):
		}
		stream, err := reclaimOnce(ctx, cfg, req)
		if err == nil {
			return stream, nil
		}
		if refusedReclaim(err) {
			return nil, err
		}
	}
}

// reclaimOnce sends one reclaim and returns the stream once orreryd grants it. A reclaim that fails closes its stream.
func reclaimOnce(
	ctx context.Context,
	cfg RunConfig,
	req *v1.AcquireRequest,
) (*connect.ServerStreamForClient[v1.AcquireResponse], error) {
	stream, err := cfg.Pools.Acquire(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, fmt.Errorf("reclaim the grant: %w", err)
	}
	if _, err = awaitGrant(stream, cfg.Stderr); err != nil {
		closeStream(stream, cfg.Stderr)
		return nil, err
	}
	return stream, nil
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

// runUnderGrant starts path under granted and returns its exit status. The grant holds while the command runs:
// holdGrant reclaims it by id whenever its stream breaks. The command gets SIGTERM when runDone closes or when orreryd
// refuses a reclaim. runUnderGrant returns only after the command exits and holdGrant has stopped, so the grant is
// never released while the command still runs. grantCtx is the context the stream was opened under, and endGrant
// cancels it.
func runUnderGrant(
	grantCtx context.Context,
	cfg RunConfig,
	path string,
	granted *v1.AcquireGranted,
	stream *connect.ServerStreamForClient[v1.AcquireResponse],
	endGrant context.CancelFunc,
	runDone <-chan struct{},
) int {
	pgid, exited, err := startCommand(&exec.Cmd{
		Path:        path,
		Args:        cfg.Argv,
		Env:         grantEnviron(cfg.Environ, granted),
		Stdin:       cfg.Stdin,
		Stdout:      cfg.Stdout,
		Stderr:      cfg.Stderr,
		SysProcAttr: &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL},
	})
	if err != nil {
		fmt.Fprintf(cfg.Stderr, "orrery run: start %s: %v\n", cfg.Argv[0], err)
		return exitCannotRun
	}
	refused := make(chan error, 1)
	holding := make(chan struct{})
	go func() {
		defer close(holding)
		// Only a refusal is sent. Once grantCtx has ended, holdGrant's error is ctx's and nothing reads it.
		if refusedBy := holdGrant(grantCtx, cfg, granted.GetGrantId(), stream); grantCtx.Err() == nil {
			refused <- refusedBy
		}
	}()
	var refusal error
	select {
	case end := <-exited:
		endGrant()
		<-holding
		return end.report(cfg.Stderr)
	case <-runDone:
	case refusal = <-refused:
	}
	// The command's output may still be copying to stderr until it has exited, so the stop error is written after that.
	stopErr := stopGroup(pgid)
	finished := <-exited
	endGrant()
	// holdGrant may still be running, so it is waited for here and writes nothing after this returns.
	<-holding
	if stopErr != nil {
		fmt.Fprintf(cfg.Stderr, "orrery run: %v\n", stopErr)
	}
	status := finished.report(cfg.Stderr)
	if refusal == nil {
		return status
	}
	fmt.Fprintf(cfg.Stderr, "orrery run: the grant ended while CMD ran: orreryd refused to reclaim it: %v\n", refusal)
	return ExitNoGrant
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

// stopGroup sends SIGTERM to every process in the group pgid. A group with no processes left is not an error.
func stopGroup(pgid int) error {
	if err := syscall.Kill(-pgid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("stop CMD: %w", err)
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
