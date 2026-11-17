package holder

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

// procSelfCgroup is the file that names the cgroup the holder runs in.
const procSelfCgroup = "/proc/self/cgroup"

// cgroupRoot is where the cgroup v2 hierarchy is mounted.
const cgroupRoot = "/sys/fs/cgroup"

// memoryCap reads the memory accounting of the scope a holder runs in: the oom_kill count of its memory.events, which
// counts each process the kernel killed in the scope, and its memory.max, the cap. Both are read under root, the cgroup v2
// mount, for the cgroup that the file at self names.
type memoryCap struct {
	self string
	root string
}

// systemMemoryCap returns the memoryCap of the running holder.
func systemMemoryCap() memoryCap {
	return memoryCap{self: procSelfCgroup, root: cgroupRoot}
}

// dir returns the directory of the holder's cgroup, and false when the holder runs in no holder scope: the file at
// self names no cgroup v2 group, or the group's name is not orrery-hold-*.scope, as Launch names a scope. A holder
// started plainly runs in its parent's group, whose events are not its own, so it reads none.
func (m memoryCap) dir() (string, bool) {
	data, err := os.ReadFile(m.self)
	if err != nil {
		return "", false
	}
	for line := range strings.Lines(string(data)) {
		group, ok := strings.CutPrefix(strings.TrimRight(line, "\n"), "0::")
		if !ok {
			continue
		}
		name := path.Base(group)
		if !strings.HasPrefix(name, scopeUnitPrefix) || !strings.HasSuffix(name, ".scope") {
			return "", false
		}
		return filepath.Join(m.root, filepath.FromSlash(group)), true
	}
	return "", false
}

// kills returns the oom_kill count of the holder's scope, and false when the holder runs in no holder scope or the
// count cannot be read.
func (m memoryCap) kills() (int, bool) {
	dir, ok := m.dir()
	if !ok {
		return 0, false
	}
	data, err := os.ReadFile(filepath.Clean(filepath.Join(dir, "memory.events")))
	if err != nil {
		return 0, false
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		value, found := strings.CutPrefix(scanner.Text(), "oom_kill ")
		if !found {
			continue
		}
		count, convErr := strconv.Atoi(strings.TrimSpace(value))
		return count, convErr == nil
	}
	return 0, false
}

// limit returns the holder scope's memory.max as the file gives it, a count of bytes or "max", and "unknown" when it
// cannot be read.
func (m memoryCap) limit() string {
	dir, ok := m.dir()
	if !ok {
		return "unknown"
	}
	data, err := os.ReadFile(filepath.Clean(filepath.Join(dir, "memory.max")))
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(data))
}

// memoryCapLine is the line the holder adds to the exit record's stderr when the scope's memory cap killed kills
// processes while the child ran. limit is the scope's memory.max.
func memoryCapLine(kills int, limit string) string {
	noun := "processes"
	if kills == 1 {
		noun = "process"
	}
	return fmt.Sprintf("orrery holder: the scope's memory cap (memory.max %s) killed %d %s", limit, kills, noun)
}
