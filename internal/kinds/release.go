package kinds

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/joshsymonds/orrery/internal/store"
)

// Release is the release kind. Its run takes the repository's release slot or waits for it, prepares and gates the
// release candidate, starts each release step in order, stores each end first, stops on any failure, judges the
// postconditions, and ends the epic released, or done when it has no release steps.
type Release struct{}

// The actions a release run takes beyond the ones every kind shares.
const (
	ActTake   ActionKind = "take"
	ActStep   ActionKind = "step"
	ActJudge  ActionKind = "judge"
	ActRecord ActionKind = "record"
	ActStop   ActionKind = "stop"
	ActEnding ActionKind = "ending"
	ActFree   ActionKind = "free"
)

// The outcomes a release run writes beyond those the other kinds name.
const (
	outcomeDone     = "done"
	outcomeReleased = "released"
)

// ReleaseResult is the result a release prepare writes to its run: the error that stopped it, whether the revision it
// records is behind the target, the candidate it records and the tip the candidate was made from. The release kind
// reads it and the executor writes and reads it, so it is defined once, here.
type ReleaseResult struct {
	Error     string `json:"error"`
	Behind    bool   `json:"behind"`
	Candidate string `json:"candidate"`
	Tip       string `json:"tip"`
}

// Decide decides a release run from its epic, the release slot and the epic's release runs. A released or done epic
// frees the slot it holds and otherwise ends passed, a clean epic ends done or takes the slot, a releasing epic goes on
// with its candidate, its release steps and its judge, and any other phase gives none.
func (Release) Decide(node Node, rows Rows) Action {
	switch rows.Epic.Phase {
	case phaseReleased, phaseDone:
		return releaseEnded(rows)
	case store.PhaseClean:
		return releaseClean(rows)
	case store.PhaseReleasing:
		return releaseRun(node.Run, rows)
	default:
		return Action{Kind: ActNone}
	}
}

// releaseEnded frees the release slot an epic that is released or done holds, and ends it passed otherwise.
func releaseEnded(rows Rows) Action {
	if holdsSlot(rows) {
		return Action{Kind: ActFree}
	}
	return Action{Kind: ActEnd, Outcome: outcomePassed}
}

// endingAction ends the epic in the phase outcome names, with the body that names the gaps its review holds.
func endingAction(outcome string, rows Rows) Action {
	return Action{Kind: ActEnding, Outcome: outcome, Output: EndingBody(outcome, Gaps(rows.Review))}
}

// releaseClean ends a clean epic with no release steps done, gives none while another epic holds the release slot, and
// takes the slot otherwise.
func releaseClean(rows Rows) Action {
	if len(rows.Epic.ReleaseSteps) == 0 {
		return endingAction(outcomeDone, rows)
	}
	if rows.Slot != "" && !holdsSlot(rows) {
		return Action{Kind: ActNone}
	}
	return Action{Kind: ActTake}
}

// holdsSlot reports whether the epic holds the release slot of its repository.
func holdsSlot(rows Rows) bool {
	return rows.Slot == rows.Epic.ID
}

// releaseRun decides a releasing epic's run. Its candidate comes first, then its release steps in order, then its judge.
func releaseRun(run store.NodeRun, rows Rows) Action {
	candidate, ok := currentCandidate(run, rows)
	if !ok {
		return candidateAction(run, rows)
	}
	return walkSteps(run, rows, candidate)
}

// currentCandidate returns the head of the candidate the run reuses, and whether it reuses one. That is the latest
// candidate that passed, at any attempt, unless a verify run of this attempt found origin's main not in it. Before the
// release's first step starts from a candidate passed at an earlier attempt, the interpreter checks that origin's main is
// in it, and a candidate that is not is not reused: it is prepared again at this attempt.
func currentCandidate(run store.NodeRun, rows Rows) (string, bool) {
	for _, candidate := range slices.Backward(rows.Releases) {
		if candidate.Part != store.ReleasePartCandidate || !candidate.Passed {
			continue
		}
		if staleAt(run.Attempt, candidate.Head, rows.Releases) {
			return "", false
		}
		return candidate.Head, true
	}
	return "", false
}

// staleAt reports whether a verify run of attempt found origin's main not in the candidate head.
func staleAt(attempt int64, head string, runs []store.ReleaseRun) bool {
	_, stale := firstMatch(runs, func(r store.ReleaseRun) bool {
		return r.Part == store.ReleasePartVerify && !r.Passed && r.Attempt == attempt && r.Head == head
	})
	return stale
}

// candidateAction decides a releasing epic whose candidate has not passed. It stops on a candidate that failed at this
// attempt. Otherwise it prepares the candidate, stops on the error the prepare recorded, and holds the candidate at its
// done gates when it is behind the target or prepared again at this attempt. Any other candidate is recorded.
func candidateAction(run store.NodeRun, rows Rows) Action {
	failed, ok := firstMatch(rows.Releases, func(r store.ReleaseRun) bool {
		return r.Part == store.ReleasePartCandidate && !r.Passed && r.Attempt == run.Attempt
	})
	if ok {
		return Action{Kind: ActStop, Output: failed.Reason}
	}
	result, err := ParseReleaseResult(run.Result)
	if err != nil {
		return Action{Kind: ActStop, Output: err.Error()}
	}
	if run.Revision == "" {
		return prepareRelease(result, rows)
	}
	generation := replacedCount(run.Attempt, rows.Releases)
	if result.Behind || generation > 0 {
		return gatesAction(run, rows, generation)
	}
	return Action{Kind: ActRecord, Items: []string{store.ReleasePartCandidate, run.Revision}}
}

// replacedCount returns how many candidates of attempt origin's main was found not in, each a verify run of the attempt
// that did not pass. Each one is a candidate the attempt has prepared again, so it is the generation of the candidate
// the run has now.
func replacedCount(attempt int64, runs []store.ReleaseRun) int64 {
	var count int64
	for _, run := range runs {
		if run.Part == store.ReleasePartVerify && !run.Passed && run.Attempt == attempt {
			count++
		}
	}
	return count
}

// gatedRun returns run as the Done gates of its candidate are walked under, at generation. The first candidate of an
// attempt is walked under the run's own id. A candidate prepared again is walked under an id that carries its generation,
// so the checks the attempt ran for the candidate it replaces cannot decide its gates.
func gatedRun(run store.NodeRun, generation int64) store.NodeRun {
	if generation > 0 {
		run.ID = fmt.Sprintf("%s-generation-%d", run.ID, generation)
	}
	return run
}

// ParseReleaseResult decodes the result of a release run, which is empty until its prepare writes one. A result that is
// not a JSON object is an error.
func ParseReleaseResult(result string) (ReleaseResult, error) {
	var parsed ReleaseResult
	if result == "" {
		return parsed, nil
	}
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		return ReleaseResult{}, fmt.Errorf("release result is not a JSON object: %w", err)
	}
	return parsed, nil
}

// prepareRelease decides a candidate with no revision. It waits while the epic is paused or parked, as prepare does,
// stops on the error the prepare recorded, and prepares otherwise.
func prepareRelease(result ReleaseResult, rows Rows) Action {
	action := prepare(rows)
	if action.Kind == ActNone || result.Error == "" {
		return action
	}
	return Action{Kind: ActStop, Output: result.Error}
}

// gatesAction decides a candidate held at the epic's Done gates, walked as an integrate run walks them (doneGates), under
// the generation of the candidate. It returns the action a gate that is not decided asks for, a gate lost at a restart
// twice stops the release with the park's reason, a gate that did not pass stops it, and the candidate is recorded once
// every gate has passed.
func gatesAction(run store.NodeRun, rows Rows, generation int64) Action {
	gate, act, settled := doneGates(gatedRun(run, generation), rows)
	switch {
	case !settled && act.Kind == ActPark:
		return Action{Kind: ActStop, Output: act.Output}
	case !settled:
		return act
	case gate != "":
		return Action{Kind: ActStop, Output: fmt.Sprintf("done gate %s did not pass at %s", gate, run.Revision)}
	}
	return Action{Kind: ActRecord, Items: []string{store.ReleasePartCandidate, run.Revision}}
}

// walkSteps decides a releasing epic whose candidate passed at candidate. Each release step in order passes at any
// attempt or is decided by stepAction from the head of the last step before it, and the judge follows the last step.
func walkSteps(run store.NodeRun, rows Rows, candidate string) Action {
	start := candidate
	for i, step := range rows.Epic.ReleaseSteps {
		position := int64(i + 1)
		passed, ok := firstMatch(rows.Releases, func(r store.ReleaseRun) bool {
			return r.Part == store.ReleasePartStep && r.Position == position && r.Passed
		})
		if !ok {
			return stepAction(run, rows, position, step, start)
		}
		start = passed.Head
	}
	return judgeAction(run, rows, candidate)
}

// stepAction decides release step position, which has not passed, from start. It stops on a step that failed at this
// attempt, and records a step whose end is stored. Otherwise it waits while the step runs, or starts it when it has no
// row, unless the epic does not hold the slot, is paused, or the step's undo is none after an earlier failure.
func stepAction(run store.NodeRun, rows Rows, position int64, step store.EpicReleaseStep, start string) Action {
	failed, ok := firstMatch(rows.Releases, func(r store.ReleaseRun) bool {
		return r.Part == store.ReleasePartStep && r.Position == position && !r.Passed && r.Attempt == run.Attempt
	})
	if ok {
		return Action{
			Kind:   ActStop,
			Output: fmt.Sprintf("release step %d (%s) failed: %s", position, step.Step, failed.Reason),
		}
	}
	label := fmt.Sprintf("release %d", position)
	id := store.NodeWorkID(run.ID, run.Attempt, label)
	if row, found := firstMatch(rows.Steps, func(s store.StepRow) bool { return s.ID == id }); found {
		return recordAction(row, []string{label, start})
	}
	if !holdsSlot(rows) {
		return Action{
			Kind: ActStop,
			Output: fmt.Sprintf(
				"release step %d (%s) did not start: this epic does not hold the release slot",
				position, step.Step,
			),
		}
	}
	if rows.Epic.Paused {
		return Action{Kind: ActNone}
	}
	if undoNoneStopped(run, rows, position, step) {
		return Action{
			Kind: ActStop,
			Output: fmt.Sprintf(
				"release step %d (%s) failed at an earlier attempt with undo none, so it runs again only when named",
				position, step.Step,
			),
		}
	}
	return Action{Kind: ActStep, IDs: []string{id}, Items: []string{label, start}}
}

// undoNoneStopped reports whether step at position has undo none, failed at an earlier attempt, and has no run named at
// this attempt, so that it must not run again.
func undoNoneStopped(run store.NodeRun, rows Rows, position int64, step store.EpicReleaseStep) bool {
	if !strings.EqualFold(strings.TrimSpace(step.Undo), "none") {
		return false
	}
	_, failed := firstMatch(rows.Releases, func(r store.ReleaseRun) bool {
		return r.Part == store.ReleasePartStep && r.Position == position && !r.Passed && r.Attempt < run.Attempt
	})
	_, named := firstMatch(rows.Releases, func(r store.ReleaseRun) bool {
		return r.Part == store.ReleasePartNamed && r.Position == position && r.Attempt == run.Attempt
	})
	return failed && !named
}

// judgeAction decides the judge after the last release step, from the candidate. It ends released when the judge of this
// attempt passed, stops on one that failed, and otherwise starts the judge, waits while it runs, or records it.
func judgeAction(run store.NodeRun, rows Rows, candidate string) Action {
	judged, ok := firstMatch(rows.Releases, func(r store.ReleaseRun) bool {
		return r.Part == store.ReleasePartJudge && r.Attempt == run.Attempt
	})
	if ok {
		if judged.Passed {
			return endingAction(outcomeReleased, rows)
		}
		return Action{Kind: ActStop, Output: judged.Reason}
	}
	id := store.NodeWorkID(run.ID, run.Attempt, store.ReleasePartJudge)
	items := []string{store.ReleasePartJudge, candidate}
	if row, found := firstMatch(rows.Steps, func(s store.StepRow) bool { return s.ID == id }); found {
		return recordAction(row, items)
	}
	if !holdsSlot(rows) {
		return Action{Kind: ActStop, Output: "the judge did not start: this epic does not hold the release slot"}
	}
	if rows.Epic.Paused {
		return Action{Kind: ActNone}
	}
	return Action{Kind: ActJudge, IDs: []string{id}, Items: items}
}

// recordAction returns none while the work of row runs, and records it with items once it has ended.
func recordAction(row store.StepRow, items []string) Action {
	if row.End == "" {
		return Action{Kind: ActNone}
	}
	return Action{Kind: ActRecord, IDs: []string{row.ID}, Items: items}
}

// firstMatch returns the first of items that matches, and whether one does.
func firstMatch[T any](items []T, match func(T) bool) (T, bool) {
	i := slices.IndexFunc(items, match)
	if i < 0 {
		var zero T
		return zero, false
	}
	return items[i], true
}
