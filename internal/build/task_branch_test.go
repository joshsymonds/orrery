package build

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// TestTaskBranchFirstStepChecksOutT1AtBuildBase covers R1: the first implementer step of a task runs in a worktree
// whose HEAD is attached to the task's first task branch, T1.1, and that branch names the build base.
func TestTaskBranchFirstStepChecksOutT1AtBuildBase(t *testing.T) {
	t.Parallel()
	f := newImplementerFixture(t)
	workspace := f.lastStart().Workspace
	const branch = "refs/heads/orrery/epic/T1.1"
	if got := buildRun(t, workspace, "rev-parse", "--symbolic-full-name", "HEAD"); got != branch {
		t.Errorf("worktree HEAD is attached to %s, want %s", got, branch)
	}
	if got := buildRun(t, f.repo, "rev-parse", branch); got != f.base {
		t.Errorf("%s = %s, want the build base %s", branch, got, f.base)
	}
}

// TestTaskBranchSendBackCutsNextBranchAndLeavesOld covers R1: after an implementer commits on T1.1 and the planner
// sends the task back, the reopened implementer step runs on T1.2 at the reopened base, and T1.1 still names that
// commit.
func TestTaskBranchSendBackCutsNextBranchAndLeavesOld(t *testing.T) {
	t.Parallel()
	plan := passPlan()
	plan.Actions = []store.EpicActionInput{{Kind: store.ActionSendBack, Task: 1, Reason: sendBackReason}}
	f := newPlanFixture(t, plan)
	f.pass()
	f.addRun(f.lastRun().id, evidence.PhaseRed, f.base, 1, evidence.VerdictFails)
	f.pass()
	old := f.lastStart()
	commit := buildCommit(t, old.Workspace)
	f.carryPendingSendBack()

	before := len(f.steps.starts)
	f.pass()
	if got := len(f.steps.starts) - before; got != 1 {
		t.Fatalf("Start calls after the send_back = %d, want one new implement step", got)
	}
	start := f.steps.starts[before]
	const first, second = "refs/heads/orrery/epic/T1.1", "refs/heads/orrery/epic/T1.2"
	if got := buildRun(t, start.Workspace, "rev-parse", "--symbolic-full-name", "HEAD"); got != second {
		t.Errorf("reopened worktree HEAD is attached to %s, want %s", got, second)
	}
	base := f.task().Build.Base
	if got := buildRun(t, f.repo, "rev-parse", second); got != base {
		t.Errorf("%s = %s, want the reopened base %s", second, got, base)
	}
	if got := buildRun(t, f.repo, "rev-parse", first); got != commit {
		t.Errorf("%s = %s, want the commit the first attempt made %s", first, got, commit)
	}
}

// TestTaskBranchCommitsLeftOutOfRefMoveCheck covers R1: a commit an implementer makes on its task branch is not a ref
// move, so the refs check does not blame it, while a move of an ordinary branch in the same window still is.
func TestTaskBranchCommitsLeftOutOfRefMoveCheck(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := buildRepo(t)
	base := buildHead(t, dir)
	path := filepath.Join(buildWorktreeRoot(t), "implement")
	if err := AddTaskWorktree(ctx, dir, testEpic, 1, path, base); err != nil {
		t.Fatalf("AddTaskWorktree: %v", err)
	}
	moves := buildMoves(t, dir, nil, func() {
		buildCommit(t, path)
		buildRun(t, dir, "update-ref", "refs/heads/feature", base)
	})
	assertMoves(t, moves, []RefMove{{Ref: "refs/heads/feature", From: "", To: base}})
}

// TestTaskBranchAcceptPinsTaskRef covers R1: accepting a task pins refs/orrery/epic/T1 at its revision, and the task
// branch its implementer committed on still names that revision after the task's worktrees are removed.
func TestTaskBranchAcceptPinsTaskRef(t *testing.T) {
	t.Parallel()
	f := newImplementerFixture(t)
	workspace := f.lastStart().Workspace
	commit := buildCommit(t, workspace)
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
	f.expectNoWorktrees(workspace)
	if got := buildRun(t, f.repo, "rev-parse", "refs/heads/orrery/epic/T1.1"); got != commit {
		t.Errorf("refs/heads/orrery/epic/T1.1 = %s after the worktree is removed, want %s", got, commit)
	}
}

// TestTaskBranchResumeCutsNextBranchAtNewBase covers R1: a resume of a waiting build onto a new base moves the worktree
// onto task 1's next branch, T1.2, whose first parent is the new base and whose history holds the commit of T1.1, and
// T1.1 still names its commit.
func TestTaskBranchResumeCutsNextBranchAtNewBase(t *testing.T) {
	t.Parallel()
	f := newImplementerFixture(t)
	identify(t, f.repo)
	workspace := f.lastStart().Workspace
	ladderCarryWork(t, workspace, "mine.go", "package mine\n")
	commit := buildHead(t, workspace)
	onto := commitOff(t, f.repo, f.base, "theirs.go", "package theirs\n")
	taskBranchResume(f, onto)
	requireNextBranch(t, f, workspace, commit, onto)
}

// TestTaskBranchCarryCutsNextBranchAtNewBase covers R1: a climb that carries the task's work onto the epic's candidate
// moves the worktree onto task 1's next branch, T1.2, whose first parent is the candidate and whose history holds the
// commit of T1.1, and T1.1 still names its commit.
func TestTaskBranchCarryCutsNextBranchAtNewBase(t *testing.T) {
	t.Parallel()
	f := newImplementerFixture(t)
	identify(t, f.repo)
	workspace := f.lastStart().Workspace
	ladderCarryWork(t, workspace, "mine.go", "package mine\n")
	commit := buildHead(t, workspace)
	onto := commitOff(t, f.repo, f.base, "theirs.go", "package theirs\n")
	ladderCarryCandidate(f, onto)
	ladderClimbExecute(f, Action{Kind: ActFail, Reason: "returned blocked: the gate is broken"})
	if task := f.task(); task.Build.Attempt != 2 || task.Build.Base != onto {
		t.Fatalf("build attempt %d base %s, want rung 2 on %s", task.Build.Attempt, task.Build.Base, onto)
	}
	requireNextBranch(t, f, workspace, commit, onto)
}

// TestTaskBranchNeverRebasedOrReset covers R1: across a first step, a send_back, a resume and a carry, no task branch
// is rebased, reset or deleted, so each commit a branch names stays reachable from it, and a branch a later resume or
// carry merged holds the commit it merged. A resume whose merge conflicts leaves the earlier branch and the worktree as
// they were, and fails with the reason git gave.
func TestTaskBranchNeverRebasedOrReset(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	plan := passPlan()
	plan.Actions = []store.EpicActionInput{{Kind: store.ActionSendBack, Task: 1, Reason: sendBackReason}}
	f := newPlanFixture(t, plan)
	identify(t, f.repo)
	f.pass()
	f.addRun(f.lastRun().id, evidence.PhaseRed, f.base, 1, evidence.VerdictFails)
	f.pass()
	first := f.lastStart().Workspace
	ladderCarryWork(t, first, "mine.go", "package mine\n")
	c1 := buildHead(t, first)
	f.carryPendingSendBack()
	before := len(f.steps.starts)
	f.pass()
	if got := len(f.steps.starts) - before; got != 1 {
		t.Fatalf("Start calls after the send_back = %d, want one new implement step", got)
	}
	reopened := f.steps.starts[before].Workspace
	requireTaskBranchAt(t, f.repo, 1, c1)

	ladderCarryWork(t, reopened, "second.go", "package second\n")
	c2 := buildHead(t, reopened)
	requireTaskBranchAt(t, f.repo, 2, c2)
	taskBranchResume(f, commitOff(t, f.repo, f.base, "theirs.go", "package theirs\n"))
	const third = "refs/heads/orrery/epic/T1.3"
	if got := buildRun(t, reopened, "rev-parse", "--symbolic-full-name", "HEAD"); got != third {
		t.Fatalf("reopened worktree HEAD is attached to %s after the resume, want %s", got, third)
	}
	if held, err := isAncestor(ctx, f.repo, c2, third); err != nil || !held {
		t.Errorf("%s does not hold %s after the resume: %v", third, c2, err)
	}
	requireTaskBranchAt(t, f.repo, 1, c1)
	requireTaskBranchAt(t, f.repo, 2, c2)

	ladderCarryWork(t, reopened, "third.go", "package third\n")
	c3 := buildHead(t, reopened)
	ladderCarryCandidate(f, commitOff(t, f.repo, f.base, "other.go", "package other\n"))
	ladderClimbExecute(f, Action{Kind: ActFail, Reason: "returned blocked: the gate is broken"})
	const fourth = "refs/heads/orrery/epic/T1.4"
	if got := buildRun(t, reopened, "rev-parse", "--symbolic-full-name", "HEAD"); got != fourth {
		t.Fatalf("reopened worktree HEAD is attached to %s after the carry, want %s", got, fourth)
	}
	if held, err := isAncestor(ctx, f.repo, c3, fourth); err != nil || !held {
		t.Errorf("%s does not hold %s after the carry: %v", fourth, c3, err)
	}
	requireTaskBranchAt(t, f.repo, 1, c1)
	requireTaskBranchAt(t, f.repo, 2, c2)
	requireTaskBranchAt(t, f.repo, 3, c3)

	t.Run("a conflicting resume leaves the earlier branch and the worktree as they were", func(t *testing.T) {
		t.Parallel()
		g := newImplementerFixture(t)
		identify(t, g.repo)
		workspace := g.lastStart().Workspace
		ladderCarryWork(t, workspace, "thing.go", "package thing // mine\n")
		commit := buildHead(t, workspace)
		onto := commitOff(t, g.repo, g.base, "thing.go", "package thing // theirs\n")
		ladderCarryCandidate(g, onto)
		before := buildRun(t, workspace, "status", "--porcelain")
		taskBranchResume(g, onto)

		const earlier = "refs/heads/orrery/epic/T1.1"
		if got := buildRun(t, workspace, "rev-parse", "--symbolic-full-name", "HEAD"); got != earlier {
			t.Errorf("worktree HEAD is attached to %s after the refused resume, want %s", got, earlier)
		}
		requireTaskBranchAt(t, g.repo, 1, commit)
		if got := buildRun(t, workspace, "status", "--porcelain"); got != before {
			t.Errorf("status %q after the refused resume, want %q as it was", got, before)
		}
		ladderCarryFiles(t, workspace, map[string]string{
			"thing.go": "package thing // mine\n", "draft.go": "package draft\n",
		})
		task := g.task()
		reason := "T1 could not move onto the new base " + onto + ":"
		if !strings.Contains(task.Build.Failure, reason) {
			t.Errorf("build failure %q, want it to start with %q", task.Build.Failure, reason)
		}
		if task.Build.Attempt != 2 {
			t.Errorf("build attempt %d, want the climb to rung 2", task.Build.Attempt)
		}
	})
}

// taskBranchAt returns the commit task 1's attempt k names in repo, and "" when the branch does not exist.
func taskBranchAt(t *testing.T, repo string, k int64) string {
	t.Helper()
	return buildRun(t, repo, "for-each-ref", "--format=%(objectname)", TaskBranchRef(passEpic, 1, k))
}

// requireTaskBranchAt fails the test unless task 1's attempt k names commit in repo.
func requireTaskBranchAt(t *testing.T, repo string, k int64, commit string) {
	t.Helper()
	if got := taskBranchAt(t, repo, k); got != commit {
		t.Errorf("%s = %s, want %s", TaskBranchRef(passEpic, 1, k), got, commit)
	}
}

// taskBranchResume holds task 1 of f waiting, then resumes it onto base through decideWait, as a waiting build is once
// its base is settled. It fails the test unless the resume is decided and carried out.
func taskBranchResume(f *passFixture, base string) {
	f.t.Helper()
	next := f.task().Build
	next.Held = HeldWaiting
	f.setBuild(next)
	view := View{Epic: passEpic, Task: f.task(), NewBase: base}
	action := decideWait(view)
	if action.Kind != ActResume {
		f.t.Fatalf("decideWait = %s, want %s", action.Kind, ActResume)
	}
	if !f.runner.execute(f.ctx, f.epic(), view, action) {
		f.t.Fatal("execute = false, want the resume carried out")
	}
}

// requireNextBranch fails the test unless the worktree at workspace is attached to task 1's second branch, whose first
// parent is onto and whose history holds commit, while task 1's first branch still names commit.
func requireNextBranch(t *testing.T, f *passFixture, workspace, commit, onto string) {
	t.Helper()
	const first, second = "refs/heads/orrery/epic/T1.1", "refs/heads/orrery/epic/T1.2"
	if got := buildRun(t, workspace, "rev-parse", "--symbolic-full-name", "HEAD"); got != second {
		t.Errorf("worktree HEAD is attached to %s, want %s", got, second)
	}
	if parent := buildRun(t, f.repo, "rev-parse", second+"^1"); parent != onto {
		t.Errorf("%s^1 = %s, want the new base %s", second, parent, onto)
	}
	if held, err := isAncestor(f.ctx, f.repo, commit, second); err != nil || !held {
		t.Errorf("%s does not hold %s: %v", second, commit, err)
	}
	if got := buildRun(t, f.repo, "rev-parse", first); got != commit {
		t.Errorf("%s = %s, want the commit the first attempt made %s", first, got, commit)
	}
	ladderCarryFiles(t, workspace, map[string]string{"mine.go": "package mine\n", "draft.go": "package draft\n"})
}
