// Package planner decides an epic's next tasks (O3c1).
package planner

import (
	_ "embed"
	"encoding/json"
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
// new notes, the open questions, the advice followed, the tasks, the triggers and the last failure. epic is read after
// the turn is claimed, so its Turns include turn, and triggers are the triggers turn claimed. follows are the store's
// follow counts per label. It returns an error when a task's evidence item cannot be encoded.
func Prompt(epic store.EpicRow, turn int64, triggers []store.EpicTrigger, follows []store.LabelFollow) (string, error) {
	var out strings.Builder
	fmt.Fprintln(&out, strings.TrimRight(role, "\n"))
	last, found := lastTurn(epic.Turns, turn)
	writeSection(&out, "Contract", contractLines(epic))
	writeSection(&out, "Decisions", decisionLines(epic, turn))
	writeSection(&out, "New notes", newNoteLines(epic.Decisions, last, found))
	writeSection(&out, "Open questions", questionLines(epic.Questions))
	writeSection(&out, "Advice followed", followLines(follows))
	tasks, err := taskLines(epic.Tasks)
	if err != nil {
		return "", fmt.Errorf("tasks section: %w", err)
	}
	writeSection(&out, "Tasks", tasks)
	writeSection(&out, "Triggers", triggerLines(triggers))
	writeSection(&out, "Last failure", failureLines(last, found))
	return out.String(), nil
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

// decisionLines returns every epic decision as `- id: text`, as `- id (answers Qn): text` when it answers question n,
// or as `- id (answers Qn with choice): text` when the question was answered with a choice, then the decisions of each
// turn before turn as `- turn n: text`.
func decisionLines(epic store.EpicRow, turn int64) []string {
	choices := make(map[int64]string, len(epic.Questions))
	for _, q := range epic.Questions {
		choices[q.Seq] = q.Choice
	}
	lines := make([]string, 0, len(epic.Decisions))
	for _, d := range epic.Decisions {
		lines = append(lines, decisionLine(d, choices[d.Question]))
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

// decisionLine returns one epic decision as `- id: text`. A decision that answers question n is `- id (answers Qn):
// text`. When choice is the choice it answered with, it is `- id (answers Qn with choice): text`, which has no `: text`
// when its text is empty.
func decisionLine(decision store.EpicDecision, choice string) string {
	switch {
	case decision.Question == 0:
		return fmt.Sprintf("- %s: %s", decision.ID, decision.Text)
	case choice == "":
		return fmt.Sprintf("- %s (answers Q%d): %s", decision.ID, decision.Question, decision.Text)
	case decision.Text == "":
		return fmt.Sprintf("- %s (answers Q%d with %s)", decision.ID, decision.Question, choice)
	default:
		return fmt.Sprintf("- %s (answers Q%d with %s): %s", decision.ID, decision.Question, choice, decision.Text)
	}
}

// newNoteLines returns the notes, the epic decisions answering no question, recorded after the point last had seen.
// With no earlier turn that is not free there are none.
func newNoteLines(decisions []store.EpicDecision, last store.EpicTurn, found bool) []string {
	if !found {
		return nil
	}
	lines := make([]string, 0, len(decisions))
	for _, d := range decisions {
		if d.Seq > last.Seen && d.Question == 0 {
			lines = append(lines, decisionLine(d, ""))
		}
	}
	return lines
}

// questionLines returns each open question as its questionHead, then each declared choice as `  id: does`, then its
// recommendation as `  recommended: id (confidence): why` when it recommends one.
func questionLines(questions []store.EpicQuestion) []string {
	lines := make([]string, 0, len(questions))
	for _, question := range questions {
		if question.State != questionOpen {
			continue
		}
		lines = append(lines, questionHead(question))
		for _, choice := range question.Choices {
			lines = append(lines, fmt.Sprintf("  %s: %s", choice.ID, choice.Does))
		}
		if question.Recommended != "" {
			lines = append(lines, fmt.Sprintf("  recommended: %s (%s): %s",
				question.Recommended, question.Confidence, question.Why))
		}
	}
	return lines
}

// questionHead returns question q as `- Qseq (park): text` when it is a park question, as
// `- Qseq (turn n, label): text` when it has a label, and as `- Qseq (turn n): text` when it has none.
func questionHead(question store.EpicQuestion) string {
	switch {
	case question.Turn == 0 && question.Label == store.ParkLabel:
		return fmt.Sprintf("- Q%d (park): %s", question.Seq, question.Text)
	case question.Label == "":
		return fmt.Sprintf("- Q%d (turn %d): %s", question.Seq, question.Turn, question.Text)
	default:
		return fmt.Sprintf("- Q%d (turn %d, %s): %s", question.Seq, question.Turn, question.Label, question.Text)
	}
}

// followLines returns each label's follow counts as `- label: followed of answered answers took the recommendation`.
func followLines(follows []store.LabelFollow) []string {
	lines := make([]string, 0, len(follows))
	for _, follow := range follows {
		lines = append(lines, fmt.Sprintf("- %s: %d of %d answers took the recommendation",
			follow.Label, follow.Followed, follow.Answered))
	}
	return lines
}

// taskLines returns each task as `- Tseq [status] title`, then its deliverable, files, requirements, gate, and its
// evidence item as compact JSON.
func taskLines(tasks []store.EpicTask) ([]string, error) {
	lines := make([]string, 0, 6*len(tasks))
	for _, task := range tasks {
		evidence, err := json.Marshal(task.Evidence)
		if err != nil {
			return nil, fmt.Errorf("encode evidence of T%d: %w", task.Seq, err)
		}
		lines = append(lines,
			fmt.Sprintf("- T%d [%s] %s", task.Seq, task.Status, task.Title),
			"  deliverable: "+task.Deliverable,
			"  files: "+strings.Join(task.Files, ", "),
			"  requirements: "+strings.Join(task.Requirements, ", "),
			"  gate: "+task.Gate,
			"  evidence: "+string(evidence),
		)
	}
	return lines, nil
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
