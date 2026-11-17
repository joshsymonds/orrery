package build

import (
	_ "embed"
	"fmt"
	"slices"
	"strings"

	"github.com/joshsymonds/orrery/internal/evidence"
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

// Brief renders the prompt of an implementer step for task T<seq> of epic: the task, its Gate and evidence, the
// Requirements it covers, the epic's exclusions and decisions, the red run's exit and verdict with the end of its
// stdout log and, when redStderr is not empty, the end of its stderr log, the previous attempt's failure when failure
// is set, and the closing instruction to commit once.
func Brief(
	epic store.EpicRow, task store.EpicTask, red store.EvidenceRun, redLog, redStderr []byte, failure string,
) string {
	lines := slices.Concat(
		[]string{
			fmt.Sprintf(
				"You implement task T%d of epic %s (%s) in this worktree, checked out detached at the task's base.",
				task.Seq,
				epic.ID,
				epic.Title,
			),
			"Task: " + task.Title,
			"Deliverable: " + task.Deliverable,
			"Change only these files:",
		},
		bullets(task.Files),
		[]string{fmt.Sprintf("Gate: %s (orreryd runs it on your commit; it must pass)", task.Gate)},
		evidenceLines(task.Evidence),
		[]string{"Requirements:"},
		requirementLines(epic.Requirements, task.Requirements),
		[]string{"Exclusions:"},
		bullets(epic.Exclusions),
		[]string{"Decisions:"},
		bullets(decisionTexts(epic)),
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
		lines = append(lines, "Your previous attempt failed: "+failure)
	}
	lines = append(lines, "Commit once, then return status done with commit set to the full id of HEAD and a short "+
		"summary. Return needs_context or blocked, with the reason in summary, when you cannot. When the work needs "+
		"files beyond the owned list, return needs_context and name each one in needs_files.")
	return strings.Join(lines, "\n")
}

// ReviewPrompt renders the prompt of a review gate's step for task T<seq> of epic: the task, its files and
// Requirements, and the range the change under review spans from base to revision.
func ReviewPrompt(epic store.EpicRow, task store.EpicTask, base, revision string) string {
	change := fmt.Sprintf(
		"The change under review is %s..%s; this worktree is checked out at %s. Inspect it with `git diff %s %s`.",
		base, revision, revision, base, revision)
	return strings.Join(slices.Concat(
		[]string{
			fmt.Sprintf("Task T%d of epic %s (%s): %s", task.Seq, epic.ID, epic.Title, task.Title),
			"Deliverable: " + task.Deliverable,
			"Files:",
		},
		bullets(task.Files),
		[]string{"Requirements:"},
		requirementLines(epic.Requirements, task.Requirements),
		[]string{change},
	), "\n")
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

// decisionTexts returns the epic's decisions, then each turn's decisions in turn order.
func decisionTexts(epic store.EpicRow) []string {
	var texts []string
	for _, decision := range epic.Decisions {
		texts = append(texts, decision.Text)
	}
	for _, turn := range epic.Turns {
		texts = append(texts, turn.Decisions...)
	}
	return texts
}

// evidenceLines renders the conclusion of a task's evidence item, then the capture it runs or the files a person
// supplies in its place.
func evidenceLines(item evidence.Item) []string {
	if len(item.Capture) > 0 {
		return []string{"Evidence: " + item.Conclusion, "Capture: " + strings.Join(item.Capture, " ")}
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
