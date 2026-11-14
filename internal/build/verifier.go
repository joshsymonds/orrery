package build

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/joshsymonds/orrery/internal/store"
)

// VerifierRole is the role text of a verifier step: the read-only role that confirms or drops one finding a review
// raised.
//
//go:embed verifier.md
var VerifierRole string

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

// VerifierPrompt renders the prompt of a verifier step for one finding of task T<seq> of epic: the role, the task and
// its Requirements, the range the change spans from base to revision, and the finding with its citation, location,
// evidence and verify-by step.
func VerifierPrompt(
	epic store.EpicRow, task store.EpicTask, base, revision string, finding store.TaskFinding,
) string {
	body := slices.Concat(
		[]string{
			fmt.Sprintf("Task T%d of epic %s (%s): %s", task.Seq, epic.ID, epic.Title, task.Title),
			"Deliverable: " + task.Deliverable,
			requirementsHeading,
		},
		requirementLines(epic.Requirements, task.Requirements),
		[]string{
			fmt.Sprintf(
				"The change is %s..%s; this worktree is checked out at %s.", base, revision, revision,
			),
			"Finding " + finding.ID + ": " + finding.Claim,
			"Citation: " + finding.Citation,
			"Location: " + finding.Location,
			"Evidence: " + finding.Evidence,
			"Verify by: " + finding.VerifyBy,
			"Return exactly one result, for " + finding.ID + ".",
		},
	)
	return VerifierRole + "\n" + strings.Join(body, "\n")
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
