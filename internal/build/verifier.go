package build

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/taskspec"
)

// Subject is what a verifier step checks a finding of: the epic, its task at task stage and nil at epic stage, and the
// base and revision the change spans.
type Subject struct {
	Epic           store.EpicRow
	Task           *store.EpicTask
	Base, Revision string
}

// VerifierStart returns the start of the verifier step id that checks finding of subject, under agent: a read-only step
// at the subject's revision in the epic's repository, with the verifier schema and its return check. The step carries
// the epic's label, and its task's label when the subject is a task.
func VerifierStart(subject Subject, finding store.TaskFinding, id, agent string) enginehost.StepStart {
	epic := subject.Epic
	start := enginehost.StepStart{
		ID:          id,
		Agent:       agent,
		ReadOnly:    true,
		Workspace:   epic.Repository,
		Revision:    subject.Revision,
		Schema:      VerifierSchema,
		ReturnCheck: VerifierCheck,
		DoneValue:   "done",
		Epic:        epic.ID,
	}
	if subject.Task == nil {
		start.Prompt = epicVerifierPrompt(epic, subject.Base, subject.Revision, finding)
	} else {
		start.Prompt = VerifierPrompt(epic, *subject.Task, subject.Base, subject.Revision, finding)
		start.Task = taskLabel(subject.Task.Seq)
	}
	return start
}

// VerifierCommon is the text the finding verifier and the closure verifier roles share: the Freeze, Evidence and Return
// sections and the undetermined result line. It is embedded from verifier_common.md, and each role's text ends with it.
//
//go:embed verifier_common.md
var VerifierCommon string

// verifierOwn is the text of the finding verifier role that only it has: its title and intro, how it decides, and its
// confirmed and dropped results.
//
//go:embed verifier.md
var verifierOwn string

// VerifierRole returns the role text of a verifier step: the read-only role that confirms or drops one finding a review
// raised. It is verifier.md's own text followed by VerifierCommon.
func VerifierRole() string {
	return verifierOwn + VerifierCommon
}

// requirementsHeading opens the Requirements block of a verifier prompt.
const requirementsHeading = "Requirements:"

// VerifierSchema is the schema a verifier step's return must match: its status and one result per finding.
const VerifierSchema = `{
  "title": "Verifier return",
  "type": "object",
  "additionalProperties": false,
  "required": ["status", "results"],
  "properties": {
    "status": {"type": "string", "enum": ["done", "blocked"]},
    "results": {
      "type": "array",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["id", "evidence", "result"],
        "properties": {
          "id": {"type": "string"},
          "evidence": {"type": "string"},
          "result": {"type": "string", "enum": ["confirmed", "dropped", "undetermined"]}
        }
      }
    }
  }
}`

// VerifierPrompt renders the prompt of a verifier step for one finding of task T<seq> of epic: the role, the task, the
// lines naming its spec version when it is past version 1 (taskspec.Lines), its Deliverable, the gate-needed changes its
// implementer declared under the heading that says when each is in scope, its Requirements, the range the change spans
// from base to revision, and the finding with its citation, location, evidence and verify-by step.
func VerifierPrompt(
	epic store.EpicRow, task store.EpicTask, base, revision string, finding store.TaskFinding,
) string {
	body := slices.Concat(
		[]string{
			fmt.Sprintf("Task T%d of epic %s (%s): %s", task.Seq, epic.ID, epic.Title, task.Title),
		},
		taskspec.Lines(task),
		[]string{
			"Deliverable: " + task.Deliverable,
			reviewDeclaredHeading,
		},
		bullets(GateChangeItems(task.GateChanges)),
		[]string{requirementsHeading},
		requirementLines(epic.Requirements, task.Requirements),
		findingLines(base, revision, finding),
	)
	return VerifierRole() + "\n" + strings.Join(body, "\n")
}

// epicVerifierPrompt renders the prompt of a verifier step for one finding of epic: the role, the epic and every
// Requirement it declares, the gate-needed changes its tasks declared under the heading that says when each is in
// scope, the range the change spans from base to revision, and the finding.
func epicVerifierPrompt(epic store.EpicRow, base, revision string, finding store.TaskFinding) string {
	ids := make([]string, len(epic.Requirements))
	for i, requirement := range epic.Requirements {
		ids[i] = requirement.ID
	}
	body := slices.Concat(
		[]string{
			fmt.Sprintf("Epic %s (%s)", epic.ID, epic.Title),
			requirementsHeading,
		},
		requirementLines(epic.Requirements, ids),
		[]string{EpicDeclaredHeading},
		bullets(EpicGateChangeItems(epic.Tasks)),
		findingLines(base, revision, finding),
	)
	return VerifierRole() + "\n" + strings.Join(body, "\n")
}

// findingLines renders the range the change spans from base to revision, the finding with its citation, location,
// evidence and verify-by step, and the closing instruction to return one result for it.
func findingLines(base, revision string, finding store.TaskFinding) []string {
	return []string{
		fmt.Sprintf("The change is %s..%s; this worktree is checked out at %s.", base, revision, revision),
		"Finding " + finding.ID + ": " + finding.Claim,
		"Citation: " + finding.Citation,
		"Location: " + finding.Location,
		"Evidence: " + finding.Evidence,
		"Verify by: " + finding.VerifyBy,
		"Return exactly one result, for " + finding.ID + ".",
	}
}

// VerifierCheck is the return check of a verifier step. It refuses a return that does not hold exactly one result,
// because the step rules on one finding and a return with any other count does not say which result rules on it. A
// value that does not parse is refused with its parse error.
func VerifierCheck(value json.RawMessage) []string {
	var ret struct {
		Results []json.RawMessage `json:"results"`
	}
	if err := json.Unmarshal(value, &ret); err != nil {
		return []string{"verifier return: " + err.Error()}
	}
	if len(ret.Results) != 1 {
		return []string{fmt.Sprintf("results: want exactly one result, got %d", len(ret.Results))}
	}
	return nil
}
