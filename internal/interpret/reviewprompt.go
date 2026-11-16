package interpret

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/joshsymonds/orrery/internal/build"
	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/store"
)

// closureRole is the role text of a closure verifier step: the read-only role that decides whether one confirmed finding
// still holds at the corrected revision.
//
//go:embed closure.md
var closureRole string

// ClosureSchema is the schema a closure verifier step's return must match: its status and one result for the finding,
// closed, open or undetermined.
const ClosureSchema = `{
  "title": "Closure return",
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
          "result": {"type": "string", "enum": ["closed", "open", "undetermined"]}
        }
      }
    }
  }
}`

// epicVerifierStart returns the start of the epic-stage verifier step id for finding, a ledger finding of the review of
// epic over base..frozen. The step runs agent read-only at frozen, with the verifier schema and its return check. Its
// Task label is set by the caller.
func epicVerifierStart(
	epic store.EpicRow, base, frozen string, finding store.LedgerFinding, id, agent string,
) enginehost.StepStart {
	return enginehost.StepStart{
		ID:          id,
		ReadOnly:    true,
		Workspace:   epic.Repository,
		Revision:    frozen,
		Agent:       agent,
		Schema:      build.VerifierSchema,
		Prompt:      epicVerifierPrompt(epic, base, frozen, finding),
		ReturnCheck: build.VerifierCheck,
		DoneValue:   "done",
		Epic:        epic.ID,
	}
}

// epicVerifierPrompt renders the prompt of the epic-stage verifier for finding: the role, the epic and its Requirements,
// the range the change spans from base to frozen, and the finding with its claim, citation, location, evidence and
// verify-by step.
func epicVerifierPrompt(epic store.EpicRow, base, frozen string, finding store.LedgerFinding) string {
	body := slices.Concat(
		[]string{
			fmt.Sprintf("Epic %s (%s)", epic.ID, epic.Title),
			"Requirements:",
		},
		epicRequirementLines(epic.Requirements),
		[]string{
			fmt.Sprintf("The change is %s..%s; this worktree is checked out at %s.", base, frozen, frozen),
			"Finding " + finding.ID + ": " + finding.Claim,
			"Citation: " + finding.Citation,
			"Location: " + finding.Location,
			"Evidence: " + finding.Evidence,
			"Verify by: " + finding.VerifyBy,
			"Return exactly one result, for " + finding.ID + ".",
		},
	)
	return build.VerifierRole + "\n" + strings.Join(body, "\n")
}

// epicRequirementLines returns one bullet per Requirement of an epic, in declared order.
func epicRequirementLines(requirements []store.EpicRequirement) []string {
	lines := make([]string, len(requirements))
	for i, r := range requirements {
		lines[i] = "- " + r.ID + ": " + r.Text
	}
	return lines
}

// closureStart returns the start of the closure verifier step id for finding, a confirmed ledger finding, at revision,
// the corrected revision the closure round runs at. The step runs agent read-only at revision, with the closure schema
// and its return check. Its Task label is set by the caller.
func closureStart(
	epic store.EpicRow, frozen, revision string, finding store.LedgerFinding, id, agent string,
) enginehost.StepStart {
	return enginehost.StepStart{
		ID:          id,
		ReadOnly:    true,
		Workspace:   epic.Repository,
		Revision:    revision,
		Agent:       agent,
		Schema:      ClosureSchema,
		Prompt:      closurePrompt(frozen, revision, finding),
		ReturnCheck: closureCheck,
		DoneValue:   "done",
		Epic:        epic.ID,
	}
}

// closurePrompt renders the prompt of the closure verifier for finding: the role, the range the change spans from frozen
// to revision, and the finding with its claim, citation, location, evidence, verify-by step and the confirmation that
// stood at the freeze.
func closurePrompt(frozen, revision string, finding store.LedgerFinding) string {
	body := []string{
		fmt.Sprintf("The change is %s..%s; this worktree is checked out at %s.", frozen, revision, revision),
		"Finding " + finding.ID + ": " + finding.Claim,
		"Citation: " + finding.Citation,
		"Location: " + finding.Location,
		"Evidence: " + finding.Evidence,
		"Verify by: " + finding.VerifyBy,
		"Confirmation: " + finding.Reason,
		"Return exactly one result, for " + finding.ID + ".",
	}
	return closureRole + "\n" + strings.Join(body, "\n")
}

// closureCheck is the return check of a closure verifier step. It refuses a return that does not hold exactly one result,
// because the step rules on one finding and a return with any other count does not say which result rules on it. A
// value that does not parse is refused with its parse error.
func closureCheck(value json.RawMessage) []string {
	var ret struct {
		Results []json.RawMessage `json:"results"`
	}
	if err := json.Unmarshal(value, &ret); err != nil {
		return []string{"closure return: " + err.Error()}
	}
	if len(ret.Results) != 1 {
		return []string{fmt.Sprintf("results: want exactly one result, got %d", len(ret.Results))}
	}
	return nil
}
