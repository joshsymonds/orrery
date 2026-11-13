package session

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// startGuard bounds each wait for a child process to act. It is generous on purpose: a CI runner under
// -race is several times slower than a laptop, and the guard only turns a hang into a failure.
const startGuard = 60 * time.Second

// pollInterval is the pause between checks while a test waits for a child process.
const pollInterval = 10 * time.Millisecond

// grandchildClaude is a fake claude that records the session marker and its arguments in argv, starts
// a background sleep, which a non-interactive shell leaves ignoring SIGINT and which outlives
// startGuard, records the sleep's pid in grandchild.pid, and then waits in a loop. Both files land in
// its working directory and are written under a temporary name and renamed, so a reader never sees a
// partial file; argv is in place before grandchild.pid appears.
const grandchildClaude = `printf '%s\n' "$ORRERY_SESSION" "$*" > argv.tmp && mv argv.tmp argv
sleep 600 &
echo $! > grandchild.pid.tmp && mv grandchild.pid.tmp grandchild.pid
while :; do sleep 0.1; done
`

// exitClaude reads the initialize request, writes distinct text to stderr and exits with status 3.
// Reading first keeps the process alive until the session has written to it, so the write cannot race
// the exit.
const exitClaude = `IFS= read -r line
echo "boom: bad flag" >&2
exit 3
`

// writeClaude writes body as an executable fake claude, under a shebang that runs it with sh, and
// returns its absolute path.
func writeClaude(t *testing.T, body string) string {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Fatalf("find sh: %v", err)
	}
	path := filepath.Join(t.TempDir(), "claude")
	script := "#!" + sh + "\n" + body
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake claude: %v", err)
	}
	return path
}

// startConfig returns a session configuration that runs claude in dir with the path of sh as the login
// shell and the current PATH as the environment.
func startConfig(t *testing.T, claude, dir string) Config {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Fatalf("find sh: %v", err)
	}
	return Config{
		Shell:          sh,
		Claude:         claude,
		Dir:            dir,
		PermissionMode: testMode,
		Name:           testName,
		Environ:        []string{"PATH=" + os.Getenv("PATH")},
	}
}

// startRealSession starts a session for cfg and stops it when the test ends.
func startRealSession(t *testing.T, cfg Config) *Session {
	t.Helper()
	s, err := Start(t.Context(), cfg)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Stop(); err != nil {
			t.Errorf("Stop in cleanup: %v", err)
		}
	})
	return s
}

// waitPID returns the pid that the fake claude writes to path, waiting for the file to appear.
func waitPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(startGuard)
	for {
		if data, err := os.ReadFile(path); err == nil {
			pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
			if err != nil {
				t.Fatalf("parse pid %q: %v", data, err)
			}
			return pid
		}
		if time.Now().After(deadline) {
			t.Fatalf("pid file %s did not appear within %v", path, startGuard)
		}
		time.Sleep(pollInterval)
	}
}

// waitGone fails the test unless pid no longer exists within startGuard.
func waitGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(startGuard)
	for {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("pid %d still exists after %v: %v", pid, startGuard, err)
		}
		time.Sleep(pollInterval)
	}
}

// collectEvents returns every event up to the close of Events, failing the test if Events does not
// close within startGuard.
func collectEvents(t *testing.T, s *Session) []Event {
	t.Helper()
	var events []Event
	deadline := time.After(startGuard)
	for {
		select {
		case event, ok := <-s.Events():
			if !ok {
				return events
			}
			events = append(events, event)
		case <-deadline:
			t.Fatalf("Events did not close within %v after %d events", startGuard, len(events))
		}
	}
}

// requireCommandReached fails the test unless the fake claude recorded, in the file at path, the
// session marker Command sets and the claude arguments Command builds for cfg.
func requireCommandReached(t *testing.T, path string, cfg Config) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read recorded argv: %v", err)
	}
	claudeArgs := wantSessionArgs(cfg.Claude, cfg.PermissionMode)[5:]
	want := cfg.Name + "\n" + strings.Join(claudeArgs, " ") + "\n"
	if string(data) != want {
		t.Errorf("fake claude recorded %q, want %q", data, want)
	}
}

func TestStartRunsSessionInDirAndStopEndsGroup(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfg := startConfig(t, writeClaude(t, grandchildClaude), dir)
	s := startRealSession(t, cfg)

	pidPath := filepath.Join(dir, "grandchild.pid")
	grandchild := waitPID(t, pidPath)
	requireCommandReached(t, filepath.Join(dir, "argv"), cfg)
	if err := s.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	waitGone(t, grandchild)
}

func TestStartProcessExitReportsStatusAndStderr(t *testing.T) {
	t.Parallel()
	cfg := startConfig(t, writeClaude(t, exitClaude), t.TempDir())
	s := startRealSession(t, cfg)

	events := collectEvents(t, s)
	if len(events) != 1 {
		t.Fatalf("got events %#v, want only Exited", events)
	}
	exited, ok := events[0].(Exited)
	if !ok {
		t.Fatalf("event = %#v, want Exited", events[0])
	}
	var exitErr *exec.ExitError
	if !errors.As(exited.ExitErr, &exitErr) || exitErr.ExitCode() != 3 {
		t.Errorf("ExitErr = %v, want exit status 3", exited.ExitErr)
	}
	if want := "boom: bad flag\n"; exited.Stderr != want {
		t.Errorf("Stderr = %q, want %q", exited.Stderr, want)
	}
}

func TestStartInvalidConfigReturnsCommandError(t *testing.T) {
	t.Parallel()
	cfg := startConfig(t, writeClaude(t, exitClaude), t.TempDir())
	cfg.PermissionMode = ""
	_, wantErr := Command(cfg)
	if wantErr == nil {
		t.Fatal("Command accepted a config without a permission mode")
	}

	s, err := Start(t.Context(), cfg)
	if s != nil {
		t.Errorf("Start returned a session for an invalid config")
		if stopErr := s.Stop(); stopErr != nil {
			t.Errorf("Stop: %v", stopErr)
		}
	}
	if err == nil || err.Error() != wantErr.Error() {
		t.Errorf("Start error = %v, want Command's error %v", err, wantErr)
	}
}
