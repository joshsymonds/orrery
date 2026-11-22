package build

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/joshsymonds/orrery/internal/store"
)

// designGate is the gate a design judge's review stores under: the review of a design task past its last review gate.
const designGate = "design"

// The verdicts a design judge gives in its return.
const (
	designPass = "pass"
	designFail = "fail"
)

// designJudgeRole is the role text of a design judge step: the read-only role that checks a design task against the
// epic's contract. It is embedded from design_judge.md.
//
//go:embed design_judge.md
var designJudgeRole string

// DesignJudgeSchema is the schema a design judge's return must match: its status, its verdict and the reasons for it.
const DesignJudgeSchema = `{
  "title": "Design judge return",
  "type": "object",
  "additionalProperties": false,
  "required": ["status", "verdict", "reasons"],
  "properties": {
    "status": {"type": "string", "enum": ["done", "blocked"]},
    "verdict": {"type": "string", "enum": ["pass", "fail"]},
    "reasons": {"type": "array", "items": {"type": "string"}}
  }
}`

// designJudgeReturn is the part of a design judge's kept return decideDesign reads: its verdict and its reasons.
type designJudgeReturn struct {
	Verdict string   `json:"verdict"`
	Reasons []string `json:"reasons"`
}

// DesignJudgePrompt renders the prompt of a design judge step for task T<seq> of epic: the role, the epic's Approach
// and every Requirement it declares, the task's Deliverable, and the change as a range from the task's base to its
// revision.
func DesignJudgePrompt(epic store.EpicRow, task store.EpicTask) string {
	ids := make([]string, len(epic.Requirements))
	for i, requirement := range epic.Requirements {
		ids[i] = requirement.ID
	}
	body := slices.Concat(
		[]string{
			fmt.Sprintf("Task T%d of epic %s (%s): %s", task.Seq, epic.ID, epic.Title, task.Title),
			"Approach: " + epic.Approach,
			requirementsHeading,
		},
		requirementLines(epic.Requirements, ids),
		[]string{
			"Deliverable: " + task.Deliverable,
			fmt.Sprintf("The change is %s..%s; this worktree is checked out at %s.",
				task.Build.Base, task.Build.Revision, task.Build.Revision),
		},
	)
	return designJudgeRole + "\n" + strings.Join(body, "\n")
}

// DesignJudgeCheck is the return check of a design judge step. It refuses a value that does not decode, and a fail with
// no reasons, since a fail the judge gives no reason for says nothing the owner can act on.
func DesignJudgeCheck(value json.RawMessage) []string {
	var ret designJudgeReturn
	if err := json.Unmarshal(value, &ret); err != nil {
		return []string{"design judge return: " + err.Error()}
	}
	if ret.Verdict == designFail && len(ret.Reasons) == 0 {
		return []string{"reasons: a fail verdict gives at least one reason"}
	}
	return nil
}

// decideDesign returns the action for the design judge of a design task, once its build is past its last review gate.
// A design review already stored for the attempt decides the task: a fails verdict spends the attempt with the judge's
// reasons, and a holds verdict accepts it. With no step the judge starts. A done return that gives a verdict is stored
// as the design review, and the next pass decides the task on it. Any other end is a no verdict: its first reruns the
// judge, and its second spends the attempt with how it ended.
func decideDesign(view View) Action {
	build := view.Task.Build
	if review, ok := designReview(view); ok {
		if review.Verdict == verdictFails {
			return spend(view, "design judge: "+review.Reason)
		}
		return Action{Kind: ActAccept, Next: build, Reason: ReviewReviewed}
	}
	step := view.Step
	if step == nil {
		if view.Paused || view.Parked {
			return Action{Kind: ActNone}
		}
		return Action{
			Kind: ActStartStep, Unit: StageDesign,
			ID: StepID(unitScope(view.Epic, view.Task), view.Task.Seq, StageDesign, build.Attempt, build.Rerun, 0, 0),
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
		if verdict, reason, ok := designVerdict(step.Result); ok {
			review := store.TaskReview{
				Attempt: build.Attempt, Position: int64(len(view.ReviewGates)), Gate: designGate, Step: step.ID,
				Verdict: verdict, Reason: reason,
			}
			return Action{Kind: ActReview, Review: review, Next: build}
		}
	}
	if action, again := silentRerun(view); again {
		return action
	}
	return spend(view, endText(step))
}

// designReview returns the design review stored for the attempt of view's task, and false when none is stored yet.
func designReview(view View) (store.TaskReview, bool) {
	for _, review := range view.Task.Reviews {
		if review.Gate == designGate && review.Attempt == view.Task.Build.Attempt &&
			review.Position == int64(len(view.ReviewGates)) {
			return review, true
		}
	}
	return store.TaskReview{}, false
}

// designVerdict returns the review verdict a design judge's kept return gives, holds for pass and fails for fail, and
// the reasons it gives joined. It reports false when the return does not decode, its verdict is neither pass nor fail,
// or it fails with no reasons.
func designVerdict(result string) (string, string, bool) {
	var ret designJudgeReturn
	if err := json.Unmarshal([]byte(result), &ret); err != nil {
		return "", "", false
	}
	reason := strings.Join(ret.Reasons, "; ")
	switch {
	case ret.Verdict == designPass:
		return verdictHolds, reason, true
	case ret.Verdict == designFail && len(ret.Reasons) > 0:
		return verdictFails, reason, true
	}
	return "", "", false
}
