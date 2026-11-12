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

// ExitLimit is the exit status of `orrery run` when CMD ran past its Limit and was killed.
const ExitLimit = 124

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

// stopPollInterval is how often a stop checks whether CMD's process group is empty.
const stopPollInterval = 10 * time.Millisecond

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
	// Limit is how long each run of CMD under a grant may run. Each run counts from its own start, so a rerun after
	// a stop gets its own Limit, and time queued for a grant does not count. A run still going when Limit passes gets
	// SIGKILL, and RunWithGrant returns ExitLimit. Zero means no limit.
	Limit time.Duration
	// After returns a channel that receives once d has passed; the stop grace and the limit run on it. Nil means
	// time.After.
	After func(d time.Duration) <-chan time.Time
}

// commandEnd is how a started command ended: its exit status, an error from waiting for it besides the exit, and
// whether the run's limit killed it.
type commandEnd struct {
	status  int
	err     error
	limited bool
}

// heldGrant is a grant a run holds: the grant, the stream that holds it, and end, which ends the grant's context and so
// its stream.
type heldGrant struct {
	granted *v1.AcquireGranted
	stream  *connect.ServerStreamForClient[v1.AcquireResponse]
	end     context.CancelFunc
}

// holdResult is how holdGrant ended: with a stop and the stream that carried it, with the first event of a reclaim that
// did not give back the original grant intact and the reclaim's stream, or with the error that refused a reclaim.
type holdResult struct {
	stream *connect.ServerStreamForClient[v1.AcquireResponse]
	stop   *v1.AcquireStop
	// answer is the first event of a reclaim that lost the original grant: a queued position, or a grant with another
	// id. stream is the reclaim's stream, which holds the place in line the reclaim took.
	answer *v1.AcquireResponse
	err    error
}

// RunWithGrant acquires the grant Request asks for, then runs Argv under it and returns the command's exit status. The
// command runs without a shell, in its own process group, with Environ plus ORRERY_GRANT and one ORRERY_POOL_<POOL> per
// granted pool whose value is its members, comma-separated. The grant lasts while the command runs. A command that did
// not run for want of a grant returns ExitNoGrant. When ctx ends, the command gets SIGTERM and RunWithGrant returns its
// status once it exits. When the grant's stream breaks while the command runs, the grant is reclaimed by its id every
// Retry until orreryd answers. A reclaim whose first answer grants the same id keeps the command running. When a member
// of the grant is held, or a reclaim's first answer is a queued position or a grant with another id, the grant is lost:
// the command gets SIGTERM, then SIGKILL once StopGrace has passed, and the grant is reclaimed by its id to wait at its
// place in line; the command then runs again under the grant orreryd grants. When orreryd refuses a reclaim, the command
// is stopped as above and ExitNoGrant is returned. When Limit passes while the command runs, its group gets SIGKILL and
// ExitLimit is returned.
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
	for {
		event, err := nextEvent(stream, stderr)
		if err != nil {
			return nil, err
		}
		if granted := event.GetGranted(); granted != nil {
			return granted, nil
		}
	}
}

// nextEvent reads the acquire stream's next queued or granted event, writing a queued position to stderr. It returns an
// error when the stream ends before such an event.
func nextEvent(
	stream *connect.ServerStreamForClient[v1.AcquireResponse],
	stderr io.Writer,
) (*v1.AcquireResponse, error) {
	for stream.Receive() {
		event := stream.Msg()
		if queued := event.GetQueued(); queued != nil {
			fmt.Fprintf(stderr, "orrery run: queued at position %d\n", queued.GetPosition())
			return event, nil
		}
		if event.GetGranted() != nil {
			return event, nil
		}
	}
	return nil, endError(stream.Err())
}

// grantFrom returns the grant that answer grants. When answer only queues the request, it waits on stream for the grant.
func grantFrom(
	answer *v1.AcquireResponse,
	stream *connect.ServerStreamForClient[v1.AcquireResponse],
	stderr io.Writer,
) (*v1.AcquireGranted, error) {
	if granted := answer.GetGranted(); granted != nil {
		return granted, nil
	}
	return awaitGrant(stream, stderr)
}

// endError returns why a stream ended: its error, or errStreamEnded when it ended without one.
func endError(err error) error {
	if err == nil {
		return errStreamEnded
	}
	return err
}

// holdGrant holds the grant while the command runs. It reads the grant's stream until a stop arrives, or until the
// stream ends. When the stream ends, it reclaims the grant by grantID. A reclaim whose first answer grants grantID
// again is read the same way. Any other first answer means the grant is lost, and holdGrant returns it with the
// reclaim's stream without reading on. It returns the stop with the stream that carried it, or the lost grant's answer,
// or the error that refused a reclaim, or ctx's error once ctx ends.
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
		answer, reclaimed, err := reclaim(ctx, cfg, grantID)
		if err != nil {
			return holdResult{err: err}
		}
		if granted := answer.GetGranted(); granted != nil && granted.GetGrantId() == grantID {
			stream = reclaimed
			continue
		}
		return holdResult{stream: reclaimed, answer: answer}
	}
}

// rejoin reclaims grantID after its command stopped, and waits at the grant's place in line until orreryd grants it
// again. Its wait ends with ctx: a grant that is granted after ctx ended is closed and not taken. It returns the
// reclaimed grant with the context its stream is open under.
func rejoin(ctx context.Context, cfg RunConfig, grantID string) (context.Context, heldGrant, error) {
	waitCtx, endWait := context.WithCancel(context.WithoutCancel(ctx))
	stopWait := context.AfterFunc(ctx, endWait)
	granted, stream, err := reclaimUntilGranted(waitCtx, cfg, grantID)
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

// reclaimUntilGranted reclaims grantID until orreryd grants it, and returns the grant and the stream that holds it. A
// reclaim whose stream ends before its grant is reclaimed again. It returns the error that refused a reclaim, or ctx's
// error when ctx ends first.
func reclaimUntilGranted(
	ctx context.Context,
	cfg RunConfig,
	grantID string,
) (*v1.AcquireGranted, *connect.ServerStreamForClient[v1.AcquireResponse], error) {
	for {
		answer, stream, err := reclaim(ctx, cfg, grantID)
		if err != nil {
			return nil, nil, err
		}
		granted, err := grantFrom(answer, stream, cfg.Stderr)
		if err == nil {
			return granted, stream, nil
		}
		closeStream(stream, cfg.Stderr)
	}
}

// reclaim asks orreryd for the grant by grantID, every Retry, until its stream answers with an event, and returns that
// first event with the stream. The reclaim is cfg.Request with its grant_id set. It returns the error that refused the
// reclaim, or ctx's error when ctx ends first. Any other failure is retried.
func reclaim(
	ctx context.Context,
	cfg RunConfig,
	grantID string,
) (*v1.AcquireResponse, *connect.ServerStreamForClient[v1.AcquireResponse], error) {
	req := proto.CloneOf(cfg.Request)
	req.GrantId = grantID
	for {
		select {
		case <-ctx.Done():
			return nil, nil, fmt.Errorf("reclaim the grant: %w", ctx.Err())
		case <-time.After(cfg.Retry):
		}
		answer, stream, err := reclaimOnce(ctx, cfg, req)
		if err == nil {
			return answer, stream, nil
		}
		if refusedReclaim(err) {
			return nil, nil, err
		}
	}
}

// reclaimOnce sends one reclaim and returns the first event its stream answers with, and the stream. A reclaim that
// fails before an event closes its stream.
func reclaimOnce(
	ctx context.Context,
	cfg RunConfig,
	req *v1.AcquireRequest,
) (*v1.AcquireResponse, *connect.ServerStreamForClient[v1.AcquireResponse], error) {
	stream, err := cfg.Pools.Acquire(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, nil, fmt.Errorf("reclaim the grant: %w", err)
	}
	answer, err := nextEvent(stream, cfg.Stderr)
	if err != nil {
		closeStream(stream, cfg.Stderr)
		return nil, nil, err
	}
	return answer, stream, nil
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
// again under the grant orreryd grants then. When the grant is lost, CMD is stopped and the reclaim that lost it waits
// at its place in line, and CMD runs again under the grant that reclaim is granted. The grant that is held when
// runUnderGrant returns is ended first, after its command has exited.
func runUnderGrant(ctx, grantCtx context.Context, cfg RunConfig, path string, held heldGrant) int {
	status, stopped := runOnce(ctx, grantCtx, cfg, path, held)
	if stopped == nil {
		return status
	}
	var (
		nextCtx context.Context
		next    heldGrant
		err     error
	)
	if stopped.answer == nil {
		nextCtx, next, err = rejoin(ctx, cfg, held.granted.GetGrantId())
	} else {
		nextCtx, next, err = resumeLost(ctx, grantCtx, cfg, held, stopped)
	}
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

// resumeLost waits for the grant that a lost reclaim holds, once CMD has been stopped. The reclaim's stream is open
// under grantCtx, so grantCtx is not ended until the grant is taken or ctx ends. A stream that ends before its grant is
// reclaimed by the grant's id, as rejoin does. It returns the granted grant with the context its stream is open under.
func resumeLost(
	ctx, grantCtx context.Context,
	cfg RunConfig,
	held heldGrant,
	lost *holdResult,
) (context.Context, heldGrant, error) {
	stopWait := context.AfterFunc(ctx, held.end)
	granted, err := grantFrom(lost.answer, lost.stream, cfg.Stderr)
	if !stopWait() {
		// ctx ended while the place was waited on, so the grant is closed and not taken.
		if err == nil {
			closeStream(lost.stream, cfg.Stderr)
		}
		return nil, heldGrant{}, fmt.Errorf("wait for the grant: %w", ctx.Err())
	}
	if err != nil {
		closeStream(lost.stream, cfg.Stderr)
		held.end()
		return rejoin(ctx, cfg, held.granted.GetGrantId())
	}
	return grantCtx, heldGrant{granted: granted, stream: lost.stream, end: held.end}, nil
}

// runOnce starts path under held and holds the grant while it runs. grantCtx is the context held's stream is open under.
// It returns CMD's exit status, and the holdResult that stopped CMD when the grant stopped, or nil. When the grant
// stopped, CMD has been stopped and has exited. A stop ends the grant. A lost grant does not end grantCtx, since the
// reclaim's stream is open under it. When ctx ends, CMD gets SIGTERM and its status is returned. When orreryd refuses a
// reclaim, CMD is stopped as a stop is, and ExitNoGrant is returned. When Limit passes while CMD runs, or while it is
// being stopped, its group gets SIGKILL, ExitLimit is returned, and CMD is not run again.
func runOnce(ctx, grantCtx context.Context, cfg RunConfig, path string, held heldGrant) (int, *holdResult) {
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
		return exitCannotRun, nil
	}
	limit := runLimit(cfg)
	result := make(chan holdResult, 1)
	go func() { result <- holdGrant(grantCtx, cfg, held.granted.GetGrantId(), held.stream) }()
	select {
	case end := <-exited:
		held.end()
		<-result
		return end.report(cfg.Stderr, cfg.Limit), nil
	case <-limit:
		killErr := killGroup(pgid)
		finished := <-exited
		finished.limited = true
		held.end()
		<-result
		if killErr != nil {
			fmt.Fprintf(cfg.Stderr, "orrery run: %v\n", killErr)
		}
		return finished.report(cfg.Stderr, cfg.Limit), nil
	case <-ctx.Done():
		finished, stopErr := terminateOnCancel(pgid, exited, limit)
		held.end()
		// holdGrant may still be running, so it is waited for here and writes nothing after this returns.
		<-result
		if stopErr != nil {
			fmt.Fprintf(cfg.Stderr, "orrery run: %v\n", stopErr)
		}
		return finished.report(cfg.Stderr, cfg.Limit), nil
	case res := <-result:
		if res.err != nil {
			finished, stopErr := stopCommand(pgid, exited, cfg.StopGrace, cfg.After, limit)
			held.end()
			if stopErr != nil {
				fmt.Fprintf(cfg.Stderr, "orrery run: %v\n", stopErr)
			}
			status := finished.report(cfg.Stderr, cfg.Limit)
			fmt.Fprintf(
				cfg.Stderr,
				"orrery run: the grant ended while CMD ran: orreryd refused to reclaim it: %v\n",
				res.err,
			)
			if finished.limited {
				return status, nil
			}
			return ExitNoGrant, nil
		}
		// The stop is written after CMD has exited, because CMD's output may still be copying to stderr until then.
		end, stopErr := stopCommand(pgid, exited, cfg.StopGrace, cfg.After, limit)
		if stopErr != nil {
			fmt.Fprintf(cfg.Stderr, "orrery run: %v\n", stopErr)
		}
		if end.limited {
			// A run its limit killed is not run again, so the grant and any reclaim of it are ended here.
			closeStream(res.stream, cfg.Stderr)
			held.end()
			return end.report(cfg.Stderr, cfg.Limit), nil
		}
		if res.answer != nil {
			fmt.Fprintln(cfg.Stderr, "orrery run: the grant was not reclaimed intact: stopped CMD")
			return end.report(cfg.Stderr, cfg.Limit), &res
		}
		closeStream(res.stream, cfg.Stderr)
		held.end()
		members := strings.Join(res.stop.GetMembers(), ", ")
		fmt.Fprintf(cfg.Stderr, "orrery run: members %s were held: stopped CMD\n", members)
		return end.report(cfg.Stderr, cfg.Limit), &res
	}
}

// runLimit returns the channel that receives once the run's Limit has passed, counted from now. It returns nil when the
// run has no limit, and a nil channel never receives.
func runLimit(cfg RunConfig) <-chan time.Time {
	if cfg.Limit == 0 {
		return nil
	}
	return cfg.After(cfg.Limit)
}

// stopCommand sends SIGTERM to CMD's group, then waits for CMD to exit and for the rest of its group to be empty. The
// grace starts when SIGTERM is sent. When CMD or its group is still running once the grace has passed on after, the
// group gets SIGKILL and stopCommand waits until CMD has exited and the group is empty. When limit passes before CMD
// has exited, the group gets SIGKILL at once and the end is limited. A nil limit never passes. It returns CMD's end and
// the errors from signaling its group.
func stopCommand(
	pgid int,
	exited <-chan commandEnd,
	grace time.Duration,
	after func(d time.Duration) <-chan time.Time,
	limit <-chan time.Time,
) (commandEnd, error) {
	termErr := signalGroup(pgid, syscall.SIGTERM)
	expired := after(grace)
	select {
	case end := <-exited:
		if waitGroupGone(pgid, expired) {
			return end, termErr
		}
		return end, errors.Join(termErr, killGroup(pgid))
	case <-expired:
		killErr := killGroup(pgid)
		return <-exited, errors.Join(termErr, killErr)
	case <-limit:
		killErr := killGroup(pgid)
		end := <-exited
		end.limited = true
		return end, errors.Join(termErr, killErr)
	}
}

// terminateOnCancel sends SIGTERM to CMD's group when the run's context ends, and waits for CMD to exit. When limit
// passes first, the group gets SIGKILL and the end is limited. A nil limit never passes. It returns CMD's end and the
// errors from signaling its group.
func terminateOnCancel(pgid int, exited <-chan commandEnd, limit <-chan time.Time) (commandEnd, error) {
	termErr := signalGroup(pgid, syscall.SIGTERM)
	select {
	case end := <-exited:
		return end, termErr
	case <-limit:
		killErr := killGroup(pgid)
		end := <-exited
		end.limited = true
		return end, errors.Join(termErr, killErr)
	}
}

// killGroup sends SIGKILL to the group pgid and waits until the group is empty. It returns the signal's error.
func killGroup(pgid int) error {
	err := signalGroup(pgid, syscall.SIGKILL)
	waitGroupGone(pgid, nil)
	return err
}

// waitGroupGone waits until no process is left in the group pgid, and reports true. It reports false when expired
// fires first. A nil expired waits without a limit.
func waitGroupGone(pgid int, expired <-chan time.Time) bool {
	for !groupGone(pgid) {
		select {
		case <-expired:
			return false
		case <-time.After(stopPollInterval):
		}
	}
	return true
}

// groupGone reports whether no process is left in the group pgid.
func groupGone(pgid int) bool {
	return errors.Is(syscall.Kill(-pgid, 0), syscall.ESRCH)
}

// report writes the end's wait error to stderr, if it has one, and returns the exit status. When the limit killed CMD,
// it writes the limit's line and returns ExitLimit.
func (end commandEnd) report(stderr io.Writer, limit time.Duration) int {
	if end.err != nil {
		fmt.Fprintf(stderr, "orrery run: %v\n", end.err)
	}
	if end.limited {
		fmt.Fprintf(stderr, "orrery run: CMD ran past its limit of %v and was killed\n", limit)
		return ExitLimit
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
