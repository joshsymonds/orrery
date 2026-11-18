package kinds

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/joshsymonds/orrery/internal/store"
)

// integratorRounds is how many rounds the release integrator of one conflicting merge runs: the first, and one more for
// each of the two times a Done gate or a capture that fails on its commit is sent back to it.
const integratorRounds = 3

// integratorWork returns the part of the work of a release run that round of its release integrator runs under. The
// step's id is store.NodeWorkID of the run's id at the generation of its candidate (gatedRun), its attempt and this
// part, so a restart finds the step under that id.
func integratorWork(round int64) string {
	return fmt.Sprintf("integrator %d", round)
}

// integratorPosition returns the position of the release run that records round of the release integrator of the
// candidate at generation. Each generation numbers its rounds after the rounds of the generation before it, so every
// round of an attempt has a position of its own.
func integratorPosition(generation, round int64) int64 {
	return generation*integratorRounds + round
}

// integratorAction decides a releasing epic whose prepare found the merge of origin's commit into the tip conflicting.
// The release integrator finishes that merge, in rounds walked in order at the generation of the run's candidate. A
// round with no release run is recorded once its step ends, waits while its step runs, or starts (integratorStart). A
// round recorded as failed stops the release with its reason, unless a replan release run of the attempt was recorded
// with it: then the release goes back to planning with that run's reason (replanAction). A round recorded as passed
// holds its commit at the epic's
// Done gates and then at its captures (mergedChecks), walked under an id of the round's own at that commit. When they
// all pass, the commit is recorded as the candidate. When one fails, the next round starts with the failure, so the
// integrator mends its merge, and a failure in the last round stops the release with it.
func integratorAction(run store.NodeRun, rows Rows) Action {
	generation := replacedCount(run.Attempt, rows.Releases)
	gated := gatedRun(run, generation)
	var head, failure, previous string
	for round := int64(1); round <= integratorRounds; round++ {
		id := store.NodeWorkID(gated.ID, run.Attempt, integratorWork(round))
		position := integratorPosition(generation, round)
		recorded, ok := firstMatch(rows.Releases, func(r store.ReleaseRun) bool {
			return r.Part == store.ReleasePartIntegrator && r.Attempt == run.Attempt && r.Position == position
		})
		if !ok {
			return integratorStart(rows, id, position, head, previous)
		}
		if !recorded.Passed {
			if act := replanAction(run, rows); act.Kind == ActReplan {
				return act
			}
			return Action{Kind: ActStop, Output: recorded.Reason}
		}
		checked := gated
		checked.ID = fmt.Sprintf("%s-integrator-%d", gated.ID, round)
		checked.Revision = recorded.Head
		var act Action
		var settled bool
		failure, previous, act, settled = mergedChecks(checked, rows)
		switch {
		case !settled && act.Kind == ActPark:
			return Action{Kind: ActStop, Output: act.Output}
		case !settled:
			act.Revision = recorded.Head
			return act
		case failure == "":
			return Action{
				Kind: ActRecord, Items: []string{store.ReleasePartCandidate, recorded.Head}, Revision: recorded.Head,
			}
		}
		head = recorded.Head
	}
	return Action{Kind: ActStop, Output: failure}
}

// integratorStart decides the round of the release integrator whose step is id and whose release run is at position,
// which has no release run. It records the step once its end is stored, and waits while it runs. With no step, it
// stops the release when the epic does not hold the release slot, waits while the epic is paused or parked, and starts
// the round otherwise. A round after the first starts from head, the commit of the round before it, with previous, the
// failure that round's commit met, as its Output.
func integratorStart(rows Rows, id string, position int64, head, previous string) Action {
	if row, found := firstMatch(rows.Steps, func(s store.StepRow) bool { return s.ID == id }); found {
		return recordAction(row, []string{store.ReleasePartIntegrator, strconv.FormatInt(position, 10)})
	}
	if !holdsSlot(rows) {
		return Action{
			Kind: ActStop, Output: "the release integrator did not start: this epic does not hold the release slot",
		}
	}
	if rows.Epic.Paused || rows.Epic.Parked != "" {
		return Action{Kind: ActNone}
	}
	return Action{
		Kind: ActIntegrator, IDs: []string{id}, Items: []string{strconv.FormatInt(position, 10), head},
		Output: previous,
	}
}

// mergedChecks walks the epic's Done gates and then the captures of its Requirements at the revision of checked, a
// commit the release integrator made, under checked's id. Until they settle it returns the action the first one that
// is not decided asks for. Once they settle, failure is "" when every gate passed and no capture failed. Otherwise it
// is the stop reason of the first gate that failed, or of the captures that failed, and previous is what that failure
// was, with the gate's exit and output tails or each capture's verdict, as the next round of the integrator reads it.
// A capture fails as an integrate run's does (captureCauses): it was not captured or not judged, or its Requirement was
// green at the stored base and its capture does not pass at the commit.
func mergedChecks(checked store.NodeRun, rows Rows) (string, string, Action, bool) {
	gate, act, settled := doneGates(checked, rows)
	if !settled {
		return "", "", act, false
	}
	if gate != "" {
		failure := fmt.Sprintf("done gate %s did not pass at %s", gate, checked.Revision)
		return failure, gateFailure(checked, rows), Action{}, true
	}
	captured, act, settled := captures(checked, rows)
	if !settled {
		return "", "", act, false
	}
	causes := captureCauses(rows, captured)
	if len(causes) == 0 {
		return "", "", Action{}, true
	}
	failure := fmt.Sprintf("%s at %s", strings.Join(causes, "; "), checked.Revision)
	return failure, captureFailures(checked.Revision, rows, captured), Action{}, true
}

// gateFailure returns what the first Done gate that did not pass at the revision of checked was: its name and how it
// ended, then the tails of its stdout and stderr that are not empty.
func gateFailure(checked store.NodeRun, rows Rows) string {
	for i, gate := range rows.Epic.DoneGates {
		check, _, decided := gateCheck(checked, i, gate, rows.Checks)
		if !decided || checkPassed(check) {
			continue
		}
		var how string
		switch {
		case check.NotRun != "":
			how = "it did not run: " + check.NotRun
		case check.TimedOut:
			how = "it ran past its limit and was killed"
		default:
			how = fmt.Sprintf("it exited %d", check.Exit)
		}
		lines := []string{fmt.Sprintf("Done gate %s failed on commit %s: %s.", gate, checked.Revision, how)}
		if tail := strings.TrimSpace(check.Stdout); tail != "" {
			lines = append(lines, "stdout tail:", tail)
		}
		if tail := strings.TrimSpace(check.Stderr); tail != "" {
			lines = append(lines, "stderr tail:", tail)
		}
		return strings.Join(lines, "\n")
	}
	return ""
}

// captureFailures returns one line for each capture at revision that captureCauses finds failing: one that was not
// captured, with why, one that was not judged, and one whose Requirement was green at the stored base and whose capture
// does not pass, with its exit, its verdict and its judge's reason.
func captureFailures(revision string, rows Rows, captured map[string]store.EvidenceRun) string {
	green := GreenAt(rows, rows.Epic.StoredBase)
	var lines []string
	for _, requirement := range itemRequirements(rows.Epic.Requirements) {
		capture := captured[requirement.ID]
		switch {
		case capture.NotCaptured != "":
			lines = append(lines, fmt.Sprintf("The capture of %s did not run on commit %s: %s.",
				requirement.ID, revision, capture.NotCaptured))
		case capture.Verdict == "":
			lines = append(lines, fmt.Sprintf("The capture of %s on commit %s was not judged.",
				requirement.ID, revision))
		case green[requirement.ID] && !capture.Passed:
			lines = append(lines, fmt.Sprintf(
				"%s was green at the stored base %s, and its capture on commit %s exited %d and was judged %s: %s",
				requirement.ID, rows.Epic.StoredBase, revision, capture.Exit, capture.Verdict, capture.JudgeReason))
		}
	}
	return strings.Join(lines, "\n")
}
