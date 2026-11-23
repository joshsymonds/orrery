package holder

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/joshsymonds/orrery/internal/engine/proc"
)

// LaunchTimeout is how long Launch waits for a holder to serve its socket.
const LaunchTimeout = 30 * time.Second

// launchDialEvery is how often Launch dials a holder's socket while the holder does not serve it yet.
const launchDialEvery = 10 * time.Millisecond

// holderStopTimeout is how long the holder gets to exit after its interrupt. It is twice the child's grace, so a
// fallback interrupt leaves the holder its child's grace before the kill.
const holderStopTimeout = 2 * DefaultGrace

// LaunchConfig describes one session's launch.
type LaunchConfig struct {
	// Orrery is the orrery binary that runs the holder. Empty takes orrery beside the running executable when that file
	// exists, else orrery on PATH. The path used is absolute.
	Orrery string
	// Scope is the absolute path of systemd-run, which runs the holder in its own user scope. Empty starts the holder
	// plainly.
	Scope string
	// StateDir is the state directory whose holders subdirectory holds the session. Empty takes a fresh directory that
	// is removed once the holder ends or the launch fails. A gate needs a StateDir, since its caller clears the session's
	// directory and no caller can reach a fresh one.
	StateDir string
	// Session names the session.
	Session string
	// Child is the session's child as session.Command builds it.
	Child proc.Config
	// Clock times the wait for the holder. Nil takes SystemClock{}.
	Clock Clock
	// GateLimit makes the child a gate when positive: the holder runs it with that limit, and the Process keeps the
	// session's directory, exit record included, once the holder ends, for the caller to clear. Zero launches the child
	// with no limit, and the directory is removed once the holder ends.
	GateLimit time.Duration
	// GateTail is the number of newest output bytes the gate keeps in its exit record, as ChildConfig.GateTail takes it.
	// Zero keeps the holder's default, and it is passed only with a positive GateLimit.
	GateTail int
	// GateLimitSignal is the FIFO whose opening for writing reaches the gate's limit, as ChildConfig.GateLimitSignal takes
	// it. It is a test seam, and the holder is passed it only with a positive GateLimit.
	GateLimitSignal string
}

// Launch starts the session's child under orrery holder and returns the child as a Process. When cfg.Scope is set, the
// holder runs in its own user scope, named for this launch, and a scope that fails fails the launch with its reason,
// with no plain start in its place. Every scope still loaded for the session, whatever its nonce, is stopped before the
// holder starts, and the Process stops the holder's scope once the holder has ended, so nothing the child started
// outlives its holder. Launch returns once the holder serves its socket and the attach at offset 0 succeeds. A holder
// that ends before it serves fails the launch with its status and stderr.
func Launch(ctx context.Context, cfg LaunchConfig) (*Process, error) {
	if cfg.GateLimit > 0 && cfg.StateDir == "" {
		return nil, errors.New("holder: a gate launch needs a state directory")
	}
	if cfg.Clock == nil {
		cfg.Clock = SystemClock{}
	}
	orrery, err := Binary(cfg.Orrery)
	if err != nil {
		return nil, err
	}
	stateDir, private := cfg.StateDir, cfg.StateDir == ""
	if private {
		if stateDir, err = os.MkdirTemp("", "orrery-hold-"); err != nil {
			return nil, fmt.Errorf("holder: make state dir: %w", err)
		}
	}
	held, err := launchIn(ctx, cfg, orrery, stateDir, private)
	if err != nil && private {
		if rmErr := os.RemoveAll(stateDir); rmErr != nil {
			err = errors.Join(err, fmt.Errorf("holder: remove %s: %w", stateDir, rmErr))
		}
	}
	return held, err
}

// launchIn starts the holder for cfg's session in stateDir and attaches to it. private reports whether Launch made
// stateDir, in which case the holder's Process removes stateDir rather than the session's directory.
func launchIn(ctx context.Context, cfg LaunchConfig, orrery, stateDir string, private bool) (*Process, error) {
	dir, err := SessionDir(filepath.Join(stateDir, holdersName), cfg.Session)
	if err != nil {
		return nil, err
	}
	sock, err := SocketPath(dir)
	if err != nil {
		return nil, err
	}
	if clearErr := Clear(ctx, dir); clearErr != nil {
		return nil, clearErr
	}
	// Clear has stopped any holder of the session, so a scope still loaded for it holds only what an ended holder's child
	// left running, such as a daemon it forked. Its nonce is not this launch's, so every loaded scope of the session is
	// stopped, whatever its nonce, before this launch starts.
	if stopErr := newScopeStopper(cfg.Scope, scopeGlob(cfg.Session)).stop(ctx); stopErr != nil {
		return nil, stopErr
	}
	var scope scopeStopper
	var unit string
	if cfg.Scope != "" {
		nonce, nonceErr := newScopeNonce()
		if nonceErr != nil {
			return nil, nonceErr
		}
		// The nonce is recorded before the holder starts, so a reattach to this holder can name its scope.
		if writeErr := writeScopeNonce(dir, nonce); writeErr != nil {
			return nil, writeErr
		}
		unit = scopeUnit(cfg.Session, nonce)
		scope = newScopeStopper(cfg.Scope, unit)
	}
	path, args := holderCommand(cfg, orrery, stateDir, unit)
	held, err := proc.Start(proc.Config{
		Path:        path,
		Args:        args,
		Dir:         cfg.Child.Dir,
		Env:         cfg.Child.Env,
		StopTimeout: holderStopTimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("holder: start holder for session %s: %w", cfg.Session, err)
	}
	if err = held.Stdin().Close(); err != nil {
		return nil, abandon(held, fmt.Errorf("holder: close holder stdin: %w", err))
	}
	go discard(held.Stdout())
	conn, err := dialServing(ctx, cfg.Clock, cfg.Session, held, sock)
	if err != nil {
		return nil, abandon(held, err)
	}
	if _, err = conn.Attach(0); err != nil {
		return nil, abandon(held, err)
	}
	// A gate's directory stays for its caller to clear, so its Process removes nothing.
	remove := ""
	if cfg.GateLimit <= 0 {
		remove = dir
		if private {
			remove = stateDir
		}
	}
	// The scope is stopped once the holder ends, long after the launch, so its stop does not end with ctx.
	return newAttachedProcess(context.WithoutCancel(ctx), held, conn, sock, remove, 0, Frame{}, scope), nil
}

// holderCommand returns the program and argument vector that start the holder: orrery holder, with the session, the
// state directory, a positive gate limit as --gate-limit with a non-empty gate limit signal as --gate-limit-signal and
// a positive gate tail as --gate-tail, and the child's path and arguments after the separator. When cfg.Scope is set,
// the program is the scope, named unit, and orrery's argv follows the scope's arguments. The scope's memory cap is the
// host's: a systemd drop-in for orrery-hold-*.scope units sets it, as it does for terminal scopes. OOMPolicy=continue
// keeps the scope, and with it the holder, running past a kill at that cap, so the holder records it in the exit
// record.
func holderCommand(cfg LaunchConfig, orrery, stateDir, unit string) (string, []string) {
	argv := []string{"holder", "--session", cfg.Session, "--state-dir", stateDir}
	if cfg.GateLimit > 0 {
		argv = append(argv, "--gate-limit", strconv.FormatInt(gateSeconds(cfg.GateLimit), 10))
		if cfg.GateLimitSignal != "" {
			argv = append(argv, "--gate-limit-signal", cfg.GateLimitSignal)
		}
		if cfg.GateTail > 0 {
			argv = append(argv, "--gate-tail", strconv.Itoa(cfg.GateTail))
		}
	}
	argv = append(argv, "--", cfg.Child.Path)
	argv = append(argv, cfg.Child.Args...)
	if cfg.Scope == "" {
		return orrery, argv
	}
	scoped := append([]string{
		"--user", "--scope", "--collect", "-p", "OOMPolicy=continue", "--unit", unit, orrery,
	}, argv...)
	return cfg.Scope, scoped
}

// gateSeconds returns limit in whole seconds, rounded up, as --gate-limit takes it.
func gateSeconds(limit time.Duration) int64 {
	seconds := int64(limit / time.Second)
	if limit%time.Second != 0 {
		seconds++
	}
	return seconds
}

// Binary returns the absolute path of the orrery binary that runs a holder. An empty orrery takes the default.
func Binary(orrery string) (string, error) {
	if orrery == "" {
		var err error
		if orrery, err = defaultOrrery(); err != nil {
			return "", err
		}
	}
	abs, err := filepath.Abs(orrery)
	if err != nil {
		return "", fmt.Errorf("holder: absolute path of orrery: %w", err)
	}
	return abs, nil
}

// defaultOrrery returns orrery beside the running executable when that file exists, and otherwise orrery on PATH.
func defaultOrrery() (string, error) {
	if exe, err := os.Executable(); err == nil {
		beside := filepath.Join(filepath.Dir(exe), "orrery")
		if _, statErr := os.Stat(beside); statErr == nil {
			return beside, nil
		}
	}
	found, err := exec.LookPath("orrery")
	if err != nil {
		return "", fmt.Errorf("holder: find orrery: %w", err)
	}
	return found, nil
}

// dialServing dials sock every launchDialEvery on clock until the holder serves it. It fails when held ends first, when
// LaunchTimeout passes on clock, or when ctx ends.
func dialServing(ctx context.Context, clock Clock, session string, held heldProcess, sock string) (*Conn, error) {
	deadline := clock.After(LaunchTimeout)
	for {
		conn, err := Dial(ctx, sock)
		if err == nil {
			return conn, nil
		}
		select {
		case <-held.Done():
			return nil, endedBeforeServing(session, held)
		case <-deadline:
			return nil, fmt.Errorf("holder for session %s did not serve within %v", session, LaunchTimeout)
		case <-ctx.Done():
			return nil, fmt.Errorf("holder for session %s: %w", session, ctx.Err())
		case <-clock.After(launchDialEvery):
		}
	}
}

// endedBeforeServing is the error for a holder that ended before it served its socket: its wait error, then its stderr.
// A holder that exited 0 has no wait error, so its status is named.
func endedBeforeServing(session string, held heldProcess) error {
	status := held.Wait()
	if status == nil {
		status = errors.New("exit status 0")
	}
	return fmt.Errorf("holder for session %s ended before serving: %w: %s",
		session, status, strings.TrimSpace(held.Stderr()))
}

// abandon stops held, the holder Launch started, and returns err, joined with the stop's error when the stop fails.
func abandon(held heldProcess, err error) error {
	if stopErr := held.Stop(); stopErr != nil {
		return errors.Join(err, fmt.Errorf("holder: stop holder: %w", stopErr))
	}
	return err
}

// discard reads the holder's stdout until it ends, then closes it. The holder writes nothing to its stdout, so a failed
// read ends the discard the same way the end of the stream does.
func discard(out io.ReadCloser) {
	defer out.Close()
	if _, err := io.Copy(io.Discard, out); err != nil {
		return
	}
}

// Live reports whether a holder holds the session in dir, which is whether the session's lock is held. A dir that does
// not exist has no holder, and neither does a dir whose lock file is missing: Live reports it not held and never creates
// the lock file.
func Live(dir string) (bool, error) {
	present, err := dirPresent(dir)
	if err != nil || !present {
		return false, err
	}
	return lockHeld(filepath.Join(dir, LockName))
}

// Ended waits until no holder holds the session in dir, then returns the exit record the holder wrote there, and whether
// it wrote one. A dir with no session lock has no holder, so Ended returns ErrGone for it, and so does a lock file that is
// removed while Ended waits. Ended never creates the lock file. It gives up when ctx ends first.
func Ended(ctx context.Context, dir string) (Exit, bool, error) {
	lock, err := awaitExistingLock(ctx, filepath.Join(dir, LockName))
	if err != nil {
		return Exit{}, false, fmt.Errorf("holder: wait for %s: %w", dir, err)
	}
	exit, found, err := readGoneExit(dir)
	if closeErr := lock.Close(); closeErr != nil {
		err = errors.Join(err, fmt.Errorf("holder: release lock of %s: %w", dir, closeErr))
	}
	return exit, found, err
}

// AttachError returns the error for an attach to the holder in dir that failed with cause. It is ErrGone when the holder's
// lock is free by now. Otherwise it wraps ErrUnattachable, and ErrNoAnswer as well when the attach ran out of its bound.
func AttachError(dir string, cause error) error {
	if errors.Is(cause, os.ErrDeadlineExceeded) {
		cause = fmt.Errorf("%w: %w", ErrNoAnswer, cause)
	}
	live, err := Live(dir)
	switch {
	case err != nil:
		return unattachable(errors.Join(cause, err))
	case !live:
		return errors.Join(ErrGone, cause)
	default:
		return unattachable(cause)
	}
}

// Keeper is a controller that keeps a live holder attached while no controller is, as Keep makes it. Done closes once the
// keeper has ended, and Err then gives why.
type Keeper struct {
	done chan struct{}
	err  error
}

// Keep attaches to the live holder serving dir as a controller that reads nothing back, so the holder keeps a controller
// and does not idle-stop. The attach is at the end of the holder's spool. The keeper reads each out frame and discards it,
// and never acks, so the spool keeps every byte above the last ack that a controller sent. When the holder's child ends,
// the keeper waits for the holder's lock to be free and then stops the holder's scope, as a launched Process does. Keep
// returns ErrGone when no holder holds the session in dir, and the error AttachError gives when the attach fails. ctx ends
// the keeper.
func Keep(ctx context.Context, dir, scope string) (*Keeper, error) {
	live, err := Live(dir)
	switch {
	case err != nil:
		return nil, err
	case !live:
		return nil, ErrGone
	}
	end, err := spoolEnd(dir)
	if err != nil {
		return nil, err
	}
	sock, err := SocketPath(dir)
	if err != nil {
		return nil, err
	}
	stopper, err := readScopeStopper(dir, scope)
	if err != nil {
		return nil, err
	}
	conn, _, err := attachBounded(ctx, sock, end, WireVersion, AttachTimeout)
	if err != nil {
		return nil, AttachError(dir, err)
	}
	k := &Keeper{done: make(chan struct{})}
	go k.run(ctx, conn, dir, stopper)
	return k, nil
}

// Done closes once the keeper has ended: its ctx ended, a later attach replaced it, its holder ended, or its connection
// failed.
func (k *Keeper) Done() <-chan struct{} {
	return k.done
}

// Err returns why the keeper ended, or nil when its ctx ended, a later attach replaced it, or its holder ended cleanly. It
// is valid once Done has closed.
func (k *Keeper) Err() error {
	return k.err
}

// run reads the holder's frames and discards them until the connection ends. An exit frame means the child has ended, and
// the holder ends with it, so run then stops the holder's scope. ctx ends the connection.
func (k *Keeper) run(ctx context.Context, conn *Conn, dir string, stopper scopeStopper) {
	defer close(k.done)
	closed := make(chan error, 1)
	stop := context.AfterFunc(ctx, func() { closed <- conn.Close() })
	exited, err := drainFrames(conn)
	if !stop() {
		// ctx ended first, so its close of the connection is the one close, and its error is waited for here.
		err = errors.Join(err, <-closed)
	}
	closeErr := conn.Close()
	if exited {
		k.err = errors.Join(closeErr, stopAfterExit(ctx, dir, stopper))
		return
	}
	if ctx.Err() == nil {
		k.err = errors.Join(err, closeErr)
	}
}

// drainFrames reads frames from conn and discards each out frame, until the connection ends. It reports whether an exit
// frame ended the reading. A clean end of the connection is no error.
func drainFrames(conn *Conn) (bool, error) {
	for {
		frame, err := conn.Read()
		switch {
		case errors.Is(err, io.EOF):
			return false, nil
		case err != nil:
			return false, err
		case frame.Type == TypeExit:
			return true, nil
		case frame.Type == TypeError:
			return false, errors.New(frame.Message)
		}
	}
}

// stopAfterExit runs once the holder's child has exited. It waits, past ctx, until the holder has released its lock, and
// then stops the holder's scope, so a ctx that ends meanwhile does not leave the scope running. A holder whose directory
// is already gone has released its lock.
func stopAfterExit(ctx context.Context, dir string, stopper scopeStopper) error {
	ctx = context.WithoutCancel(ctx)
	if _, _, err := Ended(ctx, dir); err != nil && !errors.Is(err, ErrGone) {
		return err
	}
	return stopper.stop(ctx)
}

// spoolEnd returns the stdout offset just past the last byte the spool in dir holds.
func spoolEnd(dir string) (int64, error) {
	segs, err := goneSegments(dir)
	if err != nil {
		return 0, err
	}
	return segs[len(segs)-1].end(), nil
}
