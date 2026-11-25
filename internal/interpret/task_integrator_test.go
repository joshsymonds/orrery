package interpret

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/hostexec"
	"github.com/joshsymonds/orrery/internal/store"
)

// taskClash is a scratch repository for a task merge. Its candidate is main with task T1 composed onto it, which
// changes README, and then task T3 composed onto it, which adds notes. Task T2's revision, branched from main, changes
// README and adds notes too, so merging it into the candidate conflicts in both. dir is the directory the merge's
// worktree goes under.
type taskClash struct {
	repo, candidate, revision, dir string
}

// newTaskClash makes the scratch repository taskClash describes.
func newTaskClash(t *testing.T) taskClash {
	t.Helper()
	repo := mergeRepo(t)
	t1 := taskBranch(t, repo, "t1", map[string]string{"README": "one\n"})
	nodeGit(t, repo, "checkout", "-q", "-b", "candidate", "main")
	nodeGit(t, repo, "merge", "-q", "--no-ff", "-m", taskMergeMessage(1, t1), t1)
	t3 := taskBranch(t, repo, "t3", map[string]string{"notes": "three\n"})
	nodeGit(t, repo, "checkout", "-q", "candidate")
	nodeGit(t, repo, "merge", "-q", "--no-ff", "-m", taskMergeMessage(3, t3), t3)
	candidate := nodeGit(t, repo, "rev-parse", "HEAD")
	revision := taskBranch(t, repo, "t2", map[string]string{"README": "two\n", "notes": "two\n"})
	return taskClash{repo: repo, candidate: candidate, revision: revision, dir: t.TempDir()}
}

// taskBranch commits each file of files with its text on a branch named branch made from main, and leaves main checked
// out. It returns the branch's head.
func taskBranch(t *testing.T, repo, branch string, files map[string]string) string {
	t.Helper()
	nodeGit(t, repo, "checkout", "-q", "-b", branch, "main")
	for _, file := range slices.Sorted(maps.Keys(files)) {
		nodeWrite(t, repo, file, files[file])
		nodeGit(t, repo, "add", file)
	}
	nodeGit(t, repo, "commit", "-q", "-m", branch+" work")
	head := nodeGit(t, repo, "rev-parse", "HEAD")
	nodeGit(t, repo, "checkout", "-q", "main")
	return head
}

// start makes the worktree of c's task merge under its directory, and returns the merge with the conflicts it left.
func (c taskClash) start(t *testing.T) integratorMerge {
	t.Helper()
	merge := taskMerge(c.repo, c.dir, c.candidate, c.revision)
	conflicts, err := startTaskMerge(t.Context(), hostexec.NewLocal(), merge, c.dir)
	if err != nil {
		t.Fatalf("start task merge: %v", err)
	}
	merge.conflicts = conflicts
	return merge
}

// resolveTaskMerge writes README in wt as readme, and notes as both sides' lines, and stages both.
func resolveTaskMerge(t *testing.T, wt, readme string) {
	t.Helper()
	nodeWrite(t, wt, "README", readme)
	nodeWrite(t, wt, "notes", "both\n")
	nodeGit(t, wt, "add", "README", "notes")
}

// taskMergeCommit commits the staged merge in wt with the message of a task's merge of revision, and returns its id.
func taskMergeCommit(t *testing.T, wt, revision string) string {
	t.Helper()
	nodeGit(t, wt, "commit", "-q", "-m", taskMergeMessage(2, revision))
	return nodeGit(t, wt, "rev-parse", "HEAD")
}

// taskReturn returns the return of a task integrator with verdict, commit and explanation, and the changes outside the
// conflicts it lists, each as its path and reason.
func taskReturn(verdict, commit, explanation string, changes ...map[string]string) map[string]any {
	if changes == nil {
		changes = []map[string]string{}
	}
	return map[string]any{
		"status": "done", "verdict": verdict, "commit": commit, "explanation": explanation, "other_changes": changes,
	}
}

// taskCheckOf returns the return check of the task integrator round that finishes merge.
func taskCheckOf(t *testing.T, merge integratorMerge) func(json.RawMessage) []string {
	t.Helper()
	check, err := taskIntegratorCheck(t.Context(), hostexec.NewLocal(), merge)
	if err != nil {
		t.Fatalf("return check: %v", err)
	}
	return check
}

// failuresOfReturn runs check on ret, encoded as the step stores it, and returns its failure lines.
func failuresOfReturn(t *testing.T, check func(json.RawMessage) []string, ret map[string]any) []string {
	t.Helper()
	encoded, err := json.Marshal(ret)
	if err != nil {
		t.Fatalf("encode return: %v", err)
	}
	return check(encoded)
}

// TestConflictIntegratorWorktreeHoldsConflicts: the task merge's worktree is detached at the candidate with the task's
// revision as MERGE_HEAD, so the merge is in progress and nothing is committed. README and notes stay conflicted, with
// their conflict markers, and the start returns those two paths.
func TestConflictIntegratorWorktreeHoldsConflicts(t *testing.T) {
	t.Parallel()
	c := newTaskClash(t)
	merge := c.start(t)
	if !slices.Equal(merge.conflicts, []string{"README", "notes"}) {
		t.Errorf("conflicted paths = %q, want README and notes", merge.conflicts)
	}
	wt := merge.worktree
	if head := nodeGit(t, wt, "rev-parse", "HEAD"); head != c.candidate {
		t.Errorf("worktree HEAD = %s, want the candidate %s", head, c.candidate)
	}
	if ref := nodeGit(t, wt, "rev-parse", "--symbolic-full-name", "HEAD"); ref != "HEAD" {
		t.Errorf("worktree HEAD names ref %q, want it detached", ref)
	}
	if mergeHead := nodeGit(t, wt, "rev-parse", "MERGE_HEAD"); mergeHead != c.revision {
		t.Errorf("MERGE_HEAD = %s, want the task's revision %s", mergeHead, c.revision)
	}
	readme, err := os.ReadFile(filepath.Join(wt, "README"))
	if err != nil {
		t.Fatalf("read README: %v", err)
	}
	if !strings.Contains(string(readme), "<<<<<<< ") || !strings.Contains(string(readme), ">>>>>>> ") {
		t.Errorf("README = %q, want its conflict markers", readme)
	}
}

// TestConflictIntegratorBriefCarriesBothSides: the prompt opens with the role text, then the task being merged, the
// merge with its message, each conflicted path with the tasks the candidate's side composed that changed it, the
// migration collisions, None. for a task with none, and the failure of the round before. README names only T1, which
// changed it, and notes only T3.
func TestConflictIntegratorBriefCarriesBothSides(t *testing.T) {
	t.Parallel()
	c := newTaskClash(t)
	merge := c.start(t)
	tasks := []store.EpicTask{
		{Seq: 1, Title: "Title one", Deliverable: "Deliverable one"},
		{Seq: 2, Title: "Title two", Deliverable: "Deliverable two"},
		{Seq: 3, Title: "Title three", Deliverable: "Deliverable three"},
	}
	prompt, err := taskIntegratorPrompt(t.Context(), hostexec.NewLocal(), merge, tasks, tasks[1], nil, "")
	if err != nil {
		t.Fatalf("task integrator prompt: %v", err)
	}
	if !strings.HasPrefix(prompt, "You are the task integrator for one epic.") {
		t.Errorf("prompt starts %q, want the role text", prompt[:min(len(prompt), 80)])
	}
	composed1 := nodeGit(t, c.repo, "rev-parse", c.candidate+"^")
	for _, want := range []string{
		"\n## Task\n- T2 Title two\n  deliverable: Deliverable two\n",
		"\n## Merge\nWorktree: " + merge.worktree + "\n",
		"\nCandidate: " + c.candidate + ", ",
		"\nTask's revision: " + c.revision + ", ",
		"\nCommit message: Merge T2 " + c.revision + "\n",
		"\n## Conflicted paths\n" +
			"- README\n  candidate T1 Title one, merged as " + composed1 + "\n    deliverable: Deliverable one\n" +
			"- notes\n  candidate T3 Title three, merged as " + c.candidate + "\n    deliverable: Deliverable three\n",
		"\n## Migration collisions\nNone.\n",
		"\n## Previous attempt\nNone.\n",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt lacks %q:\n%s", want, prompt)
		}
	}

	again, err := taskIntegratorPrompt(
		t.Context(), hostexec.NewLocal(), merge, tasks, tasks[1], nil, "markers: README\n",
	)
	if err != nil {
		t.Fatalf("task integrator prompt after a failure: %v", err)
	}
	if !strings.HasSuffix(again, "\n## Previous attempt\nmarkers: README\n") {
		t.Errorf("prompt ends %q, want the failure of the round before", again[max(0, len(again)-80):])
	}
}

// TestConflictIntegratorCheckRefuses: each return the task integrator check refuses, with the line that names why.
func TestConflictIntegratorCheckRefuses(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, want string
		ret        func(t *testing.T, c taskClash, wt string) map[string]any
	}{
		{
			name: "conflicted path unresolved",
			want: "unmerged: ",
			ret: func(_ *testing.T, c taskClash, _ string) map[string]any {
				return taskReturn(verdictMerged, c.candidate, "")
			},
		},
		{
			name: "change outside the conflicts not listed",
			want: "other_changes: ",
			ret: func(t *testing.T, c taskClash, wt string) map[string]any {
				t.Helper()
				resolveTaskMerge(t, wt, "both\n")
				nodeWrite(t, wt, "extra.txt", "extra\n")
				nodeGit(t, wt, "add", "extra.txt")
				return taskReturn(verdictMerged, taskMergeCommit(t, wt, c.revision), "")
			},
		},
		{
			name: "conflict marker left",
			want: "markers: ",
			ret: func(t *testing.T, c taskClash, wt string) map[string]any {
				t.Helper()
				resolveTaskMerge(t, wt, "<<<<<<< candidate\none\n=======\ntwo\n>>>>>>> task\n")
				return taskReturn(verdictMerged, taskMergeCommit(t, wt, c.revision), "")
			},
		},
		{
			name: "parents not the candidate then the revision",
			want: "parents: ",
			ret: func(t *testing.T, c taskClash, wt string) map[string]any {
				t.Helper()
				resolveTaskMerge(t, wt, "both\n")
				tree := nodeGit(t, wt, "write-tree")
				commit := nodeGit(t, wt, "commit-tree", tree, "-p", c.revision, "-p", c.candidate, "-m", "reversed")
				return taskReturn(verdictMerged, commit, "")
			},
		},
		{
			name: "clash without an explanation",
			want: "explanation: ",
			ret: func(_ *testing.T, _ taskClash, _ string) map[string]any {
				return taskReturn(verdictClash, "", "")
			},
		},
		{
			name: "clash naming a commit",
			want: "commit: ",
			ret: func(_ *testing.T, c taskClash, _ string) map[string]any {
				return taskReturn(verdictClash, c.candidate, "both sides decide the title")
			},
		},
		{
			name: "verdict neither merged nor clash",
			want: "verdict: ",
			ret: func(_ *testing.T, _ taskClash, _ string) map[string]any {
				return taskReturn("pending", "", "")
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := newTaskClash(t)
			merge := c.start(t)
			check := taskCheckOf(t, merge)
			failures := failuresOfReturn(t, check, tc.ret(t, c, merge.worktree))
			if !hasLine(failures, tc.want) {
				t.Errorf("failures = %q, want a line starting %q", failures, tc.want)
			}
		})
	}
}

// TestConflictIntegratorCheckAccepts: a merged return whose commit resolves both conflicts and changes nothing else, a
// merged return that lists its one change outside the conflicts, and a clash return with an explanation and no commit
// all pass the check.
func TestConflictIntegratorCheckAccepts(t *testing.T) {
	t.Parallel()
	t.Run("merged with nothing changed outside the conflicts", func(t *testing.T) {
		t.Parallel()
		c := newTaskClash(t)
		merge := c.start(t)
		check := taskCheckOf(t, merge)
		resolveTaskMerge(t, merge.worktree, "both\n")
		commit := taskMergeCommit(t, merge.worktree, c.revision)
		if failures := failuresOfReturn(t, check, taskReturn(verdictMerged, commit, "")); len(failures) != 0 {
			t.Errorf("failures = %q, want none", failures)
		}
	})
	t.Run("merged with a change outside the conflicts that other_changes lists", func(t *testing.T) {
		t.Parallel()
		c := newTaskClash(t)
		merge := c.start(t)
		check := taskCheckOf(t, merge)
		resolveTaskMerge(t, merge.worktree, "both\n")
		nodeWrite(t, merge.worktree, "extra.txt", "extra\n")
		nodeGit(t, merge.worktree, "add", "extra.txt")
		commit := taskMergeCommit(t, merge.worktree, c.revision)
		listed := map[string]string{"path": "extra.txt", "reason": "the task's test reads it"}
		if failures := failuresOfReturn(t, check, taskReturn(verdictMerged, commit, "", listed)); len(failures) != 0 {
			t.Errorf("failures = %q, want none", failures)
		}
	})
	t.Run("clash with an explanation and no commit", func(t *testing.T) {
		t.Parallel()
		c := newTaskClash(t)
		check := taskCheckOf(t, c.start(t))
		clash := taskReturn(verdictClash, "", "both sides decide the title")
		if failures := failuresOfReturn(t, check, clash); len(failures) != 0 {
			t.Errorf("failures = %q, want none", failures)
		}
	})
}
