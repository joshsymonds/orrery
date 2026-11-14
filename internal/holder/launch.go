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
}

// Launch starts the session's child under orrery holder and returns the child as a Process. When cfg.Scope is set, the
// holder runs in its own user scope, and a scope that fails fails the launch with its reason, with no plain start in
// its place. Launch returns once the holder serves its socket and the attach at offset 0 succeeds. A holder that ends
// before it serves fails the launch with its status and stderr.
func Launch(ctx context.Context, cfg LaunchConfig) (*Process, error) {
	if cfg.GateLimit > 0 && cfg.StateDir == "" {
		return nil, errors.New("holder: a gate launch needs a state directory")
	}
	if cfg.Clock == nil {
		cfg.Clock = SystemClock{}
	}
	orrery, err := holderBinary(cfg.Orrery)
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
	path, args := holderCommand(cfg, orrery, stateDir)
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
	return newProcess(held, conn, sock, remove), nil
}

// holderCommand returns the program and argument vector that start the holder: orrery holder, with the session, the
// state directory, a positive gate limit as --gate-limit with a positive gate tail as --gate-tail, and the child's path
// and arguments after the separator. When cfg.Scope is set, the program is the scope, and orrery's argv follows the
// scope's arguments.
func holderCommand(cfg LaunchConfig, orrery, stateDir string) (string, []string) {
	argv := []string{"holder", "--session", cfg.Session, "--state-dir", stateDir}
	if cfg.GateLimit > 0 {
		argv = append(argv, "--gate-limit", strconv.FormatInt(gateSeconds(cfg.GateLimit), 10))
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
		"--user", "--scope", "--collect", "-p", "OOMPolicy=continue", "--unit", "orrery-hold-" + cfg.Session, orrery,
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

// holderBinary returns the absolute path of the orrery binary that runs a holder. An empty orrery takes the default.
func holderBinary(orrery string) (string, error) {
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
