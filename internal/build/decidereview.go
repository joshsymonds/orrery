package build

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// The ends of a step that decideReview and decideVerify read. A step with no end is still open.
const (
	endDone            = "done"
	endReturned        = "returned"
	endStopped         = "stopped"
	endEngineRestarted = "engine_restarted"
	endGateFailed      = "gate_failed"
)

// statusDone is the status of a gate return that finished, and the verdicts a review stores.
const (
	statusDone          = "done"
	verdictHolds        = "holds"
	verdictFails        = "fails"
	verdictUndetermined = "undetermined"
)

// The results a gate return gives an assertion, and the results a finding stores.
const (
	resultUndetermined = "undetermined"
	resultConfirmed    = "confirmed"
	resultDropped      = "dropped"
	resultUndecided    = "undecided"
)

// gateReturn is the part of a review step's kept return decideReview reads: its status, its findings and the result
// each assertion got. Each finding is kept as its schema keys map to strings, so the decoder reads verify_by as the
// schema spells it.
type gateReturn struct {
	Status   string              `json:"status"`
	Findings []map[string]string `json:"findings"`
	Results  []gateResult        `json:"results"`
}

// gateResult is the result a gate return gave one assertion.
type gateResult struct {
	Result string `json:"result"`
}

// verifierReturn is the part of a verifier step's return decideVerify reads: the results it gave its finding.
type verifierReturn struct {
	Results []verifierResult `json:"results"`
}

// verifierResult is the result a verifier gave one finding, and the evidence it gave for it.
type verifierResult struct {
	Evidence string `json:"evidence"`
	Result   string `json:"result"`
}

// decideGreen returns the action for the green unit of a task: its evidence run at the commit, once it has ended. A
// test item's unit first decides its reproduction at the commit, and starts the run only once the reproduction's
// outcome is kept (decideRepro). A run that exited store.EnvironmentExit reruns or parks the unit as environmentRerun
// decides. A run that is not captured otherwise or not met spends the attempt. A run whose judge gave no verdict twice
// goes on to review with a note, and its first no verdict reruns the unit. A met run is kept as the green run and goes
// to the review gates; with none, decideReview accepts it unreviewed.
func decideGreen(view View) Action {
	build := view.Task.Build
	run := view.Run
	if run == nil {
		if view.Paused || view.Parked {
			return Action{Kind: ActNone}
		}
		if view.Task.Evidence.Kind == evidence.KindTest {
			if action, decided := decideRepro(view); decided {
				return action
			}
		}
		return Action{
			Kind: ActStartRun, Phase: StageGreen, Revision: build.Revision,
			ID: RunID(unitScope(view.Epic, view.Task), view.Task.Seq, StageGreen, build.Attempt, build.Rerun),
		}
	}
	if run.Ended.IsZero() {
		return Action{Kind: ActNone}
	}
	if run.NotCaptured == store.NotCapturedEnvironment {
		return environmentRerun(view, StageGreen+" capture")
	}
	if run.NotCaptured != "" {
		return spend(view, "green not captured: "+run.NotCaptured)
	}
	if run.Verdict == "" {
		if action, again := silentRerun(view); again {
			return action
		}
		note := fmt.Sprintf("%s green: the judge gave no verdict twice: %s", taskLabel(view.Task.Seq), run.JudgeReason)
		return noted(view, greenToReview(build, run), note)
	}
	if !run.Passed {
		return spend(view, fmt.Sprintf("green not met: exit %d, verdict %s", run.Exit, run.Verdict))
	}
	return Action{Kind: ActMove, Next: greenToReview(build, run)}
}

// greenToReview returns build moved to the review gates with run as its green run: at the first gate, with its rerun
// and its verdict counted from zero.
func greenToReview(build store.TaskBuild, run *store.EvidenceRun) store.TaskBuild {
	build.Stage = StageReview
	build.GreenRun = run.ID
	build.GatePos = 0
	build.FindingPos = 0
	build.Rerun = 0
	build.NoVerdict = 0
	return build
}

// decideRepro returns the action for the reproduction of a test item's green unit, once the unit has no run. A
// reproduction not yet started is started at the commit, and one that has not ended waits. One lost at a restart
// reruns the unit. One that did not run, or that exited 0 without timing out, is a note on the build (reproNoted): the
// reproduction is not a gate, so the green run starts once its note is kept. One that exited store.EnvironmentExit
// without timing out reruns or parks the unit as environmentRerun decides. It reports false when the green run starts.
func decideRepro(view View) (Action, bool) {
	build := view.Task.Build
	repro := view.Repro
	if repro == nil {
		return Action{
			Kind: ActRepro, Revision: build.Revision,
			ID: RunID(unitScope(view.Epic, view.Task), view.Task.Seq, unitRepro, build.Attempt, build.Rerun),
		}, true
	}
	if repro.State != store.NodeEnded {
		return Action{Kind: ActNone}, true
	}
	if repro.NotRun == store.LostAtRestart {
		return restartUnit(build), true
	}
	if repro.NotRun != "" {
		return reproNoted(view, "did not run: "+repro.NotRun)
	}
	if store.EnvironmentExited(repro.Exit, repro.TimedOut) {
		return environmentRerun(view, "reproduction"), true
	}
	if repro.Exit == 0 && !repro.TimedOut {
		return reproNoted(view, "exited 0 on the base with the commit's test files, so they pass without the rest of "+
			"the commit")
	}
	return Action{}, false
}

// reproNoted returns the action that records outcome, a reproduction's outcome, as a note on the task's build, and
// reports true; the note goes first, on a move that keeps the build at green. Once the note is kept, it reports false,
// so the green run starts. A reproduction is noted once per attempt.
func reproNoted(view View, outcome string) (Action, bool) {
	note := fmt.Sprintf("%s attempt %d reproduction: %s", taskLabel(view.Task.Seq), view.Task.Build.Attempt, outcome)
	if slices.Contains(view.Task.Notes, note) {
		return Action{}, false
	}
	return noted(view, view.Task.Build, note), true
}

// decideReview returns the action for the review of the build's gate, once its step has ended. A done review holds
// and moves to the next gate. A failed review with findings stores them and moves to their verification, or with
// verify off stores them settled as confirmed and unverified. Any other end gives no verdict: its first reruns the
// gate, and its second stores the review undetermined, with the reason it ended so, and moves to the next gate. Past
// the last gate the task is accepted as reviewed, or as unreviewed when the epic has no review gates.
func decideReview(view View) Action {
	build := view.Task.Build
	if build.GatePos >= int64(len(view.ReviewGates)) {
		if len(view.ReviewGates) == 0 {
			return Action{Kind: ActAccept, Next: build, Reason: ReviewUnreviewed}
		}
		return Action{Kind: ActAccept, Next: build, Reason: ReviewReviewed}
	}
	gate := view.ReviewGates[build.GatePos]
	step := view.Step
	if step == nil {
		if view.Paused || view.Parked {
			return Action{Kind: ActNone}
		}
		return Action{
			Kind: ActStartStep, Unit: StageReview,
			ID: StepID(
				unitScope(view.Epic, view.Task),
				view.Task.Seq,
				StageReview,
				build.Attempt,
				build.Rerun,
				build.GatePos,
				0,
			),
		}
	}
	switch step.End {
	case "":
		return Action{Kind: ActNone}
	case endStopped:
		return holdStopped(build)
	case endEngineRestarted:
		return restartUnit(build)
	case endDone:
		review := store.TaskReview{
			Attempt: build.Attempt, Position: build.GatePos, Gate: gate, Step: step.ID, Verdict: verdictHolds,
		}
		build.GatePos++
		build.Rerun = 0
		build.NoVerdict = 0
		return Action{Kind: ActReview, Review: review, Next: build}
	case endGateFailed:
		if findings, ok := raisedFindings(step.Result); ok {
			review := store.TaskReview{
				Attempt: build.Attempt, Position: build.GatePos, Gate: gate, Step: step.ID, Verdict: verdictFails,
				Findings: findings,
			}
			build.Stage = StageVerify
			build.FindingPos = 0
			if view.NoVerify {
				// Verify off settles the findings here, so the verify unit has none left to verify and spends the
				// attempt with them.
				review.Findings = settleUnverified(findings, step.ID)
				build.FindingPos = int64(len(findings))
			}
			build.Rerun = 0
			build.NoVerdict = 0
			return Action{Kind: ActReview, Review: review, Next: build}
		}
	}
	if action, again := silentRerun(view); again {
		return action
	}
	review := store.TaskReview{
		Attempt: build.Attempt, Position: build.GatePos, Gate: gate, Step: step.ID, Verdict: verdictUndetermined,
		Reason: endText(step),
	}
	build.GatePos++
	build.Rerun = 0
	build.NoVerdict = 0
	return Action{Kind: ActReview, Review: review, Next: build}
}

// decideVerify returns the action for the verifier of the build's finding, once its step has ended. A done or returned
// verifier stores the finding's result. Once every finding of the review is verified, a confirmed one spends the
// attempt, and otherwise the build moves to the next gate. Any other end gives no verdict: its first reruns the
// verifier, and its second stores the finding undecided, with the reason it ended so.
func decideVerify(view View) Action {
	build := view.Task.Build
	findings := reviewFindings(view)
	if build.FindingPos >= int64(len(findings)) {
		return settledFindings(view, findings)
	}
	step := view.Step
	if step == nil {
		if view.Paused || view.Parked {
			return Action{Kind: ActNone}
		}
		return Action{
			Kind: ActStartStep,
			Unit: StageVerify,
			ID: StepID(
				unitScope(view.Epic, view.Task),
				view.Task.Seq,
				StageVerify,
				build.Attempt,
				build.Rerun,
				build.GatePos,
				build.FindingPos,
			),
		}
	}
	switch step.End {
	case "":
		return Action{Kind: ActNone}
	case endStopped:
		return holdStopped(build)
	case endEngineRestarted:
		return restartUnit(build)
	case endDone:
		if ret, ok := parseVerifierReturn(step.Result); ok && len(ret.Results) > 0 {
			return verifiedAction(view, step.ID, verifiedResult(ret.Results[0].Result), ret.Results[0].Evidence)
		}
	case endReturned:
		return verifiedAction(view, step.ID, resultUndecided, "blocked: "+step.Reason)
	}
	if action, again := silentRerun(view); again {
		return action
	}
	return verifiedAction(view, step.ID, resultUndecided, endText(step))
}

// UnverifiedFindings returns the findings a failed review step raised in result, each settled as confirmed and
// unverified, with stepID as the step that raised it. An epic-stage verdict settles its findings through it the way a
// task-stage review does when its reviewer has verify off. It reports false when result is not a failed review with
// findings.
func UnverifiedFindings(stepID, result string) ([]store.TaskFinding, bool) {
	findings, ok := raisedFindings(result)
	if !ok {
		return nil, false
	}
	return settleUnverified(findings, stepID), true
}

// raisedFindings returns the findings of a failed review's kept return, in position order, each with the fields its
// schema gives. It reports false when result is not a failed review with findings.
func raisedFindings(result string) ([]store.TaskFinding, bool) {
	ret, ok := parseGateReturn(result)
	if !ok || !failedWithFindings(ret) {
		return nil, false
	}
	findings := make([]store.TaskFinding, 0, len(ret.Findings))
	for i, f := range ret.Findings {
		findings = append(findings, store.TaskFinding{
			Position: int64(i), ID: f["id"], Claim: f["claim"], Citation: f["citation"],
			Location: f["location"], Evidence: f["evidence"], VerifyBy: f["verify_by"],
		})
	}
	return findings, true
}

// settleUnverified marks each of findings confirmed and unverified, with stepID as the step that raised it, and
// returns them.
func settleUnverified(findings []store.TaskFinding, stepID string) []store.TaskFinding {
	for i := range findings {
		findings[i].Step = stepID
		findings[i].Result = resultConfirmed
		findings[i].Unverified = true
	}
	return findings
}

// failedWithFindings reports whether a review's kept return is a finished review that failed with findings: its
// status is done, no assertion is undetermined, and it raised at least one finding.
func failedWithFindings(ret gateReturn) bool {
	if ret.Status != statusDone || len(ret.Findings) == 0 {
		return false
	}
	for _, r := range ret.Results {
		if r.Result == resultUndetermined {
			return false
		}
	}
	return true
}

// parseGateReturn decodes a review step's kept return. It reports false when the return is not a gate return.
func parseGateReturn(result string) (gateReturn, bool) {
	var ret gateReturn
	err := json.Unmarshal([]byte(result), &ret)
	return ret, err == nil
}

// parseVerifierReturn decodes a verifier step's return. It reports false when the return is not a verifier return.
func parseVerifierReturn(result string) (verifierReturn, bool) {
	var ret verifierReturn
	err := json.Unmarshal([]byte(result), &ret)
	return ret, err == nil
}

// verifiedResult returns the result a done verifier stores for its finding: confirmed and dropped as given, anything
// else undecided.
func verifiedResult(result string) string {
	switch result {
	case resultConfirmed, resultDropped:
		return result
	default:
		return resultUndecided
	}
}

// verifiedAction stores result and reason as the finding the build is on, as the step stepID verified, and moves the
// build to the next finding.
func verifiedAction(view View, stepID, result, reason string) Action {
	build := view.Task.Build
	finding := store.TaskFindingResult{
		Attempt: build.Attempt, Review: build.GatePos, Position: build.FindingPos, Step: stepID,
		Result: result, Reason: reason,
	}
	build.FindingPos++
	build.Rerun = 0
	build.NoVerdict = 0
	return Action{Kind: ActFinding, Finding: finding, Next: build}
}

// settledFindings returns the action once every finding of the review is verified: a confirmed finding climbs the task
// with the claims it confirmed, moved by the finding at its gate, and otherwise the build moves to the next gate.
func settledFindings(view View, findings []store.TaskFinding) Action {
	var confirmed, assertions []string
	for _, f := range findings {
		if f.Result == resultConfirmed {
			confirmed = append(confirmed, fmt.Sprintf("confirmed: %s %s", f.ID, f.Claim))
			assertions = append(assertions, f.ID+" "+f.Claim)
		}
	}
	if len(confirmed) > 0 {
		gate := ""
		if pos := view.Task.Build.GatePos; pos >= 0 && pos < int64(len(view.ReviewGates)) {
			gate = view.ReviewGates[pos]
		}
		action := spend(view, strings.Join(confirmed, "; "))
		action.Move = store.RungMove{
			MovedBy: store.MovedByFinding, Gate: gate, Assertion: strings.Join(assertions, "; "),
		}
		return action
	}
	build := view.Task.Build
	build.Stage = StageReview
	build.GatePos++
	build.FindingPos = 0
	build.Rerun = 0
	build.NoVerdict = 0
	return Action{Kind: ActMove, Next: build}
}

// reviewFindings returns the findings of the review of the build's attempt and gate, nil when the task has no such
// review.
func reviewFindings(view View) []store.TaskFinding {
	for _, r := range view.Task.Reviews {
		if r.Attempt == view.Task.Build.Attempt && r.Position == view.Task.Build.GatePos {
			return r.Findings
		}
	}
	return nil
}

// holdStopped returns the action for a step the owner stopped: the build holds stopped until the owner resumes it.
func holdStopped(build store.TaskBuild) Action {
	build.Held = HeldStopped
	return Action{Kind: ActPause, Next: build}
}

// restartUnit returns the action for a step the engine ended on a restart: the unit reruns.
func restartUnit(build store.TaskBuild) Action {
	build.Rerun++
	return Action{Kind: ActMove, Next: build}
}

// endText is the reason a step that ended with no verdict gives: its end, then the step's reason.
func endText(step *store.StepRow) string {
	return step.End + ": " + step.Reason
}
