package kinds

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/joshsymonds/orrery/internal/reviewers"
	"github.com/joshsymonds/orrery/internal/store"
)

// Release is the release kind. Its run begins the release of a clean epic at once, prepares the release candidate, has
// the release integrator merge origin's commit into a tip that lacks it and verify the merged tree, sends the release
// back to planning when the integrator stopped on a change to the candidate or a release reviewer refused it, starts the
// plan that replans it for an owner's correction and resumes it once that plan has ended, has its release reviewers hold
// the candidate before the first release step, starts each release step in order, stores each end first, stops on any
// failure, judges the postconditions, and ends the epic released, or done when it has no release steps.
type Release struct{}

// The actions a release run takes beyond the ones every kind shares.
const (
	ActBegin      ActionKind = "begin"
	ActIntegrator ActionKind = "integrator"
	ActStep       ActionKind = "step"
	ActJudge      ActionKind = "judge"
	ActRecord     ActionKind = "record"
	ActStop       ActionKind = "stop"
	ActEnding     ActionKind = "ending"
	ActReplan     ActionKind = "replan"
	ActResume     ActionKind = "resume"
)

// phaseCorrecting is the phase an epic is in while its review's ledger is corrected, and while its release is planned
// again for a change to its candidate.
const phaseCorrecting = "correcting"

// ReplanPrefix returns the prefix of the plan run that plans the change to the candidate a replan of the release run at
// path, at attempt, asks for: <path>/A<attempt>/. The plan run's path is the prefix and the id of the plan node it runs.
func ReplanPrefix(path string, attempt int64) string {
	return fmt.Sprintf("%s/A%d/", path, attempt)
}

// The outcomes a release run writes beyond those the other kinds name.
const (
	outcomeDone     = "done"
	outcomeReleased = "released"
)

// ReleaseResult is the result a release prepare writes to its run: the error that stopped it, whether the tip is behind
// the target, the candidate it records and the tip the candidate was made from. A tip that holds origin's commit is the
// candidate. A tip that lacks it records no candidate: it records origin's commit as Origin, the branch of origin it was
// fetched from and the paths git's merge of the two conflicts in, none for a merge that is clean, and the release
// integrator makes the candidate. Migrations is the directory of goose migrations origin's project file declares, ""
// when it declares none, and Collisions the lines that name each new migration of the tip whose version origin's main
// uses for another file and the numbers the tip's new migrations are renumbered to, none when no version collides. A
// tip that holds origin's commit and has a collision records no candidate either: it records origin's commit and its
// branch with the tip not behind, and the release integrator makes the renumber as the candidate, since a candidate
// with a collision cannot ship. The release kind reads it and the executor writes and reads it, so it is defined
// once, here.
type ReleaseResult struct {
	Error      string   `json:"error"`
	Behind     bool     `json:"behind"`
	Candidate  string   `json:"candidate"`
	Tip        string   `json:"tip"`
	Origin     string   `json:"origin,omitempty"`
	Branch     string   `json:"branch,omitempty"`
	Conflicts  []string `json:"conflicts,omitempty"`
	Migrations string   `json:"migrations,omitempty"`
	Collisions []string `json:"collisions,omitempty"`
}

// Decide decides a release run from its epic and the epic's release runs. A released or done epic ends passed, a clean
// epic ends done or begins its release, a releasing epic goes on with its candidate, its release steps and its judge, a
// stopped epic whose run a retry turned running is sent back to planning (replanAction), a correcting epic starts its
// release's replan plan when none has started and resumes the release once that plan has ended (resumeAction), and any
// other phase gives none (Idle).
func (Release) Decide(node Node, rows Rows) Action {
	decide, ok := releaseDecision(rows.Epic.Phase)
	if !ok {
		return Action{Kind: ActNone}
	}
	return decide(node.Run, rows)
}

// Idle reports whether a release run of an epic in phase waits for the phase to move on: Decide gives it no action,
// whatever the epic's other rows hold. An ended_with_gaps epic's release run waits so, until the owner's release moves
// the epic to clean.
func (Release) Idle(phase string) bool {
	_, ok := releaseDecision(phase)
	return !ok
}

// releaseDecision returns how a release run of an epic in phase is decided, and false for a phase that gives it none.
func releaseDecision(phase string) (func(store.NodeRun, Rows) Action, bool) {
	switch phase {
	case phaseReleased, phaseDone:
		return releaseEnded, true
	case store.PhaseClean:
		return func(_ store.NodeRun, rows Rows) Action { return releaseClean(rows) }, true
	case store.PhaseReleasing:
		return releaseRun, true
	case store.PhaseStopped:
		return replanAction, true
	case phaseCorrecting:
		return resumeAction, true
	default:
		return nil, false
	}
}

// replanAction sends the release of run back to planning when a replan release run of its attempt is stored, with that
// run's reason: the report of the round of the release integrator that asked for it, or of the release review that
// refused the candidate when a retry replans that refusal. Otherwise it gives none.
func replanAction(run store.NodeRun, rows Rows) Action {
	replan, ok := firstMatch(rows.Releases, func(r store.ReleaseRun) bool {
		return r.Part == store.ReleasePartReplan && r.Attempt == run.Attempt
	})
	if !ok {
		return Action{Kind: ActNone}
	}
	return Action{Kind: ActReplan, Output: replan.Reason}
}

// resumeAction decides the release of run while its epic is correcting. When no plan run is stored under run's replan
// prefix at its attempt, it replans: the plan that carries the owner's correction to the candidate starts there. While
// that plan runs or is parked it gives none, and once it has ended it resumes the release.
func resumeAction(run store.NodeRun, rows Rows) Action {
	prefix := ReplanPrefix(run.Path, run.Attempt)
	plan, ok := firstMatch(rows.Runs, func(r store.NodeRun) bool {
		return r.Kind == kindPlan && strings.HasPrefix(r.Path, prefix)
	})
	switch {
	case !ok:
		return Action{Kind: ActReplan}
	case plan.State == store.NodeEnded:
		return Action{Kind: ActResume}
	default:
		return Action{Kind: ActNone}
	}
}

// releaseEnded ends a released or done epic's release passed.
func releaseEnded(store.NodeRun, Rows) Action {
	return Action{Kind: ActEnd, Outcome: outcomePassed}
}

// endingAction ends the epic in the phase outcome names, with the body that names the gaps its review holds and the
// findings an owner's answer closed.
func endingAction(outcome string, rows Rows) Action {
	return Action{
		Kind: ActEnding, Outcome: outcome, Output: EndingBody(outcome, Gaps(rows.Review), Decided(rows.Review)),
	}
}

// releaseClean ends a clean epic with no release steps done, and otherwise begins its release at once.
func releaseClean(rows Rows) Action {
	if len(rows.Epic.ReleaseSteps) == 0 {
		return endingAction(outcomeDone, rows)
	}
	return Action{Kind: ActBegin}
}

// ownerStopped reports whether the owner stopped the release at attempt: a stop release run of that attempt is stored.
func ownerStopped(rows Rows, attempt int64) bool {
	_, ok := firstMatch(rows.Releases, func(r store.ReleaseRun) bool {
		return r.Part == store.ReleasePartStop && r.Attempt == attempt
	})
	return ok
}

// releaseRun decides a releasing epic's run. Its candidate comes first, then its release steps in order, the first of
// them gated by its release reviewers, then its judge.
func releaseRun(run store.NodeRun, rows Rows) Action {
	candidate, ok := currentCandidate(run, rows)
	if !ok {
		return candidateAction(run, rows)
	}
	return walkSteps(run, rows, candidate)
}

// currentCandidate returns the head of the candidate the run reuses, and whether it reuses one. That is the latest
// candidate that passed, at any attempt after the last replan, unless a verify run of this attempt found origin's main not
// in it. A replan changes the epic's branch, so no candidate from its attempt or before is reused. Before the release's
// first step starts from a candidate passed at an earlier attempt, the interpreter checks that origin's main is in it, and
// a candidate that is not is not reused: it is prepared again at this attempt.
func currentCandidate(run store.NodeRun, rows Rows) (string, bool) {
	replanned := lastReplan(rows.Releases)
	for _, candidate := range slices.Backward(rows.Releases) {
		if candidate.Part != store.ReleasePartCandidate || !candidate.Passed || candidate.Attempt <= replanned {
			continue
		}
		if staleAt(run.Attempt, candidate.Head, rows.Releases) {
			return "", false
		}
		return candidate.Head, true
	}
	return "", false
}

// lastReplan returns the highest attempt among runs' replan release runs, or -1 when there is none.
func lastReplan(runs []store.ReleaseRun) int64 {
	last := int64(-1)
	for _, run := range runs {
		if run.Part == store.ReleasePartReplan {
			last = max(last, run.Attempt)
		}
	}
	return last
}

// staleAt reports whether a verify run of attempt found origin's main not in the candidate head.
func staleAt(attempt int64, head string, runs []store.ReleaseRun) bool {
	_, stale := firstMatch(runs, func(r store.ReleaseRun) bool {
		return r.Part == store.ReleasePartVerify && !r.Passed && r.Attempt == attempt && r.Head == head
	})
	return stale
}

// candidateAction decides a releasing epic whose candidate has not passed. It stops on a candidate that failed at this
// attempt. A prepare that recorded origin's commit hands the candidate to the release integrator (integratorAction):
// the merge of a commit not in the tip, whether that merge conflicts or not, or the renumber of a tip that holds it and
// has a migration collision. Otherwise it prepares the candidate, stops on the error the prepare recorded, and records
// the candidate it prepared, at any generation.
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
	if result.Origin != "" {
		return integratorAction(run, rows)
	}
	if run.Revision == "" {
		return prepareRelease(result, rows)
	}
	return Action{Kind: ActRecord, Items: []string{store.ReleasePartCandidate, run.Revision}}
}

// replacedCount returns how many candidates of attempt origin's main was found not in, each a verify run of the attempt
// that did not pass. Each one is a candidate the attempt has prepared again, so it is the generation of the candidate
// the run has now.
func replacedCount(attempt int64, runs []store.ReleaseRun) int64 {
	return store.ReleaseGeneration(runs, attempt)
}

// gatedRun returns run as the work of its candidate at generation runs under: its release integrator, its release
// reviewers and its first release step. The first candidate of an attempt runs under the run's own id. A candidate
// prepared again runs under an id that carries its generation, so the work the attempt ran for the candidate it replaces
// cannot decide its own.
func gatedRun(run store.NodeRun, generation int64) store.NodeRun {
	run.ID = store.ReleaseGatedID(run.ID, generation)
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
// attempt, and records a step whose work has ended (releaseWork). Otherwise it waits while that work runs, or starts it
// when it has none, unless the owner stopped the release at this attempt, is paused, the step's undo is none after an
// earlier failure, or the first step's candidate is not yet held by the release reviewers (releaseReviewGate).
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
	id := store.ReleaseStepWorkID(run, rows.Releases, position)
	if act, found := releaseWork(rows, step, id, []string{label, start}); found {
		return act
	}
	if ownerStopped(rows, run.Attempt) {
		return Action{
			Kind: ActStop,
			Output: fmt.Sprintf(
				"release step %d (%s) did not start: the owner stopped the release",
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
	if position == 1 {
		if act, held := releaseReviewGate(run, rows, start); !held {
			return act
		}
	}
	return Action{Kind: ActStep, IDs: []string{id}, Items: []string{label, start}}
}

// releaseWork decides the work stored under id for step, and reports whether any is. A step that names a kind runs as a
// check, which waits while it runs and is recorded with items once it has ended. Any other step runs as an agent step,
// which recordAction decides.
func releaseWork(rows Rows, step store.EpicReleaseStep, id string, items []string) (Action, bool) {
	if step.Kind != "" {
		check, found := checkByID(rows.Checks, id)
		switch {
		case !found:
			return Action{}, false
		case check.State == store.NodeRunning:
			return Action{Kind: ActNone}, true
		}
		return Action{Kind: ActRecord, IDs: []string{id}, Items: items}, true
	}
	row, found := firstMatch(rows.Steps, func(s store.StepRow) bool { return s.ID == id })
	if !found {
		return Action{}, false
	}
	return recordAction(row, items), true
}

// releaseReviewGate decides whether the release reviewers have held candidate, the candidate the first release step
// starts from. Each enabled reviewer of the release stage, in merged order, reviews candidate once at this attempt, as its
// step is started and recorded: a review that failed is decided by reviewRefused, and the next reviewer waits for the one
// before it. A reviewer with no review starts (ActStep), waits while its step runs, or is recorded once its step has
// ended. It holds when no release reviewer is enabled.
func releaseReviewGate(run store.NodeRun, rows Rows, candidate string) (Action, bool) {
	generation := replacedCount(run.Attempt, rows.Releases)
	gated := gatedRun(run, generation)
	enabled := reviewers.ForStage(rows.Epic.Reviewers, reviewers.StageRelease)
	for i, reviewer := range enabled {
		position := releaseReviewPosition(generation, int64(len(enabled)), int64(i+1))
		id := store.NodeWorkID(gated.ID, run.Attempt, "review "+reviewer.Name)
		row, found := firstMatch(rows.Steps, func(s store.StepRow) bool { return s.ID == id })
		review, ok := firstMatch(rows.Releases, func(r store.ReleaseRun) bool {
			return r.Part == store.ReleasePartReview && r.Attempt == run.Attempt && r.Position == position
		})
		if ok {
			if !review.Passed {
				return reviewRefused(rows, row, review, candidate), false
			}
			continue
		}
		items := []string{
			store.ReleasePartReview, candidate, reviewer.Name, strconv.FormatInt(position, 10),
		}
		if found {
			return recordAction(row, items), false
		}
		return Action{Kind: ActStep, IDs: []string{id}, Items: items}, false
	}
	return Action{}, true
}

// reviewRefused decides the release whose reviewer, whose step is row, recorded review failed for candidate. A reviewer
// that refused the candidate with findings (ReleaseRefusalClaims) sends the release back to planning as ActReplan: its
// output and reason are the review's reason, and its items name the epic's branch tip the candidate was made from as the
// replan's head. Once per tip: when an earlier replan already recorded that tip, the planner left the branch where it was,
// and the release stops with the review's reason and a note saying so. Any other failed review stops it with its reason.
func reviewRefused(rows Rows, row store.StepRow, review store.ReleaseRun, candidate string) Action {
	tip, ok := candidateTip(rows.Releases, candidate)
	if !ok || len(ReleaseRefusalClaims(row)) == 0 {
		return Action{Kind: ActStop, Output: review.Reason}
	}
	if _, replanned := firstMatch(rows.Releases, func(r store.ReleaseRun) bool {
		return r.Part == store.ReleasePartReplan && r.Head == tip
	}); replanned {
		return Action{Kind: ActStop, Output: review.Reason + "; the epic's branch has not moved since the last replan"}
	}
	return Action{
		Kind: ActReplan, Output: review.Reason, Reason: review.Reason,
		Items: []string{store.ReleasePartReplan, tip},
	}
}

// candidateTip returns the epic's branch tip that the latest passed candidate run whose head is candidate was made
// from, and false when no such run records one.
func candidateTip(runs []store.ReleaseRun, candidate string) (string, bool) {
	for _, run := range slices.Backward(runs) {
		if run.Part == store.ReleasePartCandidate && run.Passed && run.Head == candidate {
			return run.Start, run.Start != ""
		}
	}
	return "", false
}

// releaseReviewPosition returns the position of the review run of the release reviewer at index, counted from 1 among the
// n enabled release reviewers, which reviews the candidate of generation. Each generation numbers its reviews after those
// of the generation before it, as integratorPosition numbers the release integrator's round of each generation, so every
// review of an attempt has a position of its own.
func releaseReviewPosition(generation, n, index int64) int64 {
	return generation*n + index
}

// ReleaseRefusalClaims returns the claim of each finding that the release reviewer whose step is row refused its
// candidate with, or nil when its step did not refuse it. A step refuses by ending gate_failed with findings.
func ReleaseRefusalClaims(row store.StepRow) []string {
	if row.End != reviewEndGateFailed {
		return nil
	}
	findings, ok := reviewFailedFindings(row.Result)
	if !ok {
		return nil
	}
	claims := make([]string, 0, len(findings))
	for _, finding := range findings {
		claims = append(claims, finding["claim"])
	}
	return claims
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

// judgeAction decides the judge after the last release step, from the candidate. A release whose every step names a kind
// has no judge: each step's postcondition was checked in code before its run passed, so it ends released at once.
// Otherwise it ends released when the judge of this attempt passed, stops on one that failed, and otherwise starts the
// judge, waits while it runs, or records it. It stops without starting the judge when the owner stopped the release at
// this attempt.
func judgeAction(run store.NodeRun, rows Rows, candidate string) Action {
	steps := rows.Epic.ReleaseSteps
	if len(steps) > 0 && !slices.ContainsFunc(steps, func(step store.EpicReleaseStep) bool { return step.Kind == "" }) {
		return endingAction(outcomeReleased, rows)
	}
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
	if ownerStopped(rows, run.Attempt) {
		return Action{Kind: ActStop, Output: "the judge did not start: the owner stopped the release"}
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
