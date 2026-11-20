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

// Cost is the cost kind. Its run's check measures the epic's first Done gate at the base, and the run ends passed once
// the check has ended, whatever it exited: the cost is measured, never judged.
type Cost struct{}

// Evidence is the evidence kind. It ends passed when every Requirement that names an evidence item has an ended run
// that passed.
type Evidence struct{}

// Owned is the owned kind. It ends passed when every path its run recorded is owned by its paths.
type Owned struct{}

// Agent is the agent kind. Its run's step is a read-only agent step, and the run ends passed when the step ends done.
type Agent struct{}

// Decide decides a gate run as checkRun does.
func (Gate) Decide(node Node, rows Rows) Action {
	return checkRun(node, rows)
}

// Decide decides a command run as checkRun does.
func (Command) Decide(node Node, rows Rows) Action {
	return checkRun(node, rows)
}

// Decide decides a cost run as checkRun does, and ends it passed once its check has ended, with the same output: a
// check that exits nonzero still passes, since the cost is measured and never judged.
func (Cost) Decide(node Node, rows Rows) Action {
	act := checkRun(node, rows)
	if act.Kind == ActEnd {
		act.Outcome = outcomePassed
	}
	return act
}

// checkRun decides a gate or command run: it prepares the run's revision, starts its check, waits while the check runs,
// and starts the check's rerun when it exited store.EnvironmentExit. A check whose last rerun exits so parks the run.
// Otherwise it checks the run's refs, and ends passed or failed on the check.
func checkRun(node Node, rows Rows) Action {
	run := node.Run
	if run.Revision == "" {
		return prepare(rows)
	}
	check, id, outcome := checkChain(run, rows.Checks, checkPart, false)
	switch {
	case outcome == chainNone:
		return Action{Kind: ActStart, IDs: []string{id}}
	case outcome == chainRunning:
		return Action{Kind: ActNone}
	case outcome == chainEnvironment:
		return Action{Kind: ActPark, Output: environmentPark(node.Def.Kind + " " + run.Path)}
	case !run.RefsChecked:
		return Action{Kind: ActRefs}
	}
	return checkEnd(check)
}

// chainOutcome is how a run of a part ended, as the walk over the reruns of the part reads it.
type chainOutcome int

const (
	// chainNone is a run that has not started.
	chainNone chainOutcome = iota
	// chainRunning is a run that has started and has not ended.
	chainRunning
	// chainLost is a run lost at a restart, which a kind that reruns lost runs reruns once.
	chainLost
	// chainEnvironment is a run that exited store.EnvironmentExit without timing out.
	chainEnvironment
	// chainEnded is a run that ended any other way, which is decided as it always was.
	chainEnded
)

// chainWalk walks the runs of part at run's attempt, the first run and then each rerun of it in turn, and returns the work
// id of the run that decides part with how that run ended. outcome gives how the run with a work id ended. A run that ends
// where rerunsAfter says the next run of the part follows is passed over, so the walk stops at the first run that decides.
func chainWalk(run store.NodeRun, part string, outcome func(id string) chainOutcome) (string, chainOutcome) {
	for k := 0; ; k++ {
		id := store.NodeWorkID(run.ID, run.Attempt, runWork(part, k))
		ended := outcome(id)
		if !rerunsAfter(k, ended) {
			return id, ended
		}
	}
}

// rerunsAfter reports whether the run at position k of a part, which ended with outcome, is followed by a rerun. A run lost
// at a restart is rerun once, when it is the first run. A run that exited store.EnvironmentExit is rerun until the part has
// store.EnvironmentReruns reruns, so the last one that exits so decides the part.
func rerunsAfter(k int, outcome chainOutcome) bool {
	switch outcome {
	case chainLost:
		return k == 0
	case chainEnvironment:
		return k < store.EnvironmentReruns
	case chainNone, chainRunning, chainEnded:
		return false
	}
	return false
}

// checkOutcome returns how the check with id ended, as a walk over the reruns of a part reads it. A check lost at a restart
// is chainLost only when lostReruns is set, and is chainEnded otherwise.
func checkOutcome(checks []store.NodeCheck, id string, lostReruns bool) chainOutcome {
	check, ok := checkByID(checks, id)
	switch {
	case !ok:
		return chainNone
	case check.State == store.NodeRunning:
		return chainRunning
	case lostReruns && check.NotRun == store.LostAtRestart:
		return chainLost
	case check.NotRun == "" && store.EnvironmentExited(check.Exit, check.TimedOut):
		return chainEnvironment
	}
	return chainEnded
}

// evidenceOutcome returns how the evidence run with id ended, as a walk over the reruns of a part reads it. A run lost at a
// restart is chainLost only when lostReruns is set, and is chainEnded otherwise.
func evidenceOutcome(runs []store.EvidenceRun, id string, lostReruns bool) chainOutcome {
	run, ok := evidenceByID(runs, id)
	switch {
	case !ok:
		return chainNone
	case run.State != runEnded:
		return chainRunning
	case lostReruns && run.NotCaptured == store.LostAtRestart:
		return chainLost
	case store.EnvironmentExited(run.Exit, run.TimedOut):
		return chainEnvironment
	}
	return chainEnded
}

// checkChain returns the check that decides part of run, with its work id and how it ended. See chainWalk.
func checkChain(
	run store.NodeRun, checks []store.NodeCheck, part string, lostReruns bool,
) (store.NodeCheck, string, chainOutcome) {
	id, outcome := chainWalk(run, part, func(id string) chainOutcome {
		return checkOutcome(checks, id, lostReruns)
	})
	check, _ := checkByID(checks, id)
	return check, id, outcome
}

// evidenceChain returns the evidence run that decides part of run, with its work id and how it ended. See chainWalk.
func evidenceChain(
	run store.NodeRun, runs []store.EvidenceRun, part string, lostReruns bool,
) (store.EvidenceRun, string, chainOutcome) {
	id, outcome := chainWalk(run, part, func(id string) chainOutcome {
		return evidenceOutcome(runs, id, lostReruns)
	})
	evidence, _ := evidenceByID(runs, id)
	return evidence, id, outcome
}

// EvidenceRunID returns the work id of the evidence run that decides part of run, as Evidence.Decide reads it: the first
// run, or the rerun that a store.EnvironmentExit started. It is the first run's id when no run of part is stored.
func EvidenceRunID(run store.NodeRun, runs []store.EvidenceRun, part string) string {
	_, id, _ := evidenceChain(run, runs, part, false)
	return id
}

// environmentPark returns the reason a run parks with when its last run exited store.EnvironmentExit: what ran, and that the
// exit is an environment result, not a verdict.
func environmentPark(what string) string {
	const verdict = "an environment result, not a verdict"
	return fmt.Sprintf("%s exited %d on its last run: %s", what, store.EnvironmentExit, verdict)
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
// each Requirement that has none and was not dropped, and starts the rerun of a capture that exited
// store.EnvironmentExit, parking the run when a capture's last rerun exits so. Otherwise it waits until every run has
// ended, checks the run's refs, and ends passed when every run passed.
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
		run, id, outcome := evidenceChain(node.Run, rows.Evidence, item.ID, false)
		switch outcome {
		case chainNone:
			ids = append(ids, id)
			missing = append(missing, item.ID)
		case chainEnvironment:
			return Action{Kind: ActPark, Output: environmentPark("capture " + item.ID)}
		case chainRunning, chainLost, chainEnded:
			runs = append(runs, run)
		}
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
