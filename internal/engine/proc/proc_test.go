package proc

import (
	"bufio"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

const (
	lineTimeout = 10 * time.Second
	goneTimeout = 5 * time.Second
)

// echoLoop copies each stdin line to stdout until stdin closes.
const echoLoop = `while IFS= read -r line; do printf '%s\n' "$line"; done
`

// exitOnInterrupt installs an interrupt handler that exits 0 and waits in sleep. A shell blocked in
// read can miss the trap when the interrupt lands inside the read, so the fixture waits in sleep,
// which returns as soon as the interrupt reaches it.
const exitOnInterrupt = `trap 'exit 0' INT
echo ready
while :; do sleep 0.1; done
`

// ignoreInterrupt ignores SIGINT, then echoes stdin lines.
const ignoreInterrupt = `trap '' INT
echo ready
while IFS= read -r line; do printf '%s\n' "$line"; done
`

// grandchildOnInterrupt starts a background sleep, which a non-interactive shell leaves ignoring
// SIGINT, records its pid in the file named by $1, and then waits in sleep as exitOnInterrupt does.
const grandchildOnInterrupt = `trap 'exit 0' INT
sleep 60 &
echo $! > "$1"
echo ready
while :; do sleep 0.1; done
`

// writeFile writes content to a new file in a fresh temporary directory and returns its path.
func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

// shPath returns the absolute path of the sh that runs the fake child scripts.
func shPath(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("sh")
	if err != nil {
		t.Fatalf("find sh: %v", err)
	}
	return path
}

// startWith runs body as a shell script with args after the script path. It stops the process when
// the test ends, so no fake child outlives the test.
func startWith(t *testing.T, cfg Config, body string, args ...string) *Process {
	t.Helper()
	script := writeFile(t, "child.sh", body)
	cfg.Path = shPath(t)
	cfg.Args = append([]string{script}, args...)
	p, err := Start(cfg)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		if err := p.Stop(); err != nil {
			t.Errorf("cleanup Stop: %v", err)
		}
	})
	return p
}

// readLine returns the next line from r, failing the test when none arrives in time.
func readLine(t *testing.T, r *bufio.Reader) string {
	t.Helper()
	lines := make(chan string, 1)
	errs := make(chan error, 1)
	go func() {
		line, err := r.ReadString('\n')
		if err != nil {
			errs <- err
			return
		}
		lines <- line
	}()
	select {
	case line := <-lines:
		return line
	case err := <-errs:
		t.Fatalf("read line: %v", err)
	case <-time.After(lineTimeout):
		t.Fatal("timed out waiting for a line")
	}
	return ""
}

// readPID returns the process id that a fake child wrote into path.
func readPID(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read pid file: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("parse pid %q: %v", data, err)
	}
	return pid
}

// waitGone fails the test unless pid no longer exists within goneTimeout.
func waitGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(goneTimeout)
	for {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("pid %d still exists after %v: %v", pid, goneTimeout, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// waitDone fails the test unless the process is reaped within lineTimeout.
func waitDone(t *testing.T, p *Process) {
	t.Helper()
	select {
	case <-p.Done():
	case <-time.After(lineTimeout):
		t.Fatal("Done not closed after the child exited")
	}
}

// killedBy reports whether the exit error came from a signal sig.
func killedBy(err error, sig syscall.Signal) bool {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return false
	}
	status, ok := exitErr.Sys().(syscall.WaitStatus)
	return ok && status.Signaled() && status.Signal() == sig
}

func TestStartRejectsRelativePath(t *testing.T) {
	for _, path := range []string{"sh", "./bin/child", "bin/child"} {
		p, err := Start(Config{Path: path})
		if !errors.Is(err, ErrPathNotAbsolute) {
			t.Errorf("Start(%q) error = %v, want ErrPathNotAbsolute", path, err)
		}
		if p != nil {
			t.Errorf("Start(%q) returned a process for a relative path", path)
			if err := p.Stop(); err != nil {
				t.Errorf("Stop: %v", err)
			}
		}
	}
}

func TestChildRunsInItsOwnProcessGroup(t *testing.T) {
	p := startWith(t, Config{}, echoLoop)
	pid := p.Pid()
	pgid, err := syscall.Getpgid(pid)
	if err != nil {
		t.Fatalf("Getpgid(%d): %v", pid, err)
	}
	if pgid != pid {
		t.Errorf("process group = %d, want the child pid %d", pgid, pid)
	}
	if own := syscall.Getpgrp(); pgid == own {
		t.Errorf("child shares the test's process group %d", own)
	}
}

func TestStdinLinesComeBackOnStdout(t *testing.T) {
	p := startWith(t, Config{}, echoLoop)
	out := bufio.NewReader(p.Stdout())
	for _, want := range []string{"alpha\n", "beta\n"} {
		if _, err := io.WriteString(p.Stdin(), want); err != nil {
			t.Fatalf("write %q to stdin: %v", want, err)
		}
		if got := readLine(t, out); got != want {
			t.Errorf("stdout line = %q, want %q", got, want)
		}
	}
}

func TestStopInterruptsChildThatExitsOnSIGINT(t *testing.T) {
	p := startWith(t, Config{}, exitOnInterrupt)
	out := bufio.NewReader(p.Stdout())
	if got := readLine(t, out); got != "ready\n" {
		t.Fatalf("first line = %q, want ready", got)
	}
	if err := p.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	// The child exits 0 from its interrupt handler. A kill would make Wait report a signal.
	if err := p.Wait(); err != nil {
		t.Errorf("Wait = %v, want nil after the interrupt handler exited 0", err)
	}
}

func TestStopKillsChildThatIgnoresSIGINT(t *testing.T) {
	const timeout = 300 * time.Millisecond
	p := startWith(t, Config{StopTimeout: timeout}, ignoreInterrupt)
	out := bufio.NewReader(p.Stdout())
	if got := readLine(t, out); got != "ready\n" {
		t.Fatalf("first line = %q, want ready", got)
	}
	start := time.Now()
	if err := p.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if elapsed := time.Since(start); elapsed < timeout {
		t.Errorf("Stop returned after %v, before the %v StopTimeout", elapsed, timeout)
	}
	if err := p.Wait(); !killedBy(err, syscall.SIGKILL) {
		t.Errorf("Wait = %v, want an exit by SIGKILL", err)
	}
}

func TestStopKillsBackgroundGrandchild(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "grandchild.pid")
	p := startWith(t, Config{}, grandchildOnInterrupt, pidFile)
	out := bufio.NewReader(p.Stdout())
	if got := readLine(t, out); got != "ready\n" {
		t.Fatalf("first line = %q, want ready", got)
	}
	grandchild := readPID(t, pidFile)
	if err := p.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	waitGone(t, grandchild)
}

func TestStderrKeepsLastCapacityBytes(t *testing.T) {
	const capacity = 16
	payload := "BEGIN" + strings.Repeat("-", 1000) + "end-marker-xyz"
	payloadFile := writeFile(t, "stderr.txt", payload)
	p := startWith(t, Config{StderrCapacity: capacity}, `cat "$1" >&2`+"\n", payloadFile)
	if err := p.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	want := payload[len(payload)-capacity:]
	if got := p.Stderr(); got != want {
		t.Errorf("Stderr() = %q, want the last %d bytes %q", got, capacity, want)
	}
}

func TestChildExitingOnItsOwnClosesDoneAndWaitReportsStatus(t *testing.T) {
	p := startWith(t, Config{}, "exit 3\n")
	waitDone(t, p)
	var exitErr *exec.ExitError
	err := p.Wait()
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 3 {
		t.Errorf("Wait = %v, want exit status 3", err)
	}
	if err := p.Stop(); err != nil {
		t.Errorf("Stop after a self exit = %v, want nil", err)
	}
}

func TestSelfExitKillsRestOfGroup(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "grandchild.pid")
	p := startWith(t, Config{}, "sleep 60 &\necho $! > \"$1\"\nexit 0\n", pidFile)
	waitDone(t, p)
	waitGone(t, readPID(t, pidFile))
	if err := p.Wait(); err != nil {
		t.Errorf("Wait = %v, want nil", err)
	}
}

func TestStopIsIdempotent(t *testing.T) {
	p := startWith(t, Config{}, echoLoop)
	if err := p.Stop(); err != nil {
		t.Errorf("first Stop: %v", err)
	}
	if err := p.Stop(); err != nil {
		t.Errorf("second Stop: %v", err)
	}
}
