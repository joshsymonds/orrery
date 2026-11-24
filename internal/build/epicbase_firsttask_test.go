package build

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/storetest"
)

// newMovedBaseFixture returns a fixture for an epic that starts the way a planner turn starts it. The epic has no task
// and no branch, and its base moves to origin's main before the turn plans task 1 at it. The checkout's origin is a bare
// repository whose main is one commit ahead of the epic's base, and the checkout has fetched that commit. It returns the
// fixture and the commit origin's main names, which is the commit the base moves to.
func newMovedBaseFixture(t *testing.T) (*passFixture, string) {
	t.Helper()
	ctx := context.Background()
	setup := newStartSetup(t, readoptCommandGate("check"), readoptReviewGate())
	repo := setup.epic.Repository
	origin := filepath.Join(t.TempDir(), "origin.git")
	buildRun(t, repo, "init", "-q", "--bare", "-b", "main", origin)
	buildRun(t, repo, "remote", "add", "origin", origin)
	buildRun(t, repo, "push", "-q", "origin", "HEAD:refs/heads/main")

	clone := t.TempDir()
	buildRun(t, clone, "clone", "-q", origin, ".")
	ahead := buildCommit(t, clone)
	buildRun(t, clone, "push", "-q", "origin", "HEAD:refs/heads/main")
	if got, err := enginehost.FetchOriginMain(ctx, repo); err != nil || got != ahead {
		t.Fatalf("FetchOriginMain = %s, %v, want %s", got, err, ahead)
	}

	path := filepath.Join(t.TempDir(), "orrery.db")
	storetest.Seed(t, path)
	s, err := store.Open(ctx, path, storetest.NoSync())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	if err = s.CreateEpic(ctx, store.EpicRow{
		ID: passEpic, Repository: repo, Base: setup.base, StoredBase: setup.base, Title: "Build the thing",
		Phase: "accepted", Created: passClock(), Changed: passClock(), Gates: setup.epic.Gates,
	}); err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
	moved, err := s.MoveEpicBase(ctx, passEpic, setup.base, ahead, passClock())
	if err != nil || !moved {
		t.Fatalf("MoveEpicBase = %t, %v, want the base moved to %s", moved, err, ahead)
	}
	if _, err = s.ClaimTurn(ctx, passEpic, passClock()); err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	if err = s.CompleteTurn(ctx, passEpic, 1, passPlan(), passClock()); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
	steps := &passSteps{rows: make(map[string]store.StepRow)}
	fakeEvidence := &passEvidence{store: s}
	return &passFixture{
		t: t, ctx: ctx, store: s, repo: repo, base: ahead, stateDir: setup.stateDir,
		rolesPath: setup.rolesPath, getenv: setup.getenv, steps: steps, evidence: fakeEvidence,
		runner: New(Config{
			Store: s, Steps: steps, Evidence: fakeEvidence, Getenv: setup.getenv, StateDir: setup.stateDir,
			Now: passClock,
		}),
	}, ahead
}

// TestEpicBaseFirstTaskBuildStartsOnMovedBase covers R2: the epic's branch, which the first build pass creates at the
// epic's base, names the commit the base moved to; the first task's red run starts at that commit; and once that
// run fails, the implementer step starts at that commit too, on the task's first branch, T1.1, which names it. The git
// repositories are real because the base move reads origin's main through a real fetch and ancestry check, and the
// build branch is a real git ref. The test makes one newMovedBaseFixture, with one invented repository and bare
// origin, for all three checks, rather than one for each.
func TestEpicBaseFirstTaskBuildStartsOnMovedBase(t *testing.T) {
	t.Parallel()
	f, moved := newMovedBaseFixture(t)
	f.pass()
	if got := buildRun(t, f.repo, "rev-parse", BranchRef(passEpic)); got != moved {
		t.Errorf("%s = %s, want the moved base %s", BranchRef(passEpic), got, moved)
	}
	if got := f.lastRun().revision; got != moved {
		t.Errorf("the first task's red run starts at %s, want the moved base %s", got, moved)
	}
	f.addRun(f.lastRun().id, evidence.PhaseRed, moved, 1, evidence.VerdictFails)
	f.pass()
	start := f.lastStart()
	if start.Revision != moved {
		t.Errorf("implementer step starts at revision %s, want the moved base %s", start.Revision, moved)
	}
	const branch = "refs/heads/orrery/epic/T1.1"
	if got := buildRun(t, start.Workspace, "rev-parse", "--symbolic-full-name", "HEAD"); got != branch {
		t.Errorf("worktree HEAD is attached to %s, want %s", got, branch)
	}
	if got := buildRun(t, f.repo, "rev-parse", branch); got != moved {
		t.Errorf("%s = %s, want the moved base %s", branch, got, moved)
	}
}
