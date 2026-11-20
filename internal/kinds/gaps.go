package kinds

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/joshsymonds/orrery/internal/store"
)

// Gap is one gap an epic ends with or a correction round reconciles. Key names what the gap is: `review` for a review
// that ran no review, `reviewer:<name>` for a reviewer's gap verdict, `verify:<finding>` for an undetermined verifier,
// `closure:<finding>:<round>` for an undetermined closure, `open:<finding>` for a finding still open after every round,
// and `task:T<seq>` for a task that ended as a gap. Reason is the line that names the gap.
type Gap struct{ Key, Reason string }

// GapList returns the gaps of review, in the order Gaps reports them, and then the task gaps of tasks, in the order
// TaskGaps reports them.
func GapList(review store.Review, tasks []store.EpicTask) []Gap {
	return append(reviewGaps(review), taskGaps(tasks)...)
}

// reviewGaps returns the gaps of review in order: that no review ran, each reviewer's gap verdict in verdict order,
// and then each ledger finding's gaps in ledger order. A finding gives its undetermined verifier, each undetermined
// closure in round order, and, when it is confirmed and still open after every round, the number of rounds it stayed
// open.
func reviewGaps(review store.Review) []Gap {
	var gaps []Gap
	if review.Frozen == "" {
		gaps = append(gaps, Gap{Key: "review", Reason: "no review ran"})
	}
	for _, verdict := range review.Verdicts {
		if verdict.Verdict == reviewVerdictGap {
			gaps = append(gaps, Gap{
				Key:    "reviewer:" + verdict.Reviewer,
				Reason: fmt.Sprintf("reviewer %s: %s", verdict.Reviewer, verdict.Reason),
			})
		}
	}
	open := OpenFindings(review, int64(len(review.Rounds))+1)
	for _, finding := range review.Ledger {
		if finding.Result == reviewResultUndetermined {
			gaps = append(gaps, Gap{
				Key:    "verify:" + finding.ID,
				Reason: fmt.Sprintf("%s: verifier undetermined: %s", finding.ID, finding.Reason),
			})
		}
		for _, closure := range finding.Closures {
			if closure.Result == reviewResultUndetermined {
				gaps = append(gaps, Gap{
					Key: fmt.Sprintf("closure:%s:%d", finding.ID, closure.Round),
					Reason: fmt.Sprintf(
						"%s: closure undetermined in round %d: %s", finding.ID, closure.Round, closure.Reason,
					),
				})
			}
		}
		isOpen := func(candidate store.LedgerFinding) bool { return candidate.ID == finding.ID }
		if slices.ContainsFunc(open, isOpen) {
			gaps = append(gaps, Gap{
				Key:    "open:" + finding.ID,
				Reason: fmt.Sprintf("%s: open after %d rounds", finding.ID, len(review.Rounds)),
			})
		}
	}
	return gaps
}

// taskGaps returns a gap for each task whose status is taskGap, in seq order: the task's seq, the Requirements it
// holds, and its failure. It returns nil when no task is a gap.
func taskGaps(tasks []store.EpicTask) []Gap {
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
	gaps := make([]Gap, 0, len(gapped))
	for _, task := range gapped {
		gaps = append(gaps, Gap{
			Key: fmt.Sprintf("task:T%d", task.Seq),
			Reason: fmt.Sprintf(
				"T%d ended as a gap holding %s: %s",
				task.Seq, strings.Join(task.Requirements, ", "), task.Build.Failure,
			),
		})
	}
	return gaps
}

// gapReasons returns the reason of each gap in order, or nil when there is no gap.
func gapReasons(gaps []Gap) []string {
	if len(gaps) == 0 {
		return nil
	}
	reasons := make([]string, 0, len(gaps))
	for _, gap := range gaps {
		reasons = append(reasons, gap.Reason)
	}
	return reasons
}

// GapCause returns the cause the trigger of gap gives the planner. Its first line is `gap <key>: <reason>`. Then come a
// `verdicts:` line and one `- <reviewer>: <verdict>` line per verdict of review, with `: <reason>` when the verdict has
// a reason. Then come a `ledger:` line and, per ledger finding, a `- <id> (<reviewer>): <result>: claim: <claim>;
// reason: <reason>` line followed by one `  closure round <n>: <result>: <reason>` line per closure. A ledger with no
// finding gives `ledger: empty` in place of those lines.
func GapCause(gap Gap, review store.Review) string {
	lines := []string{fmt.Sprintf("gap %s: %s", gap.Key, gap.Reason), "verdicts:"}
	for _, verdict := range review.Verdicts {
		line := fmt.Sprintf("- %s: %s", verdict.Reviewer, verdict.Verdict)
		if verdict.Reason != "" {
			line += ": " + verdict.Reason
		}
		lines = append(lines, line)
	}
	if len(review.Ledger) == 0 {
		return strings.Join(append(lines, "ledger: empty"), "\n")
	}
	lines = append(lines, "ledger:")
	for _, finding := range review.Ledger {
		lines = append(lines, fmt.Sprintf(
			"- %s (%s): %s: claim: %s; reason: %s",
			finding.ID, finding.Reviewer, finding.Result, finding.Claim, finding.Reason,
		))
		for _, closure := range finding.Closures {
			lines = append(lines, fmt.Sprintf(
				"  closure round %d: %s: %s", closure.Round, closure.Result, closure.Reason,
			))
		}
	}
	return strings.Join(lines, "\n")
}
