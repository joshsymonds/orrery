package interpret

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/joshsymonds/orrery/internal/hostexec"
	"github.com/joshsymonds/orrery/internal/planner"
	"github.com/joshsymonds/orrery/internal/store"
)

// composedPrefix opens the subject compose gives the merge that composes a task onto the candidate: 'Merge T<n> <rev>',
// where n is the task's sequence number and rev the task's revision.
const composedPrefix = "Merge T"

// composedMerge is a merge that composes a task onto the candidate's side: its commit, the task's sequence number and
// the paths the merge changes from its first parent, the candidate it composed onto.
type composedMerge struct {
	commit  string
	seq     int64
	changed []string
}

// taskMergeMessage is the message of the merge of a task's revision into the candidate, the message compose gives the
// merge that composes a task.
func taskMergeMessage(seq int64, revision string) string {
	return fmt.Sprintf("Merge T%d %s", seq, revision)
}

// taskIntegratorPrompt renders the prompt of a round of the task integrator that merges task's revision into the
// candidate in merge: the role text, then the sections Task, Merge, Conflicted paths and Previous attempt, each under
// a `## ` heading and None. when it has no lines (planner.WriteSection). tasks are the epic's tasks, which name the
// composed merges on the candidate's side, and previous is the failure the round before met, "" for the first round. Git
// runs on host, the host the epic lives on.
func taskIntegratorPrompt(
	ctx context.Context, host hostexec.Host, merge integratorMerge, tasks []store.EpicTask, task store.EpicTask,
	previous string,
) (string, error) {
	base, err := gitOutput(ctx, host, merge.repo, "merge-base", merge.tip, merge.origin)
	if err != nil {
		return "", err
	}
	base = strings.TrimSpace(base)
	conflicted, err := taskConflictedLines(ctx, host, merge, tasks, base)
	if err != nil {
		return "", err
	}
	var out strings.Builder
	fmt.Fprintln(&out, strings.TrimRight(taskIntegratorRole, "\n"))
	planner.WriteSection(&out, "Task", taskLines(task))
	planner.WriteSection(&out, "Merge", taskMergeLines(merge, task.Seq, base))
	planner.WriteSection(&out, "Conflicted paths", conflicted)
	planner.WriteSection(&out, "Previous attempt", previousLines(previous))
	return out.String(), nil
}

// previousLines returns the lines of previous, the failure the commit of the task integrator's round before met, or none
// for the first round.
func previousLines(previous string) []string {
	if previous == "" {
		return nil
	}
	return strings.Split(strings.TrimRight(previous, "\n"), "\n")
}

// taskLines returns the title and deliverable of task, as `- T<n> <title>` and `  deliverable: <deliverable>`.
func taskLines(task store.EpicTask) []string {
	return []string{fmt.Sprintf("- T%d %s", task.Seq, task.Title), "  deliverable: " + task.Deliverable}
}

// taskMergeLines returns the Merge section: the worktree, the candidate and the task's revision with the names git
// gives each side, the merge base, and the message the merge commit carries.
func taskMergeLines(merge integratorMerge, seq int64, base string) []string {
	return []string{
		"Worktree: " + merge.worktree,
		"Candidate: " + merge.tip + ", the candidate's side: HEAD when the merge began, ours, stage 2",
		"Task's revision: " + merge.origin + ", the task's side: MERGE_HEAD, theirs, stage 3",
		"Merge base: " + base,
		"Commit message: " + taskMergeMessage(seq, merge.origin),
	}
}

// taskConflictedLines returns each conflicted path of merge as `- <path>`, then each composed merge on the candidate's
// side since base that changed the path, newest first, as the task's title and deliverable (taskSideLines). A path no
// composed merge changed says None.
func taskConflictedLines(
	ctx context.Context, host hostexec.Host, merge integratorMerge, tasks []store.EpicTask, base string,
) ([]string, error) {
	merges, err := composedMerges(ctx, host, merge.repo, base, merge.tip)
	if err != nil {
		return nil, err
	}
	var lines []string
	for _, path := range merge.conflicts {
		lines = append(lines, "- "+path)
		var sides []string
		for _, composed := range merges {
			if slices.Contains(composed.changed, path) {
				sides = append(sides, taskSideLines(composed, tasks)...)
			}
		}
		if len(sides) == 0 {
			sides = []string{"  candidate: " + integratorNone}
		}
		lines = append(lines, sides...)
	}
	return lines, nil
}

// taskSideLines returns the lines that name the task composed merge composes, as `  candidate T<n> <title>, merged as
// <commit>` and `    deliverable: <deliverable>`, or say that it is not a task of the epic.
func taskSideLines(composed composedMerge, tasks []store.EpicTask) []string {
	i := slices.IndexFunc(tasks, func(task store.EpicTask) bool { return task.Seq == composed.seq })
	if i < 0 {
		return []string{
			fmt.Sprintf("  candidate T%d: not a task of the epic, merged as %s", composed.seq, composed.commit),
		}
	}
	task := tasks[i]
	return []string{
		fmt.Sprintf("  candidate T%d %s, merged as %s", task.Seq, task.Title, composed.commit),
		"    deliverable: " + task.Deliverable,
	}
}

// composedMerges returns the merges from base to tip that compose a task, newest first, each with the task's sequence
// number and the paths it changes from its first parent.
func composedMerges(ctx context.Context, host hostexec.Host, repo, base, tip string) ([]composedMerge, error) {
	out, err := gitOutput(ctx, host, repo, "log", "--merges", "--format=%H %s", base+".."+tip)
	if err != nil {
		return nil, err
	}
	var merges []composedMerge
	for _, line := range nonEmptyLines(out) {
		commit, subject, _ := strings.Cut(line, " ")
		seq, ok := composedTaskSeq(subject)
		if !ok {
			continue
		}
		var changed []string
		changed, err = changedPaths(ctx, host, repo, commit+"^1", commit)
		if err != nil {
			return nil, err
		}
		merges = append(merges, composedMerge{commit: commit, seq: seq, changed: changed})
	}
	return merges, nil
}

// composedTaskSeq returns the sequence number of the task a subject names as compose writes it, 'Merge T<n> <rev>'. A
// subject of any other form is not a composed merge, and ok is false.
func composedTaskSeq(subject string) (seq int64, ok bool) {
	rest, found := strings.CutPrefix(subject, composedPrefix)
	number, _, spaced := strings.Cut(rest, " ")
	if !found || !spaced {
		return 0, false
	}
	seq, err := strconv.ParseInt(number, 10, 64)
	return seq, err == nil
}
