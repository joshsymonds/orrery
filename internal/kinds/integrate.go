package kinds

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// The kind names the integrate rules read node runs by. kindLegacySync is the kind of a sync run an earlier orreryd
// stored: one that ended passed still checked its revision as an integrate run does (integrated).
const (
	kindEvidence   = "evidence"
	kindIntegrate  = "integrate"
	kindLegacySync = "sync"
)

// taskIntegratorRounds is how many rounds the task integrator of one conflicting task runs: the first, and one more for
// each of the two times a round fails. A round that fails the last of them leaves the task out.
const taskIntegratorRounds = 3

// The modes of an ActTaskIntegrator action, its first item. TaskStart starts a round's step, TaskRecord records the round
// once its step ends, TaskCompose composes the round's merged commit into the candidate, and TaskFail fails the round,
// its Output being the failure the next round reads.
const (
	TaskStart   = "start"
	TaskRecord  = "record"
	TaskCompose = "compose"
	TaskFail    = "fail"
)

// The verdicts a task integrator round is recorded with: merged when its step committed the task's revision merged into
// the candidate, clash when its step found that both sides cannot hold, and ended when its step ended without a done
// return or its return was refused.
const (
	RoundMerged = "merged"
	RoundClash  = "clash"
	RoundEnded  = "ended"
)

// Integrate is the integrate kind. Its run composes a batch of accepted tasks on the stored base into a candidate, runs
// the epic's Done gates, captures at the candidate each Requirement a task of the batch claims or contributes to
// (batchRequirements) and reproduces each test item's red at the epic's base, and then ends red or advances the epic to
// the candidate. A Requirement the batch did not touch keeps the green it had at the stored base (GreenAt).
type Integrate struct{}

// integrateResult is the Result an integrate run records when it claims its batch: the tasks compose made into commits
// and the tasks it left out, each with its reason and, for a merge conflict, the paths it conflicted at.
type integrateResult struct {
	Composed []json.RawMessage `json:"composed"`
	LeftOut  []struct {
		Task      int64    `json:"task"`
		Reason    string   `json:"reason"`
		Conflicts []string `json:"conflicts"`
	} `json:"leftOut"`
}

// Decide decides an integrate run. Without a revision it prepares the batch, or ends passed when the batch is empty.
// With one it first runs the task integrator rounds of the tasks the batch left out for a merge conflict (conflictRounds),
// then reads what compose made of the batch, and ends failed when nothing was composed. Otherwise it walks the Done
// gates, then the captures of the Requirements the batch touched and then the reproductions of the test items, and settles
// the run on what they found.
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
	if act, settled := conflictRounds(run, rows, result); !settled {
		return act
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
	captured, act, settled := captures(run, rows, batchRequirements(run, rows))
	if !settled {
		return act
	}
	if act, settled = reproductions(run, rows); !settled {
		return act
	}
	return settle(run, captureCauses(rows, captured), requirementLines(rows, captured), leftOut)
}

// conflictRounds decides the task integrator rounds of each task the batch left out for a merge conflict, in the order
// the batch left them out. It returns the action the first task whose rounds are not settled asks for, and settled true
// once every such task is settled: composed by one of its rounds, or left out with its rounds ended.
func conflictRounds(run store.NodeRun, rows Rows, result integrateResult) (Action, bool) {
	for _, left := range result.LeftOut {
		if len(left.Conflicts) == 0 {
			continue
		}
		if act, settled := taskRounds(run, rows, left.Task); !settled {
			return act, false
		}
	}
	return Action{}, true
}

// taskRounds decides the rounds of the task integrator of the conflicting task seq. Round 1 starts first, and each later
// round starts only once the round before it failed, with that failure as its Output. A round recorded clash or ended
// ends the task's rounds, and so does a failed round when it is the last. It returns settled true once they have ended
// with the task left out, and otherwise the action the round it is on asks for, with settled false.
func taskRounds(run store.NodeRun, rows Rows, seq int64) (Action, bool) {
	previous := ""
	for round := int64(1); round <= taskIntegratorRounds; round++ {
		record, recorded := roundRecord(rows.Rounds, run, seq, round)
		if !recorded {
			return roundStep(run, rows, seq, round, previous)
		}
		if record.Verdict != RoundMerged {
			return Action{}, true
		}
		switch record.Outcome {
		case "":
			return roundChecks(run, rows, seq, round, record)
		case outcomeFailed:
			previous = record.Failure
		default:
			return Action{}, true
		}
	}
	return Action{}, true
}

// roundRecord returns the recorded round at round of the task seq of run, and whether one is recorded.
func roundRecord(rounds []store.IntegrateRound, run store.NodeRun, seq, round int64) (store.IntegrateRound, bool) {
	return firstMatch(rounds, func(record store.IntegrateRound) bool {
		return record.Path == run.Path && record.Attempt == run.Attempt && record.Task == seq && record.Round == round
	})
}

// TaskEnding returns how the task integrator rounds of the conflicting task seq of run ended without composing it, which is
// what its send_back says: the explanation of the round that clashed, the explanation of the round whose step ended
// without a done return, or the failure of the third round when it failed. It returns "" when the rounds did not end that
// way: a round is not recorded or is still undecided, the task was composed, or a round's step never started, since such a
// round leaves its task out as it was before.
func TaskEnding(rows Rows, run store.NodeRun, seq int64) string {
	failure := ""
	for round := int64(1); round <= taskIntegratorRounds; round++ {
		record, recorded := roundRecord(rows.Rounds, run, seq, round)
		if !recorded {
			return ""
		}
		started := slices.ContainsFunc(rows.Steps, func(row store.StepRow) bool { return row.ID == record.Step })
		if !started {
			return ""
		}
		switch {
		case record.Verdict == RoundClash:
			return fmt.Sprintf("task integrator round %d clashed: %s", round, record.Explanation)
		case record.Verdict == RoundEnded:
			return fmt.Sprintf("task integrator round %d ended: %s", round, record.Explanation)
		case record.Verdict != RoundMerged || record.Outcome != outcomeFailed:
			return ""
		}
		failure = fmt.Sprintf("task integrator round %d failed: %s", round, record.Failure)
	}
	return failure
}

// roundStep decides round of the task integrator of the conflicting task seq, which has no record. A round with no step
// starts one (TaskStart), with the run's revision as the candidate, the task's revision and previous as its Output, and
// a paused or parked epic starts none. A round whose step is running waits, and one whose step has ended is recorded
// (TaskRecord).
func roundStep(run store.NodeRun, rows Rows, seq, round int64, previous string) (Action, bool) {
	id := store.NodeWorkID(run.ID, run.Attempt, taskStepWork(seq, round))
	step, started := firstMatch(rows.Steps, func(row store.StepRow) bool { return row.ID == id })
	switch {
	case started && step.End == "":
		return Action{Kind: ActNone}, false
	case started:
		return Action{
			Kind: ActTaskIntegrator, IDs: []string{id}, Tasks: []int64{seq},
			Items: []string{TaskRecord, roundItem(round)},
		}, false
	case rows.Epic.Paused || rows.Epic.Parked != "":
		return Action{Kind: ActNone}, false
	}
	return Action{
		Kind: ActTaskIntegrator, IDs: []string{id},
		Items:  []string{TaskStart, roundItem(round), run.Revision, taskRevision(rows.Epic.Tasks, seq)},
		Tasks:  []int64{seq},
		Output: previous,
	}, false
}

// roundChecks decides the Done gates and then the captures of a merged round's commit, its record's head, each under an
// id of the round's own. A failure ends the round as TaskFail, its detail as the Output the next round reads. Once they
// all pass, the round composes its task (TaskCompose).
func roundChecks(run store.NodeRun, rows Rows, seq, round int64, record store.IntegrateRound) (Action, bool) {
	checked := run
	checked.ID = fmt.Sprintf("%s-task-%d-%d", run.ID, seq, round)
	checked.Revision = record.Head
	failure, previous, act, settled := mergedChecks(checked, rows)
	switch {
	case !settled:
		act.Revision = record.Head
		return act, false
	case failure != "":
		return Action{
			Kind: ActTaskIntegrator, Items: []string{TaskFail, roundItem(round)}, Tasks: []int64{seq}, Output: previous,
		}, false
	}
	return Action{
		Kind: ActTaskIntegrator, Items: []string{TaskCompose, roundItem(round), record.Head}, Tasks: []int64{seq},
	}, false
}

// mergedChecks walks the epic's Done gates and then the captures of every Requirement that names an evidence item at the
// revision of checked, the commit a round of the task integrator made, under checked's id: a merge of a task into the
// candidate can change any of them. Until they settle it returns the action the first one that is not decided asks
// for. Once they settle, failure is "" when every gate passed and no capture failed. Otherwise it is the reason of the
// first gate that failed, or of the captures that failed, and previous is what that failure was, with the gate's exit
// and output tails or each capture's verdict, as the next round of the task integrator reads it. A capture fails as an
// integrate run's does (captureCauses): it was not captured or not judged, or its Requirement was green at the stored
// base and its capture does not pass at the commit.
func mergedChecks(checked store.NodeRun, rows Rows) (string, string, Action, bool) {
	gate, act, settled := doneGates(checked, rows)
	if !settled {
		return "", "", act, false
	}
	if gate != "" {
		failure := fmt.Sprintf("done gate %s did not pass at %s", gate, checked.Revision)
		return failure, gateFailure(checked, rows), Action{}, true
	}
	captured, act, settled := captures(checked, rows, itemRequirements(rows.Epic.Requirements))
	if !settled {
		return "", "", act, false
	}
	causes := captureCauses(rows, captured)
	if len(causes) == 0 {
		return "", "", Action{}, true
	}
	failure := fmt.Sprintf("%s at %s", strings.Join(causes, "; "), checked.Revision)
	return failure, captureFailures(checked.Revision, rows, captured), Action{}, true
}

// gateFailure returns what the first Done gate that did not pass at the revision of checked, the commit of a round of
// the task integrator, was: its name and how it ended, then the tails of its stdout and stderr that are not empty.
func gateFailure(checked store.NodeRun, rows Rows) string {
	for i, gate := range rows.Epic.DoneGates {
		check, _, decided := gateCheck(checked, i, gate, rows.Checks)
		if !decided || checkPassed(check) {
			continue
		}
		var how string
		switch {
		case check.NotRun != "":
			how = "it did not run: " + check.NotRun
		case check.TimedOut:
			how = "it ran past its limit and was killed"
		default:
			how = fmt.Sprintf("it exited %d", check.Exit)
		}
		lines := []string{fmt.Sprintf("Done gate %s failed on commit %s: %s.", gate, checked.Revision, how)}
		if tail := strings.TrimSpace(check.Stdout); tail != "" {
			lines = append(lines, "stdout tail:", tail)
		}
		if tail := strings.TrimSpace(check.Stderr); tail != "" {
			lines = append(lines, "stderr tail:", tail)
		}
		return strings.Join(lines, "\n")
	}
	return ""
}

// captureFailures returns one line for each capture at revision, the commit of a round of the task integrator, that
// captureCauses finds failing: one that was not captured, with why, one that was not judged, and one whose Requirement
// was green at the stored base and whose capture does not pass, with its exit, its verdict and its judge's reason. A
// carried capture writes no line.
func captureFailures(revision string, rows Rows, captured map[string]store.EvidenceRun) string {
	green := GreenAt(rows, rows.Epic.StoredBase)
	var lines []string
	for _, requirement := range itemRequirements(rows.Epic.Requirements) {
		capture := captured[requirement.ID]
		if carried(capture, green[requirement.ID]) {
			continue
		}
		switch {
		case capture.NotCaptured != "":
			lines = append(lines, fmt.Sprintf("The capture of %s did not run on commit %s: %s.",
				requirement.ID, revision, capture.NotCaptured))
		case capture.Verdict == "":
			lines = append(lines, fmt.Sprintf("The capture of %s on commit %s was not judged.",
				requirement.ID, revision))
		case green[requirement.ID] && !capture.Passed:
			lines = append(lines, fmt.Sprintf(
				"%s was green at the stored base %s, and its capture on commit %s exited %d and was judged %s: %s",
				requirement.ID, rows.Epic.StoredBase, revision, capture.Exit, capture.Verdict, capture.JudgeReason))
		}
	}
	return strings.Join(lines, "\n")
}

// taskStepWork is the part of the work of a task integrator round's step: the task's seq and the round.
func taskStepWork(seq, round int64) string {
	return fmt.Sprintf("task integrator T%d %d", seq, round)
}

// roundItem returns round as the item an ActTaskIntegrator action carries it in.
func roundItem(round int64) string {
	return strconv.FormatInt(round, 10)
}

// taskRevision returns the revision the build of the task seq holds, and "" when no task has seq.
func taskRevision(tasks []store.EpicTask, seq int64) string {
	i := slices.IndexFunc(tasks, func(task store.EpicTask) bool { return task.Seq == seq })
	if i < 0 {
		return ""
	}
	return tasks[i].Build.Revision
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

// gateCheck returns the check that decides Done gate i of the run: its check, or the last rerun of it the walk over its
// reruns reaches. It returns decided false, with the action the gate asks for, while that check is not decided, and
// with a park when the gate's last rerun was lost at a restart or exited store.EnvironmentExit.
func gateCheck(run store.NodeRun, i int, gate string, checks []store.NodeCheck) (store.NodeCheck, Action, bool) {
	part := fmt.Sprintf("done %d", i+1)
	check, id, outcome := checkChain(run, checks, part, true)
	switch outcome {
	case chainNone:
		return store.NodeCheck{}, Action{Kind: ActStart, IDs: []string{id}, Items: []string{gate}}, false
	case chainRunning:
		return store.NodeCheck{}, Action{Kind: ActNone}, false
	case chainLost:
		return store.NodeCheck{}, Action{
			Kind:   ActPark,
			Output: fmt.Sprintf("%s %s: Done gate %s lost at a restart twice", kindIntegrate, run.Path, gate),
		}, false
	case chainEnvironment:
		return store.NodeCheck{}, Action{
			Kind:   ActPark,
			Output: fmt.Sprintf("%s %s: %s", kindIntegrate, run.Path, environmentPark("Done gate "+gate)),
		}, false
	case chainEnded:
		return check, Action{}, true
	}
	return check, Action{}, true
}

// checkPassed reports whether a check ran, exited 0 and did not time out.
func checkPassed(check store.NodeCheck) bool {
	return check.NotRun == "" && !check.TimedOut && check.Exit == 0
}

// batchRequirements returns the Requirements that name an evidence item, in contract order, that a task the integrate
// run composed claims or contributes to: the Requirements its batch changed. Only those are captured at its candidate;
// every other keeps the green it had at the stored base. A result whose composed tasks do not decode returns every
// Requirement that names an item, since a batch nothing is known of could have changed any of them.
func batchRequirements(run store.NodeRun, rows Rows) []store.EpicRequirement {
	items := itemRequirements(rows.Epic.Requirements)
	seqs, err := composedSeqs(run.Result)
	if err != nil {
		return items
	}
	touched := make(map[string]bool)
	for _, task := range rows.Epic.Tasks {
		if !slices.Contains(seqs, task.Seq) {
			continue
		}
		for _, id := range slices.Concat(task.Requirements, task.Contributes) {
			touched[id] = true
		}
	}
	return slices.DeleteFunc(items, func(requirement store.EpicRequirement) bool { return !touched[requirement.ID] })
}

// captures decides the captures of requirements, each of which names an evidence item. A capture with no run starts, and
// one lost at a restart or that exited store.EnvironmentExit is started again as its rerun. A capture still running makes
// it wait. A rerun lost at a restart parks the run, and so does a capture whose last rerun exits store.EnvironmentExit.
// Once every capture has ended, it returns the capture each of requirements ended with.
func captures(
	run store.NodeRun, rows Rows, requirements []store.EpicRequirement,
) (map[string]store.EvidenceRun, Action, bool) {
	captured := make(map[string]store.EvidenceRun)
	var ids, items, lost, environment []string
	running := false
	for _, requirement := range requirements {
		capture, work, outcome := evidenceChain(run, rows.Evidence, captureWork(requirement.ID), true)
		switch outcome {
		case chainNone:
			ids = append(ids, work)
			items = append(items, requirement.ID)
		case chainRunning:
			running = true
		case chainLost:
			lost = append(lost, requirement.ID)
		case chainEnvironment:
			environment = append(environment, requirement.ID)
		case chainEnded:
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
			Output: fmt.Sprintf("%s %s: capture %s lost at a restart twice", kindIntegrate, run.Path, lost[0]),
		}, false
	case len(environment) > 0:
		return nil, Action{
			Kind:   ActPark,
			Output: fmt.Sprintf("%s %s: %s", kindIntegrate, run.Path, environmentPark("capture "+environment[0])),
		}, false
	}
	return captured, Action{}, true
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
			Kind: ActPark,
			Output: fmt.Sprintf(
				"%s %s: reproduction %s lost at a restart twice", kindIntegrate, run.Path, lost[0],
			),
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
// green at the stored base and whose capture does not pass now. A regression also names the capture's exit, verdict and
// the judge's reason when the capture has a verdict and a reason. A carried capture is no cause (see carried), and
// neither is a Requirement captured holds no capture of.
func captureCauses(rows Rows, captured map[string]store.EvidenceRun) []string {
	green := GreenAt(rows, rows.Epic.StoredBase)
	var causes []string
	for _, requirement := range itemRequirements(rows.Epic.Requirements) {
		capture, ok := captured[requirement.ID]
		if !ok || carried(capture, green[requirement.ID]) {
			continue
		}
		if capture.NotCaptured != "" || capture.Verdict == "" {
			causes = append(causes, requirement.ID+" capture failed")
		}
		if green[requirement.ID] && !capture.Passed {
			regression := fmt.Sprintf(
				"%s regressed from green at the stored base %s", requirement.ID, rows.Epic.StoredBase,
			)
			if capture.Verdict != "" && capture.JudgeReason != "" {
				regression += fmt.Sprintf(": its capture exited %d and was judged %s: %s",
					capture.Exit, capture.Verdict, capture.JudgeReason)
			}
			causes = append(causes, regression)
		}
	}
	return causes
}

// requirementLines returns a line for each Requirement in contract order: green when its capture passed, green with a
// note when its capture is carried (see carried), green with a note when it was not captured and is green at the stored
// base, and not yet green otherwise.
func requirementLines(rows Rows, captured map[string]store.EvidenceRun) []string {
	green := GreenAt(rows, rows.Epic.StoredBase)
	lines := make([]string, 0, len(rows.Epic.Requirements))
	for _, requirement := range rows.Epic.Requirements {
		capture, ok := captured[requirement.ID]
		switch {
		case !ok && green[requirement.ID]:
			lines = append(lines, fmt.Sprintf(
				"%s green at the stored base %s; no task of this batch changes it, so it was not captured again",
				requirement.ID, rows.Epic.StoredBase,
			))
		case capture.Passed:
			lines = append(lines, requirement.ID+" green")
		case carried(capture, green[requirement.ID]):
			lines = append(lines, fmt.Sprintf(
				"%s green: the judge gave no verdict on its exit-0 capture (%s), so its verdict at the stored base %s carries",
				requirement.ID,
				capture.JudgeReason,
				rows.Epic.StoredBase,
			))
		default:
			lines = append(lines, requirement.ID+" not yet green")
		}
	}
	return lines
}

// carried reports whether a capture carries its Requirement's earlier verdict: the capture is judgeless, and the
// Requirement was green at the stored base. A carried capture passes its node, with a note (requirementLines).
func carried(capture store.EvidenceRun, green bool) bool {
	return green && judgeless(capture)
}

// judgeless reports whether a capture ran and exited 0 without timing out, yet the judge gave it no verdict.
func judgeless(capture store.EvidenceRun) bool {
	return capture.NotCaptured == "" && !capture.TimedOut && capture.Exit == 0 && capture.Verdict == ""
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

// integrated reports whether run checks a revision for the epic's stored base: an integrate run, or a legacy sync run,
// which checked its merge of origin's main the same way.
func integrated(run store.NodeRun) bool {
	return run.Kind == kindIntegrate || run.Kind == kindLegacySync
}

// GreenAt returns the Requirements whose capture passed in an integrate or legacy sync run that ended passed at
// revision, at the run's attempt. A rerun stands for the capture it reran. A passed run also holds a judgeless capture,
// which only the carry lets it hold, so that capture counts as green too (see judgeless). A Requirement such a run did
// not capture, since no task of its batch touched it, is green when it is green at the base the run composed its batch
// on. A Requirement whose evidence amendment ran its green run at revision is green there too when that run passed (see
// amendedGreen).
func GreenAt(rows Rows, revision string) map[string]bool {
	return greenAt(rows, revision, make(map[string]map[string]bool))
}

// greenAt is GreenAt, with known holding what it found at each revision it read. A revision being read is known as empty
// until it is read, so a base that leads back to it ends the walk.
func greenAt(rows Rows, revision string, known map[string]map[string]bool) map[string]bool {
	if green, ok := known[revision]; ok {
		return green
	}
	green := make(map[string]bool)
	known[revision] = green
	for _, run := range rows.Runs {
		if integrated(run) && run.Outcome == outcomePassed && run.Revision == revision {
			runGreen(rows, run, known, green)
		}
	}
	for _, requirement := range itemRequirements(rows.Epic.Requirements) {
		if amendedGreen(rows, requirement.ID, revision) {
			green[requirement.ID] = true
		}
	}
	return green
}

// runGreen adds to green the Requirements that run, an integrate or legacy sync run that ended passed, holds green: each
// whose capture passed or is judgeless, and each it did not capture that is green at its base (baseGreen).
func runGreen(rows Rows, run store.NodeRun, known map[string]map[string]bool, green map[string]bool) {
	var base map[string]bool
	for _, requirement := range itemRequirements(rows.Epic.Requirements) {
		capture, _, outcome := evidenceChain(run, rows.Evidence, captureWork(requirement.ID), true)
		if outcome != chainNone {
			if capture.Passed || judgeless(capture) {
				green[requirement.ID] = true
			}
			continue
		}
		if base == nil {
			base = baseGreen(rows, run, known)
		}
		if base[requirement.ID] {
			green[requirement.ID] = true
		}
	}
}

// baseGreen returns the Requirements green at the base an integrate run composed its batch on, as its result names it.
// A run whose result names no base, or names its own revision, has none.
func baseGreen(rows Rows, run store.NodeRun, known map[string]map[string]bool) map[string]bool {
	var result struct {
		Base string `json:"base"`
	}
	if json.Unmarshal([]byte(run.Result), &result) != nil || result.Base == "" || result.Base == run.Revision {
		return map[string]bool{}
	}
	return greenAt(rows, result.Base, known)
}

// amendment returns the latest evidence amendment of Requirement id that is capturing or applied, and whether there is
// one whose new item the Requirement now holds. Its runs are the proof of that item that the amendment ran: its red at
// the epic's base and, once the stored base had moved past the base, its green and reproduction at that stored base.
func amendment(rows Rows, id string) (store.EvidenceAmendment, bool) {
	i := slices.IndexFunc(rows.Epic.Requirements, func(requirement store.EpicRequirement) bool {
		return requirement.ID == id
	})
	if i < 0 {
		return store.EvidenceAmendment{}, false
	}
	item := rows.Epic.Requirements[i].Item
	for _, amended := range slices.Backward(rows.Epic.Amendments) {
		applied := amended.State == store.AmendmentCapturing || amended.State == store.AmendmentApplied
		if amended.Requirement != id || !applied {
			continue
		}
		return amended, evidence.Current(item, amended.Item)
	}
	return store.EvidenceAmendment{}, false
}

// amendedGreen reports whether the amendment of Requirement id ran its green run at revision and that run passed.
func amendedGreen(rows Rows, id, revision string) bool {
	amended, ok := amendment(rows, id)
	if !ok || amended.GreenRun == "" || amended.StoredBase != revision {
		return false
	}
	green, found := evidenceByID(rows.Evidence, amended.GreenRun)
	return found && green.Phase == evidence.PhaseGreen && green.Revision == revision && green.Passed
}

// Unbuilt returns, for each Requirement in contract order, why it lacks the proof that the epic is built: it names no
// evidence item, or it is not green at the stored base. Only a Requirement not green at the stored base holds the plan.
// A missing red at the epic's base, and a reproduction that is missing or that disagrees with its red, hold nothing, so
// neither is named. A Requirement that a task whose status is taskGap claims is not named at all: that task ended its
// ladder as a gap, and TaskGaps names it instead. It returns nil when every Requirement is green at the stored base or
// is held by a gap.
func Unbuilt(rows Rows) []string {
	green := GreenAt(rows, rows.Epic.StoredBase)
	var lines []string
	for _, requirement := range rows.Epic.Requirements {
		if gapHeld(rows.Epic.Tasks, requirement.ID) || green[requirement.ID] {
			continue
		}
		if !hasItem(requirement) {
			lines = append(lines, requirement.ID+" names no evidence item")
			continue
		}
		lines = append(
			lines,
			fmt.Sprintf("%s is not green at the stored base %s", requirement.ID, rows.Epic.StoredBase),
		)
	}
	return lines
}

// Disagreement returns the line that names test-item Requirement id when its judged red disagrees with its reproduction
// at the epic's base: the judge found red fails, yet the reproduction with the candidate's tests exited 0 and did not
// time out. It reads the reproductions of the integrate and legacy sync runs that ended passed at the stored base. It
// returns "" for any other Requirement, and when one of those reproductions shows red's exit holds: timed out or exited
// non-zero.
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

// baseReproductions returns, in run order, the reproduction of Requirement id that each integrate or legacy sync run
// which ended passed at the stored base decides on, and then the reproduction the amendment of id ran at the stored
// base, when it ran one there. A run with no reproduction of id contributes none. When the item of id is an
// amendment's, neither does a run whose capture of id is stale, since its reproduction ran the capture of an item the
// Requirement no longer holds. A rerun stands for the reproduction it reran.
func baseReproductions(rows Rows, id string) []store.NodeCheck {
	amended, isAmended := amendment(rows, id)
	var checks []store.NodeCheck
	for _, run := range rows.Runs {
		if !integrated(run) || run.Outcome != outcomePassed || run.Revision != rows.Epic.StoredBase {
			continue
		}
		if _, _, outcome := evidenceChain(
			run,
			rows.Evidence,
			captureWork(id),
			true,
		); isAmended &&
			outcome == chainNone {
			continue
		}
		if check, _, ok := reproCheck(rows.Checks, run, id); ok {
			checks = append(checks, check)
		}
	}
	if !isAmended || amended.ReproCheck == "" || amended.StoredBase != rows.Epic.StoredBase {
		return checks
	}
	if check, found := checkByID(rows.Checks, amended.ReproCheck); found && check.State != store.NodeRunning {
		checks = append(checks, check)
	}
	return checks
}

// MetAtBase returns the Requirements that an evidence node at base found met at the epic's base: each whose red run at
// the attempt of the node run was judged holding, in contract order. The node's other red runs must have passed, so a
// node that failed for any other reason returns none. That red run is the one the rerun chain decides on, as
// Evidence.Decide reads it. A Requirement dropped as already met has no red run to judge, so it is never named and does
// not count.
func MetAtBase(node Node, rows Rows) []string {
	var met []string
	for _, requirement := range itemRequirements(rows.Epic.Requirements) {
		if droppedAsMet(requirement) {
			continue
		}
		red, _, outcome := evidenceChain(node.Run, rows.Evidence, requirement.ID, false)
		if outcome != chainEnded || red.Phase != evidence.PhaseRed {
			return nil
		}
		if red.Passed {
			continue
		}
		if red.Verdict != evidence.VerdictHolds {
			return nil
		}
		met = append(met, requirement.ID)
	}
	return met
}

// hasItem reports whether a Requirement names an evidence item.
func hasItem(requirement store.EpicRequirement) bool {
	return !reflect.ValueOf(requirement.Item).IsZero()
}

// droppedAsMet reports whether the owner dropped a Requirement as already met at the epic's base: its DroppedBy is the
// seq of that decision.
func droppedAsMet(requirement store.EpicRequirement) bool {
	return requirement.DroppedBy != 0
}

// gapHeld reports whether a task whose status is taskGap claims Requirement id among tasks.
func gapHeld(tasks []store.EpicTask, id string) bool {
	return slices.ContainsFunc(tasks, func(task store.EpicTask) bool {
		return task.Status == taskGap && slices.Contains(task.Requirements, id)
	})
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

// runWork returns the work of the k-th run of part: part itself for the first run, and each rerun adds a rerun to the work
// before it.
func runWork(part string, k int) string {
	for range k {
		part = rerunWork(part)
	}
	return part
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
