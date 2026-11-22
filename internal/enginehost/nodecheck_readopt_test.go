package enginehost

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/grants"
	"github.com/joshsymonds/orrery/internal/hostexec"
	"github.com/joshsymonds/orrery/internal/store"
)

// nodeCheckReadoptScript returns the argv of a check that appends a line to started each time it runs, waits until
// release exists, then prints done and exits 3. A readopt that ran it again would append a second line.
func nodeCheckReadoptScript(started, release string) []string {
	return []string{"/bin/sh", "-c", fmt.Sprintf(
		"echo run >> '%s'; while [ ! -e '%s' ]; do sleep 0.05; done; echo done; exit 3", started, release,
	)}
}

// nodeCheckHolderDir returns the holder directory of check's gate, which the gate serves under the check's name.
func nodeCheckHolderDir(t *testing.T, checks *NodeChecks, check store.NodeCheck) string {
	t.Helper()
	run := checks.gate(filepath.Join(checks.worktrees, check.ID), check, nil)
	run.Host = gateTestHost()
	plan, err := planGate(run)
	if err != nil {
		t.Fatalf("plan the gate of node check %s: %v", check.ID, err)
	}
	return plan.dir
}

// TestNodeChecksResumeEndsADetachedGateWithItsExitOnce detaches the gate of a check, then lets the gate end under its
// holder. Resume reads the holder's exit record and tails and ends the check once. A second Resume records nothing
// more.
func TestNodeChecksResumeEndsADetachedGateWithItsExitOnce(t *testing.T) {
	t.Parallel()
	f := nodeCheckSetup(t)
	started := filepath.Join(t.TempDir(), "started")
	release := filepath.Join(t.TempDir(), "release")
	t.Cleanup(func() { _ = os.WriteFile(release, nil, 0o600) })
	check := nodeCheckOf(f.head, 60, nodeCheckReadoptScript(started, release)...)
	if err := f.st.AddNodeCheck(t.Context(), check, time.Unix(1700000000, 0)); err != nil {
		t.Fatalf("store AddNodeCheck: %v", err)
	}

	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	var runErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, runErr = f.checks.run(ctx, hostexec.NewLocal(), f.repo, check)
	}()
	gateWaitFile(t, started, done)
	cancel(errGateDetach)
	evidenceCaptureWait(t, done)
	if !errors.Is(runErr, errGateDetach) {
		t.Fatalf("detached run = %v, want errGateDetach", runErr)
	}
	// A restart removes the worktree directory, which readopt re-adds.
	if err := os.RemoveAll(filepath.Join(f.checks.worktrees, nodeCheckID)); err != nil {
		t.Fatalf("remove the worktree directory: %v", err)
	}
	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatalf("release the gate: %v", err)
	}
	holderDir := nodeCheckHolderDir(t, f.checks, check)
	gateWaitExit(t, holderDir)
	gateWaitLockFree(t, holderDir)

	if err := f.checks.Resume(t.Context()); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	nodeCheckEnded(t, f.ended)
	got := nodeCheckStored(t, f.st)
	if got.State != store.NodeEnded || got.Exit != 3 || got.TimedOut || got.NotRun != "" || got.Stdout != "done\n" {
		t.Fatalf("stored check = %+v, want ended with exit 3 and stdout %q", got, "done\n")
	}
	if got.Grant != "grant-1" {
		t.Fatalf("grant %q, want the grant the gate held", got.Grant)
	}
	if n := evidenceReadoptLines(t, started); n != 1 {
		t.Fatalf("gate ran %d times, want once", n)
	}

	if err := f.checks.Resume(t.Context()); err != nil {
		t.Fatalf("second Resume: %v", err)
	}
	f.checks.Close()
	if n := len(f.ended); n != 0 {
		t.Fatalf("second Resume reported %d ends, want none", n)
	}
	if again := nodeCheckStored(t, f.st); !reflect.DeepEqual(again, got) {
		t.Fatalf("stored check after the second Resume = %+v, want %+v", again, got)
	}
}

// TestNodeChecksResumeEndsAGateItCannotReadoptAsLostAtARestart stores a check with a grant whose holder left nothing to
// read: its directory has no lock and no exit record. Resume ends the check lost at a restart, clears the holder
// directory, and requests no grant, so the gate is never run again.
func TestNodeChecksResumeEndsAGateItCannotReadoptAsLostAtARestart(t *testing.T) {
	t.Parallel()
	f := nodeCheckSetup(t)
	check := nodeCheckOf(f.head, 60, "/bin/sh", "-c", "exit 0")
	if err := f.st.AddNodeCheck(t.Context(), check, time.Unix(1700000000, 0)); err != nil {
		t.Fatalf("store AddNodeCheck: %v", err)
	}
	holderDir := nodeCheckHolderDir(t, f.checks, check)
	if err := os.MkdirAll(holderDir, 0o700); err != nil {
		t.Fatalf("make the holder directory: %v", err)
	}
	if err := f.st.SetNodeCheckGrant(t.Context(), nodeCheckID, "grant-1", holderDir); err != nil {
		t.Fatalf("SetNodeCheckGrant: %v", err)
	}

	if err := f.checks.Resume(t.Context()); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	nodeCheckEnded(t, f.ended)
	got := nodeCheckStored(t, f.st)
	if got.State != store.NodeEnded || got.NotRun != "lost at a restart" {
		t.Fatalf("stored check = %+v, want ended lost at a restart", got)
	}
	if n := len(f.pools.requested()); n != 0 {
		t.Fatalf("grant requests = %d, want none: a lost gate is not run again", n)
	}
	if _, err := os.Lstat(holderDir); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("holder directory after the lost check: err = %v, want it cleared", err)
	}
}

// TestNodeChecksResumeRunsAnUngrantedCheckFromTheStart stores a running check with no grant, whose worktree a stopped
// start left at its path. Resume removes that worktree, runs the check once, and ends it with its exit and output.
func TestNodeChecksResumeRunsAnUngrantedCheckFromTheStart(t *testing.T) {
	t.Parallel()
	f := nodeCheckSetup(t)
	check := nodeCheckOf(f.head, 60, "/bin/sh", "-c", "echo out; exit 0")
	if err := f.st.AddNodeCheck(t.Context(), check, time.Unix(1700000000, 0)); err != nil {
		t.Fatalf("store AddNodeCheck: %v", err)
	}
	left := filepath.Join(f.checks.worktrees, nodeCheckID)
	if err := addStepWorktree(t.Context(), hostexec.NewLocal(), f.repo, left, f.head); err != nil {
		t.Fatalf("add the worktree a stop left: %v", err)
	}

	if err := f.checks.Resume(t.Context()); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	nodeCheckEnded(t, f.ended)
	got := nodeCheckStored(t, f.st)
	if got.State != store.NodeEnded || got.Exit != 0 || got.NotRun != "" ||
		got.Stdout != "out\n" || got.Grant != "grant-1" {
		t.Fatalf("stored check = %+v, want ended with exit 0, stdout %q, under grant-1", got, "out\n")
	}
	if n := len(f.pools.requested()); n != 1 {
		t.Fatalf("grant requests = %d, want 1", n)
	}
	if _, err := os.Lstat(left); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("worktree after the check: err = %v, want it removed", err)
	}
}

// TestNodeChecksResumeLeavesACheckItDrivesAlone resumes a check with no grant whose gate waits for its release. The
// second Resume finds the check driven already, so it requests no grant, and the gate runs once and its end is recorded
// once.
func TestNodeChecksResumeLeavesACheckItDrivesAlone(t *testing.T) {
	t.Parallel()
	f := nodeCheckSetup(t)
	started := filepath.Join(t.TempDir(), "started")
	release := filepath.Join(t.TempDir(), "release")
	t.Cleanup(func() { _ = os.WriteFile(release, nil, 0o600) })
	check := nodeCheckOf(f.head, 60, nodeCheckReadoptScript(started, release)...)
	if err := f.st.AddNodeCheck(t.Context(), check, time.Unix(1700000000, 0)); err != nil {
		t.Fatalf("store AddNodeCheck: %v", err)
	}

	if err := f.checks.Resume(t.Context()); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	gateWaitFile(t, started, nil)
	if err := f.checks.Resume(t.Context()); err != nil {
		t.Fatalf("second Resume: %v", err)
	}
	if n := len(f.pools.requested()); n != 1 {
		t.Fatalf("grant requests = %d, want 1: the second Resume drives nothing", n)
	}
	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatalf("release the gate: %v", err)
	}
	nodeCheckEnded(t, f.ended)
	f.checks.Close()
	if n := len(f.ended); n != 0 {
		t.Fatalf("ends recorded beyond the first: %d, want none", n)
	}
	if n := evidenceReadoptLines(t, started); n != 1 {
		t.Fatalf("gate ran %d times, want once", n)
	}
	if got := nodeCheckStored(t, f.st); got.State != store.NodeEnded || got.Exit != 3 {
		t.Fatalf("stored check = %+v, want ended with exit 3", got)
	}
}

// TestNodeChecksReadoptCutBeforeItsWorktreeLeavesTheCheckForARestart readopts a stored grant under a context that has
// already ended, so the worktree readopt re-adds is cut before the gate is touched. The check stays running with its
// holder left in place, and no grant is requested, for a restart to readopt.
func TestNodeChecksReadoptCutBeforeItsWorktreeLeavesTheCheckForARestart(t *testing.T) {
	t.Parallel()
	f := nodeCheckSetup(t)
	check := nodeCheckOf(f.head, 60, "/bin/sh", "-c", "exit 0")
	if err := f.st.AddNodeCheck(t.Context(), check, time.Unix(1700000000, 0)); err != nil {
		t.Fatalf("store AddNodeCheck: %v", err)
	}
	holderDir := nodeCheckHolderDir(t, f.checks, check)
	if err := os.MkdirAll(holderDir, 0o700); err != nil {
		t.Fatalf("make the holder directory: %v", err)
	}
	marker := filepath.Join(holderDir, "marker")
	if err := os.WriteFile(marker, nil, 0o600); err != nil {
		t.Fatalf("write the holder marker: %v", err)
	}
	if err := f.st.SetNodeCheckGrant(t.Context(), nodeCheckID, "grant-1", holderDir); err != nil {
		t.Fatalf("SetNodeCheckGrant: %v", err)
	}
	stored := nodeCheckStored(t, f.st)
	cut, cancel := context.WithCancel(t.Context())
	cancel()

	f.checks.readoptDrive(cut, f.repo, stored)

	if got := nodeCheckStored(t, f.st); got.State != store.NodeRunning || got.NotRun != "" {
		t.Fatalf("stored check = %+v, want it still running with no not-run reason", got)
	}
	if n := len(f.pools.requested()); n != 0 {
		t.Fatalf("grant requests = %d, want none", n)
	}
	if _, err := os.Lstat(marker); err != nil {
		t.Fatalf("holder directory after the cut readopt: %v, want it left for a restart", err)
	}
}

// nodeCheckQueuedPools are the pools a test cuts a check's drive under. Each Acquire is reported on queued before the pools
// it wraps queue it.
type nodeCheckQueuedPools struct {
	PoolCommands
	queued chan struct{}
}

// Acquire reports the request on queued, then queues it as the wrapped pools do.
func (p nodeCheckQueuedPools) Acquire(ctx context.Context, req grants.Request, send func(grants.Event) error) error {
	p.queued <- struct{}{}
	return p.PoolCommands.Acquire(ctx, req, send)
}

// TestNodeChecksResumeDrivesACheckCutBeforeItsGateLaunched cuts the drive of a check while its grant request queues. The
// worktree is added and the gate has not launched, so the cut removes the worktree and records nothing: the check stays
// running with no grant. Resume drives it again from the start, and it ends once.
func TestNodeChecksResumeDrivesACheckCutBeforeItsGateLaunched(t *testing.T) {
	t.Parallel()
	f := nodeCheckSetup(t)
	check := nodeCheckOf(f.head, 60, "/bin/sh", "-c", "echo out; exit 0")
	if err := f.st.AddNodeCheck(t.Context(), check, time.Unix(1700000000, 0)); err != nil {
		t.Fatalf("store AddNodeCheck: %v", err)
	}
	queue := make(chan struct{})
	queued := make(chan struct{}, 2)
	f.pools.queue = queue
	f.checks.pools = nodeCheckQueuedPools{PoolCommands: f.pools, queued: queued}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		f.checks.drive(ctx, f.repo, check)
	}()
	<-queued
	cancel()
	evidenceCaptureWait(t, done)

	got := nodeCheckStored(t, f.st)
	if got.State != store.NodeRunning || got.NotRun != "" || got.Grant != "" {
		t.Fatalf("stored check = %+v, want it running with no grant and no not-run reason", got)
	}
	if n := len(f.ended); n != 0 {
		t.Fatalf("ends recorded by the cut drive = %d, want none", n)
	}
	if _, err := os.Lstat(filepath.Join(f.checks.worktrees, nodeCheckID)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("worktree of the cut check: err = %v, want none", err)
	}

	close(queue)
	if err := f.checks.Resume(t.Context()); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	nodeCheckEnded(t, f.ended)
	got = nodeCheckStored(t, f.st)
	if got.State != store.NodeEnded || got.Exit != 0 || got.NotRun != "" || got.Stdout != "out\n" {
		t.Fatalf("stored check = %+v, want ended with exit 0 and stdout %q", got, "out\n")
	}
	if n := len(f.pools.requested()); n != 2 {
		t.Fatalf("grant requests = %d, want 2: the cut request and the restart's", n)
	}
}

// staleOpenStore lists open as the checks a restart takes up, whatever the store holds by then, as a Resume whose list
// was read before a drive ended a check.
type staleOpenStore struct {
	NodeCheckStore
	open []store.NodeCheck
}

// OpenNodeChecks returns the stale list.
func (s staleOpenStore) OpenNodeChecks(context.Context) ([]store.NodeCheck, error) {
	return s.open, nil
}

// TestNodeChecksResumeLeavesACheckThatEndedAfterItsListAlone gives Resume a list read while a check was running, then
// ends the check before Resume takes it up. Resume reads the check again, finds it ended, and runs nothing.
func TestNodeChecksResumeLeavesACheckThatEndedAfterItsListAlone(t *testing.T) {
	t.Parallel()
	f := nodeCheckSetup(t)
	started := filepath.Join(t.TempDir(), "started")
	check := nodeCheckOf(f.head, 60, "/bin/sh", "-c", "echo run >> '"+started+"'")
	if err := f.st.AddNodeCheck(t.Context(), check, time.Unix(1700000000, 0)); err != nil {
		t.Fatalf("store AddNodeCheck: %v", err)
	}
	listed := nodeCheckStored(t, f.st)
	if err := f.st.EndNodeCheck(t.Context(), check.ID, store.NodeCheckEnd{}, time.Unix(1700000001, 0)); err != nil {
		t.Fatalf("store EndNodeCheck: %v", err)
	}
	f.checks.store = staleOpenStore{NodeCheckStore: f.checks.store, open: []store.NodeCheck{listed}}

	if err := f.checks.Resume(t.Context()); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	// Wait for any drive Resume started to run to its end, without canceling it.
	f.checks.drives.Wait()
	if _, err := os.Lstat(started); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("started file after Resume: err = %v, want the ended check not run again", err)
	}
	if n := len(f.pools.requested()); n != 0 {
		t.Fatalf("grant requests = %d, want none", n)
	}
}
