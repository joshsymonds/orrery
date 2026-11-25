package interpret

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/joshsymonds/orrery/internal/hostexec"
	"github.com/joshsymonds/orrery/internal/kinds"
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

// integratorPrompt renders the prompt of the round of the release integrator that makes merge, from result, what the
// release's prepare recorded, flow, the epic's rows, and facts, what the round decides beyond its merge: the role text,
// then the sections Contract, Decisions, Merge, Conflicted paths, Migration collisions, Tasks, Candidate commits,
// Origin commits, Same change, Candidate changes, Origin changes, Changed on both sides, Done gates, Evidence commands,
// Earlier reviews and Origin since the reviewed candidates, each under a `## ` heading and None. when it has no lines
// (planner.WriteSection). Merge names the branch of origin result's commit was fetched from, and Migration collisions
// holds result's collision lines. Done gates gives each Done gate as its check resolves it (doneGateLines). Earlier
// reviews and Origin since the reviewed candidates give facts' earlier verdicts (earlierReviewLines, originSinceLines).
// Git runs on host, the host the epic lives on.
func (r *Runner) integratorPrompt(
	ctx context.Context, host hostexec.Host, epic store.EpicRow, flow store.EpicFlow, merge integratorMerge,
	result kinds.ReleaseResult, facts integratorFacts,
) (string, error) {
	base, err := gitOutput(ctx, host, merge.repo, "merge-base", merge.tip, merge.origin)
	if err != nil {
		return "", err
	}
	base = strings.TrimSpace(base)
	contract, err := integratorContract(epic)
	if err != nil {
		return "", err
	}
	sides, err := integratorSides(ctx, host, merge, base)
	if err != nil {
		return "", err
	}
	since, err := originSinceLines(ctx, host, merge.repo, facts.earlier, merge.origin)
	if err != nil {
		return "", err
	}
	var out strings.Builder
	fmt.Fprintln(&out, strings.TrimRight(integratorRole, "\n"))
	planner.WriteSection(&out, "Contract", contract)
	planner.WriteSection(&out, "Decisions", planner.DecisionLines(epic, math.MaxInt64))
	planner.WriteSection(&out, "Merge", mergeLines(merge, result.Branch, base))
	planner.WriteSection(&out, "Conflicted paths", sides.conflicted)
	planner.WriteSection(&out, migrationsSection, result.Collisions)
	planner.WriteSection(&out, "Tasks", integratorTaskLines(epic.Tasks, flow.Runs))
	planner.WriteSection(&out, "Candidate commits", sides.candidateLog)
	planner.WriteSection(&out, "Origin commits", sides.originLog)
	planner.WriteSection(&out, "Same change", sides.same)
	planner.WriteSection(&out, "Candidate changes", sides.candidateChanges)
	planner.WriteSection(&out, "Origin changes", sides.originChanges)
	planner.WriteSection(&out, "Changed on both sides", sides.both)
	planner.WriteSection(&out, "Done gates", doneGateLines(facts.gates))
	planner.WriteSection(&out, "Evidence commands", evidenceLines(epic))
	planner.WriteSection(&out, "Earlier reviews", earlierReviewLines(facts.earlier))
	planner.WriteSection(&out, "Origin since the reviewed candidates", since)
	return out.String(), nil
}

// mergeSides is what the prompt of a release integrator says of each side of its merge since the merge base: the
// conflicted paths with the commits of each side that touched them, each side's commits, the pairs of commits that hold
// one change, the paths each side changed, and the paths both sides changed.
type mergeSides struct {
	conflicted, candidateLog, originLog, same, candidateChanges, originChanges, both []string
}

// integratorSides reads, on host, what each side of merge did since base, as mergeSides holds it (conflictedLines,
// logLines, sameChangeLines, changeLines, bothLines).
func integratorSides(
	ctx context.Context, host hostexec.Host, merge integratorMerge, base string,
) (mergeSides, error) {
	var sides mergeSides
	var err error
	if sides.conflicted, err = conflictedLines(ctx, host, merge, base); err != nil {
		return mergeSides{}, err
	}
	if sides.candidateLog, err = logLines(ctx, host, merge.repo, base, merge.tip); err != nil {
		return mergeSides{}, err
	}
	if sides.originLog, err = logLines(ctx, host, merge.repo, base, merge.origin); err != nil {
		return mergeSides{}, err
	}
	if sides.same, err = sameChangeLines(ctx, host, merge.repo, base, merge); err != nil {
		return mergeSides{}, err
	}
	candidate, err := nameStatus(ctx, host, merge.repo, base, merge.tip)
	if err != nil {
		return mergeSides{}, err
	}
	origin, err := nameStatus(ctx, host, merge.repo, base, merge.origin)
	if err != nil {
		return mergeSides{}, err
	}
	sides.candidateChanges = changeLines(candidate, base, merge.tip)
	sides.originChanges = changeLines(origin, base, merge.origin)
	sides.both = bothLines(candidate, origin)
	return sides, nil
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
// held merge says origin's commit is already in the tip, so there is no merge, and gives the message of the renumber.
func mergeLines(merge integratorMerge, branch, base string) []string {
	if merge.held {
		return []string{
			"Worktree: " + merge.worktree,
			fmt.Sprintf("Release tip: %s, the candidate's side: HEAD", merge.tip),
			fmt.Sprintf("Origin's commit: %s from origin's %s, already in the release tip, so there is no merge and "+
				"no MERGE_HEAD", merge.origin, branch),
			"Merge base: " + base,
			"Commit message: " + renumberMessage(merge.tip, merge.origin),
		}
	}
	return []string{
		"Worktree: " + merge.worktree,
		fmt.Sprintf("Release tip: %s, the candidate's side: HEAD when the merge began, ours, stage 2", merge.tip),
		fmt.Sprintf("Origin's commit: %s from origin's %s, origin's side: MERGE_HEAD, theirs, stage 3",
			merge.origin, branch),
		"Merge base: " + base,
		"Commit message: " + mergeMessage(merge.tip, merge.origin),
	}
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

// pathChange is one path a side of the merge changed since the merge base, with the status git diff --name-status gives
// it, such as A, M or D.
type pathChange struct {
	status, path string
}

// nameStatus returns each path that differs between base and tip in repo on host, in git's order, with its status, both
// sides of a rename included as a delete and an add.
func nameStatus(ctx context.Context, host hostexec.Host, repo, base, tip string) ([]pathChange, error) {
	out, err := gitOutput(ctx, host, repo, "diff", "--name-status", "-z", "--no-renames", base, tip)
	if err != nil {
		return nil, err
	}
	fields := strings.Split(strings.TrimSuffix(out, "\x00"), "\x00")
	var changes []pathChange
	for i := 0; i+1 < len(fields); i += 2 {
		changes = append(changes, pathChange{status: fields[i], path: fields[i+1]})
	}
	return changes, nil
}

// changeLines returns each of changes, a side's changes from base to tip, as `- <status> <path>`. Past
// integratorChangeLimit paths it gives the first of them and a last line that says how many there are and how to list
// them all.
func changeLines(changes []pathChange, base, tip string) []string {
	shown := changes[:min(len(changes), integratorChangeLimit)]
	lines := make([]string, 0, len(shown)+1)
	for _, change := range shown {
		lines = append(lines, fmt.Sprintf("- %s %s", change.status, change.path))
	}
	if len(changes) > len(shown) {
		lines = append(lines, fmt.Sprintf("These are the first %d of %d changed paths; git diff --name-status %s %s "+
			"lists them all.", len(shown), len(changes), base, tip))
	}
	return lines
}

// bothLines returns each path that both candidate and origin, the changes of each side since the merge base, changed,
// as `- <path>`, in the candidate's order. Past integratorChangeLimit paths it gives the first of them and a last line
// that says how many there are.
func bothLines(candidate, origin []pathChange) []string {
	var paths []string
	for _, change := range candidate {
		if slices.ContainsFunc(origin, func(theirs pathChange) bool { return theirs.path == change.path }) {
			paths = append(paths, change.path)
		}
	}
	shown := paths[:min(len(paths), integratorChangeLimit)]
	lines := make([]string, 0, len(shown)+1)
	for _, path := range shown {
		lines = append(lines, "- "+path)
	}
	if len(paths) > len(shown) {
		lines = append(lines, fmt.Sprintf("These are the first %d of %d paths both sides changed.", len(shown),
			len(paths)))
	}
	return lines
}

// evidenceLines returns each Requirement of epic whose evidence item captures a command, as `- <id>: <argv>` with its
// argv as a JSON array: the commands the epic's own work was judged by, which the release integrator reads as
// references for what the repository checks and how.
func evidenceLines(epic store.EpicRow) []string {
	var commands []string
	for _, requirement := range epic.Requirements {
		if len(requirement.Item.Capture) > 0 {
			commands = append(commands, fmt.Sprintf("- %s: %s", requirement.ID, jsonList(requirement.Item.Capture)))
		}
	}
	return commands
}

// jsonList returns items as a JSON array, the form a gate's argv takes in a prompt.
func jsonList(items []string) string {
	encoded, err := json.Marshal(items)
	if err != nil {
		return strings.Join(items, " ")
	}
	return string(encoded)
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
