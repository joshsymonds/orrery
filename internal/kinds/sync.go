package kinds

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/joshsymonds/orrery/internal/store"
)

// kindSync is the kind of a sync run, and kindRelease the kind of a release run.
const (
	kindSync    = "sync"
	kindRelease = "release"
)

// outcomeSkipped is the outcome of a sync run that did not decide origin's commit: its prepare tried no merge, or its
// checks or its advance did not finish.
const outcomeSkipped = "skipped"

// Sync is the kind of the sync of origin's main. The interpreter adds a sync run under a running plan run at a quiet
// point of its epic and decides it with this kind. Its prepare fetches origin's main and records the merge of it into
// the stored base. At that merge the run walks the epic's Done gates, the captures of its Requirements and the
// reproductions of its test items, as an integrate run walks them, and then advances the epic's branch and stored base
// to the merge. A sync run never parks: what stops it ends it.
type Sync struct{}

// SyncResult is the result a sync run's prepare records: an error that kept it from reading or merging origin's main,
// or that the repository has no remote origin; else origin's branch and commit, the stored base it was merged into, and
// then whether that commit is in the stored base already, the epic's change base when that commit does not hold it, the
// path of an earlier sync run that tried it, the paths its merge conflicts in, or the merge it made. The kind reads it
// and the executor writes it, so it is defined once, here.
type SyncResult struct {
	Error      string   `json:"error,omitempty"`
	NoOrigin   bool     `json:"noOrigin,omitempty"`
	Branch     string   `json:"branch,omitempty"`
	Origin     string   `json:"origin,omitempty"`
	Base       string   `json:"base"`
	Contained  bool     `json:"contained,omitempty"`
	ChangeBase string   `json:"changeBase,omitempty"`
	TriedBy    string   `json:"triedBy,omitempty"`
	Conflicts  []string `json:"conflicts,omitempty"`
	Merge      string   `json:"merge,omitempty"`
}

// MergeLine is the line that names the merge a sync made: the merge, origin's commit and the stored base.
func (r SyncResult) MergeLine() string {
	return fmt.Sprintf("merge %s of origin's commit %s into the stored base %s", r.Merge, r.Origin, r.Base)
}

// ParseSyncResult decodes the result of a sync run. A result that is not a JSON object is an error.
func ParseSyncResult(result string) (SyncResult, error) {
	var parsed SyncResult
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		return SyncResult{}, fmt.Errorf("sync result is not a JSON object: %w", err)
	}
	return parsed, nil
}

// Decide decides a sync run. With no result it prepares, unless the epic is paused or parked, naming the commits of
// origin's main the epic's other sync runs tried (syncTries). With one it ends on what the prepare found when the
// prepare made no merge. Otherwise it walks the Done gates, the captures and the reproductions at the merge, ends
// failed when one fails, ends skipped when one cannot be decided, and advances to the merge once all pass, with why
// when the epic moved while they ran.
func (Sync) Decide(node Node, rows Rows) Action {
	run := node.Run
	if run.Result == "" {
		if act := prepare(rows); act.Kind == ActNone {
			return act
		}
		paths, origins := syncTries(rows, run.Path)
		return Action{Kind: ActPrepare, IDs: paths, Items: origins}
	}
	result, err := ParseSyncResult(run.Result)
	if err != nil {
		return Action{Kind: ActEnd, Outcome: outcomeSkipped, Output: err.Error()}
	}
	if act, ended := syncUnmerged(result); ended {
		return act
	}
	return syncChecks(run, rows, result)
}

// syncTries returns the path of each sync run of rows but the one at path that tried origin's commit (triedOrigin),
// with that commit, in the order of the runs.
func syncTries(rows Rows, path string) ([]string, []string) {
	var paths, origins []string
	for _, run := range rows.Runs {
		if run.Kind != kindSync || run.Path == path {
			continue
		}
		if origin, tried := triedOrigin(run); tried {
			paths = append(paths, run.Path)
			origins = append(origins, origin)
		}
	}
	return paths, origins
}

// triedOrigin returns origin's commit the sync run tried, and whether it tried one. A sync tried origin's commit when
// it decided it: its merge conflicted, or a Done gate or a capture failed at its merge, each of which ends it failed,
// or its merge passed every check and advanced, which ends it passed. A sync that ended skipped decided nothing,
// whatever kept it from deciding: its prepare made no merge or found no origin, git or a write failed, a check was lost
// at a restart twice or its environment failed, or its advance was not taken. Nor did a sync that a close ended, with
// no outcome, or one still running. A commit a sync tried is never tried again.
func triedOrigin(run store.NodeRun) (string, bool) {
	if run.State != store.NodeEnded || (run.Outcome != outcomePassed && run.Outcome != outcomeFailed) {
		return "", false
	}
	result, err := ParseSyncResult(run.Result)
	if err != nil || (result.Merge == "" && len(result.Conflicts) == 0) {
		return "", false
	}
	return result.Origin, true
}

// SyncTriedBy returns the path of the sync run of rows that tried origin's commit origin (triedOrigin), "" when none
// did.
func SyncTriedBy(rows Rows, origin string) string {
	paths, origins := syncTries(rows, "")
	if i := slices.Index(origins, origin); i >= 0 {
		return paths[i]
	}
	return ""
}

// syncUnmerged ends a sync run whose prepare made no merge, and reports whether it did. A repository with no remote
// origin, an error, origin's commit already in the stored base, a commit that does not hold the epic's change base and
// a commit an earlier sync tried end it skipped. A merge that conflicts ends it failed, naming the paths, and is told.
func syncUnmerged(result SyncResult) (Action, bool) {
	switch {
	case result.NoOrigin:
		return syncSkipped("the repository has no remote origin"), true
	case result.Error != "":
		return syncSkipped("the sync did not run: " + result.Error), true
	case result.Contained:
		return syncSkipped(fmt.Sprintf(
			"origin's commit %s is in the stored base %s", result.Origin, result.Base,
		)), true
	case result.ChangeBase != "":
		return syncSkipped(fmt.Sprintf(
			"the epic's change base %s is not in origin's commit %s", result.ChangeBase, result.Origin,
		)), true
	case result.TriedBy != "":
		return syncSkipped(fmt.Sprintf(
			"origin's commit %s was tried by %s", result.Origin, result.TriedBy,
		)), true
	case len(result.Conflicts) > 0:
		return syncTold(outcomeFailed, fmt.Sprintf(
			"merge of origin's commit %s into the stored base %s conflicts in %s",
			result.Origin, result.Base, strings.Join(result.Conflicts, ", "),
		)), true
	case result.Merge == "":
		return syncSkipped("the sync result names no merge"), true
	}
	return Action{}, false
}

// syncChecks decides a sync run at its merge. It walks the Done gates, then the captures and the reproductions, as an
// integrate run does, and returns the action the first one that is not decided asks for. A gate that failed and a
// capture that failed or regressed from green at the stored base end the run failed, told, its output the merge's line
// and then one line per cause. A check lost at a restart twice or exiting store.EnvironmentExit on its last run cannot
// be decided, so it ends the run skipped, told, its output the merge's line and the park reason of the walk (syncWait).
// Once every check has passed, the run advances to the merge, its output the line of what it merged and one line per
// Requirement, and its Reason why the epic moved while the checks ran (SyncMoved), empty when it did not. The executor
// ends a run whose advance has a Reason skipped with it, unless a restart left the branch at the merge already.
func syncChecks(run store.NodeRun, rows Rows, result SyncResult) Action {
	gate, act, settled := doneGates(run, rows)
	if !settled {
		return syncWait(act, result)
	}
	if gate != "" {
		return syncFailed(result, []string{"Done gate " + gate + " failed"})
	}
	captured, act, settled := captures(run, rows)
	if !settled {
		return syncWait(act, result)
	}
	if act, settled = reproductions(run, rows); !settled {
		return syncWait(act, result)
	}
	if causes := captureCauses(rows, captured); len(causes) > 0 {
		return syncFailed(result, causes)
	}
	merged := fmt.Sprintf("merged origin's commit %s into the stored base %s as %s", result.Origin, result.Base,
		result.Merge)
	return Action{
		Kind: ActAdvance, Output: outputOf([]string{merged}, requirementLines(rows, captured)),
		Reason: SyncMoved(rows, result),
	}
}

// syncWait returns act, what a check of the sync that is not decided asks for. A park ends the run skipped and told
// instead, its output the merge's line and the park's reason: a sync never parks, and a check it could not decide
// decides nothing of origin's commit, so a later sync may try that commit again.
func syncWait(act Action, result SyncResult) Action {
	if act.Kind == ActPark {
		return syncTold(outcomeSkipped, outputOf([]string{result.MergeLine()}, []string{act.Output}))
	}
	return act
}

// syncFailed ends a sync run failed and told, with the line of its merge and then causes, one to a line.
func syncFailed(result SyncResult, causes []string) Action {
	return syncTold(outcomeFailed, outputOf([]string{result.MergeLine()}, causes))
}

// syncSkipped ends a sync run skipped with output, told to no one.
func syncSkipped(output string) Action {
	return Action{Kind: ActEnd, Outcome: outcomeSkipped, Output: output}
}

// syncTold ends a sync run with outcome and output, told to the owner.
func syncTold(outcome, output string) Action {
	return Action{Kind: ActEnd, Outcome: outcome, Output: output, Tell: true}
}

// SyncMoved returns why the epic of rows is no longer at the quiet point the sync whose prepare found result started
// at, "" while it is: the stored base is not the one the merge was made on, the epic is paused, parked or in a phase it
// ends or is closed in (Ended), or its work is not at rest (Busy). An advance then would leave a task built on the
// stored base the sync started from, or a batch composed on it, behind the branch, or move the branch of an epic its
// owner holds. The kind reads it at the advance, and the executor again just before it moves the branch.
func SyncMoved(rows Rows, result SyncResult) string {
	epic := rows.Epic
	switch {
	case epic.StoredBase != result.Base:
		return fmt.Sprintf("the stored base moved from %s to %s", result.Base, epic.StoredBase)
	case epic.Paused:
		return "the epic is paused"
	case epic.Parked != "":
		return "the epic is parked"
	case Ended(epic.Phase):
		return "the epic's phase is " + epic.Phase
	}
	return Busy(rows)
}

// Busy returns why the work of the epic of rows is not at rest, "" when it is: the epic has a candidate, a planner turn
// is running, an action a planner turn returned is pending, a task is open, a task is accepted and no batch has claimed
// it, or an integrate or release run runs or is parked. A sync of origin's main starts, and advances, only while it is
// at rest. A pending action is carried out by the planner's next pass, and may rerun a node, fail a task, resume a
// release or close the epic, so the epic is not at rest until it has been.
func Busy(rows Rows) string {
	epic := rows.Epic
	if epic.Candidate != "" {
		return "the epic has the candidate " + epic.Candidate
	}
	if i := slices.IndexFunc(epic.Turns, func(turn store.EpicTurn) bool { return turn.State == turnRunning }); i >= 0 {
		return fmt.Sprintf("planner turn %d is running", epic.Turns[i].Seq)
	}
	switch {
	case rows.Actions == 1:
		return "a planner action is pending"
	case rows.Actions > 1:
		return fmt.Sprintf("%d planner actions are pending", rows.Actions)
	}
	for _, task := range epic.Tasks {
		switch {
		case task.Status == taskOpen:
			return fmt.Sprintf("task T%d is open", task.Seq)
		case task.Status == taskDone && task.Batch == "":
			return fmt.Sprintf("task T%d is accepted and no batch has claimed it", task.Seq)
		}
	}
	for _, run := range rows.Runs {
		open := run.State == store.NodeRunning || run.State == store.NodeParked
		if open && (run.Kind == kindIntegrate || run.Kind == kindRelease) {
			return fmt.Sprintf("%s run %s is %s", run.Kind, run.Path, run.State)
		}
	}
	return ""
}

// integrated reports whether run checks a revision for the epic's stored base: an integrate run, or a sync run, which
// checks its merge of origin's main the same way.
func integrated(run store.NodeRun) bool {
	return run.Kind == kindIntegrate || run.Kind == kindSync
}

// walkName returns the name a park of run's walk of its checks opens with: sync for a sync run, and integrate for an
// integrate run and for a release run, which walks its candidate's checks as an integrate run walks them.
func walkName(run store.NodeRun) string {
	if run.Kind == kindSync {
		return kindSync
	}
	return kindIntegrate
}
