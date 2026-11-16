package kinds

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/joshsymonds/orrery/internal/store"
)

// The parts a node run's work ids are derived from: the check of a gate or command, the step of an agent, and each
// Requirement's evidence run, which is named by the Requirement's id.
const (
	checkPart = "check"
	stepPart  = "step"
)

// Gate is the gate kind. Its run's check runs the gate, and the run ends passed when the check ran, exited 0 and did
// not time out.
type Gate struct{}

// Command is the command kind. Its run's check runs the command's argv and it decides as Gate does.
type Command struct{}

// Evidence is the evidence kind. It ends passed when every Requirement that names an evidence item has an ended run
// that passed.
type Evidence struct{}

// Owned is the owned kind. It ends passed when every path its run recorded is owned by its paths.
type Owned struct{}

// Agent is the agent kind. Its run's step is a read-only agent step, and the run ends passed when the step ends done.
type Agent struct{}

// Decide decides a gate run as checkRun does.
func (Gate) Decide(node Node, rows Rows) Action {
	return checkRun(node.Run, rows)
}

// Decide decides a command run as checkRun does.
func (Command) Decide(node Node, rows Rows) Action {
	return checkRun(node.Run, rows)
}

// checkRun decides a gate or command run: it prepares the run's revision, starts its check, waits while the check
// runs, checks the run's refs, and ends passed or failed on the check.
func checkRun(run store.NodeRun, rows Rows) Action {
	if run.Revision == "" {
		return prepare(rows)
	}
	id := store.NodeWorkID(run.ID, run.Attempt, checkPart)
	i := slices.IndexFunc(rows.Checks, func(check store.NodeCheck) bool { return check.ID == id })
	if i < 0 {
		return Action{Kind: ActStart, IDs: []string{id}}
	}
	check := rows.Checks[i]
	if check.State == store.NodeRunning {
		return Action{Kind: ActNone}
	}
	if !run.RefsChecked {
		return Action{Kind: ActRefs}
	}
	return checkEnd(check)
}

// checkEnd ends a check's run failed when the check did not run, with why. Otherwise it ends passed when the check
// exited 0 without timing out, and failed when not, and its output is its stdout and stderr.
func checkEnd(check store.NodeCheck) Action {
	if check.NotRun != "" {
		return Action{Kind: ActEnd, Outcome: outcomeFailed, Output: check.NotRun}
	}
	outcome := outcomeFailed
	if !check.TimedOut && check.Exit == 0 {
		outcome = outcomePassed
	}
	return Action{Kind: ActEnd, Outcome: outcome, Output: streams(check.Stdout, check.Stderr)}
}

// streams returns stdout and stderr as one output: stdout, then stderr after a newline when both are set.
func streams(stdout, stderr string) string {
	if stdout == "" || stderr == "" {
		return stdout + stderr
	}
	return stdout + "\n" + stderr
}

// prepare returns none while the epic is paused or parked, and prepare otherwise: a paused or parked epic starts no
// work, so its runs record no revision.
func prepare(rows Rows) Action {
	if rows.Epic.Paused || rows.Epic.Parked != "" {
		return Action{Kind: ActNone}
	}
	return Action{Kind: ActPrepare}
}

// Decide decides an evidence run. It ends passed with a note when no Requirement names an evidence item, and when every
// such Requirement was dropped as already met at the base. Otherwise it prepares the run's revision, starts a run for
// each Requirement that has none and was not dropped, waits until every run has ended, checks the run's refs, and ends
// passed when every run passed.
func (Evidence) Decide(node Node, rows Rows) Action {
	named := itemRequirements(rows.Epic.Requirements)
	if len(named) == 0 {
		return Action{Kind: ActEnd, Outcome: outcomePassed, Output: "no Requirement names an evidence item"}
	}
	items := slices.DeleteFunc(named, droppedAsMet)
	if len(items) == 0 {
		return Action{
			Kind: ActEnd, Outcome: outcomePassed,
			Output: "every Requirement that names an evidence item was dropped as already met",
		}
	}
	if node.Run.Revision == "" {
		return prepare(rows)
	}
	var ids, missing []string
	runs := make([]store.EvidenceRun, 0, len(items))
	for _, item := range items {
		id := store.NodeWorkID(node.Run.ID, node.Run.Attempt, item.ID)
		i := slices.IndexFunc(rows.Evidence, func(run store.EvidenceRun) bool { return run.ID == id })
		if i < 0 {
			ids = append(ids, id)
			missing = append(missing, item.ID)
			continue
		}
		runs = append(runs, rows.Evidence[i])
	}
	if len(ids) > 0 {
		return Action{Kind: ActStart, IDs: ids, Items: missing}
	}
	for _, run := range runs {
		if run.State != runEnded {
			return Action{Kind: ActNone}
		}
	}
	if !node.Run.RefsChecked {
		return Action{Kind: ActRefs}
	}
	return evidenceEnd(items, runs)
}

// itemRequirements returns the Requirements that name an evidence item, in contract order.
func itemRequirements(requirements []store.EpicRequirement) []store.EpicRequirement {
	var items []store.EpicRequirement
	for _, requirement := range requirements {
		if !reflect.ValueOf(requirement.Item).IsZero() {
			items = append(items, requirement)
		}
	}
	return items
}

// evidenceEnd ends passed when every run passed, failed otherwise. Its output has one line per Requirement in items
// order, naming the run's phase and whether it passed.
func evidenceEnd(items []store.EpicRequirement, runs []store.EvidenceRun) Action {
	outcome := outcomePassed
	lines := make([]string, 0, len(runs))
	for i, run := range runs {
		verdict := outcomePassed
		if !run.Passed {
			verdict, outcome = outcomeFailed, outcomeFailed
		}
		lines = append(lines, fmt.Sprintf("%s %s: %s", items[i].ID, run.Phase, verdict))
	}
	return Action{Kind: ActEnd, Outcome: outcome, Output: strings.Join(lines, "\n")}
}

// Decide decides an owned run. It starts while the run has no result. It ends passed when every changed path its
// result lists is owned, and failed, listing the paths that are not, when one is not. A result that is not a JSON list
// of paths ends failed.
func (Owned) Decide(node Node, _ Rows) Action {
	if node.Run.Result == "" {
		return Action{Kind: ActStart}
	}
	var changed []string
	if err := json.Unmarshal([]byte(node.Run.Result), &changed); err != nil {
		return Action{
			Kind: ActEnd, Outcome: outcomeFailed, Output: "owned result is not a JSON list of paths: " + err.Error(),
		}
	}
	var outside []string
	for _, path := range changed {
		if !owns(node.Def.Settings.Paths, path) {
			outside = append(outside, path)
		}
	}
	if len(outside) > 0 {
		return Action{Kind: ActEnd, Outcome: outcomeFailed, Output: strings.Join(outside, "\n")}
	}
	return Action{Kind: ActEnd, Outcome: outcomePassed}
}

// owns reports whether path is one of paths, or lies under a path that ends in a slash.
func owns(paths []string, path string) bool {
	return slices.ContainsFunc(paths, func(owned string) bool {
		return path == owned || (strings.HasSuffix(owned, "/") && strings.HasPrefix(path, owned))
	})
}

// Decide decides an agent run. It prepares the run's revision, starts its step, waits while the step runs, and ends
// passed when the step ends done. Its output is the step's end, with its result when it is done and its reason when it
// is not. A run whose start was refused for good has no step and has its refusal as its result, so it ends failed with
// that result.
func (Agent) Decide(node Node, rows Rows) Action {
	if node.Run.Revision == "" {
		return prepare(rows)
	}
	id := store.NodeWorkID(node.Run.ID, node.Run.Attempt, stepPart)
	i := slices.IndexFunc(rows.Steps, func(step store.StepRow) bool { return step.ID == id })
	if i < 0 {
		if node.Run.Result != "" {
			return Action{Kind: ActEnd, Outcome: outcomeFailed, Output: node.Run.Result}
		}
		return Action{Kind: ActStart}
	}
	step := rows.Steps[i]
	switch step.End {
	case "":
		return Action{Kind: ActNone}
	case stepDone:
		return Action{Kind: ActEnd, Outcome: outcomePassed, Output: step.End + ": " + step.Result}
	default:
		return Action{Kind: ActEnd, Outcome: outcomeFailed, Output: step.End + ": " + step.Reason}
	}
}
