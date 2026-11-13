package session

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/holdertest"
)

// holderClaude is a fake claude that writes its own pid to claude.pid in its working directory, under a
// temporary name renamed into place, and then waits in a loop. The shell running it execs claude, so its
// pid is the child's pid.
const holderClaude = `echo $$ > claude.pid.tmp && mv claude.pid.tmp claude.pid
while :; do sleep 0.1; done
`

func TestMain(m *testing.M) {
	os.Exit(holdertest.Main(m))
}

// parentPID returns the parent pid of pid, read from the ppid field of /proc/PID/stat.
func parentPID(t *testing.T, pid int) int {
	t.Helper()
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		t.Fatalf("read stat of pid %d: %v", pid, err)
	}
	stat := string(data)
	end := strings.LastIndexByte(stat, ')')
	if end < 0 {
		t.Fatalf("stat of pid %d has no command name: %q", pid, stat)
	}
	fields := strings.Fields(stat[end+1:])
	if len(fields) < 2 {
		t.Fatalf("stat of pid %d has %d fields after its name, want at least 2", pid, len(fields))
	}
	ppid, err := strconv.Atoi(fields[1])
	if err != nil {
		t.Fatalf("parse ppid %q of pid %d: %v", fields[1], pid, err)
	}
	return ppid
}

// cmdlineArgs returns the argument vector of pid, read from /proc/PID/cmdline.
func cmdlineArgs(t *testing.T, pid int) []string {
	t.Helper()
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		t.Fatalf("read cmdline of pid %d: %v", pid, err)
	}
	return strings.Split(strings.TrimSuffix(string(data), "\x00"), "\x00")
}

// TestStartRunsChildUnderHolderForSession pins that Start runs the child under orrery holder for the session:
// the child's parent is the holder process, with the session's name in its argv. The config names no scope, so
// the holder is started plainly, with orrery as its program.
func TestStartRunsChildUnderHolderForSession(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfg := startConfig(t, writeClaude(t, holderClaude), dir)
	startRealSession(t, cfg)

	claude := waitPID(t, filepath.Join(dir, "claude.pid"))
	holder := parentPID(t, claude)
	argv := cmdlineArgs(t, holder)
	want := []string{"holder", "--session", cfg.Name}
	if len(argv) < 4 || filepath.Base(argv[0]) != "orrery" || !slices.Equal(argv[1:4], want) {
		t.Fatalf("child pid %d has parent pid %d with argv %q, want orrery holder --session %s ...",
			claude, holder, argv, cfg.Name)
	}
}
