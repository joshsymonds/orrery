package enginehost

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/gates"
	"github.com/joshsymonds/orrery/internal/grants"
	"github.com/joshsymonds/orrery/internal/store"
)

// nodeCheckEpic is the id of the epic the check tests run checks of.
const nodeCheckEpic = "check-epic"

// nodeCheckID is the id the tests give their checks. It is a valid check id.
const nodeCheckID = "0123456789abcdef0123456789abcdef"

// nodeCheckStore is the store a test's NodeChecks writes to. EndNodeCheck passes through to the store, then reports its
// error on ended, so a test waits for a check's end as an event on the store.
type nodeCheckStore struct {
	*store.Store
	ended chan error
}

// EndNodeCheck ends the check in the store, and reports the error on ended.
func (s nodeCheckStore) EndNodeCheck(ctx context.Context, id string, end store.NodeCheckEnd, at time.Time) error {
	err := s.Store.EndNodeCheck(ctx, id, end, at)
	s.ended <- err
	return err
}

// nodeCheckFixture is a NodeChecks under test: its store, the channel its ends are reported on, the scratch repository and
// its HEAD the checks run at, and the pools it runs under.
type nodeCheckFixture struct {
	checks     *NodeChecks
	ended      <-chan error
	st         *store.Store
	repo, head string
	pools      *fakeGatePools
}

// nodeCheckSetup returns a fixture over a scratch repository, a store seeded with its epic, and NodeChecks that run with
// fake pools in scratch state directories. Close runs when the test ends.
func nodeCheckSetup(t *testing.T) nodeCheckFixture {
	t.Helper()
	repo := stepGitTestRepo(t)
	head := stepGitTestRun(t, repo, "rev-parse", "HEAD")
	st := openTestStore(t, filepath.Join(t.TempDir(), "orrery.db"))
	created := time.Unix(1700000000, 0)
	if err := st.CreateEpic(t.Context(), store.EpicRow{
		ID: nodeCheckEpic, Repository: repo, Title: "Synthetic check epic.", Intent: "Synthetic intent.",
		Care: "serious", Phase: "accepted", Created: created, Changed: created,
	}); err != nil {
		t.Fatalf("store CreateEpic: %v", err)
	}
	home := t.TempDir()
	stepGateWriteFile(t, filepath.Join(home, "orrery", "pools.toml"), stepGatePoolsText("gate"))
	ended := make(chan error, 4)
	pools := &fakeGatePools{}
	checks := NewNodeChecks(NodeChecksConfig{
		Store:        nodeCheckStore{Store: st, ended: ended},
		Pools:        pools,
		Environ:      gateEnv("XDG_CONFIG_HOME=" + home),
		StateDir:     t.TempDir(),
		GateStateDir: gateShortStateDir(t),
	})
	t.Cleanup(checks.Close)
	return nodeCheckFixture{checks: checks, ended: ended, st: st, repo: repo, head: head, pools: pools}
}

// nodeCheckOf returns a check of nodeCheckEpic at revision, whose argv is argv and whose limit is limit seconds.
func nodeCheckOf(revision string, limit int64, argv ...string) store.NodeCheck {
	return store.NodeCheck{
		ID: nodeCheckID, Epic: nodeCheckEpic, Path: "check", Revision: revision, Argv: argv, Limit: limit,
	}
}

// nodeCheckEnded waits until a check's end is reported on ended, and fails the test when the store did not record it.
// The wait has no deadline of its own: the test binary's timeout bounds a hang.
func nodeCheckEnded(t *testing.T, ended <-chan error) {
	t.Helper()
	if err := <-ended; err != nil {
		t.Fatalf("end node check: %v", err)
	}
}

// nodeCheckStored returns the stored check with nodeCheckID, and fails the test when it cannot be read.
func nodeCheckStored(t *testing.T, st *store.Store) store.NodeCheck {
	t.Helper()
	got, err := st.NodeCheck(t.Context(), nodeCheckID)
	if err != nil {
		t.Fatalf("read node check %s: %v", nodeCheckID, err)
	}
	return got
}

// TestNodeChecksCommandExitsZeroAndKeepsItsTails runs a command that writes a file in its working directory, then prints to
// stdout and stderr and exits 0. The check is stored running and then ended with exit 0 and the two tails, under the grant
// and holder it ran with. The file is in the check's worktree, not the repository, and the worktree is removed.
func TestNodeChecksCommandExitsZeroAndKeepsItsTails(t *testing.T) {
	t.Parallel()
	f := nodeCheckSetup(t)
	argv := []string{"/bin/sh", "-c", "pwd > ran.txt; echo out; echo err >&2"}

	started, err := f.checks.Run(t.Context(), nodeCheckOf(f.head, 60, argv...))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if started.ID != nodeCheckID || started.State != store.NodeRunning {
		t.Fatalf("Run = %+v, want the running check %s", started, nodeCheckID)
	}
	nodeCheckEnded(t, f.ended)

	got := nodeCheckStored(t, f.st)
	if got.State != store.NodeEnded || got.NotRun != "" || got.TimedOut || got.Exit != 0 {
		t.Fatalf("stored check = %+v, want ended with exit 0 and not run or timed out", got)
	}
	if got.Stdout != "out\n" || got.Stderr != "err\n" {
		t.Fatalf("stdout %q stderr %q, want %q and %q", got.Stdout, got.Stderr, "out\n", "err\n")
	}
	if got.Grant != "grant-1" || got.Holder == "" {
		t.Fatalf("grant %q holder %q, want the grant it held and its holder", got.Grant, got.Holder)
	}
	if _, err = os.Lstat(filepath.Join(f.repo, "ran.txt")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("ran.txt in the repository: err = %v, want the command to run in its worktree", err)
	}
	if _, err = os.Lstat(filepath.Join(f.checks.worktrees, nodeCheckID)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("worktree after the check: err = %v, want it removed", err)
	}
}

// TestNodeChecksEndsWithTheExitOfItsCommand runs a command that exits 3, and checks that the check ends with exit 3.
func TestNodeChecksEndsWithTheExitOfItsCommand(t *testing.T) {
	t.Parallel()
	f := nodeCheckSetup(t)
	if _, err := f.checks.Run(t.Context(), nodeCheckOf(f.head, 60, "/bin/sh", "-c", "exit 3")); err != nil {
		t.Fatalf("Run: %v", err)
	}
	nodeCheckEnded(t, f.ended)

	got := nodeCheckStored(t, f.st)
	if got.State != store.NodeEnded || got.Exit != 3 || got.TimedOut || got.NotRun != "" {
		t.Fatalf("stored check = %+v, want ended with exit 3", got)
	}
}

// TestNodeChecksEndsTimedOutPastItsLimit runs a command that sleeps past its one-second limit.
func TestNodeChecksEndsTimedOutPastItsLimit(t *testing.T) {
	t.Parallel()
	f := nodeCheckSetup(t)
	if _, err := f.checks.Run(t.Context(), nodeCheckOf(f.head, 1, "/bin/sh", "-c", "sleep 30")); err != nil {
		t.Fatalf("Run: %v", err)
	}
	nodeCheckEnded(t, f.ended)

	got := nodeCheckStored(t, f.st)
	if got.State != store.NodeEnded || !got.TimedOut {
		t.Fatalf("stored check = %+v, want ended and timed out", got)
	}
	if _, err := os.Lstat(filepath.Join(f.checks.worktrees, nodeCheckID)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("worktree after the timed out check: err = %v, want it removed", err)
	}
}

// TestNodeChecksEndsNotRunWhenItsProgramDoesNotStart runs argv whose program is sh, which is not a path. The program is
// read relative to the worktree and never looked up on PATH, and nothing runs it through a shell, so the check ends not run.
func TestNodeChecksEndsNotRunWhenItsProgramDoesNotStart(t *testing.T) {
	t.Parallel()
	f := nodeCheckSetup(t)
	if _, err := f.checks.Run(t.Context(), nodeCheckOf(f.head, 60, "sh", "-c", "echo hi")); err != nil {
		t.Fatalf("Run: %v", err)
	}
	nodeCheckEnded(t, f.ended)

	got := nodeCheckStored(t, f.st)
	if got.State != store.NodeEnded || !strings.HasPrefix(got.NotRun, "could not start: ") {
		t.Fatalf("stored check = %+v, want ended not run with a could not start reason", got)
	}
	if got.Stdout != "" {
		t.Fatalf("stdout %q, want none: the program never ran", got.Stdout)
	}
	if _, err := os.Lstat(filepath.Join(f.checks.worktrees, nodeCheckID)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("worktree after the check: err = %v, want it removed", err)
	}
}

// TestNodeChecksHoldsTheNeedsOfItsGate runs a check that names its gate's needs, and a check that names none. The grant
// request of the first holds the needs it names, and the second holds one member of the default gate pool. Each request
// carries the check's argv.
func TestNodeChecksHoldsTheNeedsOfItsGate(t *testing.T) {
	t.Parallel()
	f := nodeCheckSetup(t)
	argv := []string{"/bin/sh", "-c", "exit 0"}

	gated := nodeCheckOf(f.head, 60, argv...)
	gated.Needs = []store.EpicGateNeed{{Pool: "gate", Count: 2}}
	if _, err := f.checks.Run(t.Context(), gated); err != nil {
		t.Fatalf("Run gated: %v", err)
	}
	nodeCheckEnded(t, f.ended)

	plain := nodeCheckOf(f.head, 60, argv...)
	plain.ID = "fedcba9876543210fedcba9876543210"
	if _, err := f.checks.Run(t.Context(), plain); err != nil {
		t.Fatalf("Run plain: %v", err)
	}
	nodeCheckEnded(t, f.ended)

	requests := f.pools.requested()
	if len(requests) != 2 {
		t.Fatalf("grant requests = %d, want 2", len(requests))
	}
	if want := []grants.Need{{Pool: "gate", Count: 2}}; !reflect.DeepEqual(requests[0].Needs, want) {
		t.Fatalf("gated needs = %+v, want %+v", requests[0].Needs, want)
	}
	if want := []grants.Need{{Pool: gates.DefaultPool, Count: 1}}; !reflect.DeepEqual(requests[1].Needs, want) {
		t.Fatalf("plain needs = %+v, want %+v", requests[1].Needs, want)
	}
	if !reflect.DeepEqual(requests[0].Command, argv) {
		t.Fatalf("grant request command = %q, want the check's argv %q", requests[0].Command, argv)
	}
}

// TestNodeChecksSecondRunReturnsTheStoredCheck runs a check to its end, then runs another check under the same id with a
// different argv. The second Run returns the stored check, which ended with the first check's exit, and runs nothing.
func TestNodeChecksSecondRunReturnsTheStoredCheck(t *testing.T) {
	t.Parallel()
	f := nodeCheckSetup(t)
	first := []string{"/bin/sh", "-c", "exit 0"}
	if _, err := f.checks.Run(t.Context(), nodeCheckOf(f.head, 60, first...)); err != nil {
		t.Fatalf("Run: %v", err)
	}
	nodeCheckEnded(t, f.ended)

	got, err := f.checks.Run(t.Context(), nodeCheckOf(f.head, 60, "/bin/sh", "-c", "exit 3"))
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if got.State != store.NodeEnded || got.Exit != 0 || !reflect.DeepEqual(got.Argv, first) {
		t.Fatalf("second Run = %+v, want the stored check that exited 0 with argv %q", got, first)
	}
	if n := len(f.pools.requested()); n != 1 {
		t.Fatalf("grant requests = %d, want 1: the second Run runs nothing", n)
	}
}

// TestNodeChecksStoresANotRunCheckEndedAndRunsNothing runs a check given with its not run reason. It is stored ended, with
// that reason, and nothing is requested from the pools or added as a worktree.
func TestNodeChecksStoresANotRunCheckEndedAndRunsNothing(t *testing.T) {
	t.Parallel()
	f := nodeCheckSetup(t)
	unrun := nodeCheckOf(f.head, 60, "/bin/sh", "-c", "exit 0")
	unrun.NotRun = "gate has no argv"
	got, err := f.checks.Run(t.Context(), unrun)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.State != store.NodeEnded || got.NotRun != unrun.NotRun {
		t.Fatalf("Run = %+v, want the check ended not run", got)
	}
	stored := nodeCheckStored(t, f.st)
	if stored.State != store.NodeEnded || stored.NotRun != unrun.NotRun {
		t.Fatalf("stored check = %+v, want it ended not run", stored)
	}
	if n := len(f.pools.requested()); n != 0 {
		t.Fatalf("grant requests = %d, want none", n)
	}
	if _, err = os.Lstat(filepath.Join(f.checks.worktrees, nodeCheckID)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("worktree of the not run check: err = %v, want none", err)
	}
}

// TestNodeChecksRefusesABadIDAndStoresNothing runs a check whose id is not 32 lowercase hex digits. Run refuses it with
// ErrBadID, stores nothing and runs nothing.
func TestNodeChecksRefusesABadIDAndStoresNothing(t *testing.T) {
	t.Parallel()
	f := nodeCheckSetup(t)
	bad := nodeCheckOf(f.head, 60, "/bin/sh", "-c", "exit 0")
	bad.ID = "not-a-node-check-id"
	if _, err := f.checks.Run(t.Context(), bad); !errors.Is(err, ErrBadID) {
		t.Fatalf("Run with id %q: err = %v, want ErrBadID", bad.ID, err)
	}
	if _, err := f.st.NodeCheck(t.Context(), bad.ID); !errors.Is(err, store.ErrNoNodeCheck) {
		t.Fatalf("stored check after the refusal: err = %v, want ErrNoNodeCheck", err)
	}
	if n := len(f.pools.requested()); n != 0 {
		t.Fatalf("grant requests = %d, want none", n)
	}
}

// TestNodeChecksLeavesACheckCutBeforeItsWorktreeForARestart drives a stored running check under a context that has
// already ended, so its worktree add is cut. Nothing ran, so the check stays running for a restart to run, no grant is
// requested and no worktree is left.
func TestNodeChecksLeavesACheckCutBeforeItsWorktreeForARestart(t *testing.T) {
	t.Parallel()
	f := nodeCheckSetup(t)
	check := nodeCheckOf(f.head, 60, "/bin/sh", "-c", "exit 0")
	if err := f.st.AddNodeCheck(t.Context(), check, time.Unix(1700000000, 0)); err != nil {
		t.Fatalf("store AddNodeCheck: %v", err)
	}
	cut, cancel := context.WithCancel(t.Context())
	cancel()

	f.checks.drive(cut, f.repo, check)

	if got := nodeCheckStored(t, f.st); got.State != store.NodeRunning || got.NotRun != "" {
		t.Fatalf("stored check = %+v, want it still running with no not-run reason", got)
	}
	if n := len(f.pools.requested()); n != 0 {
		t.Fatalf("grant requests = %d, want none", n)
	}
	if _, err := os.Lstat(filepath.Join(f.checks.worktrees, nodeCheckID)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("worktree of the cut check: err = %v, want none", err)
	}
}

// TestNodeChecksRefusesRunAfterClose closes the checks and runs one. Run returns errNodeChecksClosed and stores nothing.
func TestNodeChecksRefusesRunAfterClose(t *testing.T) {
	t.Parallel()
	f := nodeCheckSetup(t)
	f.checks.Close()
	if _, err := f.checks.Run(t.Context(), nodeCheckOf(f.head, 60, "/bin/sh", "-c", "exit 0")); !errors.Is(
		err, errNodeChecksClosed,
	) {
		t.Fatalf("Run after Close: err = %v, want errNodeChecksClosed", err)
	}
	if _, err := f.st.NodeCheck(t.Context(), nodeCheckID); !errors.Is(err, store.ErrNoNodeCheck) {
		t.Fatalf("stored check after the refusal: err = %v, want ErrNoNodeCheck", err)
	}
}
