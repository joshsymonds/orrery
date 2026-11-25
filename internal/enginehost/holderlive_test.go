package enginehost

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/joshsymonds/orrery/internal/holder"
)

const (
	// statFlagsField is the index of field 9, the kernel flags, among the fields after a stat line's command name.
	statFlagsField = 9 - 3
	// statExiting is the kernel's PF_EXITING flag. The kernel sets it when a process starts to exit, before it closes the
	// process's files and so frees its locks, and well before the process is a zombie.
	statExiting = 0x4
)

// holderLive reports whether the holder pids names runs, as holder.Running reports it, and has not begun to exit. A
// killed holder frees its session lock while it exits, before /proc shows it a zombie, so a caller that saw its lock
// free sees it exiting here, never live.
func holderLive(pids holder.Pids) (bool, error) {
	running, err := holder.Running(pids.Holder, pids.HolderStart)
	if err != nil || !running {
		return false, err
	}
	raw, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pids.Holder), "stat"))
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ESRCH) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read /proc/%d/stat: %w", pids.Holder, err)
	}
	fields := strings.Fields(string(raw[bytes.LastIndexByte(raw, ')')+1:]))
	if len(fields) <= statFlagsField {
		return false, fmt.Errorf(
			"/proc/%d/stat has %d fields after its command name: %q", pids.Holder, len(fields), raw,
		)
	}
	flags, err := strconv.ParseUint(fields[statFlagsField], 10, 64)
	if err != nil {
		return false, fmt.Errorf("/proc/%d/stat flags %q: %w", pids.Holder, fields[statFlagsField], err)
	}
	return flags&statExiting == 0, nil
}
