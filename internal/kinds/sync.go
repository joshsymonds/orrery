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

// outcomeSkipped is the outcome of a sync run that tried no merge, or whose merge was not advanced for a reason that is
// not the merge's.
const outcomeSkipped = "skipped"

// Sync is the kind of the sync of origin's main. The interpreter adds a sync run under a running plan run at a quiet
// point of its epic and decides it with this kind. Its prepare fetches origin's main and records the merge of it into
// the stored base. At that merge the run walks the epic's Done gates, the captures of its Requirements and the
// reproductions of its test items, as an integrate run walks them, and then advances the epic's branch and stored base
// to the merge. A sync run never parks: what stops it ends it.
type Sync struct{}

// SyncResult is the result a sync run's prepare records: an error that kept it from reading or merging origin's main,
// or that the repository has no remote origin; else origin's branch and commit, the stored base it was merged into, and
// then whether that commit is in the stored base already, the path of an earlier sync run that tried it, the paths its
// merge conflicts in, or the merge it made. The kind reads it and the executor writes it, so it is defined once, here.
type SyncResult struct {
	Error     string   `json:"error,omitempty"`
	NoOrigin  bool     `json:"noOrigin,omitempty"`
	Branch    string   `json:"branch,omitempty"`
	Origin    string   `json:"origin,omitempty"`
	Base      string   `json:"base"`
	Contained bool     `json:"contained,omitempty"`
	TriedBy   string   `json:"triedBy,omitempty"`
	Conflicts []string `json:"conflicts,omitempty"`
	Merge     string   `json:"merge,omitempty"`
}

// Tried reports whether the sync tried a merge of origin's commit: it made the merge, or found it conflicting. A commit
// a sync tried is never tried again.
func (r SyncResult) Tried() bool {
	return r.Merge != "" || len(r.Conflicts) > 0
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
// origin's main the epic's other sync runs tried. With one it ends on what the prepare found when the prepare made no
// merge. Otherwise it walks the Done gates, the captures and the reproductions at the merge, ends failed when one fails,
// and advances to the merge once all pass, unless the epic moved while they ran.
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

// syncTries returns the path of each sync run of rows but the one at path whose result tried a merge, with origin's
// commit it tried, in the order of the runs.
func syncTries(rows Rows, path string) ([]string, []string) {
	var paths, origins []string
	for _, run := range rows.Runs {
		if run.Kind != kindSync || run.Path == path || run.Result == "" {
			continue
		}
		result, err := ParseSyncResult(run.Result)
		if err != nil || !result.Tried() {
			continue
		}
		paths = append(paths, run.Path)
		origins = append(origins, result.Origin)
	}
	return paths, origins
}

// syncUnmerged ends a sync run whose prepare made no merge, and reports whether it did. A repository with no remote
// origin, an error, origin's commit already in the stored base and a commit an earlier sync tried end it skipped. A
// merge that conflicts ends it failed, naming the paths.
func syncUnmerged(result SyncResult) (Action, bool) {
	switch {
	case result.NoOrigin:
		return syncEnd(outcomeSkipped, "the repository has no remote origin"), true
	case result.Error != "":
		return syncEnd(outcomeSkipped, "the sync did not run: "+result.Error), true
	case result.Contained:
		return syncEnd(outcomeSkipped, fmt.Sprintf(
			"origin's commit %s is in the stored base %s", result.Origin, result.Base,
		)), true
	case result.TriedBy != "":
		return syncEnd(outcomeSkipped, fmt.Sprintf(
			"origin's commit %s was tried by %s", result.Origin, result.TriedBy,
		)), true
	case len(result.Conflicts) > 0:
		return syncEnd(outcomeFailed, fmt.Sprintf(
			"merge of origin's commit %s into the stored base %s conflicts in %s",
			result.Origin, result.Base, strings.Join(result.Conflicts, ", "),
		)), true
	case result.Merge == "":
		return syncEnd(outcomeSkipped, "the sync result names no merge"), true
	}
	return Action{}, false
}

// syncChecks decides a sync run at its merge. It walks the Done gates, then the captures and the reproductions, as an
// integrate run does, and returns the action the first one that is not decided asks for. A gate that failed, a capture
// that failed or regressed from green at the stored base, and a check lost at a restart twice or exiting
// store.EnvironmentExit on its last run end the run failed, its output the merge's line and then one line per cause.
// Once every check has passed, the run advances to the merge, its output the line of what it merged and one line per
// Requirement, unless the epic moved while the checks ran: then it ends skipped with why.
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
	if moved := syncMoved(rows, result); moved != "" {
		return syncEnd(outcomeSkipped, result.MergeLine()+"\nnot advanced: "+moved)
	}
	merged := fmt.Sprintf("merged origin's commit %s into the stored base %s as %s", result.Origin, result.Base,
		result.Merge)
	return Action{Kind: ActAdvance, Output: outputOf([]string{merged}, requirementLines(rows, captured))}
}

// syncWait returns act, what a check of the sync that is not decided asks for. A park ends the run failed with the
// park's reason instead, since a sync never parks.
func syncWait(act Action, result SyncResult) Action {
	if act.Kind == ActPark {
		return syncFailed(result, []string{act.Output})
	}
	return act
}

// syncFailed ends a sync run failed with the line of its merge and then causes, one to a line.
func syncFailed(result SyncResult, causes []string) Action {
	return syncEnd(outcomeFailed, outputOf([]string{result.MergeLine()}, causes))
}

// syncEnd ends a sync run with outcome and output.
func syncEnd(outcome, output string) Action {
	return Action{Kind: ActEnd, Outcome: outcome, Output: output}
}

// syncMoved returns why the epic is no longer at the quiet point its sync started at, "" while it is: the stored base
// is not the one the merge was made on, or the epic is busy (Busy). An advance then would leave a task built on the
// stored base the sync started from, or a batch composed on it, behind the branch.
func syncMoved(rows Rows, result SyncResult) string {
	if rows.Epic.StoredBase != result.Base {
		return fmt.Sprintf("the stored base moved from %s to %s", result.Base, rows.Epic.StoredBase)
	}
	return Busy(rows)
}

// Busy returns why the work of the epic of rows is not at rest, "" when it is: the epic has a candidate, a planner turn
// is running, a task is open, a task is accepted and no batch has claimed it, or an integrate or release run runs or is
// parked. A sync of origin's main starts, and advances, only while it is at rest.
func Busy(rows Rows) string {
	epic := rows.Epic
	if epic.Candidate != "" {
		return "the epic has the candidate " + epic.Candidate
	}
	if i := slices.IndexFunc(epic.Turns, func(turn store.EpicTurn) bool { return turn.State == turnRunning }); i >= 0 {
		return fmt.Sprintf("planner turn %d is running", epic.Turns[i].Seq)
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
