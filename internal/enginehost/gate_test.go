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
)

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
	t.Parallel()
	dir := t.TempDir()
	result, err := runGrantedGate(t.Context(), &fakeGatePools{}, gateGrantRequest(), gateRun{
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
	if !strings.Contains(result.Output, resolved+"\n") {
		t.Fatalf("Output %q does not show the working directory %q", result.Output, resolved)
	}
	if !strings.Contains(result.Output, "out\n") || !strings.Contains(result.Output, "err\n") {
		t.Fatalf("Output %q misses stdout or stderr", result.Output)
	}
}

func TestGateEnvironDropsTmuxMarkers(t *testing.T) {
	t.Parallel()
	result, err := runGrantedGate(t.Context(), &fakeGatePools{}, gateGrantRequest(), gateRun{
		Argv:    []string{"/bin/sh", "-c", `printf '%s\n' "${TMUX-unset}" "${TMUX_PANE-unset}" "$KEEP"`},
		Dir:     t.TempDir(),
		Limit:   time.Minute,
		Environ: []string{"TMUX=/tmp/tmux-1000/default,1,0", "TMUX_PANE=%3", "KEEP=yes"},
	})
	if err != nil {
		t.Fatalf("runGrantedGate: %v", err)
	}
	if want := "unset\nunset\nyes\n"; result.Output != want {
		t.Fatalf("Output = %q, want %q", result.Output, want)
	}
}

func TestGateKillsItsGroupAtLimit(t *testing.T) {
	t.Parallel()
	pids := filepath.Join(t.TempDir(), "pids")
	started := time.Now()
	result, err := runGrantedGate(t.Context(), &fakeGatePools{}, gateGrantRequest(), gateRun{
		Argv:    []string{"/bin/sh", "-c", `sleep 60 & echo "$$ $!" > "$PIDS"; sleep 60`},
		Dir:     t.TempDir(),
		Limit:   time.Second,
		Environ: gateEnv("PIDS=" + pids),
	})
	if err != nil {
		t.Fatalf("runGrantedGate: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Fatalf(
			"runGrantedGate returned after %v: it waited for the background child instead of killing the group",
			elapsed,
		)
	}
	if !result.TimedOut {
		t.Fatal("TimedOut = false for a gate killed at its limit")
	}
	group, child := gateParsePids(t, pids)
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
			Argv:    []string{"/bin/sh", "-c", `sleep 60 & echo "$$ $!" > "$PIDS"; sleep 60`},
			Dir:     t.TempDir(),
			Limit:   time.Minute,
			Environ: gateEnv("PIDS=" + pids),
		})
		done <- outcome{result, err}
	}()

	group, child := gateParsePids(t, pids)
	cancel()
	select {
	case got := <-done:
		if got.err == nil {
			t.Fatalf("runGrantedGate returned no error after its context ended: %+v", got.result)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("runGrantedGate did not return after its context ended")
	}
	gateWaitGone(t, group, child)
}

func TestGateErrorsWhenItCannotStart(t *testing.T) {
	t.Parallel()
	_, err := runGrantedGate(t.Context(), &fakeGatePools{}, gateGrantRequest(), gateRun{
		Argv:  []string{filepath.Join(t.TempDir(), "no-such-gate")},
		Dir:   t.TempDir(),
		Limit: time.Minute,
	})
	if err == nil {
		t.Fatal("runGrantedGate returned no error for a gate binary that does not exist")
	}
}

func TestGateKeepsTheLastEightKiB(t *testing.T) {
	t.Parallel()
	result, err := runGrantedGate(t.Context(), &fakeGatePools{}, gateGrantRequest(), gateRun{
		Argv:    []string{"/bin/sh", "-c", `head -c 20000 /dev/zero | tr '\0' a; printf END`},
		Dir:     t.TempDir(),
		Limit:   time.Minute,
		Environ: gateEnv(),
	})
	if err != nil {
		t.Fatalf("runGrantedGate: %v", err)
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
	t.Parallel()
	pids := filepath.Join(t.TempDir(), "pids")
	started := time.Now()
	result, err := runGrantedGate(t.Context(), &fakeGatePools{}, gateGrantRequest(), gateRun{
		Argv:    []string{"/bin/sh", "-c", `setsid sleep 20 & echo "$!" > "$PIDS"; sleep 20`},
		Dir:     t.TempDir(),
		Limit:   time.Second,
		Environ: gateEnv("PIDS=" + pids),
	})
	gateKillDetachedAtCleanup(t, pids)
	if err != nil {
		t.Fatalf("runGrantedGate: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Fatalf("runGrantedGate returned after %v: it waited for a detached child that holds the output", elapsed)
	}
	if !result.TimedOut {
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
			Argv:    []string{"/bin/sh", "-c", `setsid sleep 20 & echo "$!" > "$PIDS"; sleep 20`},
			Dir:     t.TempDir(),
			Limit:   time.Minute,
			Environ: gateEnv("PIDS=" + pids),
		})
		done <- outcome{result, err}
	}()

	gateKillDetachedAtCleanup(t, pids)
	cancel()
	select {
	case got := <-done:
		if got.err == nil {
			t.Fatalf("runGrantedGate returned no error after its context ended: %+v", got.result)
		}
	case <-time.After(10 * time.Second):
		t.Fatal(
			"runGrantedGate did not return after its context ended: it waited for a detached child that holds the output",
		)
	}
}

func TestGateReportsExitWhenDetachedChildHoldsOutput(t *testing.T) {
	for _, exit := range []int{0, 5} {
		t.Run(strconv.Itoa(exit), func(t *testing.T) {
			pids := filepath.Join(t.TempDir(), "pids")
			started := time.Now()
			result, err := runGrantedGate(t.Context(), &fakeGatePools{}, gateGrantRequest(), gateRun{
				Argv:    []string{"/bin/sh", "-c", fmt.Sprintf(`setsid sleep 20 & echo "$!" > "$PIDS"; exit %d`, exit)},
				Dir:     t.TempDir(),
				Limit:   time.Minute,
				Environ: gateEnv("PIDS=" + pids),
			})
			gateKillDetachedAtCleanup(t, pids)
			if err != nil {
				t.Fatalf("runGrantedGate returned an error for a gate that exited on its own: %v", err)
			}
			if elapsed := time.Since(started); elapsed > 10*time.Second {
				t.Fatalf(
					"runGrantedGate returned after %v: it waited for a detached child that holds the output",
					elapsed,
				)
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

// gateWaitExit polls until the gate's holder in dir has left its exit record, and returns it.
func gateWaitExit(t *testing.T, dir string) holder.Exit {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(dir, holder.ExitName)); err == nil {
			return gateReadExit(t, dir)
		}
		if time.Now().After(deadline) {
			t.Fatalf("holder in %s never left an exit record", dir)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// gateWaitStarted returns the holder directory a gate's Started hook was handed.
func gateWaitStarted(t *testing.T, started <-chan string) string {
	t.Helper()
	select {
	case dir := <-started:
		return dir
	case <-time.After(10 * time.Second):
		t.Fatal("the gate never reached Started")
		return ""
	}
}

func TestGateRunsUnderAHolderAndKeepsItsExitRecord(t *testing.T) {
	t.Parallel()
	stateDir := gateShortStateDir(t)
	result, err := runGrantedGate(t.Context(), &fakeGatePools{}, gateGrantRequest(), gateRun{
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
	if !strings.Contains(result.Output, "out\n") {
		t.Fatalf("Output %q does not show the gate's stdout", result.Output)
	}
}

// TestGateExitRecordSurvivesAGateThatOutlivesItsAttach checks that a gate running past the holder's attach keeps its exit
// record. The holder ends after Launch has attached, so the holder's own cleanup runs at its end unless the gate keeps
// its directory.
func TestGateExitRecordSurvivesAGateThatOutlivesItsAttach(t *testing.T) {
	t.Parallel()
	stateDir := gateShortStateDir(t)
	result, err := runGrantedGate(t.Context(), &fakeGatePools{}, gateGrantRequest(), gateRun{
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
	if result.Exit != 3 || !strings.Contains(result.Output, "out\n") {
		t.Fatalf("Exit = %d, Output %q, want 3 and the gate's stdout", result.Exit, result.Output)
	}
}

func TestGateStartedSeesGrantAndHolderBeforeLaunch(t *testing.T) {
	t.Parallel()
	marker := filepath.Join(t.TempDir(), "started")
	pools := &fakeGatePools{}
	var grantID, dir string
	var launchedAtStart bool
	result, err := runGrantedGate(t.Context(), pools, gateGrantRequest(), gateRun{
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
	select {
	case <-released:
	case <-time.After(10 * time.Second):
		t.Fatal("the grant was not released after Started refused the gate")
	}
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
	go func() {
		result, err := runGrantedGate(ctx, pools, gateGrantRequest(), gateRun{
			Argv:     []string{"/bin/sh", "-c", `echo running > "$MARKER"; sleep 1; echo done; exit 0`},
			Dir:      t.TempDir(),
			Limit:    time.Minute,
			Name:     "detached",
			StateDir: gateShortStateDir(t),
			Environ:  gateEnv("MARKER=" + marker),
			Started: func(_ context.Context, _, dir string) error {
				started <- dir
				return nil
			},
		})
		done <- outcome{result, err}
	}()

	dir := gateWaitStarted(t, started)
	// The detach follows the gate's start, and the holder's handshake follows its start by milliseconds.
	gateWaitFile(t, marker)
	time.Sleep(200 * time.Millisecond)
	cancel(errGateDetach)
	var got outcome
	select {
	case got = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("runGrantedGate did not return after the detach")
	}
	if !errors.Is(got.err, errGateDetach) {
		t.Fatalf("err = %v, want errGateDetach", got.err)
	}
	select {
	case <-released:
		t.Fatal("the grant ended on detach: the holder keeps its grant")
	default:
	}
	exit := gateWaitExit(t, dir)
	if exit.Reason != holder.ReasonExited || exit.Code != 0 || !strings.Contains(exit.Stderr, "done") {
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
	if exit.Reason != holder.ReasonExited || exit.Code != 0 || !strings.Contains(exit.Stderr, "done") {
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
	group, child := gateParsePids(t, pids)
	if _, err := os.Stat(filepath.Join(dir, holder.SocketName)); err != nil {
		t.Fatalf("the gate's holder does not serve %s while the gate runs: %v", dir, err)
	}
	cancel()
	select {
	case got := <-done:
		if got.err == nil {
			t.Fatalf("runGrantedGate returned no error after its context ended: %+v", got.result)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("runGrantedGate did not return after its context ended")
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
	group, child := gateParsePids(t, pids)
	gateWaitGone(t, group, child)
}
