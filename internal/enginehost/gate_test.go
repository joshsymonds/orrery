package enginehost

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// gateHoldLock takes an exclusive flock on path the way the machine's heavy wrapper does, and returns
// the release that frees it.
func gateHoldLock(t *testing.T, path string) func() {
	t.Helper()
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatalf("open lock %s: %v", path, err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		t.Fatalf("hold lock %s: %v", path, err)
	}
	return func() { _ = file.Close() }
}

// gateEnv is the environment a test gate runs in: the test's PATH so its tools resolve, plus extra.
func gateEnv(extra ...string) []string {
	return append([]string{"PATH=" + os.Getenv("PATH")}, extra...)
}

// gateWaitFile polls until path exists with content and returns the trimmed content.
func gateWaitFile(t *testing.T, path string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil && strings.TrimSpace(string(data)) != "" {
			return strings.TrimSpace(string(data))
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("file %s never got content", path)
	return ""
}

// gateProcessGone reports whether pid has exited: no /proc entry, or a zombie waiting to be reaped.
func gateProcessGone(pid int) bool {
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return true
	}
	_, after, found := strings.Cut(string(stat), ") ")
	return found && (strings.HasPrefix(after, "Z") || strings.HasPrefix(after, "X"))
}

// gateWaitGone polls until every pid in pids has exited.
func gateWaitGone(t *testing.T, pids ...int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for _, pid := range pids {
		for !gateProcessGone(pid) {
			if time.Now().After(deadline) {
				t.Fatalf("process %d is still running after the gate was killed", pid)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
}

// gateParsePids reads the "$$ $!" pair a gate script writes: its own pid (the group id) and its
// background child's pid.
func gateParsePids(t *testing.T, path string) (int, int) {
	t.Helper()
	fields := strings.Fields(gateWaitFile(t, path))
	if len(fields) != 2 {
		t.Fatalf("pid file %s = %q, want two pids", path, strings.Join(fields, " "))
	}
	group, err := strconv.Atoi(fields[0])
	if err != nil {
		t.Fatalf("group pid: %v", err)
	}
	child, err := strconv.Atoi(fields[1])
	if err != nil {
		t.Fatalf("child pid: %v", err)
	}
	return group, child
}

func TestGateRunsArgvInDirAndReportsExitAndOutput(t *testing.T) {
	dir := t.TempDir()
	lock := filepath.Join(t.TempDir(), "slot")
	result, err := runGate(t.Context(), gateRun{
		Argv:  []string{"/bin/sh", "-c", `pwd -P; echo out; echo err >&2; exit 3`},
		Dir:   dir,
		Limit: time.Minute,
		Locks: []string{lock},
	})
	if err != nil {
		t.Fatalf("runGate: %v", err)
	}
	if result.Exit != 3 {
		t.Fatalf("Exit = %d, want 3", result.Exit)
	}
	if result.TimedOut {
		t.Fatal("TimedOut = true for a gate that exited on its own")
	}
	if result.Lock != lock {
		t.Fatalf("Lock = %q, want %q", result.Lock, lock)
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("resolve dir: %v", err)
	}
	if !strings.Contains(result.Output, resolved+"\n") {
		t.Fatalf("Output %q does not show the working directory %q", result.Output, resolved)
	}
	if !strings.Contains(result.Output, "out\n") || !strings.Contains(result.Output, "err\n") {
		t.Fatalf("Output %q misses stdout or stderr", result.Output)
	}
}

func TestGateEnvironDropsTmuxMarkers(t *testing.T) {
	lock := filepath.Join(t.TempDir(), "slot")
	result, err := runGate(t.Context(), gateRun{
		Argv:    []string{"/bin/sh", "-c", `printf '%s\n' "${TMUX-unset}" "${TMUX_PANE-unset}" "$KEEP"`},
		Dir:     t.TempDir(),
		Limit:   time.Minute,
		Locks:   []string{lock},
		Environ: []string{"TMUX=/tmp/tmux-1000/default,1,0", "TMUX_PANE=%3", "KEEP=yes"},
	})
	if err != nil {
		t.Fatalf("runGate: %v", err)
	}
	if want := "unset\nunset\nyes\n"; result.Output != want {
		t.Fatalf("Output = %q, want %q", result.Output, want)
	}
}

func TestGateUsesSecondLockWhenFirstIsHeld(t *testing.T) {
	first := filepath.Join(t.TempDir(), "slot-a")
	second := filepath.Join(t.TempDir(), "slot-b")
	release := gateHoldLock(t, first)
	defer release()
	result, err := runGate(t.Context(), gateRun{
		Argv:  []string{"/bin/sh", "-c", "exit 0"},
		Dir:   t.TempDir(),
		Limit: time.Minute,
		Locks: []string{first, second},
	})
	if err != nil {
		t.Fatalf("runGate: %v", err)
	}
	if result.Lock != second {
		t.Fatalf("Lock = %q, want the free second lock %q", result.Lock, second)
	}
}

func TestGateWaitsUntilALockFrees(t *testing.T) {
	first := filepath.Join(t.TempDir(), "slot-a")
	second := filepath.Join(t.TempDir(), "slot-b")
	marker := filepath.Join(t.TempDir(), "started")
	releaseFirst := gateHoldLock(t, first)
	releaseSecond := gateHoldLock(t, second)
	defer releaseFirst()

	type outcome struct {
		result gateResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := runGate(t.Context(), gateRun{
			Argv:    []string{"/bin/sh", "-c", `touch "$MARKER"; exit 0`},
			Dir:     t.TempDir(),
			Limit:   time.Minute,
			Locks:   []string{first, second},
			Environ: gateEnv("MARKER=" + marker),
		})
		done <- outcome{result, err}
	}()

	select {
	case got := <-done:
		t.Fatalf("gate finished while both locks were held: %+v, %v", got.result, got.err)
	case <-time.After(1500 * time.Millisecond):
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("gate started while both locks were held")
	}

	releaseSecond()
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("runGate: %v", got.err)
		}
		if got.result.Lock != second {
			t.Fatalf("Lock = %q, want the released %q", got.result.Lock, second)
		}
		if got.result.Exit != 0 {
			t.Fatalf("Exit = %d, want 0", got.result.Exit)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("gate never started after a lock freed")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("gate did not run after the lock freed: %v", err)
	}
}

func TestGateKillsItsGroupAtLimit(t *testing.T) {
	pids := filepath.Join(t.TempDir(), "pids")
	started := time.Now()
	result, err := runGate(t.Context(), gateRun{
		Argv:    []string{"/bin/sh", "-c", `sleep 60 & echo "$$ $!" > "$PIDS"; sleep 60`},
		Dir:     t.TempDir(),
		Limit:   time.Second,
		Locks:   []string{filepath.Join(t.TempDir(), "slot")},
		Environ: gateEnv("PIDS=" + pids),
	})
	if err != nil {
		t.Fatalf("runGate: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Fatalf("runGate returned after %v: it waited for the background child instead of killing the group", elapsed)
	}
	if !result.TimedOut {
		t.Fatal("TimedOut = false for a gate killed at its limit")
	}
	group, child := gateParsePids(t, pids)
	gateWaitGone(t, group, child)
}

func TestGateCancelKillsGroupAndErrors(t *testing.T) {
	pids := filepath.Join(t.TempDir(), "pids")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	type outcome struct {
		result gateResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := runGate(ctx, gateRun{
			Argv:    []string{"/bin/sh", "-c", `sleep 60 & echo "$$ $!" > "$PIDS"; sleep 60`},
			Dir:     t.TempDir(),
			Limit:   time.Minute,
			Locks:   []string{filepath.Join(t.TempDir(), "slot")},
			Environ: gateEnv("PIDS=" + pids),
		})
		done <- outcome{result, err}
	}()

	group, child := gateParsePids(t, pids)
	cancel()
	select {
	case got := <-done:
		if got.err == nil {
			t.Fatalf("runGate returned no error after its context ended: %+v", got.result)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("runGate did not return after its context ended")
	}
	gateWaitGone(t, group, child)
}

func TestGateErrorsWhenItCannotStart(t *testing.T) {
	_, err := runGate(t.Context(), gateRun{
		Argv:  []string{filepath.Join(t.TempDir(), "no-such-gate")},
		Dir:   t.TempDir(),
		Limit: time.Minute,
		Locks: []string{filepath.Join(t.TempDir(), "slot")},
	})
	if err == nil {
		t.Fatal("runGate returned no error for a gate binary that does not exist")
	}
}

func TestGateErrorsWhenNoLockIsNamed(t *testing.T) {
	_, err := runGate(t.Context(), gateRun{
		Argv:  []string{"/bin/sh", "-c", "exit 0"},
		Dir:   t.TempDir(),
		Limit: time.Minute,
	})
	if !errors.Is(err, errGateNoLock) {
		t.Fatalf("err = %v, want errGateNoLock", err)
	}
}

func TestGateKeepsTheLastEightKiB(t *testing.T) {
	result, err := runGate(t.Context(), gateRun{
		Argv:    []string{"/bin/sh", "-c", `head -c 20000 /dev/zero | tr '\0' a; printf END`},
		Dir:     t.TempDir(),
		Limit:   time.Minute,
		Locks:   []string{filepath.Join(t.TempDir(), "slot")},
		Environ: gateEnv(),
	})
	if err != nil {
		t.Fatalf("runGate: %v", err)
	}
	if len(result.Output) != 8*1024 {
		t.Fatalf("len(Output) = %d, want %d", len(result.Output), 8*1024)
	}
	if !strings.HasSuffix(result.Output, "END") {
		t.Fatalf("Output does not end with the last bytes written: %q", result.Output[len(result.Output)-16:])
	}
}

// gateKillDetachedAtCleanup reads the pid a gate script wrote for a child it detached with setsid, and
// kills that child when the test ends so it never outlives the test.
func gateKillDetachedAtCleanup(t *testing.T, path string) {
	t.Helper()
	pid, err := strconv.Atoi(gateWaitFile(t, path))
	if err != nil {
		t.Fatalf("detached pid in %s: %v", path, err)
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
}

func TestGateReturnsAtLimitPastDetachedChild(t *testing.T) {
	pids := filepath.Join(t.TempDir(), "pids")
	lock := filepath.Join(t.TempDir(), "slot")
	started := time.Now()
	result, err := runGate(t.Context(), gateRun{
		Argv:    []string{"/bin/sh", "-c", `setsid sleep 20 & echo "$!" > "$PIDS"; sleep 20`},
		Dir:     t.TempDir(),
		Limit:   time.Second,
		Locks:   []string{lock},
		Environ: gateEnv("PIDS=" + pids),
	})
	gateKillDetachedAtCleanup(t, pids)
	if err != nil {
		t.Fatalf("runGate: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Fatalf("runGate returned after %v: it waited for a detached child that holds the output", elapsed)
	}
	if !result.TimedOut {
		t.Fatal("TimedOut = false for a gate killed at its limit")
	}
	gateHoldLock(t, lock)()
}

func TestGateCancelReturnsPastDetachedChild(t *testing.T) {
	pids := filepath.Join(t.TempDir(), "pids")
	lock := filepath.Join(t.TempDir(), "slot")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	type outcome struct {
		result gateResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := runGate(ctx, gateRun{
			Argv:    []string{"/bin/sh", "-c", `setsid sleep 20 & echo "$!" > "$PIDS"; sleep 20`},
			Dir:     t.TempDir(),
			Limit:   time.Minute,
			Locks:   []string{lock},
			Environ: gateEnv("PIDS=" + pids),
		})
		done <- outcome{result, err}
	}()

	gateKillDetachedAtCleanup(t, pids)
	cancel()
	select {
	case got := <-done:
		if got.err == nil {
			t.Fatalf("runGate returned no error after its context ended: %+v", got.result)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("runGate did not return after its context ended: it waited for a detached child that holds the output")
	}
	gateHoldLock(t, lock)()
}

func TestGateReportsExitWhenDetachedChildHoldsOutput(t *testing.T) {
	for _, exit := range []int{0, 5} {
		t.Run(strconv.Itoa(exit), func(t *testing.T) {
			pids := filepath.Join(t.TempDir(), "pids")
			lock := filepath.Join(t.TempDir(), "slot")
			started := time.Now()
			result, err := runGate(t.Context(), gateRun{
				Argv:    []string{"/bin/sh", "-c", fmt.Sprintf(`setsid sleep 20 & echo "$!" > "$PIDS"; exit %d`, exit)},
				Dir:     t.TempDir(),
				Limit:   time.Minute,
				Locks:   []string{lock},
				Environ: gateEnv("PIDS=" + pids),
			})
			gateKillDetachedAtCleanup(t, pids)
			if err != nil {
				t.Fatalf("runGate returned an error for a gate that exited on its own: %v", err)
			}
			if elapsed := time.Since(started); elapsed > 10*time.Second {
				t.Fatalf("runGate returned after %v: it waited for a detached child that holds the output", elapsed)
			}
			if result.Exit != exit {
				t.Fatalf("Exit = %d, want %d", result.Exit, exit)
			}
			if result.TimedOut {
				t.Fatal("TimedOut = true for a gate that exited on its own")
			}
			gateHoldLock(t, lock)()
		})
	}
}
