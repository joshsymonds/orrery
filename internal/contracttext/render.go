// Package contracttext renders an accepted epic contract as the design packet lays one out, and holds the Quality Bar
// that every contract's checks carry verbatim. It imports only the generated engine types and the standard library, so
// the engine host and the contract stage can both use it.
package contracttext

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
)

// QualityBar is the fixed Quality Bar every contract's checks carry verbatim. It is the same for every epic.
const QualityBar = "Failing, low-quality, or bad code is unacceptable, but failure to meet a mythic platonic ideal " +
	"of code or cover literally every imaginable edge case is NOT itself a defect. This bar is FIXED " +
	"for every epic; write it verbatim — never elicit it, strengthen it, or make it a per-project " +
	"preference. A defect is exactly one of: a Requirement's named evidence not met; a Must Not Ship " +
	"entry present; a change outside the task's owned files; a change the contract did not ask for; " +
	"a violation of the implementer contract's mechanical floor (a suppressed check, a weakened or " +
	"tautological test, dead code, an unhandled error); an assertion its declared gate finds does " +
	"not hold; or a security or data-loss failure with a reachable precondition that the change " +
	"itself introduces. Everything else the orchestrator or a reviewer notices is an observation — " +
	"it may be recorded in the Decision Log and the report; it never becomes work during the run. " +
	"The craftsmanship asked of the implementer is one line: simple, foundational, secure; match the " +
	"surrounding code."

// qualityBarLine is the line a contract's checks carry for the Quality Bar.
const qualityBarLine = "Quality Bar: " + QualityBar

// checksHeading opens the checks the machines run, the last section of a rendered contract.
const checksHeading = "## Checks the machines run"

// flat makes a text one table cell or list item: its newlines become spaces and its pipes are escaped, so it cannot end
// the cell.
func flat(text string) string {
	return strings.NewReplacer("\r\n", " ", "\n", " ", "|", `\|`).Replace(text)
}

// QualityBarLine reports whether one line of checks is exactly the Quality Bar line.
func QualityBarLine(checks string) bool {
	return slices.Contains(strings.Split(checks, "\n"), qualityBarLine)
}

// Render renders a contract as the design packet lays one out: its title and approval, its sections in order, and last
// the checks the machines run. decisions are the decisions the packet asks the owner to answer; none reads None.
func Render(contract *v1.EpicContract, decisions []*v1.ContractDecision) string {
	return aboveChecks(contract, decisions) + checksHeading + "\n" + contract.GetChecks() + "\n"
}

// LinesAboveChecks counts the lines Render writes before the checks heading.
func LinesAboveChecks(contract *v1.EpicContract, decisions []*v1.ContractDecision) int {
	return strings.Count(aboveChecks(contract, decisions), "\n")
}

// aboveChecks renders the sections before the checks, each ending in a blank line.
func aboveChecks(contract *v1.EpicContract, decisions []*v1.ContractDecision) string {
	var doc strings.Builder
	fmt.Fprintf(&doc, "# %s\nApproved: %s\n\n", contract.GetTitle(), contract.GetApproved())

	fmt.Fprintf(&doc, "## What you asked for\n%s\n\n", contract.GetIntent())

	doc.WriteString("## What could go wrong, and how much we care\n")
	failures := tableRows(contract.GetFailures(), func(_ int, f *v1.EpicFailure) []string {
		return []string{f.GetId() + " " + f.GetText(), f.GetRating(), f.GetResponse()}
	})
	writeTable(&doc, []string{"If this happened", "How bad", "What we do"}, failures)
	fmt.Fprintf(&doc, "%s\n\n", careLine(contract))

	doc.WriteString("## Things you did not ask for\n")
	if len(contract.GetAdditions()) == 0 {
		doc.WriteString("None.\n\n")
	} else {
		additions := tableRows(contract.GetAdditions(), func(_ int, a *v1.EpicAddition) []string {
			return []string{a.GetLocation(), a.GetWhat(), a.GetWhy(), a.GetCost()}
		})
		writeTable(&doc, []string{"Where", "What", "Why", "Cost"}, additions)
	}

	doc.WriteString("## What will be true when done\n")
	requirements := tableRows(contract.GetRequirements(), func(_ int, r *v1.EpicRequirement) []string {
		return []string{r.GetId() + " " + r.GetText(), evidenceText(r.GetItem())}
	})
	writeTable(&doc, []string{"Must be true", "How we'll know"}, requirements)

	doc.WriteString("## What I'm assuming\n")
	premises := tableRows(contract.GetPremises(), func(_ int, p *v1.EpicPremise) []string {
		return []string{p.GetId() + " " + p.GetText(), p.GetIfWrong()}
	})
	writeTable(&doc, []string{"Assumption", "If wrong"}, premises)

	doc.WriteString("## What we won't do\n")
	for _, exclusion := range contract.GetExclusions() {
		doc.WriteString("- " + flat(exclusion) + "\n")
	}
	doc.WriteString("\n")

	doc.WriteString("## How, and why not the other ways\n")
	fmt.Fprintf(&doc, "%s\n\n", contract.GetApproach())
	alternatives := tableRows(contract.GetAlternatives(), func(_ int, a *v1.EpicAlternative) []string {
		return []string{a.GetAlternative(), a.GetWhyNot(), a.GetReconsider()}
	})
	writeTable(&doc, []string{"Alternative", "Why not", "Reconsider when"}, alternatives)

	doc.WriteString("## What leaves this machine or can't be undone\n")
	steps := tableRows(contract.GetReleaseSteps(), func(i int, s *v1.EpicReleaseStep) []string {
		return []string{strconv.Itoa(i+1) + " " + s.GetStep(), s.GetTarget(), s.GetUndo()}
	})
	writeTable(&doc, []string{"Step", "Target", "Undo"}, steps)
	fmt.Fprintf(&doc, "Then: %s\n\n", contract.GetReleaseThen())

	doc.WriteString("## Decisions I need from you\n")
	if len(decisions) == 0 {
		doc.WriteString("None.\n\n")
	} else {
		asked := tableRows(decisions, func(_ int, d *v1.ContractDecision) []string {
			return []string{decisionText(d), d.GetRecommendation(), d.GetReason()}
		})
		writeTable(&doc, []string{"Decision", "Recommended", "Why"}, asked)
	}
	return doc.String()
}

// careLine is the level of care the contract works at, set by the first failure rated at that level when one is.
func careLine(contract *v1.EpicContract) string {
	for _, f := range contract.GetFailures() {
		if f.GetRating() == contract.GetCare() {
			return "Level of care: " + contract.GetCare() + ", set by " + f.GetId()
		}
	}
	return "Level of care: " + contract.GetCare()
}

// evidenceText is the evidence a requirement shows: the command its capture runs, then the conclusion the judge checks
// the capture against. Supplied evidence has no capture, so only its conclusion is written.
func evidenceText(item *v1.EvidenceItem) string {
	capture := strings.Join(item.GetCapture(), " ")
	if capture == "" {
		return item.GetConclusion()
	}
	return capture + ": " + item.GetConclusion()
}

// decisionText is a decision's id and question, with its options when it has any.
func decisionText(decision *v1.ContractDecision) string {
	text := decision.GetId() + " " + decision.GetQuestion()
	if len(decision.GetOptions()) > 0 {
		text += " Options: " + strings.Join(decision.GetOptions(), ", ")
	}
	return text
}

// tableRows maps each item to its table row, given the item's index.
func tableRows[T any](items []T, row func(int, T) []string) [][]string {
	out := make([][]string, 0, len(items))
	for i, item := range items {
		out = append(out, row(i, item))
	}
	return out
}

// writeTable writes a markdown table: its head, its separator and its rows, then a blank line.
func writeTable(doc *strings.Builder, head []string, rows [][]string) {
	writeRow(doc, head)
	doc.WriteString("|" + strings.Repeat("---|", len(head)) + "\n")
	for _, row := range rows {
		writeRow(doc, row)
	}
	doc.WriteString("\n")
}

// writeRow writes one table row, each cell flattened onto its line.
func writeRow(doc *strings.Builder, cells []string) {
	doc.WriteString("|")
	for _, cell := range cells {
		doc.WriteString(" " + flat(cell) + " |")
	}
	doc.WriteString("\n")
}
