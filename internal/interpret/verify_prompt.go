package interpret

import (
	_ "embed"
	"fmt"
	"strings"
	"time"

	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/planner"
	"github.com/joshsymonds/orrery/internal/store"
)

// verifyRole is the role text of the verification step: the read-only judge that chooses which Done gates run at a
// commit and which Requirements are captured there, each with a reason.
//
//go:embed verify.md
var verifyRole string

// verifyLinesMax bounds the lines of git's diff stat and of the changed paths a verification prompt gives. A longer list
// is cut, with a line that says how many lines it holds, and the step reads the rest with git.
const verifyLinesMax = 400

// VerifySchema is the schema the verification step returns: its status, and its choice of each Done gate and of each
// Requirement it decides on, each with its reason. kinds.DecodeVerifyReturn reads it.
const VerifySchema = `{
  "title": "Verification choice",
  "type": "object",
  "additionalProperties": false,
  "required": ["status", "gates", "requirements"],
  "properties": {
    "status": {"type": "string", "enum": ["done"]},
    "gates": {
      "type": "array",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["gate", "run", "reason"],
        "properties": {
          "gate": {"type": "string", "description": "The Done gate's name."},
          "run": {"type": "boolean", "description": "true to run the gate at the commit, false to skip it."},
          "reason": {"type": "string", "description": "Why the gate runs or is skipped."}
        }
      }
    },
    "requirements": {
      "type": "array",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["requirement", "capture", "reason"],
        "properties": {
          "requirement": {"type": "string", "description": "The Requirement's id, such as R2."},
          "capture": {"type": "boolean", "description": "true to capture it at the commit, false not to."},
          "reason": {"type": "string", "description": "Why it is captured or not."}
        }
      }
    }
  }
}`

// verifyPrompt renders the prompt of a verification step from facts: the role text, then the sections Commit, Diff
// stat, Changed paths, Conflict resolutions, Tasks, Done gates, Passed at this commit, Requirements and Captured
// whatever you choose, each under a `## ` heading and None. when it has no lines (planner.WriteSection). An evidence
// item that does not encode fails it.
func verifyPrompt(facts verifyFacts) (string, error) {
	requirements, err := planner.RequirementItems(facts.requirements)
	if err != nil {
		return "", fmt.Errorf("verification prompt of %s: %w", facts.what, err)
	}
	var out strings.Builder
	fmt.Fprintln(&out, strings.TrimRight(verifyRole, "\n"))
	planner.WriteSection(&out, "Commit", []string{
		"Commit: " + facts.head + ", " + facts.what,
		"Its change runs from: " + facts.base,
	})
	planner.WriteSection(&out, "Diff stat", cappedLines(facts.stat))
	planner.WriteSection(&out, "Changed paths", cappedLines(facts.paths))
	planner.WriteSection(&out, "Conflict resolutions", facts.resolutions)
	planner.WriteSection(&out, "Tasks", verifyTaskLines(facts.tasks, facts.merged))
	planner.WriteSection(&out, "Done gates", doneGateLines(facts.gates))
	planner.WriteSection(&out, "Passed at this commit", passedLines(facts.passed, facts.head))
	planner.WriteSection(&out, "Requirements", requirements)
	planner.WriteSection(&out, "Captured whatever you choose", floorLines(facts.scope.Floor))
	return out.String(), nil
}

// cappedLines returns the lines of text that are not blank, cut to verifyLinesMax with a last line that says how many
// it holds when it is longer.
func cappedLines(text string) []string {
	lines := nonEmptyLines(text)
	if len(lines) <= verifyLinesMax {
		return lines
	}
	return append(lines[:verifyLinesMax:verifyLinesMax], fmt.Sprintf(
		"These are the first %d of %d lines; read the rest with git.", verifyLinesMax, len(lines)))
}

// verifyTaskLines returns each of tasks as `- T<n> <title>`, marked as the task the round merges when its seq is
// merged, then its deliverable, the Requirements it claims and those it contributes to.
func verifyTaskLines(tasks []store.EpicTask, merged int64) []string {
	lines := make([]string, 0, 4*len(tasks))
	for _, task := range tasks {
		head := fmt.Sprintf("- T%d %s", task.Seq, task.Title)
		if task.Seq == merged {
			head += " (the task this round merges)"
		}
		lines = append(lines, head, "  deliverable: "+task.Deliverable,
			"  claims: "+joinedOrNone(task.Requirements), "  contributes to: "+joinedOrNone(task.Contributes))
	}
	return lines
}

// doneGateLines returns each Done gate of gates, in order, as a verification reads it. A command gate is `- <name>:
// command <argv>` with its argv as a JSON array, then its limit, the pools it needs, `always: runs on every
// verification, whatever the choice says` when it is declared always, and its note when it has one. A gate that does
// not resolve is `- <name>: does not resolve: <why>`, with the line that says it runs whatever the choice says.
func doneGateLines(gates []resolvedGate) []string {
	lines := make([]string, 0, 4*len(gates))
	for _, gate := range gates {
		if gate.unresolved != "" {
			lines = append(lines, fmt.Sprintf("- %s: does not resolve: %s", gate.name, gate.unresolved),
				"  it runs whatever the choice says, and fails as a gate that does not resolve does")
			continue
		}
		needs := make([]string, 0, len(gate.gate.Needs))
		for _, need := range gate.gate.Needs {
			needs = append(needs, fmt.Sprintf("%s x %d", need.Pool, need.Count))
		}
		lines = append(lines, fmt.Sprintf("- %s: command %s", gate.name, jsonList(gate.gate.Argv)),
			fmt.Sprintf("  limit: %ds", int64(gate.gate.Limit/time.Second)), "  needs: "+joinedOrNone(needs))
		if gate.gate.Always {
			lines = append(lines, "  always: runs on every verification, whatever the choice says")
		}
		if gate.gate.Note != "" {
			lines = append(lines, "  note: "+gate.gate.Note)
		}
	}
	return lines
}

// passedLines returns each of passed, a Done gate whose check passed at head under a task integrator round, as `- <gate>:
// its check passed at <head> under T<seq>'s round <round>`.
func passedLines(passed []kinds.PassedGate, head string) []string {
	lines := make([]string, 0, len(passed))
	for _, gate := range passed {
		lines = append(lines, fmt.Sprintf("- %s: its check passed at %s under T%d's round %d", gate.Gate, head,
			gate.Task, gate.Round))
	}
	return lines
}

// floorLines returns the Requirements a verification captures whatever its step chooses, as one line, or none.
func floorLines(floor []string) []string {
	if len(floor) == 0 {
		return nil
	}
	return []string{strings.Join(floor, ", ") + ": a task above claims or contributes to each"}
}
