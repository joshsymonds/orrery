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
)

// ImplementerSchema is the schema an implementer step's return must match: its status, a summary, the commit it made,
// and the files it names as needed beyond its owned list.
//
//go:embed implementer.json
var ImplementerSchema string

// redLogTailBytes is how many bytes of each of a red run's logs the brief carries: the end of the log, where the
// failure is.
const redLogTailBytes = 8192

// Brief renders the prompt of an implementer step for task T<seq> of epic: the task, the standing rules a commit is
// rejected for, its Deliverable and files, with the rule for a file beyond them, its test changes and the guard's
// rules, its gate with the command orreryd runs and what happens when it fails, its evidence, the Requirements it
// covers, the epic's exclusions, the rules the epic's task-stage reviewers judge by, the red run's exit and verdict
// with the end of its stdout log and, when redStderr is not empty, the end of its stderr log, the note from the
// previous try when failure is set, and the closing instructions: continue from the worktree's earlier work, finish
// every command before returning, run the Capture, commit on top of the start commit, and what each return status
// means. The brief names no epic, turn or owner decision: the Deliverable carries every decision the implementer must
// follow.
func Brief(
	epic store.EpicRow, task store.EpicTask, gate gates.Gate, red store.EvidenceRun, redLog, redStderr []byte,
	failure string,
) string {
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
		bullets([]string{
			"changing anything the Deliverable does not ask for, including tidying, reformatting, or fixing code or " +
				"tests that are already broken; this overrides any standing instruction to fix what you find",
			"editing, renaming, or deleting an existing test the Deliverable does not name",
			"a test that repeats an existing assertion or passes with the change removed",
			"a real timer or clock in a test",
			"an Evidence clause left unproved",
			"dead code, an unhandled error, a skip, or a suppressed check",
		}),
		[]string{
			"Deliverable: " + task.Deliverable,
			"Change only these files:",
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
			"Outside those test changes, orreryd's guard refuses a commit that deletes or renames a guarded test " +
				"file, removes a test present at the base, lowers a test's assertions, adds a skip, or changes a file " +
				"a capture runs.",
			fmt.Sprintf("Gate: %s. After you return done, orreryd runs `%s` on your commit. Do not run the gate "+
				"yourself. When it fails, orreryd sends you its output: fix the cause, commit the fix on top, and return "+
				"done again.", task.Gate, shellWords(gate.Argv)),
		},
		evidenceLines(task.Evidence),
		[]string{"Requirements:"},
		requirementLines(epic.Requirements, task.Requirements),
		[]string{"Exclusions:"},
		bullets(epic.Exclusions),
		[]string{"After the gate passes, reviewers judge your commit by these rules:"},
		bullets(reviewerRules(epic.Reviewers)),
		[]string{
			fmt.Sprintf("Red at the base: exit %d, verdict %s", red.Exit, red.Verdict),
			"Red log (last 8 KiB):",
			"```",
			string(logTail(redLog)),
			"```",
		},
	)
	if len(redStderr) > 0 {
		lines = append(lines, "Red stderr (last 8 KiB):", "```", string(logTail(redStderr)), "```")
	}
	if failure != "" {
		lines = append(lines, "From your previous try on this task: "+failure)
	}
	lines = append(lines,
		fmt.Sprintf("This worktree keeps any earlier work on this task. Read `git status` and "+
			"`git log --oneline %s..HEAD` first, and continue from what is there.", task.Build.Base),
		"Finish every command you start before you return: run nothing in the background and wait on no monitor. "+
			"Ending your turn is your return.",
		"Run the Capture, when Evidence names one, and wait for it to finish before committing.",
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

// ReviewPrompt renders the prompt of reviewer's review gate step for task T<seq> of epic: the task, its files, its test
// changes and Requirements, and the range the change under review spans from base to revision. The test_parsimony
// reviewer's prompt ends with the Test cost section (reviewers.TestCost) of gate, the gate check the implementer's
// commit passed, which is the zero check when the task has none.
func ReviewPrompt(
	epic store.EpicRow, task store.EpicTask, reviewer, base, revision string, gate store.StepCheck,
) string {
	change := fmt.Sprintf(
		"The change under review is %s..%s; this worktree is checked out at %s. Inspect it with `git diff %s %s`.",
		base, revision, revision, base, revision)
	lines := slices.Concat(
		[]string{
			fmt.Sprintf("Task T%d of epic %s (%s): %s", task.Seq, epic.ID, epic.Title, task.Title),
			"Deliverable: " + task.Deliverable,
			"Files:",
		},
		bullets(task.Files),
		[]string{"Test changes:"},
		bullets(task.TestChanges),
		[]string{"Requirements:"},
		requirementLines(epic.Requirements, task.Requirements),
		[]string{change},
	)
	if reviewer == reviewers.TestParsimony {
		lines = append(lines, reviewers.TestCost(gate.Stdout, gate.Stderr)...)
	}
	return strings.Join(lines, "\n")
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
// supplies in its place.
func evidenceLines(item evidence.Item) []string {
	if len(item.Capture) > 0 {
		return []string{"Evidence: " + item.Conclusion, "Capture: " + shellWords(item.Capture)}
	}
	return []string{"Evidence: " + item.Conclusion, "Supplied: " + strings.Join(item.Supplied, ", ")}
}

// logTail returns the last redLogTailBytes bytes of log, or all of log when it is shorter.
func logTail(log []byte) []byte {
	if len(log) <= redLogTailBytes {
		return log
	}
	return log[len(log)-redLogTailBytes:]
}
