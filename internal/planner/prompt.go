// Package planner decides an epic's next tasks (O3c1).
package planner

import (
	_ "embed"
	"fmt"
	"strings"

	"github.com/joshsymonds/orrery/internal/store"
)

//go:embed role.md
var role string

// What a section says when it has no lines, and the states a turn is read by.
const (
	none         = "None."
	questionOpen = "open"
	turnFailed   = "failed"
	turnFree     = "free"
)

// Prompt renders the prompt of the planner turn turn of epic: the role text, then the contract, the decisions, the
// new notes, the open questions, the tasks, the triggers and the last failure. epic is read after the turn is claimed,
// so its Turns include turn, and triggers are the triggers turn claimed.
func Prompt(epic store.EpicRow, turn int64, triggers []store.EpicTrigger) string {
	var out strings.Builder
	fmt.Fprintln(&out, strings.TrimRight(role, "\n"))
	last, found := lastTurn(epic.Turns, turn)
	writeSection(&out, "Contract", contractLines(epic))
	writeSection(&out, "Decisions", decisionLines(epic, turn))
	writeSection(&out, "New notes", newNoteLines(epic.Decisions, last, found))
	writeSection(&out, "Open questions", questionLines(epic.Questions))
	writeSection(&out, "Tasks", taskLines(epic.Tasks))
	writeSection(&out, "Triggers", triggerLines(triggers))
	writeSection(&out, "Last failure", failureLines(last, found))
	return out.String()
}

// writeSection writes a `## ` heading, then each of lines on its own line, or None. when there are none.
func writeSection(out *strings.Builder, heading string, lines []string) {
	fmt.Fprintf(out, "\n## %s\n", heading)
	if len(lines) == 0 {
		lines = []string{none}
	}
	for _, line := range lines {
		fmt.Fprintln(out, line)
	}
}

// lastTurn returns the latest turn before turn that is not free, and whether there is one.
func lastTurn(turns []store.EpicTurn, turn int64) (store.EpicTurn, bool) {
	var last store.EpicTurn
	found := false
	for _, t := range turns {
		if t.Seq < turn && t.State != turnFree && (!found || t.Seq > last.Seq) {
			last, found = t, true
		}
	}
	return last, found
}

// contractLines returns the contract in its order: each text section as `Label: text`, each list as its label and its
// items, or as `Label: None.` when it has none.
func contractLines(epic store.EpicRow) []string {
	lines := []string{"Title: " + epic.Title, "Approved: " + epic.Approved, "Intent: " + epic.Intent}
	lines = append(lines, listLines("Failures", failureItems(epic.Failures))...)
	lines = append(lines, "Care: "+epic.Care)
	lines = append(lines, listLines("Additions", additionItems(epic.Additions))...)
	lines = append(lines, listLines("Requirements", requirementItems(epic.Requirements))...)
	lines = append(lines, listLines("Premises", premiseItems(epic.Premises))...)
	lines = append(lines, listLines("Exclusions", exclusionItems(epic.Exclusions))...)
	lines = append(lines, "Approach: "+epic.Approach)
	lines = append(lines, listLines("Alternatives", alternativeItems(epic.Alternatives))...)
	lines = append(lines, listLines("Release steps", releaseStepItems(epic.ReleaseSteps))...)
	return append(lines, "Release then: "+epic.ReleaseThen, "Checks: "+epic.Checks)
}

// listLines returns label alone above items, or label with None. when there are no items.
func listLines(label string, items []string) []string {
	if len(items) == 0 {
		return []string{label + ": " + none}
	}
	return append([]string{label + ":"}, items...)
}

// failureItems returns each failure as its id, rating and text, then its response.
func failureItems(failures []store.EpicFailure) []string {
	items := make([]string, 0, 2*len(failures))
	for _, f := range failures {
		items = append(items, fmt.Sprintf("- %s [%s]: %s", f.ID, f.Rating, f.Text), "  response: "+f.Response)
	}
	return items
}

// additionItems returns each addition as its location and what, then why and cost.
func additionItems(additions []store.EpicAddition) []string {
	items := make([]string, 0, 3*len(additions))
	for _, a := range additions {
		items = append(items, fmt.Sprintf("- %s: %s", a.Location, a.What), "  why: "+a.Why, "  cost: "+a.Cost)
	}
	return items
}

// requirementItems returns each requirement as its id and text, then its evidence and kind.
func requirementItems(requirements []store.EpicRequirement) []string {
	items := make([]string, 0, 3*len(requirements))
	for _, r := range requirements {
		items = append(items, fmt.Sprintf("- %s: %s", r.ID, r.Text), "  evidence: "+r.Evidence, "  kind: "+r.Kind)
	}
	return items
}

// premiseItems returns each premise as its id and text, then its if-wrong clause.
func premiseItems(premises []store.EpicPremise) []string {
	items := make([]string, 0, 2*len(premises))
	for _, p := range premises {
		items = append(items, fmt.Sprintf("- %s: %s", p.ID, p.Text), "  if wrong: "+p.IfWrong)
	}
	return items
}

// exclusionItems returns each exclusion as a bullet.
func exclusionItems(exclusions []string) []string {
	items := make([]string, 0, len(exclusions))
	for _, text := range exclusions {
		items = append(items, "- "+text)
	}
	return items
}

// alternativeItems returns each alternative, then why it was not taken and when to reconsider it.
func alternativeItems(alternatives []store.EpicAlternative) []string {
	items := make([]string, 0, 3*len(alternatives))
	for _, a := range alternatives {
		items = append(items, "- "+a.Alternative, "  why not: "+a.WhyNot, "  reconsider when: "+a.Reconsider)
	}
	return items
}

// releaseStepItems returns each release step, then its target and how to undo it.
func releaseStepItems(steps []store.EpicReleaseStep) []string {
	items := make([]string, 0, 3*len(steps))
	for _, s := range steps {
		items = append(items, "- "+s.Step, "  target: "+s.Target, "  undo: "+s.Undo)
	}
	return items
}

// decisionLines returns every epic decision as `- id: text`, then the decisions of each turn before turn as
// `- turn n: text`.
func decisionLines(epic store.EpicRow, turn int64) []string {
	lines := make([]string, 0, len(epic.Decisions))
	for _, d := range epic.Decisions {
		lines = append(lines, decisionLine(d))
	}
	for _, t := range epic.Turns {
		if t.Seq >= turn {
			continue
		}
		for _, text := range t.Decisions {
			lines = append(lines, fmt.Sprintf("- turn %d: %s", t.Seq, text))
		}
	}
	return lines
}

// decisionLine returns one epic decision as `- id: text`.
func decisionLine(d store.EpicDecision) string {
	return fmt.Sprintf("- %s: %s", d.ID, d.Text)
}

// newNoteLines returns the epic decisions recorded after the point last had seen. With no earlier turn that is not free
// there are none.
func newNoteLines(decisions []store.EpicDecision, last store.EpicTurn, found bool) []string {
	if !found {
		return nil
	}
	lines := make([]string, 0, len(decisions))
	for _, d := range decisions {
		if d.Seq > last.Seen {
			lines = append(lines, decisionLine(d))
		}
	}
	return lines
}

// questionLines returns each open question as `- Qseq (turn n): text`.
func questionLines(questions []store.EpicQuestion) []string {
	lines := make([]string, 0, len(questions))
	for _, q := range questions {
		if q.State == questionOpen {
			lines = append(lines, fmt.Sprintf("- Q%d (turn %d): %s", q.Seq, q.Turn, q.Text))
		}
	}
	return lines
}

// taskLines returns each task as `- Tseq [status] title`, then its deliverable, files, requirements and check.
func taskLines(tasks []store.EpicTask) []string {
	lines := make([]string, 0, 5*len(tasks))
	for _, task := range tasks {
		lines = append(lines,
			fmt.Sprintf("- T%d [%s] %s", task.Seq, task.Status, task.Title),
			"  deliverable: "+task.Deliverable,
			"  files: "+strings.Join(task.Files, ", "),
			"  requirements: "+strings.Join(task.Requirements, ", "),
			"  check: "+task.Check,
		)
	}
	return lines
}

// triggerLines returns each trigger as `- id: cause`.
func triggerLines(triggers []store.EpicTrigger) []string {
	lines := make([]string, 0, len(triggers))
	for _, trigger := range triggers {
		lines = append(lines, fmt.Sprintf("- %s: %s", trigger.ID, trigger.Cause))
	}
	return lines
}

// failureLines returns the reason of last when last failed, or None. when it did not.
func failureLines(last store.EpicTurn, found bool) []string {
	if !found || last.State != turnFailed || last.Reason == "" {
		return nil
	}
	return []string{last.Reason}
}
