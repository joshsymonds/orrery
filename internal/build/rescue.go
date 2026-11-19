package build

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/planner"
	"github.com/joshsymonds/orrery/internal/roles"
	"github.com/joshsymonds/orrery/internal/store"
)

// RescueRole is the role text of a rescue step: the read-only step that writes the version a task on the rescue rung is
// carried out under, before the one implementer attempt on that rung.
//
//go:embed rescue.md
var RescueRole string

// RescueSchema is the schema a rescue step's return must match: one flat object, every field required. A done return
// writes version, the task's rescue version, which every agent on the task reads as standing spec. A version may change
// the deliverable, test_changes, gate or evidence, but never the files and never the contract, so the schema has no
// files key.
const RescueSchema = `{
  "title": "Rescue version",
  "description": "A done return writes version, the task's rescue version, which every agent on the task reads, written as standing spec. A version may change the deliverable, test_changes, gate or evidence (an evidence change is red-checked at the task's base) but never the files and never the contract.",
  "type": "object",
  "additionalProperties": false,
  "required": ["status", "evidence", "version", "reason"],
  "properties": {
    "status": {"type": "string", "enum": ["done", "blocked"]},
    "evidence": {"type": "string",
      "description": "What the request comes down to, each fact as file:line or the exact command and its output. A blocked return gives what the prompt lacks that a version needs."},
    "version": {
      "type": "object",
      "additionalProperties": false,
      "required": ["changed", "deliverable", "test_changes", "gate", "evidence"],
      "properties": {
        "changed": {"type": "array",
          "items": {"type": "string", "enum": ["deliverable", "test_changes", "gate", "evidence"]}},
        "deliverable": {"type": "string"},
        "test_changes": {"type": "array", "items": {"type": "string"}},
        "gate": {"type": "string"},
        "evidence": {
          "type": "object",
          "properties": {
            "capture": {"type": "array", "items": {"type": "string"}},
            "limit": {"type": "integer"},
            "artifacts": {"type": "array", "items": {"type": "string"}},
            "supplied": {"type": "array", "items": {"type": "string"}},
            "conclusion": {"type": "string"},
            "judge": {"type": "string"},
            "kind": {"type": "string"},
            "tests": {"type": "array", "items": {"type": "string"}}
          },
          "required": ["capture", "limit", "artifacts", "supplied", "conclusion", "judge", "kind", "tests"],
          "additionalProperties": false
        }
      }
    },
    "reason": {"type": "string", "description": "Why this version is right. Recorded as a decision of the epic."}
  }
}`

// rescueReturn is a rescue step's return, as RescueSchema gives it. Version.TestChanges is read apart from its key,
// test_changes (decodeRescueReturn).
type rescueReturn struct {
	Status   string              `json:"status"`
	Evidence string              `json:"evidence"`
	Version  orchestratorVersion `json:"version"`
	Reason   string              `json:"reason"`
}

// decodeRescueReturn decodes a rescue step's return from raw JSON, with version's test_changes read from its key. A
// return that is not a JSON object of the schema's types is an error.
func decodeRescueReturn(raw []byte) (rescueReturn, error) {
	var ret rescueReturn
	if err := json.Unmarshal(raw, &ret); err != nil {
		return rescueReturn{}, fmt.Errorf("rescue return: %w", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return rescueReturn{}, fmt.Errorf("rescue return: %w", err)
	}
	if value, ok := fields[keyVersion]; ok {
		if err := decodeVersionTestChanges(value, &ret.Version); err != nil {
			return rescueReturn{}, err
		}
	}
	return ret, nil
}

// RescueCheck returns the return check of a rescue step on task: a value that does not decode is refused with its
// decode error, and any other value with one `field: reason` line for each rule of its shape that it breaks
// (rescueShape).
func RescueCheck(task store.EpicTask) func(json.RawMessage) []string {
	return func(value json.RawMessage) []string {
		ret, err := decodeRescueReturn(value)
		if err != nil {
			return []string{err.Error()}
		}
		return rescueShape(ret, task)
	}
}

// rescueShape returns one `field: reason` line for each rule of ret's shape that it breaks, for a rescue on task. A
// blocked return needs evidence and an empty version. Any other return is done: it needs a reason and a version that
// changes a field, the version's shape holds as a rerun's does (versionShape), and each of its test_changes is one of
// the task's files.
func rescueShape(ret rescueReturn, task store.EpicTask) []string {
	if ret.Status == statusBlocked {
		var refusals []string
		if strings.TrimSpace(ret.Evidence) == "" {
			refusals = append(refusals, "evidence: empty on a blocked return")
		}
		if !ret.Version.empty() {
			refusals = append(refusals, "version: not empty on a blocked return")
		}
		return refusals
	}
	refusals := nonEmpty("reason", ret.Reason)
	if len(ret.Version.Changed) == 0 {
		refusals = append(refusals, "version.changed: empty on a rescue")
	}
	refusals = append(refusals, versionShape(decisionRerun, ret.Version)...)
	for i, file := range ret.Version.TestChanges {
		if !slices.Contains(task.Files, file) {
			refusals = append(refusals, fmt.Sprintf("version.test_changes[%d]: not one of the task's files", i))
		}
	}
	return refusals
}

// hasRescue reports whether task has a version the rescue wrote, in any state.
func hasRescue(task store.EpicTask) bool {
	return slices.ContainsFunc(task.Versions, func(version store.TaskVersion) bool {
		return version.Source == store.SourceRescue
	})
}

// rescueDue reports whether view's task reaches its rescue unit now: it is on rung 3 with no version the rescue wrote,
// and no unit of the rung has started, so it is in implement with no step and no refs snapshot, or in red with no run.
// The rescue runs once per task, so a task with a rescue version is never due again.
func rescueDue(view View) bool {
	build := view.Task.Build
	if store.RungOf(build) != store.RescueRung || hasRescue(view.Task) {
		return false
	}
	switch build.Stage {
	case StageImplement:
		return view.Step == nil && build.Refs == ""
	case StageRed:
		return view.Run == nil
	}
	return false
}

// rescuing reports whether view's build is on its rescue unit: the orchestrate unit of rung 3 with no step, for a task
// with no rescue version. store.Stage names no stage of its own for a rescue, so the rescue is the orchestrate unit of
// its rung, which a request on that rung never leaves with an empty step.
func rescuing(view View) bool {
	build := view.Task.Build
	return build.Stage == StageOrchestrate && build.Step == "" && store.RungOf(build) == store.RescueRung &&
		!hasRescue(view.Task)
}

// decideRescue returns the action for the rescue unit: its orchestrator step, once it has ended. Without a step, the
// step starts under the derived id of the unit, once the epic is running. A stopped step holds the build, and a step
// the engine ended on a restart reruns. A done return with status done that passes the rescue's shape is carried out
// as the rescue's version (rescueDecision). A blocked return, a return that does not decode or breaks the shape, and
// any other end climb the task off their failure (rescueFail).
func decideRescue(view View) Action {
	build := view.Task.Build
	step := view.Step
	if step == nil {
		if view.Paused || view.Parked {
			return Action{Kind: ActNone}
		}
		return Action{
			Kind: ActStartStep, Unit: StageOrchestrate,
			ID: StepID(view.Epic, view.Task.Seq, StageOrchestrate, build.Attempt, build.Rerun, build.Decisions, 0),
		}
	}
	switch step.End {
	case "":
		return Action{Kind: ActNone}
	case endStopped:
		return holdStopped(build)
	case endEngineRestarted:
		return restartUnit(build)
	case endDone, endReturned:
		return rescueReturned(view, step)
	}
	return rescueFail(endText(step))
}

// rescueReturned returns the action for a rescue step that ended done or returned: the return is read against its
// shape, a blocked return climbs the task with its evidence, and a done return with status done is carried out.
func rescueReturned(view View, step *store.StepRow) Action {
	ret, err := decodeRescueReturn([]byte(step.Result))
	if err != nil {
		return rescueFail("its return does not decode: " + err.Error())
	}
	if lines := rescueShape(ret, view.Task); len(lines) > 0 {
		return rescueFail("its return is refused: " + strings.Join(lines, "; "))
	}
	if ret.Status == statusBlocked {
		return rescueFail("blocked: " + ret.Evidence)
	}
	if step.End == endDone && ret.Status == statusDone {
		return rescueDecision(view, ret)
	}
	return rescueFail(endText(step))
}

// rescueDecision returns the action that carries out ret, a rescue's done return, on the task of view. The version it
// writes is stored under the rescue's source, with the epic decision T<seq>-rescue, in one write, and the build moves
// to implement at its next rerun with no step, so the implementer's one attempt on the rung runs under that version.
// The build moves to red instead when the version changes the evidence, or when the build has no red run to hold the
// version to, so the new evidence is judged at the base first.
func rescueDecision(view View, ret rescueReturn) Action {
	build := view.Task.Build
	label := taskLabel(view.Task.Seq)
	next := build
	next.Stage = StageImplement
	if slices.Contains(ret.Version.Changed, "evidence") || build.RedRun == "" {
		next.Stage = StageRed
	}
	next.Rerun++
	next.NoVerdict = 0
	next.Step = ""
	version := versionInput(view, orchestratorReturn{Reason: ret.Reason, Version: ret.Version})
	version.Source = store.SourceRescue
	return Action{
		Kind: ActDecide, Next: next, Version: version,
		Decision: &store.TaskDecision{ID: label + "-rescue", Text: label + " rescue: " + ret.Reason},
	}
}

// rescueFail returns the action that climbs the task off its rescue, with reason as the failure: `rescue: <reason>`.
func rescueFail(reason string) Action {
	return Action{Kind: ActFail, Reason: "rescue: " + reason}
}

// rescueStart returns the start of the rescue step id for view's task in epic. The step runs the role file's
// orchestrator agent, or the planner's when the role file has no orchestrator key, read-only at the task's base in the
// epic's repository, with the rescue prompt, RescueSchema and its return check (RescueCheck).
func (r *Runner) rescueStart(
	ctx context.Context, epic store.EpicRow, view View, id string,
) (enginehost.StepStart, error) {
	agent, err := roles.AgentOr(r.getenv, roleOrchestrator, rolePlanner)
	if err != nil {
		return enginehost.StepStart{}, RoleError{Err: err}
	}
	prompt, err := r.rescuePrompt(ctx, epic, view)
	if err != nil {
		return enginehost.StepStart{}, err
	}
	task := view.Task
	return enginehost.StepStart{
		ID:          id,
		Agent:       agent,
		ReadOnly:    true,
		Workspace:   epic.Repository,
		Revision:    task.Build.Base,
		Prompt:      prompt,
		Schema:      RescueSchema,
		ReturnCheck: RescueCheck(task),
		DoneValue:   statusDone,
		Epic:        epic.ID,
		Task:        taskLabel(task.Seq),
	}, nil
}

// rescuePrompt renders the prompt of the rescue step for view's task in epic: the role text, then the sections
// Contract, Decisions, Task, Ladder, Worktree, Tasks and History, each under a `## ` heading and None. when it has no
// lines (planner.WriteSection). The Ladder comes before the Worktree, so the rungs the task climbed read first. It
// returns an error when the contract cannot be encoded, when the task's evidence item cannot, or when the epic's tasks
// cannot be read.
func (r *Runner) rescuePrompt(ctx context.Context, epic store.EpicRow, view View) (string, error) {
	task := view.Task
	var out strings.Builder
	fmt.Fprintln(&out, strings.TrimRight(RescueRole, "\n"))
	contract, err := planner.ContractLines(epic)
	if err != nil {
		return "", fmt.Errorf("contract section: %w", err)
	}
	planner.WriteSection(&out, "Contract", contract)
	planner.WriteSection(&out, "Decisions", planner.DecisionLines(epic, math.MaxInt64))
	taskSection, err := r.requestTaskLines(ctx, epic, task)
	if err != nil {
		return "", err
	}
	planner.WriteSection(&out, "Task", taskSection)
	planner.WriteSection(&out, "Ladder", rescueLadderLines(task))
	tree := readWorktree(ctx, ImplementWorktree(r.stateDir, epic.ID, task.Seq), task.Build.Base)
	planner.WriteSection(&out, "Worktree", tree.lines())
	peers, err := r.peers(ctx, epic)
	if err != nil {
		return "", err
	}
	planner.WriteSection(&out, "Tasks", peerLines(ctx, epic.Repository, task, peers))
	planner.WriteSection(&out, "History", r.historyLines(ctx, epic.ID, task))
	return out.String(), nil
}

// rescueLadderLines returns one line for each rung of task's ladder, in the format a planner's task lines give a rung:
// `  rung <n> <kind>`, then ` moved by <moved_by>: <cause>` when a failure moved the task onto it, then ` (gate <gate>:
// <assertion>)` when it has a gate, then ` -> <outcome>` when it has an outcome, and `: <reason>` when it has a reason.
func rescueLadderLines(task store.EpicTask) []string {
	lines := make([]string, 0, len(task.Rungs))
	for _, rung := range task.Rungs {
		line := fmt.Sprintf("  rung %d %s", rung.Rung, rung.Kind)
		if rung.MovedBy != "" {
			line += fmt.Sprintf(" moved by %s: %s", rung.MovedBy, rung.Cause)
		}
		if rung.Gate != "" {
			line += fmt.Sprintf(" (gate %s: %s)", rung.Gate, rung.Assertion)
		}
		if rung.Outcome != "" {
			line += " -> " + rung.Outcome
		}
		if rung.Reason != "" {
			line += ": " + rung.Reason
		}
		lines = append(lines, line)
	}
	return lines
}
