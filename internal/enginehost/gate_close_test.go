package enginehost

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/grants"
	"github.com/joshsymonds/orrery/internal/holder"
)

// queuedClosePools is a PoolCommands whose Acquire queues each request until closing is closed, then returns
// grants.ErrClosed, as a broker that closes under a queued request does. Each Acquire sends to queued once it queues.
type queuedClosePools struct {
	closing chan struct{}
	queued  chan struct{}
}

// Acquire implements PoolCommands for the fake.
func (q *queuedClosePools) Acquire(ctx context.Context, _ grants.Request, _ func(grants.Event) error) error {
	q.queued <- struct{}{}
	select {
	case <-q.closing:
		return grants.ErrClosed
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Status implements PoolCommands for the fake. The close tests do not read it.
func (*queuedClosePools) Status() grants.Status {
	return grants.Status{}
}

// Hold implements PoolCommands for the fake. The close tests do not place holds.
func (*queuedClosePools) Hold(context.Context, string) error {
	return nil
}

// Free implements PoolCommands for the fake. The close tests do not free holds.
func (*queuedClosePools) Free(context.Context, string) error {
	return nil
}

// waitAcquireQueued fails the test unless the fake's Acquire queues within a few seconds.
func waitAcquireQueued(t *testing.T, pools *queuedClosePools) {
	t.Helper()
	select {
	case <-pools.queued:
	case <-time.After(10 * time.Second):
		t.Fatal("Acquire never queued the request")
	}
}

// TestGateScopedCloseDetachesLaunchedGate checks that a gate with a Scope whose grant's Acquire ends with
// grants.ErrClosed while the gate runs is detached: its holder keeps running to its own exit, and the attempt returns
// errGateDetach.
func TestGateScopedCloseDetachesLaunchedGate(t *testing.T) {
	t.Parallel()
	stateDir := gateShortStateDir(t)
	scope := fakeSystemdRunPath(t)
	marker := filepath.Join(t.TempDir(), "running")
	pools := &fakeGatePools{drop: make(chan error, 1)}
	started := make(chan string, 1)

	done := make(chan error, 1)
	go func() {
		_, err := runGrantedGate(t.Context(), pools, gateGrantRequest(), gateRun{
			Argv:     []string{"/bin/sh", "-c", `echo running > "$MARKER"; sleep 1; echo done; exit 0`},
			Dir:      t.TempDir(),
			Limit:    time.Minute,
			Name:     "closed-launch",
			StateDir: stateDir,
			Scope:    scope,
			Environ:  gateEnv("MARKER=" + marker),
			Started: func(_ context.Context, _, dir string) error {
				started <- dir
				return nil
			},
		})
		done <- err
	}()

	dir := gateWaitStarted(t, started)
	gateWaitFile(t, marker)
	time.Sleep(200 * time.Millisecond)
	pools.drop <- grants.ErrClosed
	var err error
	select {
	case err = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("runGrantedGate did not return after the grant's Acquire ended")
	}
	if !errors.Is(err, errGateDetach) {
		t.Fatalf("err = %v, want errGateDetach", err)
	}
	exit := gateWaitExit(t, dir)
	if exit.Reason != holder.ReasonExited || exit.Code != 0 || !strings.Contains(exit.Stderr, "done") {
		t.Fatalf("exit record = %+v, want the gate's own exit 0 with its output: the holder was not left running", exit)
	}
}

// TestGateScopedCloseWhileQueuedLaunchesNothing checks that a gate with a Scope whose request is still queued when the
// broker closes launches nothing, and that its attempt returns errGateDetach.
func TestGateScopedCloseWhileQueuedLaunchesNothing(t *testing.T) {
	t.Parallel()
	stateDir := gateShortStateDir(t)
	scope := fakeSystemdRunPath(t)
	marker := filepath.Join(t.TempDir(), "started")
	pools := &queuedClosePools{closing: make(chan struct{}), queued: make(chan struct{}, 1)}
	started := make(chan string, 1)

	done := make(chan error, 1)
	go func() {
		_, err := runGrantedGate(t.Context(), pools, gateGrantRequest(), gateRun{
			Argv:     []string{"/bin/sh", "-c", `touch "$MARKER"`},
			Dir:      t.TempDir(),
			Limit:    time.Minute,
			Name:     "closed-queued",
			StateDir: stateDir,
			Scope:    scope,
			Environ:  gateEnv("MARKER=" + marker),
			Started: func(_ context.Context, _, dir string) error {
				started <- dir
				return nil
			},
		})
		done <- err
	}()

	waitAcquireQueued(t, pools)
	close(pools.closing)
	var err error
	select {
	case err = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("runGrantedGate did not return after the broker closed")
	}
	if !errors.Is(err, errGateDetach) {
		t.Fatalf("err = %v, want errGateDetach", err)
	}
	select {
	case dir := <-started:
		t.Fatalf("Started ran in %s for a request the broker closed before granting it", dir)
	default:
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("gate ran although its request was still queued when the broker closed")
	}
}

// TestGateCloseWithoutScopeStopsGate checks that a gate with no Scope whose grant's Acquire ends with grants.ErrClosed
// is stopped, its processes killed, as a gate whose grant ends is: the attempt does not detach.
func TestGateCloseWithoutScopeStopsGate(t *testing.T) {
	t.Parallel()
	stateDir := gateShortStateDir(t)
	pids := filepath.Join(t.TempDir(), "pids")
	pools := &fakeGatePools{drop: make(chan error, 1)}

	done := make(chan error, 1)
	go func() {
		_, err := runGrantedGate(t.Context(), pools, gateGrantRequest(), gateRun{
			Argv:     []string{"/bin/sh", "-c", `sleep 60 & echo "$$ $!" > "$PIDS"; sleep 60`},
			Dir:      t.TempDir(),
			Limit:    time.Minute,
			Name:     "closed-unscoped",
			StateDir: stateDir,
			Environ:  gateEnv("PIDS=" + pids),
		})
		done <- err
	}()

	group, child := gateParsePids(t, pids)
	pools.drop <- grants.ErrClosed
	var err error
	select {
	case err = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("runGrantedGate did not return after the grant's Acquire ended")
	}
	if errors.Is(err, errGateDetach) {
		t.Fatalf("err = %v: a gate with no scope must not detach", err)
	}
	if !errors.Is(err, errGateUngranted) || !errors.Is(err, grants.ErrClosed) {
		t.Fatalf("err = %v, want errGateUngranted wrapping grants.ErrClosed", err)
	}
	gateWaitGone(t, group, child)
}

// TestGateReadoptScopedCloseWhileQueuedDetaches checks that a re-adopted gate with a Scope whose reclaim is still
// queued when the broker closes is detached: its holder keeps running with no grant, and the attempt returns
// errGateDetach.
func TestGateReadoptScopedCloseWhileQueuedDetaches(t *testing.T) {
	t.Parallel()
	stateDir := gateShortStateDir(t)
	counter := filepath.Join(t.TempDir(), "counter")
	release := filepath.Join(t.TempDir(), "release")
	run := gateReadoptRun(t, stateDir, "closed-readopt", counter, release)
	run.Scope = fakeSystemdRunPath(t)
	gateDetachedRun(t, &fakeGatePools{}, run, counter)

	pools := &queuedClosePools{closing: make(chan struct{}), queued: make(chan struct{}, 1)}
	done := make(chan error, 1)
	go func() {
		_, err := readoptGrantedGate(t.Context(), pools, gateGrantRequest(), run, "grant-1")
		done <- err
	}()

	waitAcquireQueued(t, pools)
	close(pools.closing)
	var err error
	select {
	case err = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("readoptGrantedGate did not return after the broker closed")
	}
	if !errors.Is(err, errGateDetach) {
		t.Fatalf("err = %v, want errGateDetach", err)
	}
	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatalf("release the gate: %v", err)
	}
	exit := gateWaitExit(t, holderDir(t, stateDir, run.Name))
	if exit.Reason != holder.ReasonExited || exit.Code != 3 || !strings.Contains(exit.Stderr, "done") {
		t.Fatalf("exit record = %+v, want the gate's own exit 3 with its output: the holder was not left running", exit)
	}
}

// TestStepDoneGateCloseStoresNoEndAndStopsNoSession checks that a writing step whose gate, with a Scope, meets
// grants.ErrClosed while it runs stores no end, stops no session and runs no cleanup, so its row stays open for
// re-adoption.
func TestStepDoneGateCloseStoresNoEndAndStopsNoSession(t *testing.T) {
	t.Parallel()
	dir, start := stepDoneRepo(t)
	commit := stepGitTestCommit(t, dir, map[string]string{stepDoneOwned: "owned\n"})
	started := filepath.Join(t.TempDir(), "started")
	release := filepath.Join(t.TempDir(), "release")
	gate := stepDoneScript(t, fmt.Sprintf("echo started > %[1]q; while [ ! -e %[2]q ]; do sleep 0.05; done",
		started, release))
	pools := &fakeGatePools{drop: make(chan error, 1)}
	configure := stepDoneWriting(dir, start, []string{gate}, time.Minute, pools)
	f := newStepDoneFixture(t, func(cfg *stepRunConfig) {
		configure(cfg)
		cfg.scope = fakeSystemdRunPath(t)
		cfg.stateDir = gateShortStateDir(t)
	})
	t.Cleanup(func() { _ = os.WriteFile(release, nil, 0o600) })
	_, done := f.start(t)

	f.run.Return(stepDoneReturn(commit))
	gateWaitFile(t, started)
	pools.drop <- grants.ErrClosed
	waitRunEnd(t, done)

	f.settle(t)
	row := f.row(t)
	if row.End != "" || !row.Ended.IsZero() {
		t.Errorf("end = %q ended %v, want no end while the gate is left running", row.End, row.Ended)
	}
	if got := f.sessions.stoppedIDs(); len(got) != 0 {
		t.Errorf("Stop called for %v, want no session stopped", got)
	}
	if f.cleaned != 0 {
		t.Errorf("cleanup ran %d times, want 0", f.cleaned)
	}
	select {
	case <-f.run.ended:
		t.Error("ended is closed, want it open for re-adoption")
	default:
	}
}
