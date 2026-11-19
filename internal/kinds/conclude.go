package kinds

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/joshsymonds/orrery/internal/store"
)

// Conclude is the conclude kind. Its run ends the epic once its correction rounds have run: passed, with the phase clean
// when no gap is left and ended_with_gaps when one is, and an output that names each gap. A gap is one the review left
// (Gaps), or a task that ended as a gap (TaskGaps).
type Conclude struct{}

// Decide ends the epic passed. Its phase is clean, with the stored base, when neither Gaps of the review nor TaskGaps of
// the epic's tasks finds a gap. Otherwise its phase is ended_with_gaps, and its output names the review's gaps, then the
// task gaps.
func (Conclude) Decide(_ Node, rows Rows) Action {
	gaps := slices.Concat(Gaps(rows.Review), TaskGaps(rows.Epic.Tasks))
	if len(gaps) == 0 {
		return Action{
			Kind: ActEnd, Outcome: outcomePassed, Phase: phaseClean,
			Output: fmt.Sprintf("epic %s ended clean at %s", rows.Epic.ID, rows.Epic.StoredBase),
		}
	}
	return Action{
		Kind: ActEnd, Outcome: outcomePassed, Phase: phaseEndedWithGaps,
		Output: EndingBody(phaseEndedWithGaps, gaps),
	}
}

// Gaps returns the gaps of review in order: that no review ran, each reviewer's gap verdict in verdict order,
// and then each ledger finding's gaps in ledger order. A finding gives its undetermined verifier, each undetermined
// closure in round order, and, when it is confirmed and still open after every round, the number of rounds it stayed
// open.
func Gaps(review store.Review) []string {
	var gaps []string
	if review.Frozen == "" {
		gaps = append(gaps, "no review ran")
	}
	for _, verdict := range review.Verdicts {
		if verdict.Verdict == reviewVerdictGap {
			gaps = append(gaps, fmt.Sprintf("reviewer %s: %s", verdict.Reviewer, verdict.Reason))
		}
	}
	open := OpenFindings(review, int64(len(review.Rounds))+1)
	for _, finding := range review.Ledger {
		if finding.Result == reviewResultUndetermined {
			gaps = append(gaps, fmt.Sprintf("%s: verifier undetermined: %s", finding.ID, finding.Reason))
		}
		for _, closure := range finding.Closures {
			if closure.Result == reviewResultUndetermined {
				gaps = append(gaps, fmt.Sprintf(
					"%s: closure undetermined in round %d: %s", finding.ID, closure.Round, closure.Reason,
				))
			}
		}
		isOpen := func(candidate store.LedgerFinding) bool { return candidate.ID == finding.ID }
		if slices.ContainsFunc(open, isOpen) {
			gaps = append(gaps, fmt.Sprintf("%s: open after %d rounds", finding.ID, len(review.Rounds)))
		}
	}
	return gaps
}

// TaskGaps returns a line for each task whose status is taskGap, in seq order: the task's seq, the Requirements it holds,
// and its failure. It returns nil when no task is a gap.
func TaskGaps(tasks []store.EpicTask) []string {
	var gapped []store.EpicTask
	for i := range tasks {
		if tasks[i].Status == taskGap {
			gapped = append(gapped, tasks[i])
		}
	}
	if len(gapped) == 0 {
		return nil
	}
	slices.SortFunc(gapped, func(a, b store.EpicTask) int { return cmp.Compare(a.Seq, b.Seq) })
	lines := make([]string, 0, len(gapped))
	for _, task := range gapped {
		lines = append(lines, fmt.Sprintf(
			"T%d ended as a gap holding %s: %s", task.Seq, strings.Join(task.Requirements, ", "), task.Build.Failure,
		))
	}
	return lines
}

// LedgerCause returns the cause the ledger trigger of a correction round gives the planner. Its first line names the
// round. Each line after it is an open finding the round corrects, with its fields. Round 1 with no open finding says
// that no finding was confirmed; a later round with none says that none is open.
func LedgerCause(review store.Review, round int64) string {
	open := OpenFindings(review, round)
	if len(open) == 0 {
		if round == 1 {
			return "ledger round 1: no confirmed findings; nothing to correct"
		}
		return fmt.Sprintf("ledger round %d: no open findings; nothing to correct", round)
	}
	lines := []string{fmt.Sprintf("ledger round %d: open findings to correct", round)}
	for _, finding := range open {
		lines = append(lines, fmt.Sprintf(
			"%s (%s): claim: %s; citation: %s; location: %s; evidence: %s; reason: %s",
			finding.ID, finding.Reviewer, finding.Claim, finding.Citation, finding.Location, finding.Evidence,
			finding.Reason,
		))
	}
	return strings.Join(lines, "\n")
}
