package exectest

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

// KilledOrGone reports whether process pid has left the process table, is a zombie, or holds a pending SIGKILL. The
// kernel sends a child its parent-death signal while the parent exits, before the parent can be reaped, so once a
// test has reaped a parent, KilledOrGone of a child that asked for SIGKILL at its parent's death is true at once.
func KilledOrGone(tb testing.TB, pid int) bool {
	tb.Helper()
	status, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ESRCH) {
		return true
	}
	if err != nil {
		tb.Fatalf("read status of %d: %v", pid, err)
	}
	const sigkillBit = uint64(1) << (syscall.SIGKILL - 1)
	for line := range strings.SplitSeq(string(status), "\n") {
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch name {
		case "State":
			if strings.HasPrefix(value, "Z") || strings.HasPrefix(value, "X") {
				return true
			}
		case "SigPnd", "ShdPnd":
			mask, parseErr := strconv.ParseUint(value, 16, 64)
			if parseErr != nil {
				tb.Fatalf("parse %s %q of %d: %v", name, value, pid, parseErr)
			}
			if mask&sigkillBit != 0 {
				return true
			}
		}
	}
	return false
}
