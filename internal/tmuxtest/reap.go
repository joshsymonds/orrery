package tmuxtest

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// serverComm is the command name a tmux server process has: tmux names its server process so.
const serverComm = "tmux: server"

// reapGrace is how long Reap waits for an orphan to exit on SIGTERM before it sends SIGKILL.
const reapGrace = 2 * time.Second

// reapPoll is how often Reap looks for an orphan that was sent SIGTERM.
const reapPoll = 20 * time.Millisecond

// Orphan is a test's tmux server that has outlived the test binary that started it.
type Orphan struct {
	// PID is the server's process id.
	PID int
	// Name is the server's -L name.
	Name string
	// Owner is the pid of the test binary, taken from Name.
	Owner int
}

// Reaper stops the tmux servers that tests started and that outlived their test binaries: a binary killed, timed out
// or ended by a panic runs no cleanup, and a tmux server runs on as a daemon. A server is an orphan only when its
// command name is a tmux server's, its -L name is a test server's (orrery-<word>(-<word>)*-<pid>-<hex>, such as
// orrery-test-<pid>-<hex> or orrery-test-send-<pid>-<hex>), and that pid is no longer in the process table. A live
// owner's server never matches, so no other tmux is touched.
type Reaper struct {
	// ProcRoot is the process table, /proc in production.
	ProcRoot string
	// Signal sends sig to pid, syscall.Kill in production.
	Signal func(pid int, sig syscall.Signal) error
	// Exited reports whether process pid has left ProcRoot after a SIGTERM, waiting as long as the reaper allows.
	Exited func(pid int) bool
}

// DefaultReaper returns the Reaper over /proc that signals with syscall.Kill and gives each orphan reapGrace to exit
// on SIGTERM.
func DefaultReaper() Reaper {
	reaper := Reaper{ProcRoot: "/proc", Signal: syscall.Kill}
	reaper.Exited = func(pid int) bool {
		grace := time.After(reapGrace)
		poll := time.NewTicker(reapPoll)
		defer poll.Stop()
		for reaper.present(pid) {
			select {
			case <-grace:
				return !reaper.present(pid)
			case <-poll.C:
			}
		}
		return true
	}
	return reaper
}

// Reap sends SIGTERM to each orphan, and SIGKILL to one that is still the same server once Exited gives up on it. It
// returns the orphans it signaled. A process that is gone by the time it is signaled is skipped; any other failure is
// returned, joined, after every orphan has been tried.
func (r Reaper) Reap() ([]Orphan, error) {
	orphans, err := r.Orphans()
	if err != nil {
		return nil, err
	}
	var errs []error
	for _, orphan := range orphans {
		if termErr := r.Signal(orphan.PID, syscall.SIGTERM); termErr != nil {
			if !errors.Is(termErr, syscall.ESRCH) {
				errs = append(errs, fmt.Errorf("tmuxtest: SIGTERM tmux server %d (%s): %w", orphan.PID, orphan.Name,
					termErr))
			}
			continue
		}
		if r.Exited(orphan.PID) {
			continue
		}
		// The pid may have been reused while the server exited: kill only the same orphan server.
		if still, ok := r.orphanAt(orphan.PID); !ok || still != orphan {
			continue
		}
		if killErr := r.Signal(orphan.PID, syscall.SIGKILL); killErr != nil && !errors.Is(killErr, syscall.ESRCH) {
			errs = append(errs, fmt.Errorf("tmuxtest: SIGKILL tmux server %d (%s): %w", orphan.PID, orphan.Name,
				killErr))
		}
	}
	return orphans, errors.Join(errs...)
}

// Orphans returns the orphan test servers in ProcRoot, in pid order.
func (r Reaper) Orphans() ([]Orphan, error) {
	entries, err := fs.ReadDir(os.DirFS(r.ProcRoot), ".")
	if err != nil {
		return nil, fmt.Errorf("tmuxtest: read process table %s: %w", r.ProcRoot, err)
	}
	var orphans []Orphan
	for _, entry := range entries {
		pid, convErr := strconv.Atoi(entry.Name())
		if convErr != nil || pid <= 0 {
			continue
		}
		if orphan, ok := r.orphanAt(pid); ok {
			orphans = append(orphans, orphan)
		}
	}
	slices.SortFunc(orphans, func(a, b Orphan) int { return a.PID - b.PID })
	return orphans, nil
}

// orphanAt reports the orphan at pid, if the process there is one. A process that leaves the table while it is read is
// none.
func (r Reaper) orphanAt(pid int) (Orphan, bool) {
	proc := os.DirFS(r.ProcRoot)
	dir := strconv.Itoa(pid)
	comm, err := fs.ReadFile(proc, path.Join(dir, "comm"))
	if err != nil || strings.TrimSuffix(string(comm), "\n") != serverComm {
		return Orphan{}, false
	}
	cmdline, err := fs.ReadFile(proc, path.Join(dir, "cmdline"))
	if err != nil {
		return Orphan{}, false
	}
	name, ok := serverName(strings.Split(strings.TrimSuffix(string(cmdline), "\x00"), "\x00"))
	if !ok {
		return Orphan{}, false
	}
	owner, ok := testOwner(name)
	if !ok || owner == pid || r.present(owner) {
		return Orphan{}, false
	}
	return Orphan{PID: pid, Name: name, Owner: owner}, true
}

// present reports whether pid is in the process table.
func (r Reaper) present(pid int) bool {
	_, err := fs.Stat(os.DirFS(r.ProcRoot), strconv.Itoa(pid))
	return !errors.Is(err, fs.ErrNotExist)
}

// serverName returns the -L name of the tmux command line argv, when argv runs tmux with one.
func serverName(argv []string) (string, bool) {
	if len(argv) == 0 || filepath.Base(argv[0]) != "tmux" {
		return "", false
	}
	for i, arg := range argv[1:] {
		if arg == "-L" && i+2 < len(argv) {
			return argv[i+2], true
		}
		if name, ok := strings.CutPrefix(arg, "-L"); ok && name != "" {
			return name, true
		}
	}
	return "", false
}

// testOwner returns the pid of the test binary that named a test server name, when name is one:
// orrery-<word>(-<word>)*-<pid>-<hex>, each word lowercase letters, the pid decimal and the hex lowercase.
func testOwner(name string) (int, bool) {
	fields := strings.Split(name, "-")
	if len(fields) < 4 || fields[0] != "orrery" {
		return 0, false
	}
	pidText, hex := fields[len(fields)-2], fields[len(fields)-1]
	for _, word := range fields[1 : len(fields)-2] {
		if word == "" || !allIn(word, "abcdefghijklmnopqrstuvwxyz") {
			return 0, false
		}
	}
	if pidText == "" || hex == "" || !allIn(pidText, "0123456789") || !allIn(hex, "0123456789abcdef") {
		return 0, false
	}
	pid, err := strconv.Atoi(pidText)
	if err != nil || pid <= 0 {
		return 0, false
	}
	return pid, true
}

// allIn reports whether every byte of s is in set.
func allIn(s, set string) bool {
	for i := range len(s) {
		if !strings.ContainsRune(set, rune(s[i])) {
			return false
		}
	}
	return true
}
