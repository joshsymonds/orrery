package build

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// ladderWaitMove is one MoveWorktree call: the worktree it moved, the wip ref it kept the work on, and the base it moved
// the worktree to.
type ladderWaitMove struct {
	path, wip, to string
}

// ladderWaitGit is the Git the waiting build and the climb run through: the package's own writes, and a record of each
// move before it is made.
type ladderWaitGit struct {
	packageGit
	moves []ladderWaitMove
}

func (g *ladderWaitGit) MoveWorktree(ctx context.Context, path, wip, to string) error {
	g.moves = append(g.moves, ladderWaitMove{path: path, wip: wip, to: to})
	return g.packageGit.MoveWorktree(ctx, path, wip, to)
}

// ladderWaitCase is a task whose worktree was moved onto base, with the fixture it is in, the worktree, and the Git that
// recorded the move.
type ladderWaitCase struct {
	f        *passFixture
	worktree string
	base     string
	git      *ladderWaitGit
}

// ladderWaitSetup returns a fixture whose task 1 is at its base, with a commit and an uncommitted file in its worktree,
// and whose epic's base is a commit ahead of the task's base, set by setBase. It returns the fixture, the worktree and
// that commit.
func ladderWaitSetup(t *testing.T, setBase func(*passFixture, string)) (*passFixture, string, string) {
	t.Helper()
	f, worktree := ladderCarryFixture(t, StageImplement, 1)
	ladderCarryWork(t, worktree, "mine.go", "package mine\n")
	ahead := commitOff(t, f.repo, f.base, "other.go", "package other\n")
	setBase(f, ahead)
	return f, worktree, ahead
}

// ladderWaitResumed holds task 1 waiting, then resumes it through decideWait on the base a waiting build reads: the
// epic's candidate when it has one, and its stored base otherwise. The resume is carried out through a recording Git.
func ladderWaitResumed(t *testing.T, setBase func(*passFixture, string)) ladderWaitCase {
	t.Helper()
	f, worktree, ahead := ladderWaitSetup(t, setBase)
	f.setBuild(store.TaskBuild{Stage: StageImplement, Base: f.base, Attempt: 1, Revision: f.base, Held: HeldWaiting})
	epic := f.epic()
	newBase := epic.StoredBase
	if epic.Candidate != "" {
		newBase = epic.Candidate
	}
	view := View{Epic: passEpic, Task: f.task(), NewBase: newBase}
	action := decideWait(view)
	if action.Kind != ActResume {
		t.Fatalf("decideWait = %s, want %s", action.Kind, ActResume)
	}
	git := &ladderWaitGit{}
	if !f.gitRunner(git).execute(f.ctx, f.epic(), view, action) {
		t.Fatal("execute = false, want the resume written")
	}
	return ladderWaitCase{f: f, worktree: worktree, base: ahead, git: git}
}

// ladderWaitClimbed climbs task 1 off its rung with a failure, through a recording Git, so its carry moves the worktree
// onto the epic's base.
func ladderWaitClimbed(t *testing.T, setBase func(*passFixture, string)) ladderWaitCase {
	t.Helper()
	f, worktree, ahead := ladderWaitSetup(t, setBase)
	git := &ladderWaitGit{}
	action := Action{Kind: ActFail, Reason: "returned blocked: the gate is broken"}
	if !f.gitRunner(git).execute(f.ctx, f.epic(), View{Epic: passEpic, Task: f.task()}, action) {
		t.Fatal("execute = false, want the climb written")
	}
	return ladderWaitCase{f: f, worktree: worktree, base: ahead, git: git}
}

// ladderWaitCheck fails the test unless c's task 1 moved its worktree once, onto c's base with the task's wip ref, and
// its build stands on attempt on that base with no hold. The worktree keeps the task's commit on the base with its
// uncommitted file, and the task has no version.
func ladderWaitCheck(t *testing.T, c ladderWaitCase, attempt int64) {
	t.Helper()
	wip := WipRef(passEpic, 1)
	if len(c.git.moves) != 1 {
		t.Fatalf("moves = %+v, want one", c.git.moves)
	}
	if move := c.git.moves[0]; move.path != c.worktree || move.wip != wip || move.to != c.base {
		t.Errorf("move %+v, want path %s wip %s to %s", move, c.worktree, wip, c.base)
	}
	task := c.f.task()
	if task.Build.Attempt != attempt || task.Build.Held != "" || task.Build.Base != c.base {
		t.Errorf("build attempt %d held %q base %s, want attempt %d with no hold on %s",
			task.Build.Attempt, task.Build.Held, task.Build.Base, attempt, c.base)
	}
	if parent := buildRun(t, c.worktree, "rev-parse", "HEAD^"); parent != c.base {
		t.Errorf("HEAD^ %s, want %s", parent, c.base)
	}
	if subject := buildRun(t, c.worktree, "log", "-1", "--format=%s"); subject != "mine" {
		t.Errorf("HEAD subject %q, want mine", subject)
	}
	ladderCarryFiles(t, c.worktree, map[string]string{"mine.go": "package mine\n", "draft.go": "package draft\n"})
	if n := len(task.Versions); n != 0 {
		t.Errorf("task has %d versions, want none", n)
	}
}

// TestTaskLadderCarryMatchesWaitingBuild: a climb's carry moves a task's worktree onto the epic's base the way a waiting
// build resumes onto the same base, with one MoveWorktree of the same worktree and wip ref onto that base. The base is
// the candidate, or the stored base when the epic has no candidate.
func TestTaskLadderCarryMatchesWaitingBuild(t *testing.T) {
	t.Parallel()
	t.Run("candidate", func(t *testing.T) {
		t.Parallel()
		ladderWaitCompare(t, ladderCarryCandidate)
	})
	t.Run("stored base", func(t *testing.T) {
		t.Parallel()
		ladderWaitCompare(t, ladderCarryStored)
	})
}

// ladderWaitCompare checks that a waiting build resumed, and that a climb carried, onto the same base that setBase sets
// as the epic's base. The resumed build is at attempt 1 and the climbed one at attempt 2.
func ladderWaitCompare(t *testing.T, setBase func(*passFixture, string)) {
	t.Helper()
	ladderWaitCheck(t, ladderWaitResumed(t, setBase), 1)
	ladderWaitCheck(t, ladderWaitClimbed(t, setBase), 2)
}

// TestTaskLadderCarryRefUnderRefsOrrery: the ref a refused carry keeps a task's work on lies under refs/orrery/, and the
// carry version names it. The ref is listed among the refs under refs/orrery/ once the carry is refused.
func TestTaskLadderCarryRefUnderRefsOrrery(t *testing.T) {
	t.Parallel()
	ref := CarryRef(passEpic, 1, 2)
	if !strings.HasPrefix(ref, "refs/orrery/") {
		t.Errorf("CarryRef = %s, want it under refs/orrery/", ref)
	}
	if want := "refs/orrery/" + Slug(passEpic) + "/carry/T1-2"; ref != want {
		t.Errorf("CarryRef = %s, want %s", ref, want)
	}

	f, worktree := ladderCarryFixture(t, StageImplement, 1)
	ladderCarryWork(t, worktree, "thing.go", "package thing // mine\n")
	ladderCarryCandidate(f, commitOff(t, f.repo, f.base, "thing.go", "package thing // theirs\n"))
	ladderClimbExecute(f, Action{Kind: ActFail, Reason: "returned blocked: the gate is broken"})

	refs := strings.Split(buildRun(t, f.repo, "for-each-ref", "--format=%(refname)", "refs/orrery/"), "\n")
	if !slices.Contains(refs, ref) {
		t.Errorf("refs under refs/orrery/ = %q, want %s among them", refs, ref)
	}
	task := f.task()
	if n := len(task.Versions); n != 1 {
		t.Fatalf("task has %d versions, want one carry version", n)
	}
	version := task.Versions[0]
	if version.Source != store.SourceCarry {
		t.Errorf("version source %q, want %q", version.Source, store.SourceCarry)
	}
	if !strings.Contains(version.Reason, ref) {
		t.Errorf("version reason %q, want it to name %s", version.Reason, ref)
	}
}
