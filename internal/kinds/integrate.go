package kinds

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// The kind names the integrate rules read node runs by.
const (
	kindEvidence  = "evidence"
	kindIntegrate = "integrate"
)

// Integrate is the integrate kind. Its run composes a batch of accepted tasks on the stored base into a candidate, runs
// the epic's Done gates, captures each Requirement at the candidate and reproduces each test item's red at the epic's
// base, and then ends red or advances the epic to the candidate.
type Integrate struct{}

// integrateResult is the Result an integrate run records when it claims its batch: the tasks compose made into commits
// and the tasks it left out, each with its reason.
type integrateResult struct {
	Composed []json.RawMessage `json:"composed"`
	LeftOut  []struct {
		Reason string `json:"reason"`
	} `json:"leftOut"`
}

// Decide decides an integrate run. Without a revision it prepares the batch, or ends passed when the batch is empty.
// With one it reads what compose made of the batch, and ends failed when nothing was composed. Otherwise it walks the
// Done gates, then the captures and then the reproductions of the test items, and settles the run on what they found.
func (Integrate) Decide(node Node, rows Rows) Action {
	run := node.Run
	if run.Revision == "" {
		return prepareBatch(run, rows)
	}
	var result integrateResult
	if err := json.Unmarshal([]byte(run.Result), &result); err != nil {
		return Action{
			Kind: ActEnd, Outcome: outcomeFailed,
			Output: "integrate result is not a JSON compose result: " + err.Error(),
		}
	}
	leftOut := make([]string, 0, len(result.LeftOut))
	for _, left := range result.LeftOut {
		leftOut = append(leftOut, left.Reason)
	}
	if len(result.Composed) == 0 {
		return Action{Kind: ActEnd, Outcome: outcomeFailed, Output: outputOf(nil, leftOut)}
	}
	gate, act, settled := doneGates(run, rows)
	if !settled {
		return act
	}
	if gate != "" {
		return settle(run, []string{"Done gate " + gate + " failed"}, nil, leftOut)
	}
	captured, act, settled := captures(run, rows)
	if !settled {
		return act
	}
	if act, settled = reproductions(run, rows); !settled {
		return act
	}
	return settle(run, captureCauses(rows, captured), requirementLines(rows, captured), leftOut)
}

// prepareBatch decides an integrate run that has no revision. A paused or parked epic starts no batch, and a batch of
// no task ends passed. Otherwise the run prepares with the batch's tasks.
func prepareBatch(run store.NodeRun, rows Rows) Action {
	if action := prepare(rows); action.Kind == ActNone {
		return action
	}
	tasks := batchOf(run.Path, rows.Epic.Tasks)
	if len(tasks) == 0 {
		return Action{Kind: ActEnd, Outcome: outcomePassed, Output: "no accepted task to integrate"}
	}
	return Action{Kind: ActPrepare, Tasks: tasks}
}

// batchOf returns the seqs of the batch of the integrate run at path, in seq order: the tasks that run claimed, or else
// the accepted tasks no run has claimed.
func batchOf(path string, tasks []store.EpicTask) []int64 {
	var claimed, accepted []int64
	for i := range tasks {
		switch {
		case tasks[i].Batch == path:
			claimed = append(claimed, tasks[i].Seq)
		case tasks[i].Status == taskDone && tasks[i].Batch == "":
			accepted = append(accepted, tasks[i].Seq)
		}
	}
	batch := claimed
	if len(batch) == 0 {
		batch = accepted
	}
	slices.Sort(batch)
	return batch
}

// doneGates walks the epic's Done gates in order. It returns the name of the first gate whose check failed, and settled
// true once every gate passed or one failed. Until then it returns the action the gate that is not decided asks for.
func doneGates(run store.NodeRun, rows Rows) (string, Action, bool) {
	for i, gate := range rows.Epic.DoneGates {
		check, act, decided := gateCheck(run, i, gate, rows.Checks)
		if !decided {
			return "", act, false
		}
		if !checkPassed(check) {
			return gate, Action{}, true
		}
	}
	return "", Action{}, true
}

// gateCheck returns the check that decides Done gate i of the run: its check, or the rerun of a check lost at a restart.
// It returns decided false, with the action the gate asks for, while that check is not decided.
func gateCheck(run store.NodeRun, i int, gate string, checks []store.NodeCheck) (store.NodeCheck, Action, bool) {
	part := fmt.Sprintf("done %d", i+1)
	id := store.NodeWorkID(run.ID, run.Attempt, part)
	check, ok := checkByID(checks, id)
	if !ok {
		return store.NodeCheck{}, Action{Kind: ActStart, IDs: []string{id}, Items: []string{gate}}, false
	}
	if check.State == store.NodeRunning {
		return store.NodeCheck{}, Action{Kind: ActNone}, false
	}
	if check.NotRun != store.LostAtRestart {
		return check, Action{}, true
	}
	rerunID := store.NodeWorkID(run.ID, run.Attempt, rerunWork(part))
	rerun, ok := checkByID(checks, rerunID)
	switch {
	case !ok:
		return store.NodeCheck{}, Action{Kind: ActStart, IDs: []string{rerunID}, Items: []string{gate}}, false
	case rerun.State == store.NodeRunning:
		return store.NodeCheck{}, Action{Kind: ActNone}, false
	case rerun.NotRun == store.LostAtRestart:
		return store.NodeCheck{}, Action{
			Kind:   ActPark,
			Output: fmt.Sprintf("integrate %s: Done gate %s lost at a restart twice", run.Path, gate),
		}, false
	}
	return rerun, Action{}, true
}

// checkPassed reports whether a check ran, exited 0 and did not time out.
func checkPassed(check store.NodeCheck) bool {
	return check.NotRun == "" && !check.TimedOut && check.Exit == 0
}

// captures decides the captures of the Requirements that name an evidence item. A capture with no run starts, and one
// lost at a restart is started again as its rerun. A capture still running makes it wait, and a rerun lost at a restart
// parks the run. Once every capture has ended, it returns the capture each Requirement ended with.
func captures(run store.NodeRun, rows Rows) (map[string]store.EvidenceRun, Action, bool) {
	captured := make(map[string]store.EvidenceRun)
	var ids, items, lost []string
	running := false
	for _, requirement := range itemRequirements(rows.Epic.Requirements) {
		capture, work, ok := captureFor(rows.Evidence, run, requirement.ID)
		switch {
		case !ok:
			ids = append(ids, work)
			items = append(items, requirement.ID)
		case capture.State != runEnded:
			running = true
		case capture.NotCaptured == store.LostAtRestart:
			lost = append(lost, requirement.ID)
		default:
			captured[requirement.ID] = capture
		}
	}
	switch {
	case len(ids) > 0:
		return nil, Action{Kind: ActCapture, IDs: ids, Items: items}, false
	case running:
		return nil, Action{Kind: ActNone}, false
	case len(lost) > 0:
		return nil, Action{
			Kind:   ActPark,
			Output: fmt.Sprintf("integrate %s: capture %s lost at a restart twice", run.Path, lost[0]),
		}, false
	}
	return captured, Action{}, true
}

// captureFor returns the capture of Requirement id that the integrate run decides on, and its work id. That is the
// capture, or its rerun once the capture was lost at a restart. ok is false when the run has not started it.
func captureFor(runs []store.EvidenceRun, run store.NodeRun, id string) (store.EvidenceRun, string, bool) {
	work := store.NodeWorkID(run.ID, run.Attempt, captureWork(id))
	capture, ok := evidenceByID(runs, work)
	if ok && capture.NotCaptured == store.LostAtRestart {
		work = store.NodeWorkID(run.ID, run.Attempt, rerunWork(captureWork(id)))
		capture, ok = evidenceByID(runs, work)
	}
	return capture, work, ok
}

// reproductions decides the reproductions of the Requirements that name a test item, once the captures have settled. A
// reproduction with no check starts, and one lost at a restart is started again as its rerun. A reproduction still
// running makes the run wait, and a rerun lost at a restart parks it. A reproduction's result adds no cause, so it
// returns settled once every reproduction has ended.
func reproductions(run store.NodeRun, rows Rows) (Action, bool) {
	var ids, items, lost []string
	running := false
	for _, requirement := range testRequirements(rows.Epic.Requirements) {
		check, work, ok := reproCheck(rows.Checks, run, requirement.ID)
		switch {
		case !ok:
			ids = append(ids, work)
			items = append(items, requirement.ID)
		case check.State == store.NodeRunning:
			running = true
		case check.NotRun == store.LostAtRestart:
			lost = append(lost, requirement.ID)
		}
	}
	switch {
	case len(ids) > 0:
		return Action{Kind: ActRepro, IDs: ids, Items: items}, false
	case running:
		return Action{Kind: ActNone}, false
	case len(lost) > 0:
		return Action{
			Kind:   ActPark,
			Output: fmt.Sprintf("integrate %s: reproduction %s lost at a restart twice", run.Path, lost[0]),
		}, false
	}
	return Action{}, true
}

// reproCheck returns the reproduction of Requirement id that the integrate run decides on, and its work id. That is the
// check, or its rerun once the check was lost at a restart. ok is false when the run has not started it.
func reproCheck(checks []store.NodeCheck, run store.NodeRun, id string) (store.NodeCheck, string, bool) {
	work := store.NodeWorkID(run.ID, run.Attempt, reproWork(id))
	check, ok := checkByID(checks, work)
	if ok && check.NotRun == store.LostAtRestart {
		work = store.NodeWorkID(run.ID, run.Attempt, rerunWork(reproWork(id)))
		check, ok = checkByID(checks, work)
	}
	return check, work, ok
}

// captureCauses returns a line for each capture that did not run or was not judged, and for each Requirement that was
// green at the stored base and whose capture does not pass now.
func captureCauses(rows Rows, captured map[string]store.EvidenceRun) []string {
	green := GreenAt(rows, rows.Epic.StoredBase)
	var causes []string
	for _, requirement := range itemRequirements(rows.Epic.Requirements) {
		capture := captured[requirement.ID]
		if capture.NotCaptured != "" || capture.Verdict == "" {
			causes = append(causes, requirement.ID+" capture failed")
		}
		if green[requirement.ID] && !capture.Passed {
			causes = append(causes, fmt.Sprintf(
				"%s regressed from green at the stored base %s", requirement.ID, rows.Epic.StoredBase,
			))
		}
	}
	return causes
}

// requirementLines returns a line for each Requirement in contract order: green when its capture passed, and not yet
// green otherwise.
func requirementLines(rows Rows, captured map[string]store.EvidenceRun) []string {
	lines := make([]string, 0, len(rows.Epic.Requirements))
	for _, requirement := range rows.Epic.Requirements {
		if captured[requirement.ID].Passed {
			lines = append(lines, requirement.ID+" green")
		} else {
			lines = append(lines, requirement.ID+" not yet green")
		}
	}
	return lines
}

// settle ends an integrate run once its refs are checked: failed with its causes when there are any, and advanced
// otherwise. The output is the causes or the lines, then the left-out lines.
func settle(run store.NodeRun, causes, lines, leftOut []string) Action {
	if !run.RefsChecked {
		return Action{Kind: ActRefs}
	}
	if len(causes) > 0 {
		return Action{Kind: ActEnd, Outcome: outcomeFailed, Output: outputOf(causes, leftOut)}
	}
	return Action{Kind: ActAdvance, Output: outputOf(lines, leftOut)}
}

// outputOf returns lines, then leftOut, one per line.
func outputOf(lines, leftOut []string) string {
	return strings.Join(append(slices.Clone(lines), leftOut...), "\n")
}

// GreenAt returns the Requirements whose capture passed in an integrate run that ended passed at revision, at the run's
// attempt. A rerun stands for the capture it reran.
func GreenAt(rows Rows, revision string) map[string]bool {
	green := make(map[string]bool)
	for _, run := range rows.Runs {
		if run.Kind != kindIntegrate || run.Outcome != outcomePassed || run.Revision != revision {
			continue
		}
		for _, requirement := range itemRequirements(rows.Epic.Requirements) {
			if capture, _, ok := captureFor(rows.Evidence, run, requirement.ID); ok && capture.Passed {
				green[requirement.ID] = true
			}
		}
	}
	return green
}

// Unbuilt returns, for each Requirement in contract order, why it lacks the proof that the epic is built: it names no
// evidence item, it has no passed red run at the epic's base, or it is not green at the stored base. A test item that is
// green at the stored base also lacks it when its reproduction at the epic's base disagrees with its red, or did not run.
// It returns nil when every Requirement has its proof.
func Unbuilt(rows Rows) []string {
	green := GreenAt(rows, rows.Epic.StoredBase)
	var lines []string
	for _, requirement := range rows.Epic.Requirements {
		switch {
		case !hasItem(requirement):
			lines = append(lines, requirement.ID+" names no evidence item")
		case !hasRedAtBase(rows, requirement.ID):
			lines = append(lines, fmt.Sprintf("%s has no red at the epic's base %s", requirement.ID, rows.Epic.Base))
		case !green[requirement.ID]:
			lines = append(lines, fmt.Sprintf(
				"%s is not green at the stored base %s", requirement.ID, rows.Epic.StoredBase,
			))
		case isTestItem(requirement):
			line := Disagreement(rows, requirement.ID)
			if line == "" {
				line = noReproduction(rows, requirement.ID)
			}
			if line != "" {
				lines = append(lines, line)
			}
		}
	}
	return lines
}

// Disagreement returns the line that names test-item Requirement id when its judged red disagrees with its reproduction
// at the epic's base: the judge found red fails, yet the reproduction with the candidate's tests exited 0 and did not
// time out. It reads the reproductions of the integrate runs that ended passed at the stored base. It returns "" for any
// other Requirement, and when one of those reproductions shows red's exit holds: timed out or exited non-zero.
func Disagreement(rows Rows, id string) string {
	if !slices.ContainsFunc(rows.Epic.Requirements, func(requirement store.EpicRequirement) bool {
		return requirement.ID == id && isTestItem(requirement)
	}) {
		return ""
	}
	disagreed := false
	for _, check := range baseReproductions(rows, id) {
		if check.NotRun != "" {
			continue
		}
		if check.TimedOut || check.Exit != 0 {
			return ""
		}
		disagreed = true
	}
	if !disagreed {
		return ""
	}
	return fmt.Sprintf(
		"%s judge and exit disagree at the epic's base %s: red was judged fails, but its reproduction with the candidate's tests exited 0",
		id,
		rows.Epic.Base,
	)
}

// noReproduction returns the line that names test-item Requirement id when no reproduction of it at the stored base ran.
// It names the reason the first reproduction did not run, when there is one. It returns "" when a reproduction ran.
func noReproduction(rows Rows, id string) string {
	checks := baseReproductions(rows, id)
	line := fmt.Sprintf("%s has no reproduction at the epic's base %s", id, rows.Epic.Base)
	if len(checks) == 0 {
		return line
	}
	for _, check := range checks {
		if check.NotRun == "" {
			return ""
		}
	}
	return line + ": " + checks[0].NotRun
}

// baseReproductions returns, in run order, the reproduction of Requirement id that each integrate run which ended passed
// at the stored base decides on. A run with no reproduction of id contributes none, and a rerun stands for the
// reproduction it reran.
func baseReproductions(rows Rows, id string) []store.NodeCheck {
	var checks []store.NodeCheck
	for _, run := range rows.Runs {
		if run.Kind != kindIntegrate || run.Outcome != outcomePassed || run.Revision != rows.Epic.StoredBase {
			continue
		}
		if check, _, ok := reproCheck(rows.Checks, run, id); ok {
			checks = append(checks, check)
		}
	}
	return checks
}

// hasRedAtBase reports whether an evidence node run that ended passed has a passed red run of Requirement id at the
// epic's base, at the run's attempt.
func hasRedAtBase(rows Rows, id string) bool {
	for _, run := range rows.Runs {
		if run.Kind != kindEvidence || run.Outcome != outcomePassed {
			continue
		}
		red, ok := evidenceByID(rows.Evidence, store.NodeWorkID(run.ID, run.Attempt, id))
		if ok && red.Phase == evidence.PhaseRed && red.Revision == rows.Epic.Base && red.Passed {
			return true
		}
	}
	return false
}

// MetAtBase returns the Requirements whose red run at the attempt of the node run was judged met, so their conclusion
// already holds at the base. They are in contract order.
func MetAtBase(node Node, rows Rows) []string {
	var met []string
	for _, requirement := range itemRequirements(rows.Epic.Requirements) {
		red, ok := evidenceByID(rows.Evidence, store.NodeWorkID(node.Run.ID, node.Run.Attempt, requirement.ID))
		if ok && red.Phase == evidence.PhaseRed && red.Verdict == evidence.VerdictHolds {
			met = append(met, requirement.ID)
		}
	}
	return met
}

// hasItem reports whether a Requirement names an evidence item.
func hasItem(requirement store.EpicRequirement) bool {
	return !reflect.ValueOf(requirement.Item).IsZero()
}

// isTestItem reports whether a Requirement names a test item.
func isTestItem(requirement store.EpicRequirement) bool {
	return requirement.Item.Kind == evidence.KindTest
}

// testRequirements returns the Requirements that name a test item, in contract order.
func testRequirements(requirements []store.EpicRequirement) []store.EpicRequirement {
	var tests []store.EpicRequirement
	for _, requirement := range requirements {
		if isTestItem(requirement) {
			tests = append(tests, requirement)
		}
	}
	return tests
}

// captureWork names the work of the capture of Requirement id, reproWork the work of its reproduction, and rerunWork the
// work of a rerun of part.
func captureWork(id string) string {
	return "capture " + id
}

func reproWork(id string) string {
	return "repro " + id
}

func rerunWork(part string) string {
	return part + " rerun"
}

// checkByID returns the check with id among checks.
func checkByID(checks []store.NodeCheck, id string) (store.NodeCheck, bool) {
	i := slices.IndexFunc(checks, func(check store.NodeCheck) bool { return check.ID == id })
	if i < 0 {
		return store.NodeCheck{}, false
	}
	return checks[i], true
}

// evidenceByID returns the evidence run with id among runs.
func evidenceByID(runs []store.EvidenceRun, id string) (store.EvidenceRun, bool) {
	i := slices.IndexFunc(runs, func(run store.EvidenceRun) bool { return run.ID == id })
	if i < 0 {
		return store.EvidenceRun{}, false
	}
	return runs[i], true
}
