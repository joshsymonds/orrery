package hostexec

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

	"github.com/joshsymonds/orrery/internal/engine/proc"
	"github.com/joshsymonds/orrery/internal/holder"
)

// startIndex is the index of field 22, the start time, among the stat fields after the command name, which begin at
// field 3.
const startIndex = 22 - 3

// SessionDir returns the directory of session's holder files under holders, as holder.SessionDir does.
func (*Local) SessionDir(holders, session string) (string, error) {
	dir, err := holder.SessionDir(holders, session)
	if err != nil {
		return "", fmt.Errorf("hostexec: session dir: %w", err)
	}
	return dir, nil
}

// Launch starts cfg's child under a holder. A positive cfg.Limit makes the child a gate, whose holder keeps the session
// directory and its exit record for the caller to clear.
func (*Local) Launch(ctx context.Context, cfg Launch) (Held, error) {
	process, err := holder.Launch(ctx, holder.LaunchConfig{
		Scope:    cfg.Scope,
		StateDir: cfg.StateDir,
		Session:  cfg.Session,
		Child: proc.Config{
			Path: cfg.Path, Args: cfg.Args, Dir: cfg.Dir, Env: cfg.Env,
		},
		GateLimit:       cfg.Limit,
		GateLimitSignal: cfg.LimitSignal,
		GateTail:        cfg.Tail,
	})
	if err != nil {
		return nil, fmt.Errorf("hostexec: launch %s: %w", cfg.Session, err)
	}
	return process, nil
}

// Reattach takes over the gate holder that serves dir, with scope as its launch named it or empty when it was started
// plainly. It fails with an error matching ErrGone when no holder holds the session.
func (*Local) Reattach(ctx context.Context, dir, scope string) (Held, error) {
	process, err := holder.ReattachGate(ctx, dir, holder.WithScope(scope))
	if err != nil {
		return nil, fmt.Errorf("hostexec: reattach %s: %w", dir, err)
	}
	return process, nil
}

// Stop ends the holder that serves dir and leaves dir in place. A dir with no holder is not an error.
func (*Local) Stop(ctx context.Context, dir string) error {
	if err := holder.Stop(ctx, dir); err != nil {
		return fmt.Errorf("hostexec: stop %s: %w", dir, err)
	}
	return nil
}

// Clear stops the holder that serves dir, if one does, and removes dir with everything in it. A dir that does not exist
// is not an error.
func (*Local) Clear(ctx context.Context, dir string) error {
	if err := holder.Clear(ctx, dir); err != nil {
		return fmt.Errorf("hostexec: clear %s: %w", dir, err)
	}
	return nil
}

// ExitRecord returns the exit record that the holder of dir left. It reports false when dir holds no readable record.
func (*Local) ExitRecord(dir string) (Exit, bool) {
	gone, err := holder.OpenGone(dir, 0)
	if err != nil {
		return Exit{}, false
	}
	return gone.Exit()
}

// KillLeftovers kills the group of the child that dir's pids record names, when that child ended on its own and left
// processes running in its group. A dir with no pids record has nothing to kill. Nothing is signaled when the record has
// no ChildStart to check the child against, or when another process holds the child's pid.
func (*Local) KillLeftovers(dir string) error {
	pids, err := holder.ReadPids(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("hostexec: read pids: %w", err)
	}
	if pids.Child <= 0 {
		return fmt.Errorf("hostexec: pids in %s name no child", dir)
	}
	if pids.ChildStart == "" {
		return nil
	}
	held, err := pidHeldElsewhere(pids.Child, pids.ChildStart)
	if err != nil {
		return err
	}
	if held {
		return nil
	}
	return killGroup(pids.Child)
}

// pidHeldElsewhere reports whether a process other than the one that started at start holds pid. A zombie holds its pid
// as a live process does, and a pid that /proc holds no stat for is held by no process.
func pidHeldElsewhere(pid int, start string) (bool, error) {
	raw, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	// A process reaped between the open and the read fails the read with ESRCH; it is gone all the same.
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ESRCH) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("hostexec: read stat of pid %d: %w", pid, err)
	}
	got, err := statStart(string(raw))
	if err != nil {
		return false, fmt.Errorf("hostexec: stat of pid %d: %w", pid, err)
	}
	return got != start, nil
}

// statStart returns field 22, the start time, of a /proc/PID/stat line. The command name is in parentheses and may hold
// spaces and parentheses, so the fields are counted after the last ')'.
func statStart(stat string) (string, error) {
	end := strings.LastIndexByte(stat, ')')
	if end < 0 {
		return "", fmt.Errorf("hostexec: stat has no command name: %q", stat)
	}
	fields := strings.Fields(stat[end+1:])
	if len(fields) <= startIndex {
		return "", fmt.Errorf("hostexec: stat has %d fields after its command name: %q", len(fields), stat)
	}
	return fields[startIndex], nil
}

// killGroup kills every process in the group pgid. A group that already exited is not an error.
func killGroup(pgid int) error {
	if err := syscall.Kill(-pgid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("hostexec: kill group %d: %w", pgid, err)
	}
	return nil
}
