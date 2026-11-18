package build

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/joshsymonds/orrery/internal/planner"
	"github.com/joshsymonds/orrery/internal/store"
)

// The limits of what an orchestrator's prompt carries: the bytes of the implementer's diff, whose middle is cut past
// it, and the bytes of each log tail.
const (
	promptDiffLimit = 48 << 10
	promptTailLimit = 4 << 10
)

// orchestratorPrompt renders the prompt of the orchestrator step that decides the request of view's task in epic, with
// peers the epic's tasks: the role text, then the sections Contract, Decisions, Task, Worktree, Requested files, Tasks,
// Return, Gate and History, each under a `## ` heading and None. when it has no lines (planner.WriteSection). The facts
// orreryd reads come before the implementer's own account of them. It returns an error when the contract or the task's
// evidence item cannot be encoded.
func (r *Runner) orchestratorPrompt(ctx context.Context, epic store.EpicRow, view View, peers []Peer) (string, error) {
	task := view.Task
	var out strings.Builder
	out.WriteString(strings.TrimRight(OrchestratorRole, "\n") + "\n")
	contract, err := planner.ContractLines(epic)
	if err != nil {
		return "", fmt.Errorf("contract section: %w", err)
	}
	planner.WriteSection(&out, "Contract", contract)
	planner.WriteSection(&out, "Decisions", planner.DecisionLines(epic, math.MaxInt64))
	taskSection, err := r.requestTaskLines(ctx, epic, task)
	if err != nil {
		return "", err
	}
	planner.WriteSection(&out, "Task", taskSection)
	path := ImplementWorktree(r.stateDir, epic.ID, task.Seq)
	if view.Returned != nil && view.Returned.Workspace != "" {
		path = view.Returned.Workspace
	}
	tree := readWorktree(ctx, path, task.Build.Base)
	planner.WriteSection(&out, "Worktree", tree.lines())
	var asked []string
	if view.Returned != nil {
		ret, _ := parseImplementerReturn(view.Returned.Result)
		asked = ret.NeedsFiles
	}
	planner.WriteSection(
		&out,
		"Requested files",
		requestedLines(ctx, epic.Repository, task, asked, tree.changed, peers),
	)
	planner.WriteSection(&out, "Tasks", peerLines(ctx, epic.Repository, task, peers))
	planner.WriteSection(&out, "Return", returnLines(view.Returned))
	planner.WriteSection(&out, "Gate", r.gateLines(ctx, task, view.Returned))
	planner.WriteSection(&out, "History", r.historyLines(ctx, epic.ID, task))
	return out.String(), nil
}

// requestTaskLines returns the Task section: the task's label and title, deliverable, files, test_changes, the
// Requirements it claims and contributes to, its gate with its argv when it resolves, its evidence item as compact
// JSON, its base, attempt, rerun and decisions, and the turn that planned it with that turn's outline.
func (r *Runner) requestTaskLines(ctx context.Context, epic store.EpicRow, task store.EpicTask) ([]string, error) {
	item, err := json.Marshal(task.Evidence)
	if err != nil {
		return nil, fmt.Errorf("encode evidence of %s: %w", taskLabel(task.Seq), err)
	}
	gate := "Gate: " + task.Gate
	if resolved, gateErr := ResolveGate(ctx, epic, task.Gate, r.getenv); gateErr == nil && len(resolved.Argv) > 0 {
		argv, encodeErr := json.Marshal(resolved.Argv)
		if encodeErr == nil {
			gate += " " + string(argv)
		}
	}
	lines := []string{
		fmt.Sprintf("%s: %s", taskLabel(task.Seq), task.Title),
		"Deliverable: " + task.Deliverable,
		"Files: " + listText(task.Files),
		"Test changes: " + listText(task.TestChanges),
		"Requirements: " + listText(task.Requirements),
		"Contributes: " + listText(task.Contributes),
		gate,
		"Evidence item: " + string(item),
		"Base: " + task.Build.Base,
		fmt.Sprintf("Attempt %d, rerun %d, decisions %d of %d", task.Build.Attempt, task.Build.Rerun,
			task.Build.Decisions, decisionLimit),
	}
	for _, turn := range epic.Turns {
		if turn.Seq == task.Turn {
			lines = append(lines, fmt.Sprintf("Planned by turn %d: %s", turn.Seq, turn.Outline))
		}
	}
	return lines, nil
}

// listText returns items joined with commas, or None. when there are none.
func listText(items []string) string {
	if len(items) == 0 {
		return "None."
	}
	return strings.Join(items, ", ")
}

// worktreeState is what an orchestrator's prompt reads of the implement worktree: its path, HEAD, status, commits since
// the base, diff stat and diff from the base, uncommitted work included, the paths that differ from the base or are
// untracked, and why the worktree could not be read, "" when it could.
type worktreeState struct {
	path, head, stat, diff string
	status, log, changed   []string
	missing                string
}

// readWorktree reads the implement worktree at path against base. A git command that fails leaves its part empty and
// records the first failure, so the prompt says what it could not read.
func readWorktree(ctx context.Context, path, base string) worktreeState {
	tree := worktreeState{path: path}
	read := func(args ...string) string {
		out, err := runGit(ctx, path, args...)
		if err != nil && tree.missing == "" {
			tree.missing = err.Error()
		}
		return out
	}
	tree.head = read("rev-parse", "HEAD")
	if tree.missing != "" {
		return tree
	}
	tree.status = splitLines(read("status", "--short", "--untracked-files=all"))
	tree.log = splitLines(read("log", "--oneline", base+"..HEAD"))
	tree.stat = read("diff", "--stat", base)
	tree.diff = read("diff", base)
	tree.changed = splitLines(read("diff", "--name-only", "--no-renames", base))
	for _, path := range splitLines(read("ls-files", "--others", "--exclude-standard")) {
		if !slices.Contains(tree.changed, path) {
			tree.changed = append(tree.changed, path)
		}
	}
	return tree
}

// lines returns the Worktree section of tree.
func (tree worktreeState) lines() []string {
	lines := []string{"Path: " + tree.path}
	if tree.head == "" {
		return append(lines, "The worktree could not be read: "+tree.missing)
	}
	lines = append(lines, "HEAD: "+tree.head)
	lines = append(lines, planner.ListLines("Status (git status --short)", tree.status)...)
	lines = append(lines, planner.ListLines("Commits since the base (git log --oneline)", tree.log)...)
	lines = append(
		lines,
		planner.ListLines("Diff stat from the base, uncommitted work included", splitLines(tree.stat))...)
	lines = append(lines, planner.ListLines("Diff from the base, uncommitted work included; untracked files are only "+
		"in the status", splitLines(cutMiddle(tree.diff, promptDiffLimit)))...)
	if tree.missing != "" {
		lines = append(lines, "Not all of the worktree could be read: "+tree.missing)
	}
	return lines
}

// halves is how many parts cutMiddle keeps of a text it cuts: its head and its tail.
const halves = 2

// cutMiddle returns text, or, when it is longer than limit bytes, its first and last halves of limit with a line that
// says how many bytes were cut between them.
func cutMiddle(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	half := limit / halves
	head, tail := text[:half], text[len(text)-half:]
	return fmt.Sprintf("%s\n[%d bytes cut]\n%s", head, len(text)-2*half, tail)
}

// requestedLines returns the Requested files section: one line for each path the return asked for and each path the
// worktree changed outside the task's files, in that order and once each, saying whether it exists at the base and who
// holds it (holderOf).
func requestedLines(
	ctx context.Context, repo string, task store.EpicTask, asked, changed []string, peers []Peer,
) []string {
	var paths []string
	for _, path := range asked {
		if !slices.Contains(paths, path) {
			paths = append(paths, path)
		}
	}
	for _, path := range changed {
		if !slices.Contains(task.Files, path) && !slices.Contains(paths, path) {
			paths = append(paths, path)
		}
	}
	lines := make([]string, 0, len(paths))
	for _, path := range paths {
		exists := "new at the base"
		if _, err := runGit(ctx, repo, "cat-file", "-e", task.Build.Base+":"+path); err == nil {
			exists = "exists at the base"
		}
		origin := "changed outside the task's files"
		if slices.Contains(asked, path) {
			origin = "asked for"
		}
		lines = append(lines, fmt.Sprintf("- %s: %s; %s; %s", path, origin, exists, holderOf(path, task, peers).Text))
	}
	return lines
}

// peerLines returns the Tasks section: each other open task and each accepted task not yet integrated, with its unit,
// title, deliverable, files or changed paths, and the tasks it waits for; then the tasks integrated since this task's
// base; then the failed tasks with their failures.
func peerLines(ctx context.Context, repo string, task store.EpicTask, peers []Peer) []string {
	var lines, integrated, failed []string
	for _, peer := range peers {
		other := peer.Task
		if other.Seq == task.Seq {
			continue
		}
		label := taskLabel(other.Seq)
		switch {
		case other.Status == taskOpen:
			lines = append(lines, fmt.Sprintf("- %s [open, %s] %s", label, unitOf(other), other.Title),
				"  deliverable: "+other.Deliverable, "  files: "+listText(other.Files))
			if after := AfterSeqs(other.Build.After); Waiting(other) && len(after) > 0 {
				lines = append(lines, "  waits for: "+labelsOf(after))
			}
		case peer.State == Pending:
			lines = append(lines, fmt.Sprintf("- %s [accepted, not yet integrated] %s", label, other.Title),
				"  deliverable: "+other.Deliverable, "  changed: "+listText(peer.Changed))
		case peer.State == Integrated && !reaches(ctx, repo, peer.Composed, task.Build.Base):
			integrated = append(integrated, fmt.Sprintf("- %s %s, composed as %s", label, other.Title, peer.Composed))
		case other.Status == taskFailed:
			failed = append(failed, fmt.Sprintf("- %s %s", label, other.Title), "  failure: "+other.Build.Failure)
		}
	}
	lines = append(lines, planner.ListLines("Integrated since this task's base", integrated)...)
	return append(lines, planner.ListLines("Failed", failed)...)
}

// reaches reports whether descendant reaches ancestor in repo. A comparison git cannot make reaches nothing.
func reaches(ctx context.Context, repo, ancestor, descendant string) bool {
	if ancestor == "" || descendant == "" {
		return false
	}
	ok, err := isAncestor(ctx, repo, ancestor, descendant)
	return err == nil && ok
}

// labelsOf returns the labels of seqs joined with commas.
func labelsOf(seqs []int64) string {
	labels := make([]string, 0, len(seqs))
	for _, seq := range seqs {
		labels = append(labels, taskLabel(seq))
	}
	return strings.Join(labels, ", ")
}

// returnLines returns the Return section: how the implementer's step ended, its whole kept return, then every
// send-back it had before that end: each failed check and each judge verdict that sent it back, by round.
func returnLines(step *store.StepRow) []string {
	if step == nil {
		return nil
	}
	end := "Step end: " + step.End
	if step.Reason != "" {
		end += ": " + step.Reason
	}
	var sent []string
	for _, check := range step.Checks {
		if check.OK {
			continue
		}
		sent = append(sent, fmt.Sprintf("- round %d, check %s: %s", check.Round, check.Name, check.Detail))
		sent = appendTail(sent, "stdout", check.Stdout)
		sent = appendTail(sent, "stderr", check.Stderr)
	}
	for _, verdict := range step.Verdicts {
		if verdict.Kind == judgeKind && verdict.Action == judgeContinue {
			sent = append(sent, fmt.Sprintf("- round %d, judge %s: %s", verdict.Round, verdict.Verdict, verdict.Detail))
		}
	}
	return append([]string{end, "Status: " + step.Status, "Kept return: " + step.Result},
		planner.ListLines("Send-backs", sent)...)
}

// The kind of a judge verdict on a step, and the action of one that sent the step back.
const (
	judgeKind     = "judge"
	judgeContinue = "continue"
)

// appendTail appends a tail of a check's stream to lines under label, when it has one.
func appendTail(lines []string, label, tail string) []string {
	if tail == "" {
		return lines
	}
	return append(lines, "  "+label+":", indent(lastBytes(tail, promptTailLimit)))
}

// indent returns each line of text indented by four spaces.
func indent(text string) string {
	return "    " + strings.ReplaceAll(strings.TrimRight(text, "\n"), "\n", "\n    ")
}

// lastBytes returns the last limit bytes of text, from a character start.
func lastBytes(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	return strings.ToValidUTF8(text[len(text)-limit:], "")
}

// gateLines returns the Gate section: the red run at the base with its exit, verdict, judge reason and the end of its
// log, the green run when the build has one, and each run of the task's gate the implementer's step made, with its
// output tails.
func (r *Runner) gateLines(ctx context.Context, task store.EpicTask, step *store.StepRow) []string {
	var lines []string
	if task.Red != nil {
		red, redLog, redStderr := r.redEvidence(ctx, task)
		lines = append(lines, fmt.Sprintf("Red at the base %s: exit %d, verdict %s: %s", red.Revision, red.Exit,
			red.Verdict, red.JudgeReason))
		lines = appendTail(lines, "red stdout", string(redLog))
		lines = appendTail(lines, "red stderr", string(redStderr))
	}
	if task.Green != nil {
		lines = append(lines, fmt.Sprintf("Green at %s: exit %d, verdict %s: %s", task.Green.Revision,
			task.Green.Exit, task.Green.Verdict, task.Green.JudgeReason))
	}
	if step == nil {
		return lines
	}
	for _, check := range step.Checks {
		if check.Name != gateCheck {
			continue
		}
		result := "passed"
		if !check.OK {
			result = "failed"
		}
		lines = append(lines, fmt.Sprintf("Gate %s in round %d %s: %s", task.Gate, check.Round, result, check.Detail))
		lines = appendTail(lines, "stdout", check.Stdout)
		lines = appendTail(lines, "stderr", check.Stderr)
	}
	return lines
}

// gateCheck is the name of the check a writing step's gate run is stored under.
const gateCheck = "gate"

// historyLines returns the History section of task from the steps of epic labeled with the task (historyOf). A store
// read that fails is logged and leaves the earlier steps out.
func (r *Runner) historyLines(ctx context.Context, epic string, task store.EpicTask) []string {
	rows, err := r.store.LabelledSteps(ctx, epic, taskLabel(task.Seq))
	if err != nil {
		r.logger.WarnContext(ctx, "read task steps", "epic", epic, "task", task.Seq, "err", err)
	}
	return historyOf(rows, task)
}

// historyOf returns the History section of task from rows, its steps in the order they started: each earlier
// orchestrator step that ended, with its decision, the fields its version changes or its question, and its reason, and
// how the implementer's next run ended, or that it reached no decision; then how many decisions are left before orreryd
// settles the request without one.
func historyOf(rows []store.StepRow, task store.EpicTask) []string {
	var lines []string
	for i, row := range rows {
		if row.Schema != OrchestratorSchema || row.End == "" {
			continue
		}
		ret, ok := parseOrchestratorReturn(row.Result)
		if !ok || ret.Status != statusDone {
			lines = append(lines, fmt.Sprintf("- %s: no decision (%s)", row.ID, row.End))
			continue
		}
		lines = append(lines, "- "+decisionText(task.Seq, ret))
		switch ret.Decision {
		case decisionRerun, decisionGrant:
			lines = append(lines, "  version: "+changesText(ret.Version.Changed))
		case decisionAsk:
			lines = append(lines, "  question: "+ret.Question.Text)
		}
		lines = append(lines, "  then: "+nextRun(rows[i+1:]))
	}
	return append(lines, fmt.Sprintf("Decisions left before orreryd settles the request without one: %d",
		max(decisionLimit-task.Build.Decisions, 0)))
}

// changesText returns the fields a version changes, comma-separated, or `changes nothing` when it changes none.
func changesText(changed []string) string {
	if len(changed) == 0 {
		return "changes nothing"
	}
	return "changes " + strings.Join(changed, ", ")
}

// nextRun returns how the first implementer step among rows ended, or that none has run.
func nextRun(rows []store.StepRow) string {
	for _, row := range rows {
		if row.Schema != ImplementerSchema {
			continue
		}
		if row.End == "" {
			return "the implementer is running"
		}
		ret, _ := parseImplementerReturn(row.Result)
		text := fmt.Sprintf("the implementer ended %s", row.End)
		if row.Status != "" {
			text += fmt.Sprintf(", returning %s: %s", row.Status, ret.Summary)
		}
		return text
	}
	return "no implementer run since"
}
