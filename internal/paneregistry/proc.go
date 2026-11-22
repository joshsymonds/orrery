package paneregistry

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// Proc is the view of processes a binding reads: whether a pid names a live process, and that process's environment.
type Proc interface {
	Alive(pid int) bool
	Environ(pid int) (map[string]string, error)
}

// SystemProc returns the Proc of the running system. Liveness is probed with signal 0, and the environment is read
// from /proc/<pid>/environ.
func SystemProc() Proc {
	return systemProc{}
}

type systemProc struct{}

// Alive reports whether pid names a live process. Signal 0 checks for the process without sending it a signal, and
// ESRCH means no such process. EPERM means the process exists but may not be signaled, so it is alive. A pid that is
// not positive is never alive: kill would address a process group, not a process.
func (systemProc) Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// Environ returns the environment of pid, read from /proc/<pid>/environ.
func (systemProc) Environ(pid int) (map[string]string, error) {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "environ"))
	if err != nil {
		return nil, fmt.Errorf("paneregistry: read environ of pid %d: %w", pid, err)
	}
	return parseEnviron(data), nil
}

// parseEnviron splits data, NUL-separated KEY=VALUE pairs as /proc/<pid>/environ holds them, into a map. A value may
// hold '=', so each pair is split at its first '='. A piece with no '=', such as the empty piece after a final NUL,
// is not a pair and is skipped.
func parseEnviron(data []byte) map[string]string {
	env := make(map[string]string)
	for pair := range strings.SplitSeq(string(data), "\x00") {
		key, value, ok := strings.Cut(pair, "=")
		if !ok {
			continue
		}
		env[key] = value
	}
	return env
}
