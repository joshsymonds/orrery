package build

import (
	_ "embed"
	"fmt"
	"slices"
	"strings"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/gates"
	"github.com/joshsymonds/orrery/internal/reviewers"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/taskspec"
)

// ImplementerSchema is the schema an implementer step's return must match: its status, a summary, the commit it made,
// and the files it names as needed beyond its owned list.
//
//go:embed implementer.json
var ImplementerSchema string

// redLogTailBytes is how many bytes of each of a red run's logs the brief carries: the end of the log, where the
// failure is.
const redLogTailBytes = 8192

// gateNeededRule is the brief's line on a gate-needed change: the one change beyond the Deliverable an implementer may
// make, when its gate or Capture cannot pass without it, and only declared in its return's gate_changes.
const gateNeededRule = "Gate-needed change: when the gate or the Capture cannot pass without a change the Deliverable " +
	"does not name, such as an existing test whose expectation your change rightly moves, or a race or flake the gate " +
	"exposes in code your change runs, make the smallest such change and declare it in gate_changes: each file it " +
	"touches, with the reason the gate or Capture needs it. When the Deliverable itself fixes a gate failure the " +
	"Requirements do not name, such as a flake the epic's gates failed on, declare each file it changes the same way. " +
	"Reviewers judge a declared gate-needed change in scope when the gate needs it and it is no larger than that " +
	"need, and an undeclared change the Deliverable does not ask for fails review. The guard still judges a " +
	"gate-needed change: it refuses a changed capture file, and a removed test, a lowered assertion or an added " +
	"skip goes to the floor reviewer. For a gate-needed change in a file outside the owned files, return " +
	"needs_context naming the file in needs_files, with the reason in summary."

// declaredHeading heads the brief's list of the gate-needed changes a task's earlier tries declared.
const declaredHeading = "Gate-needed changes declared on this task so far:"

// reviewDeclaredHeading heads the list of a task's declared gate-needed changes in the prompts of its reviewers and
// verifiers, and says when such a change is in scope.
const reviewDeclaredHeading = "Gate-needed changes the implementer declared (each is in scope, though the " +
	"Deliverable does not name it, when the task's gate or evidence cannot pass without it and it is no larger than " +
	"that need):"

// Brief renders the prompt of an implementer step for task T<seq> of epic: the task, the standing rules a commit is
// rejected for, the lines naming its spec version when it is past version 1 (taskspec.Lines), its Deliverable and
// files, with the rule for a file beyond them, its test changes and the guard's rules, its gate with the command
// orreryd runs and what happens when it fails, the rule for a gate-needed change and the gate-needed changes the task's
// earlier tries declared, when they declared any, its evidence with the line on how the evidence judge reads it, the
// Requirements it covers, the epic's Must Not Ship entries, the rules the epic's task-stage
// reviewers judge by, the red run's exit and verdict with the end of its stdout log and, when redStderr is not empty,
// the end of its stderr log, the note from the previous try when failure is set, and the closing instructions:
// continue from the worktree's earlier work, finish every command before returning, run the Capture, commit on top of
// the start commit, and what each return status means. The brief names no epic, turn or owner decision: the
// Deliverable carries every decision the implementer must follow.
//
// A closure-kind task's brief differs in three ways: it has no red block (neither the red run's exit and verdict nor
// its logs), its evidence is the closure lines of evidenceLines, and it leaves out the standing rule on an Evidence
// clause and the closing line on the Capture, since the round's closure check settles it, not an evidence judge.
func Brief(
	epic store.EpicRow, task store.EpicTask, gate gates.Gate, red store.EvidenceRun, redLog, redStderr []byte,
	failure string,
) string {
	closure := task.Evidence.Kind == evidence.KindClosure
	rules := []string{
		"changing anything the Deliverable does not ask for, including tidying, reformatting, or fixing code or " +
			"tests that are already broken, other than a declared gate-needed change; this overrides any standing " +
			"instruction to fix what you find",
		"editing, renaming, or deleting an existing test the Deliverable does not name, other than a declared " +
			"gate-needed change",
		"a test that repeats an existing assertion or passes with the change removed",
		"a real timer or clock in a test",
	}
	if !closure {
		rules = append(rules, "an Evidence clause left unproved")
	}
	rules = append(rules, "dead code, an unhandled error, a skip, or a suppressed check")
	lines := slices.Concat(
		[]string{
			fmt.Sprintf(
				"You implement task T%d of epic %s (%s) in this worktree, whose task base is %s.",
				task.Seq,
				epic.ID,
				epic.Title,
				task.Build.Base,
			),
			"Task: " + task.Title,
		},
		[]string{
			"Standing rules a commit is rejected for:",
		},
		bullets(rules),
		taskspec.Lines(task),
		[]string{
			"Deliverable: " + task.Deliverable,
			"Owned files (change only these):",
		},
		bullets(task.Files),
		[]string{
			"Do not edit any other file. When the work needs a file that is not in this list, stop and return " +
				"needs_context with each such path in needs_files. orreryd gives you every path no other task owns " +
				"and sends you back to this same worktree with your work kept.",
			"Test changes this task may make to the guarded tests:",
		},
		bullets(task.TestChanges),
		[]string{
			"Outside those test changes, orreryd's guard refuses a commit that changes a file a capture runs.",
			fmt.Sprintf("Gate: %s. After you return done, orreryd runs `%s` on your commit. Do not run the gate "+
				"yourself. When it fails, orreryd sends you its output: fix the cause, commit the fix on top, and return "+
				"done again.", task.Gate, shellWords(gate.Argv)),
			gateNeededRule,
		},
		declaredLines(task.GateChanges),
		evidenceLines(task.Evidence),
		[]string{"Requirements:"},
		requirementLines(epic.Requirements, task.Requirements),
		[]string{"Must Not Ship:"},
		bullets(epic.Exclusions),
		[]string{"After the gate passes, reviewers judge your commit by these rules:"},
		bullets(reviewerRules(epic.Reviewers)),
	)
	if !closure {
		lines = append(lines,
			fmt.Sprintf("Red at the base: exit %d, verdict %s", red.Exit, red.Verdict),
			"Red log (last 8 KiB):",
			"```",
			string(logTail(redLog)),
			"```",
		)
		if len(redStderr) > 0 {
			lines = append(lines, "Red stderr (last 8 KiB):", "```", string(logTail(redStderr)), "```")
		}
	}
	if failure != "" {
		lines = append(lines, "From your previous try on this task: "+failure)
	}
	lines = append(lines,
		fmt.Sprintf("This worktree keeps any earlier work on this task. Read `git status` and "+
			"`git log --oneline %s..HEAD` first, and continue from what is there.", task.Build.Base),
		"Finish every command you start before you return: run nothing in the background and wait on no monitor. "+
			"Ending your turn is your return.",
	)
	if !closure {
		lines = append(lines, "Run the Capture, when Evidence names one, and wait for it to finish before committing.")
	}
	lines = append(lines,
		fmt.Sprintf("Commit your work on top of the start commit %s, never amend or rewrite a commit, then return "+
			"status done with commit set to the full id of HEAD and a short summary.", task.Build.Base),
		"needs_context means the work needs files or facts you do not have, named in needs_files or in summary. "+
			"blocked means the task cannot be done as written. Return one of them, with the reason in summary, when "+
			"you cannot finish.",
		"A commit that changes a file outside the owned files is sent back: take that file out of the commit, or "+
			"return needs_context naming it. When the work needs files beyond the owned list, return needs_context and "+
			"name each one in needs_files.",
	)
	return strings.Join(lines, "\n")
}

// reviewerRules returns the must and must-not rules of the epic's enabled task-stage reviewers, each as
// `<reviewer> must: <rule>` or `<reviewer> must not: <rule>`, in reviewer order.
func reviewerRules(rs []reviewers.Reviewer) []string {
	var rules []string
	for _, rev := range reviewers.ForStage(rs, reviewers.StageTask) {
		for _, rule := range rev.Must {
			rules = append(rules, rev.Name+" must: "+rule)
		}
		for _, rule := range rev.MustNot {
			rules = append(rules, rev.Name+" must not: "+rule)
		}
	}
	return rules
}

// shellWords renders argv as one shell command line: each element as it is when it holds only characters a shell
// leaves alone, and otherwise in single quotes, so the line runs the argv it names.
func shellWords(argv []string) string {
	words := make([]string, len(argv))
	for i, arg := range argv {
		words[i] = shellWord(arg)
	}
	return strings.Join(words, " ")
}

// shellWord returns arg as one shell word.
func shellWord(arg string) string {
	if arg != "" && strings.Trim(arg, plainShellChars) == "" {
		return arg
	}
	return "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
}

// plainShellChars are the characters a shell word may hold unquoted.
const plainShellChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_@%+=:,./-"

// ReviewPrompt renders the prompt of reviewer's review gate step for task T<seq> of epic: the task, the lines naming
// its spec version when it is past version 1 (taskspec.Lines), its files, its test changes, the gate-needed changes its
// implementer declared under the heading that says when each is in scope, its Requirements, and the range the change
// under review spans from base to revision. The floor reviewer's prompt ends the change with the notes the task's build
// holds and the guard's facts of the commit, facts, when they have any. The test_parsimony reviewer's prompt ends with
// the Test cost section (reviewers.TestCost) of gate, the gate check the implementer's commit passed, which is the zero
// check when the task has none, compared with baseCost, the base measurement of each package's CPU seconds
// (reviewers.Costs) when the epic has one.
func ReviewPrompt(
	epic store.EpicRow, task store.EpicTask, reviewer, base, revision string, gate store.StepCheck,
	baseCost map[string]float64, facts ...string,
) string {
	change := fmt.Sprintf(
		"The change under review is %s..%s; this worktree is checked out at %s. Inspect it with `git diff %s %s`.",
		base, revision, revision, base, revision)
	lines := slices.Concat(
		[]string{
			fmt.Sprintf("Task T%d of epic %s (%s): %s", task.Seq, epic.ID, epic.Title, task.Title),
		},
		taskspec.Lines(task),
		[]string{
			"Deliverable: " + task.Deliverable,
			"Owned files:",
		},
		bullets(task.Files),
		[]string{"Test changes:"},
		bullets(task.TestChanges),
		[]string{reviewDeclaredHeading},
		bullets(GateChangeItems(task.GateChanges)),
		[]string{"Requirements:"},
		requirementLines(epic.Requirements, task.Requirements),
		[]string{reviewEvidenceHeading},
		reviewEvidenceLines(task),
		[]string{change},
	)
	if reviewer == reviewFloor {
		lines = append(lines, floorSections(task.Notes, facts)...)
	}
	if reviewer == reviewers.TestParsimony {
		lines = append(lines, reviewers.TestCost(baseCost, gate.Stdout, gate.Stderr)...)
	}
	return strings.Join(lines, "\n")
}

// The headings of the floor reviewer's prompt's notes and guard facts.
const (
	floorNotesHeading = "Notes the task's build recorded. Each is an outcome that did not gate the build, such as a red " +
		"run whose conclusion held at the base or a reproduction that passed; weigh it as evidence about the tests:"
	floorFactsHeading = "Guard facts of the commit. Each is a change to a guarded test file that the guard does not " +
		"refuse; judge each as the mechanical floor asks:"
)

// floorSections returns the floor reviewer's prompt's sections of the notes the task's build holds and the guard's
// facts of the commit, each under its heading and only when it has items.
func floorSections(notes, facts []string) []string {
	var lines []string
	if len(notes) > 0 {
		lines = append(lines, floorNotesHeading)
		lines = append(lines, bullets(notes)...)
	}
	if len(facts) > 0 {
		lines = append(lines, floorFactsHeading)
		lines = append(lines, bullets(facts)...)
	}
	return lines
}

// GateChangeItems returns each declared gate-needed change as `<path>: <reason>`, in order.
func GateChangeItems(changes []store.GateChange) []string {
	items := make([]string, len(changes))
	for i, change := range changes {
		items[i] = change.Path + ": " + change.Reason
	}
	return items
}

// EpicDeclaredHeading heads the list of the gate-needed changes an epic's tasks declared in the prompts of its
// epic-stage reviewers and verifiers, and says when such a change is in scope.
const EpicDeclaredHeading = "Gate-needed changes the epic's tasks declared (each is in scope, though the contract " +
	"does not name it, when that task's gate or evidence could not pass without it and it is no larger than that " +
	"need):"

// EpicGateChangeItems returns the gate-needed changes each task of tasks that has not failed declared, as
// `T<seq> <path>: <reason>`, in task order and then in declared order.
func EpicGateChangeItems(tasks []store.EpicTask) []string {
	var items []string
	for _, task := range tasks {
		if task.Status == taskFailed {
			continue
		}
		for _, item := range GateChangeItems(task.GateChanges) {
			items = append(items, taskLabel(task.Seq)+" "+item)
		}
	}
	return items
}

// declaredLines returns the brief's list of the gate-needed changes the task's earlier tries declared, headed by
// declaredHeading, or nothing when they declared none.
func declaredLines(changes []store.GateChange) []string {
	if len(changes) == 0 {
		return nil
	}
	return append([]string{declaredHeading}, bullets(GateChangeItems(changes))...)
}

// bullets prefixes each item with "- ", or returns the single line "- none" when there are no items.
func bullets(items []string) []string {
	if len(items) == 0 {
		return []string{"- none"}
	}
	lines := make([]string, len(items))
	for i, item := range items {
		lines[i] = "- " + item
	}
	return lines
}

// reviewEvidenceHeading heads the evidence a task reviewer judges: the task's own evidence item, with the conclusion it
// must show.
const reviewEvidenceHeading = "Evidence (the task's own evidence and the conclusion it must show):"

// reviewEvidenceLines renders, for a task reviewer, the task's own evidence item as a bullet, or none when it has no
// conclusion. A capture renders as `T<seq>: <conclusion> (capture: <argv>)`, supplied files as `T<seq>: <conclusion>
// (supplied: <files>)`, and a closure item as `T<seq>: <conclusion> (closure: the round's closure check settles it)`.
// The reviewer judges the task's change and its tests as a whole, so the contract's evidence for each Requirement the
// task claims is not listed.
func reviewEvidenceLines(task store.EpicTask) []string {
	var items []string
	if line, ok := reviewEvidenceLine(fmt.Sprintf("T%d", task.Seq), task.Evidence); ok {
		items = append(items, line)
	}
	return bullets(items)
}

// reviewEvidenceLine renders item under id for reviewEvidenceLines, and false when item has no conclusion.
func reviewEvidenceLine(id string, item evidence.Item) (string, bool) {
	switch {
	case item.Conclusion == "":
		return "", false
	case item.Kind == evidence.KindClosure:
		return id + ": " + item.Conclusion + " (closure: the round's closure check settles it)", true
	case len(item.Capture) > 0:
		return id + ": " + item.Conclusion + " (capture: " + shellWords(item.Capture) + ")", true
	default:
		return id + ": " + item.Conclusion + " (supplied: " + strings.Join(item.Supplied, ", ") + ")", true
	}
}

// requirementLines renders the Requirements named by ids, in ids order, that epic declares. An id the epic does not
// declare renders nothing.
func requirementLines(declared []store.EpicRequirement, ids []string) []string {
	var items []string
	for _, id := range ids {
		i := slices.IndexFunc(declared, func(r store.EpicRequirement) bool { return r.ID == id })
		if i >= 0 {
			items = append(items, id+": "+declared[i].Text)
		}
	}
	return bullets(items)
}

// evidenceLines renders the conclusion of a task's evidence item, then the capture it runs or the files a person
// supplies in its place, then the line on how the evidence judge reads that capture's output or those files. A closure
// item has neither: evidenceLines renders instead the line that it runs no red and no green and that the round's
// closure check settles it, then the conclusion that check must find.
func evidenceLines(item evidence.Item) []string {
	if item.Kind == evidence.KindClosure {
		return []string{
			"Closure: this task runs no red and no green, and no evidence judge reads it. After the gate passes and " +
				"the reviewers judge your commit, the round's closure check settles it: it judges each open finding " +
				"of the round again on the integrated change.",
			"What that check must find: " + item.Conclusion,
		}
	}
	if len(item.Capture) > 0 {
		return []string{
			"Evidence: " + item.Conclusion,
			"Capture: " + shellWords(item.Capture),
			"The evidence judge reads only the Capture's exit and output, strictly: every clause of Evidence must be " +
				"visibly shown there, such as a named test the -v output shows passing.",
		}
	}
	return []string{
		"Evidence: " + item.Conclusion,
		"Supplied: " + strings.Join(item.Supplied, ", "),
		"The evidence judge reads only the Supplied files, strictly: every clause of Evidence must be visibly shown " +
			"in them.",
	}
}

// logTail returns the last redLogTailBytes bytes of log, or all of log when it is shorter.
func logTail(log []byte) []byte {
	if len(log) <= redLogTailBytes {
		return log
	}
	return log[len(log)-redLogTailBytes:]
}
