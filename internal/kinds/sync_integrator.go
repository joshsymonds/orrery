package kinds

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/joshsymonds/orrery/internal/store"
)

// The modes of an ActIntegrator action of a sync run, its first item. SyncRoundStart starts a round's step, and
// SyncRoundRecord records the round once its step ended.
const (
	SyncRoundStart  = "start"
	SyncRoundRecord = "record"
)

// syncIntegrator decides a sync run whose prepare found the merge of origin's commit into the stored base conflicting.
// The sync's integrator finishes that merge in integratorRounds rounds, walked in order. A round with no record starts,
// or is recorded once its step ended (syncRoundStep). A round recorded skipped ends the sync skipped, untold, and one
// recorded failed ends it failed, told, at once. A round recorded passed holds its commit at the Done gates and then
// at the captures (mergedChecks), walked under an id of the round's own. Once they all pass, the sync advances to that
// commit. When one fails, the next round starts with that failure, and a failure of the last round ends the sync
// failed, told, with its last report.
func syncIntegrator(run store.NodeRun, rows Rows, result SyncResult) Action {
	line := conflictLine(result)
	var head, previous string
	var last store.IntegrateRound
	for round := int64(1); round <= integratorRounds; round++ {
		record, recorded := roundRecord(rows.Rounds, run, 0, round)
		if !recorded {
			return syncRoundStep(run, rows, round, head, previous)
		}
		if record.Outcome != outcomePassed {
			return syncRoundEnded(result, line, round, record)
		}
		checked := run
		checked.ID = fmt.Sprintf("%s-integrator-%d", run.ID, round)
		checked.Revision = record.Head
		failure, detail, act, settled := walkMerged(checked, rows, true)
		switch {
		case !settled:
			act.Revision = record.Head
			return syncWait(act, line)
		case failure == "":
			return Action{
				Kind:     ActAdvance,
				Output:   advancedLine(result, record.Head),
				Revision: record.Head,
				Reason:   SyncMoved(rows, result),
			}
		}
		head, previous, last = record.Head, detail, record
	}
	// The loop ends only when the last round failed its checks, which ends the sync failed with that failure.
	return syncIntegratorFailed(result, []string{line, previous, "last report: " + last.Explanation})
}

// conflictLine returns the line that names the paths the merge of origin's commit into the stored base conflicts in.
func conflictLine(result SyncResult) string {
	return fmt.Sprintf(
		"merge of origin's commit %s into the stored base %s conflicts in %s",
		result.Origin, result.Base, strings.Join(result.Conflicts, ", "),
	)
}

// syncRoundStep decides round of the sync's integrator, which has no record. A round whose step is stored waits while
// the step runs, and is recorded once it ended (SyncRoundRecord). With no step, a paused or parked epic starts no
// round, and any other epic starts it (SyncRoundStart), with head, the commit the round before it recorded, and
// previous, the failure that commit met, as its Output.
func syncRoundStep(run store.NodeRun, rows Rows, round int64, head, previous string) Action {
	id := store.NodeWorkID(run.ID, run.Attempt, integratorWork(round))
	item := strconv.FormatInt(round, 10)
	step, stored := firstMatch(rows.Steps, func(row store.StepRow) bool { return row.ID == id })
	switch {
	case stored && step.End == "":
		return Action{Kind: ActNone}
	case stored:
		return Action{Kind: ActIntegrator, IDs: []string{id}, Items: []string{SyncRoundRecord, item}}
	case rows.Epic.Paused || rows.Epic.Parked != "":
		return Action{Kind: ActNone}
	}
	return Action{
		Kind: ActIntegrator, IDs: []string{id}, Items: []string{SyncRoundStart, item, head}, Output: previous,
	}
}

// syncRoundEnded ends a sync run at round, whose record is not passed. A round recorded failed ends the sync failed,
// told, with the conflict line, the round's failure and its explanation when it has one. Any other record, a round
// whose step ended engine_restarted, ends it skipped and untold, so origin's commit is left untried.
func syncRoundEnded(result SyncResult, line string, round int64, record store.IntegrateRound) Action {
	if record.Outcome == outcomeFailed {
		return syncIntegratorFailed(result, explained([]string{line, record.Failure}, record.Explanation))
	}
	output := explained([]string{line, fmt.Sprintf("integrator round %d did not finish", round)}, record.Explanation)
	return syncSkipped(strings.Join(output, "\n"))
}

// syncIntegratorFailed ends a sync run failed and told, its output and its Cause both the causes, one to a line.
func syncIntegratorFailed(result SyncResult, causes []string) Action {
	act := syncTold(outcomeFailed, strings.Join(causes, "\n"))
	act.Cause = SyncCause(result, causes)
	return act
}

// explained returns lines, with explanation after them when it is set.
func explained(lines []string, explanation string) []string {
	if explanation == "" {
		return lines
	}
	return append(lines, explanation)
}
