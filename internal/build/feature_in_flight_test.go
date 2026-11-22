package build

import (
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/hostexec"
)

// featureInFlightFixture returns a fixture whose task has passed red, with the implement worktree the engine before task
// branches added already in place: detached at the build base through AddWorktree. Its implementer step has not started,
// and the path of the worktree is returned.
func featureInFlightFixture(t *testing.T) (*passFixture, string) {
	t.Helper()
	f := newPassFixture(t)
	f.pass()
	f.addRun(f.lastRun().id, evidence.PhaseRed, f.base, 1, evidence.VerdictFails)
	path := ImplementWorktree(f.stateDir, passEpic, f.task())
	if err := AddWorktree(f.ctx, hostexec.NewLocal(), f.repo, path, f.base); err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}
	return f, path
}

// featureInFlightTaskBranches returns the refs under refs/heads/orrery/<slug>/ of the epic of the fixture, the task
// branches, one per line, and "" when there are none.
func featureInFlightTaskBranches(t *testing.T, repo string) string {
	t.Helper()
	return buildRun(t, repo, "for-each-ref", "--format=%(refname)", "refs/heads/orrery/"+Slug(passEpic)+"/")
}

// TestFeatureInFlightDetachedWorktreeKeepsItsSteps covers R3: an epic created before task branches whose task's implement
// worktree is already added detached at the build base runs its implementer step in that worktree, the worktree's HEAD
// stays detached, and the epic has no task branch.
func TestFeatureInFlightDetachedWorktreeKeepsItsSteps(t *testing.T) {
	t.Parallel()
	f, path := featureInFlightFixture(t)
	f.pass()

	start := f.lastStart()
	if start.ID != passStep(StageImplement, 1, 0) {
		t.Fatalf("step started = %s, want the implementer step %s", start.ID, passStep(StageImplement, 1, 0))
	}
	if start.Workspace != path {
		t.Errorf("implementer step workspace = %s, want the existing worktree %s", start.Workspace, path)
	}
	if got := buildRun(t, path, "rev-parse", "--symbolic-full-name", "HEAD"); got != "HEAD" {
		t.Errorf("worktree HEAD is %q, want it to stay detached", got)
	}
	if got := buildHead(t, path); got != f.base {
		t.Errorf("worktree HEAD = %s, want the build base %s", got, f.base)
	}
	if got := featureInFlightTaskBranches(t, f.repo); got != "" {
		t.Errorf("task branches = %q, want none", got)
	}
}

// TestFeatureInFlightAcceptPinsWithNoTaskBranch covers R3: accepting a task of an epic created before task branches
// pins refs/orrery/epic/T1 at its revision, and the epic still has no task branch.
func TestFeatureInFlightAcceptPinsWithNoTaskBranch(t *testing.T) {
	t.Parallel()
	f, path := featureInFlightFixture(t)
	f.pass()
	commit := buildCommit(t, path)
	f.setStep(passStep(StageImplement, 1, 0), "done", `{"summary":"done","commit":"`+commit+`"}`)
	f.pass()
	f.addRun(passRunID(StageGreen, 1), evidence.PhaseGreen, commit, 0, evidence.VerdictHolds)
	f.pass()
	if task := f.task(); task.Status != "done" {
		t.Fatalf("task status %q, want done", task.Status)
	}
	if got := buildRun(t, f.repo, "rev-parse", "refs/orrery/epic/T1"); got != commit {
		t.Errorf("refs/orrery/epic/T1 = %s, want the accepted revision %s", got, commit)
	}
	if got := featureInFlightTaskBranches(t, f.repo); got != "" {
		t.Errorf("task branches = %q, want none", got)
	}
}
