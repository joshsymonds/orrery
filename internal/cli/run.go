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

	"connectrpc.com/connect"

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
// status once it exits. When the grant's stream ends first, the command gets SIGTERM the same way and ExitNoGrant is
// returned.
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
	// Canceling the stream's context on return ends the stream, and with it the grant.
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := cfg.Pools.Acquire(streamCtx, connect.NewRequest(cfg.Request))
	if err != nil {
		return noGrant(cfg.Stderr, err)
	}
	granted, err := awaitGrant(stream, cfg.Stderr)
	if err != nil {
		return noGrant(cfg.Stderr, err)
	}
	return runUnderGrant(ctx, cfg, path, granted, watchStream(stream))
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

// watchStream reads the grant's stream until it ends, and returns the channel that receives why. The events after the
// grant carry nothing a run needs. Reading them is what notices that the grant has ended.
func watchStream(stream *connect.ServerStreamForClient[v1.AcquireResponse]) <-chan error {
	ended := make(chan error, 1)
	go func() {
		for stream.Receive() {
			// Nothing after the grant changes the run.
		}
		ended <- endError(stream.Err())
	}()
	return ended
}

// runUnderGrant starts path under granted and returns its exit status. The grant holds until the command exits. The
// command gets SIGTERM when ctx ends or when the stream ends first, and RunWithGrant returns only after it exits, so
// the grant is never released while the command still runs.
func runUnderGrant(
	ctx context.Context,
	cfg RunConfig,
	path string,
	granted *v1.AcquireGranted,
	ended <-chan error,
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
	var reason error
	select {
	case end := <-exited:
		return end.report(cfg.Stderr)
	case <-ctx.Done():
	case reason = <-ended:
	}
	// The command's output may still be copying to stderr until it has exited, so the stop error is written after that.
	stopErr := stopGroup(pgid)
	finished := <-exited
	if stopErr != nil {
		fmt.Fprintf(cfg.Stderr, "orrery run: %v\n", stopErr)
	}
	status := finished.report(cfg.Stderr)
	if ctx.Err() != nil {
		return status
	}
	fmt.Fprintf(cfg.Stderr, "orrery run: the grant ended while CMD ran: %v\n", reason)
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
