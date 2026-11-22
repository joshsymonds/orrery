package interpret

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/joshsymonds/orrery/internal/build"
	"github.com/joshsymonds/orrery/internal/hostexec"
	"github.com/joshsymonds/orrery/internal/planner"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/taskspec"
)

// integratorTaskDone is the status of an accepted task, whose change integration composed onto the epic's branch, and
// integratorNone what a task line says for a commit no integrate run records.
const (
	integratorTaskDone = "done"
	integratorNone     = "None."
)

// logFields is how many fields each line of the git log sideCommits reads holds: the commit, its subject and its author
// date.
const logFields = 3

// conflictCode returns the status code git status gives an unmerged path whose index holds stages, in order: 1 for the
// merge base, 2 for the candidate's side and 3 for origin's side. Stages git never leaves unmerged give "".
func conflictCode(stages string) string {
	switch stages {
	case "1":
		return "DD"
	case "2":
		return "AU"
	case "12":
		return "UD"
	case "3":
		return "UA"
	case "13":
		return "DU"
	case "23":
		return "AA"
	case "123":
		return "UU"
	}
	return ""
}

// sideCommit is one commit on a side of the merge, with its subject, its author date and its patch id, which is "" for
// a commit with no patch.
type sideCommit struct {
	id, subject, date, patch string
}

// integratorPrompt renders the prompt of a round of the release integrator that finishes merge, whose origin commit was
// fetched from origin's branch: the role text, then the sections Contract, Decisions, Merge, Conflicted paths, Tasks,
// Candidate commits, Origin commits, Same change, Checks after you and Previous attempt, each under a `## ` heading and
// None. when it has no lines (planner.WriteSection). head is the merge commit the round before made, "" for the first
// round, and previous the failure that commit met. Git runs on host, the host the epic lives on.
func (r *Runner) integratorPrompt(
	ctx context.Context, host hostexec.Host, epic store.EpicRow, merge integratorMerge, branch, head, previous string,
) (string, error) {
	flow, err := r.store.EpicFlow(ctx, epic.ID)
	if err != nil {
		return "", fmt.Errorf("read epic %s: %w", epic.ID, err)
	}
	base, err := gitOutput(ctx, host, merge.repo, "merge-base", merge.tip, merge.origin)
	if err != nil {
		return "", err
	}
	base = strings.TrimSpace(base)
	contract, err := integratorContract(epic)
	if err != nil {
		return "", err
	}
	conflicted, err := conflictedLines(ctx, host, merge, base)
	if err != nil {
		return "", err
	}
	candidate, err := logLines(ctx, host, merge.repo, base, merge.tip)
	if err != nil {
		return "", err
	}
	origin, err := logLines(ctx, host, merge.repo, base, merge.origin)
	if err != nil {
		return "", err
	}
	same, err := sameChangeLines(ctx, host, merge.repo, base, merge)
	if err != nil {
		return "", err
	}
	var out strings.Builder
	fmt.Fprintln(&out, strings.TrimRight(integratorRole, "\n"))
	planner.WriteSection(&out, "Contract", contract)
	planner.WriteSection(&out, "Decisions", planner.DecisionLines(epic, math.MaxInt64))
	planner.WriteSection(&out, "Merge", mergeLines(merge, branch, base, head))
	planner.WriteSection(&out, "Conflicted paths", conflicted)
	planner.WriteSection(&out, "Tasks", integratorTaskLines(epic.Tasks, flow.Runs))
	planner.WriteSection(&out, "Candidate commits", candidate)
	planner.WriteSection(&out, "Origin commits", origin)
	planner.WriteSection(&out, "Same change", same)
	planner.WriteSection(&out, "Checks after you", r.checkLines(ctx, epic))
	planner.WriteSection(&out, "Previous attempt", previousLines(previous))
	return out.String(), nil
}

// integratorContract returns the contract lines a release integrator decides by: the Intent, each Requirement with its
// evidence item (planner.RequirementItems), and each Must Not Ship entry, which the store holds as the epic's
// exclusions.
func integratorContract(epic store.EpicRow) ([]string, error) {
	requirements, err := planner.RequirementItems(epic.Requirements)
	if err != nil {
		return nil, fmt.Errorf("contract section: %w", err)
	}
	mustNot := make([]string, 0, len(epic.Exclusions))
	for _, exclusion := range epic.Exclusions {
		mustNot = append(mustNot, "- "+exclusion)
	}
	lines := append([]string{"Intent: " + epic.Intent}, planner.ListLines("Requirements", requirements)...)
	return append(lines, planner.ListLines("Must Not Ship", mustNot)...), nil
}

// mergeLines returns the Merge section: the worktree, the release tip and origin's commit with the names git gives each
// side, the branch of origin the commit was fetched from, the merge base, and the message the merge commit carries. A
// round after the first also names head, the merge commit the round before made, which is HEAD and is amended.
func mergeLines(merge integratorMerge, branch, base, head string) []string {
	lines := []string{
		"Worktree: " + merge.worktree,
		fmt.Sprintf("Release tip: %s, the candidate's side: HEAD when the merge began, ours, stage 2", merge.tip),
		fmt.Sprintf("Origin's commit: %s from origin's %s, origin's side: MERGE_HEAD, theirs, stage 3",
			merge.origin, branch),
		"Merge base: " + base,
		"Commit message: " + mergeMessage(merge.tip, merge.origin),
	}
	if head != "" {
		lines = append(lines, fmt.Sprintf(
			"Committed: an earlier round committed this merge as %s, which is HEAD now. Amend it; its parents stay "+
				"the release tip and origin's commit.", head))
	}
	return lines
}

// conflictedLines returns each conflicted path of merge as `- <code> <path>`, with the status code git status gives it
// for the stages it conflicts in, then the commits of each side since base that touched it, newest first, as
// `  candidate: <commit> <subject>` and `  origin: <commit> <subject>`.
func conflictedLines(ctx context.Context, host hostexec.Host, merge integratorMerge, base string) ([]string, error) {
	codes, err := conflictStatus(ctx, host, merge)
	if err != nil {
		return nil, err
	}
	var lines []string
	for _, path := range merge.conflicts {
		line := "- " + path
		if code := codes[path]; code != "" {
			line = fmt.Sprintf("- %s %s", code, path)
		}
		lines = append(lines, line)
		for _, side := range []struct{ name, tip string }{{"candidate", merge.tip}, {"origin", merge.origin}} {
			touched, logErr := pathCommits(ctx, host, merge.repo, base, side.tip, path)
			if logErr != nil {
				return nil, logErr
			}
			for _, commit := range touched {
				lines = append(lines, fmt.Sprintf("  %s: %s", side.name, commit))
			}
		}
	}
	return lines, nil
}

// conflictStatus returns the status code of each path git's merge of origin's commit into the release tip leaves
// unmerged, from the stages merge-tree names for it.
func conflictStatus(ctx context.Context, host hostexec.Host, merge integratorMerge) (map[string]string, error) {
	out, _, err := runMergeTree(ctx, host, merge.repo, "-z", merge.tip, merge.origin)
	if err != nil {
		return nil, err
	}
	stages := make(map[string]string)
	records := strings.Split(out, "\x00")
	for _, record := range records[1:] {
		info, path, found := strings.Cut(record, "\t")
		if !found {
			break
		}
		fields := strings.Fields(info)
		if len(fields) == 3 && !strings.Contains(stages[path], fields[2]) {
			stages[path] += fields[2]
		}
	}
	codes := make(map[string]string, len(stages))
	for path, held := range stages {
		sorted := []byte(held)
		slices.Sort(sorted)
		codes[path] = conflictCode(string(sorted))
	}
	return codes, nil
}

// pathCommits returns the commits from base to tip that touched path, newest first, each as its id and subject. It
// returns at most integratorPathLimit of them, and says so in a last line when there are more.
func pathCommits(ctx context.Context, host hostexec.Host, repo, base, tip, path string) ([]string, error) {
	out, err := gitOutput(ctx, host, repo, "log", fmt.Sprintf("-n%d", integratorPathLimit+1), "--format=%H %s",
		base+".."+tip, "--", path)
	if err != nil {
		return nil, err
	}
	commits := nonEmptyLines(out)
	if len(commits) > integratorPathLimit {
		commits = append(commits[:integratorPathLimit],
			"more commits are not shown; git log --merge -p -- PATH lists them")
	}
	return commits, nil
}

// integratorTaskLines returns each accepted task as `- T<seq> <title>`, then the lines taskspec.Lines gives for its
// spec version, each indented two spaces, when the task's current version is past version 1, then its deliverable, its
// files and the commit its integration composed onto the epic's branch, from the integrate run that claimed it.
func integratorTaskLines(tasks []store.EpicTask, runs []store.NodeRun) []string {
	composed := composedCommits(runs)
	var lines []string
	for _, task := range tasks {
		if task.Status != integratorTaskDone {
			continue
		}
		commit, ok := composed[task.Seq]
		if !ok {
			commit = integratorNone
		}
		lines = append(lines, fmt.Sprintf("- T%d %s", task.Seq, task.Title))
		for _, line := range taskspec.Lines(task) {
			lines = append(lines, "  "+line)
		}
		lines = append(lines,
			"  deliverable: "+task.Deliverable,
			"  files: "+strings.Join(task.Files, ", "),
			"  commit: "+commit,
		)
	}
	return lines
}

// composedCommits returns the commit each task's integration composed, from the results of the integrate runs that
// passed. A task composed again by a later run takes that run's commit.
func composedCommits(runs []store.NodeRun) map[int64]string {
	commits := make(map[int64]string)
	for _, run := range runs {
		if run.Kind != kindIntegrate || run.Outcome != outcomePassed {
			continue
		}
		var result batchResult
		if err := json.Unmarshal([]byte(run.Result), &result); err != nil {
			continue
		}
		for _, composed := range result.Composed {
			commits[composed.Task] = composed.Commit
		}
	}
	return commits
}

// logLines returns the commits from base to tip, newest first, as git log --oneline gives them. Past
// integratorLogLimit commits it gives the newest of them and a last line that says how many there are.
func logLines(ctx context.Context, host hostexec.Host, repo, base, tip string) ([]string, error) {
	out, err := gitOutput(ctx, host, repo, "log", "--oneline", fmt.Sprintf("-n%d", integratorLogLimit), base+".."+tip)
	if err != nil {
		return nil, err
	}
	lines := nonEmptyLines(out)
	if len(lines) < integratorLogLimit {
		return lines, nil
	}
	count, err := gitOutput(ctx, host, repo, "rev-list", "--count", base+".."+tip)
	if err != nil {
		return nil, err
	}
	if total := strings.TrimSpace(count); total != fmt.Sprint(integratorLogLimit) {
		lines = append(lines, fmt.Sprintf("These are the newest %d of %s commits.", integratorLogLimit, total))
	}
	return lines, nil
}

// sameChangeLines returns each pair of a candidate commit and an origin commit since base that hold one change: the
// pairs whose patch ids, as git patch-id --stable gives them, are equal, as `same`, and the pairs whose subject and
// author date are equal and whose patches are not, as `differs`.
func sameChangeLines(
	ctx context.Context, host hostexec.Host, repo, base string, merge integratorMerge,
) ([]string, error) {
	candidate, err := sideCommits(ctx, host, repo, base, merge.tip)
	if err != nil {
		return nil, err
	}
	origin, err := sideCommits(ctx, host, repo, base, merge.origin)
	if err != nil {
		return nil, err
	}
	var lines []string
	for _, ours := range candidate {
		for _, theirs := range origin {
			var kind string
			switch {
			case ours.patch != "" && ours.patch == theirs.patch:
				kind = "same"
			case ours.subject == theirs.subject && ours.date == theirs.date:
				kind = "differs"
			default:
				continue
			}
			lines = append(lines, fmt.Sprintf("- candidate %s and origin %s, %s: %s", ours.id, theirs.id, kind,
				ours.subject))
		}
	}
	return lines, nil
}

// sideCommits returns the commits from base to tip that are not merges, newest first, each with its subject, author
// date and patch id.
func sideCommits(ctx context.Context, host hostexec.Host, repo, base, tip string) ([]sideCommit, error) {
	span := base + ".." + tip
	out, err := gitOutput(ctx, host, repo, "log", "--no-merges", "--format=%H%x00%s%x00%aI", span)
	if err != nil {
		return nil, err
	}
	patches, err := patchIDs(ctx, host, repo, span)
	if err != nil {
		return nil, err
	}
	var commits []sideCommit
	for _, line := range nonEmptyLines(out) {
		fields := strings.Split(line, "\x00")
		if len(fields) != logFields {
			return nil, fmt.Errorf("git log of %s gave the line %q", span, line)
		}
		commits = append(commits, sideCommit{
			id: fields[0], subject: fields[1], date: fields[2], patch: patches[fields[0]],
		})
	}
	return commits, nil
}

// patchIDs returns the patch id git patch-id --stable gives each commit of span that is not a merge, by commit, on host.
// The patches git log -p --no-merges writes for span are fed to git patch-id as its stdin.
func patchIDs(ctx context.Context, host hostexec.Host, repo, span string) (map[string]string, error) {
	patches, err := gitOutput(ctx, host, repo, "log", "--no-merges", "-p", "--format=commit %H", span)
	if err != nil {
		return nil, fmt.Errorf("git log of %s: %w", span, err)
	}
	out, err := host.Git(ctx, hostexec.GitCall{
		Dir: repo, Args: []string{"patch-id", "--stable"}, Stdin: []byte(patches),
	})
	if err != nil {
		return nil, fmt.Errorf("git patch-id of %s: %w", span, err)
	}
	ids := make(map[string]string)
	for _, line := range nonEmptyLines(string(out)) {
		patch, commit, found := strings.Cut(line, " ")
		if !found {
			return nil, errors.New("git patch-id gave the line " + line)
		}
		ids[commit] = patch
	}
	return ids, nil
}

// checkLines returns what checks the integrator's commit after it returns done: each Done gate with its argv, or with
// why it does not resolve, then each capture with its Requirement and its argv.
func (r *Runner) checkLines(ctx context.Context, epic store.EpicRow) []string {
	gates := make([]string, 0, len(epic.DoneGates))
	for _, name := range epic.DoneGates {
		gate, err := build.ResolveGate(ctx, epic, name, r.getenv)
		switch {
		case err != nil:
			gates = append(gates, fmt.Sprintf("- %s: does not resolve: %v", name, err))
		case gate.Agent != "":
			gates = append(gates, fmt.Sprintf("- %s: agent %s", name, gate.Agent))
		default:
			gates = append(gates, fmt.Sprintf("- %s: %s", name, jsonList(gate.Argv)))
		}
	}
	var captures []string
	for _, requirement := range epic.Requirements {
		if len(requirement.Item.Capture) > 0 {
			captures = append(captures, fmt.Sprintf("- %s: %s", requirement.ID, jsonList(requirement.Item.Capture)))
		}
	}
	return append(planner.ListLines("Done gates", gates), planner.ListLines("Captures", captures)...)
}

// jsonList returns items as a JSON array, the form a gate's argv takes in a prompt.
func jsonList(items []string) string {
	encoded, err := json.Marshal(items)
	if err != nil {
		return strings.Join(items, " ")
	}
	return string(encoded)
}

// previousLines returns the lines of previous, the failure the commit of the round before met, or none for the first
// round.
func previousLines(previous string) []string {
	if previous == "" {
		return nil
	}
	return strings.Split(strings.TrimRight(previous, "\n"), "\n")
}

// nonEmptyLines returns the lines of text that are not empty.
func nonEmptyLines(text string) []string {
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	return lines
}
