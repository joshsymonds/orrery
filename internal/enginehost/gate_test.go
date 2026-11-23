package enginehost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/holder"
	"github.com/joshsymonds/orrery/internal/hostexec"
	"github.com/joshsymonds/orrery/internal/waittest"
)

// gateEnv is the environment a test gate runs in: the test's PATH so its tools resolve, plus extra.
func gateEnv(extra ...string) []string {
	return append([]string{"PATH=" + os.Getenv("PATH")}, extra...)
}

// gateTestHost is the Host a test gate runs on: this machine.
func gateTestHost() hostexec.Host {
	return hostexec.NewLocal()
}

// gateHeldScript writes running to $MARKER, waits until $RELEASE exists, prints done and exits 0, so a test acts on the
// running gate and then lets it end.
const gateHeldScript = `echo running > "$MARKER"; until [ -e "$RELEASE" ]; do sleep 0.01; done; echo done; exit 0`

// gateWaitFile polls until path exists with content and returns the trimmed content. It has no deadline of its own:
// the gate runs at the machine's pace, and the test binary's timeout ends a wait that never ends. ended closes once
// the run that writes path has returned; the wait then fails at once if path still has no content (see
// waittest.Until). A nil ended is a run the test holds no end of.
func gateWaitFile(t *testing.T, path string, ended <-chan struct{}) string {
	t.Helper()
	var content string
	waittest.Until(t, ended, "content in "+path, func() bool {
		data, err := os.ReadFile(path)
		content = strings.TrimSpace(string(data))
		return err == nil && content != ""
	})
	return content
}

// gateReturned returns a channel that is already closed, the ended of a run that has returned before the wait.
func gateReturned() <-chan struct{} {
	ended := make(chan struct{})
	close(ended)
	return ended
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

// gateWaitGone blocks until every pid in pids has exited. Each wait is the kernel's signal of the exit (see
// gateWaitPidExit), not a poll.
func gateWaitGone(t *testing.T, pids ...int) {
	t.Helper()
	for _, pid := range pids {
		if err := gateWaitPidExit(pid); err != nil {
			t.Fatalf("wait for process %d to exit: %v", pid, err)
		}
	}
}

// gateParsePids reads the "$$ $!" pair a gate script writes: its own pid (the group id) and its
// background child's pid. ended is gateWaitFile's.
func gateParsePids(t *testing.T, path string, ended <-chan struct{}) (int, int) {
	t.Helper()
	fields := strings.Fields(gateWaitFile(t, path, ended))
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
	t.Parallel()
	dir := t.TempDir()
	result, err := runGrantedGate(t.Context(), &fakeGatePools{}, gateGrantRequest(), gateRun{
		Host:  gateTestHost(),
		Argv:  []string{"/bin/sh", "-c", `pwd -P; echo out; echo err >&2; exit 3`},
		Dir:   dir,
		Limit: time.Minute,
	})
	if err != nil {
		t.Fatalf("runGrantedGate: %v", err)
	}
	if result.Exit != 3 {
		t.Fatalf("Exit = %d, want 3", result.Exit)
	}
	if result.TimedOut {
		t.Fatal("TimedOut = true for a gate that exited on its own")
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("resolve dir: %v", err)
	}
	if !strings.Contains(result.Stdout, resolved+"\n") {
		t.Fatalf("Stdout %q does not show the working directory %q", result.Stdout, resolved)
	}
	if !strings.Contains(result.Stdout, "out\n") {
		t.Fatalf("Stdout %q misses the gate's stdout", result.Stdout)
	}
	if !strings.Contains(result.Stderr, "err\n") {
		t.Fatalf("Stderr %q misses the gate's stderr", result.Stderr)
	}
}

func TestGateKeepsStdoutAndStderrApart(t *testing.T) {
	t.Parallel()
	result, err := runGrantedGate(t.Context(), &fakeGatePools{}, gateGrantRequest(), gateRun{
		Host:  gateTestHost(),
		Argv:  []string{"/bin/sh", "-c", `printf out; printf err >&2`},
		Dir:   t.TempDir(),
		Limit: time.Minute,
	})
	if err != nil {
		t.Fatalf("runGrantedGate: %v", err)
	}
	if result.Stdout != "out" || result.Stderr != "err" {
		t.Fatalf("Stdout = %q, Stderr = %q, want %q and %q", result.Stdout, result.Stderr, "out", "err")
	}
}

func TestGateEnvironDropsTmuxMarkers(t *testing.T) {
	t.Parallel()
	result, err := runGrantedGate(t.Context(), &fakeGatePools{}, gateGrantRequest(), gateRun{
		Host:    gateTestHost(),
		Argv:    []string{"/bin/sh", "-c", `printf '%s\n' "${TMUX-unset}" "${TMUX_PANE-unset}" "$KEEP"`},
		Dir:     t.TempDir(),
		Limit:   time.Minute,
		Environ: []string{"TMUX=/tmp/tmux-1000/default,1,0", "TMUX_PANE=%3", "KEEP=yes"},
	})
	if err != nil {
		t.Fatalf("runGrantedGate: %v", err)
	}
	if want := "unset\nunset\nyes\n"; result.Stdout != want {
		t.Fatalf("Stdout = %q, want %q", result.Stdout, want)
	}
}

// gateLimitFIFO makes a FIFO in a fresh temporary directory for a gate's limit signal, and returns its path. The gate's
// limit is reached when a process opens the FIFO for writing: see gateSignalLimit.
func gateLimitFIFO(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "limit")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatalf("make limit FIFO %s: %v", path, err)
	}
	return path
}

// gateSignalLimit reaches the limit of the gate whose limit signal is fifo, by opening it for writing. The open waits
// for the gate's holder to open the FIFO for reading, which the holder does as it starts the gate, so a test calls it
// once the gate runs: the gate's pids file is written by then.
func gateSignalLimit(t *testing.T, fifo string) {
	t.Helper()
	signal, err := os.OpenFile(fifo, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open limit FIFO %s for writing: %v", fifo, err)
	}
	if err = signal.Close(); err != nil {
		t.Fatalf("close limit FIFO %s: %v", fifo, err)
	}
}

func TestGateLimitClockKillsItsGroup(t *testing.T) {
	t.Parallel()
	pids := filepath.Join(t.TempDir(), "pids")
	limit := gateLimitFIFO(t)
	type outcome struct {
		result gateResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := runGrantedGate(t.Context(), &fakeGatePools{}, gateGrantRequest(), gateRun{
			Host:        gateTestHost(),
			Argv:        []string{"/bin/sh", "-c", `sleep 60 & echo "$$ $!" > "$PIDS"; sleep 60`},
			Dir:         t.TempDir(),
			Limit:       time.Hour,
			limitSignal: limit,
			Environ:     gateEnv("PIDS=" + pids),
		})
		done <- outcome{result, err}
	}()
	ended, doneResult := waittest.Closed(done)
	group, child := gateParsePids(t, pids, ended)
	gateSignalLimit(t, limit)
	got := <-doneResult
	if got.err != nil {
		t.Fatalf("runGrantedGate: %v", got.err)
	}
	if !got.result.TimedOut {
		t.Fatal("TimedOut = false for a gate killed at its limit")
	}
	gateWaitGone(t, group, child)
}

func TestGateCancelKillsGroupAndErrors(t *testing.T) {
	t.Parallel()
	pids := filepath.Join(t.TempDir(), "pids")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	type outcome struct {
		result gateResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := runGrantedGate(ctx, &fakeGatePools{}, gateGrantRequest(), gateRun{
			Host:    gateTestHost(),
			Argv:    []string{"/bin/sh", "-c", `sleep 60 & echo "$$ $!" > "$PIDS"; sleep 60`},
			Dir:     t.TempDir(),
			Limit:   time.Minute,
			Environ: gateEnv("PIDS=" + pids),
		})
		done <- outcome{result, err}
	}()

	ended, doneResult := waittest.Closed(done)
	group, child := gateParsePids(t, pids, ended)
	cancel()
	got := <-doneResult
	if got.err == nil {
		t.Fatalf("runGrantedGate returned no error after its context ended: %+v", got.result)
	}
	gateWaitGone(t, group, child)
}

func TestGateErrorsWhenItCannotStart(t *testing.T) {
	t.Parallel()
	_, err := runGrantedGate(t.Context(), &fakeGatePools{}, gateGrantRequest(), gateRun{
		Host:  gateTestHost(),
		Argv:  []string{filepath.Join(t.TempDir(), "no-such-gate")},
		Dir:   t.TempDir(),
		Limit: time.Minute,
	})
	if err == nil {
		t.Fatal("runGrantedGate returned no error for a gate binary that does not exist")
	}
}

// TestGateKeepsTheLastEightKiB runs a gate that names no tail and writes one byte less than 8 KiB over many lines. The
// default tail keeps all of it, so nothing is cut and the first line stays. It proves the default from below, as
// TestGateRedactTailCutInsideTokenDropsItsLineOnBothStreams proves it from above.
func TestGateKeepsTheLastEightKiB(t *testing.T) {
	t.Parallel()
	out := gateTailLines(0, 818) + "line-final\n"
	if len(out) != 8<<10-1 {
		t.Fatalf("output is %d bytes, want one byte less than 8 KiB", len(out))
	}
	if got := runGateScript(t, gateRedactHeredoc(out, false), 0).Stdout; got != out {
		t.Fatalf("len(Stdout) = %d, want the whole %d-byte output with its first line", len(got), len(out))
	}
}

// gateKillDetachedAtCleanup reads the pid a gate script wrote for a child it detached with setsid, and
// kills that child when the test ends so it never outlives the test. ended is gateWaitFile's.
func gateKillDetachedAtCleanup(t *testing.T, path string, ended <-chan struct{}) {
	t.Helper()
	pid, err := strconv.Atoi(gateWaitFile(t, path, ended))
	if err != nil {
		t.Fatalf("detached pid in %s: %v", path, err)
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
}

func TestGateLimitClockReturnsPastDetachedChild(t *testing.T) {
	t.Parallel()
	pids := filepath.Join(t.TempDir(), "pids")
	limit := gateLimitFIFO(t)
	type outcome struct {
		result gateResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := runGrantedGate(t.Context(), &fakeGatePools{}, gateGrantRequest(), gateRun{
			Host:        gateTestHost(),
			Argv:        []string{"/bin/sh", "-c", `setsid sleep 20 & echo "$!" > "$PIDS"; sleep 20`},
			Dir:         t.TempDir(),
			Limit:       time.Hour,
			limitSignal: limit,
			Environ:     gateEnv("PIDS=" + pids),
		})
		done <- outcome{result, err}
	}()
	ended, doneResult := waittest.Closed(done)
	gateKillDetachedAtCleanup(t, pids, ended)
	gateSignalLimit(t, limit)
	got := <-doneResult
	if got.err != nil {
		t.Fatalf("runGrantedGate: %v", got.err)
	}
	if !got.result.TimedOut {
		t.Fatal("TimedOut = false for a gate killed at its limit")
	}
}

func TestGateCancelReturnsPastDetachedChild(t *testing.T) {
	t.Parallel()
	pids := filepath.Join(t.TempDir(), "pids")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	type outcome struct {
		result gateResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := runGrantedGate(ctx, &fakeGatePools{}, gateGrantRequest(), gateRun{
			Host:    gateTestHost(),
			Argv:    []string{"/bin/sh", "-c", `setsid sleep 20 & echo "$!" > "$PIDS"; sleep 20`},
			Dir:     t.TempDir(),
			Limit:   time.Minute,
			Environ: gateEnv("PIDS=" + pids),
		})
		done <- outcome{result, err}
	}()

	ended, doneResult := waittest.Closed(done)
	gateKillDetachedAtCleanup(t, pids, ended)
	cancel()
	got := <-doneResult
	if got.err == nil {
		t.Fatalf("runGrantedGate returned no error after its context ended: %+v", got.result)
	}
}

func TestGateReportsExitWhenDetachedChildHoldsOutput(t *testing.T) {
	t.Parallel()
	for _, exit := range []int{0, 5} {
		t.Run(strconv.Itoa(exit), func(t *testing.T) {
			t.Parallel()
			pids := filepath.Join(t.TempDir(), "pids")
			result, err := runGrantedGate(t.Context(), &fakeGatePools{}, gateGrantRequest(), gateRun{
				Host:    gateTestHost(),
				Argv:    []string{"/bin/sh", "-c", fmt.Sprintf(`setsid sleep 20 & echo "$!" > "$PIDS"; exit %d`, exit)},
				Dir:     t.TempDir(),
				Limit:   time.Minute,
				Environ: gateEnv("PIDS=" + pids),
			})
			gateKillDetachedAtCleanup(t, pids, gateReturned())
			if err != nil {
				t.Fatalf("runGrantedGate returned an error for a gate that exited on its own: %v", err)
			}
			if result.Exit != exit {
				t.Fatalf("Exit = %d, want %d", result.Exit, exit)
			}
			if result.TimedOut {
				t.Fatal("TimedOut = true for a gate that exited on its own")
			}
		})
	}
}

// gateShortRoot returns a fresh state directory short enough that a holder's socket path fits in sun_path, which
// t.TempDir's long test-named paths can overflow. The caller removes it.
func gateShortRoot() (string, error) {
	root, err := os.MkdirTemp("", "gate")
	if err != nil {
		return "", fmt.Errorf("create short root: %w", err)
	}
	return root, nil
}

// gateShortStateDir returns a gateShortRoot directory that is removed when the test ends.
func gateShortStateDir(t *testing.T) string {
	t.Helper()
	root, err := gateShortRoot()
	if err != nil {
		t.Fatalf("%v", err)
	}
	t.Cleanup(func() {
		if rmErr := os.RemoveAll(root); rmErr != nil {
			t.Errorf("remove %s: %v", root, rmErr)
		}
	})
	return root
}

// gateReadExit returns the exit record the gate's holder left in dir.
func gateReadExit(t *testing.T, dir string) holder.Exit {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, holder.ExitName))
	if err != nil {
		t.Fatalf("read exit record: %v", err)
	}
	var exit holder.Exit
	if err := json.Unmarshal(data, &exit); err != nil {
		t.Fatalf("decode exit record %s: %v", data, err)
	}
	return exit
}

// gateWaitExit polls until the gate's holder in dir has left its exit record, and returns it. It has no deadline of
// its own, but it fails at once when the holder that pids record names is gone with no record left: a holder writes
// its record before it exits, so one that exited without it never will.
func gateWaitExit(t *testing.T, dir string) holder.Exit {
	t.Helper()
	exitPath := filepath.Join(dir, holder.ExitName)
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(exitPath); err == nil {
			return gateReadExit(t, dir)
		}
		if pids, err := holder.ReadPids(dir); err == nil {
			if running, runErr := holder.Running(pids.Holder, pids.HolderStart); runErr == nil && !running {
				if _, err := os.Stat(exitPath); err == nil {
					return gateReadExit(t, dir)
				}
				t.Fatalf("holder %d of %s exited without its exit record %s", pids.Holder, dir, exitPath)
			}
		}
		<-ticker.C
	}
}

// gateWaitStarted returns the holder directory a gate's Started hook was handed.
func gateWaitStarted(t *testing.T, started <-chan string) string {
	t.Helper()
	return <-started
}

func TestGateRunsUnderAHolderAndKeepsItsExitRecord(t *testing.T) {
	t.Parallel()
	stateDir := gateShortStateDir(t)
	result, err := runGrantedGate(t.Context(), &fakeGatePools{}, gateGrantRequest(), gateRun{
		Host:     gateTestHost(),
		Argv:     []string{"/bin/sh", "-c", `echo out; exit 3`},
		Dir:      t.TempDir(),
		Limit:    time.Minute,
		Name:     "held",
		StateDir: stateDir,
	})
	if err != nil {
		t.Fatalf("runGrantedGate: %v", err)
	}
	wantDir := filepath.Join(stateDir, "holders", "held")
	if result.Holder != wantDir {
		t.Fatalf("Holder = %q, want %q", result.Holder, wantDir)
	}
	exit := gateReadExit(t, wantDir)
	if exit.Reason != holder.ReasonExited || exit.Code != 3 {
		t.Fatalf("exit record = %+v, want reason %q with code 3", exit, holder.ReasonExited)
	}
	if result.Exit != 3 {
		t.Fatalf("Exit = %d, want 3", result.Exit)
	}
	if !strings.Contains(result.Stdout, "out\n") {
		t.Fatalf("Stdout %q does not show the gate's stdout", result.Stdout)
	}
}

// TestGateExitRecordSurvivesAGateThatOutlivesItsAttach checks that a gate running past the holder's attach keeps its exit
// record. The holder ends after Launch has attached, so the holder's own cleanup runs at its end unless the gate keeps
// its directory.
func TestGateExitRecordSurvivesAGateThatOutlivesItsAttach(t *testing.T) {
	t.Parallel()
	stateDir := gateShortStateDir(t)
	result, err := runGrantedGate(t.Context(), &fakeGatePools{}, gateGrantRequest(), gateRun{
		Host:     gateTestHost(),
		Argv:     []string{"/bin/sh", "-c", `sleep 0.3; echo out; exit 3`},
		Dir:      t.TempDir(),
		Limit:    time.Minute,
		Environ:  gateEnv(),
		Name:     "attached",
		StateDir: stateDir,
	})
	if err != nil {
		t.Fatalf("runGrantedGate: %v", err)
	}
	exit := gateReadExit(t, filepath.Join(stateDir, "holders", "attached"))
	if exit.Reason != holder.ReasonExited || exit.Code != 3 {
		t.Fatalf("exit record = %+v, want reason %q with code 3", exit, holder.ReasonExited)
	}
	if result.Exit != 3 || !strings.Contains(result.Stdout, "out\n") {
		t.Fatalf("Exit = %d, Stdout %q, want 3 and the gate's stdout", result.Exit, result.Stdout)
	}
}

func TestGateStartedSeesGrantAndHolderBeforeLaunch(t *testing.T) {
	t.Parallel()
	marker := filepath.Join(t.TempDir(), "started")
	pools := &fakeGatePools{}
	var grantID, dir string
	var launchedAtStart bool
	result, err := runGrantedGate(t.Context(), pools, gateGrantRequest(), gateRun{
		Host:     gateTestHost(),
		Argv:     []string{"/bin/sh", "-c", `touch "$MARKER"`},
		Dir:      t.TempDir(),
		Limit:    time.Minute,
		StateDir: gateShortStateDir(t),
		Environ:  gateEnv("MARKER=" + marker),
		Started: func(_ context.Context, id, holderDir string) error {
			grantID, dir = id, holderDir
			_, statErr := os.Stat(marker)
			launchedAtStart = statErr == nil
			return nil
		},
	})
	if err != nil {
		t.Fatalf("runGrantedGate: %v", err)
	}
	if grantID != "grant-1" {
		t.Fatalf("Started grant = %q, want the granted %q", grantID, "grant-1")
	}
	if dir == "" || dir != result.Holder {
		t.Fatalf("Started holder = %q, result Holder = %q: want the same directory", dir, result.Holder)
	}
	if launchedAtStart {
		t.Fatal("the gate ran before Started returned")
	}
	requests := pools.requested()
	if len(requests) != 1 || requests[0].Holder != dir {
		t.Fatalf("request Holder = %+v, want the holder directory %q", requests, dir)
	}
}

func TestGateStartedErrorReleasesGrantAndStartsNothing(t *testing.T) {
	t.Parallel()
	stateDir := gateShortStateDir(t)
	marker := filepath.Join(t.TempDir(), "started")
	refused := errors.New("store refused the gate")
	released := make(chan struct{})
	pools := &fakeGatePools{onEnd: func() { close(released) }}

	_, err := runGrantedGate(t.Context(), pools, gateGrantRequest(), gateRun{
		Host:     gateTestHost(),
		Argv:     []string{"/bin/sh", "-c", `touch "$MARKER"`},
		Dir:      t.TempDir(),
		Limit:    time.Minute,
		Name:     "unstarted",
		StateDir: stateDir,
		Environ:  gateEnv("MARKER=" + marker),
		Started:  func(context.Context, string, string) error { return refused },
	})
	if !errors.Is(err, refused) {
		t.Fatalf("err = %v, want the Started error", err)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("the gate ran although Started refused it")
	}
	if _, statErr := os.Stat(filepath.Join(stateDir, "holders", "unstarted")); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("holder directory of a gate that never launched: stat error %v, want not exist", statErr)
	}
	<-released
}

func TestGateDetachLeavesGateRunningToItsExitRecord(t *testing.T) {
	t.Parallel()
	released := make(chan struct{})
	pools := &fakeGatePools{onEnd: func() { close(released) }}
	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	started := make(chan string, 1)

	type outcome struct {
		result gateResult
		err    error
	}
	done := make(chan outcome, 1)
	marker := filepath.Join(t.TempDir(), "running")
	release := filepath.Join(t.TempDir(), "release")
	launched := make(chan struct{})
	go func() {
		result, err := runGrantedGate(ctx, pools, gateGrantRequest(), gateRun{
			Host:     gateTestHost(),
			Argv:     []string{"/bin/sh", "-c", gateHeldScript},
			Dir:      t.TempDir(),
			Limit:    time.Minute,
			Name:     "detached",
			StateDir: gateShortStateDir(t),
			Environ:  gateEnv("MARKER="+marker, "RELEASE="+release),
			Started: func(_ context.Context, _, dir string) error {
				started <- dir
				return nil
			},
			launched: func() { close(launched) },
		})
		done <- outcome{result, err}
	}()

	dir := gateWaitStarted(t, started)
	// The detach comes once the holder is attached and the gate is watched. The gate runs until the test releases it,
	// after the attempt has returned.
	<-launched
	cancel(errGateDetach)
	got := <-done
	if !errors.Is(got.err, errGateDetach) {
		t.Fatalf("err = %v, want errGateDetach", got.err)
	}
	select {
	case <-released:
		t.Fatal("the grant ended on detach: the holder keeps its grant")
	default:
	}
	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatalf("release the gate: %v", err)
	}
	exit := gateWaitExit(t, dir)
	if exit.Reason != holder.ReasonExited || exit.Code != 0 || !strings.Contains(exit.Stdout, "done") {
		t.Fatalf("exit record = %+v, want the gate's own exit 0 with its output", exit)
	}
}

// TestGateDetachDuringLaunchLeavesHolderRunning checks that a detach arriving while the gate's holder is launched leaves
// the holder running with its grant, as a detach after the launch does.
func TestGateDetachDuringLaunchLeavesHolderRunning(t *testing.T) {
	t.Parallel()
	released := make(chan struct{})
	pools := &fakeGatePools{onEnd: func() { close(released) }}
	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	var dir string

	_, err := runGrantedGate(ctx, pools, gateGrantRequest(), gateRun{
		Host:     gateTestHost(),
		Argv:     []string{"/bin/sh", "-c", `sleep 1; echo done; exit 0`},
		Dir:      t.TempDir(),
		Limit:    time.Minute,
		Environ:  gateEnv(),
		Name:     "detached-launch",
		StateDir: gateShortStateDir(t),
		Started: func(_ context.Context, _, holderDir string) error {
			dir = holderDir
			cancel(errGateDetach)
			return nil
		},
	})
	if !errors.Is(err, errGateDetach) {
		t.Fatalf("err = %v, want errGateDetach", err)
	}
	select {
	case <-released:
		t.Fatal("the grant ended on detach: the holder keeps its grant")
	default:
	}
	exit := gateWaitExit(t, dir)
	if exit.Reason != holder.ReasonExited || exit.Code != 0 || !strings.Contains(exit.Stdout, "done") {
		t.Fatalf("exit record = %+v, want the gate's own exit 0 with its output", exit)
	}
}

func TestGateCancelStopsHolderAndClearsItsDirectory(t *testing.T) {
	t.Parallel()
	pids := filepath.Join(t.TempDir(), "pids")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	pools := &fakeGatePools{onEnd: gateGroupGoneAtEnd(t, pids)}
	started := make(chan string, 1)

	type outcome struct {
		result gateResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := runGrantedGate(ctx, pools, gateGrantRequest(), gateRun{
			Host:     gateTestHost(),
			Argv:     []string{"/bin/sh", "-c", `sleep 60 & echo "$$ $!" > "$PIDS"; sleep 60`},
			Dir:      t.TempDir(),
			Limit:    time.Minute,
			Name:     "canceled",
			StateDir: gateShortStateDir(t),
			Environ:  gateEnv("PIDS=" + pids),
			Started: func(_ context.Context, _, dir string) error {
				started <- dir
				return nil
			},
		})
		done <- outcome{result, err}
	}()

	dir := gateWaitStarted(t, started)
	ended, doneResult := waittest.Closed(done)
	group, child := gateParsePids(t, pids, ended)
	if _, err := os.Stat(filepath.Join(dir, holder.SocketName)); err != nil {
		t.Fatalf("the gate's holder does not serve %s while the gate runs: %v", dir, err)
	}
	cancel()
	got := <-doneResult
	if got.err == nil {
		t.Fatalf("runGrantedGate returned no error after its context ended: %+v", got.result)
	}
	gateWaitGone(t, group, child)
	if _, err := os.Stat(dir); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("holder directory %s after the cancel: stat error %v, want not exist", dir, err)
	}
}

// TestGateNormalExitKillsWhatItLeftRunning checks that a gate which exits on its own takes its background child down
// with it: the child outlives the holder's handshake, which a child that ends at once does not.
func TestGateNormalExitKillsWhatItLeftRunning(t *testing.T) {
	t.Parallel()
	pids := filepath.Join(t.TempDir(), "pids")
	pools := &fakeGatePools{onEnd: gateGroupGoneAtEnd(t, pids)}
	result, err := runGrantedGate(t.Context(), pools, gateGrantRequest(), gateRun{
		Host:     gateTestHost(),
		Argv:     []string{"/bin/sh", "-c", `sleep 60 & echo "$$ $!" > "$PIDS"; sleep 0.5; echo tail; exit 4`},
		Dir:      t.TempDir(),
		Limit:    time.Minute,
		Environ:  gateEnv("PIDS=" + pids),
		StateDir: gateShortStateDir(t),
	})
	if err != nil {
		t.Fatalf("runGrantedGate: %v", err)
	}
	if result.Exit != 4 || result.TimedOut {
		t.Fatalf("Exit = %d, TimedOut = %t, want 4 and false", result.Exit, result.TimedOut)
	}
	group, child := gateParsePids(t, pids, gateReturned())
	gateWaitGone(t, group, child)
}
