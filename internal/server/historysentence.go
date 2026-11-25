package server

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/joshsymonds/orrery/internal/evidence"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
)

// actionMove is the action kind that moves an epic's steering from one session to another.
const actionMove = "move"

// historySentence returns the sentence of entry's row, built from the row's stored fields. No model writes one. An
// evidence entry's sentence names each run it carries (see evidenceSentence).
func historySentence(entry *orreryv1.EpicHistoryEntry) string {
	switch {
	case entry.GetTurn() != nil:
		return turnSentence(entry.GetTurn())
	case entry.GetTrigger() != nil:
		return triggerSentence(entry.GetTrigger())
	case entry.GetStep() != nil:
		return stepSentence(entry.GetStep())
	case entry.GetEvidence() != nil:
		return evidenceSentence(entry)
	case entry.GetDecision() != nil:
		return decisionSentence(entry.GetDecision())
	case entry.GetTell() != nil:
		return tellSentence(entry.GetTell())
	case entry.GetAction() != nil:
		return actionSentence(entry.GetAction())
	}
	return ""
}

// turnSentence names a turn by its seq and state, then its outcome, or its reason when it has none: "Planner turn 3
// succeeded: work".
func turnSentence(turn *orreryv1.EpicTurn) string {
	head := fmt.Sprintf("Planner turn %d %s", turn.GetSeq(), turn.GetState())
	detail := turn.GetOutcome()
	if detail == "" {
		detail = turn.GetReason()
	}
	return withDetail(head, detail)
}

// triggerSentence names a trigger by its id and gives its cause: "Trigger trig-1: owner asked".
func triggerSentence(trigger *orreryv1.EpicTrigger) string {
	return withDetail("Trigger "+trigger.GetId(), trigger.GetCause())
}

// stepSentence names a step by its label (see stepLabel). While the step runs it gives the doing line; once it has
// ended it gives how it ended and its summary: "Write the parser running: editing lexer.go", "One-a ended done: all
// green".
func stepSentence(step *orreryv1.EpicStep) string {
	if step.GetEnd() == "" {
		return withDetail(stepLabel(step)+" running", step.GetDoing())
	}
	return withDetail(stepLabel(step)+" ended "+step.GetEnd(), step.GetSummary())
}

// stepLabel returns what a step is called: its purpose, else its task, else its role, else its id. The first letter of
// the label is capitalized.
func stepLabel(step *orreryv1.EpicStep) string {
	for _, label := range []string{step.GetPurpose(), step.GetTask(), step.GetRole()} {
		if label != "" {
			return upperFirst(label)
		}
	}
	return "Step " + step.GetId()
}

// evidenceSentence names the phase of an evidence entry's runs, then each run's requirement and its result:
// "Red evidence: R1 failed at base, as expected; R2 holds at base". The phase is the entry's evidence run's, which the
// runs share.
func evidenceSentence(entry *orreryv1.EpicHistoryEntry) string {
	head := "Evidence"
	if phase := entry.GetEvidence().GetPhase(); phase != "" {
		head = upperFirst(phase) + " evidence"
	}
	clauses := make([]string, 0, len(entry.GetRuns()))
	for _, run := range entry.GetRuns() {
		clauses = append(clauses, runClause(run))
	}
	return head + ": " + strings.Join(clauses, "; ")
}

// runClause names a run's requirement, or its id when the run names none, and gives its result.
func runClause(run *orreryv1.EvidenceRun) string {
	item := run.GetItem()
	if item == "" {
		item = run.GetId()
	}
	return item + " " + runResult(run)
}

// runResult returns what a run's verdict says of its requirement. A red run passes when its conclusion fails, so a red
// run whose verdict is fails failed at base as expected; a red run whose verdict is holds holds at base. A run whose
// judge gave no verdict is not judged. A green run's result is its verdict.
func runResult(run *orreryv1.EvidenceRun) string {
	verdict := run.GetVerdict()
	switch {
	case verdict == "":
		return "not judged"
	case run.GetPhase() != evidence.PhaseRed:
		return verdict
	case verdict == evidence.VerdictFails:
		return "failed at base, as expected"
	default:
		return verdict + " at base"
	}
}

// decisionSentence gives a decision's text and who made it: "Decision by owner: use the parser". A decision stored with
// no maker names none.
func decisionSentence(decision *orreryv1.EpicDecision) string {
	head := "Decision"
	if by := decision.GetBy(); by != "" {
		head = "Decision by " + by
	}
	return withDetail(head, decision.GetText())
}

// tellSentence gives a tell's text, and whether the owner has been told it: "Told the owner: the parser is in" or
// "To tell the owner: review pending".
func tellSentence(tell *orreryv1.EpicTell) string {
	if tell.GetTold() {
		return withDetail("Told the owner", tell.GetText())
	}
	return withDetail("To tell the owner", tell.GetText())
}

// actionSentence names who took an action and what it did. A move names the sessions the epic moved from and to:
// "Owner moved the epic from session a to session b". Whoever took an action the store names no maker of is someone.
func actionSentence(action *orreryv1.EpicAction) string {
	actor := action.GetBy()
	if actor == "" {
		actor = "someone"
	}
	actor = upperFirst(actor)
	if action.GetKind() != actionMove {
		return actor + " did " + action.GetKind()
	}
	sentence := actor + " moved the epic"
	if from := action.GetFrom(); from != "" {
		sentence += " from session " + from
	}
	if to := action.GetTo(); to != "" {
		sentence += " to session " + to
	}
	return sentence
}

// withDetail returns head, then detail after a colon when detail is not empty.
func withDetail(head, detail string) string {
	if detail == "" {
		return head
	}
	return head + ": " + detail
}

// upperFirst returns s with its first letter capitalized.
func upperFirst(s string) string {
	first, size := utf8.DecodeRuneInString(s)
	if size == 0 {
		return s
	}
	return string(unicode.ToUpper(first)) + s[size:]
}
