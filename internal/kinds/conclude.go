package kinds

import (
	"fmt"
	"strings"

	"github.com/joshsymonds/orrery/internal/store"
)

// ActGapRound is the action a conclusion gives while a gap is live: a gap round, which gives each gap of Action.Gaps
// a planner turn.
const ActGapRound ActionKind = "gap_round"

// Conclude is the conclude kind. Its run ends the epic once no gap is live: passed, with the phase clean when the
// review and the epic's tasks leave no gap, and ended_with_gaps otherwise, with an output that names each gap and a
// question the owner answers. A gap is one the review left (GapList), or a task that ended as a gap. While a gap is
// live, the run gives a gap round, which gives each live gap a planner turn.
type Conclude struct{}

// Decide ends the epic passed, or asks for a gap round. With no gap in allGaps, its phase is clean, with the stored
// base. Otherwise a gap is live unless it was declared, or it has had store.GapTurnBound planner turns. While a gap
// is live the run gives a gap round of the live gaps, in allGaps order, and a paused or parked epic gives none. Once
// no gap is live, its phase is ended_with_gaps, and its output and question are GapEnding's. Either output then names
// each finding an owner's answer closed (Decided).
func (Conclude) Decide(_ Node, rows Rows) Action {
	gaps, decided := allGaps(rows), Decided(rows.Review)
	if len(gaps) == 0 {
		lines := append([]string{fmt.Sprintf("epic %s ended clean at %s", rows.Epic.ID, rows.Epic.StoredBase)},
			decidedLines(decided)...)
		return Action{Kind: ActEnd, Outcome: outcomePassed, Phase: phaseClean, Output: strings.Join(lines, "\n")}
	}
	if live := liveGaps(gaps, rows); len(live) > 0 {
		if rows.Epic.Paused || rows.Epic.Parked != "" {
			return Action{Kind: ActNone}
		}
		return Action{Kind: ActGapRound, Gaps: live}
	}
	body, question := GapEnding(rows)
	return Action{Kind: ActEnd, Outcome: outcomePassed, Phase: phaseEndedWithGaps, Output: body, Question: question}
}

// allGaps returns the gaps the epic of rows ends with: those of GapList, then the sync gap SyncGap reads, when a sync
// at the stored base failed. GapList itself does not count the sync gap.
func allGaps(rows Rows) []Gap {
	gaps := GapList(rows.Review, rows.Epic.Tasks)
	if gap, failed := SyncGap(rows); failed {
		gaps = append(gaps, gap)
	}
	return gaps
}

// gapDeclarations returns the declarations gap is read against: a sync gap's are the sync declarations of rows, which
// every succeeded turn of the epic may have made, and every other gap's are the latest succeeded turn's unresolvable
// gaps.
func gapDeclarations(gap Gap, rows Rows) []store.GapDeclaration {
	if strings.HasPrefix(gap.Key, SyncTriggerPrefix) {
		return rows.SyncDeclarations
	}
	return rows.Unresolvable
}

// liveGaps returns the gaps of gaps that rows leave live, in order: each gap that was not declared, and that has had
// fewer than store.GapTurnBound planner turns.
func liveGaps(gaps []Gap, rows Rows) []Gap {
	var live []Gap
	for _, gap := range gaps {
		if _, declared := declaredBy(gap.Key, gapDeclarations(gap, rows)); declared {
			continue
		}
		if GapTurnCount(gap.Key, rows.GapTurns) < store.GapTurnBound {
			live = append(live, gap)
		}
	}
	return live
}

// GapTurnCount returns how many planner turns the gap with key was given, among turns.
func GapTurnCount(key string, turns []store.GapTurn) int {
	count := 0
	for _, turn := range turns {
		if turn.Gap == key {
			count++
		}
	}
	return count
}

// declaredBy returns the declaration among declarations that names the gap with key, and whether there is one.
func declaredBy(key string, declarations []store.GapDeclaration) (store.GapDeclaration, bool) {
	for _, declaration := range declarations {
		if declaration.Gap == key {
			return declaration, true
		}
	}
	return store.GapDeclaration{}, false
}

// GapEnding returns the body and the gaps question of an ending with gaps. The body is EndingBody of ended_with_gaps
// over the reason of each gap of allGaps and the decided findings. The question has one entry per gap, in allGaps
// order: the gap's reason, then ` (` and its parts joined with `; ` and `)` when it has parts. The parts are
// `planner: <reason>` when the gap was declared, then `turn <n>: <reason>` for each planner turn the gap was given. The
// question recommends release when every gap is declared with release, and otherwise correct for the gaps not declared
// with release.
func GapEnding(rows Rows) (string, store.GapsQuestion) {
	gaps := allGaps(rows)
	question := store.GapsQuestion{
		Recommended: store.ChoiceRelease, Why: "the planner recommends release for every gap",
	}
	var correct []string
	for _, gap := range gaps {
		question.Gaps = append(question.Gaps, gapEntry(gap, rows))
		declaration, declared := declaredBy(gap.Key, gapDeclarations(gap, rows))
		if !declared || declaration.Recommended != store.ChoiceRelease {
			correct = append(correct, gap.Key)
		}
	}
	if len(correct) > 0 {
		question.Recommended = store.ChoiceCorrect
		question.Why = "correct for: " + strings.Join(correct, ", ")
	}
	return EndingBody(phaseEndedWithGaps, gapReasons(gaps), Decided(rows.Review)), question
}

// gapEntry returns the entry of gap in a gaps question: its reason, then its parts in brackets when it has any.
func gapEntry(gap Gap, rows Rows) string {
	var parts []string
	if declaration, declared := declaredBy(gap.Key, gapDeclarations(gap, rows)); declared {
		parts = append(parts, "planner: "+declaration.Reason)
	}
	for _, turn := range rows.GapTurns {
		if turn.Gap == gap.Key {
			parts = append(parts, fmt.Sprintf("turn %d: %s", turn.Turn, turn.Reason))
		}
	}
	if len(parts) == 0 {
		return gap.Reason
	}
	return fmt.Sprintf("%s (%s)", gap.Reason, strings.Join(parts, "; "))
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
