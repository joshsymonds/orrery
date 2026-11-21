package build

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// ladderCarryFixture returns a pass fixture whose task 1 is at stage on attempt, at its base, and the path of its
// implement worktree, added on task branch T1.1 at the base. The repository has an identity, so a carry's merge
// commits are made.
func ladderCarryFixture(t *testing.T, stage string, attempt int64) (*passFixture, string) {
	t.Helper()
	f := newPassFixture(t)
	identify(t, f.repo)
	f.setBuild(store.TaskBuild{Stage: stage, Base: f.base, Attempt: attempt, Revision: f.base})
	worktree := ImplementWorktree(f.stateDir, passEpic, f.task())
	if err := AddTaskWorktree(f.ctx, f.repo, passEpic, 1, worktree, f.base); err != nil {
		f.t.Fatalf("AddTaskWorktree: %v", err)
	}
	return f, worktree
}

// ladderCarryMerged fails the test unless the worktree at path is on task 1's branch at attempt k, whose commit merges
// the work into onto: its first parent is onto, and its second parent is the work, which holds the worktree's commit
// of mine.go. The files hold the work, and nothing is uncommitted.
func ladderCarryMerged(t *testing.T, path string, k int64, onto string) {
	t.Helper()
	branch := TaskBranchRef(passEpic, 1, k)
	if got := buildRun(t, path, "rev-parse", "--symbolic-full-name", "HEAD"); got != branch {
		t.Errorf("worktree HEAD is attached to %s, want %s", got, branch)
	}
	if parent := buildRun(t, path, "rev-parse", "HEAD^1"); parent != onto {
		t.Errorf("HEAD^1 %s, want %s", parent, onto)
	}
	if subject := buildRun(t, path, "log", "-1", "--format=%s", "HEAD^2^"); subject != "mine" {
		t.Errorf("HEAD^2^ subject %q, want mine", subject)
	}
	ladderCarryFiles(t, path, map[string]string{"mine.go": "package mine\n", "draft.go": "package draft\n"})
	if status := buildRun(t, path, "status", "--porcelain"); status != "" {
		t.Errorf("status %q, want the work committed", status)
	}
}

// ladderCarryWork leaves worktree with a commit of file, written as content, and an uncommitted draft.go: the work a
// task keeps across a climb.
func ladderCarryWork(t *testing.T, worktree, file, content string) {
	t.Helper()
	writeFile(t, filepath.Join(worktree, file), content)
	buildRun(t, worktree, "add", file)
	buildRun(t, worktree, "commit", "-q", "-m", "mine")
	writeFile(t, filepath.Join(worktree, "draft.go"), "package draft\n")
}

// ladderCarryFiles fails the test unless each file of dir holds the content want names for it.
func ladderCarryFiles(t *testing.T, dir string, want map[string]string) {
	t.Helper()
	for name, content := range want {
		if data, err := os.ReadFile(filepath.Join(dir, name)); err != nil || string(data) != content {
			t.Errorf("%s = %q, %v, want %q", name, data, err, content)
		}
	}
}

// ladderCarryBatch runs an integrate batch over commit, which composed task 1, and ends it with outcome. A failed batch
// ends with commit as the candidate, checked against the stored base, as the interpreter ends a batch composed on it.
// No task is claimed, so task 1 stays open.
func ladderCarryBatch(f *passFixture, commit, outcome, output string) {
	f.t.Helper()
	const path = "plan/S1/integrate"
	if err := f.store.StartNodes(f.ctx, passEpic, []store.NodeRun{{
		Path: path, ID: "run-batch", Node: "integrate", Kind: "integrate",
	}}, nil, passClock()); err != nil {
		f.t.Fatalf("StartNodes: %v", err)
	}
	runs, err := f.store.NodeRuns(f.ctx, passEpic)
	if err != nil {
		f.t.Fatalf("NodeRuns: %v", err)
	}
	i := slices.IndexFunc(runs, func(run store.NodeRun) bool { return run.Path == path })
	if i < 0 {
		f.t.Fatalf("no integrate run at %s", path)
	}
	result := `{"base":"","candidate":"` + commit + `","composed":[{"task":1,"commit":"` + commit + `"}],"leftOut":[]}`
	if err = f.store.ClaimBatch(f.ctx, passEpic, path, runs[i].Attempt, commit, "", result, nil); err != nil {
		f.t.Fatalf("ClaimBatch: %v", err)
	}
	end := store.BatchEnd{Path: path, Attempt: runs[i].Attempt, Outcome: outcome, Output: output, Over: commit}
	if outcome == "failed" {
		end.Candidate, end.Over = commit, f.epic().StoredBase
	}
	if err = f.store.EndBatch(f.ctx, passEpic, end, nil, passClock()); err != nil {
		f.t.Fatalf("EndBatch: %v", err)
	}
}

// ladderCarryCandidate sets the epic's candidate to commit, as a failed integrate batch that composed commit does,
// since its revision is not the stored base.
func ladderCarryCandidate(f *passFixture, commit string) {
	f.t.Helper()
	ladderCarryBatch(f, commit, "failed", "red")
	if got := f.epic().Candidate; got != commit {
		f.t.Fatalf("candidate = %q, want %q", got, commit)
	}
}

// ladderCarryStored sets the epic's stored base to commit, as a passed integrate batch that composed commit does. The
// epic has no candidate after it, so a climb's base is the stored base.
func ladderCarryStored(f *passFixture, commit string) {
	f.t.Helper()
	ladderCarryBatch(f, commit, "passed", "")
	if epic := f.epic(); epic.StoredBase != commit || epic.Candidate != "" {
		f.t.Fatalf("stored base %q and candidate %q, want %q and none", epic.StoredBase, epic.Candidate, commit)
	}
}

// ladderCarryGit is the Git a carry test runs through: the package's own writes, and a record of the base each move
// named.
type ladderCarryGit struct {
	packageGit
	moves []string
}

func (g *ladderCarryGit) MoveTaskWorktree(ctx context.Context, path, wip, epic string, seq int64, to string) error {
	g.moves = append(g.moves, to)
	return g.packageGit.MoveTaskWorktree(ctx, path, wip, epic, seq, to)
}

// TestTaskLadderCarryKeepWork: KeepWork keeps a worktree's commit and its uncommitted file on the ref, leaves the
// worktree on the commit it was on with the file in place and no wip ref, and returns "" for a path that does not
// exist.
func TestTaskLadderCarryKeepWork(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newMoveWorld(t)
	ladderCarryWork(t, w.path, "mine.go", "package mine\n")
	head := buildHead(t, w.path)
	ref := CarryRef(passEpic, 1, 2)

	commit, err := KeepWork(ctx, w.path, moveWip, ref)
	if err != nil {
		t.Fatalf("KeepWork: %v", err)
	}
	if got := buildRun(t, w.repo, "rev-parse", ref); got != commit {
		t.Errorf("%s at %s, want the commit KeepWork returned, %s", ref, got, commit)
	}
	for name, want := range map[string]string{"mine.go": "package mine", "draft.go": "package draft"} {
		if got := buildRun(t, w.repo, "show", commit+":"+name); got != want {
			t.Errorf("%s in the kept commit = %q, want %q", name, got, want)
		}
	}
	if got := buildHead(t, w.path); got != head {
		t.Errorf("HEAD %s after KeepWork, want %s where it was", got, head)
	}
	ladderCarryFiles(t, w.path, map[string]string{"draft.go": "package draft\n"})
	if got := buildRun(t, w.repo, "for-each-ref", moveWip); got != "" {
		t.Errorf("%s = %q after KeepWork, want it deleted", moveWip, got)
	}
	missing, err := KeepWork(ctx, filepath.Join(t.TempDir(), "missing"), moveWip, ref)
	if missing != "" || err != nil {
		t.Errorf("KeepWork of a missing path = %q, %v, want \"\", nil", missing, err)
	}
}

// TestTaskLadderCarryMovesWorkOntoBranch: a climb whose epic's base is ahead of the task's base merges the task's work
// onto that base on the task's next branch, and stores no carry version. The base is the candidate, or the stored base
// when the epic has no candidate.
func TestTaskLadderCarryMovesWorkOntoBranch(t *testing.T) {
	t.Parallel()
	t.Run("candidate", func(t *testing.T) {
		t.Parallel()
		ladderCarryMovesOnto(t, ladderCarryCandidate)
	})
	t.Run("stored base", func(t *testing.T) {
		t.Parallel()
		ladderCarryMovesOnto(t, ladderCarryStored)
	})
}

// ladderCarryMovesOnto climbs a task with a commit and an uncommitted file, after setBase sets the epic's base to a
// commit ahead of the task's base, and checks the climb moved the work onto that commit with no carry version.
func ladderCarryMovesOnto(t *testing.T, setBase func(*passFixture, string)) {
	t.Helper()
	f, worktree := ladderCarryFixture(t, StageImplement, 1)
	ladderCarryWork(t, worktree, "mine.go", "package mine\n")
	ahead := commitOff(t, f.repo, f.base, "other.go", "package other\n")
	setBase(f, ahead)
	ladderClimbExecute(f, Action{Kind: ActFail, Reason: "returned blocked: the gate is broken"})

	task := f.task()
	if task.Build.Attempt != 2 || task.Build.Base != ahead {
		t.Errorf("build attempt %d base %s, want rung 2 on %s", task.Build.Attempt, task.Build.Base, ahead)
	}
	ladderCarryMerged(t, worktree, 2, ahead)
	if n := len(task.Versions); n != 0 {
		t.Errorf("task has %d versions, want no carry version", n)
	}
}

// TestTaskLadderCarryRefusedKeepsWorkOnRef: a candidate that changes the lines the task's commit changes refuses the
// move, so the climb keeps the task's base. The work stays in the worktree on that base, is kept on CarryRef, and the
// task has an applied carry version that names the ref.
func TestTaskLadderCarryRefusedKeepsWorkOnRef(t *testing.T) {
	t.Parallel()
	f, worktree := ladderCarryFixture(t, StageImplement, 1)
	ladderCarryWork(t, worktree, "thing.go", "package thing // mine\n")
	ladderCarryCandidate(f, commitOff(t, f.repo, f.base, "thing.go", "package thing // theirs\n"))
	deliverable := f.task().Deliverable
	ladderClimbExecute(f, Action{Kind: ActFail, Reason: "returned blocked: the gate is broken"})
	ref := CarryRef(passEpic, 1, 2)

	task := f.task()
	if task.Build.Attempt != 2 || task.Build.Base != f.base {
		t.Errorf("build attempt %d base %s, want rung 2 on the base %s", task.Build.Attempt, task.Build.Base, f.base)
	}
	kept := buildRun(t, f.repo, "rev-parse", ref)
	for name, want := range map[string]string{"thing.go": "package thing // mine", "draft.go": "package draft"} {
		if got := buildRun(t, f.repo, "show", kept+":"+name); got != want {
			t.Errorf("%s in %s = %q, want %q", name, ref, got, want)
		}
	}
	if parent := buildRun(t, worktree, "rev-parse", "HEAD^"); parent != f.base {
		t.Errorf("HEAD^ %s, want the base %s the work stays on", parent, f.base)
	}
	ladderCarryFiles(t, worktree, map[string]string{
		"thing.go": "package thing // mine\n", "draft.go": "package draft\n",
	})
	if got := buildRun(t, f.repo, "for-each-ref", WipRef(passEpic, 1)); got != "" {
		t.Errorf("%s = %q after the climb, want it deleted", WipRef(passEpic, 1), got)
	}
	if n := len(task.Versions); n != 1 {
		t.Fatalf("task has %d versions, want one carry version", n)
	}
	version := task.Versions[0]
	if version.Source != store.SourceCarry || version.State != store.VersionApplied {
		t.Errorf("version source %q state %q, want carry applied", version.Source, version.State)
	}
	if !strings.Contains(version.Reason, ref) {
		t.Errorf("version reason %q, want it to name %s", version.Reason, ref)
	}
	if want := deliverable + "\n\n" + version.Reason; version.Deliverable == nil || *version.Deliverable != want {
		t.Errorf("version deliverable = %v, want the deliverable and the reason", version.Deliverable)
	}
}

// TestTaskLadderCarrySkips: a climb whose epic has no candidate and a stored base at the task's base moves no worktree,
// and a climb onto the gap rung carries nothing, even when a refusing candidate would otherwise be carried onto.
func TestTaskLadderCarrySkips(t *testing.T) {
	t.Parallel()
	t.Run("a stored base at the task's base moves nothing", func(t *testing.T) {
		t.Parallel()
		f, worktree := ladderCarryFixture(t, StageImplement, 1)
		ladderCarryWork(t, worktree, "mine.go", "package mine\n")
		git := &ladderCarryGit{}
		if !f.gitRunner(git).execute(f.ctx, f.epic(), View{Epic: passEpic, Task: f.task()},
			Action{Kind: ActFail, Reason: "returned blocked: the gate is broken"}) {
			t.Fatal("execute = false, want the climb written")
		}
		if len(git.moves) != 0 {
			t.Errorf("moves %v, want none", git.moves)
		}
		task := f.task()
		if task.Build.Attempt != 2 || task.Build.Base != f.base || len(task.Versions) != 0 {
			t.Errorf("build attempt %d base %s with %d versions, want rung 2 on %s with none",
				task.Build.Attempt, task.Build.Base, len(task.Versions), f.base)
		}
	})
	t.Run("the gap rung moves nothing, stores no carry version and removes the worktrees", func(t *testing.T) {
		t.Parallel()
		f, worktree := ladderCarryFixture(t, StageGreen, store.LastRung)
		ladderCarryWork(t, worktree, "thing.go", "package thing // mine\n")
		ladderCarryCandidate(f, commitOff(t, f.repo, f.base, "thing.go", "package thing // theirs\n"))
		git := &ladderCarryGit{}
		if !f.gitRunner(git).execute(f.ctx, f.epic(), View{Epic: passEpic, Task: f.task()},
			Action{Kind: ActFail, Reason: "green not met: exit 1, verdict fails"}) {
			t.Fatal("execute = false, want the climb written")
		}
		if len(git.moves) != 0 {
			t.Errorf("moves %v, want none", git.moves)
		}
		task := f.task()
		if task.Build.Attempt != store.GapRung || task.Status != store.TaskGap || len(task.Versions) != 0 {
			t.Errorf("status %q attempt %d with %d versions, want a gap on rung %d with none",
				task.Status, task.Build.Attempt, len(task.Versions), store.GapRung)
		}
		f.expectNoWorktrees(worktree)
	})
}
