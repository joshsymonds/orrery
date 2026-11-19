package kinds

import (
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
// task gaps. Either output then names each finding an owner's answer closed (Decided).
func (Conclude) Decide(_ Node, rows Rows) Action {
	gaps, decided := slices.Concat(Gaps(rows.Review), TaskGaps(rows.Epic.Tasks)), Decided(rows.Review)
	if len(gaps) == 0 {
		lines := append([]string{fmt.Sprintf("epic %s ended clean at %s", rows.Epic.ID, rows.Epic.StoredBase)},
			decidedLines(decided)...)
		return Action{Kind: ActEnd, Outcome: outcomePassed, Phase: phaseClean, Output: strings.Join(lines, "\n")}
	}
	return Action{
		Kind: ActEnd, Outcome: outcomePassed, Phase: phaseEndedWithGaps,
		Output: EndingBody(phaseEndedWithGaps, gaps, decided),
	}
}

// Decided returns the findings of review that an owner's answer closed, in ledger order, each as `<id> in round <n>`
// with the round whose closure it closed. Such a finding is no gap: the owner settled it with no code change.
func Decided(review store.Review) []string {
	var decided []string
	for _, finding := range review.Ledger {
		for _, closure := range finding.Closures {
			if closedByDecision(closure) {
				decided = append(decided, fmt.Sprintf("%s in round %d", finding.ID, closure.Round))
			}
		}
	}
	return decided
}

// decidedLines returns the lines that name the decided findings: a `Closed by decision:` line, then one `- finding`
// line each, or none when there are none.
func decidedLines(decided []string) []string {
	if len(decided) == 0 {
		return nil
	}
	lines := make([]string, 0, len(decided)+1)
	lines = append(lines, "Closed by decision:")
	for _, finding := range decided {
		lines = append(lines, "- "+finding)
	}
	return lines
}

// Gaps returns the reasons of the gaps of review that GapList lists before the task gaps, in GapList's order: that no
// review ran, each reviewer's gap verdict, and then each ledger finding's undetermined verifier, undetermined closures
// and, when it is still open after every round, the rounds it stayed open.
func Gaps(review store.Review) []string {
	return gapReasons(reviewGaps(review))
}

// TaskGaps returns the reasons of the task gaps GapList lists, in seq order: each task whose status is taskGap, with
// the Requirements it holds and its failure. It returns nil when no task is a gap.
func TaskGaps(tasks []store.EpicTask) []string {
	return gapReasons(taskGaps(tasks))
}

// LedgerCause returns the cause the ledger trigger of a correction round gives the planner. Its first line names the
// round. Each line after it is an open finding the round corrects, with its fields. Then each review gap of GapList
// that is not an open one follows as `gap <key>: <reason>`. A round with no open finding but a gap says that the gaps
// are to reconcile. A round with neither says that no finding was confirmed in round 1, and that none is open in a
// later round.
func LedgerCause(review store.Review, round int64) string {
	open := OpenFindings(review, round)
	var gaps []Gap
	for _, gap := range GapList(review, nil) {
		if !strings.HasPrefix(gap.Key, "open:") {
			gaps = append(gaps, gap)
		}
	}
	if len(open) == 0 && len(gaps) == 0 {
		if round == 1 {
			return "ledger round 1: no confirmed findings; nothing to correct"
		}
		return fmt.Sprintf("ledger round %d: no open findings; nothing to correct", round)
	}
	var lines []string
	if len(open) == 0 {
		lines = []string{fmt.Sprintf("ledger round %d: no open findings; gaps to reconcile", round)}
	} else {
		lines = []string{fmt.Sprintf("ledger round %d: open findings to correct", round)}
		for _, finding := range open {
			lines = append(lines, fmt.Sprintf(
				"%s (%s): claim: %s; citation: %s; location: %s; evidence: %s; reason: %s",
				finding.ID, finding.Reviewer, finding.Claim, finding.Citation, finding.Location, finding.Evidence,
				finding.Reason,
			))
		}
	}
	for _, gap := range gaps {
		lines = append(lines, fmt.Sprintf("gap %s: %s", gap.Key, gap.Reason))
	}
	return strings.Join(lines, "\n")
}
